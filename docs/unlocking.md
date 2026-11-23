[The Matty Plugin manual](README.md)

# Unlocking and streaming

## Unlocking Matty

The panel is locked for everybody, system admins included, until they unlock it with their own
**bearer token**. That is the publish token their streaming software uses; OBS calls it Bearer Token.
Broadcast Box matches the token to its stream, which is how the plugin knows the stream is theirs. So
unlocking *is* linking your stream, and there is nothing else to set up.

The panel's sentences are quoted in this chapter as they read on a server that keeps the default
name, Matty ("Matty is locked."). A server that has given the plugin a name of its own (see
[Name and artwork](install.md#name-and-artwork)) reads that name in their place.

A system admin can also **let people in** without a token, in System Console > Plugins > Matty >
**Access**. They can watch streams and clips, and nothing more.

Outside the panel nothing is hidden but people's Steam status and Hytale names, which only people
who can open the panel see. Live badges, medals, the profile popover and shared clip cards show for
everyone, and their Watch buttons open the lock. The server holds to every part of this,
whatever a browser does.

| | Locked | Let in | Linked |
| --- | --- | --- | --- |
| The panel | The lock screen, and nothing else | Watch only | Everything |
| Watch streams, the small player, Live now and its pictures and faces, Up next, voice, the Trophies card | No | Yes | Yes |
| Clips: the list, watching, shared cards with their pictures | No | Yes | Yes |
| Clips: the ✂, drafts, publishing, upvoting, reporting, sharing, deleting | No | No | Yes |
| Planning, changing or joining a plan; What's on?; the stream title; clips of your stream | No | No | Yes |
| Cancelling your own plan, leaving one | Not offered (the server still allows it) | Yes | Yes |
| Taking preview pictures for Live now; Pop out | No | No | Yes |
| Counted on [Top Snoopers](awards.md#top-snoopers) | No | No | Yes |
| Connect Twitch, when an admin has switched it on | No | No | Yes |
| Disconnect Twitch, for somebody who has it connected | Yes | Yes | Yes |
| People's Steam status and Hytale names, in the panel and in profile popovers | No | Yes | Yes |
| Connect Steam or Hytale, when an admin has switched it on | No | No | Yes |
| Disconnect Steam or Hytale, for somebody who has it connected | Yes | Yes | Yes |
| Badges, medals, the profile popover, shared clip cards | Shown; Watch opens the lock | As before | As before |
| System Console, for system admins | Yes | Yes | Yes |

A system admin who can open the panel, linked or let in, can still cancel anybody's plan from Up
next. Being matched to a stream only by username (*Match stream keys to usernames*), or (from
3.1.0) a Twitch account or (from 3.4.0) a Steam or Hytale account connected, never unlocks anything.

### The lock screen

The lock **takes the panel's place**, rather than covering it, so nothing of the panel runs behind it.
There is no polling of the stream list from it, and no tiles, pictures or clips. It asks the plugin
who you are when it opens, and makes one more request when you press Unlock. It does not widen the
side panel.

It is the panel itself, locked, with as little on it as will do. In the middle is a padlock in a ring of
the theme's accent. On a server that has uploaded an emblem (see
[Name and artwork](install.md#name-and-artwork)), the emblem is there instead, large and dimmed as if
behind glass, with the padlock on it. At the foot, where the unlocked panel's own bar sits, is the bar
for the token, the same shape and size as Mattermost's message box beside it: the box ("Bearer token
to unlock") on top, and under it a padlock, an eye that shows the token, and **Unlock**, quiet until
there is something in the box. One line over the bar says how it is going, and nothing at all until
there is something to say; its room is kept, so nothing moves when it speaks. A server's banner, when
it has one, stays, at the top, and also at the bottom with **Top and bottom**.

That is all, and it is the same for everybody. The lock does not say that anybody can be let in
without a token, or whom to ask. A system admin's lock is the same as anybody else's: no way to let
themselves in, no admin alerts, no set-up words. Admins find all of that in the System Console, which
works without a token, and see the full alerts in the panel once they unlock.

Under the padlock there is **one line, only when there is a reason**:

- "Unlock Matty to watch Dave." after Watch in a profile popover;
- "Unlock Matty to watch this clip." after a shared card's Watch, or a clip link;
- "You unlinked your stream.", "Your stream was unlinked here, by an admin or because another
  account linked it." or "You are no longer let in.", when the lock has just come back.

**While Matty is not set up**, the lock says "Matty is not set up yet, so it can’t be unlocked."
under the padlock, to everybody, and has no bar, since a token could only fail.

**When Matty does not answer** as the lock opens, the page may never have known who you are, as when
the plugin is restarting. Then there is no padlock, and the line says "Matty is not answering, so it
can’t tell whether you’re unlocked." There is a **Try again**, and the bar stays usable.
Somebody who is linked is never told they are locked just because the plugin was down.

**With an account connected**, Twitch, Steam or Hytale, there is a small Disconnect line above the
status line over the bar (under the not-set-up line when there is no bar). With Twitch alone it is **Disconnect Twitch**, as
in 3.1.0; see [Connecting](up-next.md#connecting). With Steam or Hytale too, it reads "Disconnect Twitch ·
Steam · Hytale", naming only what is connected, or "Disconnect Steam" for Steam alone. Pressing a
name turns the whole line into that service's own words, "Disconnect Steam? Press again", for four
seconds, and a second press disconnects it. The line is never cut short, at any width, and it never
offers Connect.

**Nothing retries by itself.** A stopped plugin answers 404 on every route, and a steady stream of 4xx
from one browser is what the fail2ban jail in front of Mattermost looks for.

**Unlocking** looks like this: the padlocks swing open, the glass over the emblem clears on a server
that has one, the lock fades, and the panel boots with its usual scan line. With less motion asked for, the panel replaces the
lock at once. Either way a screen reader hears "Unlocked.", or "An admin let you in to watch." when that
is what happened.

What the status line over the bar says:

| When | It says |
| --- | --- |
| Unlock with nothing in the bar (nothing is sent) | Paste your bearer token first. OBS calls it Bearer Token, under Settings → Stream. |
| While it is checked | Checking your token… |
| Broadcast Box does not know the token | That token was not accepted. Check it and try again. OBS calls it Bearer Token, under Settings → Stream. |
| Longer than 256 characters (nothing is asked of Broadcast Box) | That is too long to be a bearer token. OBS calls it Bearer Token, under Settings → Stream. |
| Too many tries: five, then one more every three minutes, per person | Too many tries. Wait a few minutes, then try again. |
| Broadcast Box cannot be reached | Broadcast Box is not answering, so your token can’t be checked. Try again later. |
| The token is good, but its stream's name is not one the plugin can use | Your token was accepted, but its stream name can’t be used here. Ask an admin to look at it. |
| The link could not be saved | Your token was accepted but could not be saved. Try again in a moment. |
| No answer at all | Matty is not answering. Try again in a moment. |

**Keys and focus.**

- When you open the panel yourself, the bar takes focus. That means its button, a popover's Watch, a
  card's Watch or a clip link. On a touch screen the bar does not take focus, because the keyboard
  would cover half the screen.
- When the panel opens by itself, the bar never takes focus. That way a message being typed never
  ends up in the token box.
- **Enter** unlocks. **Escape** only clears an error, never the token.
- Any letter, or **Ctrl/Cmd+V**, pressed anywhere on the lock goes into the bar, never to
  Mattermost's message box. That includes a press on Unlock or the eye, and a paste on a Russian,
  Greek, Hebrew or Arabic keyboard layout. Mattermost sends such keys to its message box, where synced
  drafts would save a pasted token on the server before anybody pressed Enter.
- After a Disconnect on that line, focus goes to the words that say it is done, then to the line's
  first name while something is still connected (to **Disconnect Twitch** when Twitch is all that is
  left), and to the lock itself when nothing is, never to the page, so the next letter still lands
  in the bar. The same key in the
  let-in bar leaves focus on its words, then on the panel, and a letter or a paste there goes
  nowhere, whatever the keyboard layout.
- After **Try again** on the No signal face, when the answer comes and the button goes, focus goes to
  the lock itself, so the next letter lands in the bar. The same button on the panel's quiet "could
  not check your access" notice leaves focus on the panel.

### Your bearer token

It is the token your streaming software publishes to Broadcast Box with. In OBS it is under
**Settings → Stream**, with the service set to WHIP, in the field called **Bearer Token**. Copy it from
there. A leading "Bearer " pasted with it is fine, because the plugin takes it off.

Broadcast Box hands one out when an admin reserves a stream for somebody. It is a long random code,
and only its owner has it. The stream's name, which anybody can read off the panel, is not the token.
See [How a person gets a red dot](#how-a-person-gets-a-red-dot) for why.

The plugin sends the token once to Broadcast Box, which says whose stream it is, and keeps it on the
server, in the plugin's store, because the stream title is written to Broadcast Box with it. It is
never sent back to a browser, written to a log, put in an address, or kept in the browser. The bar
masks it, and **Show** (on the lock, the eye, **Show token**) reveals it for 15 seconds. Where the browser can mask an
ordinary text box, the
bar is one rather than a password field. That way the browser's password manager neither offers your
Mattermost password there, nor offers to save the token as one.

### A token pasted in the wrong place

Everybody now has to paste a token at least once, and a paste that misses the bar lands in a channel.
So a message that is **nothing but a bearer token** is refused before it is posted. Broadcast Box's
tokens are UUIDs, so the check is for a message that is only a UUID, with or without one "Bearer " in
front of it. The refusal says "That looks like your bearer token. Paste it into the bar in the Matty
panel instead.". It is checked in two places:

- **In the browser**, while the page is not linked or any bearer token bar is open.
- **On the server**, for posts and for edits that change the words, from every app. For somebody not
  linked, every such message is refused while the plugin is set up. For somebody linked, only the
  token they linked with is. Bots, webhooks and Mattermost's own messages are left alone.
  Mattermost's answer is "Post rejected by plugin." followed by those words.

Its limits:

- It is a belt, not the defence. The key rule above is what keeps a token out of the message box.
- With synced drafts on, which is Mattermost's default, text in the message box is saved on the server
  as a draft before Enter is pressed, and no plugin sees a draft.
- A UUID inside a sentence is not refused.
- A thread whose root is nothing but a UUID, posted by somebody not linked, cannot be moved.

### Let in to watch

A system admin lets somebody in from **System Console > Plugins > Matty > Access**, under **Let in
without a token**. A let-in person can:

- watch streams, in the panel and in the small player;
- see Live now with its pictures and faces, Up next, who is in voice, and the Trophies card;
- see people's Steam status and Hytale names, on the Playing line and in profile popovers;
- list and watch clips, and see shared cards with their pictures;
- search a stream title's hashtags;
- cancel their own plans and leave plans they joined.

They cannot plan, join a plan, say What's on? or set a stream title. They cannot clip, upvote, report,
share or delete clips, take preview pictures, use Pop out, or connect Twitch, Steam or Hytale. Nor are
they counted on Top Snoopers.
The panel leaves those things out rather than offering them and refusing.

At the bottom of their panel, in the bar's place and the same shape, are the quiet words **Watch only**
and a **Link your stream** button. It opens a sheet with a **Bearer token** bar, **Show**, **Link** and **Cancel**. A token there makes them linked,
and the full panel takes the watch-only one's place.

If a stream of theirs is matched to them only by username and is being kept for clipping, a line above
says "Your stream is being kept for clipping. Link it to switch that off."

**The Access card**, which acts at once with no Save:

- **Let in.** Type a username, with or without `@`, and press **Let in** or Enter. The line under the
  box says "@alice can now watch.", and adds "They have linked a stream, so this only matters if they
  unlink." for somebody already linked. Bots and deactivated accounts are refused. What Remove and
  Undo did is said on the same line.
- **The list.** "3 of 500 let in without a token", then one row per person: their picture, name, `@username`, when
  they were let in and by whom, and a tag for **Deactivated** or **Account deleted**. An admin's own row
  says "(you)". Admins may let themselves in. Past eight people the card shows the first eight and
  **Show all** with the count; a row just taken off, just let in, or holding the keyboard focus stays
  shown, wherever it is in the list.
- **Remove.** It takes effect at once and needs no confirmation, because for ten seconds the row stays,
  greyed, with **Undo**. There is no Undo for an account that is deactivated or deleted, which could
  not be let in again.
- **Nobody let in.** The card reads "Nobody is let in. Everybody needs their own bearer token."
- **An unreadable list.** If the stored list cannot be read, it is set aside and reads as nobody, and
  the card and the admins' panel say so. It fails closed: people let in see the lock until they are
  added again, and linked people are unaffected. A list saved by a newer version of the plugin is read as nobody,
  kept as it is, and cannot be changed here. A list the page could not fetch says why, with
  **Refresh**.

The list is kept in the plugin's store, at most 500 people, and is **left out of the settings backup**,
as links are. A backup restored later must never let back in somebody an admin has since let out.

Somebody who is let in and then links stays on the list. If they unlink later, they drop back to
watching rather than to the lock. An admin can take them off at any time.

### Unlinking somebody

Revoking or rotating a token in Broadcast Box **does not unlink anybody here**: the plugin never checks a
stored link again. So the Access card has its own **Unlink somebody’s stream**. Type the username and
press **Unlink**. As everywhere on the page, the pill turns red as **Unlink?**, the line under it
says "Click again to unlink @alice.", and the second press does it.
Under the heading it always says "Their stream stops counting as theirs at once. They can link it again
with their bearer token unless you also revoke it in Broadcast Box, which by itself does not unlink
anybody here."

It does exactly what the person's own Unlink would do:

- the link goes, and so does their What's on?;
- their streamer rights over clips of that stream go, and their votes stop counting;
- their open windows are told at once.

The card then says one of these (the System Console calls the plugin Matty, whatever its name on
the server):

- "Unlinked. Matty is locked for @alice."
- "Unlinked. @alice is still let in to watch.", with a **Remove @alice** pill under it to take them off
  the list too.
- "@alice had no linked stream."

Each let-in, removal and unlink that changes something is written to the server log, with the user
ids of the admin and the person and nothing else.

### Relinking, and unlinking yourself

A linked person's bar always has a small **link key** among its tools, whose tooltip says which
stream they are linked as. It opens **Relink your stream** in a sheet: the same token bar, with
**Save**, and an **Unlink** pill. See [The footer bar](panel.md#the-footer-bar).

While an admin has Twitch, Steam or Hytale switched on, or you have one of them connected, the same
sheet is **Your stream and accounts**: the bar, Save and Unlink first, then **Connected accounts**.
Its key's tooltip then reads "Linked as dave. Your stream and connected accounts.", and
opening it puts focus on the sheet's heading rather than in the token box, which is one Tab away, so
somebody who came to connect Steam is not dropped into the box for their bearer token.

- **An empty bar no longer unlinks.** Save or Enter with nothing in it only says "Paste a bearer token
  to relink."
- **Unlink takes two presses.** The first press changes the pill to "Unlink? Matty locks for you
  until you enter your bearer token again.". For somebody also let in, it reads "Unlink? You will be
  able to watch only, until you enter your bearer token again."
- **Unlinking locks the panel at once**, or leaves it watch only for somebody who is let in. Every
  stream stops in the locked case. The lock then says "You unlinked your stream."
- **Somebody else can take your link.** If another account links the same token, the stream becomes
  theirs, and the lock comes back for you with "Your stream was unlinked here, by an admin or because
  another account linked it."

### When the panel opens by itself

**Open the panel automatically** works as it always has, for everybody, and that includes locked people.
They get the lock screen. Opened that way, its bar never takes focus, so somebody typing a message goes
on typing it in the message box. See [Opening the panel by itself](panel.md#opening-the-panel-by-itself).

Two things locked people do not get:

- **The panel does not come back after a reply** or a closed panel (see
  [When a thread takes the panel](panel.md#when-a-thread-takes-the-panel)). There is nothing of theirs to bring
  back, and the lock would only cover the thread they were using.
- **There is no small player.**

### Outside the panel

Everything outside the panel stays visible to everybody: the live badge, medals and badges beside
names, biggest fans and achievements, the profile popover's live row with its art, and shared clip
and plan cards. What changes for somebody locked is what their buttons do:

- **Watch in a profile popover** opens the lock, which names the stream: "Unlock Matty to watch
  Dave." Nothing is added to the watch list until then. Once unlocked, that stream starts, if it is
  still live.
- **A shared clip card** is drawn from the post alone, with no picture, and nothing is fetched. Its
  Watch and its picture open the lock, and the clip opens once it is unlocked. In a window with no
  panel of its own (a popped-out thread), the card says "Open Matty in the main window to unlock
  it." instead.
- **A clip link** lands on the lock, with the clip waiting behind it.
- **A shared plan's card** is shown in full, as plans are on everybody's profile popover; its Watch
  opens the lock with the stream waiting, and it offers no Join.
- **The header** shows the server's icon and name alone, with no clapper and no trophy.
- **Your own profile popover** offers your takebacks, whatever your access: deleting your sounds.
  (Until 3.2.1 it also offered not being named as a biggest fan.)

**The one exception is Steam status and Hytale names**, from 3.4.0: only people who can open the
panel see them, in the profile popover as inside it. A locked reader's popover has no Steam or
Hytale row, and their browser never asks for them. When they change, every signed-in session, locked
ones included, gets a signal that carries nothing but "something changed", so a locked reader can
tell only that, never who or what. See [Steam (optional)](up-next.md#steam-optional).

What was asked for waits for **ten minutes**, in that page only, and only the latest thing. Opening
the panel from its own button forgets it, since that asks for the panel rather than for that stream.

### When your access changes

A change reaches an open page from whichever of these comes first:

- a WebSocket event the server sends to that one person, which only says "ask again";
- any reconnect of the WebSocket;
- a request the server refused for access;
- the window being looked at again;
- while anything is playing, a check at least once a minute, because a stream already playing asks
  the server nothing.

On the server that made the change, it applies at once. Other servers of a cluster catch up within 20
seconds.

- **Losing access** (an unlink, another account linking the same token, a let-out): every stream stops, the watch list empties,
  the Clips dialog and the editor close, and the lock comes back with its line. A screen reader hears
  "Matty is locked." and why.
- **Linked to let in**: the streams keep playing. The clip editor closes, and the panel comes back
  watch only. A screen reader hears "Unlinked. You can still watch."
- **Let in to linked**: the full panel takes the watch-only one's place ("Linked." for a screen
  reader). **Unlocked in another window, or let in by an admin** while the lock is showing: the lock
  opens as it does for Unlock. Either way, focus moves with the swap only if it was in the panel or
  the lock; somebody typing in the message box keeps typing there.
- **Matty does not answer for a moment**: nothing is taken away. The panel stays, with its streams,
  and says quietly "Matty could not check your access just now." with **Try again**. It asks once
  more five seconds later. Only a definite answer ever locks a panel that is open.

### What the server holds to, and where it stops

The lock is enforced on the server. The panel only follows it. Every route the panel uses has a class,
checked by a gate in front of all of them, before any meter is spent or anything the request carries
is read:

- **Open to anybody signed in:**
  - the stream list and the boards, which the badges and popover need;
  - profile banners, seeing them and setting or removing your own;
  - who you are, and whether you may still watch;
  - the link route itself;
  - the signed release's manifest and signatures (see [Signed releases](security.md#signed-releases));
  - the app check's two requests (see [The app check](security.md#the-app-check));
  - the art for what is on, which the popover shows;
  - taking your own things back: clearing What's on?, cancelling your own plan, leaving one,
    disconnecting Twitch, Steam or Hytale and putting away their notices, putting your awards back
    to Automatic, and deleting your own sounds, one or all;
  - what your own profile popover may offer to take back, answered from memory.
- **Watch** (linked or let in):
  - watching a stream;
  - the 24/7 channels' pictures;
  - the preview pictures, which system admins also get for the System Console, and a Twitch card's
    picture;
  - the five clip routes that only look: the list, a clip, the cards, a clip's video and its picture.
- **Linked**: everything else, connecting Twitch, choosing your awards, sharing a plan, the
  soundboard and downloading a clip included. That includes a route nobody has
  classified, so anything added later is refused until somebody decides otherwise. Connect Twitch's
  window and Twitch's return to it are web pages rather than routes the panel calls, so they come
  before the gate, and each makes the same check itself, after its session checks.
- **System Console routes** skip the gate. Each asks for a system admin first, which is why the
  console works without a token.

A locked caller is answered 403 `locked`, and a let-in caller on a linked route 403 `watch_only`. If
the store cannot be read, the answer is 503 `store`, never a guess either way. The checks the routes
had before (planning, joining, What's on?, clips) all stay behind the gate.

A refusal spends nothing and is not logged. A refused caller is answered from memory for two seconds,
so nobody can make the server read its store with every request. The preview and 24/7 pictures are
`private, no-cache`, so a picture cached in a browser is checked again each time it is shown.

**Where it stops**, on purpose:

- **The stream list still reaches every signed-in browser**, because the badges and popover outside
  the panel use it and it is one broadcast for everybody. It carries who is live, titles, who is
  watching, the schedule, the voice roster and which awards go beside whose name. None of it plays
  anything. (Until 3.6.0 it carried the boards themselves too. They are a request of their own now,
  `GET /api/v1/boards`, which a browser makes as the page starts, again when the list says they have
  changed, and when somebody opens a profile or the Trophies card; it is open to every signed-in reader, as the list is, because a profile shows somebody's awards
  to everybody.)
- **A stream already playing keeps playing.** The plugin only relays the start of a session, and
  nothing can end one at Broadcast Box from outside. A browser that follows the lock stops within
  about a minute. One that does not keeps its stream until the stream ends, which for a 24/7 channel
  is indefinitely. It cannot start another.
- **Revoking a token in Broadcast Box unlinks nobody here**, as above: use **Unlink** as well.
- **Pop out hands out stream keys** to linked people, when **Public watch URL** is set. A key handed
  out keeps working on the public page, and at Broadcast Box, after any unlink or let-out (see
  [Why the pop-out link defaults to off](#why-the-pop-out-link-defaults-to-off)).
- **The lock is the plugin's, not Broadcast Box's.** Anybody who can reach Broadcast Box's own
  `/api/whep` with a stream key could watch around it. [LOCKDOWN.md](../LOCKDOWN.md) shows how to put
  Broadcast Box where nobody but the plugin can reach it.
- **The chat bridge is unchanged.** A post in the bridged channel goes to Mumble whoever wrote it,
  because who may post there is Mattermost's channel permission, not the panel's.

## How a person gets a red dot

A streamer links their stream once, by pasting the **bearer token** their streaming software
publishes with. Usually that is on the lock screen, where linking is what unlocks Matty (see
[Unlocking Matty](#unlocking-matty)); somebody let in to watch does it from **Link your stream**
in their bar. Once linked, the bar at the bottom of the panel holds their **stream title**.
Relinking is the small, quiet link key among that bar's tools, since it is needed once in a long
while; its tooltip says which stream they are linked as. The plugin sends that token to Broadcast Box,
which resolves it to the stream it belongs to and hands back the stream key.

The link is also what makes somebody a streamer to the rest of the panel. Planning a stream in
[Up next](up-next.md#up-next), changing one, and joining somebody else's are for linked people only, checked by
the server. People let in can read the row but not add to it. The same goes for [Clips](panel.md#clips):
making, upvoting and sharing clips are for linked people only, and watching them is for linked people
and people let in.

That resolution is the ownership check. In Broadcast Box v2.0.2 a reserved profile is stored as
a file named `<streamKey>_<token>`, and `getProfileFileNameByBearerToken` matches **only the
token half**. Stream keys are public — they are printed in `/api/status` — so anyone could type
`alice`; nobody can produce its token but its owner. Linking by token means a user
cannot claim somebody else's stream.

The token is stored server-side in the plugin's KV store, because profile edits authenticate
with it. It is never sent to the browser. The stream key, being public, is shown plainly.

**Linked means both halves agree.** A link is two entries in the plugin's store: the person's stream
key, and the key's owner. Somebody counts as linked only while each names the other. A write that
failed half way therefore never leaves somebody linked who should not be. Of two accounts linking one
token at the same moment, only the one whose write landed last is linked. A read of either entry that
fails is answered as "try again", never as locked or unlocked.

**Match stream keys to usernames** is a weaker alternative, off by default: it attributes a stream whose
key equals a Mattermost username, and usernames are self-service, so anyone could rename
themselves into someone else's badge. It attributes the stream to them, for the live badge and
their name on its card. A username match never unlocks the panel, plans nothing, and gives no say
over clips of the stream. Leave it off unless you have streams nobody can link.

**Live on Twitch.** While an admin has **Show people live on Twitch** on, somebody who has connected
their Twitch account gets the same badge while they are live there, and loses it after the same
missed checks. Live on Broadcast Box and Twitch at once, they wear one badge. A channel an admin
marked 24/7 under Twitch connections gets none. See [Twitch (optional)](up-next.md#twitch-optional).

Because badges are written as custom statuses, **Enable Custom User Statuses** must be on in
**System Console > Site Configuration > Posts**. Turning the plugin's badge setting off, or
deactivating the plugin, restores everyone's previous status.

### Locking publishing down

Broadcast Box decides how a WHIP bearer is resolved from `STREAM_PROFILE_POLICY`:

| Value | Behaviour |
| --- | --- |
| `ANYONE_WITH_RESERVED` (default) | A bearer matching a reserved profile's token streams as that profile. A bearer that *is* a reserved stream key is rejected. Anything else streams ad hoc under that name. |
| `RESERVED` | Only reserved profiles may stream, and only with their token. Everything else is refused. |

Set `STREAM_PROFILE_POLICY=RESERVED` to guarantee nobody can publish under a name they do not
hold the token for. Rotate a token without changing the display name via the admin
`ResetProfileToken` endpoint — the stream key, and therefore everything the plugin shows, stays
the same.

## Stream notes

The note — the **stream title**, the top row of the bar at the bottom of the panel — **edits in
place**: that row is always a text field holding the title, and while nothing is set it reads "What
are you streaming?" in the placeholder's grey, as Mattermost's message box does. Once the words
differ from the saved title, the check at the bar's right end, where the message box has Send,
lights up; Enter or the check saves. Clicking away saves it too, when the words have changed, both
being the same act, "I have finished", and only Escape throws the edit away, putting the saved
title back.

Broadcast Box keeps a per-stream profile — a short note (its MOTD) and a listed/unlisted flag —
for stream keys that are *reserved* on the server. Streamers can edit their own note from the
panel once they have linked their key. If a stream key is not reserved, the editor is hidden;
there is no profile to write to.

**A channel with nobody to write one** — a 24/7 stream, typically — can be given a note by an admin
in **Stream notes**, `streamKey = note` per line. It is only ever a fallback: a streamer who writes
their own from the panel is never overruled by it, and Broadcast Box's own "Welcome to …"
placeholder is blanked before the fallback is considered, so it does not block one.

Deliberate decisions:

- **Unlisting is refused by the API, not just absent from the UI.** The save handler always writes
  `isPublic: true` and ignores the request body's value.
- **The panel never hides anyone.** Broadcast Box's unlisted flag removes a stream from
  `/api/status` entirely, so an unlisted streamer would vanish from this panel *and* silently
  lose their live badge. Since the point is that everyone on the server sees who is live, the
  plugin exposes no way to unlist, always saves profiles as listed, and shows a one-click
  **Show me** rescue to anyone Broadcast Box already has hidden.
- **Your bearer token and stream key never get into a title.** The bar sits right beside the link
  key, whose sheet asks for the token to be pasted, so a mis-paste is taken out before the title is
  saved —
  either becomes "my stream" — and a title written in Broadcast Box itself is checked again before
  it reaches a card. Titles everywhere else in the panel (a plan's words, What's on?) are checked
  before they are cut to length, so a token pasted at the end of a long one is never left half in.
- **Editing requires an explicit link, not the username auto-match.** Mattermost usernames are
  self-service, so treating one as proof of ownership would let anybody rename themselves into
  control of someone else's stream. Username matching (**Match stream keys to usernames**) still
  drives the read-only live badge.

For the record, unlisted was never access control in the first place: in v2.0.2 the WHEP
playback handler never consults the flag, and `/api/status?key=<key>` returns an unlisted stream
to anyone who asks. Anybody holding the key could always watch.

### What's on?

Among the bar's tools, under the **stream title**, anybody with their stream linked has a small
**What’s on?**: a television while nothing is said.
It is for saying which movie, show or game you are streaming *right now*: the impromptu stream that
was never planned, or the plan that turned into something else. It is there even when Broadcast Box
keeps no profile for your stream and the title cannot be set, when it is the only way to say what
is on.

- **Pressing it opens a picker in a sheet above the bar**, headed **What’s on right now**, with
  "Clears itself after your stream ends" under it. The picker is the planning form's own first row:
  the film and game toggles joined to the title box, and a tick at its end to save. It rises out of
  the bar, and the panel above gives way, so the bar and the chip you pressed stay exactly where
  they were. The titles it finds open upward too, between the sheet's heading and the box, so the box
  does not move while you type. With lookups on (see
  [Movies and games](up-next.md#movies-and-games-optional)) the box finds the real title as you type, exactly
  as it does when planning; with them off, or for a kind that cannot be looked up, you choose the
  kind and type the name, and it is saved without a cover. The kind is needed, unlike a plan's:
  without one this would only be a second stream title. Enter saves, Escape closes it, and focus
  comes back to the chip either way. The keyboard still goes from the chip into the picker.
- **Once said**, the chip shows it: the cover when there is one, the kind's icon when not, and its
  title. Pressing it opens the picker on it to change it, and **Take down** in the sheet's heading
  takes it down.
- **Where viewers see it.** On the streamer's **Live now** card, as one more of the tags along the
  top of the card — on the same dark ground as the uptime, its edge in line with theirs, with a
  brighter edge when it opens a profile — on a line of its own above their name, where
  it stays readable over a bright picture; on the line under their picture, between their name and their stream title, on the
  same baseline as both; and in their profile popover's **Live now** row. A tag linked to a real
  title opens the same little profile an Up next card has — cover, a few lines, its IMDb, Steam or
  IGDB page — when it is pressed (not pointed at: the card underneath watches the stream when pressed),
  and from a card that is not already playing it offers **Watch now**. The popover only shows it,
  as it always has a plan's title: it is a glance at a person.
- **One rule decides what is on**, on the server, and every one of those places reads its answer:
  what the streamer said with What's on?, and otherwise the title linked to their own plan that
  started most recently, when that plan has one. So **What's on? beats a running plan's title** —
  a plan is what somebody meant to stream, this is what they say they are streaming. It is never
  shown on a 24/7 channel, and only on the stream the streamer's own link proves is theirs.
- **It clears itself.** Once the stream has been off the air for **Missed checks before a badge
  comes off** — the same grace the live badge has, counted in poll intervals whatever else asks
  for a poll, so an OBS reconnect costs nothing — it is taken down, and
  tomorrow's stream starts with nothing said. Said before going live, it waits for the stream, for
  up to **twelve hours**. Unlinking your stream takes it down too. While you are off the air with
  something said, the bar asks the server again once a minute, so the chip does not go on
  showing a choice that has been taken down.
- **In a narrow panel** the stream title gives way first, and the chip's title is cut short. Below
  about 360 px of bar, the chip keeps its picture and drops its words. Those are still its name, and
  in its tooltip.

On the server it is one KV document, `now_playing`, keyed by user id: the kind, the words, the
linked title in the same shape a plan carries it, when it was said and whether the stream has been
seen since. `GET`, `POST` and `DELETE /api/v1/nowplaying` read, set and take down your own. Setting
it needs your stream linked and is checked exactly as a plan is — eighty characters, your own stream
key and bearer token never stored (either becomes "my stream", for a plan's words too), a picked
title only under its own icon, and its details looked up by the server
rather than taken from the browser. Setting and taking down are metered like the stream title, 10 a
minute; taking down never needs the link. What reaches every panel is the answer on each live
card: the kind, the words and the title's details, never when it was said and never a stream key.

### Why the pop-out link defaults to off

For Broadcast Box, the plugin server only ever talks to **Broadcast Box URL**, which should be a LAN
address. (Its other outbound connections, Mumble, the lookups, Twitch, Steam, Hytale, the UnifiedPush push servers and GitHub, are listed under
[Security posture](security.md#security-posture).) It never touches your reverse proxy. The one thing that sends a *browser* to the proxy is the **Pop out** link.

If the public site is behind HTTP Basic Auth, a viewer who clicks that link gets an auth challenge, and a
single page load can produce a burst of `401`s as its subresources are fetched. That is precisely the
pattern CrowdSec's `http-auth-bf` and fail2ban's `nginx-http-auth` jail are built to ban — so a legitimate
viewer can get themselves firewalled just by clicking a link in the panel.

Leave **Public watch URL** blank unless the site is reachable by everyone who uses the plugin. Playback
works either way; the plugin negotiates WHEP itself through its own endpoint.

**Who gets it, and what it hands out.** Pop out, the player's **Open in a new tab**, is offered only
to people who have linked a stream,
on a tile of a live stream that is not a 24/7 channel. It is a plain link to the plugin's
`/api/v1/watch/popout?handle=…`, which answers with a redirect to the watch page. That redirect is
never cached and sends no referrer, and the route is refused to anybody not linked. The page it opens
has the **stream key** in its address. That is the nature of the link, and nothing the plugin does later
can take the key back: it keeps working on the public page, and at Broadcast Box, after an unlink or
a let-out. That is why people let in to watch never get it.

Before 3.0.0, with this setting filled in, every stream's address, key and all, was in the stream list
that every signed-in browser is sent; see [Upgrading to 3.0.0](upgrading.md#upgrading-to-300).

### If you run a reverse proxy with fail2ban or CrowdSec

Mattermost answers **404** on every `/plugins/<id>/...` route while a plugin is stopped,
restarting or crashed. The panel polls in the background, so a plugin that is merely *down* makes
a logged-in viewer emit a steady trickle of 404s — which a brute-force jail reads as probing and
bans them for.

Two mitigations, both worth having:

- The panel's fallback poll backs off exponentially on failure (30s, doubling, capped at 10
  minutes) and resets on the first success. In a 5-minute window a dead plugin produces about
  three requests instead of ten.
- Exempt plugin routes in your proxy's filter. For the Nginx Proxy Manager `npm` fail2ban filter,
  add `/plugins/` alongside the `/api/v4/` and `/static/` exemptions it already has — see
  [examples/fail2ban-npm-filter.conf](../examples/fail2ban-npm-filter.conf).

See [LOCKDOWN.md](../LOCKDOWN.md) for putting Broadcast Box behind a proxy and a firewall, so that its
origin is reachable only through this plugin.

## Hashtags in stream titles

A hashtag in a streamer's title — under the player, or on their Live now card — can be pressed, and
it finds the messages in Mattermost tagged with the same hashtag, as clicking one in a post does. The
results open over the panel, on a card built like the **Trophies** card, so whatever is
playing keeps playing.

**What counts as a hashtag** is exactly what Mattermost files a message under, because a search can
only find what was filed: a `#`, then a letter, then letters, digits, `-`, `_` or `.`, ending in a
letter or a digit — and no shorter than Mattermost's minimum hashtag length, which is 3 unless an
admin has changed `ServiceSettings.MinimumHashtagLength` in its configuration, the same rule that
decides which hashtags in posts become links. Punctuation round one is left off, so `(#arma3)` and
`#arma3,` are both `#arma3`; a word that only *contains* a `#` — `x#arma3`, `#arma3's` — is not a
hashtag, and neither is a `#` inside an address, which stays part of the link. On a Live now card the
hashtags sit on the line under the title, with its links, where an ellipsis cannot hide them; the
ones a title ends on are taken off the line above, and one in the middle of a sentence stays there
too, as the word it is.

**The results** come newest first: whose message it is, with their picture and their name as
Mattermost writes names (it follows **Teammate Name Display**), where it was posted — a channel, a
direct message or a group message — when, and three lines of the message as plain text with the
hashtag marked. The second page is asked for straight away, as Mattermost's own search does, and
**Load more** then asks for the next for as long as the last one found anything — on a server that
searches with Elasticsearch or Bleve a page can come back short and still have more after it — and
says when that was all. Each message is a link: clicked, it opens in its channel the way
Mattermost's own **Jump** does, and the panel stays open, back at its usual width if it was
expanded; on a phone-sized screen, where the panel covers the channel, it closes first, as Jump
closes it. Ctrl- or Cmd-click opens it in a new tab instead. With Collapsed Reply Threads on, a reply opens at
the start of its thread, and says so: opening the reply itself would put its thread in the side
panel in place of the panel.

**Open in Mattermost search**, at the bottom, runs the same search in Mattermost's own search
instead, whose results take the side panel's place — exactly what a hashtag clicked in a post does.
From a popped-out panel, the window it was popped out of does the searching and the panel stays where
it is. Where Mattermost has no search bar on screen — a phone-sized window, or a popped-out channel —
the button says so rather than doing nothing.

The search is Mattermost's own, sent from the reader's browser with their own session: it covers the
**current team's** channels they belong to, plus their direct and group messages, and finds exactly
what Mattermost's search box would find for them, and nothing more. It matches the whole hashtag, in
any case — `#Arma3` finds `#arma3`, but not `#arma` or the word `arma3`. A server with message search
switched off (`ServiceSettings.EnablePostSearch`) gets a message saying so. Nothing goes anywhere but
the Mattermost server the page came from.

People and channels that Mattermost's page had not loaded are asked for once and kept, since the next
page or the next hashtag usually names the same few, but only while the same person is signed in.
Mattermost answers each reader differently: while **Show Full Name** is off, it sends first and last
names to system admins only. So signing out, which does not reload the page, forgets them all, and a
search still on its way at that moment cannot put back what it was told. Whoever signs in next on
that page is shown only what Mattermost tells them.

## @names in stream titles

An `@dave` in a streamer's title can be pressed wherever its hashtags can, in the ticker under the
player and on the Live now card's line of links, and it opens **Mattermost's own profile card** for
Dave, the one a name in a post opens: with the panel's row in it (live now, with Watch, their awards
and plans) and Mattermost's Message and Call. Escape or a click outside closes it, and the stream keeps
playing behind it.

- **The name shown is Mattermost's.** It follows **Teammate Name Display**, as a mention in a post
  does, so with full names shown `@dave` reads "@Dave Hill" once Mattermost has found them. The ticker
  measures itself again when that happens, so the loop does not jump.
- **What counts as an @name** is Mattermost's own rule for a mention in a post: an `@` that does not
  follow a letter or digit (so `me@example.com` is an address, not a mention), then letters, digits,
  dots, dashes and underscores. A dot, dash or underscore at the end belongs to the sentence, so
  `@dave.` is `@dave`. `@all`, `@channel` and `@here` stay plain words, in any case. Nobody's name
  inside an address is taken out of it: `https://twitch.tv/@dave` stays one link.
- **Nobody is notified.** A title is not a post, and Mattermost raises mentions only from what is in
  a post's message. The plugin never puts a title in one: a shared clip's and a shared plan's message is
  fixed text and a link.
- **A name nobody has** stays as it was typed, plain, and pressing it does nothing.
- **Not on Twitch cards.** Twitch titles use `@` for Twitch accounts, and a Mattermost account with
  the same name would be the wrong person. Not in the bar's own title either, which is a text field,
  nor in tooltips, plan titles or a clip's note.
- Each @name is drawn as a quiet chip, a little brighter than the words round it on a faint lift,
  warming to the theme's accent when pointed at (on a Live now card, over its picture, it only
  brightens).

Looking somebody up is Mattermost's own work, into Mattermost's own store, from the reader's browser
to the same server: the plugin keeps nothing of its own about them, so there is nothing for it to
forget at sign-out.
