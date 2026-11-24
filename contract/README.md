# The Matty Plugin client contract

What a client of Matty Plugin (id `ceo.villa.matty`) can rely on: the routes, the
events and the documents it reads. Two clients exist, the web panel in this repository (`webapp/`)
and Matty, the native Android app, in its own. The panel ships inside the plugin and is always in
step. Matty is released on its own schedule, so the two are written to this contract and released
as a pair.

- `contract.json`: which contract this is (`revision`) and the first version of the plugin that
  spoke it (`since`). The server says both in `GET /api/v1/access` and `GET /api/v1/me`
  (`contract`, `version`).
- `fixtures/`: what the server really answers, written by its own tests
  (`server/contract_test.go`) from the handlers and structs that serve a real client, and held to
  them byte for byte afterwards.

The revision is **4**, first spoken by **4.0.0**. 3 was 3.6.0 to 3.8.0, when the plugin was
VillaNET, `ceo.villa.villanet`; 2 was 3.5.0.

### What changed in revision 4

1. **The id.** Every route is under `/plugins/ceo.villa.matty/`, and the events are
   `custom_ceo.villa.matty_streams`, `_me`, `_accounts` and `_sound`. A client written for revision
   3 reaches and hears nothing of 4.0.0. Share links posted from 4.0.0 are
   `<Site URL>/plug/ceo.villa.matty/clip/<id>` and `.../plan/<id>`; links posted before it name
   `ceo.villa.villanet` and stand for the same clips and plans.
2. **The server's name and artwork.** `GET /access`, `GET /me` and the `me` event carry `name`, what
   this server calls the plugin (never empty: "Matty" unless an admin named it), and `artwork`,
   `{"emblem": bool, "ghost": bool}`: whether there is an emblem and a No Signal ghost to show. The
   icon always exists. `<plugin>/static/brand/logo` and `/ghost` answer `404` while there is none.
