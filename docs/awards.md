[The Matty Plugin manual](README.md)

# Airtime and awards

## Airtime and medals

The plugin accumulates how long each person streams and keeps two boards — the current calendar
month and all time — behind the trophy in the top right of the sidebar header, and puts the award
next to their name on their stream card.

**Every streamer is listed, not just the top three.** The hours were always being recorded for
everybody; only the display was cut to three. Fourth place onwards gets a plain number where the
badge goes — the row is listed, the medal is earned — and your own row is picked out so you can find
yourself without reading the list.

**The awards follow people out of the panel.** A crown or a calendar appears next to the name of
anyone in the top three wherever they post, and in their profile popover, not only on their stream
card while they happen to be live. It needs no extra request: the boards already carry a user id per
row, and the panel already has the snapshot. The [Top Snooper](#the-top-snooper)'s binoculars go
the same way, after the airtime medals, and the [Best Clipper](panel.md#best-clipper)'s clapperboard after
them.

**The awards never touch anybody's custom status.** 1.16 put a medal there, because the custom
status emoji is the only thing that reaches Mattermost's members list — that list renders
`CustomStatusEmoji` and contains no plugin slot at all, and nor does the direct message list, so a
plugin has exactly one emoji per person and no other way in. It was removed again in 1.17. A
broadcast lasts an evening and the live badge borrows the slot for it; a medal can last months, and
holding somebody's own status for a season is not a thing a television plugin should do. The
members list simply does not show awards.

So the custom status is the live badge and nothing else. It is borrowed while somebody is
broadcasting and handed straight back, with what they were wearing before restored exactly. Set your
own status while the plugin is holding the slot and it stops touching it for the rest of the
broadcast — you have taken it back, and it does not fight you for it.

In a **post**, where a plugin does have a slot, the crown and calendar artwork is drawn properly
rather than as an emoji. They sit **immediately to the right of the live badge**, before the
timestamp, and that is as far left as a plugin can reach. Mattermost's post header is two columns — `col__name`, holding the
username, the tags and the custom-status emoji that the live badge uses, and `badges-wrapper`,
holding the timestamp. The only plugin slot in the entire header is inside the second one, *after*
the timestamp; `col__name` has no extension point at all. So a CSS `order` pulls the awards in front
of the timestamp, which puts them next to the live badge, but nothing a plugin renders can be placed
to its left.

Mattermost also suppresses the slot on consecutive posts from the same person — the ones where it
hides the repeated timestamp — so a run of messages carries the award once at the top rather than on
every line.

**The boards live behind the trophy** in the top right of the sidebar header, on the same row as the
panel's title, and open as a card over the whole window — the **Streamers** tab of the
[Trophies card](#the-trophies-card), beside the [Top Snoopers](#top-snoopers). They were a
disclosure inside the panel until 1.17: with nine streamers the two boards measured 554px, 55% of
everything the panel scrolls through, sitting above the thing people actually opened the panel for.
Out of the panel entirely is better than collapsed inside it, and the boards are the one thing here
that is genuinely easier to read wide than in a 400px column — side by side, with a bar per row
measured against the leader rather than against the total, because against the total eight
streamers give eight slivers.

That position is not a trick. Mattermost drops a plugin's RHS title node straight into
`.sidebar--right__title`, which is a flex container with `flex: 1 1 auto`, so a node that grows to
fill it and spaces its own children apart puts the trophy at the right-hand end of the header with
no absolute positioning at all. The card itself is a **portal onto `document.body`**, because both
that span and the header around it are `overflow: hidden` and anything opened in place would be
clipped to a strip.

The two boards have **different artwork rather than different sizes of the same medal**: a crown
for all time, a calendar for the month, each in gold, silver or bronze and carrying the place. So
which board an award came from is readable at a glance instead of from a tooltip, and neither has
to be dimmed to be told apart from the other. They are inlined as SVG in
`webapp/src/components/badges.tsx` — no request, crisp at any size, and no external fetch. The
original files are in `artwork/badges/` — deliberately outside `assets/`, which is copied into the
installable bundle, because the plugin never loads them at runtime. The PNGs there are the right
thing to upload if you ever want them as Mattermost custom emoji too.

**Under the boards, the months already won.** A third section, **Monthly medals**, lists everybody
with a place on the podium of a month that has ended — the record described under
[Medals on a profile](#medals-on-a-profile), for everyone at once, so a past champion no longer
has to be found by opening the right person's profile. Its line says how far back that goes, "Podium
finishes in months that have ended · 8 months", counting each month once however many people
placed in it.

- **One row per person**, most wins first, then the most second places, then thirds, then by name —
  the way a medal table is read. Your own row is picked out the same way as on the boards.
- **Three columns**, gold, silver and bronze, each the month calendar with a count: **×5**, **×2**. A
  place somebody never took keeps its column as a dash, so the counts line up down the list. Wins
  are in bold, in the card's own words: the card follows the Mattermost theme, and a gold figure on
  a light theme could not be read. The gold medal beside the count already says first place.
- **Every month is in the tooltip**, newest first — "Champion in 2 months: Aug 2026, Jul 2026" —
  and that is what a screen reader hears for each count. Nothing is cut to "+2 more" here. Second
  and third places are worded exactly as on the profile ("Runner-up in 1 month: Jun 2026"); a
  profile puts wins in its title instead, "Monthly champion, 2 months: …", with the same months.
- It runs **across the whole card**, under both boards: a tally is one short line per person, which
  reads as a table and not as a third narrow column. A board with no other beside it spans the card
  too, as it always has. On a phone the name gives way to an ellipsis first; the counts never wrap.
- The names come with the tally, looked up on every poll by the same resolver as the boards, so the
  two never call somebody different things and a rename shows on the next poll. Still no stream key,
  and still nothing specific to whoever is looking.
- The trophy is there whenever there is something behind it, and the medals count on their own: an
  import of past months without the all-time totals leaves both boards empty and the medals not.

**Always-on channels never earn airtime.** An unattended 24/7 channel would win every board by
simply never stopping, so any key listed in **24/7 stream keys** is excluded from counting
entirely. Paused streams are not counted either.

**Twitch time counts too**, while an admin has **Show people live on Twitch** on. The boards, the
medals on the cards and the past months' medals all rank **people**: a person's row is their
Broadcast Box time and their Twitch time added together, and pointing at the hours says how much of
it was on Twitch ("incl. 2h 10m on Twitch"). A second live on both at once counts once, and it is a
Broadcast Box second, so it stays when the Twitch hours go. Past months are merged the same way
before the top three is cut, so somebody third on Broadcast Box and fourth on Twitch can win a month.
Time on Twitch from before somebody connected never counts, and a channel an admin marked 24/7
under Twitch connections earns nothing. While the switch is off, Twitch hours are kept but left off
every board and medal. See [Twitch (optional)](up-next.md#twitch-optional).

How the hours are measured matters, because `/api/status` only ever describes the present moment:

- Time is credited as a **delta against the session's own `streamStart`**, not by adding the poll
  interval. Each session records how many of its seconds have already been counted, so nothing is
  counted twice.
- Because `streamStart` comes from the origin and is authoritative, **a session that ran while the
  plugin was restarting still gets its time** when the plugin comes back. Adding poll intervals
  would have silently lost it.
- Time lands in the month it was actually **on air**, not the month the poll that noticed it ran in.
  A broadcast running across midnight on the 1st is divided at the boundary, and so is a stretch
  recovered after the plugin was down across it — which used to be handed to the later month whole.
- **Months run on UTC**, not the server's local time. Which month a broadcast counts toward should
  not depend on where the host is configured, and moving the server — or a timezone database update
  — must not silently reshuffle history. `monthBucket` and `monthLabel` normalise internally, so a
  caller cannot reintroduce a local-time boundary.
- A `streamStart` in the future credits nothing, and an implausible one is **refused rather than
  clamped**: a delta of more than 30 hours in a single poll means the timestamp is wrong, and
  writing thirty hours of invented airtime that nothing can ever take back is worse than writing
  none. The session resynchronises and carries on counting normally.
- **Pausing, or a reconnect, does not restart the clock.** A session's mark is kept for half an
  hour after the stream stops appearing, so an OBS reconnect resumes the same session instead of
  being credited its whole length a second time. Paused and always-on streams keep their mark
  current without earning anything, so the minutes they were not counted for are not handed over
  in a lump when they come back.
- **A session can only claim time the plugin can be shown not to have been watching.** A mark that
  goes missing while the plugin is up — an unlisted stream that reappears, a mark dropped by the
  retention sweep — used to be indistinguishable from a mark that was never taken, so the broadcast
  was paid for all over again. A new mark now reaches back only as far as the gap since any session
  was last seen, which is at most one polling interval when the plugin has been running and exactly
  the length of the outage when it has not. The recovery after a restart is unaffected; that is the
  case this measures.
- Turning the leaderboard off and on again does not backfill the gap, and neither does taking a key
  out of **24/7 stream keys**. Each running session is brought up to date once, tracked on the
  session itself rather than as a single flag — a flag is used up by whichever poll happens to run
  next, even one that listed none of the streams it was meant to apply to.
- **Starting up is not a settings change.** Mattermost loads a plugin's configuration once before
  activating it, with nothing to compare against, and reading that as "the leaderboard was just
  switched on" would discard the airtime accrued while the plugin was down — on every upgrade,
  which is the one moment this is all supposed to protect.

Totals live in three KV documents rather than a row per streamer, so a poll costs a fixed number of
reads and writes no matter how many people stream. Leaderboard rows never carry a stream key.

### Airtime is the one thing here that cannot be rebuilt

Broadcast Box does not remember how long anyone streamed. Once these documents are gone, the hours
are gone, so the plugin is deliberately paranoid about them.

**A normal upgrade does not touch them.** Plugin KV lives in Mattermost's `PluginKeyValueStore`
table, namespaced by plugin id. Neither `RemovePlugin` nor `removePluginLocally`
(`server/channels/app/plugin_install.go`) deletes a row, and an upgrade is just
`removePluginLocally` followed by copying the new bundle — so uploading a new version over an
existing install, and even removing the plugin and adding it back, keep the history. The expiry
sweep only deletes rows with a non-zero `ExpireAt`, which `KVSet` never sets. The remembered
movie, show and game titles (`mediacache_00` to `mediacache_0f`, see
[Movies and games](up-next.md#movies-and-games-optional)) survive removal the same way; they are only a
cache, and the **Remembered titles** card in the settings empties them.

The real hazard is this plugin, and these are the rules that contain it:

- **A document that cannot be read is copied aside and then left alone.** Reading it as "empty" is
  how a format change turns into permanent loss: the next poll would write an empty table over the
  original. The raw bytes go to a `quarantine_<key>` entry in the plugin's KV store — once, so a
  second failure cannot overwrite the first copy — and recording stops until someone looks.
- **A schema marker** (`stats_schema`) records which format is on disk. A build that finds a newer
  version than it understands refuses to write at all, so rolling back does not rewrite the data in
  an older shape.
- **The all-time totals are backed up on every activation**, which is exactly when an upgrade
  lands. The backup is only ever replaced by a *larger* one, so a build that comes up having lost
  hours cannot quietly overwrite the good copy with its own emptier figures.
- If the stored total is ever smaller than that backup, the System Console page says so and offers
  **Restore backup**. A restore merges on the larger figure per streamer, so it can never undo
  airtime earned since the backup was taken, and running it twice does nothing.

**Twitch hours are the exception, by design.** They are kept in a ledger of their own:
`twitchair_totals`, `twitchair_month_YYYY-MM` and `twitchair_sessions`, keyed by Twitch user id, and
`twitchair_paid`, which says which seconds each person has already been paid for, so that none is
paid twice. They are **meant to be deletable**: Twitch's terms say a person's Twitch data
goes when they disconnect or ask, and an admin's **Delete all Twitch data** removes the lot. So they
are not in the export, not in the activation backup and not restored by it, because a file taken off
the server, or a backup that only ever grows, would bring deleted hours back. A minute somebody was
live on Broadcast Box and Twitch at once is a Broadcast Box minute, so it is in these documents, the
export and the backup like any other, and disconnecting never takes it away. A damaged Twitch ledger
is set aside like a damaged airtime document, and stops only the Twitch hours being recorded; the
Twitch connections card says so. A damaged list of who has connected Twitch is set aside the same
way: until an admin uses **Delete all Twitch data**, nobody counts as connected, Broadcast Box hours
go on being recorded in full and the boards show them alone.

**Where the tools live.** In **System Console > Plugins > Matty > Airtime and awards**, under
*Recorded airtime*:
how many hours are stored and across how many streams, when the last backup was taken and by which
build, anything kept aside because it could not be read, and buttons to **Export** and **Restore
backup**. That is admin data and it belongs on the admin page, not in the panel people use to watch
television.

The panel itself shows system admins *one line*, and only when something is actually wrong —
recording has stopped, or the stored total is behind the backup — pointing at the System Console.
It exists so the problem is noticed; nobody else sees it, and there is nothing to press.

The same data is reachable directly:

```bash
curl -H "Authorization: Bearer $MM_TOKEN" \
  https://mattermost.example.com/plugins/ceo.villa.matty/api/v1/admin/airtime > matty-airtime.json
```

If any monthly table could not be read, the export names it under `incomplete` rather than quietly
returning a short file with a 200.

`POST` the same shape back to restore it. `{"mode": "merge"}` (the default) keeps the larger figure
per streamer; `{"mode": "replace"}` overwrites outright; `{"mode": "restore"}` with no body merges
the plugin's own backup back in. Both endpoints require `manage_system`. Worth taking an export
before anything that moves the database or changes the plugin id. **A changed plugin id leaves
every row behind** under the old one, because the id is what namespaces them.

Three things are *not* protected, and are worth knowing about rather than discovering:

- In a Mattermost **HA cluster** every node runs its own poller against the same documents, and the
  read-modify-write is not a compare-and-set, so two nodes writing in the same second could drop a
  few seconds of one streamer's credit. Within a single server the writes are serialised, by the
  poller and the admin import sharing one lock. `saveStats` says so in a comment.
- If `stats_sessions` is lost on its own — the totals are fine, the session marks are not — every
  in-flight broadcast looks like it started while the plugin was away, and is credited again from
  its own start. That is the same code path as a genuine restart, and there is no information left
  anywhere to tell the two apart. It is bounded by the 30-hour refusal.
- Airtime that cannot be attributed at all: if the plugin is down when a broadcast *ends*, the
  minutes between its last poll and the moment the stream stopped are not recorded anywhere, by
  anyone. Nothing can recover them.

### The profile popover

Everything the plugin adds to somebody's profile is one kind of thing — a fact about that person —
so it is one kind of row: a mark on the left, a headline, and a detail line under it. Being **on
air** comes first, then **medals**, then **what they have planned**. What somebody is doing right
now outranks what they did in March.

They share one set of measurements rather than three copies of the same padding, because three
copies is how rows that are supposed to line up stop lining up. Only the tint and the mark change:
a medal's own colour, LIVE's red with a pulsing dot for live, the theme's accent for On Mumble, and
the plain tint for a plan. A
plan shows its **movie or game** mark when the streamer said which, and a calendar when they did
not, so the headlines line up either way.

The Watch button keeps its pill edges, because it is the one thing in the popover you can press.

**Steam and Hytale**, from 3.4.0, come after On Mumble and before the medals, and only for a reader
who can open the panel: a locked reader's popover has neither, and their browser never asks. In a
game, Steam's row is the game, with "On Steam · {their Steam name}" under it; otherwise Online, Busy
or Away, or, faded, just their Steam name while Steam says offline (a private profile is offline
here too). Its pill is a small arrow, named "{name}’s Steam profile", that opens their Steam profile,
because a worded pill would leave the game's name no room in Mattermost's narrow popover. Hytale's row
is their Hytale username, with **Copy**, which reads **Copied** for two seconds without moving; where
the browser will not let the page copy, it selects the name for you to copy. The two pills' words
are in the theme's accent, like Watch beside them.

**A plan linked to a movie, show or game** (see [Movies and games](up-next.md#movies-and-games-optional))
shows it here as it does in Up next: the title's **cover** where the category mark goes — a poster
or box art, or Steam's wide capsule — its art faded in behind the row, and a line naming it ("Arma 3
· 2013 · Game") under the plan's own words. The facts and a few lines about it are in the row's
tooltip. There is no link out from here, to keep the popover a glance at a person; the title's
IMDb, Steam or IGDB page is in its profile in Up next. While somebody is live, the **Live now**
row wears the art of the plan that is on now, with the live dot on the cover's corner. The art is
relayed by the server like the rest of it, so the popover never makes a request of its own to
any of them; if lookups have since been switched off, the category mark comes back.

### Medals on a profile

Somebody's awards appear in their profile popover, under their name: the artwork, what the place is
called — **Champion**, **Runner-up**, **Third place** — and which board it came from, with the hours
behind it. The [Top Snooper](#the-top-snooper) has a row of its own there too, in first place's
gold, under the airtime boards and above the months already won, and the
[Best Clipper](panel.md#best-clipper) one after it.

The badge on a post stays the artwork alone, because a post header has room for an icon and not for
a sentence. A profile has room, so it says it. The medal by itself was a rebus: a crown with a 2 in
it means "second for airtime, all time" only to somebody who already knows, and the tooltip that
said so needed a mouse and a guess that there was anything to hover.

No ordinal is printed beside the word. The artwork already carries the digit and "Runner-up" already
means second; a "2nd" there was the same fact for the third time.

The month is named rather than called "this month" — a profile is read weeks after the fact as often
as on the day. None of it sets a text colour: it is rendered inside Mattermost's own popover, so it
inherits, and the tint behind each row is the medal's own colour at low alpha, which reads on the
light and dark themes alike. The one exception is the count described next, which is the medal in
miniature — its gold, with its dark ink for the digits — a pair that carries its own contrast, so it
reads the same on either.

**Months already won stay on the profile.** The month board starts again on the 1st, and a
champion's calendar used to go with it. Every month that has *ended* is now remembered, as one more
row at the end, under the boards as they stand: **Monthly champion**, with a gold **×2**, **×3**
beside it once there is more than one, and the months themselves underneath, newest first — "Aug
2026, Jul 2026". Past three it is the three newest and "+2 more", and the tooltip has the rest.
Second and third places sit at the end of that line as a small calendar and a count each, with their
months in the tooltip. Somebody who has placed but never won gets the same row, titled **Monthly
podium**. A screen reader hears it as a sentence — "Monthly champion, 2 months: Aug 2026, Jul 2026"
— with every month and no "+2 more".

- Only months that have **ended** count, on UTC like the boards. The month in progress is already
  the **Champion · September** row above it; counting it here as well would hand somebody a win on
  the day they first go on air and take it back the moment they are overtaken.
- A month's podium is its board exactly as the board ordered it — hours, then name, then key — so a
  tie comes out the way it did on the board. 24/7 channels never place, as ever.
- A past month goes to whoever its stream key is linked to **now**, so linking a key later brings
  its history with it. A key nobody has linked earns nobody a row, and two keys linked to the same
  person count as that one person.
- It is on the **profile**, and everybody's is under the boards in the **trophy menu** (see
  [Airtime and medals](#airtime-and-medals)). The badges beside somebody's name in a post stay what
  they hold this month and all time: a post header has room for an icon per board, and a record
  belongs where there is room to read it.
- All time has no equivalent. It never ends, so there is nothing yet to have won.
- It needs **Track streamed hours and award medals**, like the boards. Reading every past month
  means a read for each month since 2020, which is not something to do every few seconds, so the server
  works it out when it starts, again when the month rolls over, and whenever the history is
  imported or restored, and keeps the answer in memory. A month that cannot be read at that moment
  is tried again a few minutes later rather than counted as nothing; one that is damaged beyond
  reading counts nothing, and is logged once.

### Headphones for whoever is on Mumble

Somebody in voice gets **headphones** in front of their medals — on their posts, on their Live Now
card, and as an **On Mumble** row in their profile, under Live now and above the awards. It is a
disc in the theme's accent with the medals' dark rim — but not their cream halo, which read as a
white outline round a small round badge — so being in voice never borrows LIVE's red, and it turns
slate, struck through, while they are muted, deafened or not allowed to speak in their channel. First in the row for the same reason the live row heads the
profile: what somebody is doing now outranks what they did in March.

It is read from the voice roster every panel already has, so nobody sees anything they could not
already see in the voice row. Only people the plugin has matched to a Mattermost account can carry
it, and nobody does while the plugin is not connected to Mumble, because not connected is not the
same as nobody being in there. Somebody on Mumble twice — phone and desktop — reads as muted only
when both are.

**A match is only a name.** With *Match names to usernames* on (the default), the
Mumble user called `alice` is taken to be Alice — which used to matter only for marking your own row
in the panel, and now puts headphones on Alice's posts and profile. If your Mumble server lets
anybody pick any name, somebody connecting as `alice` lights Alice's headphones up. It lets nobody act
as anybody else in Mattermost; beside a name it is a display, and with the soundboard on it also
decides into which channel Alice's own sounds play (see
[What a Mumble name proves](voice.md#what-a-mumble-name-proves)). If that matters on your server, turn the
match off and list the people you trust under *Names that differ*, or register names on the Mumble
server so they cannot be taken. Changes to either setting show the moment they are saved, not the
next time somebody joins or leaves.

Only Mumble names made of plain letters, digits, dots, dashes and underscores are matched to a
username, so a lookalike such as one with a Turkish dotted I (`alİce`) matches nobody. Mattermost
usernames are made of those characters only, so no real match is lost, and Mumble itself would have
let the lookalike in beside the real name. **Names that differ** compares the Mumble name you wrote
exactly, apart from the case of plain letters.

## Top Snoopers

Beside the streamers' boards, the trophy's card has a **Snoopers** tab: who has spent the most time
watching other people's streams in the panel, this month and all time, with the same bars against
the leader and your own row picked out. Every place is a plain number but one: first place all time
wears the [Top Snooper](#the-top-snooper)'s binoculars, the one award watching wins.

**What counts** is time by the clock, per person. Four tiles at once, or the same stream in two
tabs, is one hour an hour, not four. Time is counted from when you press play until the last
refresh that still sees you watching, in whole seconds, and lands in the UTC month it was watched
in, split at midnight on the 1st exactly as airtime is. That includes the short time the panel keeps
your streams connected out of sight while a thread or a search has its place in the side panel; see
[Notes and limitations](security.md#notes-and-limitations).

**What does not count:**

- **Anybody who has not linked their stream.** Only people who have linked their account with their
  bearer token are counted, and only while they are linked: time watched before linking, or after
  unlinking, is not. People an admin has let in to watch are not counted either. Hours already
  counted stay stored when somebody unlinks, but they are not on either board while that person is
  unlinked; linking again puts them back in their place.
- **Your own stream.** Otherwise going live would be a way up the board.
- **24/7 channels.** Anybody could leave one open overnight. They count only toward a 24/7 channel
  achievement, never the board.
- **A paused stream**, which has nothing on it to watch, just as it earns its streamer nothing.
- **The panel's hidden look-ins for preview pictures, and the Clips recorder.** Neither is ever
  recorded as anybody's watching: only a connection the panel opened for a signed-in person to watch
  can count, and a look-in is marked as one, while the recorder is the server's own connection,
  which the panel never opened at all.
- **Watching some other way** — Broadcast Box's own page, or a player of your own — because the
  plugin cannot tell whose connection that is.
- **Watching a Twitch stream**, in the panel or anywhere else: it plays in Twitch's player, and
  the plugin cannot see who is watching there.

**Privacy.** Nobody else's watching is recorded for the board at all. With **Show who is watching**
on, the plugin knows whose connection is whose anyway, for the faces, and keeps that in memory only;
with it off, it remembers that only for people with a linked stream, and lets go of anybody else it
was remembering at the first refresh that sees them, or a minute after they stop. (Clips keeps a
record of its own, in memory only, of who has a playback session on which stream, whatever these
settings say, for deciding who may clip and nothing else; see [Who is watching](panel.md#who-is-watching).)
For the Snoopers board, what is stored is a number of seconds per Mattermost user id, per month and
in total (`watch_totals` and `watch_month_YYYY-MM` in the plugin's KV store): not which streams, not
when, and never a stream key. With **Name each streamer's biggest fan** on, the plugin also keeps, all
time, how many seconds each linked person has watched each linked streamer (`fans_totals`), which is
who watched whom; and for each 24/7 channel with an achievement, how many seconds each linked person
has watched it (`chanwatch_totals`). Neither says when. Fan hours are counted only while **Show who is
watching** is on, and since 3.2.1 nobody can opt out of them on their own; an admin can switch biggest
fans off for everybody. The board itself names people the way the airtime boards do, and
is the same for everybody who opens it. With **Show who is watching** off, its figures move once an
hour, on the hour, rather than at every refresh: otherwise two refreshes side by side would show
whose seconds went up, which is who is watching right now — and with one stream on the air, what.
What is left is what any board of watching shows: somebody whose figure went up between two hours
watched something in that hour. Names, and who is linked, are still read at every refresh.

**How it is measured.** Every refresh pays everybody watching the time since the refresh before,
from when they pressed play if that was later. Who is watching is only known from the connections
this run of the plugin set up, so a restart pays nothing twice and nothing for connections it did
not see open; people already watching are counted again from when they next start a stream. A
stretch of refreshes that could not reach Broadcast Box pays no more than the panel would itself
have gone on showing their stream through it (four refresh intervals and a slow answer), however
long the outage. Switching the board on pays nothing for the time it was off. The tables follow the
airtime rules: one that cannot be read is never treated as empty, and a refresh that could not read
or save them is paid on the next one. A damaged one is copied to a `quarantine_` key and left
alone, and recording of watched hours stops, logged once — airtime carries on — until the plugin
next starts and finds it readable: the build that wrote it put back, or the damaged entry removed
from `PluginKeyValueStore` by hand, its bytes safe in the copy.

**The setting** is **Rank the Top Snoopers**, under **Airtime and awards**, on by default. Off, nothing is
recorded and the Snoopers tab goes; the hours already counted are kept for when it comes back on.

### The Top Snooper

Whoever is first on the **all-time** Snoopers board is the **Top Snooper**, and wears **gold
binoculars**. One person holds it: the board ranks strictly, one place each, and second place wins
nothing, nor does anybody for the month. The binoculars are drawn the way the airtime crown is — the
same gold, the same cream halo and dark outline, so the two sit together as a set beside a name, on
black or on white — but with no digit in them, because there is nobody to tell them apart from.

They go everywhere the airtime awards go:

- **Beside the name in a post**, after the crown and the calendar, in front of the timestamp.
- **On their Live now card**, after the medals, while they are live.
- **In their profile popover**, as a row of their own under the airtime boards and above the months
  already won: **Top Snooper**, "All time · 114h watching others", in first place's gold.
- **On the Snoopers tab**, in place of the 1 on the all-time board's first row, the way the crowns
  sit on the airtime boards.

Pointed at, and to a screen reader, they are "Top Snooper, all time". On the board, where the
heading already says all time, pointing at them shows "Top Snooper" and a screen reader hears "1st,
Top Snooper". Like the medals, they are read from the board the panel already has, with no request
and no field of their own, so they change hands when the board does — and with **Rank the Top
Snoopers** off there is no board, and nobody wears them. The artwork is
`artwork/badges/snoop_alltime_1.svg`, beside the crowns.

### The Trophies card

The trophy opens one card with a tab per board: **Streamers** (the month, all time and the monthly
medals, exactly as before), **Snoopers** (with [Biggest fans](#biggest-fans) under the boards) and
**Clippers**, and from 3.2.0 **Achievements** (see [Achievements](#achievements)) and, for somebody
linked, **You** (see [Awards by your name](#awards-by-your-name)). With Top Snoopers and Clips both off
there is only one tab, so there is no row of tabs either, and the card is what it always was. The
trophy itself is there whenever any tab has somebody on it (for anybody who can open the panel: a
locked header is the title alone); a Snoopers tab that is on with nobody counted yet says "Nobody
has snooped yet.", a Clippers tab with no holder says nobody holds it yet, and neither puts the
trophy up on its own.

- The tabs are proper tabs to a screen reader and a keyboard: **←** and **→** move along the row
  and choose as they go, **Home** and **End** go to either end, only the chosen tab is in the Tab
  order, and Tab from it goes into the board.
- The tab you chose last is remembered in this browser, and the card opens on it next time. Without
  a remembered choice it opens on Streamers — or on Snoopers, when only they have anybody on them,
  or on Clippers, when only it has.
- With tabs, the card hangs from near the top of the window rather than being centred, so switching
  between tabs of different heights does not move the tabs out from under the pointer.
- The third tab, **Clippers**, is the [Best Clipper](panel.md#best-clipper): the one holder, their score and
  their best clip to watch. It is there whenever Clips is on, or somebody holds
  it.
- **The row scrolls on a narrow window.** Five tabs need about 420 px, so on a phone-sized window
  the row scrolls sideways, without a scrollbar, with a soft fade at whichever end has more tabs past
  it, and the chosen tab is brought into view when the card opens and whenever the choice moves.
- For somebody linked, the trophy is also there whenever they hold an award or an achievement, so the
  You tab can be reached. (Until 3.2.1 biggest fans being counted was reason enough too, so the
  opt-out's switch could always be reached; there is no opt-out now.)

## Awards by your name

Everybody's awards go beside their name in posts, on their Live now card and beside "Clipped by" on
their clips. Which of them is up to each person, in the Trophies card's **You** tab, which is there
for anybody who has linked their stream.

- **Automatic**, which is what everybody has until they choose, is exactly what the panel always drew:
  the crown (all time) and calendar (this month) for a place on the airtime boards, the Top Snooper's
  binoculars and the Best Clipper's clapperboard, whenever held, in that order. **Nothing new ever
  appears beside a name by itself**: a biggest fan's heart, an achievement's coin and a past month's
  medal show there only when their owner chooses them. Awards are meant to be rare beside a name.
- **My choice** is up to **four** of everything you hold, in your order: the four above, your past
  months' medals ("Monthly champion", or "Monthly podium", drawn as the month's calendar with a star
  where the place was, so it is not taken for this month's), each streamer you are the biggest fan of,
  and each achievement family at the step you have reached. Press a chip to add it to the end of the
  line or take it out; the arrows beside a chosen chip, or Alt with ← and →, move it. Four is the most
  the panel ever drew, so no post header gets any wider. An empty choice is a choice too: nothing beside
  your name but the headphones.
- **Something you chose and do not hold now is skipped and kept**: win the month again and its
  calendar comes back where you put it. Something won since you chose waits until you add it, and
  carries a small **NEW**, in the theme's accent, in the tab until you next open it (remembered in
  this browser).
- At the top of the tab a line drawn like a post header shows your name and the badges your choice
  would draw, as you change it. It is saved by itself a moment after the last change ("Saved.
  Everybody sees it within a few seconds.").
- **The headphones are not an award.** They say where somebody is now, not what they won, so they
  always come first while you are on Mumble, outside the choice and outside the four.
- **Your profile and the Trophies card keep the whole record**, whatever you choose to show beside
  your name.

Going back to Automatic is open to everybody, linked or not, so somebody who has unlinked can still
clear what they chose. What a choice comes to rides the list of streams every signed-in browser has:
the awards drawn beside each name, which are public. From 3.6.0 the choice itself leaves the server
only to the person who made it, and never when they made it; the server applies it, where until then
every browser did. They are kept in the plugin's store as one document, `award_prefs`; if it ever cannot
be read, everybody's badges show Automatic, the tab says so, and the next choice starts a fresh one
(the unreadable copy is kept aside as `quarantine_award_prefs`).

## Biggest fans

Each linked streamer's **biggest fan** is the linked person who has spent the most time watching
their streams in the panel, all time, once it is at least an hour: the streamer's most devoted
snooper.

- **Who counts** is exactly who the [Snoopers board](#top-snoopers) counts: people with a linked
  stream, while linked, never on their own stream, never a paused one, never a 24/7 channel (which
  has nobody to be a fan of), never the panel's look-ins or the recorder, and never Twitch.
- **Only while the cards show who is watching.** Counting happens only while **Show who is
  watching** is on, when every Live now card already shows whose faces are on which stream. With it
  off, nothing is counted and the figures stand still. It also needs **Rank the Top Snoopers** and
  **Name each streamer's biggest fan**, both on by default.
- **Two streams at once count for both.** An hour with Dave and Bob both open is an hour as Dave's
  fan and an hour as Bob's, though only one on the Snoopers board, so someone's fan hours can add up
  to more than their snooping.
- **A tie keeps the holder**, as for the Best Clipper.
- **Counted from the start.** Nothing records who watched whom before the plugin does, so on a new
  server everybody starts at zero the day it is installed.

Where it shows: in the streamer's profile popover (**Biggest fan**, with the fan's face and name and
their hours); in the fan's, as an award (**Biggest fan of Dave**, "41 hours watching"); under the
boards on the Snoopers tab, **Biggest fans**, a line per streamer ("Dave · biggest fan · Bob · 41 h"),
yours picked out; as a quiet line on the streamer's Live now card, with the fan's face, "Biggest fan
· Bob", or "Fan · Bob" at the end of the line of links on a card with less room, where it gives way
rather than cut a title, a name or a link; and as a gold **heart** beside the fan's name, if they
choose it in the You tab. Nobody sees anybody's second place, or any other pair.

**No opting out.** Since 3.2.1 nobody can choose not to be counted. 3.2.0 had a switch in the You tab and a line in your own profile popover; both are
gone, and so is the list they kept, which 3.2.1 deleted at its first start. Whoever had opted out is
counted again from then, while the faces are on, and what was deleted for them stays deleted. The
only way to stop it is the admin's: **Name each streamer's biggest fan** off, or **Show who is
watching** off, which stops the counting for everybody. A streamer cannot hide their own biggest fan.

What is kept, and where, is under Top Snoopers' **Privacy**. **The hours themselves, if they ever
cannot be read** (or after a downgrade to a build that cannot read them), are **deleted and started
again from zero**, not kept aside: they are who watched whom, and a copy nobody can read is a copy
nobody can ever delete from. The server log says so, and system admins see "The biggest-fan hours
couldn’t be read on {date}, so they were started again from zero." The boards and airtime carry on
regardless.

## Achievements

Coins for milestones, in five families. They are **worked out at every refresh** from what the plugin
already keeps, not stored, so the first refresh after 3.2.0 gave everybody every step their history
reaches.

| Family (its head on the Achievements tab) | A step reads | Steps | Counted from | Needs |
| --- | --- | --- | --- | --- |
| Streamed (ON AIR, a mast) | "Streamed 100 hours" | 10, 50, 100, 250, 500, 1000 hours | All-time airtime by person, as the boards count it, Twitch hours included while Twitch live is on | **Track streamed hours and award medals** |
| Snooped (SNOOPER, an eye) | "Snooped 50 hours" | 10, 50, 100, 250, 500 hours | The all-time Snoopers figure, as the board shows it | **Rank the Top Snoopers** |
| Podium (PODIUM, a podium with a star) | "On the podium 3 months" | 1, 3, 6, 12 months | Months that have ended with you first, second or third | **Track streamed hours and award medals** |
| Thumbs up (CLIPPER, a thumb) | "50 thumbs up" | 10, 50, 100, 250 | The thumbs up that count toward [Best Clipper](panel.md#best-clipper), those kept from clips that have gone included | **Let people clip streams** |
| A 24/7 channel (its name, a moon) | "Lo-fi radio: 10 hours" | an admin's | Hours spent watching that channel in the panel | **Rank the Top Snoopers** and a line in **24/7 channel achievements** |

- **The coin says the step**: bronze, silver and gold for the first three, then an ink coin with a rim
  in the theme's accent, then ink with a gold rim, and past five a gold stud on the rim for each step
  more.
- **They can go down** when the figure behind them does: Twitch hours taken away by a disconnect or by
  switching Twitch live off, a relink to a new stream key (whose hours stay with the old one, as on the
  boards), an airtime import or restore, a Best Clipper reset, or unlinking, which takes somebody off
  every board. That is the price of keeping nothing derived from hours that can be deleted.
- **Only people linked now** have them, as on every board. A family whose switch is off is gone while
  it is off, and back as it was when it is on; **Give achievements** switches them all.
- **24/7 channels** are an admin's to name, in **24/7 channel achievements**, one per line:
  `radio = Lo-fi radio = 1, 10, 50, 100, 250` (the key, which must be one of the **24/7 stream
  keys**, the name the coins show, and the hours of each step), up to 8 lines of up to 8 steps. A
  fresh server has none.
  A line that cannot be used is left out and system admins are told which and why. Hours on those
  channels are counted only while the channel has its line (nothing records 24/7 watching before
  that), for linked people watching in the panel, while **Rank the Top Snoopers** and **Give achievements** are on, and they never count toward
  the Snoopers board or a biggest fan. There is no daily cap: a tile left open counts. The key never
  leaves the server; the family's id is a hash that says nothing about it. If those hours ever cannot
  be read they start again from zero, as biggest fans' do.

Where they show: in everybody's profile popover, as one row, **Achievements**, with each family's
coin and its step ("Streamed 100 hours · Snooped 50 hours · Lo-fi radio: 10 hours"); on the Trophies
card's **Achievements** tab, a family at a time, each with its steps and the people at each one,
highest first, yours picked out ("Nobody has an achievement yet. The first comes at 10 hours
streamed or watched." while that is so); in the **You** tab, **Your achievements**, each family with a
thin bar to your next step ("62 of 100 hours", or "All done."); and beside your name only if you
choose it.

**When you reach a step**, your own unlocked panel says so, quietly, for six seconds over the bar at
its bottom: **New achievement**, "Streamed 100 hours". Nobody else is told, nothing is posted, and nothing is
sent. Each browser remembers which steps it has announced for you, and the first time it sees your
achievements it remembers them without a word, so the backdated ones never arrive as a burst. A
browser that cannot remember shows none, never the same one twice.
