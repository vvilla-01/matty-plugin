# VillaNET: where things stand (2026-11-18)

Written so the work can be picked up from the project folder alone.

## Current release: 3.8.0

**3.8.0 keeps the part of a banner that shows** (18 November), for Matty 0.1.25. Victor asked to
"have the ability to select what part of the banner will show because no image would be Matty app
profile ready". Matty cuts a still picture on the phone and sends only the chosen part; an
animated GIF cannot be cut there without encoding it again, so it is sent whole and the plugin
keeps the rectangle to show, which every client applies where it draws the banner. An addition
under contract revision 3 (`contract/README.md`, "Profile banners"); nothing in the web panel or
the System Console changes.

- **The wire.** `GET /banners` entries may carry `crop: [x, y, w, h]`, fractions of the picture's
  width and height with at most four places. `POST /me/banner?crop=x,y,w,h` keeps one with the
  picture; `PUT /me/banner/crop` with `{"crop": [...]}` or `{"crop": null}` changes or clears it
  alone (its own meter, 30 in 10 minutes; regular members only; `404 not_found` with no banner).
  Its body may carry `"v"`, the picture the part was chosen for: when the banner is another picture
  by the time it arrives (set from another device meanwhile), nothing changes and the answer is
  `409 conflict`. `setCrop` compares it under the `writes` lock with the file it would rename, so
  a new upload cannot slip between the two; a rename for a crop keeps `v`, so only a new picture
  makes one stale.
  Refusals are `400 bad_crop`: not four plain decimals from 0 to 1 (no sign, exponent, NaN,
  Infinity, leading point or fifth place), a side under 0.05, `x + w` or `y + h` over 1.0001, or a
  shape under 2.5 or over 3.5 to one measured on the picture's own pixels, or a `v` that is not 16
  lower-case hex digits. The `PUT` body is read token by token: `crop` once and at most a `v` once,
  in either order, nothing else and nothing after it, at most 1 KiB. The same crop again writes and
  publishes nothing.
- **Where the crop lives: in the picture's file name**, `<user>-<v>-<x>-<y>-<w>-<h>.<ext>` in
  ten-thousandths (`server/banners.go`, `bannerName`, `bannerFilePattern`, `cropInName`). The name
  was already the whole record, so the crop cannot be read with another picture, survives a
  restart, goes with every takeback and with a new upload without one (that is another name, and
  the old goes as an old picture does), and is read with the directory listing the list is built
  from, at no extra read. Changing it renames the file under the same `writes` lock as put and
  remove, then `forget()` moves `gen`; `v`, the bytes and their address stay. `handleBanner`, when
  the name it expected is gone (a rename here or on another node whose change its list has not
  caught up with), reads the directory once more and serves the same `v` under its new name.
  `setCrop` does the same when the file it listed is gone before it reads its header or renames it
  (another node renamed it for a crop): it looks again, as `remove` does, and answers "no banner"
  only when a listing finds none; `v` is compared again on each look. A
  name whose numbers are not a crop this file would write is nobody's banner. The shape on a `PUT`
  is measured from the stored file's header alone (`storedBannerSize`).
- **Tests** in `server/banners_test.go`: `TestWhatABannersCropMayBe` (every bound and its edge, the
  query and the body), `TestABannersCropIsWrittenInFourPlaces`, `TestABannerUploadWithAndWithoutACrop`,
  `TestChangingABannersCrop` (set, same again, change, clear, the tag and the snapshot's word),
  `TestACropChangesBodyMayNameItsPicture`, `TestACropChosenForAnotherPictureChangesNothing` (the
  right `v`, a stale one in both orders and for a clear, no banner),
  `TestAMalformedCropChangeNamingAPictureIsRefused`,
  `TestABannersCropChangeIsMetered` (and the takeback still not), `TestABannersCropGoesWithIt`,
  `TestABannersCropOutlivesARestart`, `TestABannersCropChangedOnAnotherNode`,
  `TestACropChangedWhileAnotherNodeRenamesThePictureIsKept`,
  `TestANameWithACropThatCannotBeOneIsNoBanner`, `TestBannerWritersAtOnce` (under `-race`). The
  banner tests from 3.7.0 pass unchanged. `contract/fixtures/banners.json` gains Alice, whose banner
  has a crop; Bob's and Dave's have none.
- **Going back to 3.7.x** hides a GIF whose crop was chosen (3.7.x does not read the longer name);
  `docs/upgrading.md` says so. Still pictures are unaffected.

The account of 3.7.3 follows.

**3.7.3 mends the Back up download** (18 November). Victor found that Back up in the System Console
showed "0 B - Cancelled" and that pressing it quickly several times got a file through. The file's
address was let go on the next turn of the loop (`webapp/src/client.ts`), before Mattermost's
desktop app had started saving; it is now kept for ten minutes. A one-line change, read from the
code and from what he saw; it was not tried in the desktop app. The account of 3.7.2 follows.

**3.7.2 takes real people's details out of the shipped defaults** (17 November). Victor was asked
"Remove the real handles and birth dates from the plugin's shipped defaults?" and answered "Yes,
remove them". Three things changed, and nothing else the plugin does or shows:

- **Stream names** (`StreamNameOverrides`, in `plugin.json` and `server/defaults.go`): its default
  named seven people's streams and the 24/7 channel; now only the 24/7 line, which **24/7 stream
  keys**, **Stream notes**, **24/7 channel achievements** and the shipped picture already set up.
  The default is not only shown: on a server that never saved the setting, Mattermost (11.8 and
  later) and `applySectionDefaults` run on it, so such a server's unlinked streams lose the old
  names. `OnConfigurationChange` logs `streamNamesDefaultNotice` once a start while the setting is
  absent from the saved settings (`neverSaved`). `TestStreamNamesShipNamingOnlyThe247Channels`
  fails on any name for a stream that is not a 24/7 channel; `TestNeverSavedStreamNamesAreNoticedOnceAStart`.
- **Names that differ** (`VoiceNameMap`): its placeholder, a real pair, is now `alice_ = alice`; a
  placeholder is never a value. `TestTheNamesThatDifferExampleIsMadeUp` holds it to the lab's
  made-up people.
- **The birthday starting list** (`server/birthday_seed.go`), offered by "Start from the old bot's
  list" and only ever copied in when an admin pressed it and saved: empty. The route, its admin
  check and its no-store header are unchanged and it answers `[]`, so the System Console page and
  the route's shape are too; pressing the button now says "0 people" and turns on Save though
  nothing changed. An entry put back fails `TestTheStartingBirthdayListHoldsNobody`,
  `TestOnlyASystemAdminGetsTheStartingBirthdayList` and `TestTheStartingBirthdayListIsASystemConsoleRoute`;
  `TestTheStartingListKeepsTheShapeOfTheBirthdayList` is a shape check only (its own made-up
  people, never `birthdaySeed`).

Two visible effects wait on the owner's call after the preview: the Info line in the server log while
Stream names has never been saved (keep or drop), and the seed button on an empty list (keep, hide,
or say there is nothing to add without turning on Save). Both are as `docs/upgrading.md` describes.

The steps for an admin are in `docs/upgrading.md`. The old values were kept for Victor in the
git-ignored `dist/` (which `make clean` deletes, so it is to be moved out), never committed, and
are named nowhere in the repository; git history up to 671e011 still holds them. The lab's two
birthday lists (`webapp/lab/index.tsx`, `webapp/lab/mocks/client.ts`) are made-up people throughout
now, the owner's own entry there replaced too. Left as it is, for a separate change if wanted: the
lab's signed-in person, which still carries the owner's own full name (no date) in `index.tsx`,
`rows320.tsx` and `world.ts`. The tests, their golden files, the contract's fixtures, the check
scripts and the design notes now use the lab's placeholder people too, and the birthday fixture in
`server/birthdays_test.go` is made-up people and dates (a child of two parents, a leap-day
birthday). No contract change. The bundle to upload becomes
`dist/ceo.villa.villanet-3.7.2-linux-amd64.tar.gz` once the release owner has built it; the rest of
this section is the account of 3.7.1 and 3.7.0.

**3.7.1 fixes the clips lock** (17 November). Victor asked: "Fix the clips lock." The security pass
below left a fault in `server/clips_ring.go`: the ring's commit and freeze took `r.mu` on the
writer's goroutine and gave it back by hand, so a panic between the two (recovered since 3.7.0)
left it held for good, and the restart's `old.remove()` then waited on it for ever: that stream's
recording stopped without a word. Every span under a ring's lock now ends in a defer: plan,
snapshotRange and the short reads did already; commit and freeze run through `update`, remove and
release through `locked`, and each hands back the disk work (`ringCleanup`) for after the lock, as
before. A panic part way through commit or freeze also drops the ring's index before the lock goes,
so no export cuts from a half-committed sample: the ring reads as empty (`errWarmingUp`) until the
restart, or finish, removes it; snapshots already taken keep their copies and pins. finish now
takes a panic while it freezes as a write that failed (the epoch is wiped, finish returns), since a
recorder's stop runs on a goroutine with nothing to recover it. The disk was already never touched
under the lock (no write, sync, rename or removal), and that is unchanged, as is everything the
recorder does when nothing panics. The same reading of every other lock in `server/clips_*.go`
found one more span given back by hand around a call out: `clipRecorder.start` (the ring window
from the configuration, and `context.WithCancel`), now under a defer; the rest hold only plain
assignments and reads, as `recovery.go` allows. Tests, under `-race`, with a test-only
`ring.testFault` to panic inside commit or freeze:
`TestAPanicUnderTheRingsLockLetsGoOfIt`, `TestAWriterThatPanicsUnderTheRingsLockStartsAgain`,
`TestAPanicWhileFinishFreezesWipesTheEpoch`, `TestAnAbandonedWriterThatPanicsStartsNothingAgain`
and `TestARecorderStartThatPanicsLeavesItsLockFree`. A panic while a recorder stops
(`stopRecorders`), in what `closeSession` waits for (`waitFor`), or as `run` closes its session
on its own is now logged by `safely` and ends that one stop alone, the others stopping and
`OnDeactivate` returning as before (`TestARecorderStopThatPanicsStopsNothingElse`,
`TestARecorderCloseThatPanicsStopsNothingElse`, `TestARecorderClosingOnItsOwnThatPanicsStillEnds`);
so, through `safely`/`goSafely`, are the last background goroutines that ran without a recover (the
delayed accounts event, both halves of a game search and the shared fetch, which answer
`errMediaPanicked` as a source that failed, the first picture tidy, a picture's write to disk, and
the disconnect of an abandoned voice dial), each giving back what it owes by a defer, so that no
`go` statement under `server/` now runs without one (tests in `server/recovery_test.go`).
No contract change, nothing in `webapp/`. The
bundle to upload becomes `dist/ceo.villa.villanet-3.7.1-linux-amd64.tar.gz` once the release owner
has built it; the rest of this section is the account of 3.7.0.

**3.7.0 adds profile banners** (17 November). Victor asked: "Add feature have users change their
profile banner shown on Matty. This will probably require a VillaNET Mattermost plugin. Make all the
changes needed. Make it GIF compatible so animated banners can be used." The plugin keeps one
picture a person and Matty 0.1.22 sets and shows it (`server/banners.go`, "Profile banners" in
`contract/README.md`): `GET /banners` lists who has one, `GET /banner?user=&v=` serves the bytes as
they were sent (an animated GIF stays animated), `POST` and `DELETE /me/banner` set and take away
one's own (regular members only; 6 uploads in 10 minutes), and `DELETE /admin/banner?user=` is a
system admin's. A JPEG or a PNG up to 2 MiB, a GIF up to 8 MiB, each side 64 to 2400 pixels, the
kind decided from the bytes and only their header decoded. The files are under
`<data root>/banners/`, named by user id, fingerprint and type, so there is no document beside
them; the snapshot carries only `banners.version`, and is republished when it moves. An addition
under **contract revision 3** (`contractSince` stays 3.6.0); the panel shows nothing new. The
bundle to upload becomes `dist/ceo.villa.villanet-3.7.0-linux-amd64.tar.gz` once the release owner
has built it; after the security pass below, the rest of this section is the account of 3.6.x.

**Security pass (17 November), in 3.7.0.** Victor asked: "Give an overall security pass to make sure
the app and plugin is secure for use." Five fronts were audited, each finding checked by a second
reviewer: who may call what; files and fetches; sign-ins and secrets; abuse and injection; the web
panel. Nothing high or medium was found; of seven low findings, five are fixed. An animated GIF
banner is now walked block by block, never decoded, and refused `400 bad_image` past 300 frames,
419,430,400 pixels in all, a frame outside its screen or a file that does not reach its trailer
(`gifWithinBudget`). A Broadcast Box URL of plain `http://` to another machine is warned of in the
log once a change, since linked people's stream keys cross that hop unencrypted; nothing is
refused. Markdown typed in Mumble can no longer make the bot post a picture or a disguised link
(`defuseLinks`). A panic serving a request is a `500 failed` refusal, and one in the poller, the
accounts and Twitch loops, the voice connection and its listeners, the recorders and their
readers and writers, the sound player and the clips' timers is logged with its stack and the loop
goes on, where before any of them ended the whole plugin (`server/recovery.go`). The System
Console's 24/7 channel pictures are named by `?handle=`, not by the stream key in the address
(`?key=` is still taken from a page opened before the upgrade, and goes in a later version).
Left for Victor to decide: the Mumble connection accepts any certificate (findings 2 and 4), so an
attacker on the network between the two servers could take the Mumble password and feed the
bridge; pinning the certificate needs either a new System Console setting or trusting the first
certificate seen, and both change how an admin runs it.

**3.6.1 adds one rule to 3.6.0** (16 November). Victor asked: "When I'm streaming a movie, and in the
plugin on Mattermost I select/tag the movie, it still shows 'Live Now' on Matty's member list. It
should say example: 'Streaming Quantum of Solace'." Now, while somebody is live and their card's
What's on? names something that is not a game (a movie or a show) with a title, their
`people[id].activity` is `{kind: "streaming", title, source: "villanet"}`, the title looked up when
one was picked, else the words as typed; it wins over a game Steam reports for them at the same time.
A game in What's on? keeps every rule it had. Picking, changing or clearing it mid-stream rebuilds
`GET /accounts` and sends the `accounts` event (the movie's title is now part of what `onAirKey`
fingerprints). It is an addition under **contract revision 3** (`contractSince` stays 3.6.0), so
Matty 0.1.20 prints it with no change. The bundle to upload becomes
`dist/ceo.villa.villanet-3.6.1-linux-amd64.tar.gz` once the release owner has built it; the rest of
this section is the account of 3.6.0.

**Victor is still running 3.2.x on his real server**; 3.2.1 to 3.5.0 were built and never installed
there, and 3.6.0 is built and not installed there either. The bundle to upload is
`dist/ceo.villa.villanet-3.6.0-linux-amd64.tar.gz` (made by `make dist-linux` on 16 November, after
the review's fixes), **never the one under `dist/standin/`**. 3.6.0 is released as a pair with
**Matty 0.1.20**, on **contract revision 3**, and the manual has "Upgrading to 3.6.0" (`docs/upgrading.md`).

What it is: the work done on 16 November after the 3.5.0 bundle was made, taken in order from what
the research for 3.5.0 proposed and left for later (the list is at the end of the next section),
then reviewed independently and corrected before release (the last part of this section). Each
part below is written as it was built, so the earlier ones speak of contract revision 2 and of
3.5.0; the last two took it to revision 3 and 3.6.0.

### The pair harness: stand-ins for Steam and Mumble (16 November)

`build/fakebb` stood in for Broadcast Box only, so on a development machine nobody was ever in voice
and nobody had a Steam status: mic badges and "Playing" lines could be looked at in Matty only by
editing its source for the occasion. The same program now also runs (`build/fakebb/standin`):

- **A stand-in Mumble server**, on Mumble's own port. It speaks Mumble's protocol (TLS with a
  certificate it signs itself, the same messages in the same order, pings answered), so the plugin
  connects to it with the code it uses for a real server and nothing in the plugin changes:
  `MumbleServer` is a setting. Who is in voice is made up and steered while it runs:
  `curl -X POST 'localhost:7788/fake/voice?name=bob&muted=true'` (`&on=false` leaves,
  `&say=hello` writes in the text chat). It counts what the plugin sends it: a sound's voice packets
  and the chat bridge's messages.
- **A stand-in Steam**: the OpenID sign-in, a page that vouches for whichever account is picked
  (`&as=name` on its address for a script, `&cancel=1` for Cancel), and the Web API's
  `GetPlayerSummaries`. What it signs is checked for real: a signature it did not make is not valid,
  and one it made is good once, as with Steam. Steered the same way:
  `…/fake/steam?who=alice&game=Hades`, `&state=away`, `&private=true`, and `?api=429` to have the Web
  API answer badly. A name is always the same SteamID, so a connection the plugin kept still finds
  its account after the stand-in restarts.

Steam's hosts are not a setting, and must not be: the sign-in's address is fixed so that nobody can
point the server at something that is not Steam. So the plugin reaches the stand-in Steam only in a
build made with the `villanet_standin` tag. `server/standin.go` is the whole of it: Steam's two hosts
become `http://127.0.0.1:7788`, and nothing else changes; every check of the sign-in still runs.
**A release can never use it:**

- `make dist` and `make dist-linux` do not pass the tag. After building they look in `server/dist`
  for the stand-in build's own sentence, and if it is there (a tag that came in through `GOFLAGS`,
  say) they delete the binary and fail. `TestAReleaseAsksOnlySteam` holds the two hosts in every
  ordinary build.
- The stand-in bundle is made by `make standin-linux` and kept apart: under `dist/standin/`, named
  `…-standin-linux-amd64.tar.gz`, and `<version>+standin` in Mattermost's plugin list. It is never built
  through `server/dist`.
- A stand-in build refuses to start (`OnActivate` says why) unless Mattermost's Site URL is this
  machine: `localhost`, `127.0.0.1` or `::1`.

`make check` now also vets and tests `build/fakebb`, and runs, under the tag, the plugin's own code
against the stand-ins (`server/standin_test.go`): the whole Steam connection as Matty makes it
(app-connect, the sign-in, app-finish), the accounts loop reading what the stand-in is steered to say,
the ways Steam goes wrong (Cancel, an answer changed on its way back, a takeover, a 429 and what is
shown six minutes into one), and the plugin's own Mumble connection reading the roster and its
changes. That last is the first test that runs `holdVoiceConnection` against a server at all.

In the Matty project, `tools/dev-server/villanet.sh` builds and installs the stand-in bundle, starts
the stand-ins, sets the voice and Steam settings, links `victor`, `alice`, `bob` and `carol` and
connects each one's stand-in Steam account through the plugin's real routes; `dave` stays locked.
`VILLANET_BUNDLE=release` installs the bundle that is released instead, with Steam off. The stand-in
build needs the development server's Site URL to be `http://localhost:8065`, which is now that
project's `start.sh` default (it was `http://10.0.2.2:8065`).

The stand-in Broadcast Box had a fault of its own, found on the first run with more people than
`victor`: a token for a stream it did not know yet was given a stream named after the token's base64
("Ym9i" for "bob"; a name the plugin refuses where that ended in "="). It now takes the base64 off
whenever what is under it could be a stream's name.

**Checked on 16 November**, on the Matty project's development server (Mattermost 12.0.2) with the
`matty-test` emulator and Matty's debug build, nothing in the app's source changed for it: the member
list shows mic badges (open and muted) from the stand-in Mumble and "Playing" lines from the stand-in
Steam; a mute, a join and a leave steered with `curl` show within three seconds; a game changed on the
stand-in Steam shows at VillaNET's next read; the plugin list says `3.5.0+standin`; and a locked
reader (`dave`) is shown the rings and the mic badges and nothing of anybody's Steam, and never asks
`GET /accounts`.

### Clips and shared cards from one copy; the server says "full" and when a clip goes (16 November)

Additions only; the contract revision stays 2.

- **`schedule[].full`** in the snapshot: a plan with no room, by the count the join is refused on
  (`scheduleFull`), which is the places taken and not the names shown. Somebody whose account has gone
  is left out of `joined` and still holds a place, so a client counting names could offer a Join the
  server refuses. Matty, which never knew the cap, now says "Full". **The panel does not read it in
  this release**: the plan's card in a post counts the names in `joined`, as it did at 3.5.0, and the
  Up next list offers Join on a full plan and shows the server's refusal (seen in a browser on 16
  November). Reading `full` in either place changes what the panel shows, so both wait for a preview.
- **`expiresAt` on each card of `GET /clips/cards`**: when the clip goes, as the clip itself says.
  Matty goes by it alone. The panel takes it when it is earlier than its own reckoning, but a post
  card whose clip is older than `retentionDays` still says "expired" without asking, as before, so a
  clip kept through the grace after the retention is lowered reads expired there for that day. A
  reader with no card (locked, or not asked yet) is told from the post's props, as before.
