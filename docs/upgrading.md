[The Matty Plugin manual](README.md)

# Upgrading

## Upgrading to 4.2.0

4.2.0 no longer takes over from VillaNET, the plugin Matty Plugin was before 4.0.0. Nothing else
changes.

- **A server still on VillaNET upgrades through 4.1.1 first.** Install Matty Plugin 4.1.1 and let it
  take over from VillaNET as its manual says, then upgrade to 4.2.0. 4.2.0 does not look for
  VillaNET, its settings, its records or its folder: enabled beside it, it starts as a fresh
  server would, with nothing of VillaNET's.
- **A server that already took over needs nothing.** What the takeover brought over stays where it
  is, and so does its record in the plugin's store. The card about it is no longer shown to system
  admins.
- **Links and posts made under VillaNET work as before**: clip and plan cards, and links naming
  `ceo.villa.villanet`, which the Matty app opens.
- **The contract stays revision 4.** The refusal `taking_over`, `takeoverAlert` in a system admin's
  `GET /me` and `/api/v1/admin/takeover` are gone from it (`contract/README.md`); nothing else
  moves. It works with Matty 0.1.28 and 0.1.29.
- **Nothing to move.** Upgrade as usual, with `ceo.villa.matty-4.2.0-linux-amd64.tar.gz`.
- **Coming from 4.1.0 or earlier?** Read [Upgrading to 4.1.1](#upgrading-to-411) as well, and on
  back to the version you are coming from.

## Upgrading to 4.1.1

4.1.1 changes how the server sends push notifications when a push server cannot take them at once.
Nothing else changes.

- **A check message answered `507` waits for the phone.** ntfy answers `507` for a phone whose ntfy
  has not connected yet. The check message is now tried again, for up to ten minutes, and the
  registration waits meanwhile, instead of reading as failing. See
  [When a phone registers](push.md#what-is-sent-where-and-who-sees-what).
- **A notification's push, and the push that takes a notification away, are tried for five
  minutes** when they may go through later: after 2, 10 and 60 seconds and then once a minute,
  instead of three more times within 72 seconds. Of the pushes waiting to be tried again, at most
  256 are one person's, besides check messages.
- **A `Retry-After` pauses the phone it answered for**, not the whole push server: with ntfy it is
  often one phone's own limit. The push server is paused only as
  [Limits](push.md#what-is-sent-where-and-who-sees-what) says: when two people's phones get such an
  answer for the first time, one after the other, with nothing delivered there in between.
- **Nothing to do.** No setting moves, and there is no contract change. Upgrade as usual, with
  `ceo.villa.matty-4.1.1-linux-amd64.tar.gz`. It works with Matty 0.1.28 and 0.1.29.
- **Coming from 4.0.0 or earlier?** Read [Upgrading to 4.1.0](#upgrading-to-410) as well, and on
  back to the version you are coming from.

## Upgrading to 4.1.0

4.1.0 adds **push notifications** for Matty: with them on, Matty notifies people through a
UnifiedPush app on their phone, such as ntfy, whenever Mattermost would notify them, without
keeping a connection to this server. Each push is end-to-end encrypted. They are off until an admin
switches them on, and nothing else changes.

- **A new section, Push notifications**, after **App check**: **Send push notifications to Matty**
  (off), **Push servers on your own network**, **Only the push servers listed above**, **Contact for
  push servers** and **Status**, a card of how many phones get pushes, by push server. See
  [Push notifications](push.md).
- **It needs Mattermost's own push notifications on**, in **System Console > Environment > Push
  Notification Server**: only then does Mattermost decide to push, and only then does the plugin
  hear of it. While the plugin's switch is on and Mattermost pushes to nobody, system admins are
  told so among the panel's alerts.
- **With the switch on, the server contacts push servers**: whichever ones people's UnifiedPush
  apps name, under the address rules in
  [What the plugin touches](../LOCKDOWN.md#what-the-plugin-touches). A firewall that limits what the
  Mattermost server may reach must let it reach them.
- **The Matty app needs 0.1.28 or newer** for push notifications. Every Matty from 0.1.27 goes on
  working as it did, and each person switches push notifications on in Matty for themselves.
- **No contract change.** It is an addition to revision 4: `push` in `GET /access`, the
  `/me/push` routes, `GET /admin/push` and `pushAlert` in a system admin's `GET /me`
  (`contract/README.md`, "Push notifications").
- **Nothing to move.** Upgrade as usual: in **System Console > Plugins > Plugin Management**, upload
  `ceo.villa.matty-4.1.0-linux-amd64.tar.gz` and let it replace 4.0.0. The plugin makes its push key
  on its first start.
- **Going back to 4.0.0** stops push notifications; Matty then says the server does not offer them.
  The registrations and the push key stay in the plugin's store, and are used again if 4.1.0 comes
  back: a registration whose session ended meanwhile is dropped once a check finds it so, and the
  rest lapse 90 days after they were last written.
- **Coming from VillaNET?** Upgrade through 4.1.1 first: see [Upgrading to 4.2.0](#upgrading-to-420).

## Upgrading to 4.0.0

4.0.0 is the release after VillaNET 3.8.0, under a new name and a new id: **Matty Plugin**,
`ceo.villa.matty`, which Mattermost lists as **Matty**. It is the companion plugin of the Matty app,
and on a fresh server it starts with nothing of anybody's community in it. On a server that ran
VillaNET it takes over from it and goes on as it was: the same name, banner and theme, the same
people unlocked, the same hours, awards, clips and plans.

Mattermost treats a new id as a new plugin, so 4.0.0 was not installed over 3.8.0 but beside it.
From an earlier 3.x, read the sections below back to your version as well: what they say each
release changed still holds.

What people notice:

- **The panel looks like part of Mattermost.** It follows the Mattermost theme each person uses:
  its ground, words, lines and accent are the theme's, and only LIVE keeps a red of its own. The
  player and the Live now cards have no red frames: a card has a thin grey line, and the ones you
  are watching carry a quiet WATCHING tag. The bar at the foot is the twin of Mattermost's own
  message box beside it, the same height, edges and colours. See [The panel](panel.md).
- **Everything else is as it was.** The panel is still called what it was called (VillaNET's name
  comes over with its settings), with the same banner, the same theme for everybody if it gave
  one, the same artwork, and everybody's links, let-ins, hours, medals, achievements, clips, plans,
  banners and connections.
- **The Matty app needs 0.1.27 or newer.** Earlier versions of the app talk only to
  `ceo.villa.villanet`, and once the takeover is done they no longer show the panel. Get the new
  app to your people at the same time.

Matty Plugin 4.0.0 to 4.1.1 take over from VillaNET when they are enabled beside it; 4.2.0 does
not. A server still on VillaNET follows the manual that comes with 4.1.1, then upgrades: see
[Upgrading to 4.2.0](#upgrading-to-420).

### New in 4.0.0, for an admin

- **Name**, first under **Name and artwork**: what people on this server see the panel called. A
  server that took over keeps VillaNET's name; a fresh one says Matty. The System Console goes on
  listing the plugin as Matty either way. See [Name and artwork](install.md#name-and-artwork).
- **App check**, a new section after **Access**: whether a phone's Matty is the official signed
  build, on Android's own word. **Check the Matty app** is on, as **Check, and show the result**,
  which refuses nobody, and **Results** lists each Matty's verdict under **Matty apps**. See
  [The app check](security.md#the-app-check).
- **A signed release.** The bundle carries the release's signatures, and the plugin says whether
  its own files are as signed: under **This plugin**, in **App check > Results**, and in a note
  to system admins in the panel when they are not, or when its folder holds files nobody signed.
  See [Signed releases](security.md#signed-releases).
- **The client contract is revision 4** (`contract/README.md`, "What changed in revision 4"):
  every route, event and share link carries the new id, and `X-VillaNET-Peek` is `X-Matty-Peek`.
  `GET /access`, `GET /me` and the `me` event carry `name`, the server's name for the plugin, and
  `artwork`, whether there is an emblem and a ghost. What somebody named in What's on? has the
  source `"panel"` where it was `"villanet"`, and the stream id in a WHEP answer is `stream` where it
  was `villanet`. Refusals and the posts of shared clips and plans use the server's name. A settings
  backup is of kind `matty-settings`, saved as `matty-settings.json` or
  `matty-settings-with-passwords.json` (a restore still takes VillaNET's), and the airtime export is
  `matty-airtime.json`. `bannerColor` in `GET /me` may be empty, meaning no colour was chosen.
  Revision 4 also adds `release` and `app` (`GET /release`, `POST /app/challenge` and the rest),
  which a client that does not read them can ignore. Every other route and field is as it was.

## Upgrading to 3.8.0

3.8.0 lets people choose the part of a picture their Matty banner shows, and changes nothing you
see in the panel or the System Console.

- **People choose what their banner shows** in Matty 0.1.25 and later. A still picture is cut on
  the phone, so only the chosen part is sent. An animated GIF is sent whole, as before, and the
  plugin keeps the part of it to show beside it: four numbers, which Matty uses wherever it draws
  the banner. Somebody can change that part of a GIF up to thirty times in ten minutes without
  sending the picture again.
- **Nothing to set, and nothing to move.** The part to show is kept in the picture's file name in
  `<File Storage directory>/ceo.villa.villanet/banners/`, so it goes wherever the picture goes:
  taking a banner away, by its owner or by a system admin, takes it too, and a new picture sent
  without one has none. Banners set before 3.8.0 keep showing the middle of the picture.
- **Going back to 3.7.x** keeps every banner that has no part chosen, which is every still picture
  Matty sent, since those are cut on the phone. A GIF whose part was chosen is not shown by 3.7.x,
  which does not read the name its file then has: its owner sets it again. 3.7.x does not see that
  file, so it stays on the disk, and taking a banner away on 3.7.x leaves it there too. Moving
  forward to 3.8.0 again shows that GIF once more, unless its owner set a newer banner meanwhile:
  a banner taken away while on 3.7.x, by its owner or by a system admin, must be taken away again
  after upgrading. To avoid that, delete the files with a part chosen before going back: their
  names have four more numbers after the version, as in `<user id>-<v>-0-3333-10000-3333.gif`.
- **No contract change.** It is an addition to revision 3: Matty 0.1.20 to 0.1.24 keep working as
  they did, and show the middle of every banner.
- **Coming from 3.7.2 or earlier?** Read [Upgrading to 3.7.3](#upgrading-to-373) as well, and on
  back to the version you are coming from.

## Upgrading to 3.7.3

3.7.3 mends **Back up** under **Backup** in the System Console. In Mattermost's desktop app, and
now and then in a browser, the download showed as cancelled at 0 B: the file was let go before
the app had started saving it. It is now kept until the save has had time to begin. Nothing else
changes.

- **Nothing to do.** No setting moves, and there is no contract change.
- **Coming from 3.7.1 or earlier?** Read [Upgrading to 3.7.2](#upgrading-to-372) as well, and on
  back to the version you are coming from.

## Upgrading to 3.7.2

3.7.2 takes real people's names, stream keys and dates of birth out of the plugin's built-in
settings. Nothing else changes, in the panel, in Matty or in what a saved setting does.

- **Stream names starts with the 24/7 channel alone.** Up to 3.7.1 its built-in value also named
  seven people's streams on the server VillaNET was written for. Now it is
  `villanet-watts = Alan Watts Chillstep`, the channel that **24/7 stream keys**, **Stream notes**,
  **24/7 channel achievements** and the shipped picture already set up. If Stream names was ever
  saved on your server, nothing changes. If it never was, the server ran on the built-in list, and
  now a stream that no Mattermost account owns and that only the old list named reads "Stream"
  and the first six characters of its handle wherever its name shows: its card, the **Recording
  now** list under **Clips → Clips on the server** in the System Console, and, once it airs
  again, the boards (until then the boards keep the name its hours were last recorded under). A
  stream linked to an account shows the account's name, as before. The server log says once a
  start when Stream names has never been saved.
- **To keep the names you had**, before upgrading: copy what **System Console → Plugins → VillaNET
  → Streams → Stream names** shows, or use **Backup → Back up** (the file carries the values in
  use, saved or not). After upgrading, paste the lines back into Stream names and save, or use
  **Backup → Restore…** with that file. Upgraded without either, type them in from your own
  notes, one `streamKey = Display Name` a line, such as `alice-stream = Alice`.
- **"Start from the old bot's list" fills in nobody.** The birthday list's starting list held real
  dates of birth and is now empty: the button still shows on an empty list, and pressing it says
  "0 people from the old bot’s list. Save the page to keep them." It never filled anything in by itself, so a list you saved is
  exactly as it was, and an empty one stays empty and posts nothing, as before. To put a list in,
  use **Add somebody**, or restore a settings backup that has it. A backup file restores only the
  settings it names, so one written by hand works too:
  `{"kind": "villanet-tv-settings", "version": 1, "settings": {"Birthdays": "[…the list's JSON…]"}}`.
- **Names that differ** shows a made-up example, `alice_ = alice`, in its empty box. It is only an
  example: it was never a value, and what you saved is untouched.
- **On a fresh install** the three look like this: Stream names holds the one 24/7 line above,
  Names that differ is empty with `alice_ = alice` greyed in it, and the birthday list says "Nobody
  on the list yet." over its button.
- **No contract change**, and the starting list's route answers `[]` in the same shape. Matty
  works as it did.
- **Coming from 3.7.0 or earlier?** Read [Upgrading to 3.7.1](#upgrading-to-371) as well, and on
  back to the version you are coming from.

## Upgrading to 3.7.1

3.7.1 fixes a fault in Clips recording, and changes nothing you see or set.

- **A recording no longer stops without a word after an internal error.** Since 3.7.0, a bug met
  while recording a stream is logged with its stack and recording starts again at the stream's
  next keyframe. In two places, ending a stretch of the recording and adding a frame to it, such a
  bug could instead leave the recording stuck for good: that stream's clips stopped, with no
  message, until the plugin was restarted. Now recording starts again as it should, and what was
  being cut from that stretch at that moment says "not enough of the stream is recorded yet"
  rather than coming out wrong. Nothing is different while no such bug happens.
- **No internal error in the background takes the plugin down any more.** A few background jobs
  (stopping a recorder, a game search's Steam or IGDB half, a shared title lookup, keeping a
  picture on disk or tidying them, the accounts update, letting go of an abandoned voice
  connection) now log such a bug and carry on, as the rest already did: a search answers from the
  other source or says it could not search just now, and nothing waits on it for good.
- **No contract change**, and nothing in the panel or the System Console. Matty works as it did.
- **Coming from 3.6.1 or earlier?** Read [Upgrading to 3.7.0](#upgrading-to-370) as well, and on
  back to the version you are coming from.

## Upgrading to 3.7.0

3.7.0 adds profile banners for Matty, and nothing you see in the panel.

- **People can give their profile a banner** in Matty 0.1.22 and later: a picture across the top of
  their profile, which everybody who opens it sees. A JPEG, a PNG or a GIF, and an animated GIF
  stays animated. Guest accounts and bots cannot set one. The panel shows nothing new.
- **Nothing to set.** The pictures are kept beside the clips, in
  `<File Storage directory>/ceo.villa.villanet/banners/`, one file a person: a new banner replaces
  the old one, and taking it away deletes the file (an old file the disk would not let go of is
  never served, and goes with that person's next change). Like Clips, this needs **File Storage System**
  set to **Local file system**; on any other, nobody can set a banner. They take at most 2 MB a
  person for a JPEG or a PNG and 8 MB for a GIF, so at most 8 MB times the number of people who
  have set one; somebody can set six in ten minutes.
- **A system admin can take anybody's banner away.** There is no button for it in the System
  Console yet: Matty offers it to a system admin on somebody's profile, and it is
  `DELETE /api/v1/admin/banner?user=<user id>`. A deactivated account keeps its banner, as it keeps
  its connections; one deleted from Mattermost leaves its file behind until an admin takes it away
  by its id.
- **A security pass, with nothing to set.** An animated GIF banner of more than 300 frames, or
  more than about 420 million pixels in all, is refused as any other picture that is not a banner.
  The server log warns once when the **Broadcast Box URL** is plain `http://` to another machine
  (see [Broadcast Box URL](install.md#broadcast-box-url)). Links and pictures typed in Mumble reach Mattermost
  as text. A bug that would have stopped the whole plugin now fails one request, or one pass of a
  loop, and is logged with its stack. The 24/7 channel pictures in the System Console no longer
  put the channel's stream key in an address.
- **No contract change.** It is an addition to revision 3, so Matty 0.1.20 and 0.1.21 keep working
  as they did, and show no banners.
- **Coming from 3.6.0 or earlier?** Read [Upgrading to 3.6.1](#upgrading-to-361) as well, and on
  back to the version you are coming from.

## Upgrading to 3.6.1

3.6.1 changes one line in Matty and nothing you see in the panel.

- **A movie or a show you tag reads under your name.** While you are live with a movie or a show
  picked or typed in What's on?, Matty's member list says "Streaming Quantum of Solace" under your
  name instead of "Live now", and it says so even if Steam reports a game for you at the same time:
  you have said what is on. A game in What's on? works as it did.
- **Nothing to set, and nothing stored changes.** Going back to 3.6.0 is installing its bundle
  again.
- **No contract change.** It is still revision 3, so Matty 0.1.20 and later show the new line as
  soon as the server has 3.6.1, with no update to the app.
- **Coming from 3.5.x or earlier?** Read [Upgrading to 3.6.0](#upgrading-to-360) as well, and on
  back to the version you are coming from.

## Upgrading to 3.6.0

3.6.0 changes nothing you see in the panel. It moves work to the server: which awards go beside
whose name is decided there once, the boards are read when somebody looks at them instead of being
sent to everybody, and a refusal always says why in the same form.

- **Install it before, or together with, Matty 0.1.20.** 3.6.0 speaks revision 3 of the client
  contract (`contract/README.md`): it takes three events and the boards out of what a client was
  sent, so the plugin and the app go out as a pair again. Matty 0.1.20 on an older VillaNET asks it
  nothing, and shows "VillaNET needs an update" where the panel would be. Matty 0.1.19 on 3.6.0
  goes on showing who is live and playing streams, says once that the server's VillaNET is newer
  than it knows, and shows no boards and no award rows until it is updated.
- **Nothing to set, and nothing stored changes.** There is no new setting, and nothing in the
  plugin's store is read or written differently, so going back to 3.5.0 is installing its bundle
  again.
- **Open browser tabs take the new panel by themselves** when the plugin is updated. A stream
  playing in one stops once, and can be started again.
- **The boards are a request of their own**, `GET /api/v1/boards`, open to every signed-in reader as
  the list of streams is. The list no longer carries the boards, the month medals, the fans, the
  achievements, anybody's choice of awards or a card's places on the boards. It carries what those
  come to (the awards beside each name and on each card) and one word that moves when the boards
  change. **Hours that only tick are no longer sent to anybody**: 3.5.0 sent the whole list to
  every connection once a minute while somebody was live.
- **One event, `me`, in place of three** (`access`, `twitch_link` and `account_link`). It still
  carries nothing, and still means "ask again who you are".
- **A playback request has to ask for the sound and the picture together.** `POST /api/v1/whep`
  answers `400 bad_offer` to an offer that does not have exactly one audio and one video section,
  each to receive. Both clients have always sent exactly that. The check is there because the
  route used to pass on whatever a signed-in person who may watch posted to it, and Broadcast Box
  2.0.2 stops altogether, for everybody, when it is handed an offer with no picture to receive.
- **Every refusal says why in the same form**: `{"error", "code"}`, and `retryAfterMs` with every
  "too often". The sentences on the lock screen are the server's now; they read as they did.
- **Coming from 3.4.x or earlier?** 3.6.0 carries everything 3.5.0 changed, so read
  [Upgrading to 3.5.0](#upgrading-to-350) as well, and on back to the version you are coming from.

## New in 3.6.0

- **Awards decided once, on the server.** What goes beside a name, a Live now card's badges and a
  profile's rows were worked out three times over, in the panel twice and in Matty once; the server
  now decides them and both clients draw what they are sent, so they can never disagree.
- **"Playing" or "Streaming", decided once.** `GET /api/v1/accounts` says in one line what each
  person is doing (`activity`): the game Steam reports, and whether their own stream is of that
  very game. Matty prints it; the panel does not show it yet.
- **A plan says when it is full, and a shared clip's card when the clip goes** (`full` on a plan,
  `expiresAt` on a card), and one clip can be asked for by itself (`GET /api/v1/clips/clip?id=`).
  Matty reads all three; the panel reads the card's.
- **Stand-ins for developing on one machine.** `build/fakebb` stands in for Steam and for Mumble
  as well as for Broadcast Box, and `make standin-linux` builds a plugin that asks the stand-in
  Steam. That build is never a release: it is made apart, says "standin" in its file name and its
  version, and refuses to start anywhere but where the Site URL is the machine it runs on.

## Upgrading to 3.5.0

3.5.0 changes nothing you see in the panel. It is about how the panel and the native app, Matty,
talk to the server: less often, and from one account of who is live.

- **Install it before, or together with, a Matty that asks for it.** From this version the plugin
  and the app are written to one client contract and released as a pair
  ([Native apps (Matty)](building.md#native-apps-matty); `contract/README.md`). A Matty built for 3.5.0 that
  meets an older VillaNET asks it nothing, and shows "VillaNET needs an update" where the panel
  would be. An older Matty keeps working with 3.5.0, with one exception: its **Unlink my stream**
  is refused ("Paste your bearer token first.") until the app is updated, because unlinking moved
  to its own request. Unlink from the web panel meanwhile.
- **Two requests changed their form**, for the panel and the app alike: a title search is now
  `POST /api/v1/media/search` with what to look for in its body (a `GET` there answers 405), and
  unlinking is `DELETE /api/v1/me/link` (a `POST` with no token answers 400). Nothing else that was
  there before is removed. If a reverse proxy in front of Mattermost lists methods per path, let
  `DELETE` through to `/plugins/ceo.villa.villanet/`.
- **Unlinking can no longer be refused for "too many attempts".** It had shared the meter of the
  Unlock button, so five mistyped tokens could lock somebody out of taking their link back for a
  quarter of an hour.
- **Open panels are told of changes less often while somebody streams.** The boards' hours tick on
  every check of Broadcast Box, and each tick used to send the whole snapshot to every connection:
  with one person live, every 15 seconds, to everybody, around the clock. Hours that only tick are
  now sent once a minute. A change of place on a board, a new viewer, a new stream and everything
  else still go out the moment they happen.
- **A game somebody starts on Steam shows about ten seconds sooner.** The notice that tells panels
  to read Steam statuses again used to wait ten seconds; the first change of a quiet spell is now
  told at once. There is still never more than one such notice in ten seconds.
- **Coming from 3.3.x or earlier?** 3.5.0 carries everything 3.4.0 changed, so read
  [Upgrading to 3.4.0](#upgrading-to-340) as well.

## New in 3.5.0

- **One account of who is live, in voice and wearing VillaNET's badge**, worked out once on the
  server and read by the panel and by Matty alike (the snapshot's `people`), so the two can never
  disagree about a red ring, a mic or a "Live now".
- **Answers that say "nothing has changed" in a few bytes.** `GET /api/v1/streams` and
  `GET /api/v1/accounts` answer a client that still holds the last answer with `304`.
- **`GET /api/v1/access`**: the one small question a player asks while it plays, in place of
  reading everything about the reader once a minute.
- **A written contract with the native app**, in `contract/`: what the server really answers, as
  files its own tests write and Matty's tests read, and what a client must do. See
  [Native apps (Matty)](building.md#native-apps-matty).

## Upgrading to 3.4.0

- **Nothing about Steam or Hytale happens until an admin switches it on.** **Show people's Steam
  status** and **Show people's Hytale names**, in the new **Steam** and **Hytale** sections of the
  settings, are off by default. Until then the server asks Steam and Hytale for nothing, no browser is
  sent to either, and nobody can connect. To use them, follow [Steam (optional)](up-next.md#steam-optional) and
  [Hytale (optional)](up-next.md#hytale-optional): Steam needs a Web API key made for this server's domain, and
  Hytale an application its staff approve, registered with VillaNET's Redirect URI.
- **The Twitch tool has left the footer bar.** Connecting and disconnecting Twitch are now on the
  **Twitch** row of **Connected accounts**, in the sheet the link key opens, which then reads **Your
  stream and accounts**. The row's mark is the same unaltered Glitch, and it says **Live now** in red
  while you are live on Twitch. Nothing else about Twitch changes: its switches, its connections and
  hours, its routes, what `/me` says about it and its `twitch_link` event are 3.3.0's. On a server
  with Twitch, Steam and Hytale all off, the link key's sheet is exactly 3.3.0's **Relink your
  stream**. See [The footer bar](panel.md#the-footer-bar).
- **The bar has five tools at most**, so a 304 px panel's stream title keeps a few of its words, the
  soundboard's speaker included, and the rule that left it only its pencil below 360 px is gone.
  "Stream title", small above it, is cut to the title's own width, so it never runs under the pencil.
- **Disconnecting from the lock screen and over WATCH ONLY is one line.** Somebody with only Twitch
  connected sees 3.3.0's **Disconnect Twitch**, unchanged. With Steam or Hytale as well, the line
  reads "Disconnect  Twitch · Steam · Hytale", naming what they have connected, and pressing a name
  turns the whole line into that service's own "Disconnect Steam? Press again" for four seconds. See
  [The lock screen](unlocking.md#the-lock-screen).
- **Profile popovers gain a Steam row and a Hytale row**, for people who can open the panel only:
  the first thing outside the panel that a locked reader does not get. See
  [Outside the panel](unlocking.md#outside-the-panel).
- **Routes, fields and events are added; none is changed or removed.** `GET /api/v1/accounts`
  (everybody's Steam status and Hytale names, for people who may watch);
  `POST /api/v1/{steam,hytale}/connect-ticket`, `…/app-connect` and `…/app-finish` (for linked
  people); `DELETE …/link` and `POST …/notice` (open to everybody); each service's Connect window
  and callback; and `/api/v1/admin/steam` and `/api/v1/admin/hytale` for the System Console. `/me`
  gains `steam` and `hytale`, and the WebSocket gains `accounts` and `account_link`. Today's Matty
  ignores all of it. See [Native apps (Matty)](building.md#native-apps-matty).
- **A settings backup carries the new settings.** The Steam Web API key and Hytale's Client Secret
  are secrets, left out unless you tick **Include the passwords and keys**. Who has connected which
  Steam or Hytale account stays in the plugin's store, and the file's `notIncluded` list names it.
- **Going back to 3.3.0** brings the Twitch tool back and leaves any Steam and Hytale connections in
  the plugin's store, unused and unseen, where 3.3.0 cannot delete them. Press **Delete all Steam
  data** and **Delete all Hytale data** first if they should not stay behind.
- **Coming from 3.2.x?** 3.4.0 carries everything 3.3.0 and 3.2.1 changed, so read
  [Upgrading to 3.3.0](#upgrading-to-330) as well, and from 3.2.0 or earlier the notes it points to.

## New in 3.4.0

- [Steam (optional)](up-next.md#steam-optional): people who have unlocked VillaNET can connect their Steam
  account, and everybody who can open the panel then sees their Steam name, whether Steam says they
  are online, busy or away, and the game they are playing, in their profile popover and, while they
  are in a game, on a **Playing** line under In voice.
- [Hytale (optional)](up-next.md#hytale-optional): people who have unlocked VillaNET can connect their Hytale
  account, and everybody who can open the panel sees the Hytale username they picked, with **Copy**,
  in their profile popover, so friends can find each other in-game. Hytale gives other apps nothing
  more: not whether somebody is playing, nor where.
- **Your stream and accounts**: the link key's sheet holds your stream, then **Connected accounts**,
  with Twitch, Steam and Hytale. See [The footer bar](panel.md#the-footer-bar).
- **One Disconnect line** on the lock screen and over WATCH ONLY, for everything somebody has
  connected; see [The lock screen](unlocking.md#the-lock-screen).
- **Steam connections** and **Hytale connections** in the System Console: where each service sends
  people back, whether it is set up, the last check, who has connected (with **Disconnect**, and
  **Disconnect and block**), who is blocked, and **Delete all**, all of it working whether or not the
  switch is on.
- [Native apps (Matty)](building.md#native-apps-matty): the documented API a native app such as Matty uses for
  all of this with its own session, connecting in the app's own sign-in window included.

## Upgrading to 3.3.0

- **Nothing to do.** 3.3.0 changes only how a clip plays in the panel and the System Console. No
  setting, route or stored document is added, changed or removed, and the server answers exactly as
  3.2.1's did; going back to 3.2.1 loses nothing.
- **Download has moved into the player.** With **Let people download clips** on, it is the arrow
  among the clip player's controls, not a pill after **Share**; what it says ("Preparing…", "Your
  download has started.", or why not) is on the line under the picture. Who may download, the
  streamers' switches and the ticket are 3.2.1's, unchanged; see
  [Downloading a clip](panel.md#downloading-a-clip).
- **The browser's own player is gone from clips**, and with it its menu: right-click on a clip has no
  **Save video as**. The System Console's **Watch** uses the new player too, still without Download.
- **A clip's timeline is the clip's own.** The browser's player showed the whole file, which starts
  on the keyframe before the clip's own start, so a 45-second clip opened at "0:01 / 0:46" and
  jumped back to its start when it ended. Now it reads "0:00 / 0:45", from the clip's exact start to
  its end, and stops there with **Replay**.
- **Coming from 3.2.0?** 3.3.0 carries everything 3.2.1 changed, so read
  [Upgrading to 3.2.1](#upgrading-to-321) as well: at its first start it deletes the biggest-fan
  opt-out list, and it adds **Let people download clips**, off. From 3.1.0 or earlier, read
  [Upgrading to 3.2.0](#upgrading-to-320) too.

## New in 3.3.0

- **Clips play in the same player as a live stream**, in the look chosen from three previews: the
  stream tile's frame and its line under the picture with the streamer's name and the time, the tile's
  controls over the picture while it is pointed at, a red scrubber along its bottom edge, **Replay**
  at the end, the tile's loading and "can't play" screens, and keys (Space or K, the arrows, M, F).
  See [Clips and voting](panel.md#clips-and-voting).
- **One Download, in the player**, in place of 3.2.1's pill.
- **Refusals under a clip are red.** Report's and Delete's "could not … just now" (and 3.2.1's
  Download refusals) were drawn in grey, like a neutral note.

## Upgrading to 3.2.1

- **Nobody can opt out of biggest fans any more.** At its first start, 3.2.1 deletes the list of
  people who had chosen not to be named (`fan_optouts` in the plugin's store), and its set-aside copy
  `quarantine_fan_optouts` if there is one, with a line in the server log saying how many people it
  held and naming nobody. Anybody whose hours 3.2.0 still owed a deletion has them taken out first.
  Everybody who had opted out is counted again **from now**, and only while **Show who is watching**
  is on, as before; what was deleted for them stays deleted. The switch in the Trophies card's **You**
  tab, the line in your own profile popover and the System Console's **Start biggest fans again** are
  gone. **Name each streamer's biggest fan** still switches the whole thing off for everybody. See
  [Biggest fans](awards.md#biggest-fans).
- **Downloads stay off until you switch them on.** The new **Let people download clips**, under
  **Clips**, is off, so nothing changes until an admin ticks it. With it on, linked people get a
  **Download** in the clip viewer, and every streamer gets a second switch in **Clips of your
  stream**, on until they turn it off. Every download is logged with who took which clip. See
  [Downloading a clip](panel.md#downloading-a-clip).
- **Two routes are new and two are gone.** `POST /api/v1/clips/download-ticket` and
  `GET /api/v1/clips/download` are for linked people only. `POST /api/v1/awards/fan` and
  `POST /api/v1/admin/fans/restart` now answer 404. `GET /api/v1/takebacks` answers only `sounds`;
  `GET /api/v1/awards/mine` no longer says `fanNamed` or `fansReadable`; `/me`'s `ledgerAlert` and the
  airtime status lose `fansOff`; and `/me`'s `clipsStreamer` gains `downloads` while downloads are
  allowed. A clip in `GET /api/v1/clips` says `canDownload`. Nothing in the panel needs anything from
  you for any of it.
- **A settings backup carries the new setting**, and its list of what it leaves out now names the
  streamers' download switches beside their clipping ones, and no longer names fan opt-outs.
- **Going back to 3.2.0** would forget the streamers' download switches, and would find no list of
  opt-outs, so nobody would be opted out.
- Upgrading from 3.1.0 or earlier? Read [Upgrading to 3.2.0](#upgrading-to-320) first: everything in
  it still applies, apart from the opt-out.

## New in 3.2.1

- **Clip downloads**, off unless an admin switches them on: a **Download** in the clip viewer for
  people who have linked their stream, never for people let in to watch, never on the cards shared
  in channels. The file is saved by the browser straight from this server, up to five minutes of
  clip, with a clean name such as `Dave - that jump - 2026-10-13 2105.mp4`. See
  [Downloading a clip](panel.md#downloading-a-clip).
- **A streamer's say over downloads**: **Let people download clips of my stream**, in **Clips of your
  stream**, which turns them off for every clip of their stream, those already made and every one to
  come, whatever the admin allows. See [For streamers](panel.md#for-streamers).
- **No biggest-fan opt-out.** See [Biggest fans](awards.md#biggest-fans).

## Upgrading to 3.2.0

- **Nothing beside anybody's name changes by itself.** Until somebody chooses otherwise in the
  Trophies card's new **You** tab, the badges beside their name are **Automatic**, which is exactly
  what 3.1.0 drew: the crown, this month's calendar, the Top Snooper's binoculars and the Best
  Clipper's clapperboard, whenever held. Biggest fan hearts, achievements and past months' medals
  show beside a name only when its owner picks them. See [Awards by your name](awards.md#awards-by-your-name).
- **VillaNET starts keeping who watched whom, for biggest fans.** **Name each streamer's biggest
  fan**, under **Airtime and awards**, is on by default. From the first refresh after 3.2.0 is
  enabled, and only while **Show who is watching** is on (when the Live now cards already show who
  is watching which stream), it keeps, all time, how many seconds each linked person has watched
  each linked streamer (`fans_totals` in the plugin's store). Nothing was ever recorded about this
  before, so everybody starts at zero, and nobody is named until they reach an hour. In 3.2.0
  anybody could choose not to be counted, which deleted what was counted for them; 3.2.1 takes that
  away. If you would rather not keep it at all, switch the setting off straight after enabling the
  plugin; with **Show who is watching** off nothing is counted either. See [Biggest fans](awards.md#biggest-fans).
- **Achievements arrive backdated.** **Give achievements** is on by default, and achievements are
  worked out from the hours, medals and thumbs up VillaNET already has, so at the first refresh
  everybody holds every step their history reaches: they show in profiles and on the Trophies card
  at once. Nobody is shown a burst of "New achievement" notes for them. Hours on the 24/7 channels
  named in **24/7 channel achievements** (out of the box, `villanet-watts = Alan Watts = 1, 10, 50,
  100, 250`) were never recorded before, so those start from zero. See [Achievements](awards.md#achievements).
- **The soundboard is off, and needs a look at Mumble before it goes on.** Switched on, it is the
  first time VillaNET's Mumble account sends audio. Before you tick **Let people play sounds into
  Mumble**, check what [Setting the soundboard up](voice.md#setting-the-soundboard-up) lists: that account
  must not log in as SuperUser, needs Speak where it sits and Whisper in your voice channels, and the
  server's bandwidth limit must allow 64 kbit/s. **Sounds on the server**, in the new **Soundboard**
  section, says whether it can play.
- **Two changes to the headphones, whatever is switched on.** The headphones now turn slate for
  somebody Mumble does not let speak in their channel, as well as for somebody muted or deafened. And
  only a Mumble name made of plain letters, digits, dots, dashes and underscores is matched to a
  username: a lookalike such as `alİce`, with a Turkish dotted I, used to match the username `alice`
  and now matches nobody, so it no longer shows headphones beside that account's name. Mattermost
  usernames are only those characters, so nobody's real match is lost; **Names that differ** still
  matches any name, compared exactly apart from the case of plain letters. See [Headphones for whoever
  is on Mumble](awards.md#headphones-for-whoever-is-on-mumble).
- **The System Console has a Soundboard section**, after Mumble, and **Airtime** is now **Airtime and
  awards**, with the new settings in it. A settings backup carries the new settings; who chose what,
  who opted out (until 3.2.1), the new hours and the sounds stay in the plugin's store and are named
  in the file's `notIncluded` list.
- **Two answers say more.** `POST /api/v1/schedule` answers the new plan's id beside `ok`, so the plan
  sheet can offer to share it, and `GET /api/v1/clips/cards` answers each card's thumbs up, whether
  the reader gave one and whether they may, so it is now per reader and never cached. Nothing in the
  panel needs anything from you for either.
- Upgrading from 3.0.0 or earlier? Read [Upgrading to 3.1.0](#upgrading-to-310) first: everything
  in it still applies.

## New in 3.2.0

- [Awards by your name](awards.md#awards-by-your-name): up to four of your awards, in your order, beside your
  name in posts, on your Live now card and beside "Clipped by", chosen in the Trophies card's **You**
  tab; or **Automatic**. A past month's medal can be chosen too, with a star where its place was.
- [Biggest fans](awards.md#biggest-fans): each linked streamer's biggest fan, in the streamer's and the fan's
  profiles, in a **Biggest fans** list on the Snoopers tab, as a quiet line on the streamer's Live now
  card, and as a gold heart the fan may choose to show. Not being counted was a switch in the You
  tab, and a line in your own profile popover, linked or not, until 3.2.1 took it away.
- [Achievements](awards.md#achievements): five families of coins, in profiles, on a new **Achievements** tab
  of the Trophies card, and in the You tab with how far you are to the next step; a quiet "New
  achievement" note in your own panel when you reach one.
- [Sharing a plan](up-next.md#sharing-a-plan): a plan posted into a channel as a card, from Up next or straight
  after saving one, with **Join** and **Watch** on it.
- [Soundboard](voice.md#soundboard), off by default: short sounds people in Mumble play into their own
  channel from the panel's footer bar, added from a sound file, the microphone or a VillaNET clip,
  with **Sounds on the server** in the System Console.
- [@names in stream titles](unlocking.md#names-in-stream-titles): `@dave` in a title opens Dave's profile card,
  where a hashtag opens a search.
- The thumbs up on a shared clip's card; see [Sharing a clip](panel.md#sharing-a-clip).
- The Trophies card's row of tabs scrolls on a narrow window, rather than cutting the last tab off.
- Your own profile popover offers your takebacks: deleting your sounds, and, until 3.2.1, not being
  named as a biggest fan. They work whether or not you are linked.
- The two headphone changes above.

## Upgrading to 3.1.0

- **Nothing about Twitch happens until an admin switches it on.** Both of its switches, **Show people
  live on Twitch** and **Play Twitch streams inside VillaNET**, are off by default. Until then the
  server asks Twitch for nothing, no browser is sent to Twitch, and nobody can connect. To use it,
  follow [Setting it up](up-next.md#setting-it-up) under Twitch: the one new step is adding VillaNET's OAuth
  Redirect URL to the Twitch application IGDB already uses.
- **The lock screen is much simpler.** It is the panel's emblem, dimmed, with a padlock on it, and
  the black bar for the token at the foot, where the panel's own bar sits, with one line over it that
  says something only when there is something to say. A line under the emblem says why the lock is up,
  when there is a reason. It no longer tells anybody that they can be let in, or whom to ask, and a
  system admin sees nothing extra on it: no way to let themselves in, no alerts, no set-up words. Those
  are in the System Console, and the alerts show in the panel once it is unlocked. **Who to ask** is
  gone from **Access**: a value saved for it is ignored, and a settings backup that has it restores as
  usual, listing it as a setting this version does not have. See [The lock screen](unlocking.md#the-lock-screen).
- **The airtime boards and medals rank people, not stream keys**, whether or not Twitch is on, so
  that somebody's Broadcast Box and Twitch time can share one row. On a server where each linked
  stream belongs to a different person this changes nothing you can see, except that two people on
  exactly the same seconds are now ordered by their names.
- **A chat bridge fix.** With **Bridge Mumble chat into a channel** on, every earlier release let
  anybody who could connect to the voice server ring every phone on the team, which the bridge's
  filter is there to stop. `@aLL` and `@all_` went through, because only three spellings
  of each word were defused; so did `@al&#108;`, because Mattermost decodes a character reference
  before it looks for mentions; and so did any message from somebody who called themselves `@all` in
  Mumble, because their name was not defused at all. From 3.1.0 none of them mentions anybody: see
  [What is filtered, and why](voice.md#what-is-filtered-and-why). There is nothing to do.
- **Title searches come only from the panel.** `GET /api/v1/media/search` now answers 403 "Search
  from the panel." to a request that does not carry `X-Requested-With: XMLHttpRequest`, which the
  panel always sends and an image in a post cannot. Before, a Markdown image pointing at it made every
  linked reader's browser search TMDB, Steam or IGDB in their name. For the same reason, being linked
  no longer lets somebody have the server fetch a picture for any title they name, only for the titles
  the panel shows them (see [Security posture](security.md#security-posture)). The panel asks for nothing else,
  so nothing in it changes; a script of your own that calls the search must send the header.
- **A save no longer takes a stream down early.** **Missed checks before a badge comes off** now
  counts only the poller's own checks, one each **Poll interval**. Before, a plan, What's on?, link
  or settings save made a poll of its own that counted as a check too, so a stream that dropped for a
  few seconds while somebody saved something could lose its badge, have its What's on? taken down and
  its Clips recording stopped. The same goes for the three failed reads after which the panel is
  emptied. There is nothing to do.
- Upgrading from 2.11.0 or earlier? Read [Upgrading to 3.0.0](#upgrading-to-300) first: everything
  in it still applies.

## New in 3.1.0

- [Twitch (optional)](up-next.md#twitch-optional): people who have unlocked VillaNET can connect their Twitch
  account. While they are live on Twitch they wear the live badge, have a Live now card with their
  Twitch title, category, viewers and picture, and their Twitch time counts on the airtime boards and
  medals, merged with their Broadcast Box time and never counted twice. With **Play Twitch streams
  inside VillaNET** also on, the stream plays in the panel in Twitch's own player. Somebody live on
  both gets one card, the Broadcast Box one, with **Also on Twitch ↗**.
- **Twitch connections** in the System Console: the OAuth Redirect URL to register, who has
  connected, Disconnect, block, 24/7, and **Delete all Twitch data**, all of it working whether or not
  the switch is on.
- Disconnecting Twitch is open to everybody who has connected it, from the lock screen too.
- A much simpler lock screen, the same for everybody; see [The lock screen](unlocking.md#the-lock-screen).
- The Clips row under the player is gone: the clapper opens Clips.
- The chat bridge fix and the title search fix above. Also: a stream key in Broadcast Box's reason
  for refusing somebody's playback is taken out before the reason is logged, as the Clips recorder's
  already was (see [Security posture](security.md#security-posture)); and the hashtag search forgets the people
  and channels it looked up when its reader signs out (see
  [Hashtags in stream titles](unlocking.md#hashtags-in-stream-titles)).

## Upgrading to 3.0.0

- **Upgrade Mattermost to 11.11 first.** 3.0.0 supports only the latest Mattermost, 11.11.0 and
  later; 2.11.0 ran on 9.0 and later. On an older server 3.0.0 will not start: Mattermost stops it
  with "plugin requires Mattermost 11.11.0". Until the server is upgraded, 2.11.0 is the release for
  it.
- **The panel is locked for everybody who has not linked a stream**, system admins included, from
  the moment 3.0.0 is enabled. Streamers who linked their stream with an earlier release are already
  unlocked and need do nothing. Everybody else sees the lock screen the next time they open VillaNET,
  and people with no bearer token can only watch once an admin lets them in. So straight after
  enabling, go to **System Console → Plugins → VillaNET → Access** and let in the people who should
  watch. See [Unlocking Matty](unlocking.md#unlocking-matty).
- **Clips is on as soon as 3.0.0 is.** The server starts recording every live stream that is not a
  24/7 channel at its first poll, keeping the last two minutes of each on its own disk — see
  [What the server records](panel.md#what-the-server-records). If you would rather decide first, switch
  **Let people clip streams** off under **Clips** straight after enabling the plugin: that stops
  every recording at once and wipes what was held.
- **Check two things on the Mattermost host before relying on it**: that it can exchange UDP with
  Broadcast Box, and that Mattermost's file storage is the local disk. See
  [When clipping does not work](panel.md#when-clipping-does-not-work).
- **A stream-key fix.** Every release before 3.0.0 passed Broadcast Box's answer to a playback request
  on to the viewer's browser as it was, and Broadcast Box v2.0.2 names the stream in that answer after
  its stream key (`a=msid`, and the `cname`, `msid` and `mslabel` of each `a=ssrc`). So anybody who
  watched a stream in VillaNET could have read its key out of their browser's developer tools, and a
  server log written with **Log WHEP negotiation** on holds the keys too. From 3.0.0 the plugin puts
  `villanet` in their place before anything sees the answer. What a known key gives anybody depends on
  Broadcast Box, not on the plugin: with every key reserved, `STREAM_PROFILE_POLICY=RESERVED` and port
  8080 open only to the Mattermost server and the proxy, as [LOCKDOWN.md](../LOCKDOWN.md) describes,
  nothing; with an unreserved key under the default policy, publishing to it. Changing a key makes it
  a new stream as far as VillaNET is concerned — its airtime stays filed under the old one and its
  streamer links again — so the fix to reach for is Broadcast Box's (see [Locking publishing
  down](unlocking.md#locking-publishing-down)). Delete old server logs written with **Log WHEP
  negotiation** on.
- **A second stream-key fix: the Pop out address.** With **Public watch URL** set, every earlier
  release put each live stream's watch address into the stream list that every signed-in browser is
  sent. That address is the public page followed by the stream key, so every key was in every browser,
  watching or not. From 3.0.0 the list carries no address. Pop out is a link through the plugin,
  offered only to people who have linked a stream, and it redirects to the watch page. See
  [Why the pop-out link defaults to off](unlocking.md#why-the-pop-out-link-defaults-to-off). If that setting was
  ever filled in, treat every key as known, as above.

## New in 3.0.0

- [Clips](panel.md#clips): clipping, the Clips list, upvotes, **Best Clipper**, sharing a clip into a
  channel as a card, and each clip carrying its stream's game, movie or show.
- The **Top Snooper**'s gold binoculars, for whoever is first on the all-time Snoopers board; see
  [The Top Snooper](awards.md#the-top-snooper).
- Games that are not on Steam, from IGDB through a Twitch developer application, and the titles and
  their cover art remembered across restarts; see [Movies and games](up-next.md#movies-and-games-optional).
- The [birthday list](voice.md#birthdays) in the System Console folds when it has more than four people.
- The header's wordmark is the VillaNET emblem and **VILLA**, with **NET** in red; see
  [Changing the artwork](panel.md#changing-the-artwork).
- **Banner placement**: the banner at the top only, or the top and the bottom; see [Banners](panel.md#banners).
- A tidier **System Console** page: sections in the order an admin sets things up, shorter help, and
  one look for the plugin's own controls; see [Settings](install.md#settings).
- The stream-key fix above.
- **The lock screen.** VillaNET is locked until somebody unlocks it with their bearer token, or a
  system admin lets them in to watch, in the new **Access** section, which can also unlink anybody's
  stream. A bearer token pasted into a message by mistake is refused before it is posted. See
  [Unlocking Matty](unlocking.md#unlocking-matty).
- **A tidier panel.** Clips open from the clapper in the header. The footer is one quiet bar
  whose small tools open sheets, one at a time. Upvotes are a thumbs up. Up next shows only while
  something is planned. See [The footer bar](panel.md#the-footer-bar).
- The Pop out fix above.
