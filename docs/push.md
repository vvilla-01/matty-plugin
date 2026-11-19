[The Matty Plugin manual](README.md)

# Push notifications

## What it is

Matty, the Android app, can notify people of new messages while it holds a connection to this
server, which on a phone means keeping a background service running. With push notifications it
need not stay connected. The phone has a **UnifiedPush app** (a "distributor", such as
[ntfy](https://ntfy.sh)), which keeps one connection of its own to its **push server** for every app
on the phone that uses it. Matty asks that app for a push address and gives it to this server; the
server sends a push there whenever Mattermost decides to push to that person; the push server hands
it to the UnifiedPush app, which wakes Matty, and Matty shows the notification without connecting to
this server.

The server sends only when Mattermost itself decides to push, so every rule people already have in
Mattermost holds unchanged: their notification preferences, per channel and per thread, their
status (Do Not Disturb, out of office, and whether they want pushes while online, away or offline),
muted channels, collapsed threads. What a notification says follows Mattermost's own push notification
contents setting, as the official Mattermost app's do (below).

Each push is end-to-end encrypted (Web Push: RFC 8030, with RFC 8291's `aes128gcm` and VAPID, RFC
8292), so only the phone it is for can read it. Nothing about the streams, the panel or anything
else of the plugin's is ever pushed: only Mattermost's own notifications, and the plugin's few
messages about the registration itself (below).

It needs Matty 0.1.28 or later, and is off by default on both sides: an admin switches it on for
the server, and each person switches it on in Matty, under **Settings > Notifications > Push
notifications**.

## Switching it on

Two switches must be on, the plugin's and Mattermost's.

1. **The plugin's.** **System Console > Plugins > Matty > Push notifications > Send push
   notifications to Matty**. Off, no Matty can register, and nothing is sent, not even what was
   waiting to go; registrations already made are kept, and used again once it is back on (the daily
   sweep still drops those whose session is over, without a word to their phones).
2. **Mattermost's own.** **System Console > Environment > Push Notification Server**, where
   **Enable Push Notifications** must be anything but "Do not send push notifications". Only then
   does Mattermost decide to push to anybody, and only then does the plugin hear of it. If that page
   names one of Mattermost's own hosted push servers (`push.mattermost.com` and its kin), Mattermost
   pushes only with a licence that includes them; without one it pushes to nobody, Matty's people
   included.

While the plugin's switch is on and Mattermost pushes to nobody, system admins see "Push
notifications need attention:" among the panel's alerts, with Mattermost's reason and where to put
it right, and the card under **Status** says the same. Matty tells its people "Push notifications
are switched off on this server." or the same reason, and offers nothing.

**With Mattermost's push notifications on, Mattermost itself also sends**, for anybody signed in
to the official Mattermost app, to the push notification server named on that page. Matty's pushes
do not go through it and do not depend on it: a server whose people all use Matty can leave that
server as it is. Mattermost sends to it only for sessions of its own app, which Matty never
registers.

Registering is for anybody signed in, whether or not they have unlocked the panel. It is not exempt
from the [app check](security.md#the-app-check): set to **Check, and refuse a Matty that fails**, it
leaves a Matty that has not passed unable to register. Taking a registration back always works.

Then, in Matty: **Settings > Notifications > Push notifications** on, and a UnifiedPush app chosen
(Matty offers the ones installed, and says where to find one if there are none). Matty registers for
each saved server that offers push notifications, and says, for the server in view, how it stands:
"Working through <app> (<push server>)", with **Send a test**, or what is wrong. While it works,
**Stay connected in the background** is no longer needed for that server, and Matty says so.

## What is sent where, and who sees what

**When a phone registers.** Matty sends this server, for its own Mattermost session, the push
address its UnifiedPush app gave it, the phone's public key for this server's pushes and a 16-byte
secret that goes with it, and this server's push key, which it registered with. The server keeps
them, then sends a **check message** to the new address about three seconds later, with a random
number in it, and Matty sends that number back. Only a registration whose check came back is ever
sent anything else, so a push goes only to an address that the person's own phone reads. One not
confirmed within ten minutes is dropped. A check message is tried twice, two seconds apart, even
while its push server is paused for other pushes (below), so that Matty hears of one that cannot be
delivered, to an address its push server says is no more as well, while it still waits for it:
within about half a minute, unless the server is already sending as many pushes as its limits
allow. One that fails for good is in the server log as "A phone's first push could not be
delivered", with the push server's host and why. A push server that answers `507` has nobody
listening at the address yet, as ntfy answers until the phone's ntfy has connected: then the check
message is tried again after 2, 5, 10 and 20 seconds and then once a minute, for up to ten minutes
from the registration, and the registration waits meanwhile, so that it works by itself once the
phone's UnifiedPush app connects. The server log says so once for each check message, as "A push
server has no subscriber for a phone's push address yet, so its check message is tried again until
the phone's UnifiedPush app connects, for ten minutes at most", with the push server's host. A
check message is not tried again once its registration is confirmed, replaced by a later one, taken
back or more than ten minutes old. Matty sending the same registration twice within
ten seconds makes one check message. A person can have eight phones registered at once; a ninth
takes the place of the one heard from least recently.

**Whenever Mattermost decides to push** to somebody with a registered phone, the server encrypts
what Mattermost's own push carries and sends it to each of that person's phones. When they read
the conversation elsewhere, a second push says so, and Matty takes the notification away.

| Who | Sees |
| --- | --- |
| This server | The push address, the phone's public key and the secret that goes with it. What it pushes is what Mattermost would have pushed anyway. |
| The push server | This server's internet address; the **Contact for push servers**, when one is set; the User-Agent `MattyPlugin`, without a version; a token, signed with this server's push key, that names the push server's own address and lasts 12 hours; this server's public push key, which comes with every push and is the same for every phone, so that a push server can tell which phones belong to the same Mattermost server; when each push is sent; and its size, which is one of three (1,126, 2,150 or 3,174 bytes) whatever it says. Never what it says. |
| The UnifiedPush app on the phone | The same encrypted bytes, when they arrive. ntfy keeps each push it received, still encrypted, with when it came, on the phone for a month by default. |
| Matty on that phone | The notification, decrypted with keys that never leave the phone. |

**What a notification says** follows Mattermost's push notification contents setting (**System
Console > Site Configuration > Notifications > Push Notification Contents**). With full contents,
the message is in the push; with a generic description, only a sentence of Mattermost's naming the
sender and, unless the setting leaves it out, the channel; with contents fetched from the server on
receipt (which Mattermost uses only with a licence that has it, and otherwise sends the generic
description), the push carries only the ids and Mattermost's generic sentence, and Matty fetches the
message from this server itself. A message too long for one push is cut on a character boundary and
ends in "…".

**What each push is**: one `POST` (HTTPS, or plain HTTP to a listed push server) of at most 4 KB to
the address the phone registered, exactly as it registered it, with `TTL: 86400` (the push server
may keep it a day for a phone that is away), `Urgency: high` and `Content-Encoding: aes128gcm`.
ntfy reads neither `TTL` nor `Urgency`: it keeps a push for as long as its own server is set to,
12 hours by default. Every kind of push looks the same to the push server. Its plaintext is spelt
out in [contract/README.md](../contract/README.md#push-notifications), "Push notifications".

**What the server keeps**, in the plugin's key-value store, never in a settings backup: the
registrations, one per Mattermost session (the push address, the phone's public key and the secret
that goes with it), each lasting as long as its session (the daily sweep writes a person's
registrations again, once they are 30 days old, while a session of theirs stands; a registration not
written for 90 days is gone, which takes the plugin being disabled for about two months); a list of
who has one, so that every node knows whom to send for; and this server's push key, made on the
first start and never shown, logged or sent anywhere. The push address and the phone's secret are
what must stay private: anybody who has the address can send to it, and with the secret too, send
pushes that phone shows. So neither is ever logged or shown, not even to admins. See [What the
plugin touches](../LOCKDOWN.md#what-the-plugin-touches) for the keys' names.

**Limits.** The server sends at most one push at a time to each address a phone registered (a check
message goes beside what is sent to the address it is to replace), two to a person and four to a
push server, and eight in all; of the eight, at most four go to people one of whose pushes took two
seconds or more in the last ten minutes, so that slow push servers hold up at most half the sending.
A push server that many people use takes its four at a time from whoever has pushes waiting for it,
slow or not: there the half holds for the eight, not within that push server. A phone gets
at most 30 pushes in a burst and then one every two seconds; more than that are not sent (the push
that takes a notification away once it has been read is not counted). At most 20,000 pushes wait to
go on a node, and more are not sent. A push that may go through later (the push server answered
`429` or `5xx`, or did not answer) is tried again after 2, 10 and 60 seconds and then once a
minute, and no try is planned for later than five minutes after it was made: at 2, 12, 72, 132, 192
and 252 seconds when nothing else holds it up (one held up by a pause, below, can go a little
later). One that has not gone through by then is not sent, and Mattermost does not push it again. A check message and a test push are tried once more, after 2 seconds (a check message
answered `507` for longer, above), and the last push to a phone whose session is over three more
times, after 2, 10 and 60 seconds. At most 2,000 pushes wait to be tried again on a node, 256 of
them one person's, and more are not tried again; a check message always waits. A `Retry-After` (up
to a minute) pauses the phone it answered for: that phone is sent nothing but check messages
meanwhile, and everybody else's pushes to that push server go on. A phone that goes on being
answered so is taken to be at a limit of its own, as with ntfy's limits by subscriber, and pauses
only itself. The push server itself is paused, for everything but check messages, only when two
people's phones are answered so one after the other, each for the first time since a push to it
was delivered (or in an hour, or since it registered again), the second while the first's pause
still runs and with no push delivered to that push server in between; then until the later of the
two pauses ends. So when a limit is the push server's own for this server, it is paused at first,
and after that each phone waits out its own `Retry-After`. A `Retry-After` on a check message
pauses nothing. A push server that has not
answered three pushes in a row, each waited on two seconds or more, for at least two people, is
sent nothing but check messages for a minute. One person's own pushes, however many phones they
have, never pause a push server that others use.

## Your own push server, and the list

A phone's UnifiedPush app chooses the push server, not this server: ntfy, as it ships, uses
`ntfy.sh`, and anybody can point theirs at a push server of their own. The server sends to a push
server on the internet with nothing to set. It refuses a push address that is not `https://`, has a
user name or a fragment in it, or is longer than 1000 bytes; when it registers, one at `localhost`,
a name under `.localhost` or an address written out that it does not send to, unless that push
server is listed; and, at every connection, after the name is looked up, an address that is not on
the internet: nothing on the Mattermost machine, your LAN or any other private or special range. See
[the outbound rule](../LOCKDOWN.md#what-the-plugin-touches) for the whole of it.

**Push servers on your own network** is for a push server that runs on your LAN, or on the
Mattermost machine itself: one `host` or `host:port` a line, with no `https://` and no path, such as
`ntfy.home.lan` or `192.168.1.30:8080`. A listed push server may be at a private address, and may be
reached over plain `http://`. A line without a port means the usual one, 443 (or 80 for `http://`),
so list the port when it is another. The Mattermost machine itself is reached only when its line
says so: `localhost` (or a name under `.localhost`) or a loopback address. Even a listed push server
is never reached at a link-local address (`169.254.0.0/16`, where a cloud's metadata service usually
is, or `fe80::/10`, and `169.254.0.0/16` through NAT64, `64:ff9b::a9fe:0/112`), an unspecified,
multicast or broadcast one, `0.0.0.0/8`, or a cloud's metadata service elsewhere: `fd00:ec2::254`,
`100.100.100.200`, or `100.100.100.200` through NAT64 (`64:ff9b::6464:64c8`). Names are compared as
the server writes them: lower case, with no trailing dot, a name that is not ASCII in its `xn--`
form, and an IPv6 address in brackets in its shortest form. Up to 100 lines are kept. A line that is
not a host or host:port is left out, and the server log and the card say which line.

**Only the push servers listed above** refuses every push address anywhere else, public push servers
included, so that pushes go only where you chose. People whose UnifiedPush app uses another push
server are told, when they register, that this server sends pushes only to the push servers its
admin listed. Once their push server is listed, they press **Try again** in Matty; the same goes for
a push address refused for `http://` or for its address, once you list its push server.

**Contact for push servers** is an optional `mailto:` or `https:` address, sent with every push so
that whoever runs a push server can reach you about this server's pushes. Every push server people
choose sees it. Anything else is left blank.

The firewall in front of the Mattermost server must let it reach the push servers your people use:
see [the outbound rule](../LOCKDOWN.md#what-the-plugin-touches).

## The card

**System Console > Plugins > Matty > Push notifications > Status** is read from the server each time
the page opens, or with **Refresh**. It says:

- **Whether pushes can go out**: the plugin's switch, whether this server's push key is ready, and
  Mattermost's own push notifications, with where to switch them on when they are off.
- **How many phones of how many people**, and in all how many are working, waiting for their first
  push (registered, the check message not answered yet), failing and refused.
- **One row per push server**, by its host: its phones, how many work, wait, fail (and since when)
  or are refused, flagged **Refused** or **Failing** when any are. **Refused** means that push server
  answered `401` or `403`, that this server may not send there; such a phone is then tried with one
  of its pushes an hour at most, and works again by itself once one goes through. **Failing** means
  it could not be reached or did not take the pushes: after ten failures in a row, or an hour of
  nothing else with at least three, a phone counts as failing (until then, as working), is tried
  with one of its pushes at most every ten minutes, and works again by itself once one goes
  through. For either, Matty also registers again
  once, at its next start, says what is wrong, and offers **Try again**.
- **Not sent since the plugin last started on this server**, by why: made by another plugin (Calls'
  pushes are not sent), more than the queues hold, too many at once for one phone, too many waiting
  to be tried again, waiting when push notifications were switched off, or the push records could
  not be read. In a cluster these are the counts of whichever node answered.
- **More than 5000 people have registered a phone**, when they have: the counts are then of the most
  recent 5000, and every node reads the registrations of everybody Mattermost pushes to, since the
  list can no longer say who has one.
- **Lines of Push servers on your own network left out**, when there are.

It never says who uses which push server, or anything else about one person: which push server
somebody uses is theirs to know.

## A lost phone

**Log its session out in Mattermost**: the person, in **Profile > Security > View and Log Out of
Active Sessions**, or a system admin, by revoking that person's sessions. Signing out of Matty on the
phone does the same, and takes the registration back as it signs out.

Before every push the server reads that phone's Mattermost session, and sends nothing to one that
has ended or cannot be read. So from the moment the session is logged out, nothing more goes to the
phone. One that was logged out can no longer be read, but Mattermost answers a session it could not
read just then the same way, so the server reads it once more about a minute later, and drops the
registration only if it still cannot be read while other sessions it saw lately can be. So about a
minute after the first push that finds the session gone (for a quiet account, within a day: a daily
sweep checks every registration), one last push tells that phone's Matty; for a session that has
expired, at that first check. While Mattermost cannot read sessions at all, nobody is taken as
signed out: nothing is sent to a phone whose session cannot be read, the registration stays, and the
next push for that person, or the sweep, looks again, and when its first look was more than about
two minutes before, starts afresh and reads it once more a minute later (the server log says
"Sessions could not be read just now, so no phone was taken as signed out" when it tried others, at
most once in ten minutes). Matty takes every notification of this server off the phone at once,
then asks this server about its session, and signs out of it, saying to sign in again, when
Mattermost answers twice, a second apart, that the session is over; a phone that cannot reach the
server just then asks again when Matty next comes to the front, when the network comes back while
Matty runs, or at its next start. If the session is still there after all (it could only not be
read for a while), Matty registers again and shows nothing. That last push arrives when the phone is
next online, if that is within as long as its push server keeps a push for a phone that is away (a
day at most, as the server asks; ntfy keeps a push 12 hours by default, and, with
`visitor-subscriber-rate-limiting` on, only for a phone whose ntfy was connected within the last
day). Until then, notifications already on its screen stay there.

What a lost phone's screen can show is what Mattermost's push notification contents setting
allowed; a generic description keeps message text off it.

## When pushes do not arrive

- **ntfy refuses this server's pushes** (the card's row says **Refused**). An ntfy server
  that does not let anybody publish without signing in must allow anonymous publishing to the topics
  UnifiedPush uses, which all start with `up`. ntfy's documentation gives
  `ntfy access '*' 'up*' write-only` for this. Once it does, each phone works again by itself, but
  only with its first push an hour or more after it was refused (a refused phone is tried with one
  of its pushes an hour at most): the pushes before that are not sent and not kept, so press **Try
  again** in Matty once ntfy lets them in.
- **ntfy slows this server down** (`429`). ntfy limits how many requests one address may make, and
  this server is one address publishing for all of its people. On an ntfy server of your own, list
  the Mattermost server's address in ntfy's `visitor-request-limit-exempt-hosts`, or switch on its
  `visitor-subscriber-rate-limiting`, which for UnifiedPush topics counts the requests against the
  phone that subscribes rather than the address that publishes. With that on, the limits are each
  phone's own, and a phone at its limit holds up only its own pushes (**Limits**, above, says when
  a `Retry-After` pauses a push server for everybody); ntfy then also answers `507` for a phone whose
  ntfy has not connected yet, which the server waits for (**When a phone registers**, above). New
  topics count against this server's address whatever ntfy's admin exempts: ntfy allows 100 at
  once, then one more a minute. Whether a public ntfy server, ntfy.sh among them, has
  `visitor-subscriber-rate-limiting` on is for whoever runs it to say; a person can choose another
  push server in their UnifiedPush app. ntfy's own documents say that ntfy.sh allows 250 messages
  a day and bans addresses that send too fast. Matty uses one push for a
  notification, one more when it is read elsewhere, and one for each post a person writes while they
  have a phone registered (Mattermost clears the writer's own phones).
- **Another plugin cancels them.** Mattermost asks each plugin in turn, in an order that is not
  fixed, before it pushes, and a plugin that cancels a push stops it there, for the official app as
  for Matty: Matty Plugin is then never told of it. Matty Plugin itself never changes or cancels
  Mattermost's own pushes. A plugin that filters push notifications is the one to look at.
- **The plugin restarted.** Pushes waiting to go, or to be tried again, are kept in memory only:
  what was queued when the plugin stopped, restarted or was upgraded is lost, and Mattermost does
  not push it again. Pushes for messages after the restart go as usual.
- **The push server could not be reached.** A push is tried for five minutes (**Limits**, above);
  one that still cannot be handed over then is not sent, and Mattermost does not push it again. The
  server log says "A push could not be delivered", with the push server's host and why, when a
  phone's pushes begin to fail.
- **Mattermost decided not to push.** Somebody active in that very channel just now, on Do Not
  Disturb, or with push notifications for that channel set to mentions only (for a message that does
  not mention them) gets none, from Matty or the official app alike. A phone with Matty open on that
  conversation shows nothing either.
- **Matty's data was cleared on the phone.** Matty no longer has the keys to read the pushes, or the
  session it registered with. Sign in again and switch push notifications on again; log the old
  session out in Mattermost (above) so that nothing more is sent for it.
- **Matty was force-stopped** (in Android's app settings). With ntfy the pushes still arrive: ntfy
  starts Matty to hand each one over. With a UnifiedPush app that hands pushes to Android as a plain
  broadcast, Android delivers nothing to a stopped app, so the pushes that come meanwhile are lost,
  until Matty is opened once again.
- **The UnifiedPush app on the phone was stopped or removed.** Stopped (force-stopped, or without a
  network): the push server keeps the pushes for as long as it keeps any (ntfy: 12 hours by
  default), and they arrive when the app runs again. Removed: the push server may go on answering
  yes for a day, so this server cannot know; Matty says "Your UnifiedPush app was removed" when it
  is next opened.
- **The phone was away a long time.** A push server that has thrown a phone's address away answers
  `404` or `410`, and the server drops that registration at once, with no last push. Matty registers
  again by itself at its next start. ntfy never answers `404`. With
  `visitor-subscriber-rate-limiting` on, it answers `507` from a day after the phone's ntfy was last
  connected: those pushes are not kept, and after ten in a row the phone counts as failing and is
  tried with one push every ten minutes, working again by itself once its ntfy connects. After four
  days it answers `410`: the registration is dropped, and Matty registers again at its next start.
- **Matty says this server cannot reach the push server** (the card counts it as **Failing**). The
  push server did not answer, its certificate did not verify, or it is at an address this server
  does not send to: a private one, not listed under **Push servers on your own network**, or one a
  firewall in front of the Mattermost server does not let it reach. Once it can be reached, each
  phone works again by itself (a failing phone is tried with one of its pushes at most every ten
  minutes), or sooner with **Try again** in Matty.

## In a cluster

Each node sends the pushes Mattermost decides on there, under its own limits, and knows who has a
registered phone from a list every node reads every 30 seconds; a node learns of a new phone at
once only when it is the one the phone registered with. The daily sweep runs on one node. The
card's **Not sent since the plugin last started on this server** are the counts of whichever node
answered.
