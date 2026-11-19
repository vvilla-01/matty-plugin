[The Matty Plugin manual](docs/README.md)

# Locking Broadcast Box down

Matty Plugin reaches Broadcast Box over your own network, from the Mattermost server, and relays
everything a viewer needs: who is live, the playback negotiation, the preview pictures. Nobody's
browser has to reach Broadcast Box at all. So Broadcast Box can be put where only the Mattermost
server can reach it, and the world can be left only the one thing streaming software needs from
outside: publishing. This chapter describes such a set-up, what the plugin may and may not assume
about it, everything the plugin itself touches, and where its lock stops.

The addresses are examples; put your own in their place:

| Example | Stands for |
| --- | --- |
| `192.168.1.10` | the Mattermost server |
| `192.168.1.20` | the Broadcast Box machine; the plugin's **Broadcast Box URL** is `http://192.168.1.20:8080` |
| `192.168.1.30` | the reverse proxy (Nginx Proxy Manager in the examples) |
| `192.168.1.0/24` | your LAN |
| `mattermost.example.com` | Mattermost's Site URL host |
| `tv.example.com` | the public name streamers publish to |

Nothing here is required: the plugin works with a Broadcast Box that anybody can reach. But the
lock the plugin puts on the panel is the plugin's own, and it holds only as far as Broadcast Box
cannot be reached around it (see [What this does not do](#what-this-does-not-do)).

## 1. Broadcast Box's own settings

```sh
DISABLE_FRONTEND=TRUE           # no web pages, and no catch-all for unknown paths
STREAM_PROFILE_POLICY=RESERVED  # only reserved profiles publish, and only with their token
FRONTEND_ADMIN_TOKEN=           # empty: the admin API refuses every call
DEBUG_PRINT_ANSWER=             # off
```

`DISABLE_FRONTEND` matters more than it looks. In Broadcast Box's `routes.go` the web pages are
registered as `serverMux.HandleFunc("/", frontendHandler)`, and that registration is skipped
entirely when the variable is set. Because `/` was the catch-all, unknown paths then answer Go's
plain 404 instead of the page shell, which otherwise answers `200` to any address and discloses the
asset manifest. There is no lobby and no watch page.

**The admin API is still registered.** `/api/admin/*` is not gated by `DISABLE_FRONTEND`. With
`FRONTEND_ADMIN_TOKEN` empty every call to it is refused, but the routes exist. The plugin never
calls them: create, delete and rotate streamers' profiles on the Broadcast Box machine itself, by
whatever means your installation uses.

## 2. A firewall on the Broadcast Box machine

Drop everything coming in, and allow only:

| Allowed | From |
| --- | --- |
| TCP 8080 (Broadcast Box's HTTP port) | `192.168.1.10` (Mattermost) and `192.168.1.30` (the proxy) only |
| UDP 3478 (Broadcast Box's UDP mux port) | anywhere: WebRTC media |
| TCP 22 | your LAN, for yourself |
| ICMP | your LAN |
| loopback | |

With nftables, for example (a sketch to adapt, not a tested file):

```nft
table inet filter {
    chain input {
        type filter hook input priority 0; policy drop;
        iif "lo" accept
        ct state established,related accept
        ip saddr { 192.168.1.10, 192.168.1.30 } tcp dport 8080 accept
        udp dport 3478 accept
        ip saddr 192.168.1.0/24 tcp dport 22 accept
        ip saddr 192.168.1.0/24 meta l4proto icmp accept
    }
}
```

The media port open to the world is correct: a peer needs the ICE username fragment and password
and the DTLS fingerprint from a signalling exchange to use it, and signalling is reachable only
through the plugin (and, for publishing, through the proxy).

> **If the Mattermost server ever moves or changes address, change the 8080 rule with it, or the
> plugin goes blind.** It looks like every stream disappearing at once, with "Broadcast Box is not
> responding. Ask an admin to check the server." in the panel (and "Could not read the Broadcast
> Box status" in the server log). No other machine, admins' own included, can reach 8080.

## 3. A public address for publishing only

[`examples/npm-tv-publish-only.conf`](examples/npm-tv-publish-only.conf) is an Nginx Proxy Manager
"Advanced" configuration for `tv.example.com`: `/api/whip` proxied to Broadcast Box with its methods
limited and the `Link` header stripped, `/api/whip/profile` and `/api/admin` denied, everything else
`404`. There is no shared password anywhere: nothing is left behind the proxy to protect with one.

Streaming software publishes to `https://tv.example.com/api/whip` with its own bearer token, through
the proxy, from anywhere. Keep `/api/whip` behaving exactly as Broadcast Box has it.

## 4. Stream keys never reach the browser

The panel addresses streams by an opaque handle, an HMAC of the stream key under a secret of the
plugin's own, and the plugin maps it back when it relays a playback offer. The stream list carries
no key at all, and a test holds that it never does.

This is load-bearing in a set-up like this one, not merely tidy: a key read out of a browser's
developer tools would be usable against port 8080 by anything that could reach it.

- **A stream with no name** (no linked account and no line in **Stream names**) is called "Stream"
  and the first six characters of its handle, never its key.
- **A Twitch stream** has no stream key, and its handle is an HMAC of its Twitch id under a separate
  prefix, so it can never stand for a Broadcast Box stream: the playback relay, the preview
  pictures and the Clips recorder refuse it.
- **Broadcast Box's playback answers name the stream after its key.** Broadcast Box v2.0.2 names the
  media stream of every playback session after the stream key, so its WHEP answer carries the key in
  plain text, in `a=msid` and in the `cname`, `msid` and `mslabel` of each `a=ssrc` line. The plugin
  rewrites those values to `stream` the moment the answer arrives (`withoutStreamIDs`,
  `server/broadcastbox.go`), before the browser, the log or the recorder sees it. Nothing needs the
  real value: a browser only groups tracks by it.
- **Pop out is the one way a key can leave.** Pop out is the ↗ button on a playing stream, titled
  "Open in a new tab". Its link necessarily contains a key, and it exists only
  when **Public watch URL** is set, which is blank unless an admin fills it in. Pop out is a plain
  link to the plugin's `GET /api/v1/watch/popout?handle=`, which only a linked person can call: it
  answers a redirect to the watch page, with `Cache-Control: no-store` and
  `Referrer-Policy: no-referrer`, for a live stream that is not a 24/7 channel, and 404 otherwise.
  So the key reaches only the browser of a linked person who presses Pop out. **Once handed out it
  cannot be taken back:** it keeps working on the public page, and at Broadcast Box as a playback
  bearer, after that person is unlinked or let out. With `DISABLE_FRONTEND` there is no page to link
  to anyway, so keep the setting blank.

A key cannot be changed without becoming a new stream to the plugin: its airtime stays under the
old key, and its streamer links again. A server that ran the plugin before 3.0.0 should read
[Upgrading to 3.0.0](docs/upgrading.md#upgrading-to-300): releases before it let keys out in two
ways now closed.

## What the plugin touches

On Broadcast Box, three endpoints at the **Broadcast Box URL**, all allowed by the firewall above,
and, while Clips is on, WebRTC media from Broadcast Box to the Mattermost server:

| Endpoint | Used for |
| --- | --- |
| `GET /api/status` | polling who is live; unauthenticated and read-only on the LAN |
| `POST /api/whep` | relaying playback negotiation for viewers' browsers; and, while Clips is on (the default), the Mattermost server's own recorder: one receive-only session per recorded stream, opened by the server itself, with the stream key as its bearer token and no Basic Auth |
| `GET`/`POST /api/whip/profile` | reading and writing a streamer's own note, authenticated with their token; and `GET`, to check a bearer token when somebody unlocks the panel or links their stream (Broadcast Box names the stream it belongs to) |
| WebRTC media, IPv4 UDP | the recorder's picture and sound, from Broadcast Box's ICE address (its UDP mux port, 3478 above) to an ephemeral port on the Mattermost server |

It calls nothing else on Broadcast Box: not `/api/layer` (no simulcast layer is ever chosen) and
not `/api/admin/*`.

**The recorder.** While **Let people clip streams** is on, which it is unless an admin switches it
off, the Mattermost server records every live stream that is not a 24/7 channel and whose streamer
has not switched clipping off, at most four at once, keeping the last few minutes of each on its own
disk for clips. Each recorder is a WebRTC peer, as a viewer's browser is:

- **Its signalling is the same `POST /api/whep`** a viewer's browser is relayed through, from the
  same machine, so the 8080 rule for the Mattermost server already covers it.
- **Its media is IPv4 UDP only**, with no STUN, no TURN and no mDNS, and it accepts candidates only
  from the IPv4 addresses the **Broadcast Box URL**'s host resolves to (`192.168.1.20` above). A
  public `NAT_1_TO_1_IP` that Broadcast Box advertises is ignored; a peer-reflexive candidate from
  the LAN address works. So the media never leaves the LAN and goes to no other machine.
- **The media port's rule already allows it.** The Mattermost server must be able to exchange UDP
  with `192.168.1.20:3478`: its own packets out, and the replies, which arrive on an ephemeral port.
  A firewall on the Mattermost server that drops them stops clipping and nothing else; the System
  Console then gives the reason, and the recorder retries with backoff.
- **The URL must be Broadcast Box's LAN address.** `localhost`, `127.0.0.1`, or a host name mapped
  to 127.0.1.1 does not work for clipping unless Broadcast Box offers a loopback candidate, and a URL
  through the proxy gives the proxy's addresses, which are not where the media comes from.
- **Broadcast Box counts each recorder as a viewer**: its own viewer count includes one per recorded
  stream. The panel's counts and faces leave them out, and they never count as anybody's watching.
- **Each new recorder session makes Broadcast Box ask the publisher for a keyframe**, as every new
  viewer does. The recorder itself never asks for one.

Switching Clips off stops every recorder at once, closing their sessions, without waiting for a
poll. Saving a new **Broadcast Box URL** stops them too, and drops what they had recorded.

**In Mattermost itself**, Clips' **Share** makes a post in a channel as the person who pressed it,
after checking that they could have posted it themselves; edits that post's props when the clip
expires or is deleted; and deletes a share again if it could not record it. Two hooks look at every
post and every edit on the server while the plugin runs. See
[Security posture](docs/security.md#security-posture).

**What it keeps on the Mattermost server's disk.** Clips need Mattermost's file storage to be the
local disk (`FileSettings.DriverName` `local`) and switch themselves off otherwise; the cover-art
cache and profile banners use it too. All of it is under `<FileSettings.Directory>/ceo.villa.matty/`,
folders `0750` and files `0600`:

| Folder | Holds | How long |
| --- | --- | --- |
| `ring/` | each recorded stream's last few minutes, and the held ends of streams that stopped | overwritten as it goes; ends kept 10 minutes; all of it wiped when the plugin starts or stops, when Clips is switched off or a new Broadcast Box URL is saved, and per stream on a streamer's opt-out or wipe or an admin's wipe |
| `drafts/` | clips not yet published, private to their maker | 2 hours; wiped when the plugin starts or stops, or Clips is switched off; a stream's drafts on its streamer's opt-out or a wipe, and a person's when an admin blocks them |
| `clips/` | published clips and their pictures | **Keep clips for (days)** (30 by default); removed within 10 minutes of expiring |
| `media/` | cover art of looked-up movies, shows and games | as long as the title is remembered; at most 256 MB |
| `banners/` | profile banners people set in the Matty app | until their owner or a system admin takes them away |

The recordings and the ends of streams together take at most 4 GiB, and clips and drafts at most
**Space for clips (GB)** (20 by default). Nothing new is recorded, cut or published while the volume
has less than 2 GiB free, or 5% of it when that is more, and every recorder stops below 1 GiB free.
The files stay if the plugin is removed; delete the folder by hand when removing it for good.

The clips themselves (titles, pictures, videos, votes, drafts) are behind the lock on the server,
for every route under `/api/v1/clips`. Listing, opening and watching clips, their pictures and their
shared cards are for people who are linked or let in. Everything else is for linked people only:
making, publishing, voting, reporting, sharing and deleting clips, drafts, the streamer's switches
and wipe, and downloading. Only the System Console's `/api/v1/admin/clips` routes skip the lock, for
system admins. The stream list every panel is sent carries only counts, recording states and the
Best Clipper award. Clip files reach browsers only through the plugin's own routes, never from an
address of their own.

**Downloads, off unless an admin switches on "Let people download clips".** Two routes, both for
linked people only, both behind the clips' gate:

- `POST /api/v1/clips/download-ticket {"id"}` checks every rule (the switch, the link, the clip still
  kept, its streamer not having turned downloads off) and answers a ticket for that one person and
  that one clip: random, good for 60 seconds, one at a time per person, kept in the plugin's
  key-value store only as its hash (`clip_ticket_<user id>`, written with a 60-second expiry), 10
  per person per 10 minutes. A POST carries Mattermost's CSRF check, so another page, a link or an
  image in a post cannot get one.
- `GET /api/v1/clips/download?t=<ticket>` checks every rule again, spends the ticket (a
  compare-and-delete, so it works once), logs "A clip was downloaded" with the user id and the clip
  id, and answers 303 to `download?d=<address>`: the clip id, when it lapses (ten minutes later) and
  an HMAC of both and the person's user id under the plugin's own secret. Nothing is stored for it.
  `GET /api/v1/clips/download?d=<address>` checks the signature, the person and every rule again on
  every request, and serves the MP4 from the disk as an attachment: `video/mp4`, `nosniff`,
  `Content-Security-Policy: default-src 'none'; sandbox`, `Cache-Control: no-store`, ranges.
- A stale, spent or somebody else's ticket or address is answered 410 with a line of plain text, and
  a refused request writes nothing. The ticket and the address are never logged. The second address
  exists because the Mattermost desktop app asks for a download again after its save dialog, and a
  browser resumes one from the same address; both ask for the last address of the redirect, never
  the spent ticket.

The reverse proxy's access log will record both addresses. Neither is any use without that person's
own Mattermost session, the ticket is spent within a second of being made, and the address lapses in
ten minutes.

**Beyond Broadcast Box**, the Mattermost server calls the Mumble server, when voice or the chat
bridge is on, and, **only if an admin switches on "Look up movies, shows and games", which is off by
default**, TMDB (`api.themoviedb.org`, `image.tmdb.org`), the Steam store
(`store.steampowered.com` and its `steamstatic.com` picture hosts) and, if a Twitch application is
also set up in the **Twitch** section, IGDB (`api.igdb.com`, `images.igdb.com`) with its sign-in at
`id.twitch.tv`. Those lookups are made by the Mattermost server, never by a browser, and only when
somebody with a linked stream searches while planning a stream or saying What's on?, saves one with
a title picked, or a panel shows that title's art. With the setting off, none of them happens. Every
request the server makes to one of these services says it comes from
`MattyPlugin/<version> (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)`, and never
names your server or what you call it.

**The Mumble connection** is one client connection from the Mattermost server, for who is in voice
and the chat bridge. It never joins a voice channel, and it hears nobody: Mumble sends it the audio
of whoever talks in the channel where it is parked (usually the root), and it drops that unheard.
With **Let people play sounds into Mumble** on, the connection also carries audio out: sounds made
in people's browsers and checked on the server (every packet decoded and measured), shouted into the
presser's own channel. Each play is logged with the Mattermost account and the sound's id. The
sounds themselves are kept in the plugin's key-value store (`sounds` and one `sound_` key each, at
most 40 KB a sound), never on disk.

**Talking from Matty, off unless an admin switches on "Let people talk from Matty".** Then:

- **The Mattermost server listens on one UDP port**, **Voice port (UDP)** (8445 unless changed), on
  every interface, IPv4 only. It is the one port the plugin itself listens on. Phones send their
  voice there over WebRTC (ICE, then DTLS-SRTP), and it answers only connections that a join made:
  `POST /api/v1/voice/join`, through Mattermost with the person's own session, for people who are
  linked or let in. Open it to the phones that should be able to talk, UDP only. Nothing else needs
  opening: there is no STUN or TURN server, and the server gives phones only the addresses in
  **Address for phones** (or the Site URL's host) on that port.
- **Towards the Mumble server, one more client connection for each person talking**, from the
  Mattermost server's address, as the roster connection is made (TLS, the certificate not checked,
  **Server password**). What goes there: the name `<username> (Matty)`, the person's voice as their
  phone encoded it (Opus, inside the TLS connection), their own mute and deafen, and their moves
  between channels. No Mattermost credential and no stream key. The voice is passed on as it is:
  never decoded, recorded or kept, here or on the disk. At most 8 such connections are opened in
  any two minutes, the roster connection's included, below Mumble's autoban, unless an admin says
  the autoban is off.
- **In a Mattermost cluster it stays off**, and so does the port.

**The title search** is `POST /api/v1/media/search`, with what to look for in its body. Nothing that
merely loads an address makes a POST, and on a browser's session Mattermost checks its CSRF token,
so a Markdown image in a post cannot make readers' browsers search the providers in their name. For
the same reason a picture the server does not hold is fetched only for a title a panel shows, never
for any title somebody linked names; see the first item under
[What this does not do](#what-this-does-not-do). What was looked up is remembered in the Mattermost
database (the plugin's key-value store, at most about 2 MB, words and picture addresses only, no
credentials), and the pictures on the Mattermost server's disk, in `media/` above (or in memory only
when file storage is not local); both stay if the plugin is removed unless an admin forgets them
first from the settings page. The Twitch access token is kept in memory only. Clips adds no host
beyond Broadcast Box.

**Twitch, only if an admin switches on "Show people live on Twitch"** (off by default). The
Mattermost server then contacts `id.twitch.tv` (the application's access token; and, once per
connection, exchanging that person's one-time code and then revoking the token it gave) and
`api.twitch.tv` (once per connection to ask whose the token is; once a minute while anybody is
connected, to see who is live; and once a day for their names). It fetches preview pictures from
`static-cdn.jtvnw.net` and relays them. Twitch sends the person's browser back to
`https://mattermost.example.com/plugins/ceo.villa.matty/api/v1/twitch/callback` (on your Site URL).
The reverse proxy's access log will record the one-time code in that address. It can be redeemed
only once, within minutes, and only together with the state it was issued for (the ID token's nonce
must match that state's), by the Mattermost user who started it; a leaked code is useless to
anybody else. With the switch off the server asks Twitch for nothing, and people already connected
can still disconnect (which touches only the plugin's store).

Only somebody who has unlocked the panel with their own bearer token can connect a Twitch account,
and a connection unlocks nothing: it is not a link, and it never lets anybody in. What the plugin
keeps for it (each connected person's Twitch user id, login and display name, and their Twitch
hours, never a Twitch password or token) is in its key-value store, under `twitch_links` and
`twitchair_`, and goes when they disconnect.

**Steam, only if an admin switches on "Show people's Steam status"** (off by default). The
Mattermost server then contacts `steamcommunity.com` once per connection (to confirm the sign-in
Steam sent back, `check_authentication`) and `api.steampowered.com` (once per connection, and once
a minute while anybody is connected, one call for everybody, to read their status), with the
admin's Steam Web API key in a request header, never in an address. Steam sends the person's
browser back to `https://mattermost.example.com/plugins/ceo.villa.matty/api/v1/steam/callback`, with
a query that carries Steam's one-time sign-in **and the person's Steam ID** (twice, in
`openid.claimed_id` and `openid.identity`). The sign-in is accepted once, within minutes, only for
the state it names, by the Mattermost user who started it. But the reverse proxy's access log keeps
that address, and with it every connecting person's Steam ID, for as long as it keeps logs, after
Disconnect and after **Delete all Steam data**, which cannot reach it. To keep it out, log that one
path without its query: nginx's default `combined` format writes the whole request line
(`$request`), so give `location = /plugins/ceo.villa.matty/api/v1/steam/callback` an `access_log`
with a `log_format` that writes `$request_method $uri` in its place (or turn its access log off),
keeping the location's `proxy_pass` and headers as for the rest of Mattermost; or keep access logs
only briefly. For example, beside Mattermost's other locations:

```nginx
log_format matty_path '$remote_addr - $remote_user [$time_local] "$request_method $uri" '
                      '$status $body_bytes_sent "$http_referer" "$http_user_agent"';

location = /plugins/ceo.villa.matty/api/v1/steam/callback {
    access_log /var/log/nginx/access.log matty_path;
    # the same proxy_pass and proxy_set_header lines as the rest of Mattermost
}
```

(`log_format` belongs in the `http` block; this is the `combined` format with the request line cut
to its method and path. It is read from nginx's documentation, not tried: after the next Steam
connect, check that the callback's line in the log holds the path only.) Connecting in the Matty
app never sends that address to the server. What the plugin keeps (each connected person's Steam ID
and name) is under `steam_links`; statuses live in memory only. Like all of the plugin's data, it
stays in the database if the plugin is removed: press **Delete all Steam data** (System Console >
Plugins > Matty > Steam > Steam connections) first when removing the plugin for good, or when you
stop using Steam's Web API.

**Hytale, only if an admin switches on "Show people's Hytale names"** (off by default). The server
contacts `connect.accounts.hytale.com` once per connection (to exchange the one-time code, read the
chosen profile, and revoke the token). Hytale sends the browser back to
`https://mattermost.example.com/plugins/ceo.villa.matty/api/v1/hytale/callback`; a code in the
proxy's log is useless without the PKCE verifier, which never leaves the server, and the address
carries no Hytale identity. What the plugin keeps (the username and profile id) is under
`hytale_links`. Like all of the plugin's data, it stays in the database if the plugin is removed:
press **Delete all Hytale data** first when removing the plugin for good.

Only somebody who has unlocked the panel can connect either, and neither connection unlocks
anything. The Steam Web API key and Hytale's client secret go only to Steam's and Hytale's own
servers, in request headers, and are never logged or sent to a browser.

**UnifiedPush, only if an admin switches on "Send push notifications to Matty"** (off by default,
under **Push notifications**). Whenever Mattermost decides to push to somebody whose Matty has
registered for push notifications, the Mattermost server sends one `POST` (HTTPS, or plain HTTP to a
listed push server) to the push address that phone registered: one end-to-end encrypted message of
1,126, 2,150 or 3,174 bytes, with `TTL: 86400`, `Urgency: high`, a VAPID `Authorization` signed with
this server's own push key, and `User-Agent: MattyPlugin`. The push server is the one the phone's
UnifiedPush app uses: a public one such as ntfy.sh, or one of your own. It sees this server's
internet address, the **Contact for push servers** if one is set, and when and how big each push is;
it cannot read one. See [Push notifications](docs/push.md).

**The outbound rule.** The plugin itself holds the server to these, whatever a firewall allows:

- **A push address** is at most 1000 bytes of printable ASCII, `https://`, with a host and no user
  name or fragment. `http://` is allowed only to a push server listed under **Push servers on your
  own network**, and with **Only the push servers listed above** on, no other push server is. One
  at `localhost`, at a name under `.localhost`, or written as an address the rule below refuses is
  refused when it registers, unless its push server is listed.
- **At every connection**, after the name has been looked up, the address about to be connected to
  is checked, so a name cannot be pointed somewhere else later. A push server that is not listed
  must be at a globally reachable address: nothing on this machine or your LAN (loopback,
  `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, `fc00::/7`), nothing link-local,
  and nothing else in IANA's IPv4 and IPv6 special-purpose registries that is not globally
  reachable. A listed one may be on your LAN, but never at a link-local address (`169.254.0.0/16`,
  where a cloud's metadata service usually is, also through NAT64 as `64:ff9b::a9fe:0/112`, or
  `fe80::/10`), an unspecified, multicast or broadcast one, `0.0.0.0/8`, or a cloud's metadata
  service elsewhere (`fd00:ec2::254`, and `100.100.100.200`, also through NAT64 as
  `64:ff9b::6464:64c8`); and on the Mattermost machine itself only when its
  line names it (`localhost`, a name under `.localhost`, or a loopback address). A line without a
  port means the usual one: 443, or 80 for `http://`.
- **The client** uses no proxy from the environment and follows no redirect; it gives up after 5
  seconds to connect, 5 for TLS and 10 for the whole request; it takes no answer below `200` (one
  that switches protocols is refused and its connection closed), and once an answer's status has
  come, gives its body at most a second and 4 KiB, then throws it away; and it has at most 4
  requests open to one push server and 8 in all.

A firewall that limits what the Mattermost server may reach must let it make outbound TCP
connections to 443 at the push servers your people use (on the internet, for public ones) and to
the hosts and ports you listed. A push server it does not allow is one pushes cannot reach: the
card under **Push notifications > Status** shows it as Failing.

What the plugin keeps for it is in its key-value store: `push_vapid`, the server's push key (a P-256
private key, made on the first start, never shown, logged or sent anywhere); `push_user_<user id>`,
each person's registrations, one per Mattermost session (the push address, the phone's public key
and the secret that goes with it, when, and how it stands), each lasting as long as its session
(the daily sweep writes the row again, once it is 30 days old, while one of its sessions stands; a
row not written for 90 days is gone, which takes the plugin being disabled for about two months);
and `push_people`, who has one. A settings backup carries none of it. A push address is a
capability, which anybody holding it can send to, and with the phone's secret can send pushes it
shows, so neither is ever logged, part of a key's name or shown, not even on the card, which counts
phones by push server only.

**The release check and the app check fetch nothing from anywhere.** The plugin checks its own
files against the signatures shipped in its release, and a Matty app's proof against Google's
attestation roots, Google's list of revoked attestation keys and GrapheneOS's boot keys,
all of which are copies built into the release. See [Signed releases](docs/security.md#signed-releases)
and [The app check](docs/security.md#the-app-check).

**Browsers contact somebody other than this server in exactly four cases, all off by default: two
for Twitch, one for Steam and one for Hytale; the UnifiedPush push servers above are reached by the
Mattermost server and by phones, never by a browser.**

1. **Connect Twitch** (under "Show people live on Twitch"). The person who presses Connect Twitch
   and then Continue on Twitch has their own browser, in the window that opens, reach
   `id.twitch.tv` and Twitch's sign-in pages. That window cannot be opened by a link, an image in a
   post or another site: it needs a single-use ticket that only the panel can get.
2. **Twitch's player** (only if an admin ALSO switches on "Play Twitch streams inside Matty"). A
   viewer who presses play on a Twitch stream loads Twitch's own player from `player.twitch.tv`.
   Its code names, for example, `assets.twitch.tv`, `gql.twitch.tv`, `static-cdn.jtvnw.net`,
   `usher.ttvnw.net` and video segment hosts, `spade.twitch.tv` (analytics), `passport.twitch.tv`,
   `id.twitch.tv`, `edge.ads.twitch.tv`, Amazon advertising (`s.`, `c.` and
   `vaes.amazon-adsystem.com`), comScore (`sb.scorecardresearch.com`) and Nielsen
   (`secure-sts-prod.imrworldwide.com`). These are examples taken from the player's code, not a
   complete or guaranteed list: the plugin cannot list or limit what the player contacts. Twitch
   sees this server's address and the viewer's IP address and, if the viewer is signed in to Twitch
   in that browser (in the desktop app, connecting Twitch signs it in), who they are. The frame can
   play sound, go fullscreen, show browser dialogs and open unsandboxed windows (in the desktop
   app, including the built-in window for plugin pages, which carries the session); it cannot
   navigate or read the Mattermost page.
3. **Connect Steam** (under "Show people's Steam status"). The person who presses Connect Steam and
   then Continue on Steam has their own browser, in the window that opens, reach Steam's sign-in page
   at `steamcommunity.com`. Its own policy lets it load from Steam's servers (for example
   `community.fastly.steamstatic.com`, `login.steampowered.com`, `store.steampowered.com`,
   `api.steampowered.com`) and Google's reCAPTCHA (`www.google.com/recaptcha`, `www.gstatic.com`,
   `recaptcha.net`); these are examples from the page's own policy, not a complete list. That window
   cannot be opened by a link, an image in a post or another site.
4. **Connect Hytale** (under "Show people's Hytale names"). Likewise to Hytale's sign-in pages, for
   example `accounts.hytale.com`, `connect.accounts.hytale.com` and `backend.accounts.hytale.com`.
   The page loads Cloudflare's Turnstile (`challenges.cloudflare.com`) and, if the person accepts
   Hytale's cookies, Cloudflare's analytics (`static.cloudflareinsights.com`), and reports to
   Hytale's Sentry (`sentry.hytale.com`), which records session replays for a share of visits. Its
   policy lets it talk to any https host, and lets its sign-in form go to Google, Microsoft, Discord,
   Twitch and Epic Games (`accounts.google.com`, `login.microsoftonline.com`, `login.live.com`,
   `discord.com`, `id.twitch.tv`, `www.epicgames.com`), so it may offer signing in with them, and a
   person who picks one reaches that service too. These are examples from the page's own policy, not
   a complete list. That window cannot be opened by a link, an image in a post or another site.

In the Matty app, 3 and 4 happen in the app's own sign-in window instead of a browser window.

Apart from these four cases (Twitch's two, Connect Steam and Connect Hytale), browsers talk only to
this server; push notifications go from this server to the UnifiedPush push servers and on to the
phones, and no browser takes part. The Steam profile button in a profile popover is an ordinary link: it opens
steamcommunity.com only when pressed, like Watch on Twitch.

Twitch's player only runs on https at port 443 under a name, so on `mattermost.example.com:8065` it
never loads. Check that the proxy adds no `frame-src` or `child-src` of its own:

`curl -sI https://mattermost.example.com/ | grep -iE 'content-security|permissions-policy|x-frame'`

Nothing about Broadcast Box is ever sent to Twitch, and no Twitch stream passes through Broadcast
Box, the playback relay or the Clips recorder.

## Diagnosing playback when Broadcast Box is quiet

With `DEBUG_PRINT_ANSWER` off, the plugin is the only place to inspect a negotiation. Switch on
**Log WHEP negotiation** (System Console > Plugins > Matty > Troubleshooting) to get the offer, the
answer and the ICE candidates Broadcast Box advertised, in the Mattermost server log. The stream is
named by the first four characters of its key, and the answer is logged after its stream ids are
rewritten, so the log carries no more of a key than those four characters.

That holds when Broadcast Box refuses the request too. For a viewer, its reason is logged as a "WHEP
exchange failed" warning whether or not that setting is on, and the key is taken out of the reason
where the error is made, before the reason is cut to 200 bytes, for the viewers' playback relay as
for the Clips recorder.

With Clips on, the recorder goes through the same exchange, so that setting also logs every recorder
connection attempt and retry for every recorded stream, its offer carrying the Mattermost server's
own ICE candidates, that is, its local addresses. Switch it off again once the question is answered.

An answer carrying **no** candidates is always logged as a warning, whether or not that setting is
on, because it means Broadcast Box advertised no address to send media to: usually `NAT_1_TO_1_IP`
or the UDP mux port. A viewer sees "Tuning" for twelve seconds and then "No video arrived from this
stream." beside **Retry**; for the recorder, the ✂ says "Clipping isn’t working on this stream just now", and
**System Console > Plugins > Matty > Clips > Clips on the server** gives each recorder's reason.

## What this does not do

The only people who can watch a stream, see a preview picture or watch a clip are those who have
unlocked the panel with their own bearer token, and those a system admin has let in. The plugin
refuses everybody else on the server, whatever their browser does. Every plugin route has a class
(open, watch, watch-or-admin, or linked), and a route nobody has classified counts as linked. A
locked caller gets 403 `locked`, and a let-in caller on a linked-only route gets 403 `watch_only`.
There are two deliberate exceptions: a system admin sees the preview pictures and plays clips in the
System Console without a token. Restricting by channel or team membership would be a change to the
plugin, not to a proxy or a firewall.

Signing out of Mattermost does not reload the page, so the panel forgets the person itself: their
streams stop and the small player goes, nothing waits behind the lock for them, the Clips dialog
closes and their clips and drafts are dropped, and who they are is unknown again until the next
person's own answer comes. That includes what the hashtag search was told in their name (the people
and channels Mattermost's page had not loaded, which Mattermost answers per reader: with **Show Full
Name** off, first and last names go to system admins only; a search still on its way at sign-out
cannot put them back), the soundboard and the reader's place on it, their choice of awards, what
their own profile popover may take back, their achievement progress, and the votes on shared clip
cards. The same happens when the plugin stops.

What the lock does **not** cover:

- **The list of who is live still reaches every signed-in browser.** That includes titles, who is
  watching each stream (when **Show who is watching** is on), the schedule and the voice roster.
  The badges and the profile popover outside the panel need it, and it is one broadcast. The art the
  popover shows for what is on is served to anybody signed in. A picture the server does not
  already hold is fetched only for a title a panel can be showing anybody (a plan, What's on? on a
  live card, a search's results), or, for somebody linked, a clip's tag or their own What's on? that
  has not aired. Mattermost's image proxy, which would fetch a Markdown image without the reader's
  session, is off by default.
- **A stream that is already playing keeps playing.** The plugin relays only the negotiation, and
  nothing can end a session at Broadcast Box from outside. When somebody is let out or unlinks, a
  browser that cooperates stops within about a minute even if it missed every notice. A browser
  that does not cooperate keeps its stream until the stream ends, which for a 24/7 channel is
  indefinitely. It cannot start another one.
- **Revoking a token in Broadcast Box does not unlink anybody here.** A stored link is never checked
  again, so use System Console > Plugins > Matty > **Access** > **Unlink** as well.
- **A Twitch connection is no way in.** Connecting Twitch is for people who are already linked, and
  having it connected unlocks nothing. Disconnecting it, and putting away its notice, are open to
  everybody, the locked included, because Twitch's agreement says a person's data goes when they
  ask. A Twitch card's picture is watching, like a preview picture.
- **Nor is a Steam or Hytale connection.** Connecting either is for linked people; disconnecting and
  putting away its notice are open to everybody. Seeing other people's Steam status and Hytale names
  is watching: `GET /api/v1/accounts` answers linked and let-in people only, and nothing about
  either is in the stream list. Every signed-in session, locked ones included, does get the
  content-free `accounts` signal when they change, so a locked reader can tell that something
  changed and when, never who or what.
- **Steam's return address can sit in the proxy's log.** It carries the connecting person's Steam
  ID, and the plugin cannot reach that log; see the Steam paragraph under
  [What the plugin touches](#what-the-plugin-touches) for keeping its query out.
- **Pop out hands out keys.** With **Public watch URL** set, Pop out gives linked people stream keys
  (section 4). Those keys keep working on the public watch page and at Broadcast Box after any
  unlink, and nothing here can take them back, so leave the setting blank.
- **Other nodes of a cluster catch up within 20 seconds** of a let-in, a let-out or an unlink. The
  node that made the change applies it at once.
- **Biggest fans and achievements are public**, like the boards: who has watched a streamer most,
  and everybody's achievements, can be read by every signed-in reader, locked ones included.
- **A downloaded clip is out of the plugin's hands.** With **Let people download clips** on, a
  linked person can save a clip to their own disk; deleting the clip, its expiry, an unlink or a
  streamer turning downloads off afterwards cannot take that copy back. Off, as it ships, nobody
  can.
- **The soundboard trusts Mumble names** as the headphones do: plain-letter names only, and exactly
  one Mumble session per person. A name match lets nobody act as somebody else in Mattermost; it
  decides into which Mumble channel a linked person's sound plays. Anybody who can reach the Mumble
  server can take a free name, and on a default Mumble server can also register one, so **Only
  registered Mumble users** helps only once your people have registered theirs.
- **Push notifications are Mattermost's, not the lock's.** With them on, anybody signed in can
  register their Matty for them, locked or not, and gets a push whenever Mattermost would notify
  them, as its own app would; nothing in a push is about the streams. A push server sees when each
  push goes and how big it is, never what it says. A notification already on a phone's screen
  stays there until Matty takes it away; see
  [A lost phone](docs/push.md#a-lost-phone).
- **Emoji on the soundboard** come from Mattermost itself (`/static/emoji/`, `/api/v4/emoji/`),
  never from anywhere else.
- **The lock is the plugin's, not Broadcast Box's.** Anybody who could reach Broadcast Box's
  `/api/whep` directly with a stream key could watch around it. Behind the firewall in section 2 and
  the publish-only address in section 3, that is nobody. Keep it that way.

## fail2ban in front of Mattermost

A wrong token answers 403 from a plugin route, and Mattermost answers 404 for every route of a
plugin while it is stopped or restarting, so a fail2ban filter that counts 4xx answers can ban the
people reading. If yours does, exempt `/plugins/` as
[`examples/fail2ban-npm-filter.conf`](examples/fail2ban-npm-filter.conf) does, beside the
exemptions for Mattermost's own `/api/v4/` and `/static/`.
