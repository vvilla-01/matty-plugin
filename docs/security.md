[The Matty Plugin manual](README.md)

# Security and limitations

## Security posture

The plugin was reviewed across five surfaces — HTTP API, the WHEP relay, secret handling,
concurrency, and the webapp — with every finding checked against the code before being acted on.
What that produced, beyond the fixes themselves:

- **The panel is locked, and the lock is the server's.** Every route has a class (open, watch,
  watch-or-admin, linked) checked by one gate in front of all of them, before any meter is spent or
  anything the request carries is read; a route nobody has classified is linked-only, and a test fails
  if a route in `api.go` has no class or anything is dispatched above the gate without its own check.
  "Linked" means both of a link's entries agree, and a store that cannot be read is answered 503,
  never as locked or unlocked. The let-in list is written only by compare-and-set and fails closed.
  The panel's lock screen is a courtesy on top. See
  [What the server holds to, and where it stops](unlocking.md#what-the-server-holds-to-and-where-it-stops).
- **One address a user supplies reaches an outbound request: a phone's push address, and only
  under rules of its own.** Every other outbound URL is the admin's or fixed in the code. The ones
  to Broadcast Box are built from the admin's `BroadcastBoxURL`; a stream key only ever becomes a
  bearer token, and the HTTP client refuses redirects so that token cannot follow one somewhere
  else. Twitch's addresses are fixed in the code. Twitch ids and logins come from Twitch's own
  answers and are checked against a strict pattern before they are used or kept, a preview picture
  is fetched only from Twitch's picture host at a path Twitch's own answer gave, and the callback's
  one-time code goes only to `id.twitch.tv`. With **Send push notifications to Matty** on (off by
  default), anybody signed in may register a push address for their own Mattermost session, and the
  server then posts to it ([Push notifications](push.md)). That address is confined:
  - **When it is registered** it must be at most 1000 bytes of printable ASCII, an absolute
    `https://` address with a host and no user name or fragment (`http://` only to a push server the
    admin listed, and with **Only the push servers listed above** nowhere else at all), not at
    `localhost`, a name under `.localhost` or an address written out that the dialer below refuses
    (unless its push server is listed), with keys that are a valid P-256 point and a 16-byte secret,
    and this server's current push key.
  - **At every connection**, the push client's dialer checks the address it is about to connect to,
    after the name has been looked up, so no answer from a name server can lead it elsewhere: a push
    server the admin did not list must be at an address that is globally reachable by IANA's IPv4
    and IPv6 special-purpose registries (loopback, private, shared, link-local, documentation,
    benchmarking, translation, 6to4, Teredo and the rest refused, an IPv4-mapped address as the
    IPv4 address it maps); a listed one is still never reached at a link-local, unspecified,
    multicast or broadcast address (link-local through NAT64 included), `0.0.0.0/8`, or a cloud's
    metadata service (`fd00:ec2::254`, `100.100.100.200`, and `100.100.100.200` through NAT64), nor
    on this machine unless its line names it. Only the well-known NAT64 prefixes (`64:ff9b::/96`,
    `64:ff9b:1::/48`) are known to be translation: on an IPv6-only network whose NAT64 uses a prefix
    of its own, an address under that prefix looks globally reachable and leads to whatever IPv4
    address it embeds, your LAN's included, so there the firewall in front of the Mattermost server
    must refuse that prefix.
  - **The client** uses no proxy from the environment (a proxy would connect for it), follows no
    redirect, gives up after 5 seconds to connect, 5 for TLS and 10 in all, takes at most 16 KiB of
    headers and no answer below `200` (one that switches protocols is refused and its connection
    closed), gives an answer's body at most a second once its status has come and reads at most 4
    KiB of it, throwing it away, and has at most 4 requests open to a push server and 8 in all.
    Within these limits an account can still delay other people's pushes, by seconds and, with many
    such accounts, by tens of seconds, by pointing its phones at push servers of its own that answer
    slowly; **Only the push servers listed above** prevents that.
  - **What goes there is not the requester's to choose**: one encrypted message of at most 4 KB, to
    the keys they gave. Before anything else is sent to a new address, it gets one check message,
    whose random number the phone's Matty must send back from the same session; so pushes reach
    only an address the person's own phone reads. New registrations are held to 6 in 10 minutes a
    person.
  - **The address is a capability** (anybody who has it can send to that phone, and with the
    phone's secret, send pushes it shows), so neither is ever logged, part of a key's name, in a
    refusal or on the System Console's card, and an error from the client never carries them: logs name the push server's host, the status and
    a fixed word for what went wrong.
- **What the server contacts besides Broadcast Box**, each only when an admin has switched it on:
  the Mumble server (voice and the chat bridge); TMDB, the Steam store and IGDB (title lookups);
  Twitch (`id.twitch.tv`, `api.twitch.tv` and `static-cdn.jtvnw.net`, for Twitch live); Steam
  (`steamcommunity.com` and `api.steampowered.com`, for Steam status); Hytale
  (`connect.accounts.hytale.com`, for Hytale names); and the UnifiedPush push servers people's
  phones name (for push notifications). `LOCKDOWN.md` lists them host by host, and gives the push
  notifications' outbound rule.
- **A push is sent only when Mattermost decides to push**, from its push hook, which the plugin
  answers at once without reading its store or the network, and never by changing or cancelling
  Mattermost's own push. It carries what Mattermost's own push carries under its push notification
  contents setting, and nothing else; each is encrypted end to end to the phone (RFC 8291), signed
  with this server's own push key (VAPID, RFC 8292), a key that never encrypts anything and is never
  shown, logged, backed up or sent. Before each push the phone's Mattermost session is read: one
  that has expired or is somebody else's drops the registration at once, and one that cannot be
  read is sent nothing, is read again about a minute later, and drops the registration if it still
  cannot be while other sessions can be; while Mattermost can read no session, nobody is taken as
  signed out.
- **Connecting Twitch cannot be started by a link, an image in a post or another site:** the window
  needs a single-use ticket that only the panel can get, through a CSRF-protected request. It cannot
  be used to attach someone else's account either. The state is single-use, ten minutes,
  server-side, and bound to the user who started it; the return must carry that same user's
  interactive session (a bot, a personal access token or an OAuth app cannot connect); and Twitch's
  ID token must carry the nonce issued with that state, so a code that leaks cannot be redeemed by
  anyone else. No Twitch token outlives the request that received it. Only people who have unlocked
  the panel with their bearer token can connect, checked again when Twitch sends them back, and a
  Twitch connection unlocks nothing.
- **Connecting Steam or Hytale cannot be started by a link, an image in a post or another site, and
  cannot attach somebody else's account**: the same single-use ticket, ten-minute state and
  own-session check as Connect Twitch. Steam's signature covers the state, its sign-in is checked
  field by field against Steam's own fixed address before Steam is asked to confirm it, and each
  sign-in is accepted once. Hytale's code is bound to the state by PKCE, whose verifier never leaves
  the server, and the profile is read once and the token revoked at once. A sign-in started by Matty
  can only be finished by the same session through the app's own route, never by a browser. Only
  people who have unlocked the panel can connect, checked again on the way back.
- **The Steam Web API key and Hytale's client secret go only to Steam's and Hytale's own servers**,
  in headers, never in an address, and are never logged or sent to a browser or an app. Nor is a
  Steam or Hytale sign-in, a code, a token, a ticket or state, or anybody's Steam ID, Hytale profile
  id or username logged.
- **Steam status and Hytale names reach only people who can open the panel**: they are served by
  their own route behind the access gate, never in the stream list, which every signed-in browser
  receives. Nobody's Steam status is shown in the System Console, which works for a locked admin.
- **Twitch's player, when an admin allows it, runs in a frame with Twitch's own permissions:** it can
  play sound, autoplay, go fullscreen, show browser dialogs, and open windows that are not sandboxed
  (in the desktop app, including the app's built-in window for plugin pages, which carries the
  Mattermost session). It cannot navigate or read the Mattermost page, and nothing acts on messages
  it posts. It is sent only this server's origin as a referrer. Twitch sees this server's address and
  the viewer's IP address and, if the viewer is signed in to Twitch in that browser (in the desktop
  app, connecting Twitch signs it in), who they are.
- **No Twitch secret is logged or sent to a browser**: not the client secret, the application's
  token, a person's one-time code, token or ID token, nor the ticket, state or nonce of a
  connection. A Twitch stream in the stream list carries no stream key, and its handle is a separate
  HMAC that can never be a Broadcast Box stream's, so it can never reach the WHEP relay, the preview
  stills or the Clips recorder.
- **What the server tells those services about itself is the product's name, never the server's.**
  The title lookups introduce themselves as `MattyPlugin/<version> (Mattermost plugin;
  +https://github.com/vvilla-01/matty-plugin)`; push servers are told `MattyPlugin` alone, and the
  **Contact for push servers** when an admin sets one; the name a server has given the plugin is only
  ever shown to people on that server.
- **Title lookups, when an admin switches them on, stay on the server.** Somebody's search text
  goes to TMDB or Steam as a query parameter, or to IGDB in the body of the query, and nothing else
  of theirs does. What goes to IGDB is reduced first to letters, digits, spaces and a few marks, so
  nothing typed can end its search or start another clause. Pictures are fetched
  only from addresses the providers' own answers gave, over https, from a fixed list of image
  hosts, never following a redirect off that list, capped in size and checked to be a picture —
  then relayed, so no browser ever contacts any of them. A title is asked for by an id the
  server checks against a strict pattern, and its details are looked up again by the server rather
  than taken from the browser. Adult titles are refused however they are asked for. The server
  keeps to its own budget with each provider (Steam 30, TMDB 120 and IGDB 120 calls a minute, IGDB
  at most 4 a second and 4 at once), shares one fetch
  among everybody asking for the same thing at once, and treats a provider's "slow down" as
  temporary rather than remembering the title as missing. The TMDB token is never logged, never
  sent to a browser, and — like every plugin setting — kept by Mattermost in its configuration as
  plain text. The Twitch client secret and the access token it earns are never logged, never sent
  to a browser, and the token is never written to the database; the secret, like every plugin
  setting, is kept by Mattermost in its configuration as plain text. Remembered searches are filed
  under a hash rather than the words, so they do not appear in Mattermost's logs; they are not tied
  to who searched.
- **The hashtag search has no powers of its own.** It is sent by the reader's browser, as the
  reader, to Mattermost's own search API, so it finds only what that person could find in
  Mattermost's search box; the plugin's server is not involved. Messages are shown as text, never
  as HTML, and "Open in Mattermost search" only ever puts a single hashtag into the search box. The
  people and channels it looks up are forgotten when its reader signs out, and a search still on its
  way then cannot put them back, so the next person on the same page never sees full names
  Mattermost sent only to an admin.
- **No stream key is serialized to a browser.** Streams are addressed by an HMAC handle. There is a
  test asserting the marshalled snapshot contains no key, including for an unnamed stream, whose
  title used to be the key itself. Nor is one in the answer to a playback request any more:
  Broadcast Box v2.0.2 names each session's media stream after its key, every release before 3.0.0
  passed that on to the browser, and the plugin now writes `stream` in its place before anything —
  the browser, or the log with **Log WHEP negotiation** on — sees the answer. See
  [Upgrading to 3.0.0](upgrading.md#upgrading-to-300). Nor is one in the stream list any more when **Public watch
  URL** is set: Pop out is a redirect, for linked people only, and a test checks the list carries no
  `watchUrl`. The panel is not told the Broadcast Box URL either; until 3.0.0 `/me` carried it, though
  nothing used it.
- **Client-facing errors are fixed strings.** Transport errors wrap `*url.Error` and carry the
  origin's LAN address; upstream bodies can carry anything. Detail goes to the server log only,
  and not a stream key with it: Broadcast Box's reason for refusing a playback request is logged
  with the key taken out, in any case, before the reason is cut to 200 bytes, so a key across the
  cut leaves no half behind. That holds for the panel's playback relay as for the Clips recorder;
  until 3.1.0 only the recorder's was cleaned, after the cut. The exception is configuration
  validation, which is deliberately specific so an admin can act on it, and contains no secrets.
- **Writing to the schedule is for linked streamers, checked on the server.** Planning a stream,
  changing one and joining one each need the caller's stream linked, and are refused with a 403
  otherwise (planning and changing at the gate, joining in its handler, which also says `locked` or
  `watch_only`) — before a title is looked up, so nobody who cannot plan can make the server call TMDB,
  Steam or IGDB through it. Taking something back — cancelling your own plan, leaving one — never needs
  it, and an admin's cancel is unchanged. A link that cannot be read is answered as the store
  failing, not as "link your stream first". Reading the schedule is open to every signed-in user.
  **What’s on?** follows the same rule: setting it is for linked streamers, refused before anything
  is looked up, and taking it down never needs the link. A picture the server does not already hold
  is fetched only for a title the panel can be showing anybody — a plan, What's on? on a live card, a
  search's results — or, for somebody linked, two more: a clip's tag, or their own What's on? before
  it has aired. Anything else is answered 404 with no provider asked, so nobody can
  walk the providers' ids through it. Being linked is not a pass for any title, as it was until
  3.1.0: a Markdown image in a post is fetched by every reader's browser with their own session, and
  Mattermost checks no CSRF token on a GET (its image proxy, which would fetch it without the
  session, is off by default), so anybody who could post — somebody locked, a guest, somebody in
  Mumble through the chat bridge — could have had every linked reader do the walking for them. A
  title search is a `POST`, with what to look for in its body: nothing that merely loads an address
  can make one, in a browser or in any other client, and Mattermost checks the CSRF token of a
  `POST` made with a cookie session. (Until 3.5.0 it was a `GET` answered only with the
  `X-Requested-With` header a browser's image cannot send, which is a fact about browsers and held
  for nothing else.) The caller's own stream profile, which is read from Broadcast Box on every
  call, is still a `GET`, answered only with that header.
- **Clips follow the lock, checked on the server.** Every route under `/api/v1/clips` asks first, before
  it reads anything the request carries. Looking (the list, a clip, the cards, a clip's video and its
  picture) is for linked people and people let in; everything else — upvotes, reports, shares,
  drafts, deleting, and the streamer's switch and wipe — is for linked people, and a let-in caller
  there gets 403 `watch_only`, with every `can…` flag in what they can see set false. So somebody
  locked gets the same 403 `locked` for a clip that exists, one that has gone, one that never did and
  an id that is not one, its own clipper included, and spends nothing metered. A yes is reused for ten
  seconds by the routes a playback or a channel of cards would otherwise ask dozens of times (a video,
  a picture, a draft's video, the cards); a no is remembered for two seconds, so a refused caller
  cannot make the store work for every request, and a failure never is; a link that cannot be read is
  answered as the store failing. System admins are asked like everybody else there. The System
  Console's `/api/v1/admin/clips` routes are the one exception: `manage_system`, and no link, so every
  admin can moderate.
- **Drafts are their owner's alone**, their video included; anybody else asking for one is told it
  does not exist. Clip files are served only through the plugin's own routes, after that check, as
  `video/mp4` with `nosniff`, a sandboxing `Content-Security-Policy` and `private, no-cache`, with
  Range support so a player can seek. Nothing offers a download.
- **No stream key goes into a clip.** Clips and drafts name their stream by its handle, and by a tag
  that is a second, separate HMAC, kept in the store and never sent anywhere (made with its own
  prefix, since for an ordinary lower-case key the handle's own HMAC would otherwise have been the
  same). Files are named by the clip's id and logs name the handle; a title is cleaned of the
  clipper's bearer token and stream key and of the stream's key, and a tag of the stream's key.
- **The shared snapshot carries no clip content**: how many clips there are, when the newest was
  published and a hash of the list, each live stream's recording state and when its recording began,
  the ends of streams that are held (by the titles already on their cards), and the Best Clipper's
  name and score, which is an award like the medals, with the id of their best clip, which only the
  routes behind the check will look up. Titles, pictures, voters and posts come only from those
  routes.
- **The recorder talks to Broadcast Box and nothing else**: its own WHEP session per recorded stream,
  through the same relay code as a viewer's, then media over UDP with no STUN, TURN or mDNS, taken
  only from the IPv4 addresses of the Broadcast Box URL's host — Broadcast Box's public
  `NAT_1_TO_1_IP` is ignored. It never asks the publisher for a keyframe. The plugin leaves its sessions
  out of every count and face, so it is nobody's watching; Broadcast Box's own pages count it as one
  viewer per recorded stream.
- **Who may clip is checked against a record kept in memory only**: which signed-in person has a
  playback session on which stream — everybody's, linked or let in, whatever **Show who is watching**
  and **Rank the Top Snoopers** say. It is never shown, logged or returned, kept apart from the faces'
  record, at most 4,096 sessions, and each is forgotten fifteen minutes after it was last known to
  be open, and when the plugin stops.
- **Sharing makes posts on people's behalf**, the one place the plugin writes into a channel as
  anybody but its bot: a clip's share and, from 3.2.0, a plan's. A share is posted as the person who
  pressed Share, into the channel they have open, only after the plugin has checked what Mattermost would have checked had they posted it
  themselves, since a plugin's post skips all of it: that they are in the channel and have
  `create_post` there, that it is not archived or shared with another workspace, that a direct
  message's other person is still active, and the team rule for direct messages where it applies.
  Its message is fixed text and a link ("🎬 Matty clip · 0:45", "📅 Matty plan", with the server's
  name for the plugin), never a title or a person's name, so nothing typed can mention, link or
  format there; those ride in the post's props, which the card treats as a hint
  anybody could have forged and which are taken out when the clip is deleted. When a clip goes, the
  plugin edits the props of each of its posts to say so, and a share it could not record is deleted
  again, so that every share stays one it can reach. A plan's post carries even less: its props are
  the plan's id and start time and nothing else, so the card is drawn from the list of plans every
  reader already has, and an edited or cancelled plan leaves nothing of itself in any post; there is
  nothing to track, and nothing to take back.
- **Two hooks look at every post and every edit on the server, while the plugin runs.** First, a
  message that is nothing but a bearer token (a UUID, with or without "Bearer ") is refused: from
  anybody not linked, any such message while the plugin is set up; from somebody linked, their own
  token; never from a bot, a webhook or Mattermost itself, and an edit only when its words change.
  Nothing about the message is logged. See
  [A token pasted in the wrong place](unlocking.md#a-token-pasted-in-the-wrong-place). Then, a post of
  the plugin's types that it did not itself just make, for that person, channel, kind and id —
  a forgery through the REST API, or Mattermost's own copy when a thread is moved — is posted as an
  ordinary message of another type, `custom_villanet_clip_copy` or `custom_villanet_plan_copy`, with
  every one of the plugin's props blanked, rather than refused, so moving a thread whose root is a shared clip or
  plan still works. An edit to a shared clip's or plan's message is refused ("Shared clip posts can't
  be edited.", "Shared plan posts can't be edited."), and any other edit leaves its props, files and edit time
  as they were, except that its author may hide a link preview; an ordinary post that asks through
  its props to be drawn as a card loses that prop. **Only while it runs**: a `custom_villanet_clip`
  or `custom_villanet_plan` post made through the REST API while the plugin is disabled, crashed or
  being upgraded is not checked, and is drawn as a card once the plugin is back (a forged plan card
  still shows only what the reader's own list of plans says about that id). Its words are the forger's, shown to locked
  readers beside the forger's own name as its author; readers who can watch see only what the server
  says about the clip, which stays behind the check. (The post types and the props beginning
  `villanet_` keep the names they were given when the plugin was VillaNET, so that posts already
  made go on being drawn and guarded as cards; nobody is shown them.)
- **The soundboard is the server's, not the panel's.** Every route of it has a class: the board,
  adding, renaming, playing and a sound's audio are for linked people; deleting your own, one or all,
  is a takeback, open to anybody. The board, adding and renaming are refused to a linked person whom
  Mumble does not have at that moment. A press is checked against Mumble's live state, never the
  roster the panels were sent: the presser must be found in exactly one Mumble session, by a
  plain-letter name, not muted, deafened or suppressed (and registered, with **Only registered Mumble
  users**), and the plugin's own session must be able to speak; the player reads it all again before the
  first packet and before every one after, so a mute, a move or a departure stops the sound. The
  cooldown, the one-sound-at-a-time gate and the 8 a minute are taken under one lock, so any number of
  tabs or scripts pressing at once get one sound. An upload is strict JSON of at most 80 KB, every
  packet's size and header are checked, every packet is decoded (a decoder that crashes is caught,
  answered as a bad sound and logged, never a stopped plugin), the decoded sound is held to a loudness
  ceiling, the emoji is resolved by the server, and the name is cleaned of the author's bearer token
  and stream key. A sound's audio goes only to its owner and to system admins. Both audio routes, and
  the System Console's list of sounds (which reads every sound's audio to check it), are GETs
  answered only to a request carrying `X-Requested-With: XMLHttpRequest`, checked before any meter
  and before anything is read, so an image in a post pointing at one spends and reads nothing; the
  audio is served with `nosniff`, a sandboxing `Content-Security-Policy` and `private, no-cache`. Who played which sound is told to nobody but the server log. Emoji pictures
  come from Mattermost's own origin.
- **Choices, fans and achievements carry nothing private.** The choices beside names are award names
  only; the biggest fans and achievements are figures already on the boards, or worked out from them.
  Every answer that is per reader is sent `no-store`, and what the panel was told in somebody's name
  (their choices, their progress, their soundboard, what their popover may take back, and the cards'
  votes) is forgotten when they sign out.
- **Downloading a clip (3.2.1) takes a ticket and never trusts a GET alone.** Downloads are off unless
  an admin switches them on, and only for linked people. Mattermost checks no CSRF token on a GET, so
  the address a browser saves from is no good without a ticket, which only the panel's POST (CSRF
  checked) can get: random, 60 seconds, one person and one clip, one at a time per person, kept only as
  its hash, spent once, 10 per person per 10 minutes, a refusal costing none. The address it is spent
  for is signed with the plugin's own secret for that person and lasts ten minutes, so the desktop app
  and a resuming browser can ask again; every rule (the admin's switch, the link, the clip still kept,
  the streamer's say) is checked again at each step, a refused request writes nothing, and the panel
  opens no address but the plugin's own. The file is served as an attachment with `nosniff`, a
  sandboxing `Content-Security-Policy` and `no-store`, and its name is cleaned of anything a file name
  cannot carry. Each download is logged with the person's and the clip's ids, never the ticket or the
  address.
- **The endpoints that cost something are metered per user**: linking 5 per 15 minutes (a successful
  link proves a token is valid, so it is a guessing oracle), Connect Twitch 5 per 15 minutes,
  profile writes 10 per minute, What's
  on? changes 10 per minute, title searches 40 per minute, title pictures 120 per minute (only when
  not already held), playback 60 per minute. Playback is generous because a grid costs one request
  per tile and Mattermost unmounts the panel when it is closed, so reopening renegotiates every tile
  at once. Clips: drafts 5 per 10 minutes, publishing 10 an hour, upvotes 20 a minute, shares 10 per
  10 minutes, and every other clip write — deleting, a draft's picture, reporting, the streamer's
  switch and wipe, and an admin's actions — 30 a minute; from 3.2.1, download tickets 10 per 10
  minutes. From 3.2.0: award choices 10 a minute, plan shares 10 per 10 minutes, sound presses 30 a minute (and 8 sounds a minute for the
  whole server), sound uploads 20 an hour, renaming and deleting sounds 30 a minute, and a sound's
  audio 60 a minute. From 4.1.0: push registrations 6 per 10 minutes (one the server already holds,
  or one it was sent less than 10 seconds before, costs nothing, and taking one back is never
  metered) and test pushes 3 a minute. None of these is
  spent by a request the
  lock refuses: the gate comes before every meter, so somebody refused never uses up what they would
  need once unlocked. On top of those, attempts at a share, a draft or a publish, refused or not,
  are held to 60 a minute together, counted right after the link check and before anything else in
  them is done, so that a stream of refused attempts costs no read of the store and no call to
  Mattermost. A draft refused because the stream's recording is still warming up or has failed, or
  the server is busy, and a publish refused because the server is busy or its store is not
  answering, give their allowance back.
- **Every route under `/api/v1/admin/` requires `manage_system`**, and none of them asks for a bearer
  token, which is why the System Console works for a locked admin: the let-in list and unlinking
  somebody, the airtime export, import and status, and the settings backup, theme, brand, bot's
  picture, which account the plugin posts as, channel pictures,
  preview pictures, remembered titles, taking somebody's profile banner away, the app check's list,
  Twitch connections
  (disconnecting, blocking, 24/7 and Delete all Twitch data), Steam and Hytale connections
  (disconnecting, blocking and Delete all), clips (the list, each clip's
  video and picture, deleting, removing a picture,
  clearing reports, blocking and unblocking, resetting a score, and wiping a stream's recording),
  the sounds (the list, each sound's audio, deleting, blocking, **Check again** and **Start the board
  again**), and the push notifications' status, which counts phones by push server and names nobody.
  The airtime status is counts and timestamps only, never the tables.
  The airtime routes touch this plugin's own KV documents and nothing else; the plugin still never
  calls Broadcast Box's `/api/admin/*`. One deliberate exception to "no key reaches a browser" lives
  here: the airtime tables are *keyed by stream key*, so the export contains them. A system admin already has
  those keys — they are what **Stream names** is written in — and keying the export by the
  opaque handle instead would make a backup useless, since the handle is an HMAC under a
  per-installation secret and would not resolve after a rebuild. Nothing a non-admin receives
  carries a key.
- **User-triggered refreshes are coalesced** through a single-slot channel rather than a goroutine
  per request, so no endpoint multiplies load on the origin, the KV store, or every connected
  client's WebSocket.
- **Origin input is bounded**: stream count, MOTD length, stream-key shape and character set are all
  checked before anything is stored or broadcast.

`make check` runs `go vet`, `gofmt`, `go test -race`, a check that `plugin.json` and the compiled
`manifestVersion` agree, a check that the webapp is on the React Mattermost 11.11 ships, `tsc`, the
stylesheet checks, `build/check-away.mjs`, `build/check-twitch.mjs`, from 3.4.0
`build/check-accounts.mjs` (the Connected accounts rows, the Steam and Hytale popover rows, the
Playing line, the Disconnect line and the connect steps' timings), and from 3.2.0
`build/check-awards.mjs` (the awards beside a name and the plan card), `build/check-sounds.mjs` (the
soundboard's tiles, the upload's bytes and the loudness measure) and `build/check-mentions.mjs`
(@names in titles), and `build/check-squeeze.mjs`, `build/check-player.mjs` (the clip player) and
`build/check-reads.mjs` (what the panel reads of the server); `make e2e` runs the real-time recorder
tests. A Go test reads every help text in `plugin.json`, the README, every chapter of this manual
and `LOCKDOWN.md`, and fails on any sentence that promises nobody outside is
contacted without naming Twitch, Steam, Hytale and UnifiedPush, or counts the cases and leaves one out, and another on any that says the plugin never receives audio, which
was never quite true and is not a promise the soundboard could keep. The soundboard's player is tested
against a Mumble server made inside the test, on the wire. See
[Build from source](building.md#build-from-source).

## Signed releases

A release of Matty Plugin is signed with the two keys the Matty app is signed with: RSA 4096 and
ML-DSA-87, a post-quantum signature. Inside the bundle, `release/manifest.txt` lists every file of the
release with its SHA-256 and size, and beside it are the two signatures over the manifest's exact
bytes. The SHA-256 of the manifest is the release's identity, published with it. `build/checkrelease`
checks a bundle before it is installed, with the plugin's own code; see
[Build from source](building.md#build-from-source).

**The plugin checks itself** once, when it starts, against the two certificates built into it (never
one that travels with a release), and finds one of three things:

- **A signed release**: both signatures verify; the manifest is for this version and this server's
  platform; every file it lists is on disk with its size and SHA-256; and it lists what Mattermost
  loads, `plugin.json`, this platform's program and the web panel's bundle. There is no `plugin.yml`
  or `plugin.yaml` (Mattermost would read either in place of `plugin.json`), and nothing it does not
  list sits beside the web panel's bundle or in the `public` folder, both of which Mattermost serves.
  Other files it does not list are no failure, since Mattermost never reads them, but system admins
  are told about them among the panel's alerts: "This plugin is a signed release, and its folder also
  holds 2 files nobody signed.", with the first ten named and where to find them.
- **Not a signed release**: there is no `release` folder. Every development build is one, and so is
  any build somebody makes of their own.
- **Altered**: there is a `release` folder, and something about it or about the files is not as
  signed. The server log says so at a warning, naming the file, and system admins see it among the
  panel's alerts: "This plugin's files do not match the signed release they came with:", then why,
  then "It goes on running. Install the release again from its published bundle.", and where to
  look: System Console > Plugins > Matty > App check.

The App check section of the System Console starts with a line on this: **This plugin** says "This
plugin is a signed release of Matty Plugin 4.2.0.", "This plugin is not a signed release: a
development build, or somebody’s own build of Matty Plugin 4.2.0." or, as an error, "This plugin’s
files are altered. They are not as the signed release of Matty Plugin 4.2.0 has them:" and why.
Under it is the SHA-256 of the manifest the server sent, worked out in the browser, to compare with
the one published with the release, and, for a release that is signed or altered, what the check
does and does not show.

The plugin runs in every state. Any signed-in reader can ask for the manifest and both signatures
(`GET /api/v1/release`), and **Matty checks them itself**, against its own copies of the two
certificates, with the phone's own cryptography: both signatures first, then the manifest, whose
version must be the one the server reports. It shows the result where it lists the plugin: "Matty
Plugin 4.2.0 · signed release (RSA + ML-DSA)", "· not a signed release", or "· release not
verified:" and why. A phone that cannot verify ML-DSA-87 reads "not verified", never "signed" on the
RSA signature alone. An unsigned or unverified release goes on working, in the app as in the panel.

What it shows, and what it cannot:

- **It shows which release a server presents, not what code it runs.** A plugin holds no secret. A
  server program somebody altered can skip its own check and hand out a genuine release's manifest
  and signatures, which are public. It is evidence for the honest case, a server running what it was
  given, not a lock.
- **An older genuine release still reads signed**, for its own version. The check says the files are
  a release made with Matty's keys, not that they are the latest one.
- **Nothing is fetched to decide it.** The certificates are built into the plugin and into the app.

## The app check

System Console > Plugins > Matty > **App check** decides whether this server asks Matty to prove that
it is the official build, on Android's word rather than its own. It is the other half of a signed
release: the plugin shows the app its release, and the app proves itself to the plugin.

**How it proves itself.** When Matty opens it asks the server for a challenge, good for five minutes
and for one answer, and kept for that session only. It makes a fresh key in the phone's Keystore
(StrongBox where the phone has one) with an attestation challenge derived from that one, the address
it reached the server at and the person's id; sends the server the key's certificate chain; and
deletes the key. In that chain the phone's secure hardware and Android say which app asked for the
key, which certificate signed that app, and how the phone started. The server spends the challenge,
derives it again from its own Site URL and the session's user, and passes the app only when:

- the address the app reached is the Site URL's;
- the chain ends in one of Google's two hardware attestation roots, every certificate is signed by
  the next, only the first carries an attestation, and none is on Google's list of revoked
  attestation certificates as the plugin carries it;
- the key was made in the secure hardware (a trusted environment or StrongBox), with this server's
  challenge;
- the bootloader is locked, and the system is verified or signed by a key the server
  accepts: GrapheneOS's, built in, or one an admin lists in **More verified-boot keys**;
- the app is exactly one package, `ceo.villa.matty`, signed with Matty's RSA certificate
  (`E5:52:50:BE:…:27:3C`) or one an admin lists in **More signing certificates** (for people who run
  a build of their own).

**Check the Matty app** is **Off**, **Check, and show the result** (the default: the person and the
admins are told what was found, and nothing is refused), or **Check, and refuse a Matty that fails**.
With refusing on, a request that says it is Matty (by its User-Agent) from a session that has not
passed is answered `403` on every route but the few its lock screen needs and taking its own push
registration back (`DELETE /me/push`), with the reason: "Matty answers only the official Matty app,
and this copy has not been checked yet.", "… has to be checked again: this server's rules for the
check have changed." or "… did not pass the check." and why (a server with a name of its own says
that name first). The web panel is never refused by it.

A verdict belongs to one session and lasts 30 days or until the session ends; Matty proves itself
again after an update. A verdict given for a certificate or a boot key an admin has since taken off
the list, or under an older copy of the revocation list, no longer counts. A failed verdict is
judged again whenever the rules change: once an admin lists or takes off a certificate or a boot
key or changes the Site URL, or a release with another copy of the revocation list is installed,
it no longer counts. Whenever an admin changes the check's setting, either of its lists or the Site
URL, every open Matty is told at once: it asks the server again, and proves itself again if its
verdict no longer counts.

Admins see the verdicts in the same section, under **Results**, after the line on this plugin's own
release (see [Signed releases](#signed-releases)). **Matty apps** counts them ("3 sign-ins of 2
people: 2 verified, 1 not verified"), says in a line what the setting does now and what a verdict
is worth, and lists every sign-in whose verdict has not expired, newest first: the person, **Verified**,
**Not verified** or **No longer accepted**, when, the app's version code, and the server's reason
for any that is not verified. Past 20 there is **Show all**, and **Refresh** reads the list again;
nothing on it is saved. For each session the server keeps whether it passed and why not, when, the
app's version, the phone's security level and boot state, and what the app says of itself, labelled
as its own word: nothing that tells one phone from another of the same model and system. The card
shows the first four of those, not the rest. The two requests that prove it are held to 10 a minute
each.

What it shows, and what it cannot:

- **It proves the RSA certificate, not the post-quantum one.** Android attests only the first
  certificate of an app's signing history, which for Matty is its RSA certificate; the ML-DSA-87
  certificate that signs it now is never attested. The post-quantum signature protects installing
  and updating Matty on the phone, the plugin's own release carries one (above), and Matty checks
  its own signers on itself (Settings says "This copy of Matty is not the official build." when
  Android's record of them is not Matty's pair). It cannot be proved to a server.
- **It recognises the official app; it cannot wall out a program that hides what it is.** Nothing
  Mattermost gives a plugin tells the web panel from any program holding a session, so refusing
  stops honest copies that were altered or rebuilt, not a determined impostor that never says it is
  Matty.
- **It holds only while the phone is intact**: its secure hardware not broken into, its factory
  attestation key not leaked and not yet revoked, and Matty's RSA signing key safe.
- **A phone has to have a locked bootloader**, and a system that is verified or signed by a key the server accepts. A genuine Matty
  on a phone with an unlocked bootloader, or on a system signed by a key nobody listed, fails.
- **Nothing is fetched.** Google's list of revoked attestation keys (as of 2026-11-16) and
  GrapheneOS's keys (as of 2026-04-20) are copies shipped with the release; a newer copy comes with a
  newer release. The server asks Google nothing about anybody's phone.
- The development build of the plugin, which starts only where Mattermost's Site URL is the machine
  it runs on, also accepts an emulator and a debug build, and marks such a verdict **Development**
  on the card. None of that is in a release build, and a test proves a release build refuses an
  emulator's chain.

## Notes and limitations

- **Coming back after a reply leans on Mattermost's own internals**: the shape of its side-panel
  state, three action names and a few element ids, all unchanged from 9.0 to master and pinned by
  `build/check-away.mjs`. If they change, the panel stops coming back by itself and simply behaves as
  it did before 2.11.0 — it never acts on a side panel it does not recognise.
- **Popped-out side-panel windows are unaffected** by coming back and by the small player; a
  popped-out channel comes back without the small player.
- **Streams kept connected out of sight still count.** While the panel is pushed aside, its streams
  stay connected for at most a minute (four minutes and a minute for the small player's, while the
  player cannot be seen), and for that long they still count as viewers on Broadcast Box, still
  show your face on those Live now cards, and still add to your [Top Snoopers](awards.md#top-snoopers) hours.
- **Remote viewers need Broadcast Box to advertise a reachable address.** The plugin relays the
  SDP, but the ICE candidates in Broadcast Box's answer decide where media flows. If viewers
  outside the LAN cannot play, set `NAT_1_TO_1_IP` (and a reachable UDP port) on Broadcast Box. The
  Clips recorder ignores `NAT_1_TO_1_IP` and records over the LAN address whatever it says.
- **The server's name and icon live in the panel and the App Bar, not the team sidebar.** Mattermost
  exposes no hook for the team sidebar header; putting it there would mean injecting global DOM, which would not cleanly
  revert when the plugin is disabled.
- **No channel announcements.** This plugin shows who is live; it does not post "X went live"
  messages. That is a natural next feature — Broadcast Box's `WEBHOOK_URL` would drive it
  event-driven rather than by polling, and the receiving endpoint would need to validate the
  source IP since the callback is unauthenticated.
- **Twitch live polls; it does not subscribe.** EventSub would tell the plugin when somebody goes live
  but not their title, category, viewers or picture, and its webhooks need a public https address.
  So a Twitch card can be a minute or two late, and removing this server's Twitch application in
  Twitch's own Settings > Connections does not disconnect anybody here: the plugin keeps no token, so Twitch has nothing to
  tell it. Disconnect, which works whatever the switch says, and the daily name check are the ways
  out. In a Mattermost cluster each node asks Twitch, as each node polls Broadcast Box.
- **Twitch's player only runs on https at port 443, under a name**, never on a Mattermost reached at
  `:8065` or by address, and never when Mattermost is itself framed by another site.
- Simulcast layer switching is not exposed. The player, and the Clips recorder, take whatever layer
  Broadcast Box sends; the plugin never calls Broadcast Box's `/api/layer`.
- **Clips' limits** — UDP to Broadcast Box, its LAN address, H.264 without B-frames, local file
  storage, Safari's silence, reconnects, very high bitrates, a slow disk, four streams at a time — are
  under [When clipping does not work](panel.md#when-clipping-does-not-work), and what a restart loses under
  [How long things are kept](panel.md#how-long-things-are-kept).
- **Shared clip and plan posts are guarded only while the plugin runs**; see
  [Security posture](#security-posture). The Mattermost mobile app shows a shared clip or plan as
  its message and link, and never shows that the clip has gone or the plan was cancelled; see
  [Sharing a clip](panel.md#sharing-a-clip) and [Sharing a plan](up-next.md#sharing-a-plan).
- **Push notifications go out only while Mattermost pushes, and are not kept.** Without
  Mattermost's own push notifications on, the plugin hears of nothing to push; a plugin that cancels
  a push before this one is asked stops it for Matty too; what was waiting to go, or to be tried
  again, is lost when the plugin stops or restarts; and in a cluster the limits are each node's.
  Calls' pushes, and other plugins', are not sent. See
  [When pushes do not arrive](push.md#when-pushes-do-not-arrive).
- **The soundboard has not yet met a real Mumble server.** It is built on what Mumble's own source
  says and tested against a stand-in; each server's version, permissions and bandwidth limit
  decide whether sounds are heard. Adding a sound needs a browser that encodes Opus at a constant
  bitrate (not Firefox on Android); playing one works anywhere. One Mattermost server only: in a
  cluster, a press reaching a node without the Mumble connection is refused. A Murmur that has fallen
  back to CELT drops the sounds. See [Setting the soundboard up](voice.md#setting-the-soundboard-up).
- **A tile left open earns hours**, for biggest fans and the 24/7 channel achievements as for Top
  Snoopers; the panel disconnects tiles nobody can see after a minute.
- **Live viewers may lose the sound after an OBS reconnect.** Found while Clips was designed: after a
  publisher reconnects, Broadcast Box v2.0.2 goes on sending the sound under the same identity with
  the new publisher's sequence numbers, and a receiver can drop those packets as repeats. The
  recorder notices its sound stop and reconnects; a viewer's tile may stay silent until it is tuned
  in again. It is Broadcast Box's to fix, and worth reporting there.
- **Broadcast Box can authorize playback and publishing externally.** Setting `WEBHOOK_URL` makes
  it POST `{action, ip, bearerToken, queryParams, userAgent}` to that URL on every WHEP and WHIP
  connect; a non-200 reply rejects the connection with 401, and a 200 reply returns
  `{"streamKey": "…"}` which *replaces* the presented token. The lock gates playback through the
  plugin; this hook would let Broadcast Box itself refuse anybody who reached it directly with a
  stream key, and let streamers publish with a long random secret that maps to a short stream key.
  The plugin does not implement it yet.
- **Stream keys never reach the browser.** The panel addresses a stream by an opaque handle — an
  HMAC of the key under a per-installation secret — and the plugin maps it back server-side when it
  relays the WHEP offer. Only keys that are live right now resolve, so the relay cannot be used to
  probe for keys that are not already being served, and from 3.0.0 the answer it relays back no
  longer carries the key (see [Upgrading to 3.0.0](upgrading.md#upgrading-to-300)). The bearer token you paste
  to unlock or link is masked
  behind a **Show** button (on the lock, the eye, **Show token**) that re-hides it after 15 seconds, so sharing your screen after peeking
  at it does not put it on display; your own stream key, which is public anyway, appears only in the
  link key's tooltip and the Relink sheet.
- **A reserved profile's publish credential is the token, not the key.** Broadcast Box stores a
  profile as a file named `<streamKey>_<token>`, and `getProfileFileNameByBearerToken` matches only
  the token half — a UUID the streamer holds. Knowing someone's *key* does not let you publish as
  them. This is why linking asks for the bearer token rather than the stream name: holding the
  token is the proof of ownership, so nobody can claim a stream by typing its name. Note the
  distinction only holds for reserved profiles; under the default `STREAM_PROFILE_POLICY` of
  `ANYONE_WITH_RESERVED`, an *unreserved* key can still be published to by anyone who knows it.
  Set **Stream names** for every key so friendly names, not keys, appear as card titles.