- **Matty** (`VillaNetClips.kt`): the viewer asks for one clip (`GET /clips/clip?id=`) where it
  downloaded every clip to find one, and tells apart a clip that has gone (only the server's 410), a
  locked reader ("Unlock VillaNET to watch this clip.") and no answer (with Try again), which all read
  "This clip is gone" before. One copy of the clips feeds the Clips tab, the viewer and the cards, kept
  for as long as `clips.version` stands, so a vote shows in all three and coming back to the tab reads
  nothing. Cards are asked for in a loop that runs until nothing is waiting (an id that arrived while a
  batch was out used to be stranded) and follow the panel's freshness rules.
- New contract fixtures: `clip.json`, `clip-cards.json`, `refusal-clip-gone.json`; the snapshot fixture
  now has a plan in it.

### One refusal body on every route; the server's sentence is the wording (16 November)

- Every refusal is `{error, code, retryAfterMs?}` (`errorResponse`, `contract/README.md` "A
  refusal"). A refusal whose handler names no code carries its status's (`refusalCodeOf`): 245 call
  sites sent a sentence alone. Every meter's 429 says how long to wait, read from the meter
  (`writeTooMany`, `writeClipTooMany`). (Two did not until the last part below was built: the award
  picker's and the connect ticket's. `TestEveryMetersRefusalIsWrittenWithItsWait` now holds the
  source to the helpers, so a 429 written any other way fails.)
- **The link's sentences are the server's.** The lock screen and Matty each kept the same table of
  eight sentences by code, over server sentences nobody saw. The server now says those eight, word for
  word (`link…` constants in `api.go`), both tables are gone, and `build/check-away.mjs` holds the
  server's sentences to what the lock has always shown, so nothing on the lock reads differently.
- `POST /whep` is coded: `not_live`, `bad_offer`, `unreachable`, `not_configured`, `rate_limited`.
  Matty shows the server's sentence where it worded playback failures from the HTTP status.
- Joining a plan is coded: `full`, `own_plan`, `gone`, `busy`, `not_yours`. The sentences are as they
  were (the panel shows them as sent, and they read as fragments: "that one is full"; left alone
  because changing them changes what the panel shows).
- **An offer Broadcast Box cannot serve is never passed on** (`whepOfferServable`), or `400
  bad_offer`. The offer is parsed with pion's SDP parser, as Broadcast Box parses it, and refused when
  it does not parse, has a carriage return that does not end a line, or is over 64 KiB (refused before
  it is parsed); when it has other than exactly one audio and one video section, a turned-down one
  (port 0 without `a=bundle-only`) counted and refused; or when either section's own first direction
  attribute (as pion's `getPeerDirection` reads it, by key) is not `recvonly` or `sendrecv`, or it has
  none (whatever the session says). A data channel beside them is ignored. Found by the session working on Matty's
  minimised playback, reading Broadcast Box 2.0.2: an offer with no video section is answered and then
  ends the whole Broadcast Box process at the stream's next key frame. Neither client sends one, but
  the route relayed whatever a signed-in person who may watch posted. Not tried on a real Broadcast
  Box; the stand-in does not reproduce the crash.
- **Where the server's sentence is not yet the wording:** the web panel still words its clip and
  soundboard refusals itself (`clipErrorCopy`, `refusalLine`) and shows the server's sentence only
  for a code it has no words for, so that it reads as it did. Matty shows the server's.

### "Playing" or "Streaming", decided once, in `GET /accounts` (16 November)

`people[id].activity = {kind, title, source}` (`server/accounts_activity.go`): the game Steam reports
against what the card that stands for the person on the air says it is showing. Where What's on? was
picked from Steam it is matched by Steam's app id (`gameid` against `steam:<appid>`), otherwise by
name, as Matty did. Somebody connected to neither service whose stream names a game is in `people`
for this alone. (A movie on left the line to Steam's game, or to nothing; from 3.6.1 a movie or a
show in What's on? is the line while they are live, and also puts them in `people`.) It is in `GET /accounts` and never in the snapshot, since part of it is Steam's. A
stream that changes it (going live, a game picked) rebuilds the answer when a snapshot is stored
(`noteOnAir`) and sends the `accounts` event. Matty prints it (`VillaNetPeople.kt`); its own matching
(`streamIsOf`, `streamGameOf`, `gameKey`) is gone. **The panel does not show it yet**: its Playing
strip and popover rows are still Steam's alone and say nothing of streaming. Showing it there changes
what the panel looks like, so it waits for a preview.

**Checked on 16 November**, as above: `make check`; Matty's 120 unit tests; and on the emulator,
through a logging forwarder: the Clips tab reads the list once and not again on coming back to it,
the viewer asks `GET /clips/clip?id=`, a vote shows in the viewer, the list and the card, a card on
screen follows somebody else's vote within five seconds, a link to a clip that is not there says
"This clip is gone", a dropped connection says "This clip didn't load" with Try again (which then
opens it), a plan with two names and twelve places taken says "Full", "Playing Factorio" turns to
"Streaming Factorio" three seconds after What's on? is set to it and back to "Playing Hades" when
Steam says another game, a link refused with Broadcast Box stopped reads the server's sentence, and a
stream still plays (the app's real offer passes the new check: one viewer at the stand-in, sound and
picture arriving).
**Not checked:** the web panel in a browser (its checks and tsc only); a locked viewer's "Unlock"
state on the device (unit test only); anything real (Steam, Mumble, Broadcast Box, the real server).

### Contract revision 3, VillaNET 3.6.0: one `me` event, and the panel on the app's read rule (16 November)

Victor chose this one first of the two large items. It removes events, so **the contract is revision
3**, and the version had to move with it (a client tells a plugin too old for it by its version):
**`plugin.json` and `manifestVersion` say 3.6.0**, `contractSince` is 3.6.0, and Matty's
`VnAccess.CONTRACT`/`SINCE` are 3 and 3.6.0. Matty 0.1.19 on this plugin says the app is the older of
the two and misses its events; the new Matty on 3.5.0 says "VillaNET needs an update". No bundle was
built for release at the time (the release bundle was made after the review, below): the stand-in
bundle on the development server is `3.6.0+standin`.

- **One event, `me`** (`wsEventMe`, `tellMe`, `tellEverybodyMe` in `access.go`), to one person or to
  everybody, with nothing in it, in place of `access`, `twitch_link` and `account_link`, which all
  carried nothing and all meant "ask again who you are". `TestContractMeEvent` holds it.
- **The panel asks the small question on the hot paths** (`identity.ts refreshAccess`): after a
  refused request, twelve to fifteen seconds after a `me` event, and once a minute while something
  plays in a window nobody is looking at (a tab in the background, playing for its sound), it asks
  `GET /access` and reads `/me` only when the answer is not what it holds. While something plays in
  a window that is looked at it reads `/me` each minute, as it did at 3.5.0: the panel and the small
  player show what only `/me` carries. `/me` is still read on the
  `me` event itself, at start, at sign-in, on a reconnect, when the panel opens, and when its window
  is looked at again (`PanelGate`, at most once in ten seconds). That last one was left as it was on
  purpose: `me` also carries what no event announces (an admin's alerts, whether the reader's own
  stream is live), so the small question there would leave those to the next opening of the panel.
- **The panel's re-reads say which copy is in hand** (`store.ts refreshSnapshot`, `accounts.ts`): the
  30-second poll, the panel's own refresh and a reconnect send `If-None-Match` with the snapshot's tag
  (the `streams` event's tag, or the `ETag`), and the accounts read sends its own, so nearly all of
  them are a 304. A read's answer never replaces a newer push. A poll answered 304 still tells the
  page's components that a poll happened, as a poll did at 3.5.0. A reconnect now re-reads the snapshot
  and the accounts too: it used to read `/me` alone, and a push missed while the socket was down
  stayed missed until the next poll.
- **One handler** in the panel (`index.tsx meHeard`) and in Matty (`"me" -> resync()`).
- **A "that one still" answer is read to its end** (`client.ts requestIfChanged`). Seen in a browser:
  an answer nobody reads is put down in the network log as a request that failed (cancelled, in
  red), and the 304 is the commonest answer these reads get, so every poll looked like a failure to
  anybody with the log open. Nothing was wrong with what the page did with it.

**Checked:** `make check`, which now runs `build/check-reads.mjs`: `identity.ts refreshAccess`,
`store.ts refreshSnapshot` and `client.ts`'s conditional requests, each run as it is written against
a played server (the wiring and the accounts store's conditional read are in
`build/check-away.mjs`); Matty's 120 unit tests; on the emulator the new app starts with three reads
against 3.6.0, shows the panel, and after a `me` event sent from outside asks `/access` and re-reads
the snapshot and the accounts with their tags (two 304s).

**In a browser**, against `3.6.0+standin` on the development server (the desktop app's built-in
browser with its pane not on screen: the page was driven by its elements and read from its network
log, and through a logging forwarder where the headers mattered): a `me` event is followed by `/me`
and `/accounts`, and twelve seconds later by `/access`; the open panel's 30-second poll is a 304 each
time; with a stream playing the page asks `/access` once a minute and never `/me` (that page was
not on screen; since the review, one that is looked at reads `/me` each minute); and after the
page's socket was dropped and came back it read `/me` and re-read `/streams` and `/accounts` with
`If-None-Match`, both answered 304.

**Not checked:** the read after a refusal, in a browser (its checks only); the panel by eye (nothing
was looked at on a screen; nothing it shows was meant to change); anything real. One thing the
browser showed that is not this item's doing: a first read of the snapshot, sent with no tag, is made
conditional by the browser's own cache (`/streams` is `private, no-cache` with an `ETag`). `/accounts`,
`/me` and `/access` are `no-store`, so nothing of anybody's Steam is ever kept there.

### The same revision 3: awards decided on the server, and the boards out of the pushed snapshot (16 November)

The last of the six, built at Victor's word ("Build item 6"). It removes fields from the snapshot,
which revision 3 (unreleased) already allows: nothing else moves, and it is still 3.6.0.

**What it was.** The snapshot carried the four boards, the medals, the fans, the achievements and
everybody's choice of awards, and each client worked out from them who holds what: the panel in two
places (`awardLogic.ts` for what goes beside a name, `AwardList.tsx` for a profile's rows), and Matty
in a port of the second made by hand (`VillaNetAwards.kt`, "ported from the plugin's web app"). And
because a board's seconds tick for as long as anybody is on the air or being watched, the whole
snapshot went to every connection once a minute for all of that time (3.5.0 had brought it down to
that from every poll).

**What it is.**

- **The server decides** (`server/awards_decide.go`, the only copy of the rules): what goes beside
  each name (`people[id].awards` in the snapshot: the person's choice, or Automatic), each Live now
  card's badges (`streams[].awards`), each person's whole record as their profile shows it
  (`people[id].rows` with the boards: rows in order, each with its title and its facts, nothing
  formatted), and for the picker everything its reader holds (`held` in `GET /awards/mine`). The
  cases `build/check-awards.mjs` held the panel's rules to are Go tests now
  (`awards_decide_test.go`).
- **The boards are their own document**, `GET /api/v1/boards` (`server/boards.go`): the same lists
  under the same names, each family of achievements with what its steps are called, and
  `people[id].rows`. Conditional, like the snapshot. A client reads it when somebody opens a
  profile, the Trophies card or the Boards tab, each minute while a board stays open, and when the
  snapshot's one word about it (`boards.version`) moves, which it does for anything but a board's
  seconds; the web panel also as its page starts, because its header and cards draw from it, and
  Matty only once somebody looks. **Seconds that only tick push nothing any more**, however long they tick for.
- Gone from the snapshot: `allTime`, `month`, `monthLabel`, `allTimeTotal`, `monthTotal`,
  `monthMedals`, `watchBoards`, `watchMonth`, `watchAllTime`, their totals, `bestClipper`,
  `awardChoices`, `fans`, `fansOn`, `achievementKinds`, `achievements`, and a card's `allTimeRank`
  and `monthRank`. Nobody's choice leaves the server any more, only what it comes to.
- **The panel** draws what it is sent: `boards.ts` keeps the page's copy of the boards (read at the
  start, on the version, when somebody looks), `AwardBadges`, `LiveCard` and the clapper beside
  "Clipped by" read the decided lists, `AwardList` draws the rows, and the Trophies card and its
  tabs read the boards while it is open. `awardLogic.ts` is down to the picker's preview of a
  choice being made, the words for a ladder and the toast. Nothing it shows was meant to change:
  the markup is what it was (the style check counts the same 872 classes).
- **The lab** has no server, so its world makes what one would have sent with a stand-in for the
  Go rules (`webapp/lab/awards.ts`), which `check-awards.mjs` holds to the same cases as the Go
  tests, row for row.
- **Matty** reads the boards when a profile or the panel opens and while its Boards tab is on
  screen (`VillaNetBoards.kt`), and its profile draws the server's rows: `VillaNetAwardLogic` keeps
  the artwork and the formatting of figures, and its port of the rules is gone.

**The boards are open to every signed-in reader**, as the snapshot they rode was (`routeClasses`,
one line): built that way, and Victor's answer when asked afterwards (16 November, "Keep them
open"). The research proposed putting them behind the watch gate. That would take the award rows
off a locked reader's profile popover, which shows them today, so it would change what somebody
sees. What a locked reader is *sent* without asking is less than before (the decided badges, not
the boards); what they *can read* is the same. The README's "Where it stops" says so.

**Left where it was:** the names of an achievement's steps for a ladder come from the server now
(`names` with each family), but the panel still words a board's place for the small tallies of
second and third places ("Runner-up in 2 months: …"), as Matty does for a screen reader; and the
badge artwork is still bundled in both clients (serving it from the plugin was researched with
this item and is not part of it). Matty still draws no badges beside names or on its stream cards:
it is sent them now and lists them as not read.

**Checked on 16 November.** `make check`, with the rules as Go tests, the boards' fixtures
(`boards.json`, `boards-quiet.json`) and the panel's checks (`check-awards`, 35, holds the lab's
stand-in to the server's cases; `check-reads`, 9, runs the boards store). Matty's 127 unit tests,
which decode those fixtures and run its copy of the boards (`VillaNetBoardsTest`). And on the
development server with the stand-in bundle:

- **Pushes, counted on the socket.** Before, with three streams on the air and nothing happening:
  the whole snapshot, 4190 bytes, to every connection once a minute. After, in three minutes of the
  same: none. The only two pushes in that time (3315 bytes) were a vote taken back and put back by
  hand, each of which moved `boards.version`, and it came back to the same word.
- **Matty on the emulator, through a logging forwarder.** It starts with its three reads and asks
  nothing of the boards. Opening the panel reads `/boards` once; the Boards tab shows the two boards
  and reads again each minute while it is on screen, with `If-None-Match`; a profile shows the
  record (Champion twice, Best Clipper, Achievements) and asks nothing while the copy is fresh. A
  vote taken back: the push arrived, the app read the boards two seconds later, and the open
  profile's row changed to "1 upvote on 1 clip".
- **The panel in a browser, on screen this time.** The decided badges beside names in posts (crown,
  calendar and clapper; crown, calendar and binoculars; the clapper beside "Clipped by"). A choice
  made for one person ("snooper, alltime") showed beside their name and on their card within
  seconds, in that order, the boards' version unmoved. The Trophies card's five tabs from the
  boards: both pairs of boards, the Best Clipper and their best clip, the ladders, and You with
  the picker's chips from `held` and "Next: Streamed 10 hours" in the server's words. A profile
  popover's rows. `/boards` was read as the page started, once for each change of version, when the
  card or a popover opened with a copy a minute old, and once a minute while the card stayed open.

Found by watching and fixed: in both clients the read made each minute while a board is open landed
every other minute. The timer asked whether the copy was a minute old, and it was always a few
milliseconds short of it. The timer is the schedule now, with a check on each side.

**Not checked:** the lab by eye (it builds); a locked reader's profile in either client (the route
being open to them is a Go test); anything real.

### Reviewed before release (16 November)

An independent review of the whole of 3.6.0 and Matty 0.1.20 changed, on the server: the WHEP offer
check reads an offer with pion's own SDP parser and refuses more (see "One refusal body"), and a body
over 64 KiB is refused unread; a Steam shortcut's id is no app id, so such a game is matched by name
as 0.1.19 did; each stored snapshot is numbered so an older one told late cannot put back cards
that have ended; somebody deactivated stays out of the activity lines, connected or not, and comes
back when let in; tied "Biggest fan of" rows are in the order the clients had (blind to case and
accents); the clapper says "Best Clipper, all time" beside a name, as the panel does (its chip in
the picker keeps "Best Clipper"); a failed poll carries the boards' totals, so `boards.version` does
not move for a hiccup; a tagged `GET /boards` before the first publish is `304`; when a clip goes is
part of `clips.version`; an empty token is answered
"That did not get through. Try again."; the stand-in build checks the Site URL again where it takes
the stand-in's word; and a board cut is logged once per count. Tests that could not fail were made
able to. In the panel, the plan card in a post, the read of `/me` each minute while something plays
in a window that is looked at, and what a poll does for the page's components (a poll answered 304
hands them a copy of the snapshot in hand, so a card's "live for" moves on and a popped-out tile
whose stream has gone is given up) are back to how 3.5.0 had them; the You tab's picker draws from
the last answer the page had; a `me` event's read starts after the event, and counts as the
connect window's sign only when the reader's own connection changed (or the read after it said
nothing at all); and a read of the boards that fails, or comes back from before the version moved,
is made again: by the first snapshot the page hears half a minute later, for as long as it takes,
and in Matty by the next snapshot that says the word (once more for an answer that came back
behind) or the next look. The metering of takebacks is as released in 3.5.0, and the contract now
says exactly which are never metered. Left as 3.5.0 worded it: the clips store's sentence ("Clips
could not be saved just now.") also answers a read that failed, and Matty prints it then; the
System Console's Clips page shows that sentence as sent, so changing it waits for a preview.

## The release before: 3.5.0

**Victor is still running 3.2.x on his real server**; 3.2.1, 3.3.0, 3.4.0 and now 3.5.0 are built and
not installed there. 3.5.0 builds his request of 16 November, made in the Matty project: "Homogenize
the development of the VillaNET Mattermost Plugin and the Matty app together … find any efficiencies
that can be made on either side … as native to each other as possible. Don't worry about backwards
compatibility." It was written from the Matty session, both trees side by side, and is released as a
pair with **Matty 0.1.19**.

What 3.5.0 is, in one paragraph: a written **client contract** (`contract/`, revision 2) between the
server and its two clients. The server works out once who is live, in voice and wearing its badge
(`snapshot.people`, `server/people.go`; the badge rule in `reconcileBadges` reads the same live rule)
and both clients look people up. `GET /streams` and `GET /accounts` answer `304` to a client that
holds the last answer (the `streams` event carries the tag); `GET /access` is the one small read about
the reader, with `contract` and `version`; a link and an unlink answer with the caller's access.
Board seconds that only tick are sent once a minute by the clock (`noticeable`, `boardFigures`),
where every poll used to push the whole snapshot to every connection while anybody was live. The
`accounts` event goes out at once for the first change of a quiet ten seconds. Two requests changed
form: the title search is `POST /media/search` with a JSON body, and unlinking is the unmetered
`DELETE /me/link` (a `POST` with no token is 400). `GET /me/profile` is answered only with the script
header. `steam.at` left `GET /accounts`; `steam.fresh` is always sent. Nothing visible changes in the
panel: no previews were needed, and none were made.

**The pair.** Matty 0.1.19 keeps no fallback for an older VillaNET: by the version Mattermost's own
plugin list gives it knows a plugin from before 3.5.0 without asking it anything, and shows "VillaNET
needs an update" in place of the panel. So on the real server **3.5.0 goes in before, or with,
Matty 0.1.19**; Matty 0.1.18 keeps working with 3.5.0 except its Unlink (which posted an empty token).
`contract/README.md` says how the two are kept in step: the Go tests write `contract/fixtures/`,
Matty's `tools/sync-villanet-contract.sh` copies them into its tests, and its `VillaNetContractTest`
fails on stale copies, on a default that assumes what the server did not send, and on a field the
server sends that the app neither reads nor lists as not read.

**What was checked on 16 November.** `make check` (go vet, the Go tests under -race, gofmt, the
version check, tsc and every `check-*.mjs`) passes. The pair ran on the Matty project's development
server (Mattermost 12.0.2, its `tools/dev-server/villanet.sh`) against `build/fakebb`, a stand-in for
Broadcast Box with real WebRTC playback, new in this version (`make fakebb`, never in the bundle): the
app's requests were counted through a logging forwarder (three at start where there were six, none on
opening the panel, `304`s after), a stream was paused for 50 seconds and came back, a dead stream was
let go in 30, an older bundle swapped in under the running app brought up the update message with no
request sent to it, and the panel's changed requests were made the way a browser makes them (cookie
session and CSRF token): the search by `POST`, seven unlinks in a row by `DELETE` with no 429, and
both refused without the CSRF token.

**Not checked:** the web panel in a browser (only tsc, the check scripts and the requests above);
anything on the real server; the real Steam, Hytale, Twitch or Mumble, as for 3.4.0. The real-server
checklists below still stand, and 3.5.0 carries everything in 3.4.0, 3.3.0 and 3.2.1.

**What the research for it proposed and was left for later**, ranked, with the reasoning, is in the
Matty session's notes; in short: finish the pair harness (a fake Steam and voice behind a build tag),
the rest of the contract folder (route and event tables from the code, a type check for the web
panel, shared rule vectors), clips and shared cards from one table, coded refusals on every route,
Playing/Streaming decided on the server, the panel on the same read rule (one `me` event), the
settings published once, and, largest, the awards decided on the server with the boards out of the
push.

The installable bundle is `dist/ceo.villa.villanet-3.5.0-linux-amd64.tar.gz`, built by `make dist-linux`
at 14:59 on 16 November, after both reviews' fixes: **11,077,367 bytes**, sha256
`62c25b26f8d8ea587b837831a7e2ffde68bc67a731f69c6542a0865c203a1b78`. The 3.4.0, 3.3.0 and 3.2.1 bundles stay in `dist/` beside it.

**The reviews.** The Matty session ran two rounds of independent review with a second agent trying to
refute each finding. The second round covered this tree: the server lens found five things, all fixed
(an unlink by somebody with nothing linked did the whole unlink, unmetered: it now does nothing; the
people index kept `badge: "live"` through the offline grace after its wearer had taken the status
back; the snapshot served before the first poll said `"people": null`; and two tests that could not
fail on what they claimed to hold). The web-panel lens found nothing.

## 3.4.0 (built 12 November; never installed)

**Victor is running 3.2.x on his real server.** 3.2.1 and 3.3.0 were built on 13 October and never
installed. 3.4.0 builds his request of 11 November: Steam account linking with Steam status, and
Hytale account linking, "meant to integrate into the Matty Android app". The contract, with his words,
his answers of 11 November and his preview picks of 12 November at its top, is `design/3.4.0.md`
(revision 2). His picks: **P1 B "Your stream and accounts"** (the Twitch tool leaves the bar; the link
key's sheet holds the stream, then Connected accounts with Twitch, Steam and Hytale; the Twitch row's
mark is the Glitch), **P2 B "Playing now"** plus the profile popover rows, **P3 B "One line of
words"**, **Matty connects in its own sign-in window** (Q1 A), the privacy notice's country, first generic, then **the United States**
(Q5, Victor, 12 November), connections **kept** on unlinking and switch-off (Q6, Q7), the **icon-only Steam profile pill**
with "On Steam · {name}" under a game (Q8), and plain **Continue on Steam** (Q9). The previews he
picked from are in `Broadcast Box Plugin.patches/previews-3.4.0/`, the research in
`Broadcast Box Plugin.patches/research-3.4.0/`.

3.4.0 is **built and not yet installed**, lab and tests only: nothing in it has met the real Steam,
the real Hytale, a real Mattermost or Matty. It carries everything in 3.3.0 and 3.2.1. The checkpoint
of the tree before it is `/home/vvilla/Documents/Broadcast Box Plugin.pre-3.4.0` (3.3.0 as built); it
is not to be edited.

The installable bundle is `dist/ceo.villa.villanet-3.4.0-linux-amd64.tar.gz`, built by
`make dist-linux` at 07:06 on 12 November, after the review's fixes, the last audit's two (below) and
the move to Go 1.27.4: **11,065,890 bytes**, sha256
`ceb65530b9deb25c73e214ecab56133390ef728476c4788ba99433839a0f10d3`. Its
`webapp/dist/main.js` is 790,470 bytes (196,696 gzip -9; 3.3.0's was 736,677) and its server binary
28,889,248 bytes, **Go 1.27.4**, `CGO_ENABLED=0`, `-trimpath`. (The Go 1.27.2 bundle of 06:59,
11,066,330 bytes, sha256 `226f4afd…cdcb`, the review fixes' bundle of 06:14,
11,066,144 bytes, sha256 `0c10cb5b…821a`, and the integration's of 05:11, 11,064,753 bytes, sha256
`0ca4aa2a…6d18`, are replaced by it.) Its `plugin.json` says 3.4.0 and `min_server_version` 11.11.0,
with the Steam and Hytale sections after Twitch. Its `main.js` has no lab stand-in (no
`__villanetLab`, `holdClock`, `srcdoc`, `labHooks340`, `labClipboard`, `setDaveSteam` or `labSay`)
and no preview code (no `preview340`, `narrowWord`, `voiceNames` or `data-variant`). The 3.3.0 and
3.2.1 bundles stay in `dist/` beside it.

**The checks on 12 November, after the last audit's fixes:** `go clean -testcache`, then `TZ=UTC make
check` (go vet, the Go tests under -race, 58 s; check-away 175, check-twitch 24, check-accounts 29,
check-awards 34, check-sounds 34, check-mentions 15, check-player 28), `make e2e` (46 s), `make lab`
and `make dist-linux` all passed (the last two with an npm stand-in that skips `npm install`, since
`node_modules` is in place and nothing may be installed). The same set passed after the review's
fixes. The integration's own run, before them:
`go clean -testcache`, then `TZ=UTC make check` (go vet, the Go tests
under -race, gofmt, the version check; React, tsc, check-styles, check-squeeze, check-away 175,
check-twitch 24, **check-accounts 27** (new in `make check-webapp`), check-awards 34, check-sounds 34,
check-mentions 15, check-player 28), `make e2e` (on its first run, 47 s;
`TestE2ERecorderReconnectsWhenAudioStops`, which flaked once in 12 runs in 3.3.0, did not), `make lab`
and `make dist-linux` all passed. The server lane also ran `go test -race -count=3 ./server/...` (160 s), a mutation run of 19 defences (each caught
by its test), and the `https://` grep (only the three swappable bases, the profile prefix and two
checked constants). No Go module changed; `go.mod` and `go.sum` are 3.3.0's. Twitch's server files
(`server/twitch_*.go`) are byte for byte 3.3.0's, and the Twitch-off snapshot golden file is unchanged
(A1).

**govulncheck** (v1.8.0) with Go 1.27.2 reported two called vulnerabilities in the standard library:
GO-2026-6859 (net/http, fixed in go1.27.4) and GO-2026-6809 (crypto/x509, fixed in go1.27.3); the
untouched 3.3.0 checkpoint gave the same two. As for 3.2.0's move to 1.27.2, `go get
toolchain@go1.27.4` moved `go.mod`'s `toolchain` line to **go1.27.4** (the `go 1.27.1` line and every
module are unchanged; the toolchain came through the module proxy into the module cache;
`~/.local/opt/go` is untouched). After it govulncheck reports **0 vulnerabilities in called code**, and
`go clean -testcache`, `TZ=UTC make check`, `make e2e`, `make lab` and `make dist-linux` all passed again
(07:06, with the npm stand-in).

### What 3.4.0 holds

| Item | What was built | Settings |
|---|---|---|
| Steam | Connect Steam: OpenID 2.0 sign-in at Steam's hard-coded endpoint, every check of §3.4 before Steam is asked (`check_authentication`, exactly the ten fields), each response nonce accepted once (`steam_nonce_`). Status from `GetPlayerSummaries`: one call a minute for at most 100 people, only about active Mattermost accounts, the key in the `x-webapi-key` header; statuses in memory only, dropped 5 minutes after Steam said them; 403 held until the Steam settings change, 429 2 min doubling, other failures 60 s doubling, both capped at 10 min. `server/steam.go`, `server/accounts*.go`. | **Steam** section: **Show people's Steam status** (off), **Steam Web API key** (secret), **Steam connections** (card) |
| Hytale | Connect Hytale: OIDC code flow with PKCE S256, `scope=openid hytale:profile`, HTTP Basic with each half URL-escaped and nothing secret in the body, the ID token's claims checked (no signature, OIDC Core 3.1.3.7), userinfo's `sub` must match, only the username and the lower-case profile uuid kept, the token revoked whatever happens, no refresh token. Identity only: Hytale has no status or location for other apps. `server/hytale.go`. | **Hytale** section: **Show people's Hytale names** (off), **Client ID**, **Client Secret** (secret), **Hytale connections** (card) |
| The core | One provider descriptor for both (`server/accounts.go`): the CSRF-protected ticket, the connect page, the single-use 10-minute state bound to provider, user and session, the callback (K1-K13), Matty's `app-connect` and `app-finish`, the link documents by compare-and-set (`steam_links`, `hytale_links`), Disconnect (open to everybody, idempotent, works while off, and takes every takeover notice naming the account with it), block, notices, Delete all (keeps the block list), quarantine of an unreadable document, and the pages (Twitch's headers, CSP and close script). The accounts loop and view (`accounts_live.go`, `accounts_view.go`), the admin routes (`accounts_admin.go`), `UserHasBeenDeactivated`. | none of its own |
| `GET /api/v1/accounts` | Everybody's Steam status and Hytale names, `routeWatch`, from memory, `people` always an object; never in the snapshot or on `/streams`. The `accounts` event (everybody, empty, at most one per 10 s, trailing) and `account_link` (one person, empty). | |
| P1 B | The Twitch tool left the bar (five tools at most). The link key's sheet is **Your stream and accounts** whenever an account row shows: token bar, Save, Unlink, then **Connected accounts** with Twitch (the Glitch, LIVE NOW in red), Steam and Hytale rows; a row's Connect turns the sheet into that service's flow with a back arrow. `TwitchSheet` gained its `embedded` mode. With no row, the sheet is 3.3.0's **Relink your stream**. `components/AccountSheet.tsx`, `Sidebar.tsx`. | |
| P2 B | **Playing** under In voice, one line, only while somebody connected is in a Steam game, Mattermost display names (`PlayingStrip.tsx`); the profile popover's Steam and Hytale rows for readers who may watch (`AccountRows.tsx`, `PopoverLive.tsx`), icon-only profile pill, Copy and Copied in one cell, a lighter red on dark themes. | |
| P3 B | `AccountsDisconnectKey` (`AccountsKey.tsx`) under the lock's bar, under the not-set-up sentence and over WATCH ONLY: `TwitchDisconnectKey` itself for Twitch alone, otherwise "Disconnect  Twitch · Steam · Hytale", a press turning the whole line into that service's words for 4 s. | |
| Notices | Steam's and Hytale's takeover notices over the linked bar with Twitch's, one at a time, newest first (`AccountNotices.tsx`). | |
| Console cards | `AdminSteam.tsx`, `AdminHytale.tsx` (`AdminAccountParts.tsx`), in AdminTwitch's look; no per-person Steam status anywhere. | |
| Matty's API | `README.md` "Native apps (Matty)": every route, class, body, answer, code and event, and the sign-in window. Matty itself is not changed. | |
| Words | README (New in / Upgrading to 3.4.0, Steam (optional), Hytale (optional), Native apps (Matty), the four browser cases, the privacy notice, the remedy for the Steam callback in the proxy log, Delete all before removing the plugin), LOCKDOWN (the Steam and Hytale paragraphs with the nginx remedy, four cases), plugin.json, and `docs_test.go` holding every no-contact promise to name Twitch, Steam and Hytale and catching "in exactly N cases" and "only outside connections". | |

### What the build decided where the contract was silent or the task overruled itself

The server lane (two parts), the webapp lane (two parts) and the integration each reported their
choices; all are in the code's comments too.

- **Server.** The provider descriptor carries more than §2.2 lists (`nameKind`, `connectPath`,
  `callbackPath`, `callbackURL`, `declined`). Each Steam status is forgotten 5 minutes after Steam said
  it (a connect's own name read counts), and `fresh` stays the global last-good rule. A kick always
  brings the next step forward (10 s gap) but never calls Steam inside a backoff, so a deactivation or a
  cluster catch-up still shows during a long Steam wait. Only a change to **EnableSteam** or the key
  lifts a refused-key hold. Delete all restarts the loop only if it was running. A connect's name read
  (S11) still calls Steam while the loop holds a refused key (§3.5). With no good check ever, the
  "out of reach" sentence reads "Steam could not be reached; there has been no good check yet."
  Words the contract did not give: "The Hytale Client ID or Client Secret has characters they cannot
  have."; "Your Steam account is connected." (connected page with no Steam name). Wrong-method answers
  on the new routes are plain 405s, as every existing route's. The admin cards' `active` comes from
  their own `GetUsersByIds`, so they work while the loop is off; a deleted account has `name ""`.
- **Webapp.** The pencil-only rule for a six-tool bar is retired (P1 B's bar). In a Steam or Hytale
  flow, Close after a refusal goes back to the list (one Escape rule). The new popover pills switch to
  #ff7a86 on a dark theme (5.0:1 on Onyx) and keep #c8102e on light ones (4.9:1 on Denim); on an
  offline row only the mark and the name fade, never the pill. The Playing eyebrow is #8a8a8a (5.7:1).
  Names Mattermost has not loaded for the Playing line are looked up once with `POST /api/v4/users/ids`
  and dropped at sign-out. With one non-Twitch service the line is one key, "Disconnect Steam". The
  Steam pill renders only for an address under `https://steamcommunity.com/profiles/<17 digits>`.
  After a notice's OK, focus goes to the next OK, then to the panel. Twitch's own key now stays until
  its words are gone when Twitch was the only connection (a regression the lane found and fixed).
- **Integration.**
  - The halves were joined by a temporary Go scene test that dumped `/me`, `/accounts`, the ticket
    refusals and both admin cards, empty, full (online, in a game, private, a nameless game, a
    takeover, a blocked person, a deactivated person) and switched off while connected; every field
    agreed with `types.ts`, and the test was deleted. The lab's mocks were brought into line with
    the server in three places: a switch with no key or application is **off** (no `/me` field, the
    ticket 404 `off`), as the server's `steamOn`/`hytaleOn` say; somebody Steam's last answer left out
    has state `""`, not `offline`; and the card's calls today stay counted after a switch-off.
  - **Admin alerts for Steam and Hytale.** `/me.steam.problem` and `/me.hytale.problem` reached the
    panel only as an unavailable row, so a problem that leaves Connect working (Steam refusing the key,
    slowing down, out of reach; Hytale refusing the application or its scope) was told nowhere.
    `AdminAlerts.tsx` now shows "Steam needs attention: …" and "Hytale needs attention: …" exactly as
    it shows Twitch's, pointing at the System Console card.
  - **An account deleted for good** shows on the console cards as "account deleted" with its user id,
    instead of an empty name marked "deactivated" (§3.8 names both).
  - `twitchEmbed.tsx`'s header counts the four cases as README and LOCKDOWN do.

### The review's fixes (3.4.0, 12 November)

Phase R (§9.5) confirmed nine findings, all minor; one more (web-4) was dropped. All nine were fixed
at the root, none against Victor's picks or the contract, so none was skipped. Each has a test that
fails without its fix (checked by taking each fix out again in a scratch copy of `server/`), or a
source check in `check-accounts.mjs` / `check-twitch.mjs`, and the webapp ones were walked in the lab
with real key and mouse input. The full table is "Review log (build)" at the end of
`design/3.4.0.md`.

- **sec-1 / srv-2, Delete all and a connection already on its way.** A callback or Matty app-finish
  that had spent its state was still talking to Steam or Hytale when Delete all ran, and wrote its
  SteamID or uuid back afterwards, even with the switch turned off first. Delete all now writes
  `cleared` (its time) into the fresh document, always, even where nothing was stored; `setAccountLink`
  refuses a connection whose state was made no later than that, inside the same compare-and-set (so it
  holds on a cluster), answered as `expired`. `finishAccount` also looks at the switch again after
  the service has answered (`off`). `TestDeleteAllStopsAConnectionAlreadyOnItsWay` (Steam and Hytale;
  browser, Matty, switched off first). Twitch's `deleteAllTwitchData` has the same gap and is left
  as it is, Twitch being unchanged in 3.4.0 (open item below).
- **srv-1, Hytale's "refused the Client ID or Secret" outlived the fix.** Changed Hytale settings
  (switch, Client ID or Secret) now forget the last sign-in's problem, as Steam's settings lift
  Steam's hold. `TestAFixedHytaleApplicationIsNotStillSaidToBeWrong`.
- **srv-3, a Disconnect that changed nothing kicked the loop.** Any session could bring Steam's next
  call to 10 s. A Disconnect, notice OK, unblock or Clear that changes nothing now installs nothing,
  rebuilds nothing, kicks nothing and tells nobody. `TestALinkWriteThatChangesNothingDoesNothing`.
- **srv-4, two link writes could be cached in the wrong order.** Each service's link writes on a node
  now hold its write lock from the read to the install of the result (lock order runLock, write lock,
  lock), and a write that changed nothing installs nothing. `TestLinkWritesAreCachedInTheOrderTheyWereSaved`.
- **web-1, focus fell to the body when a service went from somewhere else.** `AccountsDisconnectKey`
  now reads where focus is as each render begins, whatever changed the line: focus that falls goes to
  the words while its own press is out or saying, else to the key that took the gone one's place, and
  to Twitch's own key when the line turns into it, or to the lock or panel. Lab: all four cases land
  on a key or the lock, never BODY; a keyboard press still says its words, then hands on as before.
- **web-2, a flow stayed open after the reader was blocked.** Sidebar sends a flow back to the list
  when its row leaves the list (not while a Disconnect's words hold the place), as 3.3.0's Twitch sheet
  closed with its tool. Lab: Steam's and the embedded Twitch flow both go back; the words still hold.
- **web-3, nameless people on the console cards.** Every label, hint and note uses the Mattermost
  name or else the user id; the server sends `deleted` (only when Mattermost answered without the
  account), so "account deleted" is never guessed from a missing name ("deactivated or deleted" when
  Mattermost could not be asked). The lab mock gives a deleted account no name. Lab shot taken.
- **web-5, checks on functions nothing called.** `PopoverLive` now draws exactly `popoverRowsFor`'s
  rows (the rows carry the pill's name and address), and `AccountFlow`'s Waiting is `waitingAt`'s
  answer on the panel's clock; `check-accounts.mjs` holds both components to that at the source
  (27 checks become 29). Lab: the popover and Waiting look as before.

The lab shots of the fixes are in the fixer's scratchpad
(`/tmp/claude-1000/-home-vvilla-Documents-Broadcast-Box-Plugin/7553f2d7-302c-4aee-b0c9-4cacc3c4eaa9/scratchpad/shots/web*.png`),
which is temporary.

### The last audit's fixes (3.4.0, 12 November)

The acceptance audit found every criterion met and two gaps, both closed (neither touches Victor's
picks or the contract; "Review log (build)" in `design/3.4.0.md` has them):

- **A stale promise in the System Console.** The Mumble section's **Show who is in voice** help still
  said Broadcast Box, Mumble, the title lookups and Twitch were "the only outside connections VillaNET
  makes". It now names **Show people's Steam status** and **Show people's Hytale names** too.
  `docs_test.go`'s pattern gains "only outside connections?", which missed it, and
  `TestTheNoOutsideContactPatternKnowsEveryWording` holds the pattern to one sentence of each wording
  used, that one among them. With the old sentence put back in a scratch copy, both promise tests fail
  on it.
- **Playing hid whoever had just started.** The line's scroll snapping carried the row along with the
  person it rested on, so at 304 px, with Dave alone playing, Bob starting moved the row to scrollLeft
  55 with Bob behind the start's fade; one to eight left 468. `PlayingStrip` now keeps a row the
  reader left at its start there when who is playing changes (at once, and again a frame later); one
  they scrolled along keeps their place. The lab gains a **check: stays at start** chip
  (`__villanetLab.v340.playingStaysAtStart()`): nobody, one, two, eight, one, eight at 304 px, with
  `scrollLeft` 0 at every step and in every frame between. It passes, and fails without the fix (55,
  200, 468). Real wheel input in headless Chrome: a reader who scrolled along keeps their place
  through a re-read and a change of game, and once back at the start stays there.

### The lab walk (3.4.0)

Driven by script in headless Chrome (a copy of `tools/shot.mjs` that keeps one browser, emulates
focus and presses with the real mouse) against `dist/villanet-tv-lab.html` served on 127.0.0.1, every
screenshot looked at, at 304, 400, 464 and 776 px, the popovers in Denim and Onyx; compared with the
montages in `previews-3.4.0/montage/`. The shots are in `/tmp/claude-1000/b340-shots/` (temporary;
copy them to `Broadcast Box Plugin.patches/shots-3.4.0/` to keep them):

- `p1-bar-{304,400,464,776}.png`, `p1-sheet-*`, `p1-off-sheet-*`: the bar has five tools at most;
  at 304 px with the soundboard's speaker the title keeps "Arma …" and the eyebrow is cut to
  "STREAM …", as the B column of `p1-bar.png`. With everything off the sheet is 3.3.0's **Relink your
  stream**, focus in the token field; with any row, **Your stream and accounts**, focus on its
  heading, and the key's name "Linked as villanet-victor. Your stream and connected accounts."
  Nothing connected, and connected (Twitch LIVE NOW in red, Steam online, playing, a 32-character
  game, private, offline; Hytale) at four widths, and switched off while connected ("(Steam is
  switched off here)"). As `p1-sheet.png` B with the Glitch.
- `flow-*`: each Steam and Hytale step at 304 and 400 px: Getting ready…, confirm, refused by code
  (`rate_limited`, `store`, `interactive_only`), waiting with the 4 s pop-up hint, Hytale's 60 s hint,
  connected, Disconnect armed, failed ("Could not disconnect Steam. Try again in a moment."), just
  disconnected, and after 6 s the list with focus on that row's Connect; the embedded Twitch flow
  ("Connect Twitch", back arrow, no second head). Escape in a flow goes back to the list, on the list
  it closes the sheet with focus on the key. As `p4-steam.png`, with departure 4.
- `rows-*`: the three rows at 304 px with each Disconnect armed (the words on the second line, the
  16-character Hytale name whole); an admin's unavailable rows with both callback problems.
- `admin-alerts-400.png`: "Steam needs attention: …" and "Hytale needs attention: …" (integration).
- `notice-*`: one notice, and three at 304, 400 and 776, newest first; OK shows the next, focus going
  to the next OK, then to the panel.
- `playing-*`: nobody (no line, the gap given back), one, and eight at four widths, scrolled to the
  end at 304; each item titled "{name} is playing {game} on Steam". As `p2-playing.png` B.
- `p3-*`: the lock's and WATCH ONLY's line with all three at four widths; each word armed at 304 px,
  the whole line replaced by that service's words and never cut (the key's scrollWidth equals its
  width); Twitch alone (3.3.0's **Disconnect Twitch**), Steam alone ("Disconnect Steam"), two; Steam
  disconnected by keyboard on the lock: focus on the words, then on the line's first word. As
  `p3-lock.png` and `p3-watchonly.png` B.
- `pop-*`: a locked reader's popover has no Steam or Hytale row (Onyx and Denim); let in and linked
  have both, after On Mumble. Dave's Steam row in ELDEN RING, a 30- and a 60-character game (cut, the
  full words in the title), a game with no name ("In a game"), online, offline (mark and name faded,
  the pill not), Denim and Onyx (the pills light red on Onyx); Bob's Hytale Copy, Copied in the same
  cell, Copy refused (the name selected). As `p2-popover.png` Q8 · 2.
- `console-*`: both cards set up and working (Dave deactivated, "it answered for all 3 active
  people", a blocked person), not set up and off, Steam's key refused with Hytale's scope and both
  set-aside copies, Steam out of reach, and both callback problems; no status anywhere. As
  `p4-console.png`.
- `pages/` (48 shots at the Connect window's 520 × 760) and the montages `m-pages-steam.png`,
  `m-pages-hytale.png`: every page id for both services, as written by `TestAccountPagesRender`; a
  name holding `<b>M&"</b>` shows as text. As `p4-pages.png`.

### Tested only in the lab and the tests (3.4.0)

- **Steam and Hytale themselves.** Every request to them is made to a local fake in the Go tests
  (`steamOpenIDBase`, `steamAPIBase`, `hytaleBase`). What Steam's and Hytale's real pages, answers,
  rate limits and refusals do is UNVERIFIED (contract Appendix A); the checklist below is where each is
  first seen.
- **Mattermost.** Sessions, `GetUsersByIds`, `UserHasBeenDeactivated` and the WebSocket events are
  mocks in the tests and stand-ins in the lab. The popover's dark-theme detection reads
  `--center-channel-bg-rgb` (or a hex `--center-channel-bg`), set inline on the lab's fake popover and
  on `:root` by real Mattermost.
- **The windows.** In the lab, Continue opens no window: a stand-in "connects" Victor after a moment
  or never answers, and a pop-up blocker and the desktop app are switches.
- **The callback pages** are Go; the lab cannot serve them. `VILLANET_PAGES_OUT` writes all 48 to HTML
  files, which were shot at a pop-up's size.
- **Matty**: only the server half exists; the app's sign-in window is later Matty work.
- **The nginx remedy** in LOCKDOWN is read from nginx's documentation, not run here.

### Victor's B0 tasks (before the real-server checks; none blocks installing)

1. **Hytale**: at accounts.hytale.com → Third-party developer apps, apply for a **confidential**
   client with the scopes **openid** and **hytale:profile** and, as its Redirect URI, exactly
   `<Site URL>/plugins/ceo.villa.villanet/api/v1/hytale/callback` (no trailing slash, no query; System
   Console › Hytale connections shows it with Copy). Read the portal's developer terms and say if any
   rule touches showing or keeping usernames, and whether the portal takes a second Redirect URI (for
   a lab).
2. **Steam**: make the Web API key at steamcommunity.com/dev/apikey, signed in to the Steam account
   that will own it, with the **domain name of the Site URL** (for example `chat.example.org`). Steam
   asks for an account that has spent 5 US dollars and uses the Steam Guard Mobile Authenticator.
3. **Where the server is** (Q5): answered 12 November, "The server is in the USA"; the README's privacy
   notice names the United States.

### The 3.4.0 real-server checklist (UNVERIFIED until then; contract §10.2)

After 3.2.1's F1, D1-D8 and 3.3.0's P1-P9 (neither was installed):

- **G1. Steam, web and desktop.** Connect in Chrome, Firefox and the desktop app. The desktop popup
  carries the session and the page closes or says to. Note what Steam's page says about the realm, and
  whether QR sign-in works in the desktop popup.
- **G2.** Post `![x](https://<site>/plugins/ceo.villa.villanet/api/v1/steam/connect)` in a test
  channel and open `…/steam/connect` by hand: the `stale` page, or nothing; no Steam contact; no count.
- **G3. Steam status.** With a public profile: online, then in a game; the row and the Playing line
  within about two minutes. Make the profile private: offline here, and the owner's row says why.
  Note the lag.
- **G4. Hytale.** With the application approved: connect, pick a profile, see the username, Copy it.
  Note what Hytale does for an account with no game profile, and whether `invalid_scope` comes back
  to VillaNET or stays on Hytale's page.
- **G5.** Unlink (or be let out) while connected: the lock screen's and WATCH ONLY's Disconnect line
  disconnects each service, by mouse and by keyboard.
- **G6.** Switch each service off with people connected: Disconnect still shows and works; nothing
  else shows.
- **G7. System Console:** Disconnect, Disconnect and block, Unblock, Clear, Delete all, with the
  switches on and off.
- **G8.** `curl -sI` a callback page: the CSP of §2.8, and nothing the proxy adds. Then look at the
  reverse proxy's access log after a Steam connect: if the line for `…/api/v1/steam/callback` holds
  `openid.claimed_id` (the Steam ID), apply LOCKDOWN's remedy and check that the next connect's line
  holds the path only.
- **G9.** After an hour with people connected: the card's call count is about 60 more (one call a
  minute, whatever the number of people).
- **G10. Matty, once its later change exists:** the sign-in window on a real phone, Steam (password
  and Steam Guard; QR) and Hytale (Turnstile); the interception of the return; every refusal's words.
- **G11. Onyx and another dark theme:** the popover's Steam and Copy pills read light red; a locked
  reader's network tab shows no `GET /api/v1/accounts` and their popovers no Steam or Hytale rows;
  Playing names for people Mattermost has not loaded; Copy in the desktop app and Firefox.
- **G12. Twitch, moved:** Connect and Disconnect Twitch from its row under Connected accounts, the
  LIVE NOW word while live, and Twitch's takeover notice beside Steam's and Hytale's.

### Open for Victor (3.4.0)

- **The existing Watch pill** in the profile popover is about 2.2:1 on Onyx; 3.4.0 does not change it
  (departure 7). Say if it should get the new pills' lighter red on dark themes.
- **The dimmed (offline) Steam popover row's name** is at 0.6 opacity, as in the approved preview,
  about 3.3:1 on Denim; the pill beside it keeps full strength.
- **Go 1.27.4**: the `toolchain` line moved from go1.27.2 to go1.27.4 to clear two standard-library
  findings (above). Say if you would rather stay on 1.27.2 (`go get toolchain@go1.27.2`, then rebuild).
- **Words the builders chose** where the contract had none: the P3 key's title for Steam and Hytale
  ("Your Steam account, {name}, is connected to VillaNET. Disconnecting removes it."), the console
  cards' "both active people", "Asked Steam … ago; it refused the key." and "Nobody connected is
  active in Mattermost, so nothing is asked of Steam."
- **A cluster** asks Steam once a minute from each node (three nodes, 4,320 calls a day), and a link
  written on one node shows on the others within a minute.
- **Twitch's Delete all** (`deleteAllTwitchData`) has the gap the review found in Steam's and Hytale's
  (sec-1): a Connect Twitch already past its state when it is pressed can write its connection back.
  3.4.0 leaves Twitch's server byte for byte as it was; the same `cleared` fence would close it in a
  later Twitch change.

## 3.3.0 (built 13 October; never installed)

**Victor is running 3.2.0 on his real server.** 3.2.1 was built that same day and never installed.
3.3.0 builds the second item of his 13 October request, which 3.2.1 left for previews: "Have the clip
viewer player be the same aesthetic as the main video stream player." He saw three looks
(`Broadcast Box Plugin.patches/previews-3.3.0/montage/`) and picked **A, "Tile"**, with **Download in
the player** (3.2.1's pill after Share goes) and **the streamer only** on the name line. The contract,
with his words and picks, is `design/3.3.0.md`.

3.3.0 is **built and not yet installed**. It carries everything in 3.2.1, so installing it on Victor's
3.2.0 server also does what "Upgrading to 3.2.1" in the manual (`docs/upgrading.md`) says (the fan opt-out list deleted at
the first start; **Let people download clips** added, off), and its checklist below comes after
3.2.1's (F1, D1-D8). The checkpoint of the tree before it is
`/home/vvilla/Documents/Broadcast Box Plugin.pre-3.3.0` (3.2.1 as built, without `dist/`); it is not
to be edited.

The installable bundle is `dist/ceo.villa.villanet-3.3.0-linux-amd64.tar.gz`, built by
`make dist-linux` at 14:43 on 13 October, after the review's six fixes (below): **10,945,901 bytes**,
sha256 `28987d2b2703c257b317d1e289b9fee2bd6adbe7649721ff0432f837740855d4`. Its `webapp/dist/main.js`
is 736,677 bytes (184,229 gzip -9; 3.2.1's was 722,525) and its server binary 28,631,200 bytes, Go
1.27.2, `CGO_ENABLED=0`. Its `plugin.json` says 3.3.0 and `min_server_version` 11.11.0; its `main.js`
has no lab stand-in (no `__villanetLab`, no `holdClock`, no `srcdoc`) and no preview code (no
`clipPreview`, no `data-look`, none of looks B's or C's classes). The 3.2.1 bundle stays in `dist/`
beside it. (The first 3.3.0 bundle, built at 13:43 before the review, was 10,945,634 bytes, sha256
`b5bb6fe7b91a679dc4d66cba5373069b0d4828c26fc22731bfb35e50678d2650`, main.js 735,629 bytes; it is
replaced and is not to be installed.)

**The checks on 13 October, after the review's fixes:** `go clean -testcache`, then `TZ=UTC make
check` (go vet, the Go tests under -race in 129 s, gofmt, the version check; React, tsc,
check-styles, check-squeeze, check-away 166, check-twitch 21, check-awards 34, check-sounds 34,
check-mentions 15, and check-player 28, which was 23 before the review), `make e2e`, `make lab` and
`make dist-linux` all passed. `make e2e` failed once, on its first run straight after the `-race`
suite, in `TestE2ERecorderReconnectsWhenAudioStops`, which is server code nothing in 3.3.0 touched. It
then passed three whole runs (47 s each) and that test alone eight times in a row (`-count=8`), so it
is a real-time flake, noted here in case it comes back. No Go module changed; the only server change
is `manifestVersion`.

### What 3.3.0 holds

| Item | What was built | Settings |
|---|---|---|
| The clip player, look A | `components/ClipTile.tsx`, one player for the Clips dialog's viewer **and** the System Console's **Watch**, in place of the browser's `<video controls>`. The stream tile's frame (`.vntv-player`, `Screen`) and its line (`TileInfo`: the streamer's face and name, the time "0:12 / 0:45" at the end). The tile's controls over the picture while it is pointed at or has focus (play, pause or Replay; mute and volume; Download when offered; full screen), held up while paused; a red scrubber along the bottom edge that is a slim line at rest; a click on the picture plays or pauses; the tile's tuning burst saying LOADING, a ring when stalled, CAN'T PLAY on static with Try again on the tile's line. The timeline is the clip's own (lead-in off it, nothing past the end), and it stops at the end with Replay. Keys: Space or K, ←/→ five seconds, J/L ten, M, F; the scrubber's arrows, Page Up/Down, Home, End. No `controls` and the picture's context menu blocked: no Save video as. The pure rules in `webapp/src/clipLook.ts`. | none |
| Download in the player | 3.2.1's flow (`downloadClip`, `saveClipFrom`) pressed from an arrow among the player's controls, only while `clipDownloadOffered`, CAN'T PLAY included, where it is the one control left on the picture; "Preparing…", "Your download has started." and the refusals on the tile's line. The pill after Share is removed. Never in the System Console. | 3.2.1's **Let people download clips** (off) and the streamers' switches, unchanged |
| The failure line is red | `.vntv-clipview-status.vntv-notice-error` takes `var(--vntv-red-text)`: Report's and Delete's refusals were grey since `.vntv-clipview-status` comes later at the same specificity (3.2.1's Download refusals too). | none |
| The stream tile | Unchanged. `Hud` gained opt-in props (`edge`, `keepUp`, `onSurface`; no switch without `onPower`), `TileInfo` an `end`, `Tuning` the plate's word; a stream's tile passes none of them. | none |

### What the build decided where the preview, the contract or the task left room

- **`clipLook.ts` is the player's pure rules**, not the preview's switch store (`ClipLook`,
  `setClipPreview`, `downloadInPlayer`, gone): `clipLength`, `fileSecondsOf`, `clipAt`, `seekTarget`,
  `atEnd`, `ranPastEnd`, `shareOf`, `loadedShare`, `secondsAt`, `clipClock`, `clockWords`,
  `playerState`, `fileWait`, `playerKey`, `scrubberKey`. It imports only `clipLogic.ts`, so
  `build/check-player.mjs` runs it with clipLogic standing in, as check-away does with tagSearch.
- **Only look A's code.** Gone with B and C: the Hud's `centre` (a clip has no switch: a Hud given no
  `onPower` draws none), `keepUp`'s 'centre' (it is a boolean, `data-keep-up`), the `Switch`, B's row
  and `SoundGroup`, the `.vntv-cliptile-body` container (only B's and C's `@container` rules needed
  it), and the time tip over the scrubber's handle (A hid it; A's clock under the picture moves with a
  drag). The scrubber is always A's edge, so its rules are written as one.
- **`Tuning` takes the plate's word** (default "Tuning") rather than the preview's copy of it as
  `Loading`.
- **The clock reads the length at the end**: a 44.6 s clip ends at "0:45 / 0:45", where the
  preview's (position rounded down, length rounded) said "0:44 / 0:45".
- **"Loading the clip…"** (a clip opened before the viewer has it, only seen from a link or a slow
  server) shows the player's empty frame, the same size as the player, instead of 3.2.1's rounded
  stage, so the dialog does not change shape when the clip arrives. The stage's rules, and the old
  player's (`.vntv-clipview-video`, `.vntv-clipview-failed`), are removed.
- **Pressing Report or Delete clears what Download last said** under the picture, as one line of
  words did for all three in 3.2.1; the preview left it standing.
- **Full screen keeps the cursor over a paused clip**, whose controls are up
  (`.vntv-player:fullscreen .vntv-hud[data-keep-up="true"]`); a stream hides it when its controls
  sleep.
- **J and L** (ten seconds) are kept from the preview's code, though Victor's list names Space or K,
  the arrows, M and F.
- **The lab keeps the clip states** as a world field, `clipFile` ('ok', 'hang', 'fail'), with a
  **Clip file** row and `window.__villanetLab.clipPlayer` (`open`, `close`, `file`, `downloads`,
  `blockVictor`, `latency`, `frames`), in `lab/clipPlayer.ts`; the System Console's video follows it
  too. `lab/clipPreview.tsx`, the look and placement chips, is not carried over.
- **The System Console**: the tile reads well on the white page (a black object with the dark red
  frame, as in the panel), so nothing was changed for it. In the error state **Try again** takes the
  console's own white pill style, like the row's buttons just above the tile; said here in case
  Victor would rather have the panel's red pill.
- **LOCKDOWN.md** now says it is written for 3.3.0, with a line that 3.3.0 changes nothing it
  describes. The manual's upgrade notes (`docs/upgrading.md`) point people coming from 3.2.0 at 3.2.1's.

### The review's fixes (3.3.0, 13 October)

Six findings were confirmed by the review and fixed before the bundle above; none went against
Victor's picks, so none was skipped. Each was checked again in the lab with real mouse and key input
over CDP (headless Chrome), and code-1 also in headless Firefox 155 with a real dropped connection.
The shots are in `Broadcast Box Plugin.patches/shots-3.3.0/review-fixes/`.

- **ux-1, Download gone when the clip cannot play.** The failed tile drew no controls at all, so
  CAN'T PLAY took Download away just when downloading to play elsewhere was the way out (and a codec
  ruled out by `canPlayType` kept it, inconsistently). Now `clipLook.ts playerControls` decides
  what the controls hold: everything while the clip can be played; once the file has failed,
  Download alone while it is offered. It comes up as the controls do, when pointed at or tabbed to;
  at rest the error tile is as Victor saw it (static, CAN'T PLAY, the reason and Try again). Lab, Clip
  file **fails**, downloads on: at rest unchanged (`ux1-failed-rest.png`); pointed at, the arrow at
  bottom right (`ux1-failed-pointed.png`); Tab from Back reaches it first, then Try again
  (`ux1-failed-tabbed.png`); pressing it says "Preparing…" and then "Your download has started.";
  downloads off, no controls, and focus that was on the arrow goes to the player.
- **ux-2, the focused scrubber read out every second.** Its `aria-valuenow`/`aria-valuetext`
  followed the clip every frame. Now, while it has focus and the clip plays on, they hold where the
  clip was when focus came or the reader last moved it (`clipLook.ts heardAt`, the slider's version
  of the scissors' `labelKey`), and catch up once on pause, a drag, its own keys or Replay. Lab: 0
  changes in 5 s of play under focus (was 5); End while playing, one change, to "0:45 of 0:45";
  Replay, "0:00 of 0:45" held while it plays; a drag, where it was let go; pause, the time once.
  Unfocused, it still follows the clip.
- **ux-3, an invisible scrubber over LOADING.** It was only `opacity: 0`, so it stayed a Tab stop
  and took clicks, and a seek there was undone when the file came. Now
  `.vntv-cliptile[data-loading="first"] .vntv-hud .vntv-clip-scrub` hides it (`visibility: hidden`,
  `pointer-events: none`, one class above the reveals), the slider is `tabIndex -1` while asleep,
  focus that was on it goes to the player (where K still works), and `seekTo` refuses any seek
  before the file's metadata, which `onLoadedMetadata` would only undo. Lab, Clip file **never
  comes**: Tab from Back goes Play, Mute, Volume, Download, Full screen, Upvote; the bottom edge is
  the row's shade, and a click there does nothing; the scrubber focused just before LOADING hands
  focus to the player.
- **ux-4, LOADING faded to a ghost when pointed at.** The stream's rules that fade the plate to
  0.16 to make room for the switch applied to a clip, which has none. Two clip-only rules after them
  (`.vntv-cliptile .vntv-screen:has(> .vntv-hud[data-awake="true"]) .vntv-plate` and the
  `:focus-visible` one) keep LOADING and CAN'T PLAY at full opacity; the stream tile's rules are
  untouched. Lab: plate opacity 1 pointed at and with focus in the row (`ux4-loading-pointed.png`,
  `ux34-loading-row-focus.png`).
- **code-1, Try again after a mid-play failure left the player saying Pause.** Firefox fires no
  `pause` after a dropped connection, and `load()` stops a playing element silently, so the player
  thought it was playing (controls asleep over a stopped picture) and the stream never got its sound
  back. Now a failure pauses a playing element (its `pause` event sets the state and gives the sound
  back at once), and Try again pauses before it reloads. Firefox 155, real dropped connection then
  503: `error`, then `pause`; CAN'T PLAY with Download; after Try again, paused, Play, controls held
  up. Chrome, the review's own repro (an `error` with no `pause`): the same.
- **code-2, a press slid off a control played or paused the clip.** The browser sends that click to
  the picture. The Hud now remembers a press's pointer only when the press began on the bare picture
  (`surfacePressRef`), so a press taken back from Full screen, Download or Pause does nothing, while
  a plain click on the picture still plays or pauses and a plain click on a button is the button's.
  A stream's tile passes no `onSurface` and is unaffected.

`build/check-player.mjs` gained five checks (28): `playerControls` (Download kept on CAN'T PLAY, and
that the tile draws by it), `heardAt`, and source guards for the scrubber out of play over LOADING,
the clip's plate not fading (with the stream's fade still there) and the surface press. The README's
clip player paragraph and `design/3.3.0.md` (§1.1, §1.2, §2, §5) say the same.

**The final shots** (`final-playing-400.png`, `final-rest-304.png`, `final-download-400.png`, in
`/tmp/claude-1000/cv330-final/` and copied to `review-fixes/`): the Clips dialog at 400 px playing
and pointed at, at 304 px playing at rest, and at 400 px after Download ("Your download has
started."), each cropped to the dialog card. The dialog is a portal on the page's body, not inside
the lab's side panel (`.lab-rhs`), which at those window widths lies mostly outside the window, so
the card is the frame, as in the preview's montages.

### The lab walk (3.3.0)

Driven by script in headless Chrome (`tools/shot.mjs`) against `dist/villanet-tv-lab.html` served on
127.0.0.1, every screenshot looked at; labelled montages and the raw shots are in
`Broadcast Box Plugin.patches/shots-3.3.0/`:

- `states-400.png`, `states-304.png`, `states-776.png`: the dialog 400, 304 and 776 px wide (447,
  351 and 823 px windows): paused, playing pointed at, playing at rest (the slim red line only),
  scrubbing (hot handle, clock following), ended with Replay at "0:45 / 0:45", loading (LOADING over
  "that jump"), error (CAN'T PLAY, the reason and Try again on the tile's line), downloads off.
- `desktop.png`: the whole dialog in a 1500 × 1000 window, paused and playing: letterboxed, the title
  and Share · Report · Delete in sight, no Download pill.
- `download.png`: at 400 and 304: Download in the player, "Preparing…" (the arrow pulsing, Report
  and Delete dimmed), "Your download has started.", the refusal in red when Victor blocks his stream
  behind the open viewer (the arrow goes), downloads off (no arrow), and somebody let in (no actions,
  no arrow).
- `beside-stream.png`: Dave's live tile in the side panel beside the clip, at 304, 400 and 776.
  **Unchanged, checked:** the same states built from the 3.2.1 checkpoint (a scratch copy, its own lab)
  and from 3.3.0, compared element by element: the stream tile's 41 elements have the same geometry
  and the same 21 computed properties at 304, 400 and 776 px, at rest and pointed at; the only
  difference is the scrolling title's position, which is time.
- `console.png`: **System Console → Clips on the server → Watch**: paused, playing pointed at,
  playing at rest, loading, error, and a 900 px window; never a Download.
- `refusal.png`: Report and then Delete refused (the lab's writes failing): 3.2.1's line grey,
  rgb(214, 214, 214); 3.3.0's red, rgb(255, 90, 106).
- **The keys**, by script with the tile focused: K plays, Space pauses, → +5 s, ← back, L +10, J back,
  Ctrl+K nothing, M mutes and unmutes, F asks for full screen on the whole tile, an arrow on the volume
  is the volume's, Space on Download is the button's, K on Download plays, the scrubber's Home, Page Up
  (+10) and End (ended, "0:45 / 0:45", Replay), K at the end replays from the clip's start, the picture's
  context menu is prevented, Tab and Escape go on to the dialog (Escape closes it).
- **The pointer**: a mouse click on the picture plays, a second pauses; a touch tap only wakes or puts
  away the controls; a press at three quarters of the track lands at 33.8 s ("0:33 / 0:45").
- **Against the preview** (`previews-3.3.0/montage/viewer-*.png`, the A column, and its raw shots):
  pixel-identical (RMSE 0) for paused at 400 and 304, downloads off at 304, the desktop dialog, and
  Download, started, refused and off. The rest differ only by the video's frame at the moment of the
  shot, the random static, the pulse's phase, how much the browser had buffered (the track's faint
  "loaded" shade), and the speaker: headless Chrome refuses play with sound without a user gesture,
  so the playing shots play muted; the preview's tool passed a gesture. No difference in code.

### The 3.3.0 real-server checklist (UNVERIFIED until then)

After 3.2.1's F1 and D1-D8 (3.2.1 was never installed):

- **P1.** A clip opens paused with the controls up, "0:00 / 0:45" style time on the line under it,
  the streamer's name only; play with sound takes the sound from the stream that had it and gives it
  back on pause, Back and close.
- **P2.** It plays to its own end and stops with Replay, and Replay starts at the clip's own first
  frame, the in-point chosen in the editor, not the keyframe before it.
- **P3.** The keys (Space or K, the arrows, J/L, M, F) with the player clicked, and Escape still closes
  the dialog.
- **P4.** Full screen takes the whole tile; Escape leaves it; the controls come and go there too.
- **P5.** With downloads on: the arrow downloads as D2 did, the words appear under the picture, and
  right-click on the picture offers no Save video as (Chrome, Firefox, and the desktop app).
- **P6.** On a phone or touch screen: a tap wakes the controls, a drag on the scrubber scrubs without
  scrolling the dialog.
- **P7.** Firefox and Safari: the volume's handle is white, the scrubber is red, and the controls come
  up when tabbed into (`:has()`).
- **P8.** The System Console's Watch plays a clip in the same player, with no Download.
- **P9.** In Firefox, with downloads on, a clip whose connection drops while it plays with sound:
  CAN'T PLAY, the stream that had the sound gets it back, and pointing at the picture brings up
  Download; after Try again the player says Play with its controls up (review fixes ux-1, code-1).

### Open for Victor (3.3.0)

- **Try again in the System Console** wears the console's own white pill, inside the black tile, like
  the console's buttons above it. Say if it should be the panel's red pill.
- **J and L** (ten seconds back and on) came along from the preview's code; say if they should go.
- **The clip editor's player** (the scissors' editor, `ClipEditor.tsx`) is unchanged; it has its own
  trimmer and controls.
- **Download on CAN'T PLAY** (review fix ux-1) comes up as the controls do, when the picture is
  pointed at or tabbed to, so the error tile at rest is the one in the previews. Say if it should be
  held up there, as a paused clip's controls are.

## 3.2.1 (built 13 October; never installed)

**Victor is running 3.2.0 on his real Mattermost server** (from 13 October: he joined Mumble, saw the
soundboard's speaker in the footer bar and asked how to add sounds). That afternoon he asked for three
things, in his words: "Server toggle to allow/deny clip downloading.", "Have the clip viewer player be
the same aesthetic as the main video stream player." and "Don't let users opt out of biggest fan."
3.2.1 builds the first and the third, to `design/3.2.1.md`, which records his answers. The second is a
visible redesign, so it is being shown to him as previews first and is **not** in 3.2.1.

3.2.1 is **built and not yet installed**. The checkpoint of the tree before it is
`/home/vvilla/Documents/Broadcast Box Plugin.pre-3.2.1` (3.2.0 as installed, without `dist/`); it is
not to be edited.

The installable bundle is `dist/ceo.villa.villanet-3.2.1-linux-amd64.tar.gz`, built by
`make dist-linux` at 12:35 on 13 October, after the review's two fixes (below): **10,941,267 bytes**,
sha256 `ba26ae8293589e9dacb84af13e3d766cac8f2f743ef39da3f1327e23c6204f42`. Its `webapp/dist/main.js`
is 722,525 bytes (179,592 gzip -9) and its server binary 28,631,200 bytes, Go 1.27.2,
`CGO_ENABLED=0`. Its `plugin.json` says 3.2.1 and `min_server_version` 11.11.0; its `main.js` has no
lab stand-in (no `__villanetLab`, no `holdClock`, no `srcdoc`) and no trace of the opt-out's routes or
words. (The first 3.2.1 bundle, built at 11:51 before the review, was 10,941,211 bytes, sha256
`d7ef8bc0dc0f6f89ef4dad0ef901aba74a2592d93910eddf6d60efe73fb5d331`; it is replaced and is not to be
installed.)

**The checks on 13 October, after the review's fixes:** `go clean -testcache`, then `TZ=UTC make
check` (go vet, the Go tests under -race in 160 s, gofmt, the version check; React, tsc, check-styles,
check-squeeze, check-away 166, check-twitch 21, check-awards 34, check-sounds 34, check-mentions 15),
`make e2e` (47 s), `make lab` and `make dist-linux` all passed. No Go module changed.

### The review's fixes (3.2.1, 13 October)

Two findings were confirmed by the review and fixed before the bundle above; nothing else changed.

- **sec-3: the streamer's download switch could not be reached while Clips were off.** `streamerView`
  returned nothing whenever the snapshot's `clips.enabled` was false (clipping switched off, Clips with
  nowhere to keep anything because the directory would not take writes at activation, or the
  document halted), so **Clips of your stream** and its download switch went, while downloads of the
  clips already made went on: the server's `canDownload` and `downloadable()` never depended on
  clipping. Fixed in the panel, at the switch's own condition, as `design/3.2.1.md` 1.3 already said
  ("while `me.clipsStreamer.downloads` is present"): a linked streamer whose "me" carries `downloads`
  keeps the scissors and the sheet while Clips are off, with the download switch alone in it
  (`streamerView`'s new `{kind: 'downloads'}`; no clipping switch, notice or Wipe, and the scissors'
  dot says off). Clipping switched off with the sheet open, focus on its switch goes to the download
  switch; with downloads off too, the sheet closes as before. The server needed no change.
  Tests: check-away "with Clips off, a streamer keeps the download switch alone while an admin allows
  downloads", and Go `TestAStreamerCanBlockDownloadsWhileClipsAreOffOrCannotBeKept` (clipping off,
  and a storage problem: the snapshot says off, `/me` still carries `downloads`, a clip is still
  downloadable, and the streamer's PUT blocks it, an address already handed out included).
- **web-1: focus fell to the page's body when Download went with focus on it.** From 3.2.1 a switch
  moved anywhere makes an open viewer ask about its clip again, and Download can go with nobody
  pressing it. The row of actions now has `useFocusHome`: focus that was on a pill taken out goes to
  the pill that took its place (`actionAfterLoss`, pure, in `clipLogic.ts`), and to Back once the row
  is empty or gone, which also covers a clip deleted elsewhere while a pill had focus. Test: check-away
  "a pill taken from the clip viewer’s row hands focus to the one that took its place, then to Back".

Lab walk for the fixes, in headless Chrome over CDP with real mouse and Tab key events against
`dist/villanet-tv-lab.html` served on 127.0.0.1 (each screenshot looked at; kept in
`Broadcast Box Plugin.patches/shots-3.2.1/` as `fix-*.png`): downloads on and clipping off, the
scissors are there ("Clips of your stream: off") and the sheet holds only **Let people download clips
of my stream**, focused as it opens, at the default width and at 304 px; pressing it says "Off.
Download is gone from every clip of your stream." and the lab's block is set; clipping back on gives
both switches; clipping off with focus on clipping's switch hands focus to the download switch;
downloads off too closes the sheet and focus goes to the footer's title. In the viewer on "Victor
reads the briefing": Share, Tab to Download, then Victor's block (and separately the admin's switch)
leaves SHARE IN TOWN SQUARE · REPORT · DELETE with focus on Report, and the next Tab reaches Delete;
the clip deleted elsewhere with focus on Report leaves "This clip has gone" with focus on ‹ ALL CLIPS;
Delete pressed twice still returns to the list as before.

### What 3.2.1 holds

| Item | What was built | Settings (default) |
|---|---|---|
| 1. Clip downloads | **Download** in the clip viewer, after Share, for linked people only, while `canDownload` (the server's per-clip answer in `GET /clips` and `GET /clips/clip`: the admin's switch, a link, the clip still kept, its streamer not having said no). The browser saves the file straight from the plugin's own address through a temporary `<a download>`, never into the page. A single-use ticket (`POST /api/v1/clips/download-ticket`, CSRF-checked, 60 s, one person and one clip, kept as its hash in `clip_ticket_<user>`, 10 per person per 10 minutes) is spent by `GET /api/v1/clips/download?t=`, which logs "A clip was downloaded" (user id, clip id) and answers 303 to a **download address** (`?d=`, HMAC-signed for that person, ten minutes, no storage) that serves the MP4 as an attachment with `nosniff`, a sandbox CSP, `no-store` and ranges. File names like `Dave - that jump - 2026-10-13 2105.mp4`, cleaned, in the reader's Mattermost time zone. The streamer's switch **Let people download clips of my stream** in **Clips of your stream**, shown only while downloads are allowed, on until switched off, kept beside clipping's switch as `clipsDoc.noDownloads` by stream tag (`PUT /api/v1/clips/streamer {"downloads":bool}`), reaching every clip of the stream, past and future. Not on shared cards or in the System Console. `controlsList="nodownload"` stays. | **Let people download clips** (**off**), in Clips |
| 3. No biggest-fan opt-out | Removed end to end: `POST /api/v1/awards/fan`, `POST /api/v1/admin/fans/restart`, the `fan_optouts` copy and the per-pass purge, `ledgerAlert.fansOff` and the airtime status's `fansOff`, `awards/mine`'s `fanNamed`/`fansReadable`, `takebacks`' `fan`, the You tab's switch, the own popover's takeback, the Airtime card's **Start biggest fans again**, the admin alert line, their lab chips, types and checks. At activation, once: a leftover `fan_optouts` is deleted (and `quarantine_fan_optouts`), after finishing any deletion 3.2.0 still owed, with an Info line naming nobody. Fans are still counted only while **Show who is watching** is on. | **Name each streamer's biggest fan** (on), unchanged; its help text now says nobody can opt out |

### What the build decided where the contract was silent or the task overruled itself

- **A second step after the ticket.** The task asked for a single-use ticket whose GET "spends the
  ticket, then streams the MP4 ... and Range support". Mattermost Desktop v6.3.0 cancels every new
  download and asks for the same address again after its save dialog (`src/main/downloadsManager.ts`
  `handleNewDownload`: `event.preventDefault()` then `webContents.downloadURL(url)`), and a browser
  resumes from the same address with a Range request, so a ticket spent by the first request would
  make every desktop-app download fail. The ticket stays single-use; the GET that spends it answers 303
  to a signed download address for that one person and clip, which may be asked again for ten
  minutes. Electron's `DownloadItem.getURL()` is Chromium's `GetURL()`, the last URL of the redirect
  chain (`components/download/internal/common/download_item_impl.cc:937`), so the desktop app asks
  for the address, never the spent ticket. Checked in Mattermost Desktop v6.3.0's and Chromium's
  source; **not yet tried in the desktop app itself** (check D4 below).
- **The two routes are classified in the clip family's table, not in `routeClasses`.** `ServeHTTP`
  hands every `/api/v1/clips…` path to `serveClips` before its switch, and
  `TestEveryRouteHasAnAccessClass` fails for any `routeClasses` path the switch does not serve. Both
  routes are in `clipRoutes`, linked because they are not in `clipWatchRoutes`, behind
  `requireClipsAccess`, and both are tried as every kind of caller by `TestClipGateClasses` (whose
  "no token spent by anybody refused" list gained `downloadLimiter`).
- **The clips' version moves with the download switches**, while downloads are on: a clip open in
  somebody's viewer asks about itself again and its Download follows the admin's switch and the
  streamer's at once. With downloads off it is 3.2.0's version byte for byte, so the 3.1.0 golden
  snapshot test still holds. The streamer's switch republishes the clips.
- **The download address is relative** (`Location: download?d=…`): `http.Redirect` would have made it
  absolute from the path Mattermost hands the plugin, which has `/plugins/<id>` stripped.
- **The panel opens no address but its own**: `isDownloadAddress` (pure, in `clipLogic.ts`) accepts
  only `/plugins/ceo.villa.villanet/api/v1/clips/download?t=` and 64 hex.
- **The set-aside copy `quarantine_fan_optouts` is deleted too**, at the same first activation, with
  its own log line: it names who opted out, and nothing reads it now.
- **`fansOn` stays in the snapshot**, meaning biggest fans and Top Snoopers are on; it still gates the
  Biggest fans section and the fan badges. The trophy no longer shows for a linked reader only because
  fans are on (that was there so the opt-out could always be reached).
- **`GET /api/v1/takebacks` stays**, answering only `{"sounds": n}`, for **Delete my sounds**.
- **The streamer's done notes** say what changed for the button ("On. Download is back on every clip of
  your stream." / "Off. Download is gone from every clip of your stream."); the notice under the switch
  says the position.
- **A clip deleted, or Clips' storage gone, after an address was handed out** is refused on the next
  request; the file's name is worked out again on each request (the reader's time zone from
  `GetUser`).
- **The design addendum** (`clips-addendum.md` A§1) carries an amendment pointing at 3.2.1 for its rule
  6, beside the one the task asked for at the top of `3.2.0.md`.

**Also in this tree, not made by this build:** while 3.2.1 was being built, another session changed
the soundboard so that an empty board reaches the panel as an empty list (`server/sounds.go`: the
board answer's `sounds` is never `omitempty`; `webapp/src/components/Soundboard.tsx`: a reader in voice
with no list gets an empty board; `server/sounds_test.go`: `TestAnEmptyBoardIsSentAsAnEmptyList`).
It is the likely answer to Victor's "how do I add sounds?": with no sound on the server, 3.2.0 showed
an empty sheet with no way to add the first one. It is not in `design/3.2.1.md`, and it passed every
check above; it is in this bundle.

### The lab walk (3.2.1)

Driven by script in headless Chrome against `dist/villanet-tv-lab.html`, each screenshot looked at;
kept in `Broadcast Box Plugin.patches/shots-3.2.1/`:

- `viewer-off.png`: downloads off (as shipped): the viewer has Share, Report and Delete, no Download.
- `viewer-on.png`: **downloads on**: SHARE IN TOWN SQUARE · DOWNLOAD · REPORT · DELETE.
- `viewer-pressed.png`: after pressing Download: "Your download has started." (the lab saves nothing
  and counts the press).
- `viewer-refused.png`: Victor's own stream's clip, his block set behind the open viewer: "The streamer
  has turned off downloads of their clips." With the block set the usual way, the open viewer asks
  again and the Download pill goes by itself.
- `viewer-letin.png`: downloads on, Victor let in to watch: no actions at all, no Download.
- `sheet-admin-off.png`: **Clips of your stream** with downloads off: exactly 3.2.0's sheet.
- `sheet-on.png` and `sheet-off-304.png`: the second switch, on at the default width, switched off at
  304 px with its notice and "Off. Download is gone from every clip of your stream."
- `you-tab.png`: the You tab with biggest fans on: By your name and Your achievements, no Biggest fan
  switch; "Biggest fan of Erin" is still a chip in the picker.
- `own-popover.png`: Victor's own profile popover: **Delete my sounds** only.
- `console-clips.png`: the System Console's Clips section with **Let people download clips** (False)
  and its help.

### The 3.2.1 real-server checklist (UNVERIFIED until then)

- **F1.** After installing 3.2.1, the server log has, once, "Deleted the list of people who had chosen
  not to be named as a biggest fan…" with `people=N` (only if anybody had opted out under 3.2.0), and
  no such line on the next restart. The You tab and your own popover show no opt-out.
- **D1.** With **Let people download clips** off: no Download anywhere; nothing else in Clips changed.
- **D2.** On, as a linked person in Chrome or Firefox: Download in the viewer saves the clip with a
  clean name in your own time zone, the server log has "A clip was downloaded" with your user id and
  the clip id, and the reverse proxy's access log shows the two addresses (the ticket and `?d=`).
- **D3.** A five-minute clip downloads without the tab's memory growing by the file's size (the
  browser's own download, not the page).
- **D4.** **In the Mattermost desktop app**: Download opens its save dialog (or saves to its download
  folder) and the file arrives whole, even after waiting a minute in the save dialog. This is the
  case the download address is for; it has been checked only in the source.
- **D5.** A download interrupted part way resumes in the browser's download list (a Range request to
  the same address).
- **D6.** As somebody let in: no Download; as a streamer, **Let people download clips of my stream**
  off: no Download on any clip of your stream for anybody, and it stays off after an unlink and relink.
- **D7.** Opening the ticket address a second time, or after a minute, shows "This download link has
  run out or was already used. Press Download in VillaNET again." and downloads nothing.
- **D8.** With **Let people download clips** on and **Let people clip streams** off: a linked
  streamer still has the scissors, and **Clips of your stream** holds only **Let people download
  clips of my stream**; switching it off takes Download off every clip of their stream.

### Open for Victor (3.2.1)

- **The streamer's download switch lives in Clips of your stream**, and since the review's fix sec-3
  that sheet stays while clipping is off (or Clips cannot keep anything) as long as downloads are
  allowed, with the download switch alone in it. Say if it should have a home of its own instead.
  One case is left as it was: while the clips document is halted (it would not decode, and an admin
  is alerted to restart), the switch shows but cannot be changed ("Downloads of your stream’s clips
  could not be changed just now."), and clips stay downloadable from this node's last good copy, which
  honours every block set before the halt. Say if downloads should stop while halted.
- **A downloaded clip cannot be taken back**: deleting the clip, its expiry or a streamer's later "no"
  stop further downloads, never a copy already saved. LOCKDOWN says so.
- **The file's time** is when the clip starts in the reader's own Mattermost time zone (UTC when theirs
  is not set); the name says no zone.
- Item 2, the clip viewer's restyle to match the stream player, waits on his pick of the previews.
  (He picked look A; built as 3.3.0, above.)

## 3.2.0 (built 13 October; on Victor's real server)

3.2.0 is **installed on the real server** since 13 October. The text below is as it stood when it was
built, and every word about the biggest-fan opt-out is superseded by 3.2.1 above.

3.2.0 is **built and unreleased**, and so are 3.1.0 and 3.0.0 under it: none of the three has been
installed on the real Mattermost server. 3.2.0 is Victor's request of 12 October, eight items, built to
`design/3.2.0.md` (revision 2) with his answers and preview picks recorded at its top, in four phases: the
preview (P), the shared fixtures (B0), a server builder and a webapp builder in parallel (B1), and one
integrator (B2). The checkpoint of the tree before any of it is
`/home/vvilla/Documents/Broadcast Box Plugin.pre-3.2.0` (3.1.0 as it stood).

The installable bundle is `dist/ceo.villa.villanet-3.2.0-linux-amd64.tar.gz`, built by
`make dist-linux` at 05:51 on 13 October, after the review fixes ("Review fixes (3.2.0)" below) and a
last wording fix ("Deleted your sound." for one sound): **10,938,948 bytes**, sha256
`6f2e35356c108a6dea717df7cd880da85ca88492fa90994e5b2712ba9482ff7b`. (Replaced at 05:51: the 05:41
bundle, 10,938,945 bytes, sha256 `32e79a28…`.) Its
`webapp/dist/main.js` is 722,849 bytes (179,722 gzip -9; 3.1.0's was 562,725) and its server binary
28,623,008 bytes, built with Go 1.27.2 and `CGO_ENABLED=0`. (Replaced: the integration's bundle of
04:03, 10,932,305 bytes, sha256 `60159bdd…`.) Its `plugin.json` says version 3.2.0 and
`min_server_version` 11.11.0. Its `main.js` has no lab stand-in (no `__villanetLab`, no `holdClock`,
no fixture, no `srcdoc`), and names no host that 3.1.0's did not (the IMDb, TMDB, Steam, IGDB and
Twitch links, and Twitch's player prefix). Like 3.1.0 it needs **Mattermost 11.11.0 or later** and,
for Clips, **Local file system** storage.

**The checks on 13 October**, last run on the tree with the review fixes: `go clean -testcache`, then
`TZ=UTC make check` (go vet, the Go tests under -race in 94 s, gofmt, the version check; React, tsc,
check-styles, check-squeeze, check-away 159, check-twitch 21, check-awards 34, check-sounds 34,
check-mentions 15), `make e2e`, `make lab` and `make dist-linux` all passed, as did
`CGO_ENABLED=0 go build ./server` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and
windows/amd64, and `go mod tidy -diff` and `go mod verify` were clean. The same checks had passed on
the joined tree before the fixes, where govulncheck v1.8.0 found **0 vulnerabilities in called code**
("Security updates (3.2.0)" below); the fixes changed no module. The words tests run unskipped:
`TestEveryNoOutsideContactPromiseMentionsTwitch` and `TestNoDocSaysThePluginNeverReceivesAudio`
(README, LOCKDOWN and now `plugin.json`). **Nothing in 3.2.0 has met a real Mumble server**, and the
soundboard's audio path has been tested only against a Mumble server made inside the Go tests (the
checklist below starts with it).

### What 3.2.0 holds

Victor's eight items, with his answers (Q1, Q2, Q4, Q5, Q7, Q8, Q10, Q11) and his preview picks:

| Item | What was built | Settings (default) |
|---|---|---|
| 1. Awards by your name | Up to four of everything held, in order, or **Automatic** (exactly 3.1.0's badges); chosen in the Trophies card's **You** tab (pick P2 A) with a live post-header preview; lost awards skipped and kept; NEW marks per browser; past months' medals choosable, drawn with a **star** where the place was (P4 C). One document, `award_prefs`. Back to Automatic is open to everybody. | none (always on) |
| 2. Biggest fan | Per linked streamer, the linked viewer who has watched them most in the panel, all time, from an hour. **Counted only while Show who is watching is on** (Q1, "Only with faces on"); with it off nothing is counted and nothing moves. Opt-out from the You tab or one's own profile popover, linked or not, which deletes what was counted. Shown in both people's popovers, a Biggest fans section on the Snoopers tab, a gold heart badge the fan may choose, and **the quiet line on the Live now card** (P3 B: "Biggest fan · Bob", "Fan · Bob" on a two-up card). Ledger `fans_totals`, opt-outs `fan_optouts`. | **Name each streamer's biggest fan** (on) |
| 3. Achievements | Five families worked out live at every refresh, so backdated (Q8): Streamed (ON AIR), Snooped (SNOOPER), Podium, Thumbs up (CLIPPER) and admin-defined 24/7 channels, shipped with `villanet-watts = Alan Watts = 1, 10, 50, 100, 250`, no daily cap (Q11). The Achievements tab **by family** (P4 A); coins bronze, silver, gold, then **ink with a red rim** (P4 B), ink with a gold rim, then gold studs; a quiet toast for a new step in the earner's own panel only. Channel hours in `chanwatch_totals`, from zero. | **Give achievements** (on), **24/7 channel achievements** |
| 4. Share a plan | `POST /api/v1/schedule/share`: a `custom_villanet_plan` post as the sharer with fixed text and a link, props only the id and start time. The **banner** card (P5 A): art at 2:1 with LIVE on it, words under it, no banner without art; **Join** before the start and **Watch** once live (Q7), cancelled and ended faces with no title. From an Up next share tool and the plan sheet's offer after saving. The clip card's forged-post guards generalised (`share_cards.go`). | none |
| 5-6. Soundboard | A **sixth footer tool**, a speaker, shown only while the reader is linked and in Mumble (P1 A, Victor's final choice); below a 360 px panel the stream title keeps only its pencil. **A tiles** (emoji over a two-line name). One shared board (Q4); a sound plays into the presser's own Mumble channel by shout, the bot parked (Q5); a Mumble name match, logged, is enough (Q2). Greyed and refused while muted, deafened, suppressed, in two sessions or (with the setting) unregistered; a 10 s wait after your own sound, one sound at a time, 8 a minute. Adding: a file, the microphone or a VillaNET clip (Q10), trimmed, levelled and encoded to Opus in the browser (WebCodecs, 40 kbit/s CBR, 20 ms); the server checks and decodes every packet with `github.com/pion/opus` v0.1.0 (pure Go) and plays them with `Conn.WriteAudio`, no cgo. System Console card **Sounds on the server** (Listen, Delete, Block, Check again, Start the board again). | **Let people play sounds into Mumble** (**off**), **Wait between sounds** (10), **Only registered Mumble users** (off), **Sounds on the server** |
| 7. @names in titles | Mattermost's mention rule, drawn as **chips** (P7 B) in the ticker and on the Live now card's links line, opening Mattermost's own profile card; nobody notified; not on Twitch cards. | none |
| 8. Thumb on shared clip posts | A **pill after Watch** (P6 A), a vote button for linked readers who may vote, a dashed count for let-in readers and the clipper; cards on screen refresh at most every 30 s. | none |

Two roster changes apply whatever the switches (contract A1): the headphones' `muted` includes
Mumble's Suppressed, and a Mumble name matches a username only if it is plain letters, digits, dots,
dashes and underscores (lowered ASCII-only), so a lookalike such as `vİctor` matches nobody.

With every new switch off and nobody having chosen, the snapshot is 3.1.0's byte for byte
(`TestWithEveryNewThingOffTheSnapshotIs310s`, against `server/testdata/snapshot-310.golden.json`).

### What the builders decided where the contract was silent or overruled

From their reports (each is also in the code's comments):

- **Victor's Q1 overrides §4.3**: there is no hourly freeze for fans with faces off, because nothing is
  counted then (`TestFansStandStillWithFacesOff`). A pending opt-out purge runs even while fans are
  switched off, because opting out promises deletion.
- **Victor's pick A overrides §2.7 and §7.13**: the soundboard is a footer tool, and its help text says
  so. The In voice row is unchanged.
- **Byte order**: every u16 in the sound formats is big-endian (uploads, the `VNS2` blob, the audio
  routes). The audio GETs need `X-Requested-With: XMLHttpRequest`.
- **New refusal codes**: `moved` (409, the presser changed Mumble channel before packet 0), `fans_off`
  (503, an opt-out while `fan_optouts` cannot be read), `store` (503) and the plan share's `gone`,
  `cannot_post`, `forbidden`, `rate_limited`, `bad_request`. Deafened is checked before muted, so a
  deafened person is told to undeafen.
- **Extra fields**: `GET /sounds` may say `blocked: true` and `why: "bot_cannot"`; award saves answer
  `{ok, show}`; achievement holders carry `displayName` and `username` (an approved departure). The
  built-in family names are Streamed, Snooped, Podium and Thumbs up.
- **The cards route** works out `canVote` without asking Mattermost whether the reader is an admin
  (being an admin has no bearing on voting).
- **Two `plugin.json` sentences that stopped being true** were rewritten: the Mumble subtitle ("never
  joins a channel, and hears nobody") and **Match names to usernames** ("lets nobody act as anybody
  else in Mattermost").
- **The lab**: Dave is Bob's biggest fan, his all-time Snoopers figure is 52 hours, and Victor holds the
  6-month Podium step (the lab's seven past months), so the contract's lab examples fit the existing
  lab history. `holdClock` was added to `webapp/src/clock.ts` beside the lab-only `advanceClock`; the
  plugin never calls it and the bundle does not contain it. The own-popover takebacks are bordered
  buttons, as the P2 montages drew them.

### What the integration (B2) changed

- **The two halves agree.** A temporary Go scene test dumped the joined server's answers (the snapshot
  with every 3.2.0 field; `/me` with `soundAlert`, `achievementsAlert` and `ledgerAlert`; awards mine,
  display POST and DELETE, takebacks, fan opt-out and its 503 `fans_off`; achievements mine; the sounds
  board live and blocked; a real add, play, a cooldown refusal with `retryAfterMs`, an edit and
  remove-mine against a Mumble server made in the test; the admin sounds list and check; the airtime
  status with `fansOff`; `POST /schedule`'s `{ok, id}`; clip cards; a plan share) and each was
  type-checked with tsc against `webapp/src/types.ts` (excess properties are errors there), and the
  check was shown to catch an injected fault. All agreed. Every path `client.ts` calls has a class in
  `routeClasses` or sits under `/api/v1/admin`; the WebSocket event is
  `custom_ceo.villa.villanet_sound` with `{busyUntil}` on both sides; settings keys, the custom setting
  `soundsadmin`, the post type, its props and the plan link route match; the blob header offsets
  (count at 12, data at 47) match. An earlier integration run, cut off by a usage limit, had already
  brought `types.ts`, `client.ts`, `soundLogic.ts`, `check-sounds.mjs` and the lab's world and mock
  client into line with the server; nothing further needed changing. The temporary test is deleted.
- **Fixed: two Mumble sessions did not grey an open soundboard.** The sheet asked for the board again
  only when the reader's first roster row changed, so a second session (a phone beside the desktop,
  through Names that differ) left the tiles live until a press was refused. The sheet now also asks
  again when the number of the reader's rows changes (`Sidebar.tsx`, `rosterKey`); checked in the lab
  both ways.
- **README.md**: the "Upgrading to 3.2.0" and "New in 3.2.0" notes and the sections for every feature
  (Awards by your name, Biggest fans, Achievements, Sharing a plan, Soundboard with Adding a sound, Your
  own sounds, Setting the soundboard up, What a Mumble name proves and How a sound gets into Mumble,
  @names in stream titles, the thumb under Sharing a clip), the privacy note (fans counted only while
  faces are shown; `fans_totals` and `chanwatch_totals` under Top Snoopers' Privacy), the soundboard's
  Mumble requirements, and every word-for-word change of §10.2; plus the toolchain, the check scripts,
  the bundle size and the lab's 3.2.0 rows under Build from source, and three corrections found in the
  lab walk (Join stays beside Watch on a live plan card; Check again sits at the foot of Sounds on the
  server; the Achievements table names each family's head).
- **LOCKDOWN.md**: written for 3.2.0; the Mumble connection's paragraph (audio out with the soundboard
  on, every packet checked, each play logged with the account and the sound's id); the three bullets
  of §10.3 under "What this does not do"; and the sign-out paragraph's 3.2.0 caches.
- **Comments**: `voicePerson.UserID` in `server/voice.go` no longer says the match is never used for
  anything (the panel offers the soundboard by it; the server matches presses again from Mumble's live
  state); the lab's webpack config says six modules are swapped. The other §10.4 comments were already
  done by the builders and were checked.
- `plugin.json`: the soundboard's help says "tiles, each an emoji over a short name" instead of
  "square tiles" (an A tile is about 74 px tall). Version **3.2.0** in `plugin.json` and
  `manifestVersion`.
- The words test unskipped, and extended to `plugin.json`.

### Review fixes (3.2.0)

A review of the joined tree (security, server and webapp lenses) confirmed 21 findings, each
reproduced or read in the code before it was fixed. All 21 are fixed, at the cause, each with a Go
test or a check in `build/check-*.mjs` that fails without the fix (shown by putting the old code back
in a scratch copy), and the panel ones were driven in the lab with real key events.

Server:

- **sec-2: GET /api/v1/admin/sounds** answers only the System Console itself (`madeByScript`, "Ask
  from the panel."), checked before anything is read: it reads every sound's audio to check it, and a
  Markdown image an admin's browser rendered made it do so on every render. Contract §2.2 named only
  the two audio GETs; the list reads blobs too, so its rule applies
  (`TestTheAdminSoundListAnswersOnlyTheConsoleAndReadsNothingOtherwise`). The README's security note
  says so.
- **sec-3: an opt-out that landed during the pass that first credited the viewer** could leave their
  row in `fans_totals`. The watch pass now reads the opt-outs inside `statsWriteLock`, and an opt-out
  lands under the same lock (one small write; the rewrite of the table only when there is something to
  purge), so a pass is wholly before an opt-out (its row is purged) or wholly after it (it sees them
  out) (`TestAnOptOutDuringThePassThatFirstCreditsAViewerLeavesNoRow`).
- **srv-01: a purge still owed was marked done without deleting anything** once the viewer had opted
  back in. Every pass now takes out the rows of everybody on the purge list as well as everybody opted
  out, and does not count a purged viewer in the same pass; the display leaves them out meanwhile
  (`TestAPurgeStillOwedIsDoneEvenAfterOptingBackIn`).
- **srv-02: opt-outs never read were treated as readable and empty.** Until `fan_optouts` has been
  read once, biggest fans count and show nothing (fail closed, §4.4); the admins' "Start biggest fans
  again" alert stays for a list that was read and would not decode
  (`TestFansCountNothingUntilTheOptOutsHaveBeenRead`).
- **srv-03: a chosen 24/7 channel family that left the catalogue made every later save fail.** A save
  now takes out of the list any `ach:` family that was in the person's stored choice and is no longer
  in the catalogue, and answers what it stored; the You tab adopts that answer, so the dimmed chip
  goes. A family not already in their choice is still refused `bad_choice` (§3.4). This is the
  reviewers' "strip it when storing"; keeping such an id instead would have let something outside the
  catalogue be stored, against §3.4
  (`TestAChosenFamilyThatLeavesTheCatalogueComesOutWithoutStoppingAnyChange`).
- **srv-05: a periodic re-read could put back bytes older than this node's own write** (the board, the
  award choices, the opt-outs). Each document now has one conversation with the store at a time on a
  node (`soundStore.io`, `awardDocs.io`): a read and its install, or a write and its install, never
  interleaved; a waiting re-read asks again whether it is still due. Lock order is `statsWriteLock`,
  then `io`, then the copy's own lock (`TestARereadOfTheBoardNeverPutsBackWhatABlockReplaced`,
  `TestARereadNeverPutsBackTheOptOutsFromBeforeAnOptOut`).

Panel:

- **sec-1, web-3: the microphone.** Recording stops at 6 s by a timer, not only by the waveform's
  frames (which a hidden tab stops), and stops the moment the page is hidden; a throw once the
  microphone is open closes it. A recording can be cancelled, which closes the microphone at once and
  hands nothing over; Back, and leaving the sheet, cancel it, and a recording whose permission prompt
  was answered after the flow was left is cancelled as it starts. Checked in headless Chrome with a
  fake microphone against the real `startRecording`: hidden 1.4 s in, the track ended at once and 1.44
  s came back; with frames switched off, the timer stopped it at 6,000 ms; cancel ended the track with
  no result. In the lab, Back during and just after a recording stays on the first step.
- **web-4:** an encoder that errs (WebCodecs rejects the flush) is said as "This browser can't make
  sounds…", not as the store failing, and the encoder and its data are closed (checked in Chrome).
- **web-5:** the trim keeps its quarter of a second against the sound's own ends (the other handle
  gives way, inside a long clip's 20 s window too), and a whole sound shorter than 0.25 s, which the
  design keeps whole, ends in silence up to 0.25 s, so it encodes to the 13 packets the server takes
  (Chrome: a 150 ms click made 8 packets before, 13 now). Refusing short sounds instead would have
  needed words the contract does not have and undone "a sound shorter than 250 ms is all of it".
- **web-6:** the empty board's invitation says "This browser can't make sounds…" in place of its three
  ways in where WebCodecs cannot encode at a constant bitrate, and an add flow opened straight onto
  Record or From a clip goes back to the first step to say so (§7.13 step 1).
- **web-1: focus in the soundboard.** Each step of adding a sound takes the focus to its first control
  (the clips list once it has arrived; the emoji button on coming back from the chooser); back on the
  board the focus goes to the sound added or changed, to ADD after Back, to the tile in a deleted
  sound's place, or to the first tile, ADD or the invitation after Delete all; while the board is
  asked again it waits on the sheet itself. Escape works throughout. Focus is only brought back when
  the control that had it was taken away, never taken from elsewhere in the page.
- **web-12:** one live region, first in the sheet and kept across board and add mode, says every
  status line, "Ready again in 10 seconds." as a wait starts and "Playing … for you only.", each once;
  "Added “X”." is now announced.
- **web-13:** Listen here (and the console's Listen) can no longer play after the sheet or page has
  gone, and a second Listen stops the first, even while either is still being fetched or decoded.
- **web-7:** the soundboard sheet keeps its scrollbar's room whether it scrolls or not, taken from its
  right padding (measured, so none where scrollbars float), so at 304 px the grid stays four 56.5 px
  tiles a row when a reason box makes the sheet scroll (lab: 1400×700, muted, scrolling, tiles
  unmoved).
- **web-2:** a choice still waiting for its 800 ms when the Trophies card closes or the tab changes is
  sent at once (not after a sign-out); **web-15:** Automatic / My choice is a real radio group, one
  tab stop and the arrow keys choosing the other.
- **web-8:** two new achievement steps in one refresh are two toasts, one after the other (`stepToSay`
  returns every step).
- **web-10:** "Your achievements" shows the steps the snapshot says are held, with no bar or figure,
  until the reader's figures arrive, and "Couldn't load how far along you are just now." with Try
  again if they cannot; never "0 of 10 hours" for somebody at 250.
- **web-11:** a refused thumb on a shared clip card says the clip vote's own words (`rate_limited`
  "Slow down a little.", `own_clip`, gone, the lock's), "Couldn't count that. Try again." only for a
  failure; a clip that has gone turns the card to its gone face.
- **web-14:** after saving a plan from the keyboard, the focus goes to Share in the offer (Escape
  closes it and goes back to the plan tool), and "Planned." is said by a live region that was there
  first.

Looked at in the lab (headless Chrome, every shot read): the soundboard at 304 px idle and muted at
1400×700 and 1400×760, at 400 and 776 px, the add flow's first, trim and name steps at 304 px, the
invitation with an encoder that cannot, two toasts in a row, the You tab's achievements loading and
loaded, and the plan offer with Share focused.

### Security updates (3.2.0)

The server builder's govulncheck found 7 vulnerabilities in called code: GO-2026-6617, -6613, -6612,
-6611, -6608, -6607 and -6603, in Go 1.27.1's standard library (fixed in 1.27.2) and
`golang.org/x/net` v0.58.0 (fixed in v0.60.0). None was in `pion/opus` or in 3.2.0's own code.

| Change | How |
|---|---|
| `golang.org/x/net` v0.58.0 → **v0.60.0** | `go get golang.org/x/net@v0.60.0`, then `go mod tidy`. It brought `golang.org/x/crypto` v0.56.0 → v0.57.0, `golang.org/x/sys` v0.47.0 → v0.48.0, `golang.org/x/text` v0.41.0 → v0.42.0 and `golang.org/x/mod` v0.40.0 → v0.41.0. |
| Toolchain **go1.27.2** | `go get toolchain@go1.27.2`, which added `toolchain go1.27.2` to `go.mod` (the `go 1.27.1` line is unchanged). With the default `GOTOOLCHAIN=auto` the installed Go 1.27.1 fetched 1.27.2 through the module proxy into the module cache and now builds with it; `~/.local/opt/go` is untouched. |

After both, govulncheck v1.8.0 (Go 1.27.2, vuln DB of 8 October) reports **0 vulnerabilities in
called code and 0 in imported packages**. The one module-level item left is GO-2026-5932, the
unmaintained `golang.org/x/crypto/openpgp`, which has no fix and which nothing imports (as in 3.1.0).
Every check above ran on the updated modules and toolchain.

### Tested only in the lab and the tests (3.2.0)

- **The soundboard's audio has never reached a real Mumble server.** The player tests run gumble
  against a fake TLS Mumble server inside the Go test and check, on the wire: the header byte, the
  sequence +2 per packet and the final bit, 20 ± 5 ms average spacing, one voice target per channel
  change, zero packets for a presser muted or moved before packet 0, the next packet final after a
  mute, one sound from 30 simultaneous presses, and the player stopping with the connection. Murmur's
  real ACLs (Speak, Whisper), bandwidth meter, CELT fallback, SuperUser and registration behaviour,
  and whether it answers the permission query, are unverified (V1-V9 below).
- **Browser-made sounds** are checked against fixtures Chrome and Firefox made in B0
  (`server/testdata/sound-*-40k.json`, `webapp/lab/fixtures/sound-packets.json`); in the lab the
  microphone, decoder and encoder are a stand-in fed from those packets, so the real WebCodecs path in
  the desktop app, Chrome, Edge, Firefox and Safari is untested in this build, as are the desktop
  app's microphone prompt and a clip decoded from its own MP4.
- **Emoji**: the lab carries 74 emoji PNGs and a stand-in for Mattermost's emoji search; custom emoji
  and `/api/v4/emoji/` are untested against a real server.
- **@names**: Mattermost's `AtMention` and profile popover are lab stand-ins (one resolves after
  500 ms on request); Teammate Name Display and the popover over the panel are untested on 11.11.
- **Shared plan and clip cards** were drawn in the lab's channel in Denim and Onyx, compact display
  and a 304 px thread; the hooks and `CreatePost` as the sharer are unit-tested only, as in 3.0.0.
- **The B2 lab walk** (13 October), on the joined tree, in headless Chrome, every shot looked at: the
  footer bar at 304, 360 and 400 px (pencil only at 304); every soundboard state of §7.12-§7.14 at
  400 px, and the sheet at 304 and 776 px; every step of adding a sound at 400 px and the main ones at
  304 px; the Trophies You, Achievements and Snoopers (Biggest fans) tabs, and the tab row at 375 and
  420 px windows; the toast; Live now cards with fan lines and @name chips at 304, 400 and 776 px; the
  ticker's chips at 304 and 400; every plan card state and the clip card thumb in Denim and Onyx;
  profile popovers with the takebacks and an opted-out reader; post headers with choices; Sounds on the
  server and the Airtime card's ledger alerts in the console; the panel's admin alerts; the lock screen
  at 304 and 400 (its code and CSS are unchanged since 3.1.0); and the let-in footer (no speaker, no
  share). The lab made no request but the avatar pictures it has always asked its own origin for.
  Montages are kept in `/home/vvilla/Documents/Broadcast Box Plugin.patches/shots-3.2.0/integration/`,
  with the webapp builder's key montages and the badge contact sheet in `…/shots-3.2.0/webapp-build/`.
- The webapp builder's walk (304, 360, 400, 464 and 776 px, 375 and 420 px windows, Denim and Onyx)
  was compared with the P1-P7 montages Victor picked from.

### The 3.2.0 real-server checklist (UNVERIFIED until then; contract §13.2)

Only after Victor says go, and with him present. **The Mumble checks first: they decide whether the
soundboard works at all.** Switch **Let people play sounds into Mumble** on only for them.

- [ ] V1. Which Murmur version runs, its `bandwidth` and `opusthreshold`, and whether people are
  registered. The **Sounds on the server** card's status line, with the soundboard on.
- [ ] V2. Where VillaNET's Mumble account is parked, and whether people talk in that channel (the
  README's corrected sentence: it hears nobody, and drops what Mumble sends it).
- [ ] V3. Add a sound in the desktop app and in Chrome; play it into the voice channel: heard by
  everybody there, the presser included, at a level like a voice; not heard in another channel.
- [ ] V4. Mute, deafen, and (with an ACL) remove Speak from the presser: the tiles grey within about
  2 s, and a press sent anyway is refused. Mute in the middle of a sound: it stops.
- [ ] V5. Remove Whisper from the bot in the voice channel: the press is refused and the admin line
  names the channel.
- [ ] V6. Two people pressing at once, and one person pressing in two tabs at once: one sound plays,
  the others are told another sound is playing or to wait; the cooldown ring matches the server.
- [ ] V7. A Mumble client named like somebody else: first an exact name while that person is away
  (what the panel shows), then a lookalike (`vİctor`) beside them (no headphones, no soundboard);
  then tick **Only registered Mumble users**, and check whether a stranger can self-register a free
  name on Victor's server.
- [ ] V8. A full 6 s sound arrives whole (no cut-off from the bandwidth meter).
- [ ] V9. Listen here, and the admin card's Listen, in the desktop app and Chrome.

Then the rest:

- [ ] V10. Share a plan into a channel, a DM and a group message; read it on the web (light and
  dark), in the desktop app, in a permalink preview, in search results, and on the phone (the fixed
  text and the link).
- [ ] V11. Join and Watch from the card; a locked reader's Watch opens the lock with the stream
  waiting.
- [ ] V12. Press an @name in the ticker, on a Live now card, in the popped-out panel: the popover
  opens above the panel, Escape closes it, focus comes back; the name as the server's Teammate Name
  Display shows it.
- [ ] V13. The thumb on a shared clip in a busy channel; a vote in another browser arrives within
  30 s.
- [ ] V14. The award picker, biggest fans and achievements with real hours; the Alan Watts channel's
  hours starting from zero; the own-popover takebacks beside Mattermost's own self row.

3.1.0's and 3.0.0's checklists ("The real-host checklist" below) still apply and come before these,
except the soundboard's, which needs only Mumble.

### Known gaps from 3.2.0 (for Victor to judge; none blocks a release)

- `GET /api/v1/takebacks` (each person's own profile popover) reads the `sounds` document at most once
  every 30 s for the whole server while the soundboard is off and the board has not been read, which
  contract A1's list of reads does not mention. It is per request, not per poll.
- The player tests are real time (about 10 s a run) and timing-sensitive (the 20 ± 5 ms spacing, and
  one extra packet allowed after a mute for gumble's read goroutine); they passed in every `-race` and
  `-count=3` run on this machine.
- The lab's Avatar asks the page's own origin for `/api/v4/users/<id>/image` (since 3.1.0), which
  fails quietly; a stand-in would make the lab strictly request-free.
- With the soundboard's speaker in the bar, a panel narrower than 360 px shows the stream title as its
  pencil alone (Victor's pick A); with the soundboard off, the bar is 3.1.0's.

## 3.1.0 (built 6 October, never released)

3.1.0 was **built and never released**; 3.2.0 above carries all of it: everything in 3.0.0 below, which was never installed either, plus
**Twitch live** (`design/twitch-live.md` revision 2 with its amendments; "What 3.1.0 added" below).
Victor asked on 5 October for it to be built now, without waiting to try 3.0.0 on the real server.
On 6 October a security and optimization pass went into it, still as 3.1.0 ("Security and
optimization pass (3.1.0)" below).

The installable bundle is `dist/ceo.villa.villanet-3.1.0-linux-amd64.tar.gz`, built by
`make dist-linux` at 19:10 on 6 October, after the security and optimization pass and all of Victor's
decisions of that day (the Glitch in the footer bar, and the simpler lock screen with its review
fixes: "Victor's decisions, 6 October" below): **10,615,876 bytes**, sha256
`af0ac45c990e61c2f5837bfe0b0fa205a8286cf8ac95fc49228de9c1cca864f1`; its `main.js` is 562,725 bytes.
Before it: `TZ=UTC make check`, `make e2e` and `make lab` passed.
(Replaced bundles: 15:48, with the Glitch but before the simpler lock, 10,613,947 bytes, sha256
`ee196766…`; 04:15, after the security and optimization pass, without the Glitch, 10,613,845
bytes, sha256 `b6b3ebd6…`; 02:18, after the Twitch review fixes, 10,645,249 bytes, sha256
`fd81bae8…`; 01:22, the integration's, 10,644,641 bytes, sha256 `af697adb…`.) Its `plugin.json`
says version 3.1.0 and `min_server_version` 11.11.0, and has no `AccessContact`. Its
`webapp/dist/main.js` has no `srcdoc`, no lab stand-in, the Glitch's path once, the Twitch player
prefix once, and none of the lock's removed lines. Like 3.0.0, it needs **Mattermost 11.11.0 or
later** and, for Clips, **Local file system** storage.

**The checks on 6 October** (integration, after both halves were joined and the README, LOCKDOWN and
version were done): `TZ=UTC make check`, `make e2e`, `make lab` and `make dist-linux` all passed. The
words test (`TestEveryNoOutsideContactPromiseMentionsTwitch`) runs unskipped. After the review fixes
("What the review fixes changed" below): `go clean -testcache`, then `TZ=UTC make check`, `make e2e`,
`make lab` and `make dist-linux` all passed again. **Nothing in 3.1.0 or
3.0.0 has been installed on the real Mattermost server, and nothing has contacted the real Twitch:**
the server tests use a fake Twitch, and the lab a stand-in player and a simulated Connect window
("Tested only in the lab and the tests" below).

## Victor's decisions, 6 October

**The lock screen, 6 October (evening).** In his words, in order: "Simplify the locked out screen a
lot -- its too busy. Give me a preview before you build." Then, having seen three variants built in
sandbox copies: "Also remove the 'Just Watching?' text. Don't advertise that". Then: "Remove admin lines
too". The record is the amendment of 6 October at the top of `design/lock-screen.md`.

- **Variant C, "Behind glass"**: the panel's emblem, large and dimmed, with a padlock badge; the black
  token bar docked at the foot where the panel's footer bar sits, with a padlock, the box, an eye
  (Show token / Hide token) and an arrow (Unlock) inside it; one status line over the bar, its room kept
  so nothing moves when it speaks; a reason line under the emblem only when there is a reason;
  Disconnect Twitch on its own line when connected; No signal with Try again; and the not-set-up
  sentence. Nothing else.
- **Nothing on the lock says anybody can be let in, or whom to ask**, and **nothing on it is for admins
  only**: no "Let yourself in", no admin alert of any kind (not even one line), no set-up words. A
  system admin's lock is everybody's, pixel for pixel in the lab. Admins have it all in the System
  Console, and the full alerts in the panel once unlocked.
- **Who to ask (`AccessContact`) is removed** end to end, server, webapp and lab. A configuration or a
  settings backup from before keeps working: the key is ignored (the restore names it as a setting this
  version does not have). Go tests hold both.
- **Fixed with it**: after **Disconnect Twitch** pressed by keyboard, on the lock and in the let-in
  footer, focus fell to the page's body and the next letter went to Mattermost's message box (in the
  build before C too). Focus now goes to the words after the press, then to the lock's or the panel's
  root. `build/check-twitch.mjs` and `build/check-away.mjs` gained checks.
- **The review of it found four more, all fixed:**
  - **Try again** had the same fault as Disconnect Twitch, and always had: pressed by keyboard on the
    No signal face, the answer that changed the face took the button away with focus on it, and the
    next letter went to the message box. The panel's quiet "could not check your access" notice,
    which has the same button, did the same. `TryAgain.tsx` now hands its focus to the lock's or the
    panel's root as it goes, only when it still has it.
  - **A Ctrl+V from a Russian, Greek, Hebrew or Arabic keyboard** got past the new stop on the words
    after Disconnect Twitch in the let-in footer, and past the same older stops on the panel itself,
    a tile and the Twitch sheet, which went by the letter V alone; Mattermost, which also goes by the
    key's code, took it to the message box. All four now use the lock bar's rule, `isPasteKey`.
  - The README still said the lock tells somebody with no token "whom to ask"; and the comment on
    `/me`'s `letInAlert` (`server/api.go`) still gave the reason for showing alerts on the lock.
    Both now say what is true.
  - `build/check-away.mjs` holds Try again to its rule, and fails on a paste told by the letter
    anywhere but `focusTrap.ts`. Checked in the lab with real key presses, against the build before
    the fixes (where each of them reached the message box).
- After it and the review fixes: `TZ=UTC make check`, `make e2e`, `make lab` and `make dist-linux`
  passed, and the 3.1.0 bundle under "3.1.0" has all of it. Final screenshots:
  `/tmp/claude-1000/lock-final/final-*.png` (which does not last).

**Earlier that day:**

His words: "Delete two folders keep the baseline and Icon would be better -- I see the icon in other
apps/plugins without issue. Takeover notice only to linked people."

- **The footer bar's Twitch tool is Twitch's Glitch**, not the word, because at a 304 px panel the
  five tools left the stream title a few letters. Simple Icons 16.34.0's path (CC0), unaltered, in
  one colour (the bar's grey), in the same 34 px square as the other tools, with the connected dot
  in the button's corner, clear of the mark. Cards, tiles and popovers keep the plain word (their
  TWITCH tag, **Watch on Twitch ↗**, **Also on Twitch ↗**). Provenance and rules of use are in the
  amendment of 6 October at the top of `design/twitch-live.md`; `build/check-twitch.mjs` holds the
  path to the letter. In the lab with "Arma most nights #arma3", the title's room at 304 px went
  from 44 to 49 px ("Arm…" to "Arma …"), and at 400 px from 124 to 145 px (17 characters to 20).
  After review the Glitch is drawn at 14 px, not 16: at 16 it stood about 2 px taller than the
  icons beside it, because it fills its square where their strokes leave a margin; at 14 it has
  their height and top line. Its dot moved to 2 px from the corner, so it stays inside the button's
  rounded corner and its ring no longer bites a notch out of the lit square on hover or with the
  sheet open; `check-twitch.mjs` now holds that too. The title's room is unchanged by both.
  `make check`, `make lab` and `make dist-linux` passed after it, and the bundle under "Current
  release" has it; screenshots are in `/tmp/claude-1000/glitch-shots/` (which does not last).
- **The takeover notice stays shown to linked people only**, as built (it was an open item under
  "Known gaps", as was the 304 px footer; both are answered).
  Somebody let in or locked is not told in the panel; the server keeps the notice for up to 30
  days, so they usually see it if they unlock in that time. It goes sooner if the account is
  disconnected from its new owner (by them, or by an admin) or its Twitch data is deleted, and it is
  not shown while they are blocked from connecting (the amendment in `design/twitch-live.md` lists
  every case). No code change.
- **Two checkpoint folders deleted**: `Broadcast Box Plugin.pre-twitch-3.1.0` and
  `Broadcast Box Plugin.twitch-3.1.0`. `Broadcast Box Plugin.baseline-2.11.0` and
  `Broadcast Box Plugin.patches` are kept ("Backup folders next to the project" below).

## 3.0.0

3.0.0 was **never released**; 3.1.0 carries all of it. Three pieces of work went into it on 5
October: the panel tidy-up, the lock screen and the System Console cleanup (items 7, 8 and 9 below).
The cleanup was built in a copy of the tree, `/home/vvilla/Documents/Broadcast Box Plugin.ux-console`, and merged after the other two.
Then, the same night, 3.0.0 was retargeted at **Mattermost 11.11 only** (Victor: "Only support latest
version"; he runs 11.11): item 10 below.

The installable bundle is `dist/ceo.villa.villanet-3.0.0-linux-amd64.tar.gz`, built by
`make dist-linux` at 23:29 on 5 October (10,525,983 bytes, sha256
`bd166b089603c893d99c8d4cfa4bc8c867e33a0909d5d113806c2ae68395e25f`; 2.11.0's was 8.3 MB). It holds
everything below: the tidy-up, the lock, the System Console cleanup, the Mattermost 11.11 retarget
(its `plugin.json` says `min_server_version` 11.11.0) and the review fixes after them. The bundle it
replaced, built at 21:45 the same day, was the lock build from before the console merge, still with
`min_server_version` 9.3.0. A good bundle is newer than every file under `server/` and
`webapp/src/`, and than `plugin.json`.

It needs **Mattermost 11.11.0 or later** (`min_server_version`), and supports only that. Victor runs
11.11. An older server refuses to start it ("plugin requires Mattermost 11.11.0"); 2.11.0 ran on 9.0
and later. Clips also need Mattermost's file storage to be **Local file system**.

**The checks on 5 October**, after the console merge, the 11.11 retarget and the fixes from their
review (below): `go clean -testcache`, then `TZ=UTC make check`, `make e2e`,
`make e2e E2EFLAGS=-race`, `make lab` and `make dist-linux` all passed, in that order.
Last, at 23:29, after the birthday test was pinned to UTC (Victor's OK) and the plugin description was
reworded: `go clean -testcache`, then plain `make check` (no TZ=UTC needed now), `make e2e`, `make lab` and
`make dist-linux` all passed.

On the night of 2–3 October, before this work, `TZ=UTC make check`, both e2e runs and `make lab` had
all passed. **Nothing in 3.0.0 has been installed on the real Mattermost server yet.** Everything was
tested in the lab and the tests only (below).

| Version | What it added |
|---|---|
| 2.8.0 | Monthly medal tally on profiles and in the trophy menu |
| 2.9.0 | Clickable #hashtags in stream titles with a results window; name/title baseline fix; minus button only with 2+ streams; planning needs a linked account |
| 2.10.0 | Top Snoopers board (Trophies card, Snoopers tab); What's on? for impromptu streams; publish tokens and stream keys taken out of every title |
| 2.11.0 | VillaNET comes back after a reply or a closed panel; the stream keeps playing in a mini-player meanwhile; What's on? picker slides up; new card tag; centred Live Now count |
| 3.0.0 | Clips (record, clip, trim, publish, upvote, Best Clipper, share as a card, clip tags); the Top Snooper badge; the birthday list folds; games from IGDB, with titles and cover art remembered; the new header wordmark; Banner placement; WHEP answers no longer carry the stream key; the panel tidy-up (one quiet footer bar with sheets, a thumbs-up upvote, Up next only while something is planned); the lock screen (unlock with your bearer token, admins let people in to watch only and can unlink anybody, enforced on the server); no stream key in the snapshot for Pop out; a bearer token kept out of channels |
| 3.2.0 | Awards by your name (up to four, or Automatic, from the Trophies card's You tab); biggest fans (counted only while faces are shown, with an opt-out that deletes); achievements (five families, live and backdated, Alan Watts' 24/7 channel); sharing a plan as a banner card with Join and Watch; the soundboard for people in Mumble (a sixth footer tool, sounds made in the browser, checked and decoded on the server with pion/opus, played by shout into the presser's channel; off by default); @names in stream titles as chips; a thumbs-up pill on shared clip cards; stricter Mumble name matching and Suppressed in the headphones; x/net 0.60.0 and Go 1.27.2 |
| 3.1.0 | Twitch live (connect a Twitch account, linked people only; the live badge, a Live now card, airtime merged per person, one card for a simulcast with Also on Twitch; Twitch's player in the panel behind its own switch; Twitch connections in the System Console; Disconnect for everybody, the lock screen included); the boards rank people; the Clips row under the player is gone (the clapper opens Clips); the security and optimization pass (no broadcast mention from Mumble in any spelling or name, title searches only from the panel, pictures only for titles the panel shows, no stream key in a refused playback's log line, the hashtag search forgetting on sign-out, no push for a bitrate alone, voice changes without a poll, kept date formatters, grpc and x/crypto updated) |

## What 3.1.0 added

**Twitch live**, built to `design/twitch-live.md` (revision 2, its amendment box, and lock-screen §9.2)
in three phases: a server builder and a webapp builder in parallel, then one integrator. Victor's
binding answers:

- **Connect with Twitch**: the OAuth authorization code flow, identity only (`scope=openid`), through a
  single-use connect ticket from a CSRF-protected POST, a server-side state bound to the user and
  their interactive session, and an ID-token nonce. No Twitch token outlives the request.
- **Display**: the LIVE badge, a Live now card (title, category, Twitch's viewer count, a picture the
  server relays), and the stream playing inside VillaNET in Twitch's plain iframe embed, behind
  **Play Twitch streams inside VillaNET**, off by default. Below a 400 × 300 stage, watching opens a
  larger player over the page in the same click.
- **Airtime**: Twitch time counts toward the boards and medals, merged per person; Broadcast Box is
  authoritative and a simulcast is counted once (paid spans). The boards and medals now rank people
  even with Twitch off.
- **A simulcast gets one card**: the Broadcast Box one, with "Also on Twitch ↗".
- **A Twitch connection is not "linked"** and never unlocks anything. **Connect is for linked people
  only**; **Disconnect is open to everybody**, the locked included (a small Disconnect Twitch on the
  lock screen).
- **The same Twitch application as IGDB** (TwitchClientID/TwitchClientSecret).
- **The defaults stand for Q2, Q3, Q5, Q6 and Q7**: Disconnect purges that person's Twitch hours;
  while the switch is off the hours are kept, off the boards; What's on? or the plan wins over
  Twitch's category; the plain embed, with sound moved by reloading; the README's privacy notice plus
  the consent note in the Connect sheet.

New settings: **Show people live on Twitch** (`EnableTwitchLive`, off), **Play Twitch streams inside
VillaNET** (`TwitchEmbed`, off) and **Twitch connections** (`TwitchSetup`, a custom card: the OAuth
Redirect URL to register, who is connected, Disconnect, block, 24/7, Delete all Twitch data). New KV
keys: `twitch_links`, `twitch_ticket_<user>`, `twitch_state_<state>` (10 minutes each), and the Twitch
ledger `twitchair_totals`, `twitchair_month_YYYY-MM`, `twitchair_sessions`, `twitchair_paid`, which
the airtime export and backup never include. The README's "Twitch (optional)" section is the privacy
notice; `LOCKDOWN.md` lists every Twitch host and the two cases in which a browser reaches Twitch.

**What the integration (phase T2) changed**, beyond the README, LOCKDOWN, the `media.go` header, the
words test's skip and the version:

- The two halves' JSON was checked end to end against a real joined server (a scene test's snapshot,
  `/me` for linked, let-in and locked people, the admin status, the WebSocket event and the route
  classes): it matched the contract, and nothing had to change in it.
- **Disconnect Twitch for somebody let in to watch.** The contract gave Disconnect to linked people
  (the footer's sheet) and locked people (the lock screen), which left out somebody who connected while
  linked and was then unlinked and let in. They now get the same small key on a line over the WATCH
  ONLY bar; never Connect.
- **The airtime card says "Twitch hours are kept separately; see Twitch connections."** while the
  switch is on (§7.12, which neither half had built): a `twitchSeparate` field in the airtime status,
  with a test.
- **The Twitch connections card no longer says "Twitch is asked once a minute"** when the switch is on
  but no application is set up (nothing is asked then).
- **The lab's boards count a Twitch-only person** in "N streamers", as the server's do.

**What the review fixes changed** (seven findings from independent reviewers, each confirmed by a
verifier; all fixed, none skipped; recorded as "As built" in `twitch-live.md`'s amendment box):

- **Delete all Twitch data never loses the block list** (`deleteTwitchKeys`). It rewrites
  `twitch_links` by compare-and-set, keeping `blocked`, so a block another admin saves at the same
  moment is read again and kept; and a read the store refuses writes nothing, where it used to write
  an empty block list and still ask the admin to press again. Test:
  `TestDeleteAllNeverLosesTheBlockList`.
- **A link document that will not decode no longer stops Broadcast Box airtime for everybody.** It
  used to skip every Broadcast Box pass and carry frozen boards until Delete all. Now it reads as
  nobody connected: Broadcast Box is paid in full (no paid spans) and the boards and medals are built
  from Broadcast Box alone. A KV error still skips that one poll. If the document decodes again, the
  Twitch resync epoch is armed, so the stretch Broadcast Box paid alone is not paid again by Twitch.
  Test: `TestAnUnreadableLinkDocumentNeverStopsBroadcastBoxHours`; README's airtime section says it.
- **A Twitch tile no longer flips between the player and the plate as the body starts scrolling**,
  which reloaded Twitch's player many times a second under a classic scrollbar wider than the 8 px
  hysteresis (Firefox's, which Mattermost's 6 px rule does not reach). The panel's body keeps its
  scrollbar's room (`scrollbar-gutter: stable` on `.vntv-body`), so everything in it is that much
  narrower, scrolling or not; a Twitch tile needs a panel about 6 px wider in Chrome to play inline.
  check-twitch pins the rule and models the loop.
- **The theatre's Tab trap reaches Twitch's player.** `focusTrap.ts` counts an `iframe` as a stop (no
  other dialog has one), and a stop just after the card sends Tab out of the frame back to the top.
- **Focus in a theatre whose stream ends goes to the waiting tile**, also after the panel re-rendered
  meanwhile: `focusHome.ts` no longer forgets an element that still has focus just because it is in a
  portal outside the box.
- **Disconnect in the footer's Twitch sheet with Twitch switched off** (or, for a non-admin, not
  available) shows its words for their six seconds: the bar keeps the sheet, and the tool, from the
  press until the sheet closes; then focus goes to the bar's title. A Twitch tool that goes while it
  has focus (a block, the switch) sends focus there too.
- **The Twitch sheet keeps focus off the page's body** when Continue on Twitch or Disconnect goes
  with its phase: focus goes to the words (Waiting's, or Disconnected's), where a stray letter stops,
  and Tab from Waiting's words reaches Cancel.

The Clips row under the player was taken out on 5 October, at Victor's request, before the Twitch
build started: the clapper in the header is the way into Clips. Nothing of it is left in the code.

## Security and optimization pass (3.1.0)

The pass Victor asked for after Twitch live, done on 6 October in the main tree, still as 3.1.0. The
tree from before it was kept as `/home/vvilla/Documents/Broadcast Box Plugin.twitch-3.1.0` (deleted
later that day at Victor's word); each change below was confirmed by diffing `server/` and
`webapp/src/` against it before it was written up. The 04:15 bundle was built after the pass, and
before the Glitch; the 15:48 bundle replaced it, and the 19:10 one named under "3.1.0",
with the simpler lock, replaced that ("Victor's decisions, 6 October" above).

| Item | What changed | Tested in |
|---|---|---|
| ssec-1, chat bridge (`server/bridge.go`) | `@all`, `@channel` and `@here` are defused in any case and wherever Mattermost thinks the word ends (a trailing `_ . - :` still counts); a character reference typed in Mumble (`&#64;`, `&#108;`, `&commat;`…) shows as typed instead of being decoded into a mention (`&amp;` shows inside `code`); and every `@` in the sender's name is defused, since a Mumble user named `@all` made the bot post a real `@all`. | `server/bridge_test.go`: `TestRelayedTextCannotMentionEverybody` (extended), `TestEverySpellingOfABroadcastMentionIsDefused`, `TestAMumbleNameCannotMentionAnybody`, all judged by a model of Mattermost's own mention rules (`mattermostWouldBroadcast`). |
| ssec-2, title search and pictures (`server/media_api.go`, `api.go`) | `GET /api/v1/media/search` answers 403 "Search from the panel." without `X-Requested-With: XMLHttpRequest`, checked after the access gate and before the link and the meter. A picture the server does not hold is fetched only for a title shown to anybody (a plan, What's on? on a live card, a search's results) or, for somebody linked, a clip's tag or their own What's on? not yet aired (`refShownToLinked`); a link is no longer a pass for any title, because a Markdown image in a post is fetched by every reader's browser with their session. | `server/media_test.go`: `TestAnImageInAPostCannotSearchForTitles`, `TestOnlyATitleShownHereFetchesAPicture` (replaces `TestOnlyAShownTitleOrSomebodyLinkedFetchesAPicture`); `server/access_test.go`: `TestOnlyALinkUnlocks` (extended). |
| ssec-3, stream key in a WHEP refusal (`server/broadcastbox.go`, `exchangeWHEP`) | Broadcast Box's refusal text is cleaned of the stream key, in any case and before the 200-byte cut, for every caller: the viewers' playback relay (which logs it as "WHEP exchange failed" whatever **Log WHEP negotiation** says) as well as the Clips recorder, which was the only one cleaned before, and after the cut. | `server/broadcastbox_test.go`: `TestARefusedPlaybackRequestLogsNoStreamKey`. |
| sperf-1, bitrate (`server/poller.go`, `publish`, `noticeable`) | Broadcast Box's measured bitrate no longer counts as a change: it stays in the stored and pushed snapshot, but a poll where only the bitrate moved pushes nothing. Before, a 24/7 channel pushed the whole snapshot to every connection on every poll (15 s), around the clock. | `server/plugin_test.go`: `TestABitrateThatMovesOnItsOwnPublishesNothing`. |
| sperf-3, voice roster (`server/voice.go`, `poller.go`) | A roster change the panel shows (join, leave, mute, deafen, the connection dropping) republishes the last snapshot with the new roster, at most once every 2 s (`republishEvery`), run by the poller loop (`publishRosterSoon`, `publishRosterNow`). It never polls Broadcast Box or touches the store; it used to cost a whole poll each. Comment and texture changes still push nothing. A roster republish and a clips republish each put both the roster and the clips on (`addVoice`, `addClips`), so neither takes back the other's change. | `server/voice_test.go`: `TestARosterChangeIsPublishedWithoutAPoll`. |
| wsec-1, hashtag search (`webapp/src/tagSearch.ts`, `index.tsx`) | The caches of people and channels Mattermost had not loaded are forgotten on sign-out and in `uninitialize` (`forgetTagSearch`), and a search still on its way at sign-out cannot refill them. Mattermost sanitizes those answers per viewer: with Show Full Name off, full names go only to system admins. | `build/check-away.mjs`: the three "hashtag search:" checks, and the check that `index.tsx` calls `forgetTagSearch` in both places. |
| wperf-2, dates (`webapp/src/time.ts`) | One `Intl.DateTimeFormat` is kept per locale, zone and set of options; a formatter with no zone is still made each time, so it follows a change of the machine's zone. `viewerZone()` is read once per render, and whether Intl knows a zone is remembered per zone name. A snapshot push went from about 6 ms to 3 ms with 4 tiles and 5 Up next cards, and a push or tick with the Clips list open on 101 clips from about 35 ms to 4.5 ms (the fixer's measurements, not repeated for this write-up). | `build/check-away.mjs`, which now also loads `time.ts` and `tagSearch.ts` (the latter with stand-in modules, `loadWith`): the five "time:" checks. |
| Dependencies (`go.mod`) | `google.golang.org/grpc` v1.83.1 → v1.83.2 (GO-2026-6443) and `golang.org/x/crypto` v0.55.0 → v0.56.0 (GO-2026-6354, GO-2026-6355). | govulncheck v1.8.0: 0 vulnerabilities in called code (below). |
| sperf-2, the offline grace (`server/poller.go`, `nowplaying.go`, `clips_manager.go`) | Only the poller's own ticks advance a miss or failure counter: `offlineStreak` (the badge), the What's on? off-air count, the recorder slots' `missing`, the Twitch cover streak and `pollFails`. A refresh poll (a plan, What's on?, link or Twitch save) and a settings-save restart may still reset a count when they see the stream again, and still publish. The first poll after a restart counts only when the plugin has just started or a whole interval has passed since the last tick (`pollerTickedAt`), so a settings save cannot count either. Airtime, watch time and Twitch credit were already by elapsed clock time, so they are unchanged. The verifier first dropped this as not worth its risk; it was fixed because the bug deletes an aired What's on? entry. | `server/poller_test.go`: `TestOneMissedTickAndAnyNumberOfRefreshesTakeNothingDown`, `TestRefreshesAndSettingsSavesAreNotChecks`, `TestRefreshesDuringAnOutageDoNotEmptyThePanelSooner`, `TestARefreshThatSeesTheStreamAgainStartsTheGraceAfresh`, `TestRefreshesNeverSwapATwitchCard` (each fails on the old code), `TestTheFirstPollAfterAQuietIntervalIsATick`. |
| wperf-1, the stylesheet's comments (`build/squeeze-css-loader.js`, both webpack configs) | At build time only, the CSS literals in `styles.ts` lose their 436 comments, each line is trimmed and empty lines are dropped; nothing is joined and no string or `${…}` is touched. `main.js` went from 669,251 to 559,972 bytes (175,896 to 140,288 gzip -9). The verifier first dropped this as risky for about 3 ms a page load; it went in in this conservative form, with proof. Checked once in headless Chrome: the same 1,349 rules (1,348 with identical cssText, the 1,349th, the Up next rail's multi-line mask gradient, differing only in the indentation Chrome keeps inside a `var()` value), and 0 differences in the computed style of every panel element (212 and 470 elements, at 400 and 776 px). | `build/check-squeeze.mjs` (in `make check-webapp`): the same 37,121 tokens, the same whitespace boundaries, all 20 interpolations, no comment left, 13 malformed inputs refused, an exact-output sample, and both webpack configs applying the loader. |

**Dependencies.** After the update govulncheck v1.8.0 reports **0 vulnerabilities in called code**.
The one module-level item left is GO-2026-5932, the unmaintained `openpgp` package, which has no fix
and which the plugin does not use. Neither earlier advisory was reachable: Mattermost's plugin RPC
serves `net/rpc`, not gRPC (grpc is linked through `hashicorp/go-plugin` but not called), and nothing
imports `golang.org/x/crypto/ssh`. For this write-up `go.mod` and the server's package list
(`go list -deps ./server`) were checked: no `openpgp` and no `x/crypto/ssh` package is in the build.
govulncheck itself was not run again.

**Dropped, and why:**

- **sperf-4**, duplicate identity reads per poll. Removing them would save about 1.3 KV reads a second
  on a server with 30 streamers, which is not worth a refactor that cuts across the code.
- **wperf-3**, batching the subscriptions' renders on the legacy root (Mattermost 11.11's
  `ReactDOM.render`). After wperf-2 those renders take under 1 ms a push, so it would save almost
  nothing.

## What 3.0.0 added

Victor's requests, each recorded in the design named.

1. **Clips** (`design/clips-spec.md` rev 2 plus `design/clips-addendum.md`, which wins; Victor's
   rules and answers are in addendum A§1, with the lock's amendment of 5 October under it).
   - Clipping, publishing, upvoting, reporting and sharing are for linked people only. Listing and
     watching are for people who are linked, or whom an admin has let in to watch (item 8).
     Everything is enforced on the server. Locked people see the lock screen, not clips.
   - Every system admin, linked or not, has full moderation in System Console
     (`/api/v1/admin/clips*` check only for a system admin, Q-A). In the panel, admins are locked
     like everybody else until they unlock it or let themselves in.
   - Who may clip: somebody linked who is watching that stream in VillaNET now (or was when it
     ended: the 10-minute tail), or its streamer (by the current link).
   - Recording is on for every live non-24/7 stream, with a streamer notice, an "Allow clips of my
     stream" switch and a wipe. Clip length 2 min by default, 5 min at most (admin), trim only.
     Clips kept 30 days by default.
   - Best Clipper: one all-time holder by total counted upvotes, no runner-up anywhere; beside names
     in posts, in the profile popover and on the Trophies card's Clippers tab. Expiry and a
     streamer's deletion keep the votes; the clipper's or an admin's deletion voids them. Votes count
     only while the voter is linked (Q-C). No download button anywhere.
   - Sharing: Share posts the clip into the current channel as a `custom_villanet_clip` post by the
     sharer (Q-D), and works while Clips is off (Q-E). The card shows title, clipper, streamer, length
     and Watch; the plain-text message carries the link and no title or names (Q-F); Watch opens
     VillaNET's panel on the clip. A locked reader's card shows no thumbnail, and its Watch opens the
     lock with the clip waiting behind it. Expired and deleted clips say so. Nobody who is not linked
     can delete their own clips or drafts (Q-B).
   - Clip tags (A§4.14): a clip carries its stream's game, movie or show (What's on?, or the plan on
     now) from the moment it was cut, shown in its details and on the shared card beside the
     clipper's title.
2. **Top Snooper badge**: one holder, first on the all-time Snoopers board, gold binoculars drawn
   like the crown, beside names in posts, in the popover and on the Trophies card; gone while the
   Snoopers board is off. It is also on Live Now cards — built beyond the request; Victor was told and
   has not objected.
3. **System Console birthday list folds** with more than four people: a summary line, Show/Hide the
   list, and Add somebody opens it.
4. **Games that are not on Steam, from IGDB** (`design/media-lookups.md` Parts A–D and "Answered by
   Victor"; Part E is superseded by `design/twitch-live.md` and not built). A Twitch section holds the
   Twitch application's Client ID and Secret, shared with the coming Twitch live feature. Steam comes
   first. Searches and titles are remembered in the plugin's KV store (IGDB's for 3 days a search and
   7 a title, never served past them), and cover art is kept on disk under
   `<FileSettings.Directory>/ceo.villa.villanet/media/`.
5. **Header wordmark**: the VillaNET skull emblem (28 px, the App Bar icon) and "VILLA" in white with
   "NET" in red (23 px), replacing the script "Villa" and red "NET" pill.
6. **Banner placement**: "Top only" or "Top and bottom" (the default), the choices of Mattermost's
   Classification Markings.
7. **The panel tidy-up** (5 October). Victor chose design A, "One quiet bar", from three variants; the
   variants are in the `ux-variant-a`, `-b` and `-c` folders. There is no separate design document,
   so the code and the README describe it.
   - **The Clips row** that sat directly under the player was taken out again on 5 October, at
     Victor's request: the clapper in the header is the way into Clips.
   - **The footer is one quiet bar.** For somebody linked, it holds the stream title and a few small
     tools: What's on?, the scissors for clips of your stream (with a dot that says whether they are
     on), the link key that relinks, and the calendar that plans. Each tool opens a sheet that rises
     out of the bar, one at a time.
   - **Upvotes are a thumbs up.**
   - **Up next is there only while something is planned**, for everybody. Planning starts from the
     calendar.
   - Two grafts Victor accepted with it: Relink has one home, the link key, always in a linked
     person's bar; and the clip sheet's **Wipe** is an all-caps Oswald pill.
8. **The lock screen** (5 October, `design/lock-screen.md` rev 2, with its "As built" section).
   - **Nobody uses the panel until they unlock it** with their own bearer token (which is linking),
     system admins included. The System Console works without a token.
   - **A system admin can let people in** without a token, in a new **Access** section. Let-in people
     can watch streams and clips and do nothing else. Admins can also **unlink** anybody.
   - **Outside the panel nothing is hidden**: badges, medals, the popover's live row and shared clip
     cards. Their Watch buttons open the lock, which remembers what was asked for (for 10 minutes).
   - **The panel opens by itself onto the lock** for locked people when **Open the panel automatically**
     says so (Victor's answer), without putting the bar in focus.
   - **The server enforces all of it**: a front gate gives every route a class, and the old
     per-route checks stay.
   - A bearer token pasted into a message is refused before it is posted (webapp and server). Pop out
     no longer puts stream keys in the snapshot.

9. **The System Console cleanup** (5 October, built in the `ux-console` copy, merged after the lock).
   - **Seventeen sections in the order an admin sets things up**: Broadcast Box, Access (the lock's),
     Panel, Streams, Artwork, Live badge, Airtime, Clips, Birthdays, Theme, Movies and games, Twitch,
     Mumble, Mumble chat bridge, Bot account, Backup, Troubleshooting, each with a one-line subtitle.
     Every setting's key, type, default and choices are unchanged; many display names are shorter.
   - **Short help texts** that keep every disclosure; the longer story is in the README. The four the
     lock had left (Open the panel automatically, Match stream keys to usernames, Let people clip
     streams, Public watch URL) were rewritten for the lock in the merge.
   - **One look for the eleven cards** (`webapp/src/components/adminParts.tsx`, the list in
     `webapp/src/adminSettings.ts`): a head line with **No Save needed** where it acts at once, rows
     with a picture, pills, two status lines, and a second click, red with a question mark, for
     anything that cannot be undone. The Access card moved onto it in the merge.
   - **The old bot's birthday list** is no longer in the webapp bundle every user loads: the System
     Console fetches it from the admin-only `GET /api/v1/admin/birthdays/seed`, which skips the lock's
     gate as every System Console route does.
   - **The lab draws the whole page** from `plugin.json` the way Mattermost does
     (`webapp/lab/consolePage.tsx`), under **Show › System Console**; the Access card's states are
     chosen in the lab's **Access card** row first.
10. **Mattermost 11.11 only** (5 October, after the console merge; Victor supports only the latest
    Mattermost, which he runs).
   - `min_server_version` is **11.11.0**.
   - **React 18.2.0.** Mattermost 11.11.0 and 11.11.1 ship react and react-dom 18.2.0, with
     @types/react 18.2.64 and @types/react-dom 18.2.25 (`webapp/channels/package.json` lines 76, 81,
     127 and 131, and the only copies in `webapp/package-lock.json`); `webapp/package.json` pins
     exactly those. Plugins still get it off the page (`window.React`, `window.ReactDOM`, set in
     `plugins/export.ts`), so the bundle carries none.
   - **Mattermost 11.11 mounts its page on a legacy root** (`ReactDOM.render` in
     `webapp/channels/src/entry.tsx`) unless the `EnableConcurrentReact` feature flag is on, which it
     is not by default. So the lab mounts the same way, and its **React root › concurrent** row
     reloads it on `createRoot`, as a server with the flag on would run it.
   - `build/check-react.py` holds the pin to what Mattermost ships at `min_server_version` (a table in
     the script, with its source), checks `node_modules` against the pin and webpack's externals, and
     fails if `min_server_version` moves without a row for it.
   - Dropped as 9.x/10.x only: the System Console footer ("Needs Mattermost 10.0 or later"), the
     "masked here from Mattermost 11.5" caveats in four help texts, the README's 9.x and 10.x notes,
     the lab console's 10.11 view, the `typeof` guards round registry methods 11.11 has (checked in
     its `plugins/registry.ts`), the archived-channels setting read for search (11.11 always searches
     them), and the hand-off to 10.2's search form, with the lab's search bar rebuilt as 11.11's.
     What was kept, and why, is under "Known gaps".
11. **The review of the merge and the retarget** (5 October, fixed the same night).
   - **Two presses mean two presses.** Holding Enter used to confirm a two-press button by itself:
     Enter repeats after about half a second, and each repeat pressed the button (or the Unlink box)
     again. Now a press from a key that has not come up since the arming press never confirms
     (`webapp/src/twoClicksLogic.ts`, checked in `build/check-away.mjs`). Every two-press button in
     the panel and the System Console goes through it.
   - **The Access card** says what a let-in, Remove or Undo did under the let-in box, and what an
     unlink did under Unlink, rather than everything under Unlink. Past eight people the list shows
     its first eight and **Show all N** (rows just taken off, just let in or holding focus stay
     shown), so a full list of 500 no longer pushes every later section 30,000 px down. A list that
     could not be loaded has **Refresh**. The username in a row never shortens, and the two username
     boxes line up.
   - **Help texts:** **Show who is watching** says again that the stream list carrying the faces
     reaches every signed-in browser, locked ones included; the Access subtitle fits one line and its
     help text no longer repeats the lock.
   - **Smaller things:** the console's upvote lines wrap after a dot, never before one, and keep
     "(12 banked)" whole; a card that could not load says why once, not twice (Clips, Access, preview
     pictures, 24/7 channels, Movies and games, Bot account); Forget says "Forgot 54 remembered
     entries and 37 pictures."; the lab's **four people** chip is lit only for those four; the last
     "on 9.x an svg click throws" notes became 11.11's reason (an svg click follows the permalink).

Also in 3.0.0, from the reviews: WHEP answers no longer carry the stream key (every earlier release
passed it to viewers' browsers; see `LOCKDOWN.md` §4 and the manual's "Upgrading to 3.0.0", in `docs/upgrading.md`); with
**Public watch URL** set, the snapshot no longer carries every stream's watch address with its key in
it (Pop out is a redirect for linked people only); `/me` no longer carries the Broadcast Box URL, nor
`publicSiteUrl`; `setLink` no longer fails open on a read error, which used to leave a previous owner
linked; preview and 24/7 pictures are `private, no-cache`, so a cached one never outlives a
let-out; a title's picture the server does not hold is fetched only for a title on screen or for
somebody linked (now by the lock's meaning of linked); the settings backup says it carries no clips and names
stream keys; the help texts of **Show who is in voice**, **Log WHEP negotiation** and **Let people
clip streams** now name the title lookups, the recorder's own sessions in the log, and the
memory-only record of who is watching what; the red text of error notices and of the Share, Delete and Wipe buttons raised past
4.5:1.

## Tested only in the lab and the tests

- **All of Twitch live (3.1.0).** Nothing has contacted the real Twitch. The server half is tested
  against a fake Twitch (`server/twitch_scene_test.go` on the media fake: OAuth, Helix and the picture
  host), including every test the contract names, under `-race`. The webapp half was walked in the lab
  at 304, 400, 464 and 776 px, every state of contract §11.3, with screenshots in
  `/tmp/claude-1000/villanet-shots/twitch-int/` (which does not last). The lab's approximations:
  - Twitch's player is a stand-in that counts its loads; the real frame, its referrer policy, its
    sandbox and autoplay are untested (§13.2 steps 4, 5 and 5a below);
  - the Connect window is simulated, so Twitch's consent screen, the callback pages closing
    themselves, and the desktop app's popup are untested;
  - headless Chrome fires no focus events unless asked, so a theatre whose stream ends was checked
    with a simulated focus; the theatre's focus trap and Escape were checked with real Tab and Escape
    key presses;
  - the review fixes were checked in headless Chrome with CDP focus emulation and real key presses:
    the theatre's Tab round with a same-origin `srcdoc` frame injected in the lab as a stand-in for
    Twitch's cross-origin one (never in the plugin); focus on the waiting tile when the stream ends,
    with and without a viewer-count change first; the sheet's words and focus after Continue (pop-up
    blocked) and after Disconnect with the switch on and off, with a typed letter kept out of the
    message box; and no player reload anywhere from 430 to 470 px with the body at its overflow edge
    (the same run without the gutter rule reloaded about 24 times in 250 ms). That used Chrome's 15 px
    classic scrollbar, which stands in for Firefox's: the scrollbar loop was not run in Firefox;
  - reduced motion was checked with the browser's emulation (nothing Twitch animates);
  - the System Console card was looked at in the lab's console page, which is light only, as
    Mattermost's System Console is.
- **The security and optimization pass (3.1.0)** is checked by the Go tests and `check-away.mjs`
  only. The chat bridge tests judge a post by a model of Mattermost's mention rules
  (`getExplicitMentions` and `mention_parser_standard.go` at v11.11.1, through Mattermost's own
  Markdown parser), not by a running Mattermost; the hashtag search's sign-out is checked with
  stand-ins for Mattermost; the date timings are the fixer's own measurements.
- **All of 3.0.0.** The panel, the Clips dialog, the editor, the shared-clip card and the System
  Console views were looked at in the lab (`make lab`) in headless Chrome, narrow, popped out and at
  phone width, the card in Mattermost's Denim and Onyx themes.
- **The recorder has never met the real Broadcast Box.** `make e2e` runs it in real time against an
  in-process stand-in that answers WHEP the way Broadcast Box v2.0.2 does, over loopback. The design
  research's prototype ran against a real Broadcast Box v2.0.2 (about 4% of a core and 25 MB per
  6 Mbit/s stream), but this build has not.
- **Mattermost's side of sharing** (the hooks, `CreatePost` as the sharer, the clip link route, the
  landing page on phones, permalink previews, Move Thread) was checked in Mattermost's source and in
  unit tests, not on a running server.
