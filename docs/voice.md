[The Matty Plugin manual](README.md)

# Voice and birthdays

## Who is in voice

One row at the top of the panel listing whoever is on the Mumble server, and — the part that makes
it worth having — **no row at all when nobody is in there**. (The Up next rail follows the same rule:
no row while nothing is planned.)
On the evenings voice is empty it costs nothing; on the evenings it is not, the names are already on
screen without anybody opening anything.

The row is grouped by Mumble channel: each channel somebody is in, in the order Mumble shows its
channels, with the channel's name over a line of the people in it. Channels nobody is in are left
out. Your own name is picked out, muted people are dimmed rather than hidden, with a crossed
microphone after the name, and deafened people have crossed headphones there instead. Each line
scrolls sideways rather than wrapping, so a busy channel is still one line. The panel only shows
who is where. Joining a channel and talking there is done from the Matty app, while an admin allows
it: see [Talking from Matty](#talking-from-matty).

### How it gets the names

The plugin holds **one Mumble client connection** for this, using [gumble](https://github.com/layeh/gumble).
It never moves from the channel Mumble puts it in, and it hears nobody: Mumble sends it the audio of
anybody who talks in the channel where it is parked (usually the root), and it drops that unheard. A
Mumble client is sent every channel and every user connected to the *server*, not only the ones
beside it, so the roster is the whole channel tree and everybody who is on, wherever they are.

With the soundboard on, it also **sends** audio: the sounds people play, shouted into their own
channel. See [Soundboard](#soundboard).

This connection, and the one more for each person [talking from Matty](#talking-from-matty), to the
same Mumble server, are the plugin's only outbound connections besides Broadcast Box itself — and,
only if an admin switches them on, the title lookups (TMDB, Steam and IGDB), Twitch live (Twitch),
Steam status (Steam), Hytale names (Hytale) and UnifiedPush [push notifications](push.md), to the
push servers people's phones name.

**Not built on Matterbridge**, which was the obvious idea and does not work. Its Mumble side turns
arrivals and departures into prose chat messages — *"Alice joined"* — for the one channel it sits
in, and its HTTP API carries messages only. There is no roster in it anywhere. Rebuilding one from
that event stream would leave every plugin restart knowing nothing, with no way to ask, and an empty
row on an evening when four people are in there is worse than no row at all.

### Things that would otherwise go wrong

- **The bridge is not a person.** Matterbridge sits in Mumble as `Mattermost`, and the plugin's own
  connection sits there too. Both are left out — the bridge by name in **Names to leave out**,
  the plugin automatically by whatever name it connected under. Without that the row would list them
  permanently and would never disappear, which is the only thing it is really for.
- **Unreachable is not empty.** An empty list means nobody is in voice, and that is only ever
  published when the plugin is actually connected and in a position to know. When it cannot reach
  Mumble the row simply is not there, and the reason is shown to **system admins only** — everybody
  else would get a warning about a server they can neither reach nor fix.
- **Mumble is chatty about things the panel does not show** — a comment, a texture, a channel's
  description. The roster is compared before it is published, so those change nothing and send
  nothing. A channel made, removed, renamed, moved or reordered is a change, and is sent.
- **A change the panel does show costs no poll.** Somebody joining or leaving, muting or deafening
  (muted people are dimmed), or the connection to Mumble dropping puts the new roster on the stream
  list as it was last sent, and sends that: Broadcast Box is not asked anything and nothing is read
  from or written to the database. Changes are sent at most once every 2 seconds, a run of them as
  one message, so somebody using mute as push-to-talk does not send every client a message a press.
  Before 3.1.0 each such change cost a whole poll of Broadcast Box.
- **A map has no order.** The roster is sorted, because an order that changed every poll would
  republish the snapshot for ever.
- **Talking-right-now is deliberately absent from the roster.** Knowing who is speaking means
  receiving the audio,
  which means joining the channel and taking the bandwidth. A name list that changes several times a
  second is also not a name list anybody can read. The Matty app shows who is talking only in the
  channel it is connected to, from its own voice connection.
- **Somebody here from Matty is their Mattermost account.** A person talking from the app is on the
  Mumble server under a name the plugin chose for them, `alice (Matty)`, and the roster shows them as
  the account that joined, with a phone mark, whatever the name rules say.

### What the connection costs you

Mumble servers use a self-signed certificate as a matter of course — the desktop client trusts them
on first use and remembers the fingerprint. There is no certificate authority to check one against,
so the plugin does not verify it, and that is worth stating plainly: on a network where somebody can
already intercept traffic, they could impersonate the voice server and collect the **Mumble server
password** if you have set one. Nothing else of yours crosses that connection — no Mattermost
credential, no stream key — and no audio goes out on it but, with the soundboard on, the sounds
people play.

The name matching marks your own row, puts the headphones by your name and, with the soundboard on,
decides into which Mumble channel your sounds play. It lets nobody act as anybody else in Mattermost,
which is why a name match is good enough here and is emphatically not good enough for stream keys;
what a name proves, and what it does not, is under
[What a Mumble name proves](#what-a-mumble-name-proves).

## Talking from Matty

Off unless an admin switches on **Let people talk from Matty** (System Console > Plugins > Matty >
Mumble). Then the Matty app (0.1.31 and later) lists the Mumble channels in a team's channel list,
as Discord lists its voice channels, and anybody who may watch can join one, talk, mute, deafen,
move to another channel and leave. The web panel stays read only.

The phone does not talk to the Mumble server. It opens one WebRTC connection, audio only, to the
Mattermost server, on one UDP port, and the plugin opens a Mumble connection of that person's own to
the Mumble server and passes the voice between the two as it is: Opus from the phone goes to Mumble
as the phone made it, and Opus from Mumble goes to the phone as Mumble sent it. Nothing is decoded,
mixed, recorded or kept.

### What it needs

- **Show who is in voice**, on and connected: a join is checked against the channels its connection
  sees, and talk is off while it is not connected.
- **One UDP port**, **Voice port (UDP)**, 8445 unless you change it, open to phones on the Mattermost
  server, for UDP. Every phone uses that same port. Behind a router, forward it to the Mattermost
  server, UDP, with the same number.
- **The address phones reach this server at**, **Address for phones**: below.
- **A single Mattermost server.** In a cluster, talk is off, and the app says "Talking from Matty
  works on a single Mattermost server only." A phone's voice reaches the one node whose address it
  was given, while its join, moves and leave are requests that any node may be handed, and each
  node holds only the Mumble connections it made; each node would also need an address and a port
  of its own.

Talk is on, and the app offers **Join Voice**, while all of that holds; otherwise the app shows who is
in each channel and no button, and `GET /api/v1/access` says why (see
[the contract](../contract/README.md#voice)).

### The port and the firewall

The port listens on every interface of the Mattermost server, IPv4 only. With `ufw`:

```sh
sudo ufw allow 8445/udp
```

Nothing else needs opening: there is no STUN or TURN server, and the phone needs no port of its own
open. Changing the port drops every phone for a moment; each one joins again by itself on the new
one. A port that cannot be opened (another program has it) leaves talk off, and system admins see
why in the panel and on the **Talking from Matty** card. A port outside 1024 to 65535 is not
replaced by another: talk stays off with "The voice port must be between 1024 and 65535.", in the
app, on the card and in the panel. While the port is down, talk is tried again after 30 seconds,
then twice as long each time, up to every ten minutes.

### The address

**Address for phones** is what each phone is told to send its voice to: one IPv4 address or host
name a line, all of them given to every phone, which uses whichever answers. Leave it blank and the
host of Mattermost's **Site URL** is used, unless that is this machine itself (`localhost`,
`127.0.0.1`), which a phone cannot reach: then talk stays off with "Set the address phones reach
this server at." A host name is looked up when talking starts and again every ten minutes; while
talk is up, a look-up that fails keeps the addresses it had, also when talk restarts for another
setting with **Address for phones** unchanged. IPv6 is not used: an IPv6 address leaves talk off
with a sentence saying so.

Behind a router, this is the public IPv4 address, or a name for it; on a home network where phones
are on the same Wi-Fi, the server's LAN address works too, and both can be listed.

### Names on the Mumble server

Each person talking from Matty is on the Mumble server as **their Mattermost username followed by
" (Matty)"**, such as `alice (Matty)`, so that Mumble's own users can tell who is on a phone. A
Mumble server whose rule for names does not take spaces or brackets is asked for `alice.matty`
instead, and once it has taken that, every later join is asked for under the dotted name first,
until the Mumble settings change or the plugin restarts. A name somebody registered on the Mumble server is told as `The
name "alice (Matty)" is registered to somebody else on the voice server.`, and the other name is
tried. A name is never the plugin's own (**Connect as**), one of **Names to leave out**, or another
phone's on this server, compared without regard to case: Mumble would throw whoever had it first
off the server. Such a join is refused with a sentence that names the name. The Mumble server's
own users are not checked: a Mumble user who connects without a certificate under a phone's name
throws that phone off the server, and a phone joining throws off a Mumble user without a
certificate who has its name, because that is how the Mumble server treats two connections of one
name that have no certificate.

These connections have no certificate, so they are never a registered Mumble user, and a channel
that only lets registered users in (or that needs a certificate) refuses them, with Mumble's reason.
They go through Mumble's ACLs as anybody unregistered does.

### What the Mumble server sees

For each person talking from Matty: one more client connection from the Mattermost server's address,
calling itself Matty and Mumble 1.3.0, with **Server password** (the same as the roster connection's),
over TLS. Voice travels inside that connection (Mumble's "UDP tunnel"), so no UDP is needed towards
the Mumble server. The person's own mute and deafen are the connection's self mute and self deafen,
and a Mumble admin can move, mute, kick or ban them as anybody else; the app is told. A phone that
loses its connection (a tunnel, a lift, a network change) leaves its Mumble connection in place,
deafened, for 45 seconds, for the phone to come back to; then it goes.

The voice goes up only while the person is talking, as Mumble's own voice activation does: from a
packet the phone marks as loud enough (its audio level, which the phone measures), until 400 ms
after the last one, so that a phone in a quiet room does not light up as talking all evening.

### The autoban, and the two ways round it

A Mumble server as it comes bans an address after too many connections from it, as its own settings
file has it: `autobanAttempts` (10) connections in `autobanTimeframe` (120 seconds) ban the address
for `autobanTime` (300 seconds), and with `autobanSuccessfulConnections` (true) the connections that
succeed count as well as those that fail. Every phone connects from the Mattermost server's address,
so a busy evening would get the plugin banned, roster connection and all.

So Matty opens **at most 8 connections to the Mumble server in any two minutes**, the roster
connection's included, and asks anybody who joins past that to wait a moment ("Too many people
joined voice just now. Try again in a moment."); the app tries again by itself. Each person may
make at most 2 joins that need a new connection in any two minutes ("You have joined voice too
often just now. Try again in a moment."), so that nobody can use them all up alone; a join whose
first name is refused opens two connections. A person whose phone comes back within the
45 seconds needs no new connection, and neither does one who joins again from another phone. There
are two ways round the autoban, both in the Mumble server's own settings file (`mumble-server.ini`
or `murmur.ini`):

- `autobanSuccessfulConnections=false`: connections that succeed are not counted;
- `autobanAttempts=0`: the autoban is off.

Then switch on **The voice server's autoban is off**, and the plugin's own limit counts but refuses
nobody. Leave it off while the Mumble server bans as it comes.

### Limits

- **Four voices at once to a phone.** A fifth person talking in the channel is not heard on that
  phone until one of the four has gone quiet for a moment.
- **50 people talking from Matty at once** on a Mattermost server, **6 joins a minute** a person,
  and **2 joins that need a new Mumble connection in any two minutes** a person (a join whose first
  name is refused opens two connections).
- A voice packet larger than 1 KB, or more than 100 of them in a second, is dropped.
- A session ends when the person leaves, when Mumble kicks or bans them or the connection to it is
  lost, when they join again from another phone, when talk is switched off or the Mumble settings
  change, when they may no longer watch, when the Mattermost session they joined from signs out
  (looked at once a minute), and when the plugin stops, restarts or is upgraded ("Voice is
  restarting on this server.", after which the app joins again by itself). The app is told which,
  in a sentence.

### The card and the alert

**Talking from Matty**, at the end of the Mumble section, shows whether the voice port is open and
the addresses phones are given, or why not; whether the Mumble server is connected; how many people
are talking from Matty through this server now (never who); whether the autoban is marked as off;
and the Mumble server's last refusal of somebody joining, with when. While talk is switched on and
the port is down, system admins also see why in the panel.

## Soundboard

Off unless an admin switches on **Let people play sounds into Mumble**. Then somebody who has linked
their stream and is in Mumble has a **speaker** at the start of the tools in the panel's footer bar,
and it opens the **Soundboard**, a sheet that rises from the bar like the others: tiles, each an
emoji over a short name, and the line "Plays in Lounge", naming the Mumble channel you are in.
**Press a tile and that sound plays into your channel**, for everybody there who is not deafened,
you included, and for nobody in any other channel. Anybody who would rather not hear them can mute
the plugin's Mumble account (its name is the Mumble section's **Connect as**, Matty unless an admin
changed it) in their own Mumble client: right-click it and choose **Local Mute**, or turn it down with
**Local Volume Adjustment**.

The plugin's own Mumble account does not move to play it: it stays where Mumble parked it, so nobody
sees it come and go, and shouts the sound into your channel from there.

- **One shared board.** Everybody it is for sees and plays every sound on it, oldest first, so tiles
  never move under anybody's finger; a new one goes at the end.
- **Only for people in Mumble.** The speaker is there only while you are; the server sends the board
  only to somebody linked whom it finds in Mumble at that moment, and refuses adding and renaming to
  anybody else. Let-in and locked readers never see any of it.
- **Muted means off.** While you are muted, deafened, or not allowed to speak in your channel, the
  tiles are grey and do nothing, and the sheet says why ("You’re muted in Mumble. Unmute to play
  sounds."); the speaker has a slash across it. The server reads your state from Mumble at the press
  and again before every 20 ms of the sound, whatever the panel shows: a press sent anyway is
  refused, and muting, leaving or moving channel in the middle of a sound stops it there.
- **A wait, and one at a time.** After your sound ends you wait 10 seconds (**Wait between
  sounds**): the tile you pressed sweeps round and counts the seconds down, the others dim, and the
  sheet says "Ready again in 7 seconds". Only one sound plays on the whole server at a time, then a
  quarter of a second of quiet; while somebody else's plays a thin bar in the theme's accent empties
  under the head and the sheet says "Another sound is playing." Nothing ever queues. At most 8 sounds a minute play on
  the server in all, and somebody pressing faster than the panel allows is held to 30 presses a
  minute. Presses sent at the same moment, from one person in several tabs or from five people, give
  exactly one sound.
- **Two sessions are one too many.** Somebody whom the plugin finds in Mumble twice is told to leave one
  before they can play. It can only happen through **Names that differ**, since Mumble never lets
  two people share a name.
- From the keyboard the grid is one Tab stop: the arrows move between tiles, Enter or Space plays,
  Home and End go to either end.

### Adding a sound

The last tile is **ADD**. A sound is a quarter of a second to six seconds, from any of three places:

- **Upload a file**: MP3, WAV, OGG, M4A, FLAC or WebM, up to 4 MB.
- **Record**: up to 6 seconds from your microphone. In the desktop app, Mattermost asks whether
  the page may use the microphone; choose **Allow**, and not **Deny Permanently**, which would take
  the microphone away from Calls too.
- **From a clip**: any clip you can watch, newest first, with a search past six. A long clip
  is cut through a 20-second window that a strip of the whole clip moves along.

Then the same steps for all three: **trim** it, dragging the two ends of a window that starts at the
first loud moment (from the keyboard, the arrows move an end by 10 ms and Page Up and Page Down by
100 ms), with **Listen** to hear it as it will be; pick an **emoji**, from your recent ones, forty
suggested for sounds, or Mattermost's own emoji search, custom emoji included when the server has
them; and give it a **name** of up to 16 characters, two lines on its tile. **Add to the board**
then makes it ("Making it…") and adds it ("Adding…"), and its new tile pulses once.

**The sound is made in your browser.** It is decoded there, trimmed, faded in and out over 5 ms,
levelled to the loudness a voice has in Mumble (−20 LUFS, as broadcasters measure it, then held under
−1 dB at its loudest), and encoded to Opus, the codec Mumble speaks, at 40 kbit/s in 20 ms packets,
exactly what Mumble's own client sends. That needs a browser whose WebCodecs can encode Opus at a
constant bitrate: the Mattermost desktop app, Chrome or Edge 119 or later, Firefox 130 or later, or
Safari 26 or later. Elsewhere ADD says "This browser can’t make sounds." and names those, rather than
sending something the server would refuse. Playing works in any browser.

Each person can have 16 sounds on the board, and the board 120 in all; adding is held to 20 an hour
each, and renaming, changing an emoji and deleting to 30 a minute.

### Your own sounds

Your own tiles have a small **⋯** when pointed at or focused (always, on a touch screen), and the
same choices on a right click or the keyboard's context menu key: **Listen here**, which plays it in
your browser only, **Rename**, **Change emoji** and **Delete** (two clicks). Under the grid, **Delete
all my sounds** (two clicks) takes every one of yours off the board at once.

Deleting your own is a takeback, so it works whatever else is true: after you have left Mumble or
unlinked, with the soundboard switched off, or after an admin has blocked you. Your own profile
popover offers **Delete my sounds** whenever you have any on the board, so it can be reached from
outside the panel, linked or not.

When somebody unlinks or their account is deactivated, their sounds stay on the board until they or
an admin delete them.

### Setting the soundboard up

**Check Mumble first.** Murmur's own rules decide whether anybody hears a sound, and a missing
permission is silence, not an error, so:

- **The plugin's Mumble account must not log in as SuperUser.** Mumble never lets SuperUser speak.
- **It needs Speak in the channel it sits in, and Whisper in your voice channels.** A sound is
  "shouted" into a channel through what Mumble calls a voice target, which needs Whisper there, and
  Mumble drops all audio from somebody who may not speak where they are. Both are Mumble's defaults
  unless an ACL takes them away.
- **The server's bandwidth limit must allow 64 kbit/s.** A sound uses about 53 with Mumble's own
  overhead; Mumble's default is far above that, but older configurations often say 72. Anything over
  the limit is dropped without a word, and a sound would be cut off.
- **Opus, not CELT.** Murmur falls back to the older CELT codec when fewer of the connected clients
  than its `opusthreshold` (100 % by default) can do Opus, and then drops Opus, which is all the
  soundboard sends. Every Mumble client since 1.2.4 does Opus.
- **One Mattermost server.** In a cluster only one node can hold the plugin's Mumble name, so a press
  that reaches another node is told "Matty can’t reach Mumble just now."
- It needs **Show who is in voice**, and the plugin's connection to Mumble, like the headphones.

**Sounds on the server**, in the System Console's **Soundboard** section, starts with a line from a
fresh look at the plugin's own Mumble session: whether it can play, or why not ("…is muted on the
Mumble server.", "…isn't allowed to speak where it sits.", "…has logged in as SuperUser, which
Mumble never lets speak.", a channel where it lacks Whisper, a bandwidth limit below 64 kbit/s), with
**Check again** at the card's foot to look afresh. The same line is shown to system admins in the panel once it is unlocked. Under it,
how many sounds there are and how much room they take, then every sound: its emoji and name, who
added it, how long, how big and how loud, when, with **Listen** and **Delete** (two clicks), and a
warning on a sound whose audio is missing or damaged. Then **Blocked from the soundboard**: a
username and **Block**, and the list with **Unblock**. Somebody blocked can neither add nor play, and
their sheet says "An admin has turned the soundboard off for you."; their sounds stay until deleted.
It all works whether or not the soundboard is switched on, and without a bearer token.

If the list of sounds ever cannot be read, the card says so, the board reads as empty and refuses
adding and playing, and a copy is kept in the plugin's store as `quarantine_sounds`. **Start the
board again** (two clicks) writes an empty one; the old sounds' audio is deleted when the plugin
next starts, which leaves time to put the list back by hand. Nothing is ever swept while the list
cannot be read, and never a sound added in the last ten minutes.

Every play is logged at Info, "Played a sound", with the Mattermost account's id and the sound's id;
so is every sound added and deleted. Never a sound's name, a Mumble password or a certificate.

### What a Mumble name proves

The soundboard finds you in Mumble the way the headphones do: by **Names that differ**, or by a
Mumble name equal to your username. That decides **into which Mumble channel your own presses play**,
nothing more: the press is made in Mattermost, by an account that has to be linked, and logged with
it. A name lets nobody act as anybody else in Mattermost.

- **Only plain names match a username**: letters, digits, dots, dashes and underscores, compared
  without case. A lookalike, such as `alİce` with a Turkish dotted I, the Kelvin sign for a K, or a
  fullwidth letter, matches nobody, because Mumble itself would let it in beside the real `alice`.
- **An unregistered Mumble name is first come, first served.** A stranger who takes "alice" while
  Alice is not in Mumble makes Alice's panel offer her the soundboard, and lets Alice play into
  the stranger's channel. While Alice is in Mumble under that name, nobody else can take it.
- **Registering proves less than it sounds.** On a default Murmur anybody may register a name nobody
  has registered yet. **Only registered Mumble users** offers the soundboard only to registered names,
  which helps once your people have registered their own (or you have taken **SelfRegister** away from
  `@all` in Mumble's root channel), because a registered name cannot be used without its certificate
  or password. It is off by default.

### How a sound gets into Mumble

The plugin's server side cannot make Opus: Mumble's Go library encodes it only through C code, and the
plugin is built without any. So the browser makes the Opus (above), and the server **checks** it:
every packet must be one 20 ms mono Opus frame of at most 128 bytes, 13 to 301 of them, and every one
is decoded, with `github.com/pion/opus`, a decoder written in Go, inside a guard that turns a decoder
crash into a refused sound rather than a stopped plugin. The decoded sound is held to a loudness
ceiling, so a sound made by hand rather than by the panel cannot be ear-splitting: its level as a
whole, and its loudest 400 ms, measured with broadcasters' K-weighting so a deep sound is not
mistaken for a loud one, must stay well under what the panel's levelling could ever make, and a sound
must not be silent. The emoji is looked up by the server itself, and a name is cleaned of the
author's own bearer token and stream key. A sound is kept in the plugin's store (at most 40 KB, about
5 MB for a full board), not on disk.

To play one, the server writes the stored packets onto the plugin's Mumble connection one every 20 ms,
numbered as Mumble's own client numbers them, the last one marked as the end, to a voice target that
shouts into the presser's channel. Pacing them in real time keeps them under Mumble's bandwidth
meter. The panels of everybody in voice are told when a sound starts and ends, so their tiles wait,
and nothing about which sound or whose.

## Mumble chat, in the same channel

The same connection that reads the voice roster also carries text chat, both ways, so the plugin
replaces a Matterbridge instance rather than sitting beside one. Turn it on with **Bridge Mumble chat**
(System Console > Plugins > Matty > Mumble chat bridge) and name the channel in **Channel** as
`team/channel`.

The bot is the plugin's own — created through `EnsureBotUser`, so there is **no access token to mint,
store or rotate**. One credential fewer than a separate bridge, not one more.

**Relayed messages carry the sender's name in the text**, and that is not a stylistic choice. A
plugin cannot post as somebody else: Mattermost's `SanitizeProps` strips `override_username` and its
siblings from every locally-originated post and re-injects them only for incoming webhooks —
explicitly not for bots, OAuth apps or plugins, because "there's no legitimate per-post override use
case for them". The plugin API exposes no webhook functions at all. Matterbridge does exactly the
same when configured with a token rather than a webhook URL.

### Running it alongside your old bridge

**Name the old bridge's Mattermost account in "Accounts to ignore" before turning this
on.** This is the one setting that matters, and the reason is worth reading once.

Matterbridge's recommended Mattermost setup is an *ordinary user account* with a password or a
personal access token — not a bot account. Mattermost marks bot posts with `from_bot` and webhook
posts with `from_webhook`; it marks an ordinary account's posts with nothing. So the plugin cannot
tell Matterbridge from a person, and relaying its posts back to Mumble — where it reads them again,
and posts them again — is an **endless loop**, not a doubling.

As a backstop for the day somebody forgets, the plugin stops relaying for a minute if it ever sees
more than thirty messages in one, and says so in the log.

### What is filtered, and why

- **Private Mumble messages are never published.** A message addressed to the plugin's own
  connection rather than to a channel is dropped: reposting it would publish something somebody
  deliberately said to one listener.
- **Relayed text cannot mention everybody.** `@all`, `@channel` and `@here` are defused with a
  zero-width space — otherwise anybody who can connect to the voice server could ring every phone
  on the team without having a Mattermost account at all. `@alice` still works, because that is a
  person talking about a person. The three words are found as Mattermost finds them, not from a list
  of spellings:
  - **In upper or lower case, or any mix.** Mattermost lower-cases a word before it compares it, so
    `@aLL` rings every phone just as `@all` does.
  - **Wherever Mattermost thinks the word ends.** It takes a trailing `_`, `.`, `-` or `:` off a word
    first, so `@all_` and `@all:` are `@all`. Only a letter or a digit carries the word on: `@allison`
    is somebody's name and is left alone.
  - **After Mattermost has decoded the Markdown**, which is when it looks for mentions. There
    `&#108;` is an `l` and `&#64;` an `@`, so `@al&#108;` and `&#64;all` are `@all` by then. A
    character reference typed in Mumble (`&#64;`, `&#108;`, `&commat;` and so on) is therefore shown
    exactly as it was typed rather than decoded. Inside `` `code` ``, where Markdown decodes nothing,
    its `&` shows as `&amp;`.
- **The sender's name cannot mention anybody.** It goes into the post as the bold part of
  `**name**: message`, and Mattermost reads a mention straight through the bold, so a Mumble user
  called `@all` made the bot post a real `@all` with every message they sent. Every `@` in a name is
  defused, `@alice` included — a name says who is talking, and never needs to mention anybody.
- **Relayed text cannot become a picture or a disguised link.** Markdown typed in Mumble, such as
  `![ ](https://tracker.example/p.png)` or `[https://chat.example.com](https://elsewhere.example)`,
  would otherwise be drawn by every reader's client, which fetches the picture straight from where
  it is. A zero-width space between `]` and `(` keeps the text reading as typed and inert. A bare
  address still becomes a link, and that shows where it goes.
- **A relayed message is one line.** Mumble chat is HTML and can carry newlines, and a newline
  followed by another bold name is a post that looks like two relayed messages — the second from
  somebody who never said it.
- **Mumble chat is parsed as HTML, not pattern-matched.** It is untrusted input from anybody who
  can connect, a pasted image arrives as a `data:` URI that can run to megabytes, and a regular
  expression over hostile HTML is a bug waiting for the right message. Images become `[image]`.
- **What goes to Mumble is bounded in bytes, not characters.** Murmur measures its limit in bytes
  and the text is HTML-escaped on the way out, so a line of emoji can be several times its own
  length by the time the server counts it — and over the limit murmur rejects the message rather
  than truncating it.

## Birthdays

Replaces a cron job and a script holding its own bot token. The list is edited in **System Console >
Plugins > Matty > Birthdays**, which is behind `manage_system` — so it is admin-only by construction
rather than by a check, and there is no endpoint and no panel surface that could reach it.

It is stored as JSON in the same shape as a `birthdays.json` file, so an existing list pastes
straight in and nothing is trapped in the editor. Somebody with an account needs a username;
somebody without one — a child — needs a parent instead, and the greeting is addressed to the
parents. One parent or two, as a string or a list, exactly as the script accepted.

A list of more than four people opens folded, as one line over it: how many are on it, whose
birthday is next and what they are turning, and how many rows need fixing. **Show the list** opens
it, and a second **Hide the list** under a long open list folds it again without scrolling back up.
**Add somebody** works folded too: it opens the list with the cursor in the new row. It always
starts folded rather than remembering how it was left, so getting past it to the settings underneath
never means scrolling through every birthday first.

**The interesting part is posting exactly once.** A greeting that arrives twice is worse than one
that arrives late, and there are two ways for it to double: the poller asks "is it anybody's
birthday?" some five and a half thousand times a day, and Mattermost in high availability runs this
plugin on every node at once. Both are answered the same way — the date is claimed with a
compare-and-set that only succeeds if nobody has written it yet, and only the node that wins the
write posts anything.

Which day it is depends on where you are standing, so there is a timezone setting; blank means the
server's own, which is what cron used. The timezone database is compiled into the plugin, so this
works on a host that has none. A list that will not parse is reported to admins in the panel, because
a broken list looks exactly like a list with no birthdays in it today.

29 February works as a birthday and fires only in leap years, which is the behaviour the script had.

### A cake next to their name

**Put a cake by their name** gives whoever has a birthday a 🎂 custom status for the whole day
in the birthday time zone — midnight to midnight, not from the hour the greeting goes out. It is their
birthday from midnight whatever hour an admin would rather the channel found out.

It is the same borrowed slot as the live badge, because Mattermost has exactly one custom status
per person, and **the cake wins**: somebody streaming on their birthday is having a birthday. That
precedence is decided in one place on purpose. Two independent writers — one for live, one for
birthdays — would each read the other's emoji as the person taking their own status back, and
somebody who was both would have their status rewritten twice every fifteen seconds for ever.

Only people with a **username** in the list can be given one: a custom status belongs to an
account, and the children in the list do not have one. Their parents are not badged either; it is
not their birthday. Whatever was in the slot first is handed back when the day ends, exactly as it
is when a broadcast does.

### Why @all did not notify anybody before 2.0

A greeting that says `@all` arrived, until 2.0, as plain text that rang nobody's phone.

The permission for `@all`, `@channel` and `@here` is `use_channel_mentions`, and the built-in roles
that grant it are `channel_user`, `channel_guest` and `team_user`. **`system_user` — which is all a
bot has by default — does not.** Mattermost checks it against the *post's author*, so a bot that is
not in the channel posts perfectly well and its `@all` reaches nobody.

The plugin did try to put its bot in the channel. But channel membership needs **team** membership
first, and `AddChannelMember` does not add it — so on a server where the bot had never been put on
a team by hand, every attempt failed, at debug level, where nobody was looking. 2.0 joins the team
first and logs a warning that names the consequence if either step fails.