3. **`X-VillaNET-Peek` is `X-Matty-Peek`** (the web panel's preview sessions).
4. **`people[id].activity.source` is `"panel"`** where it was `"villanet"`: the title somebody named
   in What's on?.
5. **The stream id written into WHEP answers is `stream`** (it was `villanet`; nothing reads it).
6. Refusal sentences name the server's name for the plugin, `name`: "Matty is locked. Unlock it
   with your bearer token." (`fixtures/refusal-locked.json`) is "Acme TV is locked. …" on a server
   named Acme TV. They are shown as sent, as before. The messages of shared clip and plan posts
   name it too ("🎬 Matty clip · 0:45", "📅 Matty plan"); posts made before keep the words they had.
7. **Files a person saves**: settings backups are of kind `matty-settings` (a restore still takes
   `villanet-tv-settings`), saved as `matty-settings.json` or `matty-settings-with-passwords.json`;
   the airtime export is `matty-airtime.json`; clips and drafts are `clip-<id>.mp4` and
   `draft-<id>.mp4`; a downloaded clip of a streamer with no name is named after the server's name.
8. **`bannerColor` in `GET /me` may be empty**: no colour chosen, and a client then uses its theme's
   accent.

Nothing else moved: every other route's shape and every other field are as in revision 3. Two things
were added, each with a section of its own below: [a signed release](#a-signed-release) and
[the app check](#the-app-check). From 4.1.0, a third: [push notifications](#push-notifications).
From 4.3.0, a fourth: the voice server's channels and more about each person, and talking in them
from Matty, in [voice](#voice). From 4.4.0, a fifth: a newer release of the plugin, for system
admins, in [plugin updates](#plugin-updates); and a sixth: the oldest Matty a server answers, in
[a minimum Matty](#a-minimum-matty).

**Historical identifiers.** Some names were chosen when the plugin was VillaNET and stay as they are,
because they are stored in posts and data that already exist and nobody is shown them: the post
types `custom_villanet_clip`, `custom_villanet_clip_copy`, `custom_villanet_plan` and
`custom_villanet_plan_copy`, and the post props beginning `villanet_` (`villanet_clip_id`,
`villanet_plan_id`, `villanet_share_nonce` and their kin). A client draws cards by them exactly as
before.

Revision 3 changed two things from 2: three events, `access`, `twitch_link` and `account_link`,
became one, `me`; and the boards, the medals, the fans, the achievements and everybody's choice of
awards left the snapshot, which carries what was decided from them instead (see "Awards and the
boards").

## Keeping the two in step

1. Change the server.
2. Write the fixtures afresh and read the difference like any other:
   `VILLANET_UPDATE_GOLDEN=1 go test ./server/ -run TestContract`
3. In Matty: `tools/sync-villanet-contract.sh`, then make its tests pass. Its `VillaNetContractTest`
   decodes every fixture with the models the app uses and fails while its copies differ from
   these; while something the app holds that the server did not send is not empty (a default that
   assumes what the server never said); and while the server sends a field the app neither reads
   nor lists, with a reason, as not read.

**Raise `contractRevision`** (and `contractSince`, in `server/plugin.go`) when a client written for
the current revision would misread the new answers: a field removed, renamed, retyped or given
another meaning, a route or an event removed. **Never for an addition.** A client compares the
number for order: an app meeting an older contract asks nothing of that plugin and says which
version of the plugin it needs; an app meeting a newer one shows what it can read and says the app is the older
of the two. So raising the number puts every installed app out of step until it is updated.

No fixture holds a secret (no bearer token, no provider key, and a stream key only in the caller's
own `/me`), and no snapshot or event fixture holds anything of Steam or Hytale: the test that
writes them checks.

## What a client reads

Base: `<Site URL>/plugins/ceo.villa.matty/api/v1`. Every request the client itself decides to
make carries `Authorization: Bearer <session token>` and `X-Requested-With: XMLHttpRequest`.

| Read | For | Notes |
| --- | --- | --- |
| `GET /access` | anybody signed in | `{access, configured, contract, version, name, artwork, release, app}`. The one read about the reader every screen needs, and the one a player repeats while it plays. `name` is what to call the plugin wherever a person reads it; `artwork` says whether to draw an emblem and a ghost; `release` is `{state, version}`, whether this plugin is a signed release (see "A signed release"); `app` is what the app check is set to and found for this session (see "The app check"), and from 4.4.0 `app.minimum` names the oldest Matty this server answers (see "A minimum Matty"); from 4.1.0, `push` says whether this server takes push registrations, with the key to register with (see "Push notifications"); from 4.3.0, while voice is on, `voice` says whether a Matty may talk in its channels (see "Voice"); from 4.4.0, for system admins only, `update` names a newer release of the plugin and says whether it can be installed from here (see "Plugin updates"). `access` is `linked`, `letIn`, `locked` or `unknown` (the store did not answer: take nothing away, ask again). |
| `GET /release` | anybody signed in | `{state, version, reason?, manifest?, rsa?, mldsa87?}`: whether this plugin is a signed release, and the release's manifest and both signatures for a client to verify for itself. See "A signed release". |
| `POST /app/challenge` | anybody signed in, 10 a minute | `{challenge, expires}`: a challenge for this session's app check, good for five minutes and for one answer. See "The app check". |
| `POST /app/attest` `{challenge, origin, chain, claims}` | anybody signed in, 10 a minute | `{state, reason?, development?, versionCode?, expires}`: the verdict for this session. See "The app check". |
| `GET /streams` | anybody signed in | The snapshot. Answers with an `ETag`; sent back as `If-None-Match` it answers `304` when nothing a person could see has changed. |
| `GET /boards` | anybody signed in | The boards and everybody's record (below). `ETag` and `304` as above, and `304` to any tagged read before the server has published anything since it started. Read when somebody looks at a board or a profile, each minute while a board stays open, and when `snapshot.boards.version` moves (see "When to read it"); never on a timer of its own otherwise. |
| `GET /banners` | anybody signed in | Everybody's profile banner (below). `ETag` and `304` as above. Read when a profile is opened and `snapshot.banners.version` is not the `version` of the copy in hand. |
| `POST /me/banner` | anybody signed in who is a regular member (not a guest, not a bot), 6 in 10 minutes | The picture as the body, `Content-Type` `image/jpeg`, `image/png` or `image/gif`; from 3.8.0 `?crop=x,y,w,h` may say the part to show. `201 {v, type, crop?}`. |
| `PUT /me/banner/crop` | the same, 30 in 10 minutes, from 3.8.0 | `{"crop": [x, y, w, h]}` or `{"crop": null}`, and `"v"` may name the picture it was chosen for: the part of the banner one has now that shows. `200 {v, type, crop?}`; `409 conflict` when the banner is another picture now. |
| `DELETE /me/banner` | anybody signed in, never metered | `{ok: true}`, also when there was none. |
| `DELETE /admin/banner?user=` | a system admin, whatever the lock | `{ok: true}`, also when there was none. |
| `GET /accounts` | linked or let in | Everybody's Steam status and Hytale names, and what each person is doing (below). `ETag` and `304` as above. Nothing in it moves by itself. |
| `POST /me/link {token}` | anybody signed in, 5 per 15 minutes | `{streamKey, access, configured}`: the caller's access after the link. An empty token is `400 bad_request`. |
| `DELETE /me/link` | anybody signed in, never metered | `{streamKey: "", access, configured}`: the caller's access after the unlink. |
| `GET /me` | anybody signed in | The web panel's own document: the reader's stream key, connected accounts and panel settings, and `name`, `artwork`, `release` and `app` as on `/access`; for system admins, `releaseAlert` when the plugin's files are not as signed or its folder holds other files nobody signed, and from 4.1.0 `pushAlert`, a sentence, while push notifications are switched on here and Mattermost pushes to nobody (see "Push notifications"), and from 4.3.0 `voiceTalkAlert`, a sentence, while talking from Matty is switched on and its voice port is down (see "Talking from Matty"), and from 4.4.0 `updateAlert`, `{version, url}`, while a newer release of the plugin is known (see "Plugin updates"). `bannerColor` is `""` when no colour is set: draw the banner in the theme's accent. A native app has no use for it. |
| `GET /clips` | linked or let in | `{clips: [...]}`, newest first: each clip as `fixtures/clip.json`. `snapshot.clips.version` changes whenever a clip, a title, a picture, a count or when a clip goes does: a client keeps the list it has until that moves. |
| `GET /clips/clip?id=` | linked or let in | One clip. `410 clip_gone` once it has expired or been deleted, which is the only answer that means so. |
| `GET /clips/cards?ids=` | linked or let in | Up to 25 ids, comma separated: `{cards: {id: card or null}}`, `null` for a clip that has gone. A card says when its clip goes (`expiresAt`). |
| `POST /clips/vote?id=` `{up}` | linked | `{upvotes, voted}`. |
| `POST /clips/share?id=` `{channelId}`, `POST /schedule/share` `{id, channelId}` | linked | `201 {postId, channelId}` for a new post; `200` with the earlier post for the same share made in the last minute. |
| `POST /schedule/join` `{id, join}` | joining: linked; leaving: anybody signed in | `{ok: true}`. The plan in the snapshot says whether there is room (`full`). |
| `POST /whep` | linked or let in | The WebRTC offer as the body (`application/sdp`), the stream's handle in `X-Stream-Handle`; the answer is the SDP. The offer is read as Broadcast Box's own WebRTC library reads it, and must have exactly one audio and exactly one video section (a section turned down at port 0 counts, and is refused), each offered (a port other than 0, or port 0 with `a=bundle-only`) and each with its own first direction attribute `recvonly` or `sendrecv` (a section with none of its own is refused, whatever the session says). A data channel beside them is fine. A request with no handle or no body at all is `400 bad_request`. Any other offer, one that does not parse, one with a carriage return that does not end a line, and a body over 64 KiB, is `400 bad_offer` and is never passed on to Broadcast Box. |

Events (names after `custom_ceo.villa.matty_`):

| Event | To | Carries | Do |
| --- | --- | --- | --- |
| `streams` | everybody | `payload`: the snapshot as a JSON string; `tag`: its tag, bare (the `ETag` of `GET /streams` is the same tag in quotes) | Show it. Keep the tag for the next conditional read. |
| `me` | one person, or everybody | `name` and `artwork`, as `GET /access` gives them; nothing about anybody | What the server holds about the reader may have changed: their access (a link, an unlink, a let-in), or one of their own connections (Twitch, Steam, Hytale); or, sent to everybody, the server's name for the plugin or its artwork, or the app check's setting or what it accepts (the certificates, the boot keys, the Site URL; a Matty then proves itself again if its verdict no longer counts), or, from 4.4.0, the oldest Matty this server answers. Ask again: `GET /access`, and for a client that shows connections, `GET /me`. It never says what changed, so it can never be what unlocks anybody. |
| `accounts` | everybody | nothing | If the reader may watch, `GET /accounts` again. Sent at once for the first change of a quiet ten seconds, and at most once in any ten. |
| `sound` | each person in voice | `busyUntil`: when the next sound may start (Unix ms) | The web panel's soundboard greys its tiles until then. It names no sound and no person; a client with no soundboard ignores it. |

Pictures and video are loaded, not asked for: a native client sends them the session token only,
to the signed-in server's exact origin, and never `X-Requested-With` (duty 1). `v` is a version from
the snapshot (`stillVersion`, `thumbnailVersion`, `pictureVersion`, `previewVersion`), from the
clip (its picture's) or from `GET /banners`, there only to tell a cached copy from a new one.

| Load | For | Notes |
| --- | --- | --- |
| `GET /stills?handle=&v=` | linked or let in, and system admins | A live card's preview picture. |
| `GET /channel/picture?handle=&v=`, `GET /channel/thumbnail?handle=&v=` | linked or let in | A 24/7 channel's picture and its thumbnail. |
| `GET /twitch/preview?handle=&v=` | linked or let in | A Twitch card's picture. |
| `GET /clips/thumb?id=&v=`, `GET /clips/video?id=` | linked or let in | A clip's picture, and its video (`HEAD` and ranges too). |
| `GET /media/image?ref=&size=` | anybody signed in | A title's art (`size` is `thumb`, `cover` or `backdrop`). |
| `GET /banner?user=&v=` | anybody signed in | Somebody's profile banner, `v` from `GET /banners`. Kept for a year: another picture is another `v`. |
| `<plugin>/static/brand/icon`, `<plugin>/static/brand/logo`, `<plugin>/static/brand/ghost` | anybody, no session needed | The icon (always there: Matty's own unless an admin uploaded another), and the emblem and the No Signal ghost, which exist only when `artwork.emblem` and `artwork.ghost` say so and answer `404` otherwise. |

## A signed release

An addition to revision 4: `release` in `GET /access` and `GET /me`, and `GET /release`. A client
that does not read them loses nothing.

A release of the plugin is signed with Matty's own two keys, the ones the app is signed with: RSA
4096 and ML-DSA-87. The bundle carries `release/manifest.txt` and, beside it, the two signatures
over its exact bytes. The plugin checks its own folder once when it starts, and says what it found:

- `signed`: both signatures verify against the two certificates built into the plugin, the
  manifest is for this version and this server's platform, every file it lists is on disk with its
  size and SHA-256, and it lists `plugin.json`, this platform's executable and the web panel's
  bundle. There is no `plugin.yml` or `plugin.yaml` (Mattermost reads either in place of
  `plugin.json`), and nothing it does not list is in the web panel's bundle's folder (Mattermost
  serves every file there) or in the `public` folder (Mattermost serves every file there to
  anybody). Other files on disk that it does not list are told to system admins and are no
  failure: Mattermost does not read them.
- `unsigned`: there is no `release` folder. Every development build is unsigned, and so is any
  build somebody makes of their own (`fixtures/release-unsigned.json`).
- `altered`: there is a `release` folder, and something about it, or about the files, is not as
  signed. `reason` says what.

It is evidence, not a lock: the plugin runs in every state, and a server program that was altered
can skip its own check. What it shows is which release a server presents, not what code it runs.

`GET /release` answers `{state, version}`, and, whenever there is a release folder, what is in it
as found: `manifest` (the text), `rsa` and `mldsa87` (each signature, base64). `reason` is there
when the state is `altered`. A client verifies for itself and never takes `state` for it:

1. Both signatures, over the bytes of `manifest` exactly as sent, before anything in it is read.
   `rsa` is RSASSA-PSS with SHA-512, MGF1 with SHA-512 and a 64-byte salt, 512 bytes (on Android,
   `SHA512withRSA/PSS`); `mldsa87` is pure ML-DSA-87 (FIPS 204) with the empty context, 4627 bytes
   (`ML-DSA-87`). Each is checked against a certificate the client holds itself, never one that
   travels: SHA-256 `E5:52:50:BE:A6:69:00:39:B7:84:9E:33:B0:49:08:8E:CB:B9:98:D9:F2:0A:AA:8C:7E:B2:DC:5A:43:63:27:3C`
   (RSA) and `75:78:CB:63:B4:3E:CC:6F:B1:27:64:51:54:F5:22:FB:15:D1:3A:26:08:E4:59:BC:F4:2A:7C:D1:41:86:A9:49`
   (ML-DSA-87). Either one failing or missing is a failure.
2. Then the manifest, strictly: printable ASCII and line feeds, ending in one, and in this order

       matty-release-manifest 1
       product ceo.villa.matty
       kind plugin
       version 4.0.0
       platform linux-amd64
       files 4
       <sha256> <size> assets/icon.svg
       ...

   `product` and `kind` exactly so, `version` the `version` the server reports, `platform` one or
   more platforms sorted and joined by commas, `files` the number of lines after it. Each line is
   a lower-case SHA-256, a size in bytes with no leading zero, and a path, sorted by byte value,
   each path once. The first line names the format and is what keeps these signatures from meaning
   anything else.

The SHA-256 of the manifest is the release's identity. `build/sign-release.sh` makes a signed
release and `build/checkrelease` checks one, with the plugin's own code.

## The app check

An addition to revision 4: `app` in `GET /access` and `GET /me`, `POST /app/challenge`, `POST
/app/attest`, and the refusal `app_unverified`. A client that does not read them loses nothing while
the server's setting is `off` or `report`.

Matty proves to the server, on Android's word rather than its own, that it is the official build: a
key made in the phone's Keystore with a challenge from the server, whose attestation says which app
asked for it, which certificate signed that app, and whether the phone's bootloader is locked and its
system verified. The verdict is the session's: another session of the same person, or the same
session after a new sign-in, proves itself again.

`app` is `{check, state, reason?, development?, versionCode?}`:

- `check` is the server's setting: `off` (nothing is checked, and the two routes answer `403`),
  `report` (the default: checked and shown) or `require` (a Matty that has not passed is refused).
- `state` is `verified`, `unverified` (checked and failed: `reason` says why, in the server's
  words) or `none` (never checked in this session, or the verdict has expired, or it was given for
  what the server no longer accepts, or `check` is `off`). A verdict of verified is held, each time
  it is read, to what it was given for: once its signing certificate or the phone's own boot key is
  taken off the admins' lists, or a plugin release carrying another copy of Google's revocation
  list is installed, it is `none`. A verdict of unverified counts only under the rules it was
  judged by: once what the server accepts changes in any way (a certificate or a boot key listed or
  taken off, another copy of the revocation list, the Site URL), it is `none`; the setting `check`
  is not one of those rules. Either kind of verdict that stops counting is `none` with the reason
  "This server's rules for the app check have changed, and this Matty is to be checked under the
  new ones." When the setting `check`, the certificates or boot keys it accepts, or the Site URL
  change, the server sends the `me` event to everybody; a Matty then asks `GET /access` again and
  proves itself if its verdict no longer counts.
  `reason` may also come with `none` when the server could not read its records just now, and,
  from 4.5.0, with `check` at `require` and a minimum in force (see "A minimum Matty"), to a client
  whose `User-Agent` says a version that meets the minimum while its session's pass was proved with
  an older version, or with none kept: then `state` is `none`, with no `versionCode`, and the
  reason is the refusal's sentence, so that the client proves itself again.
- `development`, with `verified`: only the development build of the plugin would pass it (an
  emulator, a debug build). A client says so beside the verdict.
- `versionCode` is the app's version code as Android attested it. A verdict lasts 30 days or until
  the session ends, whichever is sooner; a client proves itself again when `state` is `none` and
  `check` is not `off`, and when its own version code is not `versionCode`.

Proving itself (`fixtures/app-challenge.json`, `app-attest-verified.json`,
`app-attest-unverified.json`, `access-app-verified.json`):

1. `POST /app/challenge` (no body) answers `{"challenge": "<base64 of 32 bytes>", "expires": <ms>}`.
   A session may hold three at once; a fourth replaces the oldest.
2. The attestation challenge is the SHA-256 of the UTF-8 text

       matty-app-check 1
       <origin>
       <user id>
       <challenge>

   lines joined by a single line feed, with none after the challenge, which is the base64 exactly as
   sent. `origin` is the address the client reaches the server at, as `scheme://host[:port]`: lower
   case, the port left out when it is the scheme's default (80, 443), no path, and the host as
   OkHttp's `HttpUrl.host` writes it: an IPv6 address in brackets in its shortest form (RFC 5952),
   one that maps an IPv4 address as that IPv4 address, and a name that is not ASCII in its `xn--`
   form (`https://bücher.example` is `https://xn--bcher-kva.example`). For `https://chat.example.com`, user `uid-alice` and challenge
   `AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=` it is
   `c398158673574461506bf44b65ed7a43ef8322a0804efb3b2ee73c94e475e6ab`.
3. The client generates an EC P-256 signing key in the Android Keystore with that as its attestation
   challenge (StrongBox where the phone has it), reads the key's certificate chain, deletes the key,
   and sends `POST /app/attest` with `{"challenge": "<as sent>", "origin": "<as in 2>", "chain":
   ["<base64 DER>", ...], "claims": {...}}`, the chain leaf first, as Keystore gives it. `claims` is
   what the app reads of itself (`package`, `versionCode`, `currentSigner`, `history`, `hybrid`):
   kept beside the verdict for admins as the app's own word, and never decided by.
4. The server spends the challenge (once, for this session, within its five minutes: otherwise `409
   conflict`, and the client asks for a new one), holds `origin` to its own Site URL's, derives the
   challenge again from its own origin and the session's user, and reads the chain. The answer is
   `200` with the verdict whether it passed or not: `{"state": "verified"|"unverified", "reason"?,
   "development"?, "versionCode"?, "expires": <ms>}`. A body that is not such a request is `400`.

What passes, in this order: the origin is the Site URL's; the chain ends in a root whose key is one
of Google's two hardware attestation roots (by its SubjectPublicKeyInfo SHA-256: `feb2ea75...fbae`
for the RSA root, every issue of it, and `3ee44512...07ec` for the ECDSA one) and that signed
itself; from that root down, every certificate is signed by the next, and every one that signs is
a certificate authority (but the attestation key's own on a factory-provisioned chain, which some
phones were given without the flag); and only the first certificate carries an attestation; no
certificate between the first and the root has expired (on a remotely provisioned chain) or is not
yet valid; none is on Google's list of revoked attestation certificates as the plugin carries it;
the attestation is a version the server reads (2, 3, 4, 100 to 500, and later hundreds); both
security levels are TrustedEnvironment or StrongBox; the challenge is the derived one; the key was
generated in the secure hardware; the secure hardware's root of trust says the bootloader is locked
and the boot state is Verified, or SelfSigned with a verified-boot key the server accepts
(GrapheneOS's, built in, and any an admin adds); the app is exactly one package, `ceo.villa.matty`,
signed with exactly one certificate, Matty's (`E5:52:50:BE:...:27:3C`, the RSA certificate: Android
attests the first certificate of an app's signing history, never the ML-DSA one) or one an admin
adds; and the key is not one every app may use.

With `require`, every request whose `User-Agent` begins `Matty/` and whose session has not passed is
refused on every route but `GET /access`, `GET /me`, `GET /release`, the two above,
`DELETE /me/push` (taking its own push registration back always works) and, from 4.3.0,
`POST /voice/leave` (leaving voice always works), with `403` and
`app_unverified` (`fixtures/refusal-app-unverified.json`); the sentence says why. Requests that
do not say they are Matty, the web panel's among them, are never refused by it. The check recognises
the official app; it cannot wall out a program that hides what it is.

## A minimum Matty

An addition to revision 4, from 4.4.0: `minimum` in the `app` of `GET /access` and `GET /me`, and
the refusal `app_outdated` (`fixtures/access-app-minimum.json`, `fixtures/refusal-app-outdated.json`).
Only a client whose `User-Agent` begins `Matty/` is ever refused by it.

`app.minimum` is the minimum in force, for everybody, whatever `app.check` is, and is left out
while there is none. An admin sets **Minimum Matty version** (under **App check**) to no minimum, to
a version `N.N.N`, which is then the minimum, or, from 4.5.0, to the latest release of Matty: the
newest release on GitHub that the server knows, which it looks for once a day and when an admin
asks, and none until it knows one. A setting that is neither counts as none. When a look finds
another version, every node answers it within seconds, and the server sends the `me` event to
everybody once. The server reads a Matty's version from its
`User-Agent`: what follows `Matty/` up to the first space, with a `-…` or `+…` ending left out
(`Matty/0.1.32-debug (Android 17; Mattermost client)` is 0.1.32), compared with `minimum` part by
part. A request whose `User-Agent` begins `Matty/` with a version older than `minimum`, or with no
version that can be read, is refused on every route of the plugin's API but the ones the app check
leaves open (`GET /access`, `GET /me`, `GET /release`, the two app check routes, `DELETE /me/push`
and `POST /voice/leave`), before the app check is asked, with `403`, `Cache-Control: no-store` and
`app_outdated`: "This server needs Matty 0.1.32 or newer. You have 0.1.31.", without the second
sentence when the version cannot be read. The static files and the Twitch, Steam and Hytale connect
pages and their callbacks are outside this gate and the app check's alike. A request that does not
say it is Matty, the web panel's among them, is never refused by it.

When the setting changes what is in force, the server sends the `me` event to everybody, and a Matty
asks `GET /access` again. A voice session keeps the version its `POST /voice/join` said; when a
minimum comes into force or changes, every session joined from a Matty that does not meet it ends
with `end` `off` and the refusal's sentence as `reason`, and a session joined by a client that did
not say it is Matty stays. A join from such a Matty that was still connecting to the voice server
then ends its session the same way and is answered as the gate answers: `403` `app_outdated`, with
the sentence and `Cache-Control: no-store`. A Matty older than `minimum`, told so by `GET /access` or by the refusal,
closes that server until it is updated, and reads `GET /access` again to know when it may open it,
since an admin may lower or clear the minimum.

The minimum stops the plugin's routes and voice, and nothing else: a push registration made before
it keeps receiving pushes, and the `streams` event, sent to every open client, still reaches an old
Matty, which cannot play from it. With `app.check` at `off` or `report`, the version is the
client's own word: a changed copy can say any version, or not say it is Matty at all, and then
neither the minimum nor the app check refuses it.

From 4.5.0, with `app.check` at `require`, the minimum also goes by what the phone proved. A
verdict keeps the version the client's `User-Agent` said in its `POST /app/attest`, beside the
attested `versionCode`; a pass kept without it counts as given under rules that have changed (state
`none`), so that the client proves itself again. After the two refusals above, a request saying it
is Matty from a session whose pass carries a version older than `minimum`, or none, is refused with
`403`, `Cache-Control: no-store` and `app_unverified`: "Matty answers only the official Matty app,
and this copy has to be checked again: this server needs Matty 0.1.33 or newer, and this phone last
proved Matty 0.1.32." (the server's name for the plugin first, and without the last part when no
version was kept). To a Matty whose `User-Agent` meets the minimum while its session's pass is
refused this way, `GET /access` and `GET /me` say `none`, with that sentence (see "The app
check"). A session whose pass carries a version that meets `minimum` passes whatever its
`User-Agent` says now, as long as the first refusal lets it through, and is told `verified`. A
voice session held by a join from a session whose pass is refused this way ends with `end` `off`
and that sentence when a minimum comes into force above what it proved, and a join is refused as
the gate refuses. A client that was just updated proves itself
again because its version code is no longer `versionCode`, and passes.

`GET /admin/appcheck` is the System Console's card, for system admins (`403` otherwise): the
setting `check`, `minimum` while one is in force, `minimumFrom` (`setting` or `latest`, left out
for none), `latest` while the setting is the latest release (`{version?, lookedAt?, trouble?,
troubleAt?}`, times in ms, the trouble left out from the next look that works), `byWord` (true
while a minimum is in force and `check` is not `require`), `problem`, and `people`, each with its
sessions' verdicts, `said` among them, the version kept with the verdict. From 4.5.0,
`POST /admin/appcheck/latest` looks for the newest Matty at once and answers the same: `409
conflict` while the setting is not the latest release, `429 rate_limited` within a minute of the
last look anybody asked for (`retryAfterMs` is what is left of that minute, at least 1000), `503
store` when the store did not answer.

## A refusal

Every refusal, from every route, is one body (`fixtures/refusal-*.json`):

```jsonc
{
  "error": "Too many tries. Wait a few minutes, then try again.",  // a sentence a person can read
  "code": "rate_limited",                                          // a stable word for what was refused
  "retryAfterMs": 180000                                           // only with a refusal that is about time
}
```

- **`error` is the wording.** A client shows it as it is sent. It keeps words of its own only for no
  answer at all (no network, the plugin restarting). For the link, playback and the schedule, neither
  client keeps a table of sentences by code: the lock screen and Matty each did, word for word, and
  the server now says those sentences itself. The web panel still words its clip and soundboard
  refusals itself (`clipErrorCopy`, `refusalLine`), and shows the server's sentence only for a code it
  has no words for: kept in this release so that it reads as it did.
- **`code` is always there**, and is what a client acts on. A refusal whose handler names none
  carries its status's: `bad_request` (400), `signed_out` (401), `forbidden` (403), `not_found` (404),
  `method_not_allowed` (405), `conflict` (409), `gone` (410), `too_large` (413), `rate_limited` (429),
  `unreachable` (502), `unavailable` (503), `failed` (anything else).
- **`retryAfterMs`** is how long until asking again can work, read from the meter that refused.
  Left out when the refusal is not about time.

The codes a client of the routes above meets:

| Code | From | Means |
| --- | --- | --- |
| `app_outdated` | every route of the plugin's API but `GET /access`, `/me`, `/release`, the two app check routes, `DELETE /me/push` and `POST /voice/leave`, to a request saying it is Matty, while a minimum Matty version is set, from 4.4.0 | This Matty is older than the server's minimum, or says no version that can be read; the sentence names the version needed. Asking again works once the app is updated or the server's minimum allows it: read `GET /access` again (see "A minimum Matty"). |
| `app_unverified` | every route but `GET /access`, `/me`, `/release`, the two app check routes, `DELETE /me/push` and `POST /voice/leave`, to a request saying it is Matty, while the app check is `require` | This session's Matty has not passed the app check: not checked yet, or checked and failed (the sentence says which); or, from 4.5.0, it passed with a version older than the minimum in force, or with none kept ("… has to be checked again: this server needs Matty 0.1.33 or newer, and this phone last proved Matty 0.1.32."). Prove it (see "The app check") and ask again; a Matty that failed for what it is (another certificate, an unlocked phone) fails again. |
| `locked`, `watch_only` | any route that is not open | The reader may not do this: not let in at all, or let in to watch where a link is needed. Ask `GET /access` again (duty 3). |
| `store` | any | The server could not read or save just now (its app check records among them, while the app check is `require`), or, from `PUT /me/push`, its confirm and its test, push is switched on but the server's push key is not ready yet. Worth trying again: a client keeps what it still has to send. |
| `rate_limited` | any metered route | Too often. `retryAfterMs` says when. Never from `DELETE /me/link`, `DELETE /me/banner`, the Twitch, Steam and Hytale disconnects (`DELETE /twitch/link`, `/steam/link`, `/hytale/link`) or dismissing their notices, which no meter stands in front of. Leaving or cancelling a plan, clearing What's on?, going back to Automatic and removing your own sounds share their route's meter (the schedule's, What's on?'s, the award picker's, the soundboard's edits), whose wait is seconds and is given in `retryAfterMs`. |
| `bad_request`, `too_long`, `not_accepted`, `bad_name`, `unreachable`, `not_configured` | `POST /me/link` | The token was unreadable, too long, not Broadcast Box's, for a stream name that cannot be used, could not be checked, or the plugin is not set up. |
| `not_live` | `POST /whep` | No such stream on the air now. A player may ask again in a moment. |
| `bad_offer` | `POST /whep` | The offer is not one Broadcast Box can serve (the `POST /whep` row says exactly what is refused), or is over 64 KiB. Asking again with the same offer never works. |
| `unreachable`, `not_configured` | `POST /whep` | Broadcast Box did not answer, or the plugin is not set up. |
| `bad_image`, `too_large`, `forbidden` | `POST /me/banner` | Not a JPEG, a PNG or a GIF that is what its `Content-Type` says, or with a side under 64 or over 2400 pixels, or a GIF with too many frames or pixels; over 2 MiB (8 MiB for a GIF); a guest account or a bot. Asking again with the same picture never works. |
| `bad_crop` | `POST /me/banner`, `PUT /me/banner/crop` | The crop is not one: not four plain decimals, outside the picture, too thin, far from three wide by one high on the picture, or (for the `PUT`) a body that is not exactly `{"crop": …}` with at most a `"v"` beside it. The rules are under "Profile banners". Asking again with the same crop never works. |
| `not_found` | `GET /banner`, `PUT /me/banner/crop` | Nobody's banner at that `user` and `v`: none, or not that one any more. For the `PUT`: the reader has no banner whose crop could change. |
| `conflict` | `PUT /me/banner/crop` | The body names, by its `v`, a picture that is not the reader's banner any more: another device set a new one while this part was being chosen. Nothing changed. Asking again with the same `v` never works; read `GET /banners` again and choose a part of the new picture. |
| `clip_gone` | the clip routes | The clip has expired or been deleted (`410`). |
| `gone`, `full`, `own_plan`, `busy`, `not_yours` | the schedule routes | The plan is no longer there, has no room, is the caller's own, is being changed by too many at once, or is somebody else's to change. |
| `unavailable`, `not_found`, `voice_denied`, `voice_refused`, `conflict`, `bad_request`, `too_large` | the voice routes, from 4.3.0 | Talk is off or the voice server cannot be reached; no such channel; the voice server kept the reader out of the channel; the voice server refused the reader's connection; not in voice. See [Talking from Matty](#talking-from-matty). |
| `conflict`, `rate_limited`, `store`, `stopping`, `unreachable` | `POST /admin/update/bundle`, from 4.4.0 | Updates are off, no newer version is known or it cannot be installed from here; asked for too soon, or one is being prepared; the store did not answer; the plugin stopped while it got the bundle; the download or the check failed. Each sentence is to be shown as it is. See [Plugin updates](#plugin-updates). |

(The clips and the soundboard have more codes of their own, for routes only the web panel calls.)

## The people index

`snapshot.people` is always sent, `{}` when there is nobody to say anything about:

```jsonc
"people": {
  "<Mattermost user id>": {
    "live":  "<handle>",            // on the air; the handle of the card that stands for them
    "voice": "open" | "muted",      // in voice; muted only when every one of their sessions is
    "badge": "live" | "birthday"    // the custom status beside their name is the plugin's, not theirs
  }
}
```

Each field is absent when it does not apply, and somebody with none of them is not in the index.
(A fourth, `awards`, is in "Awards and the boards".) The server works them out once, where
everything they depend on is known (`server/people.go`), and that is the only definition there is:

- **Live** is one card each: never a 24/7 channel, a paused stream or a Twitch entry their own
  stream covers; their Broadcast Box stream before their Twitch one. The red badge beside a name
  is set by the same rule.
- **A client looks people up and draws.** It does not work out who is live from the cards, or who
  is in voice from the roster: the cards and the roster are for drawing the list and the voice
  section.
- **`badge: "live"`** means the person's custom status ("🔴 Live now") was put there by the plugin
  while they broadcast. A client that says what somebody is doing (a line under their name) leaves
  that status out: it only repeats the LIVE ring. `"birthday"` is the cake, which is theirs to show.

## What somebody is doing

`GET /accounts` says it, for each person there is something to say about, and nothing else does:

```jsonc
"people": {
  "<Mattermost user id>": {
    "steam":    { "name": "...", "url": "...", "state": "online", "inGame": true, "game": "ELDEN RING" },
    "hytale":   { "name": "..." },
    "activity": { "kind": "playing" | "streaming", "title": "ELDEN RING", "source": "steam" | "panel" | "twitch" }
  }
}
```

- **`activity` is the line under a name**: "Playing ELDEN RING", or "Streaming ELDEN RING" when the
  person's own stream is of that very game, or "Streaming Quantum of Solace" when they are live and
  have tagged a movie or a show in What's on?. The server decides it once
  (`server/accounts_activity.go`): the game Steam reports, against what the card that stands for them
  on the air says it is showing (What's on?, or Twitch's category). Where What's on? was picked from
  Steam it is matched by Steam's app id, otherwise by name. While they are live, a movie or a show
  in What's on? (anything it names that is not a game) is the line, with `source` `"panel"`,
  whatever game Steam reports for them at the same time: the streamer has said what is on. Its
  `title` is the title looked up when one was picked, otherwise the words as typed. `title` is `""`
  only with `"playing"`, for a game Steam does not name. `source` is who names the title. (Added in
  3.6.1, under revision 3, as `"villanet"`; `"panel"` from revision 4. Until 3.6.1 a movie on left
  the line to Steam, or to nothing.)
- **A client prints it.** It does not put Steam's game and the stream's tag together itself, and it
  does not compare game names.
- Somebody connected to neither service, on the air with a game, a movie or a show on their card, is
  in `people` for `activity` alone.
- It is in `GET /accounts` because part of it is Steam's, and whatever is derived from Steam is for
  readers who may watch: it is never in the snapshot. When a stream changes it (somebody goes live, or
  picks, changes or takes down a game, a movie or a show), the `accounts` event says to read again, as when Steam does. That event is sent at
  most once in ten seconds, so after a stream starts or ends the line can follow the LIVE ring by up
  to ten seconds.
- Somebody whose Mattermost account is deactivated has no line, connected to a service or not, from
  the moment the server knows of it: at once when they are deactivated while the plugin runs, and
  otherwise at the accounts loop's next look (a minute at most), which runs only while Steam or
  Hytale is on. Let back in, they have it again from the next look: the accounts loop's, or, with
  both services off, the moment their stream next starts.

Two more things the snapshot says so that no client works them out:

- **`schedule[].full`**: a plan with no room for anybody else, by the server's own count of the places
  taken. A client that counted the names in `joined` would be wrong when one of them is somebody
  whose account has gone, who is left out of the names and still holds a place.
- **A clip card's `expiresAt`** (`GET /clips/cards`): when the clip goes, by the retention in force
  when the server answered. Matty says "expired" by it, in both directions. The web panel takes it
  when it is earlier than its own reckoning, but still says "expired" without asking once the clip's
  age passes `clips.retentionDays`, as it always did: so a clip kept through the grace after the
  retention is lowered can read expired in a post card there while the server still serves it.

## Awards and the boards

Who holds which award, which of them go beside a name, and what a profile says are decided on the
server (`server/awards_decide.go`), where the boards, the medals, the fans, the achievements and
everybody's choice are in hand. A client draws what it is given: it maps an award to its artwork,
writes a figure the reader's way (seconds as a duration, `YYYY-MM` as a month), and works out
nothing about who holds what.

**In the snapshot**, because every screen with a name on it needs them at once:

```jsonc
"boards": { "version": "3f2a…" },              // when to read GET /boards again: see below
"people": {
  "<Mattermost user id>": {
    "awards": [                                 // beside their name, in this order, at most four
      { "id": "alltime", "kind": "alltime", "rank": 2, "label": "2nd for airtime, all time" },
      { "id": "ach:streamed", "kind": "ach", "family": "streamed", "tier": 3, "label": "Streamed 100 hours" }
    ]
  }
},
"streams": [ { "handle": "…", "awards": [ … ] } ]   // the same, on a Live now card
```

- `people[id].awards` is the person's own choice, or Automatic for somebody who never chose (the
  crown, this month's calendar, the Top Snooper's binoculars and the Best Clipper's clapperboard,
  whenever held). An award chosen and not held now is left out. Nobody's choice itself leaves the
  server, only what it comes to.
- `streams[].awards` is a card's set: the person's choice with the card's own places, or, on
  Automatic and for a card with no person, the crown and the calendar at the card's places and the
  binoculars on the Top Snooper's own Broadcast Box card. A 24/7 channel has none.
- `kind` picks the artwork: `alltime` (a crown) and `month` (a calendar) with `rank` 1 to 3;
  `snooper`; `clipper`; `past` (a past month's medal, `rank` its best place); `fan`; `ach` (an
  achievement's coin, by `family` and `tier`). `label` is what it says to a screen reader.
- The clapperboard beside "Clipped by" is drawn for whoever has a `clipper` award in
  `people[id].awards`: beside the name is beside the name.

**`GET /boards`**, for whoever is looking at a board or a profile (`fixtures/boards.json`):

```jsonc
{
  "version": "3f2a…",
  "allTime": [...], "allTimeTotal": 12, "month": [...], "monthTotal": 7, "monthLabel": "November 2026",
  "monthMedals": [...],
  "watchBoards": true, "watchAllTime": [...], "watchAllTimeTotal": 9, "watchMonth": [...], "watchMonthTotal": 4,
  "bestClipper": {...}, "fans": [...], "fansOn": true,
  "achievementKinds": [...], "achievements": [...],
  "people": {
    "<Mattermost user id>": {
      "rows": [                                 // their whole record, as their profile shows it, in this order
        { "kind": "board", "id": "alltime", "rank": 2, "title": "Runner-up", "board": "All time", "seconds": 1483200 },
        { "kind": "board", "id": "snooper", "rank": 1, "title": "Top Snooper", "board": "All time", "seconds": 90000, "doing": "watching others" },
        { "kind": "clipper", "id": "clipper", "rank": 1, "title": "Best Clipper", "upvotes": 142, "clips": 9 },
        { "kind": "fan", "id": "fan:<streamer id>", "rank": 1, "title": "Biggest fan of Dave", "streamerId": "…", "streamer": "Dave", "hours": 41 },
        { "kind": "achievements", "id": "achievements", "rank": 0, "title": "Achievements", "steps": [ { "id": "streamed", "tier": 3, "name": "Streamed 100 hours" } ] },
        { "kind": "months", "id": "past", "rank": 1, "title": "Monthly champion", "gold": ["2026-08", "2026-03"], "tallies": [ { "rank": 3, "months": ["2026-02"] } ] }
      ]
    }
  }
}
```

- Everything above `people` is as the snapshot carried it until 3.6.0, under the same names. Each is
  left out while its feature is off, as before.
- `people[id].rows` is the whole record whatever the person shows beside their name, for everybody
  who holds anything; `people` is `{}`, never absent, when nobody does. A row carries the facts of
  its own `kind` and nothing formatted. `rank` is the place that tints the row (0 for the
  achievements, which have none). Months are newest first.
- **When to read it.** When a board or a profile is opened, with the tag of the copy in hand; again
  each minute while a board stays open (the web panel's Trophies card, Matty's Boards tab), since
  the seconds on a board tick, and not while a profile does; and whenever
  `snapshot.boards.version` is not the `version` of the copy in hand. A client that draws from the
  boards outside a board or a profile reads them as soon as the first snapshot names a version: the
  web panel does, as its page starts, because its header and its cards draw from them. Matty draws
  nothing from them until somebody looks, and reads nothing until then. That word moves when anything
  in the boards changes other than a board's seconds: a place, a name, a medal, a fan and their
  whole hours, a step, a vote for the Best Clipper. So nothing but seconds is ever behind in a copy,
  and a client with nothing open asks nothing while they tick.
- It is open to everybody signed in, as the snapshot is: a profile outside the panel shows
  somebody's record to every reader, as it always has. Nothing in it is Steam's or Hytale's.

`GET /awards/mine`, for a linked reader choosing what shows beside their own name, answers
`{show, readable, held}`: `held` is everything they hold now, in the order a picker lists it, each
as an award above with `short` (the words on its chip) and, where it applies, `wins` and `months`
(a past month's medal) or `streamer` and `hours` (a biggest fan).

## Profile banners

One picture a person, drawn across the top of their profile. The plugin keeps the pictures; Matty
sets and shows them. The web panel shows and sets nothing of it.

**In the snapshot**, only when to read them again:

```jsonc
"banners": { "version": "8dcbaf2c3cdc1382" }   // always there; moves whenever anybody's banner is set, replaced or taken away, or its crop changes
```

**`GET /banners`** (`fixtures/banners.json`):

```jsonc
{
  "version": "8dcbaf2c3cdc1382",               // the same word as snapshot.banners.version
  "people": {                                  // always there, {} when nobody has one
    "<Mattermost user id>": { "v": "dc6f296e288355d5", "type": "image/png" },
    "<another>": { "v": "dc6f296e288355d5", "type": "image/png", "crop": [0, 0.3333, 1, 0.3333] }
  }
}
```

- `v` is a fingerprint of the picture's bytes, 16 lower-case hex. It is public, not a secret: it
  only tells one picture from another. `type` is `image/jpeg`, `image/png` or `image/gif`.
- `crop`, from 3.8.0, is the part of the picture that shows: `[x, y, w, h]`, four numbers from 0 to
  1, fractions of the picture's own width and height, `(x, y)` the rectangle's top left corner.
  Drawn so that the rectangle fills the banner (a GIF keeps playing). Without it, the middle of
  the picture fills the banner, as before. It is written as plain decimals with at most four
  places and no trailing zeros (`0.5`, `0.3333`, `0`, `1`). `x + w` and `y + h` may come to
  `1.0001`, which rounding each number can make: draw no further than the picture's edge. A crop a
  client cannot use is to be treated as none. The fixture shows both: Alice's banner has a crop,
  the middle third of a square picture across its whole width; Bob's and Dave's have none. Setting,
  changing or clearing a crop moves the list's `version` and `ETag`, and `snapshot.banners.version`
  with them; `v` does not move, since the picture is the same bytes at the same address.
- **`GET /banner?user=<id>&v=<v>`** is the picture, exactly as it was sent: a GIF stays animated.
  It comes with its own `type` as `Content-Type`, `X-Content-Type-Options: nosniff`,
  `Content-Security-Policy: default-src 'none'; sandbox` and `Cache-Control: private,
  max-age=31536000, immutable`. It is loaded as a picture (duty 1). `404 not_found` once that `v`
  is not the person's any more.
- **`POST /me/banner`**: the picture's bytes as the body, under its `Content-Type`. At most 2 MiB
  for a JPEG or a PNG and 8 MiB for a GIF, each side from 64 to 2400 pixels (a GIF's logical
  screen). The server sniffs the bytes and decodes their header, and both must be what the
  `Content-Type` says; it decodes nothing more and re-encodes nothing. A GIF's blocks are walked,
  not decoded: at most 300 frames, covering at most 419,430,400 pixels between them, each inside
  the logical screen, and the file must reach its trailer. A new picture replaces the
  old, which is gone at once. `201 {v, type, crop?}`. Refused `400 bad_image`, `413 too_large`, `403
  forbidden` (a guest account or a bot), `429 rate_limited` with `retryAfterMs` (6 in 10 minutes a
  person), or `503 store` (a server whose files are not on its own disk keeps no banners).
- **`?crop=x,y,w,h`** on that `POST`, from 3.8.0: the part of this picture to show, kept with it
  (a comma may come escaped as `%2C`). Each number is a plain decimal from 0 to 1 with at most four
  places (`0`, `1`, `0.5`, `0.3333`; never `.5`, `1e0`, a sign, a space, `NaN` or `Infinity`). It is
  refused `400 bad_crop` when it is not four such numbers or is given twice, when `w` or `h` is
  under `0.05`, when `x + w` or `y + h` is over `1.0001`, or when the rectangle measured in the
  picture's own pixels (`w × width` by `h × height`) is under 2.5 or over 3.5 times as wide as it
  is high (exactly 2.5 and 3.5 are accepted). What the numbers are is checked before the meter,
  as the `Content-Type` is, and spends nothing; the shape is checked once the picture is read, and
  that refusal is an upload like any other refused. A picture sent without `?crop=` has no crop,
  also when the same picture had one: it is a change, as a new picture is. The answer carries the
  `crop` it kept.
- **`PUT /me/banner/crop`**, from 3.8.0: `{"crop": [x, y, w, h]}` changes the crop of the banner one
  has now, and `{"crop": null}` clears it, without sending the picture again. For a GIF above all,
  which a phone cannot cut; Matty cuts a still picture itself. The body may also name the picture
  the part was chosen for: `{"crop": [x, y, w, h], "v": "<v>"}` or `{"crop": null, "v": "<v>"}`,
  with `v` as the list writes it (16 lower-case hex digits), the two fields in either order. The
  body is exactly that object: `crop`, written once, its numbers by the same rules as the query's,
  and at most a `v`, written once, nothing else and nothing after it, at most 1 KiB; anything else
  is `400 bad_crop`, and so is a `v` that is not 16 lower-case hex digits. The same rules as the
  query's, measured on the stored picture's pixels. `200 {v, type, crop?}`, the banner as the list
  now says it. Asking for the crop it already has (or none when it has none) changes nothing and
  moves nothing. With a `v`, the crop is set only on that picture: when the reader's banner is
  another picture now (another device set it while the part was being chosen), nothing changes,
  the list's `version` and `ETag` stay, and the answer is `409 conflict` with a sentence to show;
  without one, the crop is set on whichever picture the banner is. Refused `404 not_found` with no
  banner (with or without a `v`), `403 forbidden` for a guest account or a bot, `429 rate_limited`
  with `retryAfterMs` (30 in 10 minutes a person, a meter of its own, not the upload's; any body
  read, refused or not, spends one), `503 store` as above.
- **`DELETE /me/banner`** takes the reader's own away, its crop with it, and **`DELETE /admin/banner?user=<id>`** a
  system admin's taking anybody's away. Both answer `{ok: true}`, also when there was none, and
  neither is metered.
- **When to read it.** When a profile is opened and `snapshot.banners.version` is not the `version`
  of the copy in hand, or there is no copy; with the tag of the copy in hand. Then load
  `GET /banner` for whoever has one. A client that never opens a profile reads nothing.
- Which plugin has banners: `GET /access` says the plugin's `version`; a client offers them from
  3.7.0, and the crop from 3.8.0. Both were additions to revision 3, and every 4.0.0 has them: a client that does not know
  `crop` keeps working, and shows the middle of the picture.

## Plugin updates

An addition to revision 4, from 4.4.0: `update` in a system admin's `GET /access`, `updateAlert`
in their `GET /me`, and `POST /admin/update/bundle`, the checked bundle a system admin's client
installs. A client that does not read them loses nothing.

`update` is `{version, url, install}` (`fixtures/access-update.json`) and `updateAlert` is
`{version, url}`. Each is there only while the server's **Look for updates** setting is on and a
release newer than the one running is known; for everybody else, and otherwise, it is left out.
`version` is the newer release, `N.N.N`; `url` is its page, which the server builds itself and on a
release is always `https://github.com/vvilla-01/matty-plugin/releases/tag/v<version>`; `install`,
always written, is whether a bundle of it can be had from this server (below). A client shows it to
the admin, says "Matty Plugin <version> is available" or the like, and opens the page only when the
address is a release's page there; anything else it shows as text. Both are read from the server's
memory: asking costs no store read. The `version` of `GET /access` and `GET /me` is the version
running.

`POST /admin/update/bundle`, for system admins (`403` otherwise), is the newer release the last
look found: downloaded from GitHub by the server, unpacked, checked against Matty's two
certificates exactly (every file listed, none added, the version and platform asked for) and packed
again from what was checked. It answers `200` with `Content-Type: application/gzip`,
`Content-Disposition: attachment; filename="ceo.villa.matty-<version>-<platform>.tar.gz"`,
`Content-Length`, `Cache-Control: no-store`, `X-Matty-Update-Version: <version>` and the archive.
A client uploads nothing but a `200` with a body of at least one byte whose
`X-Matty-Update-Version` is `N.N.N` and is the version it offered: another version means a newer
one was found meanwhile, and the client reads the offer again. The client uploads exactly those bytes to the same server's `POST /api/v4/plugins` (multipart,
`plugin` the file, `force` `true`) with the admin's own session, and then reads `GET /access` until
`version` is the new one. The plugin never installs anything itself. Refusals, in this order, each
with a sentence to show as it is:

| Status and code | When |
| --- | --- |
| `409 conflict` | **Look for updates** is off. |
| `409 conflict` | No newer version is known. |
| `409 conflict` | It cannot be installed from here: this plugin is not a signed release, the release has no bundle for this server's platform, or Mattermost does not take plugin uploads (**Enable Plugins** and **Enable Plugin Uploads** on, **Require Plugin Signature** off). The first that holds is said. |
| `429 rate_limited`, with `retryAfterMs` | A bundle was asked for less than a minute ago anywhere on the server (`retryAfterMs` is what is left of that minute, at least 1000; a bundle that failed still counts), or one is being prepared on this node. |
| `503 store` | The store did not answer. |
| `503 stopping` | The plugin stopped while it got the bundle: "The plugin stopped while it got the bundle. Try again once it is back." Never an empty `200`. |
| `502 unreachable` | The download or the check failed; the sentence is one of a fixed set and holds no word of the download. |

`GET /admin/update` and `POST /admin/update/check` are the System Console card's
(`fixtures/admin-update.json`): whether the setting is on (`enabled`), the running version, this
server's `platform` (such as `linux-amd64`, always there), the newest release seen and its page, when it last looked, the last trouble (`trouble` and `troubleAt`, left out from the next look that works or the next bundle handed out), and, while a newer one is
known, `install` (`{"ready": true}`, or `{"ready": false, "missing": [...]}` with every reason that
holds, in order: `unsigned`, `asset`, `uploads`) and `inVoice`, how many phones are in voice through
the plugin on that node (left out when none). `POST /admin/update/check` looks now: `409 conflict`
while the setting is off, `429 rate_limited` within a minute of the last look anybody asked for
(`retryAfterMs` is what is left of that minute, at least 1000),
`503 store` when the store did not answer. Both are for system admins alone, the panel's own.

## Push notifications

An addition to revision 4, from 4.1.0: `push` in `GET /access`, the routes `/me/push`,
`/me/push/confirm` and `/me/push/test`, the plaintexts below, and `pushAlert` in a system admin's
`GET /me`. A client that does not read them loses nothing. A client offers push from plugin 4.1.0
(the `version` in `GET /access`) and only while `push.state` is `on`.

When Mattermost decides to push to somebody (its own rules: notification preferences, status,
muting, its push contents setting), the server sends an end-to-end encrypted Web Push (RFC 8030;
RFC 8291 `aes128gcm`, one record of record size 4096, padded to a multiple of 1024 bytes of
plaintext; VAPID, RFC 8292) to the UnifiedPush address each of that person's sessions registered.
Each push is at most 4096 bytes, sent with `TTL: 86400` and `Urgency: high` whatever its type. A
`message` or `clear` push goes only when Mattermost decides to push, so only while its own push
notifications are switched on (`hello`, `test` and `ended` do not wait for them); pushes other
plugins make (Calls) are not sent. While an admin has push switched off, nothing at all is sent,
not even what was waiting to go.

`push` in `GET /access` is `{state, key?, reason?}` (`fixtures/access-push-on.json`, and
`access.json` for off): `state` is `on` or `off`; `key`, while `on`, is the server's VAPID public
key (the uncompressed P-256 point, base64url without padding, 87 characters), which a client gives
its distributor as `vapid` and the server as `key`; `reason`, while `off`, is a sentence to show
("Push notifications are switched off on this server.", "Push notifications are not ready on this
server yet. Try again in a moment." while the server's push key is not loaded, or why Mattermost
pushes to nobody, naming the System Console page). Reading it costs the server no store read.

A registration is the session's: the session that makes the request (another session of the same
person registers on its own; at most 8 a person, a ninth replacing the one least recently seen). It
lasts as long as the session, and goes when the session is over, or still cannot be read about a
minute after it first could not be, while other sessions can be (with one `ended` push first, to a
registration whose `hello` was confirmed), when the push server answers `404` or `410` (to a
`hello`, the registration waiting for it reads as `failing` instead), or when the client takes it
back. Mattermost answers a session that is no more and a store that fails alike, so while no
session can be read, none is taken as over, and nothing is sent to one that cannot be read. The
client need not do anything to keep it: the server's daily sweep writes a registration
again, once it is 30 days old, while its session stands. One the server has not written for 90 days
is gone (which takes the plugin being disabled for about two months), and `GET /me/push` then
answers `none`.

| Route | For | Notes |
| --- | --- | --- |
| `PUT /me/push` `{endpoint, p256dh, auth, key}` | anybody signed in; 6 in 10 minutes a person for a registration the server does not hold already | `endpoint` the UnifiedPush address (at most 1000 bytes; `https`, or `http` only to a push server the admin listed; no user name, no fragment); `p256dh` the client's P-256 public key and `auth` its 16-byte secret, both base64url without padding; `key` the `push.key` it registered with. The same four as the session's confirmed registration: `{"state":"confirmed",...}` and nothing sent. Anything else: `{"state":"pending",...}` (`fixtures/push-pending.json`), and a `hello` goes to the new address about 3 seconds later; a confirmed registration stays in use until the new one is confirmed. The same four as the pending registration made less than 10 seconds before: answered as it stands, with no new `hello`, and not counted against the 6. `400 bad_request` with a sentence that quotes nothing it was sent (an address at `localhost`, a name under `.localhost` or a refused address written out is refused unless the admin listed its push server); `400 push_key` when `key` is not the server's (`fixtures/refusal-push-key.json`): read `GET /access` and register with the current key; `503 unavailable` with `push.reason` while push is off (`fixtures/refusal-push-off.json`); `503 store` with `push.reason` while push is switched on but the server's push key is not ready yet. |
| `POST /me/push/confirm` `{nonce}` | anybody signed in | The `hello`'s `nonce`: the pending registration becomes the session's. `{"state":"confirmed",...}` (`fixtures/push-confirmed.json`). `409 conflict` for a nonce that is not the latest one, or older than 10 minutes (`fixtures/refusal-push-conflict.json`): a superseded hello, to be ignored. `503` while off. |
| `GET /me/push` | anybody signed in | How the session's registration stands (`fixtures/push-state-*.json`), below. Works while off. |
| `DELETE /me/push` | anybody signed in, never metered | Takes the session's registration (and one waiting) away: `{"state":"none"}`. Works while off, and is not refused by the app check (see "The app check"). |
| `POST /me/push/test` | anybody signed in, 3 a minute | Sends a `test` push to the session's confirmed registration: `202` (`fixtures/push-test.json`). `409 conflict` with none; `503` while off. |
| `GET /admin/push` | a system admin, whatever the lock | The System Console's card (`fixtures/admin-push.json`): `{on, ready, mattermost, mattermostProblem?, people, devices, hosts: [{host, devices, working, waiting, failing, failingSince?, refused}], full?, problem?, dropped?}`. Never anything by person. |

Every one of these routes answers `503` with the code `store` when the server could not read or
write its push records, and so do `PUT`, the confirm and the test while push is switched on but the
server's push key is not ready yet, and every one the app check guards when, under `require`, it
could not read its app check records: nothing was changed, and asking again later can work, so a
client keeps what it still has to send (the `PUT`, the confirm). `503 unavailable` from them means
only that push is off on this server.

The answers of the `/me/push` routes are `{state, host?, status?, kind?, since?}`: `host` the push
server's host (with its port when it is not the scheme's own); `since` when the state began (Unix
ms). `state` is:

- `none`: no registration for this session (register: `PUT`);
- `pending`: registered, the `hello` not yet confirmed (no `hello` within 45 seconds: ask
  `GET /me/push` again; `PUT` again, a few times at most, only while it still says `pending`); a
  `hello` is tried twice, 2 seconds apart, even while its push server is paused for other pushes, so
  one that cannot be delivered reads as `refused` or `failing` (`404` and `410` as `failing`, `kind`
  `answer`) within about 30 seconds of the `PUT`, unless the server is already sending as many pushes
  as its limits allow; a `hello` the push server answers `507` (ntfy's answer while the phone's ntfy
  has not connected yet) keeps it `pending`, and is tried again after 2, 5, 10 and 20 seconds and
  then once a minute while the pending registration lives (10 minutes from the `PUT`) and is the
  latest; one answered `507` and then another answer to try later reads as `failing`;
- `confirmed`: pushes go to it;
- `refused`: the push server answered `status` (`401` or `403`); the server tries it with one
  message at most once an hour, and one that goes through makes it `confirmed` again, as registering
  again does; for ntfy, often a server that does not allow anonymous publishing to `up*` topics;
- `failing`: sends kept failing (ten in a row, or an hour of nothing else with at least three
  failures) and it is sent one message at most every ten minutes, and nothing else; one that goes
  through makes it `confirmed` again, as registering again does; `kind` says how: `unreachable` (no answer), `address` (this
  server's address rules refuse the push address: one whose host is written as an internet address
  the rules refuse, or one the rules no longer allow, such as a host since taken off the list; a
  name that leads only to addresses the rules refuse reads as `unreachable`), `certificate` (its
  certificate did not verify) or `answer` (it answered, but not with a yes);
- `refresh`: the registration was made with a key the server no longer has: register afresh with
  the distributor with `push.key`, then `PUT`.

A `refused` or `failing` registration waiting for its `hello` reads as that, about the new address.
While a registration waits for its `hello`, its key is the one that decides `refresh`: the `PUT`
after a key change, with the new key, answers `pending`. While the server has no key loaded (it
says `off` in `GET /access`), `GET /me/push` answers from what it holds, never `refresh`.

**The plaintexts.** JSON, every field always there (`fixtures/push-payload-*.json`). `v` is 1: a
client drops a push with another `v`, one that does not decrypt or parse, and one whose `user` is not
the account it signed in as. `at` is the server's clock (Unix ms) when Mattermost decided to push,
or when the server made the hello, test or ended push.

- `message`: `{v, type, user, post, channel, root, team, sender, sender_name, channel_name,
  channel_type, text, crt, id_loaded, from_webhook, at}`: what Mattermost put in its own push for
  the server's push contents setting, and nothing it left out. `text` is cut to fit on a character
  boundary, ending "…". `crt` is whether the reader has collapsed threads. With `id_loaded`, only
  `post`, `channel`, `root`, `team` and Mattermost's generic `text` are there and `sender` is
  empty: the client fetches the post itself. Mattermost's sub type and icon override are left out.
- `clear`: `{v, type, user, channel, root, crt, at}`: the reader has read that channel (`root`
  empty) or that thread elsewhere.
- `hello`: `{v, type, user, nonce, at}`: answer with `POST /me/push/confirm`.
- `test`: `{v, type, user, at}`: the answer to `POST /me/push/test`.
- `ended`: `{v, type, user, at}`: the server found the session over, or could still not read it
  about a minute after it first could not, while it could read others, and dropped the
  registration; it sends nothing more there. A session that could not be read may still stand,
  so the client asks Mattermost (`GET /api/v4/users/me`) and signs out only on Mattermost's own
  `401` (its error, with an `id`), asked twice a second apart; when the session answers, it
  registers again (`GET /me/push` says `none`).

A push is not stored by the server and not sent again: what was waiting to go when the plugin
stops or restarts is lost.

## Voice

The snapshot's `voice` object is who is on the voice server (Mumble) the plugin connects to, as
read by the plugin's own connection there. From 4.3.0, an addition to revision 4, it also carries
the voice server's channels, and for each person their session, their channel, whether they are
deafened and whether they are there through Matty; and `GET /access` carries `voice`. A client
that does not read them loses nothing.

**In the snapshot** (`fixtures/snapshot.json`), only while an admin has voice switched on; absent
otherwise (`fixtures/snapshot-quiet.json`):

```jsonc
"voice": {
  "connected": true,                  // the plugin is connected to the voice server now
  "channels": [                       // from 4.3.0: the channel tree, in the order to draw it
    { "id": 0, "name": "Test", "depth": 0 },
    { "id": 2, "name": "Gaming", "parent": 0, "depth": 1 },
    { "id": 4, "name": "Duo", "parent": 2, "depth": 2, "max": 2 },
    { "id": 1, "name": "Lobby", "parent": 0, "depth": 1 }
  ],
  "people": [                         // everybody connected to the voice server
    { "session": 5, "name": "Bob", "userId": "u-bob", "channel": 2, "muted": true, "deaf": true },
    { "session": 12, "name": "dave (Matty)", "userId": "u-dave", "channel": 1, "phone": true },
    { "session": 7, "name": "Guest" }
  ]
}
```

- **`connected`** false: the plugin is not connected to the voice server just now (it is down, it
  refused the plugin, or the plugin is still connecting), and `channels` and `people` are absent.
  That is not "nobody is in voice": a client shows nothing and claims nothing. Connected with
  nobody there, `people` is absent.
- **`channels`**, from 4.3.0: every channel of the voice server, in tree order: a channel before
  the channels under it, and channels under the same one by their position on the voice server and
  then by name. `id` is always there, `0` included: `0` is the root. `parent` is the id of the
  channel it is under, `0` included, on every channel but the root, which has none. `depth` is `0`
  for the root, `1` under it, and so on. `max` is how many people the channel takes, only when it
  has a limit of its own (a channel without one takes as many as the voice server's own setting
  allows, which the plugin is not told). A channel whose parent the plugin was not told of is left
  out, with the channels under it. Absent from servers before 4.3.0: a client then draws the people
  as one list.
- From 4.4.0, while an admin has set **Channels to show**, `channels` holds only the channels listed
  there and the channels under them, in the same order (`fixtures/voice-shown.json`). A shown
  channel whose parent is not shown has no `parent` and `depth` `0`, and the channels under it count
  their `depth` from there, so more than one channel may have no `parent`. The shapes are the same.
- **`people`**: one for each connection to the voice server, sorted by name (in any case, then
  exactly), the plugin's own connection and the names an admin hides left out, and from 4.4.0
  everybody in a channel that **Channels to show** leaves out: they are not in voice as far as a
  client is told. The same person may be there more than once (a desktop and a phone), each with
  their own entry.
  - `session`, from 4.3.0: the voice server's number for the connection, which tells two entries
    apart while both are connected. Somebody who connects again has another.
  - `name`: as the voice server knows them.
  - `userId`: the Mattermost account they are, when the server could tell: for somebody there
    through Matty, the account that joined, whatever the name; for anybody else, by the server's
    name rules (the admin's list of names that differ, and, when an admin allows it, a name that is
    a username). Absent: nobody the server knows.
  - `channel`, from 4.3.0: the id of the channel they are in. Absent is `0`, the root.
  - `muted`: silent, for whatever reason: muted or deafened, by themselves or by an admin, or not
    allowed to speak in the channel they are in.
  - `deaf`, from 4.3.0: deafened, by themselves or by an admin. Somebody deafened is `muted` too.
  - `phone`, from 4.3.0: there through Matty, on a connection the plugin holds for them.
- `people[id].voice` in the people index is the same people by account, `muted` only when every
  entry of theirs is (see "The people index").
- Who is speaking is not in the snapshot.
- A change to the channels or to anybody's entry is sent as a new snapshot (the `streams` event),
  at most once every two seconds, a run of changes as one; a change nobody draws (a channel's
  description, somebody's comment) sends nothing.

**In `GET /access`**, from 4.3.0, only while voice is on (`fixtures/access-voice.json`): `voice` is
`{talk, reason?}`. `talk` is `on` while a Matty may join a voice channel and talk from it, and
`off` otherwise; `reason`, with `off`, is a sentence to show ("Talking from Matty is switched off
on this server."). A client shows the channels and the people from the snapshot whatever `talk`
says, and offers joining only while it is `on`.

### Talking from Matty

From 4.3.0, a Matty may join the voice server's channels and talk there, through the plugin: the
phone opens one WebRTC connection, audio only, to the Mattermost server, and the plugin holds a
Mumble connection for the reader and passes the voice between them without decoding it. Every route
here needs `X-Requested-With: XMLHttpRequest` (else `403 forbidden`), takes and answers JSON of at
most 64 KiB (else `413 too_large`), and is never cached.

**`GET /access`'s `voice`** (`fixtures/access-voice.json`, `fixtures/access-voice-on.json`):
`talk` is `on` while all of these hold: an admin has switched it on with a voice server set, the
server's voice port is open with addresses to give phones, the plugin's own connection to the voice
server is connected, and Mattermost is not a cluster. Otherwise `off`, with `reason`, one of:

- "Talking from Matty is switched off on this server."
- "Talking from Matty works on a single Mattermost server only."
- "The voice server is not connected."
- "The voice port must be between 1024 and 65535."
- "Talking from Matty is not working on this server just now." (the port or the addresses; the
  admins are told why)
- "Talking from Matty is starting on this server. Try again in a moment."

A client offers joining only while it is `on`, and asks again after a `503 unavailable` from a
voice route.

| Route | For | Notes |
| --- | --- | --- |
| `POST /voice/join` `{channel, muted, deafened, sdp}` | linked or let in, 6 a minute | `200 {sdp, session, channel, name}` (`fixtures/voice-join.json`): the answer to the offer, with every candidate (no trickle); the reader's session number on the voice server; the channel they are in; and the name they have there, `"<username> (Matty)"` or, on a voice server that does not take it, `"<username>.matty"`. `channel` is required, `0` (the root) included; `muted` and `deafened` say how to join, deafened being muted too. A join by somebody who has a voice session takes it over, on the voice server's connection the session has, and the old phone is told `replaced`. From 4.4.0 the mute and deafen are answered at once, as asked, and a join that changes the channel can take up to a second longer than the voice server's answer: the move waits for its turn among the session's own messages. A join to the channel the reader's session already sits in is not refused, even one that **Channels to show** leaves out. |
| `POST /voice/move` `{channel}` | linked or let in, 60 a minute with `/voice/state` | `200 {channel}` (`fixtures/voice-move.json`). From 4.4.0 a move can take up to a second longer than the voice server's answer, waiting for its turn as a join's move does, and a move to the channel the session is in answers at once, even one that **Channels to show** leaves out. |
| `POST /voice/state` `{muted, deafened}` | the same | `200 {muted, deafened}` as asked (`fixtures/voice-state.json`), at once, without waiting for the voice server to take them: `self` says when it has. Deafened is muted too. From 4.4.0, asking again for what was asked while `self` still says otherwise sends it to the voice server again in the session's next turn. |
| `POST /voice/leave` | anybody signed in, never metered | `200 {left}` (`fixtures/voice-leave.json`): whether there was a session to end. Answered whatever `talk` says, whatever the reader's access and whatever the app check found: leaving is never refused. |
| `GET /admin/voice` | system admins | The System Console's card (`fixtures/admin-voice.json`); from 4.4.0, while the voice server is connected, `unmatched` is the lines of **Channels to show** that name no channel, and `several` the ones that name more than one: `[{line, paths, more?}]`, `paths` being the first five channels the line names, each as a line would name that channel alone (`/Lobby` right under the root, `Gaming/Duo` deeper, the root as `Lobby (the root)`), and `more` how many more it names. Whether the voice server is connected or not, `tooLong` is the lines of the setting longer than 256 characters, which are not used, each as its first 40 characters and `…`, and `dropped` how many lines come after the first 200, which are not used either. |

**The offer** has exactly one audio section with `a=sendonly` offering Opus (the microphone, with
the `urn:ietf:params:rtp-hdrext:ssrc-audio-level` header extension: without it the reader is never
heard), one to eight audio sections with `a=recvonly`, one data channel section, and at most 16
candidates in any one section; anything else, or a section with port 0, is `400 bad_request`. The server sends
nothing to a candidate whose address is unspecified, multicast or broadcast. The phone opens the
data channel `voice` itself, and no other: a second data channel, or one by another name, ends the
connection at once. The
server answers the first `min(4, recvonly sections)` with a track each, Opus at payload type 111,
and the rest inactive. The server's candidates are host candidates on one UDP port, IPv4 only.

**The tracks.** Each person the phone hears takes one track for as long as they talk: a spell of
talking starts with the RTP marker set, timestamps follow the voice server's own sequence (960 a
20 ms packet), and a track is free again 300 ms after its speaker's last packet. More than four
people talking at once: the fifth is not sent until a track is free. The microphone's voice goes to
the voice server only while the reader is talking by its audio level: from a packet at level 40 or
louder (a smaller number is louder, as RFC 6464 has it), until 400 ms after the last at 50 or
louder; and never while muted, deafened, muted by the voice server or not allowed to speak there.
From 4.4.0 it also waits for the voice server's word: nothing goes up, and the reader's own session
is not in `talking`, while the voice server holds the reader self muted or self deafened, whatever
was asked. A mute stops the voice at once; after an unmute it goes up once the voice server has
taken it.

**The data channel `voice`**: the server speaks, JSON text, one message each; the phone sends
nothing and closes it only by closing the connection.

```jsonc
{"talking": [12, 15]}        // the sessions heard on this phone now, and its own while its voice goes up; on every change
{"self": {"channel": 2, "muted": false, "suppressed": false, "selfMuted": true, "selfDeafened": false}}
                             // the reader's channel, muted by the voice server, not allowed to speak there,
                             // and the reader's own mute and deafen as the voice server holds them;
                             // when the channel opens and whenever the voice server's word on the
                             // session changes (it may repeat)
{"end": "kicked", "reason": "You were removed from the voice server: Spamming."}
                             // the session is over; the server closes the connection after it
```

(`fixtures/voice-signal-talking.json`, `-self.json`, `-end.json`.) `selfMuted` and `selfDeafened`,
from 4.4.0, are what the voice server holds, which may lag what the reader asked for, and differ
from it while the voice server has not taken it (it takes a user's own state messages only so fast,
and the plugin sends them again until it does); a reader that finds them missing takes them as
`false`. `channel` may name a channel that `channels` does not hold: one that **Channels to show**
leaves out, where a Mumble admin moved the reader, who stays connected. `end` is one of `kicked`,
`banned`, `mumble` (the voice server's connection was lost, or its settings changed, or the plugin
is stopping, restarting or being upgraded, "Voice is restarting on this server.": joining again
can work), `replaced` (another join took the session over), `off` (talk was switched off, the
reader may no longer watch, or, from 4.4.0, the join came from a Matty below a minimum that has come
into force, with the refusal's sentence; see "A minimum Matty") and `session` (the Mattermost
session that joined has ended). `reason` is a sentence to show. A leave, or a phone that never came
back, gets no `end`.

**A phone that loses its connection** (closed, failed, or disconnected for 5 seconds) leaves its
session in place for 45 seconds, deafened on the voice server; a join in that time takes it over
without a new connection to the voice server. After that the session ends.

**Refusals** (with the usual body):

| Status and code | When |
| --- | --- |
| `503 unavailable` | `talk` is off (the sentence is `voice.reason`, `fixtures/refusal-voice-off.json`); the voice server could not be reached or did not answer; the connection to it was lost; 50 people already talk from Matty on this server; or the plugin's own limit of connections to the voice server is used up for now, with `retryAfterMs` (`fixtures/refusal-voice-budget.json`). Worth trying again, after `retryAfterMs` when there is one. |
| `404 not_found` | No such channel on the voice server, or, from 4.4.0, one that **Channels to show** leaves out and the reader's session is not in (`fixtures/refusal-voice-not-found.json`). |
| `403 voice_denied` | The voice server did not let the reader into that channel: "The voice server did not let you into that channel." (with its reason after a colon when it gave one), or "That channel is full." (`fixtures/refusal-voice-denied.json`); or a Mumble admin kicked or banned the reader while the join was under way, with the sentence the `end` of a kick or a ban has. |
| `409 voice_refused` | The voice server refused the reader's connection: the server password, the server full, its Mumble version, a certificate needed, or the name (one it does not take, one in use, or one registered to somebody else) (`fixtures/refusal-voice-refused.json`). Asking again does not help until an admin changes something. |
| `409 conflict` | A move or a change with no voice session. |
| `400 bad_request` | No `channel`, an offer of another shape, a body that is not JSON, or an account with no username. |
| `429 rate_limited` | More than 6 joins a minute, or 60 moves and changes; or a third join in two minutes that needs a new connection to the voice server ("You have joined voice too often just now. Try again in a moment."), with `retryAfterMs`. A join that takes over the reader's session needs none. |
| `403 locked`, `watch_only`, `app_outdated`, `app_unverified`, `503 store` | As everywhere. |

## What a client must do

1. **Never dress a loaded address as its own request.** `X-Requested-With: XMLHttpRequest` says
   "this client decided to make this request". Some reads here are answered only with it (the
   soundboard's audio, the caller's stream profile), because in a browser a picture in a post
   cannot carry it. A native client chooses its own headers, so it keeps the rule itself: nothing
   it loads as a picture, as video or as a download sends that header. Such loads send the session
   token only, and only to the signed-in server's exact origin. (Whatever reaches a provider
   outside, the title search, is a `POST` and does not rest on this.)
2. **Ask while playing.** Nothing can end a playing stream from outside: the relay only carries
   its start. While a stream plays, on screen or not, the player asks `GET /access` at least once a
   minute and stops when the answer is `locked` or `configured` is false. `unknown` takes nothing
   away.
3. **Ask after a refusal.** An answer coded `locked` or `watch_only` from any route means what the
   client holds about the reader is behind: ask `GET /access` (not more than once in two seconds).
   And show the refusal's own sentence: see "A refusal".
4. **Show a locked reader nothing about anybody's Steam or Hytale**, whatever was fetched before
   the lock: drop it when access goes.
5. **Never put a token in a query string.**
6. **Read conditionally.** Send the tag of the copy in hand with every re-read of `/streams`,
   `/accounts`, `/boards` and `/banners`. After the client's own write, the change arrives as a push or an
   event; there is no need to read everything again.
7. **Decide nothing about awards.** Draw `people[id].awards` beside a name, `streams[].awards` on a
   card and `people[id].rows` on a profile, as given. A client that ranks a board itself, or applies
   somebody's choice, will sooner or later disagree with the other one.