- **The simpler lock of 6 October** was looked at only in the lab, in headless Chrome: idle at 304, 400
  and 776 px, and at 400 px a system admin with every alert (identical to anybody's), a wrong token, a
  reason line, Twitch connected, No signal and not set up. The focus fix after Disconnect Twitch was
  driven with real key presses through a small CDP driver, on the lock and in the let-in footer, before
  and after its words lapse; the build before it sent the next letter to the lab's message box, and
  this one does not. The let-in footer's and the relink form's token bars were compared with the build
  before C: the same layout, words and focus, and pixel for pixel the same, but for a one-pixel raster
  difference at the bar's focused edge in the let-in sheet before anything is pressed (its layout and
  computed styles are identical). Screenshots are in `/tmp/claude-1000/lock-final/` (which does not last).
- **The panel tidy-up and the lock screen** were looked at only in the lab, at 400 and 320 px, in the
  main window and popped out, and driven with real mouse and keyboard input from a small CDP driver.
  The lab's approximations:
  - the paste test is a simulated Ctrl+V, so the routing is confirmed but a real clipboard paste is
    not;
  - "phone-sized" stands in for a coarse pointer;
  - reduced motion was checked with the browser's emulation;
  - Mattermost's own move of focus into the side panel is a lab copy of its two timer hops;
  - the WebSocket's `access` event, a reconnect and a store that does not answer are lab buttons.
    A second server in a cluster is not simulated at all.
  Nothing about the lock has met a real Broadcast Box token, a real password manager or a real
  cluster.
- Still open from earlier releases:
  - 2.11.0: separate popout windows, Mattermost's own focus moves with real key presses, browsers that
    block autoplay, and plugin-disable cleanup.
  - 2.9.0: the "Open in Mattermost search" hand-off (the lab's stand-in is now 11.11's search bar
    only, a button that opens a box; the hand-off for 10.2's form is gone), and jumping to a result
    on a phone-sized or expanded panel.
  - 2.10.0: clicking the What's on? title in a profile popover does not open its details card
    (Mattermost probably closes the popover on an outside click).
  - The Live Now count centring: if it still looks off, take a zoomed screenshot.

## The real-host checklist

Only after Victor says go, and with him present (addendum A§5.1). Mattermost on 192.168.0.101,
Broadcast Box on 192.168.0.106. The 3.0.0 checks below come first; Twitch live's are at the end, and
are all UNVERIFIED until then.

**Before installing**

- Mattermost is 11.11.0 or later; **File Storage System** is **Local file system**; the data volume has
  well over 2 GiB free (recording stops below 1 GiB; new recordings, drafts and publishes need 2 GiB
  or 5% of the volume, whichever is more).
- **Site URL** is set in Mattermost (a shared clip's link needs it).
- The plugin's **Broadcast Box URL** is the LAN address, `http://192.168.0.106:8080`, not localhost,
  127.0.0.1 or the public name.
- The Mattermost host can exchange UDP with `192.168.0.106:3478` (outbound, and the replies on an
  ephemeral port). CT106 already allows UDP 3478 from anywhere.
- Streamers' OBS: H.264, **B-frames 0** (not the NVENC UHQ tune), **Keyframe Interval 2 s**.
- To try IGDB, set up the Twitch application: sign in at dev.twitch.tv/console with a Twitch account
  that has two-factor authentication on; Applications → Register Your Application; any name; OAuth
  Redirect URLs `http://localhost`; any category; Client Type **Confidential**; Create. Then Manage,
  copy the **Client ID**, press **New Secret** and copy the **Client Secret**, and paste both into
  System Console → Plugins → VillaNET → **Twitch**, with **Look up movies, shows and games** on.
- Take an airtime export (System Console → Airtime → Export) before upgrading, as before every
  upgrade.
- **The upgrade locks the panel for everybody who has not linked a stream**, system admins included.
  Decide who should watch without a token. Straight after enabling, let them in under System Console
  → Plugins → VillaNET → **Access**. People who linked a stream before are already unlocked. The lock
  does not tell anybody that they can be let in (Victor, 6 October).

**Clips: the spec's §6, as the addendum rewords it (A§3.10), then A§5.3**

- A1. With somebody streaming from OBS, the stream's recording is ready within the keyframe interval
  plus 5 s. VillaNET's viewer count and faces do not change; Broadcast Box's own count goes up by one.
  The admin view shows "Dave — 2:00 held".
- A2. ✂, or C on the tile, opens the editor at once, cutting, with the cursor in the title. Within
  3 s at 2:00 and 6 Mbit/s it shows a draft ending at the moment of the click, sound in sync.
- A3. A linked viewer who tuned in 10 s ago can clip the whole 2:00. A linked user not watching is
  told "Tune in to this stream to clip it."; a let-in user has no ✂ and C does nothing; a locked user
  has no panel at all. The linked streamer can clip their own stream without watching it.
- A4. Trimming moves in 100 ms steps, the handles cannot cross or go under 5 s, the published clip
  starts at the chosen start (±1 frame) and ends within a frame of the chosen end, its card shows the
  length chosen, and every control works from the keyboard.
- A5. A published clip, with its picture, is in every other linked or let-in user's Clips within 2 s,
  with the "new" mark. An empty title shows "Dave, 3 Oct 21:04".
- A6. Clips play and seek in the desktop app and in Firefox; DevTools shows 206 answers through the
  plugin's route.
- A7. Drafts, their video included, are invisible to everybody but their owner.
- A8. Upvote and un-upvote survive a reload; your own clip cannot be upvoted; voting makes no extra
  `/api/status` request to Broadcast Box (its log).
- A9. Best Clipper: most counted upvotes, a tie keeps the holder; expiry and a streamer's delete
  keep the votes, a clipper's or admin's delete removes them; deactivated and unlinked voters do not
  count; blocked people cannot hold it; the badge is in posts, the popover and the Trophies card, with
  no runner-up anywhere; it disappears everywhere while Clips is off.
- A10. Clip length offers 0:30 to 5:00 only; 999 set in config.json behaves as 300; a fresh install
  is 2:00, 30 days, Clips on.
- A11. A clip past Keep clips for leaves the list at once (410) and the disk within 10 minutes, its
  upvotes kept for Best Clipper; lowering the setting removes nothing for 24 hours, and the admin view
  says what will go and when.
- A12. Switching Clips off removes every ✂ within one poll, even while Broadcast Box's status is
  failing; no recorder session is left on Broadcast Box, `ring/` is empty, drafts are gone; published
  clips stay watchable and votable for linked users.
- A13. No stream key in any snapshot, API answer, KV document, file name or log line (grep the logs).
  No traffic but Broadcast Box's LAN address (TCP 8080, UDP 3478), Mumble and the opt-in lookups.
- A14. With the Mattermost host's UDP to Broadcast Box blocked: the ✂ says "Clipping isn't working on
  this stream just now", admins see a reason in the panel and System Console, watching is unaffected,
  and unblocking recovers without a restart.
- A15. OBS stopping and restarting within the grace: a new recording starts, with sound in later
  clips; the ✂ offers "Clip the last 2:00 before the stream dropped" until the new recording has 5 s;
  a 3 s stall with no reconnect does not restart the recording (the clip freezes, with silence).
- A16. Disabling and enabling the plugin, or upgrading it, keeps published clips, votes and the
  holder; recordings, tails and drafts are wiped.
- A17. With storage set to S3, Clips says why it is off and nothing crashes.
- A18. `make check` and `make e2e` pass, and every webapp state is reachable in the lab. (Done.)
- A19. With focus anywhere in the panel, editor or Clips dialog, C, I, O and Space never type into
  the message box or move focus out of the dialog.
- A20. A double click on PUBLISH, or publishing one draft from two tabs, gives exactly one clip; a
  publish whose answer was lost is recognised as published.
- A21. A linked streamer sees the notice while live; switching "Allow clips of my stream" off makes
  every ✂ on their stream say they switched it off within one poll, and the recorder's Broadcast Box
  session goes; "Wipe the last 2:00" makes drafts cut from their stream disappear for everyone, and
  the next draft starts after the wipe.
- A22. After a stream ends, a linked person who was watching can clip its last 2:00 from the No
  Signal tile or from Just ended, for 10 minutes.
- A23. Lowering Keep clips for from 30 to 3 and setting it back within 24 hours deletes nothing.
- A24. One person cannot publish beyond their share; other people still can.
- A25. As a locked user (curl with their cookie), every `/api/v1/clips*` route answers 403 `locked`,
  files, cards and their own clips and drafts included; their panel is the lock screen, and DevTools
  shows no `/clips*` request at all. As a let-in user, the five watch routes (GET `/clips`,
  `/clips/clip`, `/clips/cards`, `/clips/video`, `/clips/thumb`) answer, and every other one gives
  403 `watch_only`.
- A26. Unlinking and relinking: the holder changes within one sweep, drafts come back on relink within
  2 h, the opt-out stays with the stream.
- A27. In System Console a system admin, linked or not, can list, watch, delete, block and wipe; a
  non-admin gets 403 `forbidden` on every `/api/v1/admin/clips*` route (curl). A locked admin's panel
  is the lock screen and makes no `/clips*` request.
- A28. Share posts one card, by the sharer, in the current channel within 1 s; a double click or a
  retry within 60 s posts once; an 11th share of one clip is refused; Share is disabled in a shared
  channel. On the mobile app the post is the fixed message with no title or names, and its link opens
  VillaNET on the clip after a browser sign-in (on the lock first, for somebody who has not
  unlocked); on a phone browser's first visit, View in Browser
  leads to the clip and View in App shows "This link you are trying to open is invalid." Note whether
  a link preview appears (none is expected).
- A29. A locked reader's card shows title, clipper, streamer and length and no picture, and makes no
  `/clips*` request. Its Watch opens the lock with "Unlock VillaNET to watch this clip.", and the clip
  opens after unlocking; in a thread popout the card says "Open VillaNET in the main window to unlock
  it." A linked or let-in reader's channel of several cards makes one `GET /clips/cards`; a vote
  elsewhere makes none.
- A30. Deleting a shared clip makes every open card say "This clip was deleted." at once, and neither
  the card, its accessible name, the post's props (`GET /api/v4/posts/{id}`) nor the message carries
  the title or names. An expired clip's card says "This clip has expired." within 10 minutes. Neither
  shows "(edited)" on the web.
- A31. While VillaNET runs, a `custom_villanet_clip` post made through `POST /api/v4/posts` arrives as
  an ordinary message with no card; patching a shared post's props changes nothing the card shows;
  editing its message is refused; moving a thread whose root is a shared clip works, and the copy is
  an ordinary message.
- A32. Watch on a card opens VillaNET and the clip in the main window, and the clip in a popped-out
  channel or thread window (11.3+). The card reads well in Denim and Onyx.
- A33. Signing out and back in with a password (no page reload): a linked user's card shows its
  picture without the panel being opened, and the Best Clipper badge and the clapper appear.

**The lock screen (`design/lock-screen.md` §9.5, with what the build added)**

- R1. On Mattermost 11.11, a locked person opening VillaNET by hand sees the lock, and
  the bar takes focus (not on a phone). Opened by itself, it never does.
- R2. Unlock with a real token: the padlock swings open, the panel boots, and "Unlocked." is announced.
- R3. A wrong token says "That token was not accepted. Check it and try again."
- R4. **What the deployed Broadcast Box answers for an unknown token.** 204 is expected. If it is
  anything else, add exactly that status to `not_accepted` in a follow-up.
- R5. Let in, then let out, while watching: the tiles stop and the lock comes back, saying "You are no
  longer let in."
- R6. A takeover by another account (the same token linked from a second account) locks the first one,
  with "Your stream was unlinked here, by an admin or because another account linked it."
- R7. A popped-out panel (11.3+) follows an unlock made in the main window, when it is looked at.
- R8. A locked admin's System Console works: every card, the preview pictures, and clips playing in
  Clips on the server.
- R9. Pop out, with a Public watch URL set: offered only to linked people. It opens the watch page in
  a browser and in the desktop app, and the stream list (DevTools, `/api/v1/streams`) carries no
  `watchUrl`.
- R10. A card's Watch in a thread popout (it says where to unlock), and in a channel popout (11.3+, it
  opens that window's lock).
- R11. With a saved Mattermost password, in Chrome, Edge and Firefox, the bar is never offered that
  password, and unlocking never offers to save or update a password.
- R12. Ctrl/Cmd+V on Unlock, Show and Try again lands in the bar, never in the message box, with a real
  clipboard and on a non-Latin keyboard layout. A token-shaped message is refused before it is
  posted.
- R13. Let out while the WebSocket is down, then reconnect: the panel locks. A popout left visible on
  a second monitor stops within 60 s.
- R14. On a phone, iOS Safari does not zoom when the bar is focused, and opening the lock does not
  raise the keyboard.
- R15. The server's guard: a message that is only a UUID, posted from the mobile app by somebody not
  linked, is refused with "Post rejected by plugin. That looks like your bearer token…". The same from
  a linked person goes through unless it is their own token.
- R16. With **Open the panel automatically** at **When somebody is live**, a locked person's page load
  opens the lock, and typing in the message box meanwhile stays in the message box.
- R17. Straight after the upgrade, a streamer who linked before 3.0.0 is unlocked without doing
  anything, and a viewer with no token sees the lock, with nothing on it about being let in.
- R18. The Access card in the real System Console: add by `@name` and by `name`, the count, Remove and
  Undo, a deactivated account's tag, "(you)"; Unlink's two presses and each result, and the Remove
  beside "still let in to watch". A let-in person's open panel follows within a moment.
- R19. A locked person's popover Watch opens the lock naming the stream, and the stream plays after
  unlocking; ten minutes later it no longer would.
- R20. **The simpler lock (6 October) on Mattermost 11.11**, at the side panel's narrowest and widest
  and on a phone: the dimmed emblem with its padlock, the bar docked at the foot with the eye and the
  arrow inside it, nothing else; a wrong token's words over the bar move nothing; the arrow turns red
  once something is typed. In Mattermost's dark and light themes, and with an uploaded emblem.
- R21. A system admin with an alert showing (stop the voice server, say) sees exactly the lock anybody
  sees, and the alert in the panel once unlocked. The not-set-up face says only "VillaNET is not set up
  yet, so it can't be unlocked.", for an admin too.
- R22. With Twitch connected and locked: Tab to **Disconnect Twitch**, Enter twice; the words take
  focus, and a letter typed then lands in the bar, never in the message box; after six seconds focus is
  on the lock, and a letter still lands in the bar. The same in a let-in panel's footer, where the
  letter goes nowhere.
- R23. On a server whose configuration has **Who to ask** saved from 3.0.0: the plugin starts, the
  **Access** section shows only **Let in without a token**, and restoring a 3.0.0 settings backup says
  "Ignored, because this version has no such setting: AccessContact."
- R24. Disable the plugin with a fresh page's lock open, so it shows No signal; enable it, Tab to
  **Try again** and press Enter: the face changes, focus is on the lock, and a letter typed lands in
  the bar, never in the message box. The same from the panel's quiet "could not check your access"
  notice, where the letter goes nowhere. With a Russian keyboard layout, Ctrl+V on a clicked tile, or
  on the words after Disconnect Twitch in a let-in footer, stays out of the message box.

**The panel tidy-up**

- The footer bar at 320 px and at full width, with a What's on? poster set: the stream title gives
  way, and every tool stays inside the bar.
- Each sheet (What's on?, planning, clips of your stream, relink) opens and closes from its tool and
  with Escape, and only one is open at a time.
- The clapper opens Clips (there is no Clips row under the player), and the thumbs-up votes, in the
  desktop app and a browser.
- Up next appears with the first plan and goes with the last.

**Twitch live, 3.1.0 (`design/twitch-live.md` §13.2, with the lock's amendments)**

Before: Mattermost's **Site URL** is `https://<a name>` on port 443 (Twitch's player runs nowhere
else, and Twitch accepts no other callback). Then:

- [ ] W0. **Register the callback.** In System Console → Plugins → VillaNET → **Twitch connections**,
  copy the **OAuth Redirect URL** (`https://<site>/plugins/ceo.villa.villanet/api/v1/twitch/callback`)
  and add it, exactly, under OAuth Redirect URLs in the Twitch application IGDB already uses
  (dev.twitch.tv/console/apps), then Save there. The card should say "Client ID and Client Secret are
  set." Switch on **Show people live on Twitch**; the card's last check should say Twitch was asked.
- [ ] W1. **Connect** as a linked person in Chrome, Firefox and the desktop app. The desktop popup must
  carry the Mattermost session and close, or say to close it; the panel must sit in Waiting (not the
  pop-up hint) while it is open. Note whether `scope=openid` shows any permission text on Twitch's
  consent screen, and whether the callback page closes itself.
- [ ] W1a. Post `![x](https://<site>/plugins/ceo.villa.villanet/api/v1/twitch/connect)` in a test channel,
  and open `…/twitch/connect` in a new tab by hand: neither may open Twitch or count against Connect
  (the `stale` page, or nothing).
- [ ] W1b. **Linked only.** A let-in person and a locked person have no Connect; `POST
  /api/v1/twitch/connect-ticket` as either answers 403. Start Connect as a linked person, get unlinked
  (Access → Unlink) before approving on Twitch: the window shows the "Not allowed" page and nothing
  is connected.
- [ ] W2. After connecting, check whether VillaNET appears in Twitch's Settings > Connections. If it
  does, the Disconnect words stay as written.
- [ ] W3. Go live on Twitch: the card within about two minutes, the badge, the picture (relayed: the
  browser's DevTools shows it from `/plugins/…/twitch/preview`, not from Twitch), the category, and
  **Watch on Twitch ↗**.
- [ ] W4. With **Play Twitch streams inside VillaNET** on: the player plays inline in a panel of about
  464 px. **Check the referrer**: if the player shows an error with `strict-origin`, try `no-referrer`
  and `origin` (`TWITCH_REFERRER_POLICY` in `webapp/src/twitchEmbed.tsx`) and report which works.
- [ ] W5. The sound handed between a Broadcast Box tile and a Twitch tile (one reload of Twitch's
  player each way). Firefox's autoplay setting. Fullscreen in the desktop app (an Allow dialog once).
- [ ] W5a. Optional: whether the player still works with `allow-modals` taken out of its `sandbox`.
  Report it; the build keeps Twitch's own list.
- [ ] W6. A 1000 px-wide window (a panel of at most 400 px): a card click opens the larger player at
  once. A 1300 px window with the panel at about 464 px: the player plays inline.
- [ ] W7. `curl -sI https://<site>/ | grep -iE 'content-security|permissions-policy|x-frame'` on the
  proxy: no `frame-src` or `child-src` of the proxy's own.
- [ ] W8. Simulcast for ten minutes: one card (the Broadcast Box one, with Also on Twitch), the boards
  rise by ten minutes, not twenty, and the Broadcast Box export (Airtime → Export) shows the ten
  minutes.
- [ ] W9. Disconnect: the Twitch-only hours leave the boards on the next poll after the purge, and the
  simulcast minutes stay. Then switch **Show people live on Twitch** off with somebody still connected:
  they still see Disconnect (in the footer's Twitch sheet; on the lock screen when locked; over WATCH
  ONLY when let in), and it works.
- [ ] W10. **Twitch connections** with the switch on and off: Copy, Disconnect, Disconnect and block
  (the blocked person has nothing Twitch in the panel), Unblock, 24/7 (tagged 24/7, no badge, no
  airtime), and Delete all Twitch data (everybody disconnected, block list kept). The airtime card
  says Twitch hours are kept separately while the switch is on.
- [ ] W11. Grep the server log and the proxy's access log after a connect: no token, ID token, ticket
  or nonce in the server log; the proxy's log has the one-time code in the callback's URL, which is
  expected and useless to anybody else (`LOCKDOWN.md`).

**The rest of 3.0.0**

- The Top Snooper's binoculars on a real post, in the popover, on the Trophies card and on a Live Now
  card, for the right person; gone with the Snoopers board switched off.
- The birthday list in the real System Console: folded with more than four people, Show/Hide the
  list, and Add somebody opening it on a new row.
- IGDB: plan a stream as a game and search "Hytale" — it comes from IGDB, says "Data from IGDB", and
  its art loads. Restart Mattermost; the same search and art come back without asking IGDB, and files
  are in `<data>/ceo.villa.villanet/media/`. **Forget remembered titles** clears both.
- The header wordmark in the real header, in the desktop app and a browser, docked and popped out.
- **Banner placement** set to **Top only** matches a Classification Markings banner set the same way.

**The security and optimization pass (3.1.0)**

- With the chat bridge on, `@aLL test`, `@all_ test` and `@al&#108; test` typed in Mumble, and a
  message from a Mumble user named `@all`, notify nobody; each reads as typed; `@victor` still
  notifies Victor from a message, and not from a name.
- Planning a stream still searches and shows thumbnails; the footer's What's on? shows its art before
  going live; a tagged clip's details show the title's art.
- With a 24/7 channel on the air and nothing else changing, the browser's WebSocket frames show no
  stream list every 15 s; joining and leaving Mumble still shows in the voice row within about 2 s.

## Known gaps and follow-ups

- **Mattermost 11.11 only: what was kept.** The retarget (below) left these alone on purpose:
  - the server's own section defaults (`server/defaults.go`): every server VillaNET supports seeds
    them itself, so they change nothing there, but taking them out changes how settings load and
    which tests hold them;
  - the guards around `registerAppBarComponent` and `registerGlobalComponent`, which 11.11's
    `plugins/registry.ts` marks INTERNAL, subject to change without notice; the Root mount stays the
    fallback for the small player and the Clips dialog;
  - the server taking a non-string `props.type` off posts (`claimsClipCard`), which 11.11's webapp
    ignores but older clients did not;
  - the plugin's own error boundary round a shared clip's card, though 11.11 wraps it in one too:
    this one shows the post's message rather than Mattermost's plugin error;
  - the hand-made subscriptions (`useSessionInfo`, `useIdentity`) and the id counters, rather than
    React 18's `useSyncExternalStore` and `useId`: they work on 18 as written;
  - source references such as "9.0 to master" in comments, which say how long a fact has held.
- **The lab after the retarget has been looked at, not used in anger**: React 18.2 on both roots,
  the System Console page on 11.11 and master, and the new search bar. Nothing has run on a real
  Mattermost 11.11 yet; R1 and the rest of the real-host checklist cover it.
- **A narrow popped-out window widens to about 917 px** (found on 5 October, and older than the lock).
  It pushes the let-in footer's **Link your stream** key off screen, and linked panels widen the same
  way. It may be the Up next rail overflow below; that has not been checked.
- **Twitch live, from the 3.1.0 build** (none blocks a release; each is for Victor to judge):
  - In a three-column Live now row (776 px) with the − or + button on it, a Twitch card's uptime tag
    gives way first and reads "5…": Twitch's tag carries the audience count. It is the tag row's
    existing rule (the uptime shrinks first), not a new one.
  - The Connect sheet waits up to five minutes when Twitch never confirms ("fails" in the lab): Twitch
    sends no failure the panel could hear, and the window says what went wrong.
  - In a Mattermost cluster each node asks Twitch and credits Twitch time, as each node polls
    Broadcast Box (contract §15).
- **Share slots**: one linked account can fill a clip's ten share slots; there is no per-sharer limit
  (a design decision).
- **The settings backup** still names stream keys (always-on channels, stream names and notes); the
  console and README now say to keep it private. Leaving those settings out of the file would be a
  server change.
- **After 3.0.0, found by reviewers, not Clips:** the Up next rail overflows sideways in popped-out
  windows (`.vntv-upnext-rail`; `contain: inline-size` is the suggestion, after checking the rail
  does not collapse in narrow layouts). (The other item here, `.vntv-hint.vntv-notice-error` at about
  3.3:1, was fixed with the lock: it now uses `--vntv-red-text`.)
- **What the lock does not cover**, by design: the stream list, faces, schedule, boards and voice
  roster still reach every signed-in browser; a stream already playing in a browser that ignores the
  lock keeps playing until it ends; revoking a token in Broadcast Box unlinks nobody (use Access →
  Unlink); Pop out's keys cannot be taken back. See `LOCKDOWN.md`, "What this does not do".
- **Out of scope for 3.0.0** (spec §7): AV1 and B-frame recording, choosing a simulcast layer, sound
  in Safari, a per-person clip length.
- **Broadcast Box defect** found in the research: after a publisher reconnects, Broadcast Box v2.0.2
  reuses the outgoing audio SSRC with the new publisher's sequence numbers, and receivers drop the
  audio as replays. The recorder reconnects when its sound stops; live viewers may stay silent until
  they retune. Worth reporting to Glimesh/broadcast-box.

## Next

- Phase R of `design/3.4.0.md` (§9.5) is done: nine findings fixed, one dropped ("Review log
  (build)" there, and "The review's fixes (3.4.0)" above). The acceptance audit after it met every
  criterion and found two gaps, both closed ("The last audit's fixes (3.4.0)" above).
- Victor's B0 tasks for 3.4.0 (the Hytale application, the Steam Web API key; "Victor's B0 tasks"
  above). None blocks installing.
- Victor installs 3.4.0 when he says go (it carries 3.3.0 and 3.2.1, never installed), and runs
  3.2.1's checklist (F1, D1-D8), 3.3.0's (P1-P9) and then 3.4.0's (G1-G12), with 3.2.0's ("The 3.2.0
  real-server checklist", Mumble checks V1-V9 first) and 3.0.0's and 3.1.0's ("The real-host
  checklist") as far as they are not done yet.

## The design folder

- `clips-spec.md` (revision 2) and `clips-addendum.md` (wins over the spec; Victor's rules in A§1):
  what Clips was built from, with *As built* notes where the build differs, and the lock's
  amendments (who may *view* clips) marked "Amended 2026-10-05".
- `clips-research.json`: the capture research behind the spec.
- `media-lookups.md`: IGDB, the Twitch application and remembered titles (Parts A–D built; Part E
  superseded by `twitch-live.md`).
- `3.4.0.md` (revision 2): 3.4.0, built: Steam status and Hytale names, with Victor's request, his
  answers of 11 November and his preview picks of 12 November (with the adopted departures) at its
  top, which win over the rest. The build's decisions are in "Current release: 3.4.0" above.
- `3.3.0.md`: 3.3.0, built: the clip player in the stream tile's look (Victor's preview pick A),
  Download in it, and the red failure line. The build's decisions are in "Current release: 3.3.0"
  above.
- `3.2.1.md`: 3.2.1, built, never installed (3.3.0 carries it): clip downloads and no biggest-fan
  opt-out, with Victor's answers of 13 October at its top. The build's decisions are in "3.2.1"
  above.
- `3.2.0.md` (revision 2): 3.2.0, built and installed. Victor's request, his answers and his preview
  picks with the approved departures are at its top and win over the rest, below the amendment for
  3.2.1 (no opt-out); the builders' and integrator's deviations are in the 3.2.0 section above and in
  the code's comments.
- `twitch-live.md`: 3.1.0, amended by the lock; built. The builders' and integrator's deviations are
  in this file ("What 3.1.0 added") and in the code's comments.
- `lock-screen.md` (revision 2): the lock screen, the let-in list and admin unlink, built into 3.0.0,
  with an "As built" section and §12.1's map of the merged panel; the amendment of 6 October at its top
  (the simpler lock, no Who to ask) wins over the rest.
- `rhs-return-spec.md` with `rhs-return-research.json`: what 2.11.0 was built from (rule R20 added
  for the lock: no return and no mini-player for somebody locked).

## Backup folders next to the project

Six are left in `/home/vvilla/Documents/`, and **none is needed to build or install 3.4.0**.
The project is not a git repository, so `pre-3.4.0`, `pre-3.3.0`, `pre-3.2.1` and `pre-3.2.0` below
are the only earlier copies of the main tree.

- `Broadcast Box Plugin.pre-3.4.0`: the tree as 3.3.0 was built, the checkpoint 3.4.0 was built and
  diffed against. Keep it until 3.4.0 has passed its checklist.

- `Broadcast Box Plugin.pre-3.3.0` (about 147 MB, with its `node_modules`, without `dist/`): the tree
  as 3.2.1 was built, the checkpoint 3.3.0 was built and diffed against. Keep it until 3.3.0 has
  passed its checklist.

- `Broadcast Box Plugin.pre-3.2.1` (about 147 MB, with its `node_modules`, without `dist/`): the tree
  as 3.2.0 was installed, the checkpoint 3.2.1 was built and diffed against. Keep it until 3.2.1 has
  passed its checklist.

- `Broadcast Box Plugin.baseline-2.11.0` (3.8 MB): the untouched 2.11.0 tree. Keep it until 3.0.0
  has passed the real-host checklist: it is the 2.11.0 source to diff against or go back to (the
  2.11.0 bundle itself is in `dist/`).
- `Broadcast Box Plugin.pre-3.2.0` (143 MB, with its `node_modules`): the tree as it stood before
  3.2.0 (3.1.0 after the security pass and the simpler lock), the checkpoint 3.2.0 was built and
  diffed against. Keep it until 3.2.0 has passed the real-server checklist.
- `Broadcast Box Plugin.patches` (about 140 MB): `previews-3.2.0/` (the P1-P7 preview code, each with a
  diff against the 3.1.0 webapp, and the montages Victor picked from), `shots-3.2.0/` (the build's
  and the integration's lab montages), `shots-3.2.1/` (3.2.1's lab walk), `previews-3.3.0/` (the clip
  player's looks A, B and C as code, a diff against 3.2.1's webapp, and the montages Victor picked A
  from), `shots-3.3.0/` (3.3.0's lab walk), `previews-3.4.0/` (the P1-P4 preview code, `p1/p2/p3-webapp.diff`
against 3.3.0, and the montages Victor picked from), `research-3.4.0/` (the Steam, Hytale, Mattermost,
Matty and nginx sources the 3.4.0 contract cites), and, from before, the 3.0.0 join notes (`JOIN-NOTES.md`), the recording
  facts for these docs, the wordmark, banner and IGDB patches, all applied, and `tools/shot.mjs`,
  the headless screenshot script the lab is looked at with. Its open items are copied into "Known
  gaps" above.

Deleted: on 5 October, every phase snapshot and sandbox (`ux-console`, `pre-console-merge`,
`pre-mm11`, `base-2026-10-05`, `ux-variant-a`, `-b` and `-c`, `pre-lock-webapp`, `igdb-sandbox`,
`media-sandbox` and the `baseline-3.0.0-…` and `pre-phase…`/`pre-fix…` snapshots); on 6 October, at
Victor's word, the two Twitch checkpoints, `pre-twitch-3.1.0` (the tree just before Twitch live was
built) and `twitch-3.1.0` (the tree just before the security and optimization pass).

## Building on a new machine

- Go 1.27.1 and Node 24 (npm 11) were installed under `~/.local/opt/go` and `~/.local/opt/node`, not on
  the PATH. Before building: `export PATH="$HOME/.local/opt/go/bin:$HOME/.local/opt/node/bin:$PATH"`.
  From 3.2.0 `go.mod` names `toolchain go1.27.2`, so that Go 1.27.1 fetches Go 1.27.2 through the module
  proxy the first time (into `~/go/pkg/mod/golang.org/toolchain@…`) and builds with it; on a machine
  with no internet, put Go 1.27.2 or later on the PATH instead.
- `make check`: `check-server` (Go vet, the tests with -race, gofmt, the version check) and
  `check-webapp` (the React check, TypeScript, the stylesheet rules, the side-panel and Clips rules,
  the date formatting in `time.ts` and the hashtag search's sign-out in `build/check-away.mjs`,
  Twitch's pure rules in `build/check-twitch.mjs`, and from 3.2.0 the awards and plan card in
  `build/check-awards.mjs`, the soundboard in `build/check-sounds.mjs` and @names in
  `build/check-mentions.mjs`). Each half runs on its own too.
- The React check (`build/check-react.py`) wants `webapp/node_modules` to hold exactly the React
  pinned in `webapp/package.json`, which is the React Mattermost ships at `min_server_version`: 18.2.0
  for 11.11 (types 18.2.64 and 18.2.25). A `node_modules` restored from before 5 October has React 17:
  run `npm install` in `webapp` (or `make lab`, which does). Moving `min_server_version` fails the
  check until its row in the script is filled in from Mattermost's `webapp/channels/package.json`.
- `make e2e`, and `make e2e E2EFLAGS=-race`: the real-time recorder tests that `make check` leaves out.
- `make dist-linux`: the small Linux amd64 bundle used for each release. `make dist` builds every
  platform.
- `make lab`: builds `dist/villanet-tv-lab.html`, the panel on fake data. Serve `dist/` with
  `python3 -m http.server 8078 --bind 127.0.0.1` and open `/villanet-tv-lab.html`.
- `make fakebb` (and once, with ffmpeg, `make fakebb-media`): `build/fakebb/fakebb`, the stand-ins for
  Broadcast Box, Steam and Mumble, for running the plugin and its clients on one machine.
  `make standin-linux`: the bundle for that machine only, built to ask the stand-in Steam
  (`dist/standin/`; "Current release: 3.6.0" above says why it can never be a release). The Matty project's
  `tools/dev-server/villanet.sh` runs both and sets everything up.
- `build/make-test-clip.sh` and `build/make-lab-clip.sh` remake the test and lab clips with ffmpeg;
  their output is committed, so builds never need them.
- `webapp/node_modules` (about 65 MB) and the build folders can be skipped in a backup; the first
  build reinstalls them with npm and fetches Go modules, which needs internet.
- The project is not a git repository.

## What to back up

- This project folder. `dist/` holds every release bundle so far (about 290 MB); only the newest is
  needed to install.
- Claude's notes about this project: `~/.claude/projects/-home-vvilla-Documents-Broadcast-Box-Plugin/memory/`.
  The rest of that folder is the conversation history, worth keeping if you want past sessions.
