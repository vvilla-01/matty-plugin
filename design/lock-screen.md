# VillaNET lock screen: build contract (folds into 3.0.0, unreleased)

> **Amended 2026-10-06 (3.1.0): a much simpler lock.** This box wins over everything below it. A section
> below that it changes carries a pointer here, or is marked superseded.
>
> Victor's words, in the order he said them:
>
> 1. "Simplify the locked out screen a lot -- its too busy. Give me a preview before you build." Three
>    variants were built in sandbox copies of the tree and shown to him at 400 and 304 px. He chose
>    **C, "Behind glass"**.
> 2. "Also remove the 'Just Watching?' text. Don't advertise that". Nothing on the lock may tell anybody
>    that they can be let in without a token, or whom to ask.
> 3. "Remove admin lines too". A system admin's lock is everybody's. No "Let yourself in: System Console →
>    … → Access", no red "VillaNET needs attention in the System Console.", no admin alert of any kind,
>    and no set-up words for an admin on the not-set-up face. Admins have all of that in the System
>    Console, and the full admin alerts in the panel once they unlock (`AdminAlerts.tsx`, which stays).
>
> **What is on the screen now** (`LockScreen.tsx`). The banners are as before: at the top, and also at the
> bottom with **Top and bottom**. There is no plate: the padlock says locked, and a visually hidden `<h2>`
> says "VillaNET is locked" ("VillaNET is unlocked" while it opens, "VillaNET is not answering" on No
> signal).
>
> | Face | In the middle | At the foot |
> |---|---|---|
> | Locked | The panel's emblem, large (46% of the panel's width, 128 to 208 px) and dimmed to 55% as if behind glass, with a padlock badge ringed in red across its lower right edge. Under it, **only when there is a reason**, one line: the `lockLine` words of §4.3 ("Unlock VillaNET to watch Dave.", "You are no longer let in." and the rest). Saying why the lock is up is not advertising. | The black token bar, docked where the unlocked panel's footer bar sits, with that bar's height and corners. Inside it: a padlock (the bar's `<label>`, "Bearer token" to a screen reader), the input ("Bearer token to unlock"), an eye (**Show token** / **Hide token**) and an arrow (**Unlock**, red once there is something to send). Over the bar, the status line. Under it, **Disconnect Twitch** on a line of its own, at the right, when the person has Twitch connected. |
> | Locked, saying something | The same. Nothing moves. | The status line over the bar: "Checking your token…", or the refusal in red. After an empty press, and after a token refused as `not_accepted` or `too_long`, the refusal is followed by "OBS calls it Bearer Token, under Settings → Stream." (`lockStatus`). |
> | No signal | The emblem with no badge, "VillaNET is not answering, so it can’t tell whether you’re unlocked.", and **Try again**. | The bar, as on the locked face: a locked person can still unlock. |
> | Not set up | The emblem with its badge, then "VillaNET is not set up yet, so it can’t be unlocked." (`LOCK_NOT_SET_UP`), the same for everybody, system admins included. **Disconnect Twitch** under it when connected. | Nothing: a token could only fail. |
> | Starting | The emblem at 25%, and plain black for the first 250 ms. | Nothing. |
> | Unlocking | Both padlocks swing open and the glass clears; then the stage and the dock fade (§4.6). | The arrow stays where it was, named **Unlocked**, pressing nothing. |
>
> Nothing else, for anybody: no plate, no row heads, no hint under the bar, no line saying whom to ask, no
> admin line.
>
> **The status line keeps its room.** Its place over the bar is two lines high (three in a dock narrower
> than 360 px) whether or not it is saying anything, and while it says nothing it is only black. So
> nothing on the lock jumps when it speaks, the longest of its own words (a refusal with the OBS hint)
> included. The emblem is centred in the space above that room, a little above the panel's middle, which
> reads as centred.
>
> **Removed with the lines:**
>
> - **The Who to ask setting (`AccessContact`), end to end**, since it only ever fed the viewer's line:
>   `plugin.json`; the configuration field, `cleanAccessContact`, `maxAccessContactRunes` and the call in
>   `normalize`; its section default; `/me`'s `accessContact`; the webapp's type; `lockLogic`'s
>   `viewerLineWords` and `cleanContact`; and the lab's `accessContact` and its chip. The Access section
>   now has one setting, **Let in without a token**. A configuration saved before 3.1.0 still holds
>   `accesscontact`, and loads as before: Mattermost's load ignores a key with no field
>   (`LoadPluginConfiguration` at v11.11.1 is a plain `json.Unmarshal`). A settings backup that carries it
>   restores, and the restore lists it under "Ignored, because this version has no such setting" and writes
>   none of it (`TestAConfigurationSavedWithWhoToAskStillLoads`, `TestABackupWithWhoToAskInItStillRestores`).
> - `LOCK_NEEDS_ATTENTION`, the admin's set-up sentence on the not-set-up face, and the stylesheet's
>   `.vntv-lock-alert`, `.vntv-lock-quiet` and `.vntv-lock-line b`.
> - Not removed: the server still sends a system admin's alerts with `/me` in every access state (§3.8),
>   because the panel needs them the moment it unlocks. The lock does not show them.
>
> **Checks.** `build/check-away.mjs` holds every word the lock's logic can say (each reason, each status,
> not set up, No signal) to a pattern that refuses a way in and an admin's words; holds `LockScreen.tsx`'s
> code, comments aside, to the same pattern and to never reading `isAdmin`, `AdminAlerts` or
> `accessContact`; and fails if `accessContact` or "Who to ask" turns up anywhere in the webapp or the lab.
> In the lab a system admin's lock with every alert on is the same as anybody's, pixel for pixel, idle,
> with Twitch connected and not set up.
>
> **A focus fix after Disconnect Twitch**, found by two of the preview's reviewers and already in the build
> before C. Pressed by keyboard, on the lock or in the let-in footer, it left focus on the page's body, so
> the next letter went to Mattermost's message box. The server tells the page over its socket that the
> account has gone before it answers the press, so "me" lost the account first, and the key took its whole
> line out of the page, the words that were to take focus included; the words came back a moment later
> with nothing focused. Now the line stays from the press until its words lapse (`disconnectLineShown` in
> `twitchLogic.ts`), focus goes to the words (`tabIndex -1`), and when the words lapse with focus on them
> it goes to the key if it is still there, or else to the root of the lock or of the panel: never to the
> body. A letter typed on the words goes into the bar on the lock, as from anywhere on the lock, and stops
> there in the let-in footer. Checked in the lab with real key presses in both places, before and after the
> words lapse (the old build and C both sent the letter to the message box), and by `check-twitch.mjs`.
>
> **The same for Try again**, found by the review of this build and as old as Try again itself. Pressed by
> keyboard on the No signal face, the answer that changed the face to Locked took the button out of the
> page with focus on it, and the next letter went to the message box; the panel's quiet "VillaNET could
> not check your access just now." notice, which has the same button, did the same when it went. Now
> `TryAgain.tsx` hands its focus to the root of the lock or of the panel as it goes, only when it still
> has focus then: a layout effect's cleanup, which runs while the button is still in the page. A letter
> typed after that goes into the bar on the lock, and stops on the panel.
>
> **A paste is a paste by Mattermost's rule everywhere.** The words after Disconnect Twitch in the
> let-in footer stopped a stray paste by the letter V alone, as the panel's root, a tile and the Twitch
> sheet already did; a Ctrl+V from a Russian, Greek, Hebrew or Arabic keyboard types another letter, and
> Mattermost, which also goes by the key's code (86), took it to the message box. All four now ask
> `isPasteKey` (`focusTrap.ts`), which the lock's bar already went by. `check-away.mjs` holds Try again to
> its rule and fails on any paste told by the letter outside `focusTrap.ts`.
>
> **Brought in line below:** §0 rows 8 and 10, §1.1's admin alerts row, §3.8 and T13, §4.1–§4.7, §8.1,
> §9.2, §9.6, §10, §11, and questions 13 and 17. Text kept for the record is struck through, or marked
> *Superseded (2026-10-06)*.

Written 2026-10-05 for Victor's request of that day:

> Have the plugin show a locked look/screen with a black input text bar so the user can't interact with
> the plugin at all until they input their bearer token. Have the style match the rest of the plugin.
> Obviously any further bearer token checks to further interact with the plugin will become null/void
> since it is now gate keeped at the very beginning.

"Bearer token" is the publish token that VillaNET's **Link Your Account** box already takes. Broadcast Box
resolves it to the stream key (`server/api.go` `handleLink`, `server/links.go`). OBS shows the same field
as **Bearer Token** on its Stream settings when the service is WHIP (O1 in §2). **So entering it at the
lock screen is linking.** No new kind of credential is created.

His answers to the follow-up questions are binding, and this contract is built on them:

- **System admins are locked in the panel like everyone else.** The System Console keeps working without a
  token.
- **A system admin can let specific people in without a token.** This is a list that admins manage in the
  System Console.
- **People who are let in can only watch.** They may watch streams and see and watch clips: the list, the
  viewer, the cards and the thumbnails. They may NOT make, publish, upvote, report or share clips. They may
  not plan, join plans or use What's on?, and they do not see streamer controls. Linked people keep
  everything they have today.
- **Outside the panel, things stay visible to everyone.** That covers LIVE badges, medals beside names, the
  live information in the profile popover, and shared clip cards in channels. For a locked user, every
  action from those places (Watch, open and so on) opens the panel's lock screen.
- **The "Link Your Account" prompts inside the panel become moot**, because the panel is locked from the
  start. The **server** checks stay, because they are what makes the lock real. Where watching is enough
  they become "linked or let in", and everywhere else they stay "linked".
- **Style:** the same look as the rest of the plugin: black, the skull emblem, Oswald capitals, the plugin's
  red, and a black input bar for the token.

**How this relates to the other contracts**

- **`design/clips-addendum.md` (with `clips-spec.md`).** Owner rule 1 ("the WHOLE Clips feature is for
  LINKED users only") and A§3 change for *viewing* only. Viewing becomes "linked or let in". Every other
  clip route stays linked-only. §9.1 lists each amended passage. On who may *view* clips, this document
  wins over the addendum. On everything else, the addendum stands.
- **`design/twitch-live.md` (3.1.0).** A Twitch connection still is not a link, and it does not unlock the
  panel either. §9.2 gives every Twitch route a class.
- **`design/rhs-return-spec.md`.** The lock screen is the panel's content, so it comes back after a reply
  the same way the panel does. §4.9 covers the one rule it adds.
- **Build order.** This feature is built **after** the panel tidy-up winner has been merged into
  `Sidebar.tsx` and the System Console cleanup has been merged (§12).

**Precedence:** Victor's answers first, then this document, then everything else.

**Revised** the same day after a security and a UX review. The Review log at the end lists each finding, how
it was checked, and what changed.

**Built** into 3.0.0 the same day, in the main tree, on top of the merged panel tidy-up (design A, "One quiet
bar"). The System Console cleanup has not been merged yet; it follows this build. **"As built" at the end**,
before the Review log, lists every place the build differs from this contract and why, and §12.1 records the
mapping of the merged tree. Short *As built* notes also sit where a section was changed. Nothing in 3.0.0 has
been installed on the real server; every check was made in the tests and the lab.

**Notation**

- `file:line` is the main tree as it stood on 2026-10-05, before the tidy-up merge. Lines in `Sidebar.tsx`
  will move, so builders find each place by the symbol named beside it.
- `[MM v9.3.0 …]`, `[MM v10.11.0 …]` and `[MM master …]` cite Mattermost source, saved under
  `/tmp/claude-1000/villanet-mm/`. **That folder can vanish**, so every fact a builder needs is restated in §2.
- `[BB …]` cites Glimesh/broadcast-box at `main`, and `[OBS …]` cites obsproject/obs-studio at `master`.

---

## 0. Decisions in one screen

| # | Question | Decision | Section |
|---|---|---|---|
| 1 | What does "unlocked" mean? | One of two things. **Linked**: the person proved a stream with their own bearer token, which is today's link. **Let in**: a system admin put them on the list, which allows **watching only**. Everyone else is **locked**, including system admins, people matched to a stream only by username, and people with only a Twitch connection. | §1 |
| 2 | Where is it enforced? | On the server, in two layers. **Layer 1** is a new front gate in `ServeHTTP`. Every route has a class (open, watch, watch-or-admin, linked or admin), and a route nobody has classified counts as linked. The clip family's own gate gets the same classes. **Layer 2** is the existing per-route "linked" checks, which stay. The gate runs before every meter. | §3.5, §3.6 |
| 3 | What does a refusal look like? | 403 `{"error":…, "code":"locked"}` for a locked caller, 403 `code:"watch_only"` for a let-in caller on a route that needs a link, and 503 `code:"store"` when the store does not answer. The server stops sending `not_linked`. | §3.4 |
| 4 | Where is the let-in list kept? | In one KV document, `let_in`, holding at most 500 people. It is written only by compare-and-set. Each node keeps a copy for at most 10 s, and a failed read is never covered by an older copy. If the document will not parse, or comes from a newer version, it **fails closed**: let-in people see the lock, and linked people are unaffected. | §3.2 |
| 5 | How do admins manage it? | Through an **Access** card in the System Console. Admins add by username and remove by row, and can also **unlink** somebody (Q2). The card uses the `/api/v1/admin/let-in` and `/api/v1/admin/unlink` routes, which check `isSystemAdmin` only. | §3.3, §8 |
| 6 | What does `/me` say? | A new `access` field: `"linked"`, `"letIn"`, `"locked"` or `"unknown"`. It never lists other people. `publicSiteUrl` is removed, and `media` is sent only to linked people. | §3.8 |
| 7 | How fast does a change reach the person? | A content-free `custom_ceo.villa.villanet_access` WebSocket event goes to that one person whenever they link, unlink, have their key taken, are let in or out, or are unlinked by an admin. Their webapp reads `/me` again. On the node that made the change the server applies it at once; on other nodes it takes at most 20 s. Because the event can be missed and nothing can end a playing session from outside, the webapp also reads `/me` after every WebSocket reconnect and every 60 s while anything is playing. | §3.9 |
| 8 | What is the lock screen? | It **replaces** the panel body rather than covering it. *Amended 2026-10-06 (the box at the top):* it shows the emblem, dimmed, with a padlock badge; a line under it only when there is a reason; and the black token bar docked at the foot, with a status line over it and Disconnect Twitch under it for somebody connected. Nothing says that anybody can be let in, and nothing is for admins only. ~~It shows the emblem, a LOCKED plate (the heading), a line only when there is more to say, and two rows of equal weight: **Stream here?** with a black **BEARER TOKEN** bar, Show and an UNLOCK pill, and **Just watching?**, which says whom to ask to be let in.~~ Nothing of the panel mounts behind it: no snapshot poll, no tiles, no WHEP, no stills and no clip requests. | §4 |
| 9 | Focus and keys | The bar never takes focus by itself when the panel opens automatically, and a locked person gets no return after a reply. When the person opened the panel themselves (not on a touch screen), the bar is focused once, by the same focus guard `away.ts` uses. A printable key or a paste pressed anywhere on the lock goes into the bar, never to Mattermost's message box, and a post shaped like a bearer token is refused before it is sent. | §4.5 |
| 10 | Unlocking | The padlocks open (*2026-10-06:* the badge's and the bar's; the glass over the emblem clears; there is no plate to read UNLOCKED). The lock fades, and the panel mounts with its usual boot sweep. With reduced motion the swap is instant and only the screen reader hears "Unlocked." | §4.6 |
| 11 | Watch only | A let-in person gets the panel without planning, Join, What's on?, the title bar, streamer controls, Pop out or taking preview stills. Wherever the merged panel puts its link entry point, they get one quiet **WATCH ONLY** line with a small **Link your stream** key that opens the same black bar. In Clips they can list and watch; upvote, report, share, delete, drafts and the scissors are absent rather than prompting. | §5 |
| 12 | In-panel link prompts | Removed: the footer Link Your Account CTA, the Clips locked view, the scissors link row, the card link row, the Clippers locked CTA, the link tooltip, and the `not_linked` copy. | §6 |
| 13 | Outside the panel | Everything stays visible. For a locked user, Watch in the popover, Watch or the picture on a card, and a clip link each open the lock screen and remember what was asked for. A locked user never sees the mini-player. | §7 |
| 14 | Settings backup | The let-in list is **left out**, as links are. | §8.3 |
| 15 | Pop out | `watchUrl` (which contains a stream key) leaves the broadcast snapshot. Pop out becomes a redirect route for **linked** people only, because the key it hands out cannot be taken back by a let-out or an unlink (Q10). | §3.10 |
| 16 | Words | The user-facing word is **bearer token** everywhere, because it is Victor's word and OBS's label (Q12). "Guest" is never used, because it is a Mattermost account type. | §4.3 |
| 17 | When the store does not answer | `unknown` never tears anything down. A page that last knew the person as linked or let in keeps the panel and says it could not check. Only a definite `locked` locks an open panel. A page that never had an answer shows the lock's NO SIGNAL face, not LOCKED. | §4.1, §4.9 |
| 18 | What counts as linked | Both link entries must agree: `link_user_<u>` names K **and** `link_key_<K>` names u. A failed or racing write in `setLink` therefore leaves nobody linked by mistake, and `setLink` no longer swallows read errors. | §3.1, §3.8 |

---

## 1. The rule and the three kinds of user

**The rule:** nobody uses the VillaNET panel until they unlock it with their own bearer token, or until a
system admin has let them in to watch. The server enforces this on every route.

| Kind | How somebody becomes it | `/me` `access` |
|---|---|---|
| **Linked** (everything) | They send `POST /api/v1/me/link` with a token that Broadcast Box accepts, from the lock screen, from the let-in footer, or from the linked footer when relinking. The link is `link_user_<uid>` (`links.go:17-21`), as today, and it counts only while the reverse entry `link_key_<key>` names the same person (§3.1). | `"linked"` |
| **Let in** (watch only) | A system admin adds them in System Console → VillaNET → Access, and they are not linked. A linked person who is also on the list counts as linked. | `"letIn"` |
| **Locked** | Everyone else. That includes system admins with no link who are not on the list, a person whose stream is matched only by username (`linkSourceUsername`, `poller.go:455-470`), anyone with only a Twitch connection (3.1.0), and bots and personal-token callers with no link. | `"locked"` |
| *(not known)* | The store did not answer when `/me` asked. The server never guesses; route refusals are 503. | `"unknown"`. The webapp keeps the last definite answer it had in this page. With none, it shows the lock in its NO SIGNAL face, with Try again (§4.1). |

### 1.1 Who may do what

"Server" means the route stays callable but nothing in the webapp offers it (§3.5, D3). Admin rows apply on
top of the column the admin is in.

| Capability | Locked | Let in | Linked |
|---|---|---|---|
| Open the panel | Lock screen only | Yes | Yes |
| Unlock or link with a bearer token | Yes, on the lock screen | Yes, with the footer's **Link your stream** | Relink or unlink in the footer, as today |
| Live now, Up next (read), voice strip, birthdays and notices in the panel | No | Yes | Yes |
| Watch a stream in a tile (WHEP), the mini-player, the panel in a popout window | No | Yes | Yes |
| Live preview pictures (`GET /stills`) and 24/7 channel pictures | No | Yes | Yes |
| Take and share preview stills (claim, upload, peeks) | No | **No** (Q6). A still is shown to everybody on the Live now card, which is publishing, not watching. | Yes |
| Pop out to the public watch page (only when the admin has set one) | No | **No** (Q10). It hands out a stream key that a let-out cannot take back. | Yes |
| Hashtag search from a tile's title | No | Yes | Yes |
| Shown as a face watching a stream | No | Yes | Yes |
| Counted on Top Snoopers | No | **No** (Q5) | Yes, as today |
| Trophies card: boards, medals, Clippers tab with Watch | No | Yes | Yes |
| Clips: list, open, watch, shared cards with thumbnails | No | **Yes** | Yes |
| Clips: the scissors, the C key, drafts, Just ended, trim, publish | No | No | Yes, as today |
| Clips: upvote, report, share | No | No | Yes |
| Clips: delete their own, delete as the streamer, delete as an admin from the panel | No | No (admins use the System Console) | Yes, as today |
| Streamer controls: the clipping switch and wipe | No | No | Yes |
| The username-matched streamer notice | No | A one-line notice with no switch (Q8) | — |
| Plan, change a plan, join a plan, look up titles | No | No | Yes |
| Cancel their own plan, leave a plan, clear What's on? (takebacks) | Server only | Yes, for their own | Yes |
| A system admin cancels anybody's plan | Server only | Yes (Q4) | Yes |
| What's on? and the stream title (profile) | No | No | Yes |
| Twitch Connect (3.1.0) | No | **No** (Q7). Connecting makes them a streamer shown to everybody. | Yes |
| Twitch Disconnect (3.1.0) | Yes, from the lock screen | Yes | Yes |
| **Outside the panel:** LIVE badge, medals and badges beside names, the popover's live row and art, shared clip cards drawn from the post | Visible | Visible | Visible |
| **Outside the panel:** popover Watch, a card's Watch and picture, a clip link | Opens the lock screen | As today | As today |
| The card's thumbnail and server-side card details | No (drawn from the post only, as unlinked readers get today) | Yes | Yes |
| System Console, every VillaNET card (system admins) | Yes | Yes | Yes |
| The chat bridge: a post in the bridged channel is relayed to Mumble (`bridge.go:282-290`) | Unchanged (Q16). It is the channel's chat, governed by Mattermost's own channel permissions, not the panel. | Unchanged | Unchanged |
| Admin alerts: airtime, voice, birthdays, clips, let-in list (system admins) | ~~On the lock screen (Q13)~~ Not on the lock screen (2026-10-06: "Remove admin lines too"); the System Console as always | In the panel | In the panel |

---

## 2. Facts this rests on

These were checked on 2026-10-05.

| Id | Fact | Source |
|---|---|---|
| M1 | `PublishWebSocketEvent(event, payload, broadcast)` has existed since 5.2. The event arrives as `custom_<pluginid>_<event>`. `WebsocketBroadcast.UserId` sends it to one user. | [MM v9.3.0 server/public/plugin/api.go:941-947]; `websocket_message.go` [v9.3.0 :97] [v10.11.0 :125] [master :151] |
| M2 | Mattermost deletes any client-supplied `Mattermost-User-Id`, then sets it from the session. A **GET** authenticated by cookie gets the user id **without a CSRF check**. A non-GET by cookie needs `X-CSRF-Token`. | [MM v9.3.0 server/channels/app/plugin_requests.go:136-210]; [v10.11.0 :173, :227]; [master :194, :283] |
| M3 | `GetUserByUsername` has existed since 5.2 and returns an AppError with status **404** when the user is not found and 500 otherwise. Usernames are stored lower-cased (`NormalizeUsername` = `strings.ToLower`, applied on save). | [MM v9.3.0 server/public/plugin/api.go:150-154]; [v9.3.0 server/channels/app/user.go:416-428]; [v9.3.0 server/public/model/user.go:414-416, :443] |
| M4 | **`GetUsersByIds` is not in the v9.3.0 plugin API.** It first appears at v10.11.0 (`api.go:139-143`). With `min_server_version` 9.3.0, the list is named with one `GetUser` per person, behind a cache. | [MM v9.3.0 api.go] (absent); [MM v10.11.0 api.go:139-143] |
| M5 | `KVCompareAndSet` (5.12) and `KVSetWithOptions` (5.20) exist at 9.3.0. | [MM v9.3.0 api.go:892, :909] |
| M6 | `PublishPluginClusterEvent` (5.36) and the `OnPluginClusterEvent` hook exist at 9.3.0. VillaNET uses neither today, and this design does not start (§3.9). | [MM v9.3.0 api.go:1142-1150]; [v9.3.0 hooks.go:38, :248-255] |
| M7 | When the RHS opens, master and v10.11.0 focus the first focusable element in `#rhsContainer`, unless a post textbox is in the RHS. That takes **two timer hops**: `handleRHSFocus` waits a `setTimeout(…, 0)` and calls `focusElement`, which dispatches its focus event inside a second `setTimeout(…, 0)`; the a11y controller then focuses the target. `#rhsContainer` puts the title header before the plugin body, so **`autoFocus` on mount is undone** by the header's first button, and no fixed timer of the plugin's can be relied on to run after it (the lesson `away.ts:566-572` already records). v9.3.0 has no `handleRHSFocus`. | [MM master webapp/channels/src/components/sidebar_right/sidebar_right.tsx:141-178, utils/a11y_utils.ts:46-60, utils/a11y_controller.ts:909-925]; [v10.11.0 :144-176] |
| M8 | `shouldFocusMainTextbox` leaves keys alone when focus is in an INPUT or TEXTAREA. It sends a printable key, or a **paste (Ctrl/Cmd+V) from any other element, a BUTTON included**, to the message box: a document `keydown` listener focuses the box during the keydown, so the character or the paste lands there. Space on a BUTTON or A is spared. React 17 handles events at Mattermost's root, so `stopPropagation` in a VillaNET handler keeps a key from both Mattermost's React handlers and the document. The same move works for VillaNET: focusing the bar inside the keydown makes the paste land in the bar. | [MM v9.3.0 post_utils.ts:224-262]; [MM master post_utils.ts:246-264, components/advanced_text_editor/use_textbox_focus.tsx:70-104]; the existing panel comment at `Sidebar.tsx:1116-1124` |
| M9 | `registerAdminConsoleCustomSetting` finds a component by the **lower-cased** manifest key. | House use and comment at `webapp/src/index.tsx:355-357` |
| M10 | `registerReconnectHandler` and `registerMessageWillBePostedHook` exist at v9.3.0. The hook runs before a post is sent and can refuse it by returning `{error: {message}}`. Mattermost does not promise to replay a plugin WebSocket event that was sent while the socket was down. | [MM v9.3.0 webapp/channels/src/plugins/registry.ts:733, :743-754]; [master :965, :994] |
| M11 | **Synced drafts are on by default** (`AllowSyncedDrafts` defaults to true), so text that lands in the message box is saved on the server as a draft before anybody presses Enter. | [MM v9.3.0 server/public/model/config.go:398, :883-884] |
| M12 | Mattermost's viewport is `width=device-width, initial-scale=1.0`, with no `maximum-scale`, and its mobile stylesheet sets `select, textarea, input { font-size: 16px }` for WebKit so that iOS Safari does not zoom into a focused field. | [MM v9.3.0 and master webapp/channels/src/root.html:6]; [sass/responsive/_mobile.scss, master :2021-2026, v9.3.0 :2299-2304] |
| BB1 | Broadcast Box `GET /api/whip/profile` answers **204 "Profile not found"** for any token it cannot resolve, and 200 with JSON otherwise. So a wrong token is reported as 403 `not_accepted`, which is today's mapping (`api.go:564-568`). **The version deployed on Victor's server was not checked** (real-host check R4). | [BB main internal/server/handlers/whip/profile.go:19-37] |
| BB2 | Broadcast Box can reset a profile's token (`ResetProfileToken`). VillaNET never checks a stored link again (`clearLink` is only called from `handleLink`, `api.go:524`, and from `setLink`, `links.go:102`), so a reset in Broadcast Box does **not** lock anybody out of VillaNET. That is why an admin unlink exists (Q2). | [BB main internal/server/authorization/stream_profile.go:222] |
| BB3 | Broadcast Box's `/api/whep` accepts only POST and PATCH, so nothing, the plugin included, can end a playing WHEP session from outside. Profile tokens are `uuid.New().String()`. | [BB main internal/server/handlers/whep.go:20]; [internal/server/authorization/helpers.go:102-103] |
| O1 | OBS labels the WHIP key field "Bearer Token". | [OBS master frontend/settings/OBSBasicSettings_Stream.cpp:433]; [frontend/data/locale/en-US.ini:237] |

These repository facts are the ones the design leans on. The full map was made read-only on 2026-10-05.

- **Anyone signed in can watch today.** `POST /api/v1/whep` (`api.go:317-326`, `:680-759`) checks no link.
- **`GET /api/v1/stills` (`stills.go:900`) is open today.** It is also the one panel route the System Console
  uses (`AdminStills.tsx:178`, through `stillURL`). Every other console component calls only `/api/v1/admin/*`.
- **`watchUrl`** (`plugin.go:151-153`, filled at `poller.go:386-388`) is in the snapshot that is broadcast to
  everyone, and it contains a stream key. The webapp uses it only for Pop out (`Player.tsx:319, :512`), and
  it never reads `me.publicSiteUrl`.
- **Takeover.** `setLink` takes a proven key from its previous owner (`links.go:115-126`), and nobody tells
  them.
- **Tests.** Only the unlink path of `handleLink` is tested (`nowplaying_test.go:237`).
- **`getLinkedUser` and `getLinkedKey` swallow store errors** (`links.go:60-64, 87-94`), and `setLink` uses
  both (`:108, :115`). A failed read today skips the takeover and leaves the previous owner linked (§3.8).
- **`handleLink` already strips one leading "Bearer "** from what it is sent (`cleanStreamKey`, `api.go:521`,
  `links.go:41-48`), so a token pasted from WHIP documentation with its prefix works.
- **`writeImage` already sends an ETag and answers 304** to a matching `If-None-Match` (`brand.go:135-160`).
- **The plugin cannot end a playing session.** It keeps Broadcast Box's session address from the browser
  (`api.go:676-678`), and Broadcast Box has no DELETE (BB3).
- **The chat bridge** relays every non-bot post in the bridged channel to Mumble (`bridge.go:282-290`).

---

## 3. Server

### 3.1 The access helper (`server/access.go`, new)

```go
// access is what somebody may do in the panel. Decided on the server for every
// request, from the link and the let-in list; the webapp only follows it.
type access int

const (
	accessLocked access = iota // neither linked nor let in: the lock screen
	accessLetIn                // let in by a system admin: watch only
	accessLinked               // linked their own stream with their bearer token
)

// String is the word /me uses: "locked", "letIn", "linked".
func (a access) String() string

// accessOf reads both link entries first, fresh from the store: somebody is
// linked only when link_user_<them> names a key whose link_key_ names them
// back, so a write that failed or raced half way leaves nobody linked. Only
// for somebody not linked does it look at the let-in list (this node's copy,
// §3.2). err != nil
// means the store did not answer: the caller is told 503 "store", never
// "locked" and never "let in", so a database blip neither shuts out a linked
// person nor opens the door to anybody.
func (p *Plugin) accessOf(userID string) (level access, key string, err error)

// accessSeenOf is accessOf with a ten-second memory of yes-answers (linked or
// let in) per person, for the routes one playback or one channel of cards calls
// many times. A failed read is never remembered, so nobody is shut out by a
// blip. A no is remembered only by the gate, for two seconds, on the node
// that refused; accessChanged there drops it at once.
func (p *Plugin) accessSeenOf(userID string) (level access, key string, err error)

// accessChanged is told whenever somebody's access may have changed (§3.9).
func (p *Plugin) accessChanged(userID string)
```

**Rules**

- **"Linked" means both link entries agree.** `linkedKey(u)` (`links.go:69`) returns K, and
  `link_key_<normalizeKey(K)>` names u. Either read failing is an error, never a no. Whoever wrote the reverse
  entry last is the only one linked, and a forward entry left behind by a failed cleanup reads as locked. No
  migration is needed, because `setLink` has always written both entries (`links.go:127-141` in 2.11.0 and
  today). A username match, a Twitch connection and being a system admin never count. T7 holds this.
- **The "seen" cache replaces the clip store's `linkSeen`** (`clips_access.go`, `requireClipsLinkSeen`).
  - Each entry is `{level, key, at}`, kept for `accessSeenTTL = 10 * time.Second`.
  - The cache is bounded at `maxClipPeople` (5000), with the same pruning.
  - Entries are guarded by an epoch the way `noteLinkSeen` is, so an answer still on its way from before a
    change is not kept.
  - `forgetLink` and `accessChanged` both drop the entry and move the epoch.
- **The gate remembers a refusal for 2 s** (`accessNoTTL = 2 * time.Second`), so that a refused caller cannot
  drive a store read per request (§3.5).
  - The entry is `{level, notAdmin, at}`, under the same epoch and bound as the seen cache. `notAdmin` is
    recorded only when a watchOrAdmin route asked the admin question and the answer was no.
  - Within the window, a request that the remembered level would also fail is refused from memory, with no
    read. A request it would pass (a let-in person on a watch route, or an admin on watchOrAdmin when
    `notAdmin` was never recorded) reads as usual.
  - `accessChanged` and `forgetLink` drop it at once on the node that changed. On another node a remembered
    refusal can delay an unlock by at most 2 s, and only for somebody who was making refused requests. The
    lock screen itself makes none (`/me` and `/me/link` are open).
  - A store error is never remembered.
- **Best Clipper still learns from the link only.** The fresh path of the clip gate keeps calling
  `s.noteLinked(userID, key != "", now, epoch)` with the **link** answer, never the access answer
  (`clips_access.go:61`). A let-in person is "not linked" for votes, exactly as an unlinked one is today.

### 3.2 The let-in list (`server/letin.go`, new)

**Storage:** the KV key is `let_in`. No existing key or prefix collides with it (`plugin.go:108-125`,
`stills_admin.go:49`).

```json
{"version":1,"people":{"<26-char user id>":{"at":1759680000000,"by":"<admin user id>"}}}
```

```go
const (
	kvLetIn              = "let_in"
	letInVersion         = 1
	maxLetIn             = 500              // a bound somebody chose; far above this server's people
	letInMaxAge          = 10 * time.Second // a node's copy is read again after this
	maxLetInRequestBytes = 4096
)

type letInDoc struct {
	Version int                   `json:"version"`
	People  map[string]letInEntry `json:"people"`
}

type letInEntry struct {
	At int64  `json:"at"` // milliseconds since the epoch
	By string `json:"by"` // the system admin who let them in
}
```

**Reading**

| What is stored | What it means | Writes | Admin alert (`me.letInAlert`) |
|---|---|---|---|
| Nothing | Nobody is let in | Allowed. Created with a nil old value, so only if still absent. | — |
| Version 1 that parses | The list | Allowed | — |
| Will not parse, or `version < 1`, or `people` missing | Quarantined with `p.setAside(kvLetIn, raw, …)` (`community.go:421`), then read as an empty list. Let-in people see the lock; linked people are unaffected. | Allowed. They start a fresh list. | "The let-in list could not be read and was set aside. Nobody is let in until people are added again." |
| `version > 1` | Read as an empty list (fail closed). **Not** set aside, so a downgrade followed by an upgrade keeps it. | Refused: 503 `newer` | "The let-in list was saved by a newer VillaNET. Nobody is let in until that version is back." |
| The read fails | `accessOf` returns an error (503 `store`) for somebody **not linked**. An older copy is kept but **not used** (fail closed), and the next request reads again. A copy younger than `letInMaxAge` is used without any read, so this happens only once a copy is due. Linked people never reach this read. | — | — |

**Node copy**

- `p.letIn` holds `{people map[string]struct{}, state, readAt, gen}` under its own lock.
- `letInHas(userID, now)` reads the store again only when `now - readAt >= letInMaxAge`.
- **A generation counter orders refreshes and writes.** A refresh notes `gen` before it reads, and installs
  what it read only if `gen` is unchanged when it finishes. A write that succeeds bumps `gen` and installs
  what it wrote, at once. So a refresh that began before a write and finishes after it is thrown away,
  instead of putting the old list back on the writing node for up to 10 s.
- There is no stale-copy fallback. With it, a let-out could stay let in on other nodes for a minute while the
  store failed; without it, the bound in §3.9 (20 s) holds whatever the store does.
- The clock is injected (`p.letIn.now`), as the clip store's is, so tests never sleep.

**Writing**

- Every write goes through `p.casUpdate(kvLetIn, apply)` (`community.go:228-271`). It compares exact bytes,
  creates the document only if it is absent, makes 8 attempts, and otherwise returns `errTooBusy`.
- `apply` re-checks the bound and whether the person is already there on every attempt.

**Bound:** at most `maxLetIn` = 500 people. Adding a person who is already on the list does not count
against it.

### 3.3 Admin routes

These are copied from the shape of `/admin/stills/block` (`stills_admin.go:660-743`):

- `isSystemAdmin` is checked **first**, before anything in the request is read.
- Every answer sets `Cache-Control: no-store`.
- The routes have no meter (admin only, like the other console routes).
- Every error is `{"error": "<text>", "code": "<code>"}`.

| Method + path | Request | Success | Errors (status, code, text) |
|---|---|---|---|
| `GET /api/v1/admin/let-in` | — | 200 `{"people":[{"userId","username","name","at","byUsername","state"}],"count":n,"max":500,"problem":"","readOnly":false}`. `state` is `"active"`, `"deactivated"` or `"gone"` (GetUser 404). `name` is the display name, or `""`. People are sorted by username without regard to case, with `gone` last. `problem` is the alert text from §3.2, and `readOnly` is true for `newer`. | 403 `forbidden` "That is not allowed."; 503 `store` "The let-in list could not be read. Try again in a moment." |
| `POST /api/v1/admin/let-in` | `{"username":"alice"}`, capped at 4096 bytes. It is trimmed, one leading `@` is stripped, and it is lower-cased (M3). | 200 `{"ok":true,"added":true\|false,"linked":true\|false}`. `added:false` means they were already on the list. `linked` reads their link once, so the card can say it only matters if they unlink. | 403 `forbidden`; 400 `bad_request` "Could not read the request."; 400 `bad_request` "Say who to let in."; 404 `no_user` "There is no such user."; 400 `bot` "Bots cannot be let in."; 400 `deactivated` "That account is deactivated."; 409 `list_full` "The list is full (500 people). Take somebody off it first."; 503 `newer` "This list was saved by a newer VillaNET, so it cannot be changed here."; 503 `busy` "Too many changes at once. Try again in a moment."; 503 `store` "The list could not be saved, so nothing has changed. Try again in a moment." (also when GetUser fails with anything other than 404) |
| `DELETE /api/v1/admin/let-in?userId=<26 chars>` | By **id**, because the row may belong to a renamed, deactivated or deleted account. The id is cleaned like `cleanClipUserID` (`clips_api.go:1032`). | 200 `{"ok":true,"removed":true\|false}`. Taking off somebody who is not on the list is still `ok`. | 403; 400 `bad_request` "Say who to take off the list."; 503 `newer`, `busy` or `store` (store text: "That could not be saved, so they are still let in. Try again in a moment.") |
| `POST /api/v1/admin/unlink` (Q2) | `{"username":"alice"}`, cleaned as above | 200 `{"ok":true,"wasLinked":true\|false,"letIn":true\|false}`. `letIn` says whether they are still on the let-in list, so the card can say they can still watch (§8.1). It runs the unlink path of `handleLink` for that person: `clearLink`, then `clearNowPlaying` (best effort), `refreshSoon`, `clipsLinkChanged` and `accessChanged`. | 403; 400; 404 `no_user`; 503 `store` "Could not unlink that stream. Try again in a moment." |

**Logging.** Each change is logged at Info, only when something actually changed:

- "A system admin let somebody in to watch", with `user_id` (the admin) and `let_in_user_id`
- "A system admin took somebody off the let-in list", with `user_id` and `let_out_user_id`
- "A system admin unlinked somebody's stream", with `user_id` and `unlinked_user_id`

Nothing else is logged: no username, no key and no token.

**Names.** `GetUser` is called once per person, with a 10-minute cache (M4). The clip store's people cache
(`clipFacts`, `clips_access.go`) has exactly these fields and is reused. A failed lookup shows the row with
the id and `state:"active"`, never as `gone`.

**Afterwards.** Every successful add, remove or unlink calls `accessChanged(target)` (§3.9).

### 3.4 Refusals: codes and texts

`errorResponse` (`api.go:17-19`) gains `Code string `json:"code,omitempty"``, and
`writeCodedError(w, status, text, code)` is added beside `writeError`. Existing callers do not change.

| Code | Status | Fixed text | When |
|---|---|---|---|
| `locked` | 403 | "VillaNET is locked. Unlock it with your bearer token." | A locked caller on any route that is not open. This includes clip routes, where it replaces `not_linked`. |
| `watch_only` | 403 | "You are let in to watch only. Link your stream to do that." | A let-in caller on a route that needs a link |
| `store` | 503 | "VillaNET could not check your access just now. Try again in a moment." | `accessOf` failed. The clip family keeps its own `store` text (`clipErrorTexts`). |

**Clips.** `clipErrLocked` and `clipErrWatchOnly` are added to `clips.go:489-520` with the texts above. The
server no longer sends `clipErrNotLinked`. The webapp still treats `not_linked` exactly like `locked`, as a
belt for a tab that was loaded before the upgrade.

**Layer 2.** `requireLinkedStream` (`community_api.go:112`) keeps each refusal text (for example
`searchNeedsLink`). It reads access instead of only the link, and adds `code:"watch_only"` for a let-in
caller and `code:"locked"` for a locked one. Behind layer 1 this happens only in a race, or on the takeback
routes' "join" branch (§3.5).

### 3.5 Layer 1: the front gate in `ServeHTTP`

**Order** (`api.go:121` on):

1. `/static/` needs no session (unchanged).
2. *(3.1.0: the Twitch HTML routes, §9.2.)*
3. 401 when there is no `Mattermost-User-Id` (unchanged).
4. The clip family goes to `serveClips`, and admin clips to `serveAdminClips` (unchanged dispatch; §3.6
   changes the clip gate). **These, `/static/` and the 3.1.0 Twitch HTML routes are the only dispatches
   allowed above layer 1**, because each runs an access check of its own. A comment at the top of `ServeHTTP`
   says that nothing may be dispatched above the gate without its own access check, and T1 fails on any
   other prefix dispatch there.
5. **Admin paths** (`/api/v1/admin/` and anything under it) **skip layer 1**. Each handler checks
   `isSystemAdmin` first, as today. This is what keeps the System Console working with no token.
6. **Everything else:** `class := routeClassOf(r)` (path and method, plus the one header in the WHEP row
   below).
   - A caller refused in the last 2 s whose remembered level fails this class is refused again from memory,
     with no read (§3.1).
   - **open:** no read; go to the switch.
   - **watch:** `accessSeenOf` for GET, and `accessOf` for every other method. Linked and let-in callers pass.
     A locked caller gets 403 `locked`.
   - **watchOrAdmin:** as watch. A caller refused as locked passes anyway when `isSystemAdmin` is true. That
     extra question is asked only for a locked caller.
   - **linked:** `accessOf`. Linked callers pass. A let-in caller gets 403 `watch_only` and a locked caller
     gets 403 `locked`. For a path that is in no class at all (a true unknown, which the switch answers 404),
     `accessSeenOf` is used instead, so a linked caller sending made-up paths costs one read per 10 s.
   - A store error gives 503 `store`.
7. The existing switch runs, with its method checks, then the meters, then the handlers.

**Why the gate comes before everything else.** It runs before every meter. Today several meters are spent
before the link check (`api.go:192, 210, 297, 308, 322`). Those meters are still spent, after the gate, for
callers who pass. A refused caller therefore never spends a token they would need once unlocked. This is the
house rule from the media search (`api.go:229-236`) and the clip gate (A§3.2).

**What it costs.** Today `POST /whep` spends `playbackLimiter` before any store read, and an unknown path is a
404 from memory (`api.go:316-326`). With the gate first, a refused caller's request would cost a store read
with nothing metering it. The 2 s refusal memory (§3.1) bounds that at one read per 2 s per node for each
refused person, whatever they send, and the unknown-path rule above bounds a linked person's made-up paths.
Nobody who is let through spends anything. T2 asserts both.

**Route classes.** `routeClasses` is a `map[string]map[string]routeClass` keyed by path, then method. **A
route that is not in it, or a method not listed for it, is `linked`.** So a route added later is refused to
locked and let-in people until somebody classifies it on purpose, and an unknown path from a locked caller
gets 403, not 404.

| Path | Method | Class | Notes |
|---|---|---|---|
| `/api/v1/streams` | GET | open | Badges, medals and the popover need it (`PopoverLive.tsx:43-53`). `watchUrl` is removed from it (§3.10). |
| `/api/v1/me` | GET | open | §3.8 |
| `/api/v1/me/link` | POST | open | The lock screen's door. Its method check and `linkLimiter` still come first (`api.go:162-171`). |
| `/api/v1/me/profile` | GET, POST | linked | The stream title is a streamer control. |
| `/api/v1/nowplaying` | GET, POST | linked | What's on? |
| `/api/v1/nowplaying` | DELETE | open | A takeback (D3): it clears only the caller's own entry. |
| `/api/v1/schedule` | POST | linked | Plan |
| `/api/v1/schedule/edit` | POST | linked | Change a plan |
| `/api/v1/schedule/join` | POST | open | One route for join and leave, decided by the body. **Join** still needs a link in the handler (`community_api.go:282`, `requireLinkedStream` with codes, §3.4). **Leave** is a takeback. |
| `/api/v1/schedule/remove` | POST | open | A takeback. The handler allows the caller's own plan, or any plan for an admin (`:198`). |
| `/api/v1/media/search` | GET | linked | Title search for planning. *Amended 3.1.0 (security pass): after the gate, and before the link check and `searchLimiter`, a request without `X-Requested-With: XMLHttpRequest` is answered 403 "Search from the panel." (`madeByScript`, `media_api.go`; the check in `api.go`). The panel sends that header on every request (`client.ts`, `baseHeaders`); a Markdown image in a post, which every reader's browser fetches as a GET with their session and no CSRF check, cannot. Without it, such an image searched TMDB, Steam or IGDB in each linked reader's name, spent their searches and the shared budget, and made every title it listed `refShownHere`. `TestAnImageInAPostCannotSearchForTitles`.* |
| `/api/v1/media/image` | GET | open | The popover shows now-playing art (`PopoverLive.tsx:283, 304`). ~~The existing fetch rule (`refShownHere` or a link, `media_api.go:176-186`) stays as it is.~~ *As built: "a link" now means linked by §0 row 18 (both entries agree, `hasLink`), so a locked caller with a leftover forward entry cannot have a provider asked about an unshown title (T7, `media_test.go`).* **Superseded in 3.1.0 (security pass):** a link is no longer a pass for any title. A picture the server does not hold is fetched for a title `refShownHere` knows (a plan, What's on? on a live card, a search's results), or, for somebody linked (`hasLink`, asked last), a title `refShownToLinked` knows: a clip's tag, or their own What's on? that has not aired. Anything else is a 404 with no provider asked. The reason is the same image in a post: with a link as a pass, anybody who could post (somebody locked, a guest, somebody in Mumble through the chat bridge) could have every linked reader walk the providers' ids, and Mattermost's image proxy is off by default. `mayFetchPictureFor`; `TestOnlyATitleShownHereFetchesAPicture` (which replaces `TestOnlyAShownTitleOrSomebodyLinkedFetchesAPicture`) and T7. |
| `/api/v1/channel/picture` | GET | watch | |
| `/api/v1/channel/thumbnail` | GET | watch | |
| `/api/v1/stills/claim` | POST | **linked** (Q6) | The gate comes before `claimLimiter`. A still is shown to everybody on the Live now card, which is publishing. |
| `/api/v1/stills` | GET | **watchOrAdmin** | The System Console's AdminStills uses it (D1). |
| `/api/v1/stills` | POST | **linked** (Q6) | The gate comes before `uploadLimiter`, and the existing lease and block checks stay. `checkStill` reads only the header (`stills.go:861-871`), so what is uploaded is close to arbitrary. |
| `/api/v1/whep` | POST | watch | The gate comes before `playbackLimiter`, and before the handler's `refreshSoon()` for an unknown handle (`api.go:705`). A **peek** (a request carrying `X-VillaNET-Peek`) is class **linked** (Q6): it exists only to take a still. |
| `/api/v1/watch/popout` (new) | GET | **linked** (Q10) | §3.10 |
| `/api/v1/admin/…` | any | — | Skips layer 1 (step 5). |
| anything else | any | linked | Fail closed |

**Takebacks (D3).** Cancelling your own plan, leaving a plan and clearing What's on? stay open on the server,
for anyone signed in. They only ever remove the caller's own things, and keeping them open means losing a link
can never strand somebody's name where they cannot take it back. Nothing in a locked webapp offers them: plans
of a person who has lost their link stay (as README promises) until they are unlocked again or the plan's time
passes. A system admin who has a link or is let in can cancel anybody's plan from Up next (Q4).

### 3.6 The clip family

The structure stays: `clipRoutes` (`clips_api.go:54-105`) and `serveClips` (`:215-247`), which runs the
route, then the method, then the gate, then the handler.

**Watch class.** A new map lists the routes a let-in caller may use. Every other clip route is linked.

```go
// clipWatchRoutes are the clip routes a person a system admin let in may use:
// looking, never acting. Every other clip route needs a link.
var clipWatchRoutes = map[string]map[string]bool{
	clipsPath:            {http.MethodGet: true},
	clipsPath + "/clip":  {http.MethodGet: true},
	clipsPath + "/cards": {http.MethodGet: true},
	clipsPath + "/video": {http.MethodGet: true, http.MethodHead: true},
	clipsPath + "/thumb": {http.MethodGet: true},
}
```

**The gate.** `requireClipsLink` and `requireClipsLinkSeen` become one function,
`requireClipsAccess(w, userID, watchOK, seen bool) (key string, ok bool)`.

- `seen` keeps today's `clipLinkSeenRoutes`: video, thumb, `draft/video` and cards use `accessSeenOf`.
- **Linked:** pass, with the key.
- **Let in and `watchOK`:** pass with `key == ""`.
- **Let in otherwise:** 403 `watch_only`.
- **Locked:** 403 `locked`.
- **Error:** 503 `store`.
- It still runs before body parsing, id checks, lookups and limiters, and it uses the gate's 2 s refusal
  memory (§3.1), so a refused caller cannot drive a store read per clip request either.

**Invariant.** Behind the gate, `key == ""` happens **only** for a let-in caller on a watch route. So
`clipViewerOf(userID, key, now)` sets the new `clipViewer.WatchOnly = key == ""`. T11 tests the invariant.

**Views** (`clipViewOf`, `clips.go:1661-1701`):

- When `viewer.WatchOnly` is true, `CanVote`, `CanReport` and `CanDelete` are all false, whatever `Admin` or
  `Mine` say. `OfMyStream` is already false, because the tag is empty.
- A new `CanShare bool `json:"canShare"`` is `!viewer.WatchOnly && !viewer.Bot`. The webapp hides Share
  whenever it is false (§5).
- `Voted` stays as stored, so a vote from when the person was linked still shows as theirs, read-only.

**Cards** (`GET /clips/cards`) are the same for every reader who passes the gate (`clipCardOf`), so let-in
readers get the server card and thumbnail. Locked readers are refused, and the webapp never asks on their
behalf (§7).

**Admin clip routes** (`/api/v1/admin/clips*`) are unchanged: `isSystemAdmin` only, no access gate (Q-A).

### 3.7 Other checks that change, and checks that stay

- **Become "linked or let in"** (they are new: these routes check nothing today): WHEP except peeks, stills
  GET (which also lets admins through), `channel/picture` and `channel/thumbnail`. Layer 1 enforces all of
  them.
- **Become "linked"** (also new): stills claim and upload, and WHEP peeks (Q6). Layer 1 enforces them.
- **Watch-class pictures are revalidated every time.** `GET /stills` and `channel/picture` answer
  `private, max-age=86400` when `v` matches (`stills.go:916-920`, `channelpics.go:334-338`), so showing one
  again never reaches the gate: after a let-out, an unlink, or a different person signing in on the same
  browser, it would still come from the HTTP cache. They become `private, no-cache`, as the clip thumbnails
  already are for exactly this reason (`clips_api.go:525-546`), and `channel/thumbnail`'s plain `no-cache`
  (`channelpics.go:399`) becomes `private, no-cache` too. `writeImage` already sends an ETag and answers 304
  (`brand.go:135-160`), so a re-display is a cheap 304 when allowed, and a 403 when not. The version stays in
  the address, so a new picture still gets a new address.
- **Faces.** A face comes from whoever opened a session (`api.go:739-740`), so from now on only from linked or
  let-in people. Nothing in that code changes.
- **Top Snoopers stays linked.** Counting checks the link (`api.go:739`, `watchtime.go:175, 465`) and does not
  change. Let-in people's watching is never counted (Q5).
  *As built: those checks read only `link_user_`, so a let-in person with a leftover forward entry was still
  remembered, paid and named. They now use `hasLink` (`links.go`, both entries agree, a failed read counts
  as no): `handleWHEP`'s Top Snoopers record, `linkedNow`, and the per-pass `linkFor` in `watchingNow`. T16
  holds it.*
- **Stay "linked"**, unchanged in code:
  - `requireLinkedStream` at `community_api.go:133, 215, 282` and `api.go:233`, gaining codes as in §3.4;
  - `nowplaying.go:477, 502`;
  - `claimedToken` (`api.go:591`, `:603`, `:636`);
  - every clip write route, every draft route, `streamer`, `wipe`, and DELETE `clip`.
- **Admin checks:** all unchanged.

### 3.8 `/me` and `/me/link`

**`/me`** (`handleMe`, `api.go:337-385`) still answers 200 to anyone signed in. It now computes `accessOf`
**once** and uses that answer for every field below.

| Field | Change |
|---|---|
| `access` (new) | `"linked"`, `"letIn"` or `"locked"`, or `"unknown"` when `accessOf` failed. |
| `streamKey` | Linked only, as today. It is `""` for everyone else, including `unknown`. |
| `linkedLive`, `clipsStreamer` | Linked only, as today. |
| `media` | **Linked only.** It exists for the planning form. |
| `publicSiteUrl` | **Removed.** The webapp never reads it, and the address sits next to a key (D2). |
| `popOut` (new) | `true` only for **linked** callers, and only while Public watch URL is set (§3.10, Q10). |
| `alsoLetIn` (new) | Linked callers only: whether they are also on the let-in list (this node's copy; `false` when it cannot be read). Used only to word the Unlink confirmation (§5). |
| ~~`accessContact` (new)~~ | ~~The admin's **Who to ask** text (§8.1, Q17): trimmed, control characters removed, at most 64 runes, `""` when blank. Sent to everybody; it is public text an admin chose.~~ **Removed 2026-10-06**, with the setting (the box at the top). |
| `letInAlert` (new) | System admins only, in any access state: the §3.2 alert text, or absent. |
| everything else | Unchanged. The lock screen needs the banner fields and `configured`. Outside the panel, `badgeEnabled` and `styleCodeBlocks` are still needed. Admin alerts go to admins in any access state (Q13); *since 2026-10-06 the panel shows them once it is unlocked, and the lock never does.* |

**`/me/link`** (`handleLink`, `api.go:512-585`) keeps every status and text. The footer still shows the
server's text. Each error **gains a code**, so the lock screen can word its own message (§4.3):

| Status | Text (unchanged) | New code |
|---|---|---|
| 429 | "Too many attempts. Wait a few minutes and try again." (`api.go:167`) | `rate_limited` |
| 400 | "Could not read the request." | `bad_request` |
| 400 | "That publish token is too long." | `too_long` |
| 503 | the `cfg.validate()` text, which never contains the URL | `not_configured` |
| 502 | `clientSafeError(err)` | `unreachable` |
| 403 | "That publish token was not accepted. …" | `not_accepted` |
| 502 | "Broadcast Box returned a stream name this plugin cannot use." | `bad_name` |
| 500 | "Could not unlink your stream." / "Could not save the link." | `store` |

**`setLink` must not fail open** (`links.go:98-145`). Today a failed read of `link_key_<K>` skips the
takeover, so the previous owner keeps `link_user_<owner>=K` with no error, stays "linked", and through
`streamTag(key)` keeps the streamer switch and wipe for K's clips (`clips_api.go:1311-1321`). A failed read of
the person's own previous key can leave a stale `link_key_` naming them. The changes:

- `getLinkedUser` gains a sibling `linkedUser(key) (string, error)`, as `getLinkedKey` has `linkedKey`.
  `setLink` uses `linkedKey` at `:108` and `linkedUser` at `:115`, and returns either error **before any
  write**. `handleLink` then answers 500 `store`, which the lock screen already words (§4.3).
- The other callers of `getLinkedUser` (attribution, `poller.go:455` and `stats.go:503`) keep the swallowing
  form; they decide nobody's access.
- **Two links of the same key at once** (two nodes, or two tabs) can both read no owner, because the writes
  are not compare-and-set. The agreement rule (§3.1) makes that safe: whoever wrote `link_key_<K>` last is
  linked, and the other is left with a forward entry that reads as locked. The loser's lock screen shows the
  unlock and then, when `refreshIdentity` answers, the lock again. That is accepted for a race between two
  accounts that hold one token, and no compare-and-set is added.

**On success:** both the link and the unlink paths call `accessChanged(userID)` after `clipsLinkChanged`.
`setLink` calls `accessChanged(previousOwner)` when it takes a key from somebody (`links.go:115-126`).

The linker's limiter is unchanged: a burst of 5, one more every 3 minutes, per user, per node
(`plugin.go:531`).

### 3.9 When access changes

```go
const wsEventAccess = "access" // arrives as custom_ceo.villa.villanet_access

func (p *Plugin) accessChanged(userID string) {
	// drop the seen-cache entry and move the epoch (§3.1)
	// then tell that one person's open windows, with nothing in the event:
	p.API.PublishWebSocketEvent(wsEventAccess, map[string]any{}, &model.WebsocketBroadcast{UserId: userID})
}
```

**Called for:**

- a link or an unlink (`handleLink`, both paths);
- the previous owner when a key is taken over (`setLink`);
- a let-in or a let-out;
- an admin unlink.

**Never called for:** a username match, a Twitch connection, or a change to somebody else.

**Payload.** The event has **no payload** on purpose. It says only "read `/me` again", so it can never be
the thing that unlocks anybody.

**Timing**

- **On the node that made the change:** at once. The node's let-in copy was replaced and its seen entry was
  dropped.
- **On other nodes:** within `letInMaxAge` + `accessSeenTTL`, which is at most 20 s, and this holds while the
  store fails too, because a failed let-in read fails closed (§3.2). WebSocket events already fan out across a
  cluster (`ClusterEventPublish`).
- **Cluster events (M6) are not used.** Twenty seconds is an acceptable delay for a list admins change by
  hand, and it keeps the plugin from adopting a new hook for this alone.

**Sessions already playing (D8).** A WHEP session that is already open survives an unlink or a let-out. The
plugin only relays the SDP, the media then flows between the browser and Broadcast Box, and nothing can end
the session from outside (BB3). So stopping playback is the webapp's job, and it must not hang on one event
arriving. A window learns of the change from any of these, whichever comes first:

- the `access` event, at once (§4.9);
- `registry.registerReconnectHandler`, which calls `refreshIdentity()` after every WebSocket reconnect,
  because an event sent while the socket was down may never arrive (M10);
- **while any session is open in that window**, a re-read of `/me` every 60 s (`ACCESS_RECHECK_MS` in
  `motion.ts`), through the shared `refreshIdentity()`. This covers a popped-out panel left visible on a
  second monitor, whose `focus` never fires, and pictures and clip media, whose 403s arrive through `<img>`
  and `<video>` and never reach the refusal listener (§4.8). `/me` answers 200, so this adds nothing a
  fail2ban filter would count;
- window `focus` and `visibilitychange` (§4.9).

A cooperating webapp therefore stops within about 60 s even if it missed every event. A browser that does not
cooperate keeps the stream it already has until that stream ends, which for a 24/7 channel is
**indefinitely**, and it cannot open another. LOCKDOWN.md says so (§9.4).

### 3.10 Pop out without a key in the snapshot (D2)

**Today:** the snapshot is one broadcast to everyone (`poller.go:685`), so it cannot be trimmed per person.
Yet with Public watch URL set, it carries `watchUrl = PublicSiteURL + "/" + key` for every stream.

**Changes**

- `apiStream.WatchURL` is deleted. Its comment at `plugin.go:151` goes too.
- `/me.popOut` (§3.8) tells linked people that Pop out exists.
- **New `GET /api/v1/watch/popout?handle=<handle>`**, class **linked** (Q10). A stream key, once handed out,
  cannot be taken back by a let-out or an unlink: it keeps working on the public watch page, and at Broadcast
  Box as a WHEP bearer (`broadcastbox.go:163`). Linked people have Pop out today and keep it; let-in people do
  not get it.
  - The handle must be in the current snapshot and must not be a 24/7 stream. Pop out is offered today
    wherever `watchUrl` was filled.
  - If it is, and Public watch URL is set, the route answers **302** with `Location: <PublicSiteURL>/<url.PathEscape(key)>`,
    `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.
  - Otherwise it answers 404 `{"error":"There is no watch page for that stream."}`.
  - It logs nothing that contains the key.
- **Webapp:** Player's Pop out becomes a plain
  `<a href={PLUGIN_BASE + '/api/v1/watch/popout?handle=' + encodeURIComponent(handle)} target='_blank' rel='noopener noreferrer'>`.
  It can be a plain link because a cookie GET is authenticated (M2).
- **Risk:** the key still ends up in the address bar of the page that was popped out, as it does today, and
  nothing VillaNET does later can revoke it. That is the nature of the feature. LOCKDOWN already advises
  leaving Public watch URL blank, and §9.4 now says why.
- **Desktop app:** a plugin URL opens the app's own pop-up, which may then follow the redirect. That has not
  been checked (real-host check R9).

### 3.11 Secrets and logging

- **The bearer token** is only ever in the body of `POST /me/link` and in the `link_token_` KV entry. It is
  never put in a response, a log, a URL, a WebSocket event, browser storage or the lab's fixtures, and the
  webapp keeps it out of Mattermost's message box (§4.5), where synced drafts would store it (M11).
- **Keys** are never in an access refusal, an admin list or an access event.
- **Logs** of access changes carry only user ids (§3.3).
- **Refusals** (403 `locked`, 403 `watch_only`) are **not** logged. A locked browser makes almost no
  requests, and logging each one would turn a forgotten tab into log noise.

### 3.12 Go tests

All of these must be deterministic under `go test -race`. They use injected clocks (the `now` fields), the
`plugintest` mock, and `httptest` for Broadcast Box. They never sleep. File names are suggestions.

| # | Test | What it holds |
|---|---|---|
| T1 | `TestEveryRouteHasAnAccessClass` (`access_test.go`) | Reads `api.go` with `os.ReadFile` and collects **every** quoted `/api/v1/…` path in each `case`, including a case that lists several (`api.go:205` lists four). Requires each one either to be in `routeClasses` or to start with `/api/v1/admin/`, and names any that is missing. It also fails on any `underPath(` or `strings.HasPrefix(r.URL.Path` dispatch inside `ServeHTTP` that is not on its allow-list (`/static/`, `clipsPath`, `adminClipsPath`, and in 3.1.0 the Twitch HTML routes). |
| T2 | `TestRouteAccessTable` | For every (path, method) in `routeClasses`, multiplied by the callers {locked, let in, linked, locked admin, let-in admin, linked admin}, it checks the status class: passes the gate, 403 `locked`, or 403 `watch_only`. It also checks that **no meter token was spent** on a refusal. A fake limiter counts calls to `allow`. A refused caller's second request inside 2 s causes **no** store read (the KV mock counts `KVGet`), and one after 2 s causes exactly one; a linked caller's made-up path causes at most one read per 10 s. A peek from a let-in caller is 403 `watch_only`. `GET /stills`, `channel/picture` and `channel/thumbnail` answer `Cache-Control: private, no-cache` with an ETag, and a matching `If-None-Match` gets 304 only after the gate (403 for a locked caller). |
| T3 | `TestUnclassifiedRouteIsLinked` | `/api/v1/nope` gives a locked caller 403 `locked` and a let-in caller 403 `watch_only`. A linked caller gets 404, and a POST with the wrong method on a known route gets 405 for a linked caller. |
| T4 | `TestAdminRoutesIgnoreTheLock` | Every `/api/v1/admin/*` route answers a locked system admin exactly as it answers a linked one, and refuses a non-admin with 403. |
| T5 | `TestStillsGetLetsLockedAdminsThrough` | `GET /stills` passes for a locked admin and is refused (403 `locked`) for a locked non-admin. |
| T6 | `TestAccessStoreErrorsAre503` | A failed link read gives 503 `store` on watch and linked routes, and `/me` reports `access:"unknown"`. A failed let-in read gives 503 for somebody not linked, even with an older copy in hand (11 s old). Linked people are never affected by the let-in read. A failed read of either link entry is 503, never locked. |
| T7 | `TestOnlyALinkUnlocks` | A username-matched stream (`linkSourceUsername`), `isSystemAdmin`, and (3.1.0) a Twitch link each leave the person `locked`. A forward entry whose reverse entry names somebody else, or nothing, is `locked`. |
| T8 | `TestLetInDocument` | Covers: create on an empty store; add; adding the same person again (`added:false`, no write); remove; removing somebody who is absent; full at 500 gives 409; bots; deactivated accounts; GetUser 404 gives `no_user` and 500 gives `store`; a username with `@` and capitals. Concurrency: a CAS that loses and then wins, and `errTooBusy` after 8 losses. Garbage is set aside (quarantine and compare-and-delete) and reads as empty. `version:2` reads as empty, refuses writes with `newer`, and is **not** deleted. The node copy is re-read after 10 s and kept while younger, using an injected clock. **Generation:** a refresh that started before a write and finishes after it is discarded, and the writing node keeps the written list. **Let-out then store failure:** the person is refused (503) on another node once its copy is due, never let in. |
| T9 | `TestAccessChangedTellsOnePerson` | Link, unlink, takeover (the event goes to the **previous** owner), let-in, let-out and admin unlink each publish `access` with an empty payload to exactly that `UserId`. A username match publishes nothing. **Fail closed:** when `KVGet` of `link_key_<K>`, or of the person's own `link_user_`, fails inside `setLink`, nothing is written, the previous owner is unchanged, nothing is published, and `handleLink` answers 500 `store`. |
| T10 | `TestLinkAgainstBroadcastBox` | A fake `/api/whip/profile` checks `Authorization: Bearer base64(token)`. Cases: 200 with a key links (200 `streamKey`); 204 gives 403 `not_accepted`; 401 and 500 give 502 `unreachable`; an unusable name gives 502 `bad_name`; 257 runes give 400 `too_long` with no Broadcast Box call; not configured gives 503 `not_configured`; the 6th try inside the burst gives 429 `rate_limited`, and the fake sees no 6th request. Every `Log*` argument is scanned to prove that **the token never appears**. |
| T11 | `TestClipGateClasses` (replaces `TestEveryClipRouteRefusesUnlinked`, `clips_api_test.go:244`) | A locked caller gets 403 `locked` on every clip route. A let-in caller passes the five watch routes and gets 403 `watch_only` on every other route. **Linked-only handlers never receive `key == ""`.** A let-in viewer's `ClipView` has `canVote`, `canReport`, `canDelete` and `canShare` all false, including for an admin and for a clip they made while linked. |
| T12 | `TestBestClipperIgnoresLetIn` | The gate records the link answer, so a let-in voter's old votes do not count. |
| T13 | `TestMeAccess` | `access` in each state. `media` only for linked callers. No `publicSiteUrl`. `popOut` only for a linked caller and only when the setting is set. `alsoLetIn` only for linked callers. ~~`accessContact` cleaned and capped.~~ *No `accessContact` (2026-10-06).* Admin alerts, including `letInAlert`, for a locked admin. |
| T14 | `TestPopOut` | A locked caller gets 403 `locked` and a let-in caller 403 `watch_only`. A linked caller gets 302 with the right `Location`, `no-store` and `no-referrer`. Blank setting, unknown handle and 24/7 stream each give 404. The snapshot's JSON contains no `watchUrl`. |
| T15 | `TestAdminUnlink` | Clears the link, What's on? and the seen cache, and publishes `access`. A user with no link gives `wasLinked:false`. `letIn` is true for somebody also on the list, and false otherwise. |
| T16 | `TestLetInNotCountedAsSnooper` | A let-in caller's WHEP session adds no Top Snoopers time. |

---

## 4. The lock screen

### 4.1 Where it lives, and that nothing is behind it

- **New `components/PanelGate.tsx`** is registered in place of `Sidebar`:
  `registry.registerRightHandSidebarComponent(PanelGate, <SidebarTitle/>)` (`index.tsx:257`). It stays
  mounted the whole time the RHS shows VillaNET and renders exactly one of:
  - **starting:** "me" has not been read in this window yet. It shows the lock frame with the emblem at 25%
    opacity and nothing else, `aria-busy="true"`, and a visually hidden "Starting VillaNET…". The first 250 ms
    are plain black, so a fast answer shows no emblem at all.
  - **`<LockScreen/>`:** when access is `locked`. Also when it is `unknown` (or "me" could not be read at
    all) **and this page has never had a definite answer**; the lock then shows its **NO SIGNAL face**: the
    plate reads NO SIGNAL (*2026-10-06: no plate, and no padlock badge*), the line says the plugin is not answering, and Try again comes first. The bar is
    still offered, because a locked person can still unlock, but a linked person during a plugin restart is
    never told they are locked.
  - **`<Sidebar access={access} key={access}/>`:** when the last definite access in this page is `linked` or
    `letIn`. An `unknown` after that keeps the panel, with its sessions, and the panel shows a quiet notice
    with Try again (§4.3, §4.9).
  - What is shown comes from the pure `shownAccessOf(latest, lastDefinite)` in `lockLogic.ts`: `latest` when
    it is definite, otherwise `lastDefinite`, otherwise `unknown`. `lastDefinite` lives in `identity.ts` and
    is cleared on sign-out. The `key` remounts the
    panel once when somebody moves between let-in and linked, so its mount-time loads (profile, What's on?,
    drafts) run for the new rights. It also replays the boot sweep, which is the unlock moment.
- **On mount**, PanelGate calls `refreshIdentity()`, which is shared and never rejects. Each time the panel
  opens, access is therefore read fresh.
- **In a popped-out window** PanelGate runs `fitPopoutWindow` on its own root (`popout.ts`), as Sidebar does
  (`Sidebar.tsx:110-112`). There the panel sits in the page flow and is only as tall as its contents, so
  without it the lock would not fill the window, would not be centred, and the bottom banner would float
  under the form.
- **Focus when PanelGate swaps its content** (lock to panel, panel to lock, let-in panel to linked panel and
  back): it moves focus to the new root (the panel's or the lock's, both `tabIndex -1` and labelled VillaNET)
  **only** when `document.activeElement` was inside the old root, or is `body`. Otherwise focus stays where
  the person put it, such as Mattermost's message box, whose keys the new root's stray-key stop would
  otherwise swallow. This one rule covers an unlock, an admin letting the person in, Unlocked elsewhere,
  linking from the let-in footer, and Unlink.
- **Access is worked out by the pure `accessOfMe(me, signedInUserId)` in the new `lockLogic.ts`:**
  - `null` gives `'unknown'`.
  - A missing `access` field gives `'locked'` (fail closed).
  - A `me.userId` that is not the signed-in Mattermost user gives `'locked'`, the same rule `linkOf` uses
    (`clips.ts:82-93`).
  - Otherwise the result is `me.access`.
- **Nothing of the panel runs behind the lock.** `Sidebar` does not mount, so none of its effects start: the
  snapshot poll (`pollWhileShown`), `widenWhileOpen`, `captureStillsWhileOpen`, `fetchMyProfile`, the boot
  timer, tiles, `mountVideo` and WHEP, or the Clips row.
  - The lock screen itself makes exactly **one** request when it mounts (the shared `/me`), plus
    `POST /me/link` when somebody presses Unlock.
  - It does **not** widen the RHS. The lock is a narrow form, and widening is panel behaviour.
- **What the lock screen keeps from the panel:**
  - the banner (top, and the bottom too when the placement is "Top and bottom"), using `me.bannerText` and
    `bannerColor`. The `bannerStyle` code at `Sidebar.tsx:1098-1102` moves into a shared `bannerStyleOf(me)`
    helper;
  - the "not set up" notice, from `me.configured`;
  - ~~for system admins, the admin alerts (Q13).~~ *Superseded 2026-10-06: no admin alerts on the lock,
    not even one line.* The block at `Sidebar.tsx:1155-1200` becomes a shared `<AdminAlerts me={me}/>`, with
    the new `letInAlert` added, which the unlocked panel shows.

### 4.2 Layout

*Superseded 2026-10-06 by variant C, "Behind glass" (the box at the top).* This is the layout now; the
3.0.0 drawing follows it, struck, for the record.

```
┌────────────────────────────────────┐  ← Mattermost's RHS header: SidebarTitle shows the wordmark only (§7)
│ [banner, if set]                   │
│                                    │
│            ╭──────────╮            │  the panel emblem, 46% of the panel's width (128–208 px),
│            │  emblem  │            │  at 55% opacity, as if behind glass
│            ╰───────(🔒)            │  padlock badge across its lower right edge, ringed in red, undimmed
│                                    │
│   (a line only when there is a     │  13 px / 1.5, #c9c9c9, max 300 px, centred
│   reason: "You are no longer let   │
│   in.")                            │
│                                    │
│  (status line, a live region:      │  12 px, sitting on the bar; two lines' room kept, three under
│   empty, or "Checking your token…",│  360 px, so nothing moves when it speaks; errors in --vntv-red-text
│   or an error in red)              │
│ ╭────────────────────────────────╮ │
│ │ 🔒  Bearer token to unlock  👁 →│ │  the docked bar: 46 px, 14 px corners, black, --vntv-muted edge
│ ╰────────────────────────────────╯ │  padlock = label; eye = Show/Hide token; arrow = Unlock
│                  DISCONNECT TWITCH │  only when Twitch is connected
│ [bottom banner, if "both"]         │
└────────────────────────────────────┘
```

Not set up: the emblem with its badge, "VillaNET is not set up yet, so it can’t be unlocked." under it,
Disconnect Twitch under that when connected, and no dock. No signal: no badge, the line, **Try again**,
and the dock as above.

<details><summary>The 3.0.0 layout (superseded)</summary>

```
┌────────────────────────────────────┐  ← Mattermost's RHS header: SidebarTitle shows the wordmark only (§7)
│ [banner, if set]                   │
│                                    │
│            ╭──────────╮            │
│            │  emblem  │            │  brand/logo (the tentacled skull), 112 px, opacity 0.92
│            ╰──────────╯            │
│                                    │
│          ┌──────────────┐          │
│          │ 🔒  LOCKED   │          │  plate: an <h2>, 2 px white-50% box, Oswald 600, 0.22em tracking
│          └──────────────┘          │
│   (a line only when there is more  │  13 px / 1.5, #c9c9c9, max 300 px, centred
│   to say: "to watch Dave")         │
│                                    │
│   STREAM HERE? Enter your bearer   │  row head: Oswald 600 caps 12 px #fff, then 13 px #c9c9c9
│   token.                           │
│                                    │
│   BEARER TOKEN                     │  label: Oswald 600, 11 px, 0.14em, #777
│   ┌──────────────────────┬──────┐  │
│   │ ••••••••••••••••••   │ SHOW │  │  the black bar, 40 px
│   └──────────────────────┴──────┘  │
│   The token your streaming software│  hint: 11 px, #c9c9c9 (not #777: it says what a bearer token is)
│   publishes with. OBS calls it …   │
│   ┌──────────────────────────────┐ │
│   │        🔒  UNLOCK            │ │  .vntv-link-cta, full width of the form
│   └──────────────────────────────┘ │
│   (status line, live region)       │  12 px; errors in --vntv-red-text
│                                    │
│   ─────────────────────────────    │  1 px --vntv-separator
│   JUST WATCHING? Ask @victor to    │  the same head and size as STREAM HERE?
│   let you in.                      │  {contact}, or "a system admin"; hidden when not set up
│   [admin alerts — admins only]     │
│ [bottom banner, if "both"]         │
└────────────────────────────────────┘
```

</details>

**The column**

- The form column is at most 320 px wide.
- Everything in the middle sits in one inner column, `.vntv-lock-column`, with `margin-block: auto`,
  inside `.vntv-lock-stage`, with at least 32 px above and 24 px below. Auto margins centre the column
  when it fits and collapse to 0 when it does not. *Since 2026-10-06 the bar is not in the column: it is
  in `.vntv-lock-dock`, under the stage, which stays put while the stage scrolls.*
- The stage scrolls (`overflow-y: auto`) when the RHS is short, **from the top**. A flex column centred with
  `justify-content: center` overflows above the top as well as below, and the emblem and the line would
  then be clipped where no scrolling reaches them (a short window with both banners; a phone with the soft
  keyboard up). So the stage uses `justify-content: flex-start`.

**Sizes**

- *2026-10-06:* the emblem is `clamp(128px, 46cqi, 208px)` wide, a container unit on the stage
  (`container: vntv-lock / inline-size`), so it follows the side panel however narrow it is dragged; 160 px
  in a browser without container units. A media query would measure the window, not the side panel.
  ~~At a panel width of 340 px or less, the emblem is 88 px.~~
- At a window height of 620 px or less, the emblem is 104 px (*2026-10-06; was 72 px*), and the gap over
  the line 14 px.
- On a phone, where the RHS is full width, there is never horizontal scroll. The bar's text is 16 px below
  768 px, so that iOS Safari does not zoom the page when the bar is focused (M12).

**The emblem**

- It is the **panel emblem** (`brand/logo` through the existing `--vntv-mark`), so an admin's uploaded
  artwork shows here too. It is not the App Bar icon, which the header wordmark already shows.
- *2026-10-06:* dimmed to 55%, as if behind glass; much more and the plugin's red ring went a muddy
  maroon. The glass clears to 92% as the lock opens.
- The corner watermark (`.vntv-panel::after`) is **turned off** on the lock screen
  (`.vntv-panel.vntv-lock::after { content: none; }`). Two skulls compete, and here the emblem is the
  centrepiece.

**The padlock** is a new `PadlockIcon` in `icons.tsx` on a 24-unit grid. Its body is a rounded rect, and its
shackle is a separate `<path className='vntv-lock-shackle'>` with `transform-origin` at its right leg.
*2026-10-06:* it is drawn twice: in white in the badge on the emblem (a black disc ringed in
`--vntv-red`, with a black moat so it reads over any uploaded emblem), and in grey at the head of the bar,
red while the bar is being typed in. Both shackles swing open as the lock opens. ~~It is drawn in
`--vntv-red` inside the plate and in white inside the Unlock pill.~~

**Admin-uploaded artwork** that is not white on black can look heavy even dimmed. That is accepted: it is
the admin's own emblem.

### 4.3 Words

*Brought in line with the amendment of 2026-10-06 (the box at the top).* The lock says a reason, a status
or a fact, never a way in and never anything only for admins; the sheets' words (the let-in footer's Link
your stream and the relink form) are unchanged. Rows that no longer apply are struck.

| Where | Exact text |
|---|---|
| Heading | No plate. A visually hidden `<h2>`: "VillaNET is locked", "VillaNET is unlocked" during the unlock transition, and "VillaNET is not answering" in the NO SIGNAL face. ~~Plate (the heading): `LOCKED`, `UNLOCKED` during the unlock transition, and `NO SIGNAL` in the NO SIGNAL face.~~ |
| Line (default) | None. The padlock already says it, and the bar what to do. |
| Line, after a popover Watch | "Unlock VillaNET to watch {name}." `{name}` is the stream's shown streamer name, or "this stream". |
| Line, after a card Watch or a clip link | "Unlock VillaNET to watch this clip." |
| Line, after this page's own Unlink (cause `self`, §4.9) | "You unlinked your stream." |
| Line, after losing a link any other way (cause `unlinked`) | "Your stream was unlinked here, by an admin or because another account linked it." |
| Line, after being let out in this page (cause `letOut`) | "You are no longer let in." |
| Line, NO SIGNAL face | "VillaNET is not answering, so it can't tell whether you're unlocked." |
| ~~Streamer row~~ | ~~Head **STREAM HERE?**, then "Enter your bearer token." Under it: the label, the bar, the hint and Unlock.~~ No row: the docked bar alone. |
| ~~Viewer row~~ | **Removed 2026-10-06** ("Don't advertise that"). Nothing on the lock says that anybody can be let in without a token, or whom to ask. ~~Head **JUST WATCHING?**, then "Ask {contact} to let you in."~~ |
| ~~Viewer row, for a system admin~~ | **Removed 2026-10-06** ("Remove admin lines too"). ~~"Let yourself in: System Console → Plugins → VillaNET → Access." (Q3)~~ |
| ~~Admin alerts~~ | **Removed 2026-10-06**, and so is the one red line C put in their place, "VillaNET needs attention in the System Console." |
| Label (a real `<label htmlFor>`) | On the lock, the padlock at the head of the bar, with "Bearer token" visually hidden. In the sheets, `BEARER TOKEN`, as before. |
| Placeholder | On the lock, "Bearer token to unlock" (in the panel's letters; the token itself in the bar's monospace). In the sheets, "Paste it here". |
| Reveal | On the lock, an eye whose name is "Show token" / "Hide token" (the eye struck through while shown). In the sheets, "Show" / "Hide". Neither has `aria-pressed`. Either hides the token again after 15 s, as the footer's does (`Sidebar.tsx:52, 265-272`). |
| Hint | In the sheets, under the bar: "The token your streaming software publishes with. OBS calls it Bearer Token, under Settings → Stream." In `#c9c9c9`, because it is the one sentence that says what a bearer token is. On the lock there is none under the bar: "OBS calls it Bearer Token, under Settings → Stream." follows the status after an empty press and after a `not_accepted` or `too_long` refusal (`lockStatus`). A pasted leading "Bearer " is fine: the server strips it (`api.go:521`). |
| Button | On the lock, an arrow named "Unlock", "Checking…" while the request is out, and "Unlocked" as it opens. In the sheets, "Link" or "Save", and "Checking…". |
| Not set up | "VillaNET is not set up yet, so it can’t be unlocked." (`LOCK_NOT_SET_UP`) under the emblem, the same for everybody. The bar and Unlock are **not shown**, because they could only fail. |
| ~~Not set up (system admin)~~ | **Removed 2026-10-06.** ~~The admin also sees the existing notice text: "Broadcast Box is not set up yet. A system admin can add the server URL in **System Console → Plugins → VillaNET**."~~ The System Console says it. |
| `unknown`, with no definite answer yet in this page | The NO SIGNAL face (§4.1): no padlock badge, the line above, and a **Try again** text key (`.vntv-textkey`, §4.7) that calls `refreshIdentity()`. The bar stays usable. |
| `unknown` after a definite answer | No lock. A quiet `.vntv-notice` at the top of the panel: "VillaNET could not check your access just now." with a **Try again** text key. It goes when an answer comes. |

**Status line.** These are the words for each `/me/link` result. Mapping a code to copy is pure
(`lockLogic.ts`, `lockCopy(code)`). A code it does not know shows the server's text.

| Result | Status text | Tone |
|---|---|---|
| Unlock pressed with an empty or whitespace-only bar (**no request**) | "Paste your bearer token first." | error |
| Request out | "Checking your token…" | polite |
| `not_accepted` (403), or a 200 whose `streamKey` is empty | "That token was not accepted. Check it and try again." | error |
| `rate_limited` (429) | "Too many tries. Wait a few minutes, then try again." | error |
| `unreachable` (502) | "Broadcast Box is not answering, so your token can't be checked. Try again later." | error |
| `bad_name` (502) | "Your token was accepted, but its stream name can't be used here. Ask an admin to look at it." | error |
| `not_configured` (503) | "VillaNET is not set up yet, so it can't be unlocked." | error |
| `too_long` (400) | "That is too long to be a bearer token." | error |
| `bad_request` (400) | "That did not get through. Try again." | error |
| `store` (500) | "Your token was accepted but could not be saved. Try again in a moment." | error |
| No answer, 404 (the plugin restarting), or anything else | "VillaNET is not answering. Try again in a moment." | error |
| Success | Visually hidden: "Unlocked." | polite |
| Let in while looking at the lock (the `access` event) | Visually hidden: "An admin let you in to watch." | polite |

*On the lock (2026-10-06)*, the status line sits over the docked bar, and the empty press and the
`not_accepted` and `too_long` refusals are followed by "OBS calls it Bearer Token, under Settings →
Stream.", since the lock has no hint under its bar. The sheets say it under theirs, as before.

There is **no automatic retry**, ever. A plugin that is restarting answers 404 on every route, and a steady
stream of 4xx from one browser is what the fail2ban jail in front of Mattermost looks for (`stills.ts:49-55`,
`fail2ban-npm-filter.conf`).

### 4.4 States and transitions

`lockLogic.ts` (pure, imports nothing, checked by `build/check-away.mjs`) holds the reducer.

| State | Bar | Unlock | Status |
|---|---|---|---|
| `idle` | editable | enabled | — |
| `empty` (after pressing with nothing) | editable, focused (rule below), `aria-invalid` | enabled | "Paste your bearer token first." |
| `checking` | **`readOnly`**, not `disabled`, so focus and the screen reader's place stay | `aria-disabled="true"`, label "Checking…", clicks ignored | "Checking your token…" |
| `refused(code)` | editable. The text is kept so a typo can be fixed, Show goes back to Hide, `aria-invalid="true"`, and the border is `--vntv-red-hot`. Typing clears the error. Focus goes back to the bar (rule below). | enabled | the copy for the code |
| `unlocking` | `readOnly`; the value is already cleared from state | hidden. *As built: kept in place, reading UNLOCKED (since 2026-10-06 the arrow, named Unlocked), `aria-disabled`, pressing nothing. Taken away, it dropped the column by half its height as the padlock swung, and took the keyboard focus to `body`, from where a key went to the message box.* | "Unlocked." (visually hidden) |

**Focus after `empty` and `refused`.** Both put focus back in the bar, but only when focus is on one of the
lock's own controls (Unlock, Show, Try again) or on `body`. After a mouse click on Unlock that is refused,
focus would otherwise stay on the Unlock button, and a second Ctrl/Cmd+V would go to Mattermost's message box
(M8). A person who has meanwhile clicked into the message box is left there.

**Unlocking, step by step**

1. `linkStream(token.trim())`. The bar never sends `""`, because `""` means **unlink** (`api.go:523`). The
   empty check is made after trimming and after stripping one leading "Bearer ", as the server does
   (`cleanStreamKey`), so "Bearer " on its own counts as empty.
2. On 200 with a non-empty `streamKey`:
   - the token is cleared from React state;
   - `setIdentity({...me, streamKey, access: 'linked', clipsStreamer: undefined})` is set optimistically, as
     `submitLink` does today (`Sidebar.tsx:635-681`);
   - `refreshIdentity()` runs in the background;
   - the state becomes `unlocking`, and after `LOCK_OPEN_MS` PanelGate shows the panel.
3. On an error, the state becomes `refused(code)`.

**The token is never kept** anywhere but the bar's own React state: not in a module variable, not in storage
and not in the identity. It is cleared on success and on unmount. It is never written to the console.

### 4.5 Keys, focus and screen readers

**The root** is `<div className='vntv vntv-panel vntv-lock' role='region' aria-label='VillaNET' tabIndex={-1}>`.

- **Its key stop covers the whole lock, not only the root.** The panel's stop (`Sidebar.tsx:1125-1137`) runs
  only when the root itself is the target. On the Show pill, the Unlock pill, Try again or an admin-alert
  link (*since 2026-10-06 the eye, the arrow, Try again, Disconnect Twitch or its words*), a paste would still go to Mattermost's message box (M8). Synced drafts then store the token on the
  server at once (M11), and the next Enter, which is the lock's own submit key, posts it to the channel. Every
  user must now paste a token at least once, so this would happen. The lock's `onKeyDown` therefore handles
  every target inside the lock that is not the bar:
  - **Ctrl/Cmd+V, or a printable key** with no Ctrl, Cmd or Alt: if the bar is editable, `bar.focus()`
    inside the keydown, so the paste or the character lands in the bar (the same move Mattermost makes for
    its own box, M8), then `stopPropagation()`. If the bar is not editable (`checking`, `unlocking`, not set
    up), only `stopPropagation()`.
  - Space on a button or a link is left alone, because it presses it (Mattermost spares it too). Tab, F6,
    Enter, Escape and shortcuts with a modifier go on as usual.
  - The helper, `keepTokenKeys(barRef)`, goes next to `keepKeysInside` in `focusTrap.ts`. The same helper
    wraps **every** token bar: the lock, the let-in footer's `TokenBar`, and the linked relink form (§5),
    which has the same hole today (`Sidebar.tsx:1404-1452`).
- A click on the empty part of the lock focuses the root (it has `tabIndex -1`), and the rule above applies
  there too.
- **A bearer token is refused before it can be posted.** `index.tsx` registers
  `registry.registerMessageWillBePostedHook` (M10). While this page's access is not `linked`, or while any
  token bar is open, it refuses a post whose message, trimmed and with one leading "Bearer " removed, has the
  shape of a Broadcast Box token (a UUID, BB3). The answer is "That looks like your bearer token. Paste it
  into VillaNET's bar instead." The test is the pure `looksLikeToken(text)` in `lockLogic.ts`. **The hook
  alone is not enough:** with synced drafts on (M11), a token that lands in the message box is already stored
  on the server as a draft, which is why the key rule above is the real defence and the hook is the belt.

**The bar**

- **It is not a password field where that can be avoided.** Browsers' own password managers ignore
  `autoComplete='off'` on a password field and ignore the extension attributes, so Chrome or Firefox could
  offer the person's saved **Mattermost** password here, which would then go to Broadcast Box as a token.
  Chromium is also likely to treat "a typed password field, a successful request, then the field removed" as
  a submission, which is exactly how the lock unlocks, and to offer to save the token as the site's password
  or to overwrite the saved one. (That was not checked in Chromium source here; real-host check R11.) So:
  - Where `CSS.supports('-webkit-text-security', 'disc')` holds (checked once at run time):
    `<input type='text'>`, masked while hidden with `-webkit-text-security: disc` (`.vntv-lock-input[data-masked="true"]`).
  - Elsewhere: `type={revealed ? 'text' : 'password'}` with `autoComplete='new-password'`, which browsers do
    not fill with a saved password.
  - Both: `autoCapitalize='off' autoCorrect='off' spellCheck={false}`, `aria-describedby` (below), and
    `data-1p-ignore`, `data-lpignore='true'` and `data-bwignore='true'` for the extension managers.
  - The linked relink form and the let-in `TokenBar` use the same component, so the same rule.
- It is not inside a `<form>`, as in the house pattern.
- The server never logs the value it forwards (§3.11, T10).
- Ids come from a module counter, because `useId` is React 18 and this webapp is pinned to React 17.

**Keys in the bar**

- **Enter** submits, unless `composing(event)` (`keys.ts:8`) says an IME is composing.
- **Escape** clears an error state (the message and `aria-invalid`) and nothing else. It never clears the
  bar: Escape is what people press to dismiss an autofill list, an IME candidate or a popover, and a pasted
  token should not vanish with it. Clearing the bar is ordinary editing.
- Both call `event.stopPropagation()`. Every other key is left to the INPUT, which Mattermost already leaves
  alone (M8).

**Focus when the lock appears**

- **Opened by the person**: the App Bar or channel header button when the press actually opens VillaNET,
  popover Watch, card Watch, or a clip link.
  - Each VillaNET opener sets `openedByHand`, **stamped with the time** (in `watching.ts`, beside
    `openPanel`). It counts for 1 s (`OPENED_BY_HAND_MS`) and is then ignored, so a flag nobody consumed can
    never focus the bar on a later, automatic mount.
  - The App Bar and channel header callbacks (`index.tsx:327-338`) are toggles (`toggleRHSPlugin`). They set
    the flag **only when VillaNET is not already the open RHS plugin**, read the way `away.ts` reads the RHS
    (`awayLogic.panelOf`). A press that closes the panel sets nothing.
  - The lock **subscribes** to the flag. On mount, or when an opener fires while the lock is already mounted,
    it consumes a fresh flag and starts the focus guard with the bar as its target. That is the existing
    `startFocusGuard` (`away.ts:566-617`), factored out so both can use it: it corrects Mattermost's two-hop
    move into the RHS when it happens (M7), applies itself at its deadline when it does not (v9.3.0), and stops
    as soon as the person presses or clicks anything. Like the guard today, it only moves focus that is on
    `body` or inside the RHS, never out of a text field elsewhere.
    *As built: the lock's guard takes focus at once (`atOnce`) and from anything that is not a text field
    (`fromAnythingButText`, `focusGuard.ts`). Mattermost moves focus into the RHS only when it goes from
    closed to open (checked in its source), so opening VillaNET over a thread left focus on the App Bar
    button, where a Ctrl+V went to the message box. A text field is still never touched.*
  - **Not on a touch screen.** When away's world says `mobile` (Mattermost's `mobileView`, `away.ts:275`), or `(pointer: coarse)` matches, the bar is not
    focused: the soft keyboard would cover half the full-screen RHS for every viewer with no token who only
    came to read the lock.
- **Opened by itself** (auto-open, which locked people do get, by Victor's answer to Q1): **never** focus the bar. Somebody
  typing a message would otherwise type it into the token field, and Enter would post it to `/me/link`. A
  locked person gets no return after a reply at all (§4.9).

**Screen readers**

- The status line is `role='status' aria-live='polite'`. Errors are announced through it, which avoids an
  assertive alert interrupting typing.
- The bar has `aria-describedby='<line id> <hint id> <status id>'` (the line only when there is one), so a
  person who lands in it hears why they are there ("to watch {name}", "no longer let in"), what a bearer token
  is, and any error. ~~The region has `aria-describedby='<viewer row id>'`, so the way in for people with no
  token is read too.~~ *2026-10-06:* on the lock the bar has no hint to point at (`<line id> <status id>`),
  and the region is described by the line alone, when there is one: there is no way in to read out.
- The bar has `aria-invalid` in the error states.
- *Superseded 2026-10-06: there is no plate and there are no rows. The heading is a visually hidden
  `<h2>` ("VillaNET is locked", "…is unlocked", "…is not answering"), and the eye's name changes between
  "Show token" and "Hide token".* **The plate is the heading.** It is an `<h2>` styled as the plate. Its visible word is LOCKED (or UNLOCKED,
  or NO SIGNAL), with a visually hidden "VillaNET is " before it, so heading navigation finds "VillaNET is
  locked". The two row heads are `<h3>`.
- **Show/Hide** is a plain button whose label changes, **without** `aria-pressed`: both together read as
  "Hide, toggle button, pressed". The linked footer's reveal (`Sidebar.tsx:1426-1431`) changes the same way.
- The emblem is a decorative `div`, and `aria-hidden`.

**Tab order:** Try again (NO SIGNAL face only), bar, Show, Unlock, Disconnect Twitch (3.1.0, when shown).
~~The admin alerts' links.~~ (*2026-10-06:* the eye and the arrow for Show and Unlock; nothing for admins.)
After Disconnect Twitch, focus goes to its words, and from them, when they lapse, to the lock's root (the
box at the top). Focus is **not trapped**:
this is a panel beside the channel, not a dialog.

### 4.6 Unlocking: the transition

The motion constants go in `motion.ts`: `LOCK_OPEN_MS = 440`, `OPENED_BY_HAND_MS = 1000`,
`ACCESS_RECHECK_MS = 60000` and `PENDING_MS = 600000`. There is no focus-delay constant: focus on opening goes
through the focus guard (§4.5).

1. **0 to 180 ms.** `data-unlocking="true"` on the root. The shackle lifts and swings open
   (`translateY(-2px) rotate(-28deg)`, 180 ms ease-out). ~~The plate's text becomes UNLOCKED~~ (*2026-10-06:
   both shackles, the badge's and the bar's, and the glass over the emblem clears to 92% over 160 ms*), and the bar's
   border goes `--vntv-red`.
2. **160 to 420 ms.** The stage (*and since 2026-10-06 the dock*) fades (`opacity 1 → 0`, `scale 1 → 0.98`, 260 ms ease-in).
3. **At `LOCK_OPEN_MS`.** PanelGate swaps in `<Sidebar/>`.
   - Sidebar's `booting` starts as `!reducedMotion() && !cameBack()` (`Sidebar.tsx:89-94`), so the boot scan
     line sweeps down the newly mounted panel.
   - Focus moves to the panel root (`.vntv-panel`, `tabIndex -1`, labelled VillaNET), which a screen reader
     reads as arriving in VillaNET, under the swap rule of §4.1: only when focus was inside the lock or on
     `body`. Somebody typing in the message box when an admin lets them in keeps typing there.
   - "Unlocked." was already said, from PanelGate's own live region, which survives the swap.

**With `prefers-reduced-motion: reduce`:** no shackle movement, no fade and no boot sweep. The swap happens at
once (`LOCK_OPEN_MS` is treated as 0), and "Unlocked." is still announced.

The same transition plays when an admin lets the person in while they are looking at the lock.

### 4.7 Styles (`webapp/src/styles.ts`)

**House rules for this file**

- The stylesheet is one template literal. **No backtick** may appear anywhere in it, not even in a comment.
- No `\00b7`-style escapes: use the character itself.
- The reduced-motion block stays **last**.
- Run `python3 build/check-styles.py` (and, since 3.1.0, `node build/check-squeeze.mjs`) after every edit,
  then look at the lab, because a broken rule fails silently.

**Where the new rules go.** A new `/* ---- Lock screen ---- */` section goes after the `.vntv-clips-locked`
block. The `.vntv-clips-locked` block itself is deleted (§6).

*Superseded in part 2026-10-06 (the box at the top).* The lock section of `styles.ts` is now the record:
its comments say why each rule is as it is. In outline, what variant C added and what is left:

| Class | Now |
|---|---|
| `.vntv-lock-mark`, `.vntv-lock-emblem` | The emblem's box, `clamp(128px, 46cqi, 208px)` square (160 px without container units, 104 px under 620 px of height), and the emblem in it at `opacity: 0.55`, `0.92` while unlocking, `0.25` starting. |
| `.vntv-lock-badge` | The padlock on the emblem: absolutely placed across its lower right edge, a black disc with a 2 px `--vntv-red` ring and a 4 px black moat. |
| `.vntv-lock-line` | Under the emblem, `margin-top: 22px`; the reason, the No signal line, and the not-set-up sentence. |
| `.vntv-lock-dock`, `.vntv-token-dock` | The dock under the stage (`padding: 0 12px 12px`, a container), and the bar's grid in it: Twitch's words, the status, the bar, the Disconnect key. |
| `.vntv-lock-say` | The status line's place over the bar: `min-height: 44px` (62 px in a dock under 360 px), the words at its foot. |
| `.vntv-token-dock .vntv-lock-bar` | 46 px high, 14 px corners, the footer bar's look in the lock's black. |
| `.vntv-lock-pad`, `.vntv-lock-tool`, `.vntv-lock-go` | The padlock label at the bar's head; the eye and the arrow, 34 px squares; the arrow red once there is something to send. |
| `.vntv-token-dock .vntv-twitch-lock …` | Disconnect Twitch on its own line under the bar, at the right; its words in the top row. |
| `.vntv-lock[data-unlocking="true"] .vntv-lock-stage, … .vntv-lock-dock` | The fade, `vntv-lock-out`. |

Gone with the lines (2026-10-06): `.vntv-lock-alert`, `.vntv-lock-quiet` and `.vntv-lock-line b`. Gone
with C: the plate, the form, the rows and their heads, the Unlock pill, and the lock's alerts box. The
3.0.0 table follows, with the classes that no longer exist struck.

| Class | Rules (the builder may tune values within the house look; the structure is fixed) |
|---|---|
| `.vntv-panel.vntv-lock::after` | `content: none` |
| `.vntv-lock-stage` | `flex: 1 1 auto; overflow-y: auto; display: flex; flex-direction: column; align-items: center; justify-content: flex-start; padding: 32px 16px 24px; container: vntv-lock / inline-size`. **Never** `justify-content: center`, which overflows above the top, out of reach (§4.2). |
| `.vntv-lock-column` | `margin-block: auto; width: 100%; max-width: 320px; display: flex; flex-direction: column; align-items: center`. It centres when it fits and scrolls from its top when it does not. |
| `.vntv-lock-emblem` | `width: 112px; height: 112px; background: var(--vntv-mark) center / contain no-repeat; opacity: 0.92; margin-bottom: 18px` |
| `.vntv-lock[data-state="starting"] .vntv-lock-emblem` | `opacity: 0.25` |
| ~~`.vntv-lock-plate`~~ | The `.vntv-plate` look without its absolute positioning: `display: inline-flex; align-items: center; gap: 8px; border: 2px solid rgba(255,255,255,0.5); border-radius: 3px; padding: 7px 14px; font-family: var(--vntv-display); font-weight: 600; font-size: 15px; letter-spacing: 0.22em; text-indent: 0.22em; text-transform: uppercase` |
| `.vntv-lock-line` | `margin: 12px 0 22px; max-width: 300px; color: #c9c9c9; font-size: 13px; line-height: 1.5; text-align: center` |
| ~~`.vntv-lock-form`~~ | `width: 100%; max-width: 320px; display: flex; flex-direction: column; gap: 8px` |
| ~~`.vntv-lock-row`~~ | `width: 100%; display: flex; flex-direction: column; gap: 8px`. The viewer row adds `margin-top: 22px; padding-top: 16px; border-top: 1px solid var(--vntv-separator)`. |
| ~~`.vntv-lock-rowhead`~~ | `margin: 0; font-family: var(--vntv-display); font-weight: 600; font-size: 12px; letter-spacing: 0.14em; text-transform: uppercase; color: #ffffff`. The words after it are 13 px `#c9c9c9`, the same in both rows. |
| `.vntv-lock-label` | `font-family: var(--vntv-display); font-weight: 600; font-size: 11px; letter-spacing: 0.14em; text-transform: uppercase; color: var(--vntv-muted)` |
| `.vntv-lock-bar` | **The black bar:** `position: relative; display: flex; align-items: center; height: 40px; background: #000000; border: 1px solid var(--vntv-muted); border-radius: 4px; transition: border-color 120ms ease, box-shadow 120ms ease` |
| `.vntv-lock-bar:focus-within` | `border-color: var(--vntv-red); box-shadow: 0 0 0 1px var(--vntv-red), 0 0 14px rgba(200, 16, 46, 0.25)` |
| `.vntv-lock-bar[data-error="true"]` | `border-color: var(--vntv-red-hot)` |
| `.vntv-lock-input` | `flex: 1 1 auto; min-width: 0; height: 100%; padding: 0 12px; background: transparent; border: 0; outline: none; color: #ffffff; caret-color: var(--vntv-red-hot); font: 14px ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace; letter-spacing: 0.04em`. Placeholder colour: `var(--vntv-muted)`. `[data-masked="true"]` adds `-webkit-text-security: disc` (§4.5). |
| `.vntv-lock-bar` edge, why `--vntv-muted` | The bar is black on the black panel, so its border is the only thing that shows where it is. `--vntv-muted-border` (#555) is about 2.8:1 on #000, under the 3:1 WCAG 1.4.11 asks of a field's boundary; `--vntv-muted` (#777) is about 4.7:1. The red focus ring stays. |
| `@media (max-width: 767px)` | `.vntv-lock-input { font-size: 16px; }` (M12). Viewport-based on purpose: it is about the device. |
| `.vntv-lock-reveal` | A small inner pill: `height: 28px; margin-right: 6px; padding: 0 10px; background: transparent; border: 1px solid #333333; border-radius: 3px; color: #c9c9c9; font-family: var(--vntv-display); font-size: 11px; letter-spacing: 0.12em; text-transform: uppercase`. When `:focus-visible`: `outline: 2px solid #ffffff; outline-offset: 2px`. |
| ~~`.vntv-lock-unlock`~~ | Used together with `.vntv-link-cta`: `width: 100%; justify-content: center; margin-top: 6px`. `[aria-disabled="true"]` gives `opacity: 0.8; cursor: progress`. |
| `.vntv-lock-status` | `min-height: 18px; font-size: 12px; color: #c9c9c9`. `[data-tone="error"]` gives `color: var(--vntv-red-text)`. |
| `.vntv-lock-hint` | `font-size: 11px; line-height: 1.45; color: #c9c9c9` |
| `.vntv-textkey` (shared controls, not the lock section: the let-in footer uses it too) | **The small text key** (Try again, Link your stream): `display: inline-flex; align-items: center; gap: 6px; width: auto; height: 26px; padding: 0 8px; border: 1px solid transparent; border-radius: 3px; background: transparent; color: #c9c9c9; font-family: var(--vntv-display); font-size: 11px; letter-spacing: 0.12em; text-transform: uppercase; cursor: pointer`, at full opacity. `:hover` gives `border-color: var(--vntv-muted)`; `:focus-visible` copies `.vntv-relink`'s outline. `.vntv-relink` itself (a 26 px round icon button at 0.55 opacity, `styles.ts:5330-5346`) is **never** used for words. |
| `.vntv-lock-shackle` | `transform-origin: 16px 10px; transition: transform 180ms ease-out` |
| `.vntv-lock[data-unlocking="true"] .vntv-lock-shackle` | `transform: translateY(-2px) rotate(-28deg)` |
| `.vntv-lock[data-unlocking="true"] .vntv-lock-stage` | `animation: vntv-lock-out 260ms ease-in 160ms both` (opacity and a 0.98 scale) |
| ~~`@container vntv-lock (max-width: 340px)`~~ | The emblem is 88 px. |
| `@keyframes vntv-lock-out` | `from { opacity: 1; transform: scale(1); } to { opacity: 0; transform: scale(0.98); }`. Outside the reduced-motion block. `check-styles.py` does not check animation names, so a missing rule would only show as a swap that pops; the lab's slowed unlock (§10) is where to look. |
| `@media (max-height: 620px)` | The emblem is 72 px, with `margin-bottom: 12px`. |

Two additions go **inside** the reduced-motion block, which stays last:
`.vntv-lock-shackle, .vntv-lock-bar { transition: none; }` and
`.vntv-lock[data-unlocking="true"] .vntv-lock-stage { animation: none; }`.

**Contrast fix carried along.** `.vntv-hint.vntv-notice-error` (`styles.ts:5179-5181`) colours words with
`--vntv-red`, which is about 3.5:1 on black. That is against the file's own rule at `:48-53`. It becomes
`--vntv-red-text`. The lock screen never uses `--vntv-red` for words.

### 4.8 Refusals from anywhere make the webapp read access again

- **`client.ts`.** `requestJSON` (`:58-70`) throws a new `RequestError extends Error` with `status` and `code`,
  where `message` is still the server's text, so every caller that shows `.message` keeps working. `readError`
  returns both the text and the code.
- **Telling identity about a refusal.** `client.ts` exports `setAccessRefusalListener(fn)`. `requestJSON`, the
  WHEP request (`client.ts:902-925`) and `clipErrorOf` (`:582-601`) call it whenever the code is `locked`,
  `watch_only` or `not_linked`.
  - `index.tsx` wires the listener to `refreshIdentity()`, with a floor of one call per 2 s.
  - A listener is used rather than an import because `identity.ts` already imports `client.ts`, and the
    other direction would make a cycle.
  - Pictures and clip media load through `<img>` and `<video>`, whose 403s never reach this listener. The
    60 s re-read while a session is open (§3.9) covers them.
- **`types.ts`.** `ClipErrorCode` gains `'locked' | 'watch_only'`, and `clipErrorCopy` gains words for them:
  - `locked`: "VillaNET is locked."
  - `watch_only`: "You can watch, but not do that."

  `not_linked` gets the `locked` words.
- **`Me`** gains:
  - `access?: 'linked' | 'letIn' | 'locked' | 'unknown'`. It is optional only so that a missing field reads as
    locked.
  - `popOut?: boolean`
  - `letInAlert?: string`

  `publicSiteUrl` goes. The comment at `types.ts:268-273` changes to say that the panel decides from
  `access`, and never from `streamKey` or `clipsStreamer`.

### 4.9 How the lock is reached: openings and windows

| Opening | Behaviour |
|---|---|
| App Bar button, channel header button | Unchanged (`toggleRHSPlugin`). A press that opens VillaNET sets `openedByHand` (§4.5); a press that closes it sets nothing. A plain opening like this also clears anything pending (§7). |
| **Auto-open** (`index.tsx:196-222`, `me.openPanel`) | ~~Skipped unless access is linked or let in (Q1).~~ **Superseded by Victor's answer of 2026-10-05: the panel opens by itself for everybody the setting says, locked people included, straight onto the lock screen.** The bar is never focused on an automatic opening (§4.5), so nobody typing a message types it into the token field. Built that way (`index.tsx`, `initialize`). |
| **Return after a reply or a closed panel** (`away.ts`, `rhs-return-spec.md`) | **Not armed while access is not watch-capable.** The return exists to bring streams back, and a locked person has none. Bringing the lock back over the thread they were using would hide the thread behind a screen they cannot use, and Back to thread (`ReturnStrip`, which lives in the panel body, `Sidebar.tsx:1142`) would not be there to undo it. So `awayLogic.arm` (`:197-214`) gains the access as an input, beside `latchCandidate`'s, and sets neither `replyRoot` nor `returnOnEnd` unless it is `linked` or `letIn`. For watch-capable people nothing changes. A token typed but not sent is lost when the lock unmounts; a half-typed credential should not outlive the screen it was typed on. |
| **Popped-out panel** (11.3+, `index.tsx:344-346`) | The same PanelGate runs in that window with its own module instance. PanelGate listens for window `focus`, and for `visibilitychange` to visible, and calls `refreshIdentity()` at most once per 10 s. An unlock in another window therefore shows up when this one is looked at, even if this window does not receive the WebSocket event (UNVERIFIED; real-host check R7). A popout left visible on a second monitor gets neither, which is why a window with a session open also re-reads every 60 s (§3.9). PanelGate fits the window (§4.1). |
| **The `access` event** | `registry.registerWebSocketEventHandler('custom_ceo.villa.villanet_access', …)` calls `refreshIdentity()` now, and once more 12 s later to catch another node's copy (§3.9). It is registered in every window, and so is `registry.registerReconnectHandler`, which calls `refreshIdentity()` after a reconnect (§3.9, M10). |
| **Sign-out** (`followSignIn`, `index.tsx:239-255`) | It already calls `resetClips()` and `setIdentity(null)`. It also gains `stopAllSessions()` (`sessions.ts:745`), `clearWatching()`, and clearing anything pending (§7). Today the watch list outlives sign-out, so the next person on that page would inherit tiles and the mini-player. *Amended 3.1.0 (security pass): it also calls `forgetTagSearch()` (`tagSearch.ts`), before `setIdentity(null)`, and so does `uninitialize`. The hashtag search keeps the people and channels it had to fetch because Mattermost's page had not loaded them, and Mattermost answers those per reader: with Show Full Name off, first and last names go to system admins only (`getUsersByIds`, `model.User.Sanitize`). Kept past sign-out, an admin's answers were shown to whoever signed in next on the page. A counter bumped by `forgetTagSearch` keeps a search still on its way at sign-out from filling them again. Checked in `build/check-away.mjs` (the hashtag search checks, and that `index.tsx` calls it in both places).* |
| **Access drops** while the page is open (unlink, takeover, let-out, admin unlink) | A new `followAccess(store)` in `index.tsx` subscribes to identity and acts only on **definite** answers. **To `locked`** from linked or let in, it stops every session; clears the watch list; drops clip caches (`dropClipCaches`); closes the Clips dialog (`DialogState` becomes null) and the Trophies card; clears anything pending; and records the cause for the lock's line (§4.3): `self` when the drop follows this page's own successful Unlink, `unlinked` for any other loss of a link, `letOut` for a let-out. **From linked to `letIn`** (Q15), it closes the clip editor, drops drafts, moves a Clips dialog that is on Drafts or Just ended back to the list, and leaves sessions alone. **To `unknown`**, nothing is torn down: the panel stays with its sessions, shows its quiet notice (§4.3), and reads `/me` once more after 5 s. The server's 503 `store` already keeps anything new from starting, so a teardown would buy no security, and §3.1's rule that a blip shuts nobody out holds in the webapp too. |

---

## 5. Let in: the watch-only panel

**By role, not by place.** The panel tidy-up merges first, and it reworks exactly the footer and the way into
planning: variant A turns the footer into a dock whose title slot is Link Your Account for unlinked people and
whose Plan tool opens a link sheet (`linkWhy='plan'`); variant B moves the link form into a sheet too. So every
rule in this section and in §6 is stated for a **role**, wherever the merged panel puts it:

- the **link entry point**: whatever an unlinked person was offered in order to link;
- the **plan entry point**: anything that starts planning, including a tool that asks an unlinked person to
  link first;
- the **relink form**: a linked person's way to change their link;
- the **footer line**: the panel's own quiet status line, in the winner's footer or dock idiom.

Line numbers below are the pre-merge tree and only help find the role. §12 adds a mandatory step that maps
every role onto the merged tree before W3 starts.

`Sidebar` receives `access: 'linked' | 'letIn'`. Two flags replace every place that reads
`Boolean(me.streamKey)`, `linkOf()` or `linkedNow()` to decide what to show. As of 2026-10-05 those are:

- `Sidebar.tsx:209, 253, 257, 323, 765, 895, 915-935, 953, 1446, 1458, 1471, 1494, 1582, 1589, 1621`
- `SidebarTitle.tsx:41`, `ClipsRoot.tsx:106`, `Player.tsx:81, 110`, `ClipPostCard.tsx:152, 254`
- `clips.ts:92, 1416`

`streamKey` itself stays for what it is: the linked person's own key, used for relinking and What's on?. The
two flags are:

- `linked = access === 'linked'`
- `mayWatch = access === 'linked' || access === 'letIn'`

In `clips.ts`, `linkedNow()` splits in two:

- **`mayWatchClipsNow()`** is used by `loadClips` (763), `loadClip` (811), `sendCards` (1184), `askForCard`
  (1242) and `watchSharedClip` (1401).
- **`linkedNow()`** is kept for `startClip` (299), `retryCapture` (324), `editDraft` (332), the publish path
  (440, 494, 510, 521), `refreshDrafts` (947) and `dropClipCaches` (992).

**What a let-in person sees**

| Part | Let in |
|---|---|
| Live now | As today: Watch, the cards, preview pictures and faces. **No plan entry point** of any kind (pre-merge: the "+ Plan" pill, `Sidebar.tsx:1318`; variant A: the dock's Plan tool); `canPlan` is false. |
| Up next | Read-only: no **+** and no **Join** (`UpNext.tsx:542-556, 664-680`, already driven by `canPlan`/`canJoin`). The **×** shows on the person's own plans (a takeback) and, for a system admin, on every plan (Q4). |
| Tiles | As today. The scissors do not show (`scissorsView` returns null when not linked). C does nothing and does not leak, because the stray-key stop holds when the tile has focus (`Player.tsx:196-203`). Pop out is absent (Q10); for linked people it is a plain link (§3.10) when `me.popOut`. Hashtag search works. |
| Mini-player, away return | As today |
| Clips row | *(Removed 2026-10-05 at Victor's request; the header's clapper is the way into Clips.)* Shown, with no draft count, no "just ended" note (`clipLogic.ts:1503-1505`), and `tailHeld` false (`Sidebar.tsx:903`). |
| Clips dialog | The list and the viewer. **No** Just ended (`ClipsModal.tsx:137-205, 496`), **no** Drafts (`:210-327, 501`), **no** Yours filter (`:532-536`). In the viewer, the upvote count is static (`ClipCard.tsx:107` already does this when `!canVote`); Report and Delete are absent (`ClipViewer.tsx:655, 664` follow the server's flags); **Share is absent when `!clip.canShare`** (`ClipViewer.tsx:651-654` renders it unconditionally today). The empty-list copy becomes "No clips yet." with no invitation to make one. |
| Trophies card | As today. Clippers' Watch works (`linked === true` becomes `mayWatch` at `LeaderboardModal.tsx:444-451, 637-640`). The Watchers note **drops** its "Link your account to be counted." suffix (`:360`). |
| Header (`SidebarTitle`) | The clapper with **no** draft count, and the trophy. |
| Footer line (in place of the link entry point) | **One** quiet element in the merged panel's footer or dock idiom, wherever its link entry point was: an `EyeIcon` (12 px) and **WATCH ONLY** in Oswald capitals, 11 px, `--vntv-muted`, then a small **Link your stream** text key (`.vntv-textkey`, §4.7, with `LinkIcon`); it is a key, not a CTA, and nothing else prompts or explains. Pressing it opens the same token bar component as the lock screen (`TokenBar`, shared from `LockScreen.tsx`) wherever the merged panel opens its link form (inline, or in its sheet), with the label BEARER TOKEN, Show, a **Link** pill, a **Cancel** pill and the lock's key rule (§4.5). Escape or Cancel closes it and puts focus back on the key. Success makes access linked, and the panel remounts (§4.1) with the full footer; focus follows the swap rule of §4.1. Victor sees one lab screenshot of this line in the winning style before W3 is signed off (§12). |
| Username-matched streamer (Q8) | When the snapshot attributes a live stream to them by username and it is being kept for clipping (today's `StreamerClips` case, `clipLogic.ts:1578-1590`), one line above the footer line: "Your stream is being kept for clipping. Link it to switch that off." It has no switch and no wipe. |
| Not shown | The stream title bar and What's on? (`Sidebar.tsx:1494-1617`), the hidden-stream row (`:1390-1402`), `StreamerClips` (`:1458-1466, 1621-1629`). |
| Not fetched | `fetchMyProfile` and `loadProfile` (`Sidebar.tsx:208`), `loadNowPlaying`, and drafts. |
| Preview stills | **Not taken** (Q6). `captureStillsWhileOpen` (`stills.ts:108`, used by Sidebar and `MiniPlayer.tsx:560`) and peeks run only when `linked`. Everybody else's stills still show. |

**Linked people** keep everything, including the relink form wherever the merged panel puts it (pre-merge:
the footer, `Sidebar.tsx:1404-1452`), with these changes:

- The label and placeholder say **bearer token** (Q12), and the form uses the lock's bar and key rule (§4.5).
- **An empty bar no longer unlinks.** Today the form opens empty (`openLink` clears `draftKey`,
  `Sidebar.tsx:743-748`), takes focus, and Enter sends `""`, which unlinks (`:1414-1424`). Now that unlinking
  locks the whole panel and stops every stream, that is one stray Enter too many. Enter or Save with nothing
  in the bar (after trimming and stripping "Bearer ") does nothing but show the hint "Paste a bearer token to
  relink."
- **Unlink is its own muted pill** in the form, with a two-press confirm in place (`useTwoClicks`,
  `twoClicks.ts`). Armed, it reads "Unlink? VillaNET locks for you until you enter your bearer token again."
  When `me.alsoLetIn` is true it reads "Unlink? You will be able to watch only, until you enter your bearer
  token again." The second press sends `""`. On success, `followAccess` does the rest (§4.9), with the cause
  `self`.

---

## 6. Removed or simplified, and what stays on the server

**Removed from the webapp.** These are unreachable, since only a locked person ever saw them and a locked
person now sees the lock screen. They are stated by role (§5); §12's mapping step records where each one
landed in the merged tree.

| What | Where |
|---|---|
| The **link entry point** for unlinked people and its tooltip, wherever the merged panel puts it (pre-merge: the footer's **Link Your Account** CTA; variant A: the dock's title slot; variant B: the sheet's link button). For let-in people it becomes the footer line (§5). | `Sidebar.tsx:1471-1484` (`:1477`) |
| Any **plan entry point** that asks an unlinked person to link first (variant A: the Plan tool, "link your account first", `linkWhy='plan'`). The plan entry point is absent unless `linked`. | after the merge |
| Focus fallbacks to `linkButtonRef` | `Sidebar.tsx:594-601, 787, 1094` |
| The link-request plumbing, **repurposed** as `openLock(pending?)`, with two targets. A **locked** page opens the panel to the lock in `main` **and** `channel-popout` windows (`askToLink` today opens it only in `main`, `clips.ts:686-691`); an `other-popout` window keeps the line "Open VillaNET in the main window to unlock it." (§7). A **let-in** page opens the footer's `TokenBar` (today's `requestLinkForm` path). | `identity.ts:108-148`, `Sidebar.tsx:757-770`, `clips.ts:686-691` |
| The Clips dialog's `LockedView` and its focus target | `ClipsModal.tsx:86-114, 574-575, 679-680` |
| The scissors' link row, and the link branch of `pressScissors` | `clipLogic.ts:507, 549-551`; `Player.tsx:138-139` |
| The clapper's and the Clips row's locked labels | `clipLogic.ts:1436-1446, 1484-1485` |
| The Clippers tab's locked CTA | `LeaderboardModal.tsx:453-468` |
| The shared card's in-card link row | `ClipPostCard.tsx:278-298` |
| `CLIP_NOT_LINKED` and `CLIP_NOT_LINKED_ONE`. The editor's `not_linked` copy becomes its `watch_only` copy: "Your stream is no longer linked. Link it again to publish this draft (kept until {time})." with the link action. `followAccess` closes the editor on linked to let in (§4.9), so this is the belt for a draft open in another window. | `clipLogic.ts:1042-1043, 1077-1087` |
| The editor's "link" action, which now calls `openLock()`: for a let-in person that opens the footer's `TokenBar` | `ClipEditor.tsx:773-776` |
| `.vntv-clips-locked*` styles, and `.vntv-link-cta-row` if nothing else uses it (`build/check-styles.py` reports orphans) | `styles.ts:6472-6506, 5184-5188` |
| README lines describing the in-panel CTA | §9.3 |

**Kept** (`.vntv-link-cta` is reused by the Unlock pill): `linkStream`, `submitLink` (the linked footer), and
the footer form.

**Stays on the server**

- Every existing linked check (§3.7). They are what makes the lock real for anybody who skips the webapp.
- Layer 1 adds a check in front of them. It does not replace them.

---

## 7. Outside the panel

| Surface | Locked | Let in / linked |
|---|---|---|
| LIVE custom status (`presence.go:157`), the red dot, medals and badges beside names (`PostAwardBadges`, `AwardList`) | Visible, unchanged | Unchanged |
| Profile popover live row, art and voice (`PopoverLive.tsx`) | Visible. **Watch** sets pending `{kind:'stream', handle, name}` and calls `openLock()` with `openedByHand`; it **does not** add to the watch list. The lock's line names the stream (§4.3). After unlock, PanelGate takes the pending stream and calls `watchStream(handle)`, but only if the handle is still in the snapshot; otherwise it drops it silently. | Unchanged (`watchStream` adds and opens) |
| Shared clip card (`ClipPostCard.tsx`) | The card is drawn from the post's own props, as unlinked readers' cards are today (`clipLogic.ts:391-392`): no thumbnail and **no card request**. The state formerly called `unlinked` is renamed `locked`. **Watch** and the picture button call `openLock({kind:'clip', clipId})`. The in-card link row is gone. `unknown` keeps today's refresh-then-decide (`:243-262`). | Server card, thumbnail, and Watch through `watchSharedClip` |
| A card in a window **with no panel** (a thread popout, `windowKind()` other than main or a channel popout) | Watch and the picture are replaced by one muted line: "Open VillaNET in the main window to unlock it." There is nothing to open in that window. | Unchanged |
| Clip link route (`ClipLinkRoute.tsx` to `clips.ts:1414-1458`) | It keeps the clip pending as today's `{view:'locked', clipId}` `DialogState` (`clips.ts:631`) and opens the panel to the lock. `ClipsHost` renders **nothing** while access is not watch-capable. On unlock, the pending state becomes the viewer on that clip. | Opens the clip |
| Clips dialog and editor (`ClipsRoot.tsx:104-181`) | Not rendered. Pending state is kept. | As §5 |
| Mini-player (`MiniPlayer.tsx`, `away.ts`) | **Never.** `MiniPlayerHost` renders null unless the access is watch-capable. The watch list is also empty, because `followAccess` and sign-out clear it. | Unchanged |
| Header (`SidebarTitle.tsx:38-123`) | **The wordmark only.** No clapper and no trophy, so there are no Trophies for locked people. Starting and `unknown` look the same. | Let in: the clapper without drafts, and the trophy. Linked: as today. |
| Settings → Plugin preferences (`userSettings.ts`) | Mattermost draws these, so they are unchanged. | Unchanged |

**Pending** lives in a new `lockPending.ts`.

- It holds only the latest item: a stream, or a clip id through `DialogState`.
- It is in memory only, for this page, and **stamped**: it lapses after 10 minutes (`PENDING_MS`). An unlock
  hours later must not start somebody's stream unasked.
- It is cleared on sign-out, on access dropping, once it has been used, and on a **plain opening** (the App
  Bar or header button), which asks for VillaNET and not for that stream. A locked person gets no return
  after a reply (§4.9), so nothing else carries it.
- The lock **subscribes** to it, so a second Watch pressed while the lock is open changes the line in place.

---

## 8. System Console

### 8.1 The Access card (`components/AdminAccess.tsx`, new)

**`plugin.json`**

- A new section, `access`, with the display name **"Access"**. It goes directly after the Broadcast Box
  connection section, or in whatever position that section takes after the System Console cleanup merge.
- Its section help text:

  > Everybody, system admins included, sees a locked VillaNET panel until they unlock it with the bearer token
  > their streaming software publishes with. People you let in here can watch streams and clips without a
  > token, but cannot clip, vote, share, plan or use What's on?. The System Console works without a token.

- Two settings (*one since 2026-10-06*):
  - `{"key":"AccessList","display_name":"Let in without a token","type":"custom"}`;
  - ~~`{"key":"AccessContact","display_name":"Who to ask","type":"text","default":"","help_text":"Shown on the lock screen to people with no token: Just watching? Ask (this) to let you in. For example @victor. Leave it blank to say a system admin. Plain text, at most 64 characters."}`
    (Q17). Mattermost draws and saves it; `/me.accessContact` carries it (§3.8).~~ **Removed 2026-10-06**
    with the line it fed ("Don't advertise that"); an old configuration or backup that has it loads and
    restores with the key ignored (the box at the top).
- It is registered as `registry.registerAdminConsoleCustomSetting('accesslist', AdminAccess, {showTitle: true})`
  (M9).

**The card** uses the unified admin card from the System Console cleanup. Until that merges, use the
`.vntv-admin` and `.vntv-admin-line` classes and the `.vntv-pill` controls that AdminBirthdays and AdminStills
use.

1. **Add row.** A `.vntv-input` with the placeholder "username", accepting `@alice` or `alice`, then a **Let in**
   pill. Enter submits. While saving, the pill reads "Saving…". Errors show the server's text in
   `.vntv-notice-error`.
   - Success says "@alice can now watch." On `linked:true` it adds " They have linked a stream, so this only
     matters if they unlink."
2. **Count.** "3 of 500 let in."
3. **List.** One row per person: avatar (`Avatar.tsx`), display name and `@username`, "let in {date} by
   @{admin}", a tag for **Deactivated** or **Account deleted**, and a **Remove** pill.
   - Removing needs no confirmation, because it can be undone. The row stays, greyed, with **Undo** for 10 s.
     Undo is a fresh POST by username, and is impossible for a deleted account.
   - An admin's own row shows "(you)" (Q3).
4. **Empty state.** "Nobody is let in. Everybody needs their own bearer token."
5. **Problem.** `problem` from the GET is shown in `.vntv-notice-error` at the top. With `readOnly`, the add
   row and the Remove pills are disabled.
6. **Unlink somebody** (Q2). A separate small row under the list: an input, "username", and an **Unlink**
   pill.
   - It asks for confirmation in place, the way the AdminClips delete does: "Unlink @alice? Their stream
     stops counting as theirs at once. They can link it again with their bearer token unless you also revoke
     it in Broadcast Box." It does not promise a lock, because somebody on the let-in list keeps watching
     (Q15).
   - The result is worded from the answer (§3.3): "Unlinked. VillaNET is locked for @alice." when `letIn` is
     false; "Unlinked. @alice is still let in to watch." with a **Remove** pill beside it (the same DELETE as
     the row's) when `letIn` is true; "@alice had no linked stream." when `wasLinked` is false.

**No polling.** The card loads once on mount, and again after each change.

*As built (`AdminAccess.tsx`):*

- The card has no intro paragraph of its own; the section's help text says the same.
- **Undo** shows only for an **active** account. The server refuses to let in a deactivated account
  (400 `deactivated`) or a bot, so Undo would only fail there.
- A removed row keeps its place in the list's order while its Undo lasts. When a pill goes, focus
  moves to the next row, then the previous one, then the add box.
- A row's `name` is sent as `""` when it would only repeat the username (`letin.go`), and the card
  also ignores a name that differs from the username only in case.
- **Unlink** confirms in two presses, as planned, but the words are placed differently. The pill
  itself reads "Click again to unlink @alice", which screen readers also hear. The explanation, "Their
  stream stops counting as theirs at once. They can link it again with their bearer token unless you
  also revoke it in Broadcast Box, which by itself does not unlink anybody here.", is always shown
  under the heading **Unlink somebody's stream**, rather than only during the few seconds the
  confirmation waits. Enter in the box arms it, and Enter again confirms.
- The **Remove** beside "Unlinked. @alice is still let in to watch." belongs to that note, so anything
  that writes a new note takes it away. Pressing it moves focus to that person's row.
- While a request is out, the pills stay focusable (`aria-disabled`) rather than `disabled`, so focus
  is not dropped.

### 8.2 What the System Console must keep doing for a locked admin

- Every `/api/v1/admin/*` route skips layer 1 (§3.5).
- AdminStills' `stillURL` uses `GET /stills`, which is watch-or-admin (D1).
- AdminClips plays clips through `/admin/clips/video` (Q-A).
- T4 and T5 hold all of this. A builder adding a console card must use admin routes only. Grep the `Admin*.tsx`
  imports from `client.ts` as part of review. As of 2026-10-05, `stillURL` is the only non-admin one.

### 8.3 Settings backup: the list is left out

- `settings.go:19-35` gains: "OUT: who is let in to watch without a token."
- The file's own list of omissions names it.

**Why it is left out**

- It is per-person access state, like links, and not a setting.
- Restoring an older backup would silently re-admit people an admin has since let out, which is exactly the
  wrong failure for a lock.
- A settings file is a file in somebody's Downloads folder.
- The list is short to re-enter.
- A move to a new server keeps the KV store anyway, if it is a database move.

---

## 9. Other documents and the words

### 9.1 `design/clips-addendum.md`

Add a dated note under A§1:

> **Amended 2026-10-05 by `design/lock-screen.md`.** Rule 1's viewing (listing, opening, watching, the
> cards with their thumbnails) is for linked people **and people a system admin has let in**. Everything else
> in Clips stays linked-only. "Unlinked people see the Link Your Account call to action" becomes: locked people
> see the panel's lock screen, and let-in people see clips with no controls to act on them. In rule 7, a
> locked reader's card shows no thumbnail and its Watch opens the lock screen. A let-in reader's card is a
> linked reader's card.

**A§3.2**

- `requireClipsLink` and `requireClipsLinkSeen` are replaced by `requireClipsAccess` (lock-screen §3.6).
- `not_linked` is replaced by `locked`, and `watch_only` is added.
- The link cache becomes the access cache.

**A§3.3** gains a **Let in** column:

- GET `/clips`, `/clips/clip`, `/clips/cards`, `/clips/video` and `/clips/thumb` are as for a linked user,
  with all `can*` flags false.
- Every other route is 403 `watch_only`.
- The **Unlinked** column is renamed **Locked**, with 403 `locked`.

**A§3.7** adds the two codes. **A§3.8** drops the locked view. Q-B reads "somebody not linked (locked or let
in)". Q-A and Q-C stand.

### 9.2 `design/twitch-live.md` (3.1.0)

- **Decision 11** adds: "nor does it unlock the panel or let anybody in". §4.8 says the same.
- **Classes for the §4.1 routes** (D9):

  | Route | Class |
  |---|---|
  | `connect-ticket`, `connect` | **linked** (Q7) |
  | `callback` | Open as a route, **but** after its session checks it runs `accessOf` with the same class as `connect`. A state issued while the person was allowed stays valid for 10 minutes (twitch-live.md:61, :497), so a let-out or unlink inside that window must still stop the connection: a caller no longer allowed gets the "not allowed" HTML page, and the state is discarded. |
  | `DELETE link`, `notice` | open (takebacks) |
  | `preview` | watch |
  | `/admin/twitch*` | admin |

  `connect` and `callback` sit before the user-id check, so `connect` runs `accessOf` itself, after the
  session checks in §4.2 there.
- **Decision 10** ("Disconnect is always offered to a connected person") is kept for **locked** people too:
  the lock screen shows a small **Disconnect Twitch** text key ~~under the viewer row~~ (*2026-10-06: on a
  line of its own under the docked bar, at the right, or under the not-set-up sentence when there is no
  bar; after Unlock in the tab order*), when `/me` says the person is connected. Its words take focus after
  a press, and the lock's root when they lapse (the box at the top). Twitch's agreement requires deletion on request (DSA §VI.F).
- **Decision 13** "Anyone signed in" becomes "anyone linked" (Q7). This supersedes the 3.1.0 answer that
  anyone may connect, which predates the lock.
- **§4.9 `TwitchConnect`** renders for linked people only, never in the let-in footer line, and never on the
  lock screen except Disconnect.
- **§6.** Twitch tiles exist only in an unlocked panel. Nothing more is needed.

### 9.3 `README.md`

*As built (2026-10-05):* the section was not renamed. "How a person gets a red dot" keeps its heading,
which other text links to, and a new **"Unlocking VillaNET"** section goes before it. That section
covers the lock screen, the bearer token, the token-in-message guard, let-in people and the Access
card, admin unlink, relinking and unlinking yourself, the auto-open answer, outside the panel, access
changes, and what the server holds to and where it stops. The README also gains a **"The footer
bar"** section for the tidy-up. Its Settings table gains the two Access rows (one since 2026-10-06,
when **Who to ask** went), and the Public watch URL row takes the text below.

- **The "Link your account" section** (around line 278) becomes **"Unlocking VillaNET"**:

  > The panel is locked for everybody, system admins included, until they unlock it with their own **bearer
  > token**: the publish token their streaming software uses (OBS calls it Bearer Token). Broadcast Box
  > matches it to their stream, which is how VillaNET knows the stream is theirs. A system admin can also
  > **let people in** without a token, in System Console → VillaNET → Access; they can watch streams and
  > clips, and nothing more. Outside the panel nothing is hidden: live badges, medals, the profile popover
  > and shared clip cards show for everyone, and their Watch buttons open the lock.

- **Lines 35, 49, 75-79, 82-84, 287-290 and 654-664.** "Not linked" becomes "let in (watch only)", and "the
  Link Your Account button" becomes "the lock screen" or "Link your stream".
- **Line 533** ("linked or not" × on your own cards) stands.
- **Line 234 (Public watch URL).** "Only used for the **Pop out** link, which goes through VillaNET and is
  offered only to people who have linked a stream. A key it hands out keeps working after an unlink, so leave
  this blank unless you need it."
- **Settings table.** Add the **Access** row.

### 9.4 `LOCKDOWN.md`

Replace `:202-204` ("Any Mattermost user can watch any stream…") with:

> Only people who have unlocked VillaNET with their own bearer token, or whom a system admin let in, can
> watch a stream, see a preview picture or watch a clip; the plugin refuses everybody else on the server,
> whatever their browser does. Two exceptions are deliberate: a system admin sees the preview pictures and
> plays clips in the System Console without a token. What the lock does **not** cover: the list of who is
> live, with titles, the schedule, the boards and the voice roster, still reaches every signed-in browser,
> because the badges and the profile popover outside the panel need it and it is one broadcast; a stream
> already playing when somebody is let out or unlinks keeps playing in a browser that does not cooperate
> until that stream ends, which for a 24/7 channel is indefinitely (it cannot be restarted); revoking a token
> in Broadcast Box does not unlink anybody here, so use System Console → VillaNET → Access → Unlink as well;
> and with **Public watch URL** set, Pop out hands linked people stream keys, which keep working on the public
> watch page and at Broadcast Box after any unlink and cannot be taken back here, so leave it blank. A wrong
> token answers 403 from a plugin route, so keep the fail2ban filter's `/plugins/` exemption
> (`fail2ban-npm-filter.conf`).

### 9.5 `design/STATUS.md` and `design/rhs-return-spec.md`

- **STATUS.md.** Under "What 3.0.0 added", add item 7, "Lock screen", which points here. *As built: it is
  item 8; item 7 is the panel tidy-up, which landed first.*
- **Real-host checklist.** Add:

  | Check | What to confirm |
  |---|---|
  | R1 | Locked panel on Mattermost 9.3, 10.11 and 11: opening and focus (§4.5) |
  | R2 | Unlock with a real token |
  | R3 | A wrong token shows "not accepted" |
  | R4 | **What the deployed Broadcast Box answers for an unknown token.** Expected 204 (BB1). If it is anything else, add exactly that status to `not_accepted` in a follow-up. |
  | R5 | Let in, then let out, while watching: tiles stop, and the lock comes back |
  | R6 | A takeover by another account locks the first one |
  | R7 | The 11.3+ popout follows an unlock made in the main window |
  | R8 | A locked admin's System Console, including the stills pictures |
  | R9 | Pop out, in a browser and in the desktop app |
  | R10 | A card's Watch in a thread popout, and in a channel popout (11.3+) |
  | R11 | With a saved Mattermost password, in Chrome, Edge and Firefox: the bar is never offered that password, and unlocking never offers to save or update a password |
  | R12 | Ctrl/Cmd+V on Unlock, Show and Try again lands in the bar, never in the message box; a token-shaped message is refused before it is posted |
  | R13 | Let out while the WebSocket is down, then reconnect: the panel locks. A popout left visible on a second monitor stops within 60 s |
  | R14 | Phone: iOS Safari does not zoom when the bar is focused, and opening the lock does not raise the keyboard |

- **`rhs-return-spec.md`.** One sentence: a person whose access is not watch-capable gets no return after a
  reply or a closed panel, because there is nothing of theirs to bring back.

### 9.6 `plugin.json` help texts

- **Open the panel by itself** (`OpenPanel`): add "Only for people who can watch (linked or let in); everybody
  else opens it themselves."
- **Auto-match stream keys to usernames** (`AutoLinkByUsername`): add "A username match never unlocks the
  panel; only a bearer token or the Access list does."
- The Access section's own text is in §8.1, as was `AccessContact`, which went on 2026-10-06.

*As built: not done in 3.0.0's build.* The System Console cleanup, which is still being prepared in a copy of
the tree, rewrites exactly these help texts (and renames both settings, to **Open the panel automatically**
and **Match stream keys to usernames**). Editing them here would only make that merge conflict, so they are
left for whoever merges it. The first sentence above is also wrong now: by Victor's answer to Q1, the panel
**does** open by itself for locked people. Wording for the merge:

- `OpenPanel`: "For somebody who has not unlocked VillaNET it opens onto the lock screen, and never puts the
  bearer token bar in focus."
- `AutoLinkByUsername`: replace "use **Link Your Account** in the panel" with "unlock VillaNET with their
  bearer token", and add "A username match never unlocks the panel; only a bearer token or the Access list
  does."
- `EnableClips`: "Making, upvoting and sharing clips is for people who have linked their stream with their
  bearer token; people a system admin lets in under **Access** can watch them."
- `PublicSiteURL`: "Pop out is offered only to people who have linked a stream, through the plugin. The page
  it opens has the stream key in its address, and that key keeps working after an unlink, so leave this
  blank unless you need it."
- The `access` section is in the main tree's `plugin.json`, directly after `origin` (Broadcast Box). The
  console copy has none, so the merge must carry it across into whatever place the cleanup gives the
  Broadcast Box section (§8.1), with `AccessList` registered as `'accesslist'`.

*Done in the console merge (5 October):* all four are rewritten in the cleanup's short style, and so
are **Show who is watching** (it says the stream list that carries the faces reaches every signed-in
browser, locked ones included) and the settings backup's list of what the file leaves out (the
let-in list). The `access` section stays directly after Broadcast Box, second of seventeen. Its
subtitle is one line, as every section's now is ("Locked until each person enters their own bearer
token, and who may watch without one."). The page's header already says that everybody, the admin
included, unlocks with their own token, so `AccessList`'s help text, which Mattermost shows under the
card, says only what being let in allows and that the System Console works without a token. The Access card is drawn from the cleanup's card parts
(`adminParts.tsx`): its Unlink confirms the way every other card does, the pill turning red as
**Unlink?** and the status line saying "Click again to unlink @alice."; the explanation stays under
its heading, and the pill points at it with `aria-describedby`.

---

## 10. Lab

`webapp/lab` must reach every state, so the lab renders **PanelGate** (the registered component), not
`Sidebar`, as the panel.

**Access row** (replaces the Linked row, `lab/index.tsx:935-962`): **linked**, **let in**, **locked**,
**unknown**, and **starting** (`/me` held for 2 s). Buttons drive a live change, each through the same handler
the real event, timer or reconnect uses:

- **Let out now:** the server begins refusing with `locked`, a tile's next request carries it, and the panel
  flips to the lock with tiles stopped.
- **Let in now:** while the lock is showing, the world flips to let in and the `access` handler runs: the
  hidden "An admin let you in to watch." and the unlock transition.
- **Unlink while let in:** linked to let in, with the clip editor and a draft open. The editor closes, drafts
  go, the panel remounts watch-only, and sessions keep playing.
- **Unlocked elsewhere:** the identity changes; it is applied on the next **Window focused** press.
- **Window focused:** dispatches `focus` and `visibilitychange` on the window. Scripted `element.focus()` fires
  no focus events while the lab pane's window is unfocused, so a button is the only reliable way to look.
- **Socket down / Reconnect:** let out while "down" (the access event is dropped); Reconnect runs the
  reconnect handler and the panel locks.
- **Clock +60 s:** runs the while-playing re-read (§3.9). With the world let out silently, it locks.
- **Store blip:** the next `/me` answers `unknown` once. A linked page keeps the panel, its tiles and the quiet
  notice; a fresh page shows the NO SIGNAL face.

**Lock screen.** A switch picks what the next `linkStream` mock answers:

- success (once a token of 6 or more characters is entered; the lab never holds a real token)
- 403 `not_accepted`
- 429 `rate_limited`
- 502 `unreachable`
- 502 `bad_name`
- 503 `not_configured`
- 400 `too_long`
- 500 `store`
- 400 `bad_request`
- 200 with an empty `streamKey` (worded as not accepted)
- no answer

Alongside it are the existing **Server slow** toggle (for `checking`) and a **Not set up** toggle, which shows
the notice and hides the bar.

**Lock states to look at:**

- empty submit (no request is made), and "Bearer " alone counting as empty;
- masked, revealed, and the reveal hiding itself after 15 s, with a lab clock button;
- each line variant: none, a pending stream, a pending clip, own Unlink, unlinked another way, let out, and
  the NO SIGNAL face;
- ~~the viewer row with a **Who to ask** text, blank, and as a system admin; hidden when not set up;~~
  (*gone 2026-10-06, and the lab's chip with it; the **every admin alert** chip now shows that a system
  admin's lock is everybody's*)
- the bar's edge at rest, not only focused.

**Keys and the message box.** The lab's **Mattermost's own habit** switch (`lab/index.tsx:264-288`) skips
every key pressed with Ctrl or Cmd, so today it cannot show a paste going to the message box. It gains
Ctrl/Cmd+V from any element that is not an INPUT or TEXTAREA, as Mattermost does (M8). With it on:

- click Unlock with a wrong token, get refused, press Ctrl+V and then Enter: the paste is in the bar and
  nothing reaches the message box. The same on Show, Try again and ~~an admin-alert link~~ Disconnect Twitch's
  words (*2026-10-06*), and in the let-in
  footer's `TokenBar` and the linked relink form;
- Space on Unlock still presses it;
- typing a made-up UUID (`00000000-0000-4000-8000-000000000000`) in the lab's message box and pressing Enter
  while locked shows the post hook's refusal, and does not while linked with no token bar open.

**Mattermost focuses the side panel** (new switch). It mimics `sidebar_right.tsx`: a `setTimeout(0)`, then
`focusElement`'s second `setTimeout(0)`, then focus on the first focusable element in the lab RHS (its header
button). Without it, "the bar takes focus" would pass trivially.

**Openings**

- by button, with the side-panel focus switch on and off: the bar ends up focused, through the guard;
- a header press that closes the panel, then a return after a reply as a linked person: the stale flag
  focuses nothing;
- auto-open: never for a locked person;
- after a reply while locked: no return, and the thread stays (`awayLogic`'s "locked: no return" case);
- popout window: the lock fills it, centred, with the bottom banner at the bottom;
- phone width (375) with "coarse pointer" on: no focus on opening; and the focused bar at 16 px;
- heights 560 and 900; at 560 with both banners (~~and every admin alert~~, none on the lock since
  2026-10-06), scrolled to the top, the emblem shows;
- each swap with focus in the lab's message box (unlock, Let in now, linking from the let-in footer,
  Unlink): focus stays in the message box;
- banner top, and banner both;
- reduced motion: instant swap, no sweep;
- a locked admin with every alert (*since 2026-10-06 the same as anybody's lock*);
- the unlock transition, at normal speed and at a slowed lab speed.

**Outside, while locked**

- Popover Watch opens the lock naming the stream, adds no tile, and plays after unlock.
- A shared card shows no thumbnail; its Watch and picture open the lock, and the clip opens after unlock.
- In a "thread popout" window kind, the card shows its line instead. In a "channel popout" window kind, its
  Watch opens that window's lock.
- A second popover Watch while the lock is open changes the line in place; an App Bar opening clears it;
  **Clock +10 min** lapses it.
- The clip link lands on the lock.
- The mini-player never appears.
- The header shows the wordmark only.

**Let in**

- Up next is read-only, with × on their own plan; for an admin, × on every plan.
- Tiles have no scissors, and C does nothing.
- The footer shows WATCH ONLY and the Link your stream key, and the bar opens, fails, and links (with a remount).
- The username-matched notice (`setStreamer` on the let-in person).
- The Clips row has no drafts.
- The dialog has no Just ended, Drafts, Yours, Share, Report or upvote button.
- Clippers Watch works.
- The Watchers note has no prompt.
- Shared cards fetch with thumbnails.
- A refusal with `watch_only` (a lab button sends a vote through the mock) triggers the identity refresh.

**Linked**

- Enter or Save on an empty relink bar does nothing but show the hint.
- The **Unlink** pill arms ("Unlink? …", and the let-in wording with `alsoLetIn`); the second press locks with
  "You unlinked your stream." and stops tiles.
- Preview stills are taken; as let in, they are not.

**Mocks**

- `meOf` (`lab/world.ts:1536-1547`) returns `access`, `popOut`, `letInAlert` and `alsoLetIn`
  (~~and `accessContact`~~, gone 2026-10-06).
- `fetchMe` honours the starting delay.
- The clip mocks (`lab/world.ts:1173-1174, 1272-1298`) set `canVote`, `canReport`, `canDelete` and `canShare`
  to false for let-in people, and refuse vote, report, share, drafts and delete with `watch_only`, or with
  `locked` when locked.
- WHEP and stills mocks refuse with `locked` when locked.
- The `access` WebSocket event is simulated by calling the same handler.

**The System Console card.** The lab's console page gets **AdminAccess** with mock list states: empty, three
people (one deactivated, one deleted, one "(you)"), full at 500, `problem` set, `readOnly`, an add error for
each code; add success with and without `linked:true`; Remove, then the greyed row with Undo for 10 s (on the
lab clock); a deleted account's row with no Undo; and unlink confirm and each result (`letIn` true with its
Remove pill, `letIn` false, `wasLinked` false).

---

## 11. Webapp checks

- **`lockLogic.ts`** (pure, imports nothing) is loaded by `build/check-away.mjs` beside `awayLogic`,
  `miniLayout` and `clipLogic`. Checks:
  - `accessOfMe`: `null` gives unknown; a missing field gives locked; a user mismatch gives locked; and the
    three words.
  - `lockCopy`: every code in §4.3, an unknown code falling back to the server's text, and an empty-key 200
    giving not accepted.
  - The reducer: empty submit sends nothing; `checking` ignores a second submit; typing clears an error;
    success clears the token.
  - The line chosen for each pending kind and each loss cause (`self`, `unlinked`, `letOut`).
  - `mayWatch` and `linked`.
  - `shownAccessOf`: a definite answer wins; `unknown` keeps the last definite one; with none, `unknown`.
  - `looksLikeToken`: a UUID, with and without "Bearer " and surrounding spaces, is a token; a UUID inside a
    sentence, a short word and an empty message are not.
  - The empty check: "", spaces and "Bearer " alone are empty.
  - `openedByHand` freshness (inside 1 s, not after) and pending lapsing after 10 minutes.
- *Added 2026-10-06:* every word the lock's logic can say (`lockLine` for each reason, `lockStatus` for
  each phase and code, `LOCK_NOT_SET_UP`, `LINE_NO_SIGNAL`) is held to a pattern that refuses a way in
  ("let you in", "just watching", "who to ask", "System Console", "needs attention", "without a token",
  "watch only"); `LockScreen.tsx`'s code, comments aside, to the same pattern and to never reading
  `isAdmin`, `AdminAlerts` or `accessContact`; and no `accessContact` or "Who to ask" anywhere in the
  webapp or the lab. `build/check-twitch.mjs` holds `disconnectLineShown`, which keeps Disconnect
  Twitch's words in the page for focus to go to.
- **`clipLogic.ts`:** `scissorsView` is null for let-in; `clapperView` has no draft count for let-in; the
  card state is `locked` versus server for each access; the copy for the `locked` and `watch_only` codes.
- **`awayLogic.ts`:** `latchCandidate` returns nothing, and `arm` sets neither `replyRoot` nor `returnOnEnd`,
  when the access is not watch-capable (both gain that input).
- **The usual checks:** `python3 build/check-react.py`, which catches React 18 APIs such as `useId` and
  `useSyncExternalStore`; `tsc --noEmit`; `python3 build/check-styles.py`, which catches orphaned classes
  after §6's removals.

---

## 12. Build plan

**Before starting**

- The panel tidy-up winner is merged into `Sidebar.tsx`.
- The System Console cleanup is merged.
- A checkpoint copy of the project is taken, as is house practice.
- **Mandatory, after the merge and before W3:** grep the merged tree for `openLink`, `linkButtonRef`,
  `linkWhy`, `askToLink`, `requestLinkForm`, `linkedNow`, `Link Your Account`, `link your account` (any case)
  and `canPlan ?`. Classify every hit by its role in §5 (link entry point, plan entry point, relink form,
  footer line, or other) and against §6, and record the list in this document as **§12.1** before W3 starts.
  Then show Victor one lab screenshot of the let-in footer line in the winning style.
- Build in the main tree.
- **Every build prompt carries Victor's answers (top of this document) verbatim.**
- Toolchain: `export PATH="$HOME/.local/opt/go/bin:$HOME/.local/opt/node/bin:$PATH"` in the same shell call.
  On the laptop, use `TZ=UTC`.

**The webapp builder can start W1 as soon as S1's JSON exists.** This document fixes that JSON, so W1 can
start on day one against the lab mocks.

| Phase | Server builder | Webapp builder |
|---|---|---|
| **1** | **S1:** `access.go` (§3.1, with the agreement rule and the refusal memory); `letin.go` (§3.2, with the generation counter); the admin routes (§3.3); the `/me` fields (§3.8); the `/me/link` codes; `setLink` failing closed (§3.8); `accessChanged` and the WebSocket event (§3.9); `writeCodedError`. Tests T6, T7, T8, T9, T10, T13, T15. | **W1:** `types.ts` (`Me`, `ClipErrorCode`); `client.ts` `RequestError` and the refusal listener; `lockLogic.ts` with its checks; `lockPending.ts`; the identity wiring in `index.tsx` (the `access` event, the reconnect handler, the 60 s re-read while playing, the post hook, the refusal listener, `followAccess`, the sign-out additions, auto-open skipped). |
| **2** | **S2:** layer 1 and `routeClasses` (§3.5), with the top-of-`ServeHTTP` comment; the clip gate classes, `WatchOnly` and `CanShare` (§3.6); `requireLinkedStream` codes (§3.4); the picture cache headers (§3.7); the pop-out route, with `watchUrl` and `publicSiteUrl` removed (§3.10). Tests T1-T5, T11, T12, T14, T16. | **W2:** `PanelGate.tsx` (with `fitPopoutWindow` and the swap focus rule), `LockScreen.tsx` with `TokenBar` and `keepTokenKeys`, the focus guard factored out of `away.ts`, `PadlockIcon`, `bannerStyleOf`, `AdminAlerts`, the motion constants, styles (§4.7, with `.vntv-textkey`), and the `.vntv-hint` contrast fix. |
| **3** | Review: grep every `case "/api/v1/` against `routeClasses`. Confirm no log line carries a token. Run `gofmt`. | **W3:** the let-in panel (§5); the removals (§6); `SidebarTitle`; the Clips dialog trims and `canShare`. **W4:** outside the panel (§7): popover, card, clip link, mini-player, Player pop-out link. |
| **4** | — | **W5:** `AdminAccess.tsx` and its `plugin.json` section and registration (§8). **W6:** the lab (§10). |
| **5** | Docs, by one writer: §9.1-§9.6. |

**Gates**

- **Server:** `TZ=UTC make check-server`, `make e2e`, `make e2e E2EFLAGS=-race`.
- **Webapp:** `TZ=UTC make check-webapp`; `make lab`, served on `127.0.0.1:8078`, with **every §10 state
  looked at** in screenshots; then `make dist-linux`.
- **Deployment:** only when Victor says go and is present, with the §9.5 real-host checks.

### 12.1 The merged tree, mapped (recorded after the build, 2026-10-05)

The tidy-up winner was design A, "One quiet bar". It makes the footer one bar whose tools open
sheets, one at a time (`Sidebar.tsx`, `type Sheet = '' | 'onnow' | 'plan' | 'clips' | 'link'`). Every hit
of the mandatory grep was classified by role, and this is where each landed:

| Role (§5, §6) | In the merged tree | Now |
|---|---|---|
| Link entry point | The **Link Your Account** button in the bar's title slot, for unlinked people | **Removed.** Let-in people get the footer line instead: **WATCH ONLY** and the **Link your stream** text key, which opens a `'link'` sheet holding `TokenBar` (variant `link`, with Link and Cancel). |
| Plan entry point | The calendar tool in the bar. Unlinked, it opened a link sheet first ("link your account first", `linkWhy='plan'`). | **The link-first branch is removed.** The calendar is in the bar for linked people only (`canPlan = linked`), and nobody else has a way into planning. |
| Relink form | Three homes, by state: a "Linked as … · Relink" row at the foot of the clip sheet while Clips was on; a small link key in the bar only while there was no clip sheet; and a Relink button of its own in the "title can't be set" state | **Moved** to its one home, a **link key** (`.vntv-bar-relinktool`) that is always in a linked person's bar, whether or not Clips is on. It opens the `'link'` sheet, headed **Relink your stream** with "Linked as {key}", holding `TokenBar` (variant `relink`, with Save) and the **Unlink** pill. In the "title can't be set" state the key is lit red (`data-attention`) instead of offering its own button. It fits at 320 px with the What's on? poster set. |
| Footer line | — | Let in: one quiet line, as above. Linked: the stream title bar with its tools. |
| Other link prompts | The Clips dialog's `LockedView`, the scissors' link row and `pressScissors`'s link branch, the card's in-card link row, the Clippers tab's call to action | **Removed** (§6). |
| `askToLink` | `clips.ts` | **Became `openLock(pending?)`** in `lockPending.ts`, with two targets: the let-in footer's bar, or the lock. |
| `requestLinkForm` / `takeLinkRequest` / `useLinkRequest` | `identity.ts`, for the footer form | Kept, and used only by `openLock` for a let-in reader (the clip editor's Link your stream). |
| `linkedNow` | `clips.ts` | Split into `mayWatchClipsNow()` (the list, a clip, cards, a shared clip's Watch) and `linkedNow()` (capture, drafts, publish), as §5 says. |

Victor accepted two grafts with design A, and both are built. Relink has one home (above). The clip sheet's
**Wipe** is now the panel's all-caps Oswald pill, in the lighter red; armed, it reads **CLICK AGAIN TO
WIPE**, white on red. The username-attributed notice is no longer a bar matter. It is §5's let-in line
(Q8), above the WATCH ONLY line.

---

## 13. Questions for Victor (the build uses each default until he says otherwise)

**Answered by Victor on 2026-10-05 (binding):** admins CAN unlink someone (the Access card's two-press Unlink);
the panel DOES open by itself for locked people when the existing auto-open setting says so - straight onto the
lock screen (this overrides the default of question 1); connecting Twitch (3.1.0) is for LINKED people only. Every
other question keeps its default. Earlier binding answers: admins are locked like everyone (System Console works
without a token); admins can let people in; let-in people are watch only; badges, medals, popover info and clip
cards stay visible outside the panel and every action from them opens the lock screen.


Every default keeps to his answers. An alternative that loosens them is built only if he says yes.

1. **Should the panel still open by itself for a locked person** (Open the panel by itself: When somebody is
   live / Every time)? **Default: no.** It opens by itself only for people who can watch. Locked people open it
   themselves.
2. **May a system admin unlink somebody from the Access card?** Today nobody can, and revoking a token in
   Broadcast Box does not unlink (BB2), so without this a linked person can never be locked out again.
   **Default: yes** (§3.3, §8.1).
3. **May a system admin let themselves in?** **Default: yes.** An admin can already do anything on the
   server, and refusing it would be theatre. Their row says "(you)". Alternative: refuse your own username.
   *(2026-10-06: still yes, in the Access card; the lock no longer says so.)*
4. **May a let-in system admin cancel other people's plans from Up next?** The System Console has no
   plan-cancel of its own. **Default: yes.** This is moderation, not planning. Deleting clips stays in the
   System Console.
5. **Should let-in people's watching count on Top Snoopers?** **Default: no.** The board stays for linked
   people, as today.
6. **May let-in people take and share the Live now preview pictures?** **Default: no.** A still they upload
   becomes the picture on that stream's Live now card for everybody, which is publishing, not watching, and
   the server reads only its header (`stills.go:861-871`). Linked viewers still take them, so the cards keep
   their pictures. Alternative (loosens his answer): yes, with the existing System Console block.
7. **3.1.0: may let-in people Connect Twitch?** **Default: no**, linked people only. Connecting makes them a
   streamer whose Twitch live shows to everybody and counts on the airtime boards, which is more than
   watching. This narrows his earlier 3.1.0 answer ("anyone may connect"), which predates the lock: a
   Twitch-only streamer now needs a Broadcast Box token, or a yes here. Disconnect is offered to anyone
   connected, locked people included, on the lock screen. Alternative (loosens): linked or let in.
8. **A let-in person whose live stream is matched to them by username, and is being recorded for Clips: tell
   them?** **Default: yes.** One line with no switch: "Your stream is being kept for clipping. Link it to
   switch that off."
9. **The stream list (who is live, titles, the schedule, boards, voice) still reaches locked browsers,**
   because the badges and popover outside the panel use it. **Default: leave it.** Nothing in it plays video,
   and Victor asked for those outside surfaces to stay visible.
10. **Pop out.** **Default: keep it for linked people only**, through the new redirect, with no stream key in
    the broadcast (§3.10). A key it hands out cannot be taken back by a let-out or an unlink, so let-in people
    do not get it. Alternatives: remove Pop out altogether (it is blank on his server anyway, where LOCKDOWN
    runs Broadcast Box with its frontend off), or (loosens) let-in people too.
11. **Should the settings backup carry the let-in list?** **Default: no** (§8.3).
12. **Words: "bearer token" everywhere** a person reads it (lock screen, footer, README), with "publish token"
    kept only in code? **Default: yes.** It is his word and OBS's label.
13. ~~**Should a locked admin see the admin alerts on the lock screen** ("Only you can see this")?
    **Default: yes.** They come with `/me` at no extra cost, and an admin should not have to unlock to learn
    the airtime store is halted.~~ **Superseded 2026-10-06** by Victor's "Remove admin lines too": no
    admin alert on the lock, not even the one line variant C had. The System Console has them, and the
    panel shows them in full once unlocked.
14. **Should stored links be re-checked against Broadcast Box now and then**, so that a token revoked there
    locks the person here? **Default: no**, not in this build. Q2's Unlink covers it by hand. A periodic
    re-check would need a rule for when Broadcast Box is down, and gets its own design if wanted.
15. **Should a let-in person who links leave the let-in list automatically?** **Default: no.** They stay on
    it, and if they later unlink they drop back to watch only rather than to the lock. The admin can remove
    them, and the admin's Unlink says when somebody is still let in, with a Remove pill beside it (§8.1).
16. **Should the chat bridge stop relaying locked people's posts to Mumble?** The bridge relays every non-bot
    post in the bridged channel (`bridge.go:282-290`). **Default: no, unchanged.** It is the channel's chat,
    and who may post there is Mattermost's own channel permission, not the panel. Alternative: relay only
    linked or let-in people (one `accessOf` per post in that channel).
17. ~~**May admins set a "Who to ask" text for the lock screen** (for example "@victor")? **Default: yes**, an
    optional plain-text setting in the Access section; blank says "a system admin" (§8.1). Without it, a
    viewer with no token cannot tell whom to ask.~~ **Superseded 2026-10-06** by "Don't advertise that":
    the lock does not tell anybody that they can be let in, and the setting is removed.

---

## As built (3.0.0, 2026-10-05)

Built in the main tree in one day. The server half was built first, the webapp half against it, then a
review found 17 issues and fixers dealt with them. Every place the build differs from this contract is listed
here. Where a section above changed, it also carries a short *As built* note.

### Server

**Everything in §3 is built, and T1–T16 are written.** The new files are `server/access.go` (access
levels, the 10 s and 2 s memories, `routeClasses`, the front gate), `letin.go` (the list and the admin
routes), `watch_popout.go`, `tokenguard.go` and `access_test.go`. The `access` section is in `plugin.json`,
directly after Broadcast Box (`origin`). The new tests also passed five runs in a row under `-race`. As a
spot check, the code was broken six ways, and each break failed its test: locked people allowed to
watch, a read error swallowed in `setLink`, a dispatch added above the gate, the refusal memory dropped,
the both-entries rule dropped, and a route's class removed.

Where the server differs:

- **A server-side bearer-token guard on posts.** §4.5 put the post check in the webapp only.
  `tokenguard.go` keeps the same rule in `MessageWillBePosted`, and in `MessageWillBeUpdated` for an
  edit that changes the words, so a phone, another tab or anything else that posts gets it too:
  - From anybody not linked, it refuses a post that is nothing but a UUID, with or without one
    leading "Bearer ". It refuses the same from anybody whose access cannot be read just then.
  - From a linked person, it refuses only the token they linked with.
  - It leaves alone bots, webhooks, Mattermost's system messages, and a server where VillaNET is not
    set up.
  - Mattermost answers "Post rejected by plugin. That looks like your bearer token. Paste it into
    VillaNET's bar instead." [MM v9.3.0 server/channels/app/post.go:305-307, :704-708].
  - The limits are written in the file. It cannot see synced drafts, which are the reason the
    webapp's key rule is the real defence. A UUID inside a sentence passes. A thread whose root is
    nothing but a UUID, posted by somebody not linked, cannot be moved.
- **Link writes are stricter.** `setLink` deletes the reverse entry of the person's previous key
  only while it still names them. It takes over a previous owner only while that owner's forward
  entry is this very key. `clearLink` removes the reverse entry only while it names the person
  unlinking. Without these, the both-entries rule (§0 row 18) could have unlinked a third person.
- **"Linked" means both entries everywhere.** That includes Best Clipper's lookups and `streamerOf`.
  After review finding 1, it also includes the Top Snoopers record in `handleWHEP`, `linkedNow`,
  `watchingNow`, and `mayFetchPictureFor`, through the new `hasLink` (a failed read counts as no).
  This amends §3.7's "Top Snoopers does not change" and the §3.5 `media/image` note (both marked
  above). T16, T7 and `media_test.go` hold it.
- **The access event is sent on every successful let-in, let-out or admin unlink**, including one that
  changed nothing.
- **One test clock.** The access cache's clock is the clip store's by default, so a clip test that
  moves one moves both.
- **Smaller choices:**
  - The admin channel-picture preview is `private, no-cache` too.
  - An empty unlink request says "Say whose stream to unlink."
  - A let-in row's `name` is `""` when it would only repeat the username (review findings 3 and 4).

**Checks on the final tree.** After `go clean -testcache`, `TZ=UTC make check-server` passed: `go vet`,
`go test -race ./server/...` in about 33 s, gofmt, and the version check (3.0.0). `make e2e` and
`make e2e E2EFLAGS=-race` passed. The new and changed tests passed with `-race -count=5`.

### Webapp

**Everything in §4–§8 and §10 is built.** The new files in `webapp/src` are `lockLogic.ts`,
`lockPending.ts`, `access.ts`, `clock.ts`, `focusGuard.ts` and `banner.ts`, and in `components/` they are
`PanelGate.tsx`, `LockScreen.tsx` (which also holds the shared `TokenBar`), `AdminAlerts.tsx`,
`AdminAccess.tsx` and `TryAgain.tsx`. The card is registered as `'accesslist'`.

Where the webapp differs, or chose:

- **Auto-open follows Victor's answer**: everybody the setting covers, locked people included, opens
  onto the lock, and the bar is never focused on an automatic opening (§4.9, marked above).
- **Relink's one home** and the **Wipe** pill: see §12.1.
- **The Unlock pill stays while the lock opens**, reading UNLOCKED (§4.4, marked above).
- **Focus on an opening by hand** is taken at once, from anything but a text field (§4.5, marked
  above).
- **A paste is recognised as Mattermost recognises it**: Ctrl or Cmd with the key whose code is 86,
  whatever letter the keyboard layout types, so Russian, Greek, Hebrew and Arabic layouts are covered
  (`isPasteKey`, `focusTrap.ts`). The lab's "Mattermost takes stray keys" switch carries its own copy
  of Mattermost's rule, and `build/check-away.mjs` checks the key rule.
- **Escape in the footer's bearer token sheets** (Relink and Link your stream) closes the sheet from
  anywhere in it. In the bar, the first Escape clears an error and the next one closes. The lock's own
  bar is unchanged: Escape there only ever clears an error.
- **A refused token is masked again**, and Show goes back to Hide.
- **Screen readers are told more.** PanelGate's live region says "VillaNET is locked." and the reason
  line when the panel locks, "Linked." when a let-in panel becomes a linked one, and "Unlinked. You can
  still watch." the other way. The lock's region reads its line first, then the way in.
- **Try again** is one shared key (`TryAgain.tsx`). It says "Checking…" while it asks, and "Still not
  answering. Try again in a moment." when the answer is still unknown. It never retries by itself.
- **"The store didn't answer"** for somebody already known keeps their details (stream key, the clip
  switch, Pop out), and the panel shows only the quiet notice.
- **The lock's line is frozen during the unlock fade**, so the column does not jump. The starting face
  keeps the banners.
- **Unlink counts as the person's own from the moment it is sent**, so the right line shows even if the
  lock arrives before the answer.
- **Words** use typographic apostrophes (’), as everywhere else in the panel.
- **The Access card**: see §8.1's note.
- **The lock section of `styles.ts`** sits before "Clips: the dialog", because the
  `.vntv-clips-locked` block it was meant to follow is deleted.
- **Lab hooks in plugin code**: `pretendWindowKind` (`away.ts`), `slowLockOpening` (`motion.ts`) and
  `advanceClock` (`clock.ts`).

**Confirmed in the lab, with real mouse and keyboard input** (a small CDP driver), at 400 and 320 px, in the main
window and popped out:

- Keys typed on Unlock and Show land in the bar, and so does Ctrl+V on a Russian layout. On an
  ordinary button the same key goes to the message box, as in Mattermost.
- Space on Unlock with the bar empty says "Paste your bearer token first." and puts focus back in the
  bar. A token-shaped message is refused before posting.
- Opening by hand focuses the bar, with and without Mattermost's simulated move. Openings at phone size
  and automatic openings do not.
- An unlock or a let-in leaves focus in the message box when it was there.
- Each loss reason has its own line.
- Let out stops the tiles. Unlink while let in closes the editor and keeps the stream playing.
- A popover Watch or a clip link waits behind the lock and plays or opens after unlock, and lapses
  after 10 minutes.
- Reduced motion swaps at once and still announces.
- Let-in people take no preview stills.

**Approximations in the lab:**

- The "phone-sized" chip stands in for a coarse pointer.
- Reduced motion has no lab switch; it was checked with the browser's emulation.
- The paste test is a simulated Ctrl+V: the routing is confirmed, but a real clipboard paste is not
  (R12).

**Checks on the final tree.** `make check-webapp` passed: React 17 throughout, `tsc` clean, `styles.ts`
with 2 stylesheets, 1039 rules, 1103 selectors and 607 classes used, none undressed, and
`check-away.mjs` with 127 checks. `make lab` passed (`dist/villanet-tv-lab.html`, 1699 KiB). `make
dist-linux` had not been run when this was written. The installable bundle in `dist/` predates this
work and must be rebuilt.

### Not done

- **§9.6 help texts**: left for the System Console merge, with wording in §9.6's note.
- **Disconnect Twitch on the lock**: 3.1.0 does not exist yet (§9.2). `access.go` carries a comment with
  the classes its routes must get.
- **Already open before this work, and not caused by it:** in a narrow popped-out window the whole
  popout widens to about 917 px, which pushes the let-in footer's Link your stream key off screen.
  Linked panels do the same. It is tracked in `design/STATUS.md`.

### Docs (§9.1–§9.5), done 2026-10-05

- **README:** the new "Unlocking VillaNET" section, and the panel's description for the tidy-up.
- **LOCKDOWN:** §4's Pop out history, and "What this does not do".
- **design/STATUS.md.**
- **Notes** in `clips-addendum.md`, `twitch-live.md` and `rhs-return-spec.md`.

---

## Review log

Reviewed 2026-10-05 (security lens: S1-S14; UX lens: U1-U24). Every finding was checked against the code in
the main tree and, for Mattermost and Broadcast Box, against their source saved under
`/tmp/claude-1000/villanet-mm/`. None was rejected outright. Where a reviewer offered two fixes, the column
says which was taken and why.

| # | Sev | Finding | Checked against | Verdict and change |
|---|---|---|---|---|
| S1 | major | A token pasted while focus is on a lock control, or on `body`, goes to the message box, where synced drafts store it | MM v9.3.0 `post_utils.ts:224-262` (paste from any non-INPUT/TEXTAREA); `registry.ts:743-754`; `config.go:398, :883-884`; `Sidebar.tsx:1125-1137` | **Accepted.** §4.5: the key stop covers the whole lock and moves a paste or character into the bar inside the keydown; a `MessageWillBePosted` hook refuses token-shaped posts (`looksLikeToken`); the doc says the hook alone is not enough. M8, M10, M11, BB3 added; lab and R12. |
| S2 | major | Ending playback hangs on one event; a missed event leaves a stream playing, forever for a 24/7 channel | BB `whep.go:20` (POST and PATCH only); `api.go:676-678`; MM `registry.ts:733` (v9.3.0), `:965` (master) | **Accepted.** §3.9: reconnect handler, and a 60 s `/me` re-read while a session is open; §4.8 notes `<img>`/`<video>` 403s; LOCKDOWN text says "indefinitely"; lab Socket down/Reconnect and Clock +60 s; R13. |
| S3 | major | `setLink` fails open: swallowed read errors skip the takeover; concurrent links both succeed | `links.go:60-64, 87-94, 108, 115-126`; `clips_api.go:1311-1321` | **Accepted, with a different race fix.** §3.8: `linkedUser(key) (string, error)`, and `setLink` returns read errors before any write (500 `store`). For the race, instead of compare-and-set, §3.1 makes "linked" require both entries to agree, which also covers any half-finished cleanup. T6, T7, T9. |
| S4 | major | Defaults for Q6 (stills) and Q7 (Twitch Connect) loosen "watch only" | `stills.go:783-872`, `:861-871` | **Accepted.** Both default to no; stills claim, upload and peeks are linked; Twitch connect is linked. Q6 and Q7 keep "yes" as the loosening alternative. |
| S5 | minor | A refresh racing a write can reinstall the old list; the stale-copy rule keeps a let-out let in for up to 70 s | design §3.2 (no ordering; stale-copy row) | **Accepted.** Generation counter; the stale-copy fallback is dropped, so a failed let-in read fails closed and the 20 s bound holds. T8 cases. |
| S6 | minor | With the gate first and no remembered no, a refused caller drives unmetered store reads | `api.go:316-326` (limiter before any read today) | **Accepted (the cache option).** A 2 s refusal memory under the epoch, dropped by `accessChanged`; made-up paths use `accessSeenOf`; the clip gate uses it too. A per-user refusal meter was not taken: it must be checked before the read, so a person who was refused many times and then unlocked would find it empty. T2 counts reads. |
| S7 | minor | Watch-class pictures cached for a day bypass the gate on re-display | `stills.go:916-920`; `channelpics.go:334-338, :399`; `clips_api.go:546`; `brand.go:135-160` | **Accepted.** `private, no-cache`; `writeImage` already sends the ETag and 304. §3.7, T2. |
| S8 | minor | The admin Unlink says "locks at once" for somebody who stays let in | design §8.1, §3.3, Q15 | **Accepted.** `POST /admin/unlink` returns `letIn`; the confirmation no longer promises a lock; the result is worded from the answer, with Remove. T15. |
| S9 | minor | Pop out hands let-in people keys a let-out cannot revoke; LOCKDOWN text overstated | `broadcastbox.go:163`; LOCKDOWN.md:14 (`DISABLE_FRONTEND`) | **Accepted (linked-only).** Pop out route and `/me.popOut` are linked only, which keeps what linked people have today; Q10 offers removal. LOCKDOWN text names the Public watch URL hole and the two admin exceptions. |
| S10 | minor | A Twitch callback can complete after a let-out inside the state's 10 minutes | `twitch-live.md:61, :497` | **Accepted.** §9.2: the callback re-runs `accessOf` with `connect`'s class and discards the state if refused. |
| S11 | minor | `unknown` tears down a linked person's panel on one store blip | design §4.9, §3.1 | **Accepted** (merged with U2). `followAccess` acts only on definite answers; `unknown` keeps the panel with a notice and one re-read after 5 s. |
| S12 | minor | The chat bridge, a non-panel input, is not in the capability table | `bridge.go:282-290` | **Accepted.** §1.1 row (unchanged) and Q16. |
| S13 | minor | Built-in password managers ignore `autocomplete=off` on password fields | `Sidebar.tsx:1407-1414` | **Accepted** (merged with U4). §4.5: `type=text` with `-webkit-text-security` where supported, else `new-password`; extension attributes kept; R11. |
| S14 | minor | Prefix dispatches above the gate escape layer 1; T1 misses multi-path cases | `api.go:138-145, :205` | **Accepted.** §3.5 step 4 allow-list and a comment at the top of `ServeHTTP`; T1 collects every path in a case and fails on other prefix dispatches. |
| U1 | blocker | After a refused Unlock click, focus stays on the button, and Ctrl+V then Enter posts the token | `Sidebar.tsx:1125-1137`; MM master `post_utils.ts:246-264`, `use_textbox_focus.tsx:70-104` | **Accepted** (with S1). §4.4: `empty` and `refused` return focus to the bar when focus is on a lock control or `body`; one key helper for every token bar, the relink form included; the lab's habit switch learns Ctrl/Cmd+V (it skipped modifiers, `lab/index.tsx:271`). |
| U2 | major | `unknown` shows LOCKED and stops streams for a linked person | design §4.1, §4.9 | **Accepted.** `shownAccessOf` keeps the last definite answer; with none, the lock shows a NO SIGNAL face, not LOCKED. |
| U3 | major | A stale `openedByHand` focuses the bar on a later automatic mount | `index.tsx:259, 327-338` (`toggleRHSPlugin`) | **Accepted.** Set only when the press opens VillaNET; stamped and ignored after 1 s; the lock subscribes so an opener while mounted applies at once. |
| U4 | major | Chrome likely offers to save the token as the site password on unlock | Not checked in Chromium source | **Accepted as likely.** Same change as S13; R11 confirms on real browsers. "Blur and clear before the swap" was not taken, since a password manager may keep the typed value regardless. |
| U5 | major | A centred flex column with overflow cuts off the top, out of reach | CSS behaviour of `justify-content: center` with overflow | **Accepted.** Inner `.vntv-lock-column` with `margin-block: auto`; stage `flex-start`; lab check at 560 px. |
| U6 | major | §5 and §6 assume the pre-tidy footer; the variants move the link prompts | `ux-variant-a/…/Sidebar.tsx:173, 1500, 1617`; `ux-variant-b/…/Sidebar.tsx:140-143` (read only) | **Accepted.** §5 and §6 are defined by role; §12 adds a mandatory grep-and-classify step (§12.1) before W3, and a screenshot for Victor. |
| U7 | major | An empty relink bar plus Enter unlinks, which now locks everything | `Sidebar.tsx:743-748, 1414-1424`; `api.go:521-523` | **Accepted.** Empty Enter or Save only shows a hint; Unlink is its own pill with a two-press confirm (`twoClicks.ts`); `/me.alsoLetIn` words it. |
| U8 | major | The return after a reply brings the lock back over the thread, with no Back to thread | `awayLogic.ts:197-214`; `Sidebar.tsx:1142`; `ReturnStrip.tsx:13-20` | **Accepted (the preferred fix).** `arm` sets no return unless the access is watch-capable; away checks and a lab case. |
| U9 | major | The lock speaks only to streamers; viewers get one small line | design §4.3 | **Accepted, with one change.** Two rows of equal weight; the admin row; hidden when not set up; hint in `#c9c9c9`. The default line is dropped rather than reading "VillaNET is locked." twice, since the plate is now that heading. "Who to ask" is a plain-text setting (Q17). Stripping "Bearer " was already done by the server (`api.go:521`); the client's empty check now matches it. |
| U10 | major | Several states are unreachable in the lab | `lab/index.tsx` (no RHS focus simulation; habit skips modifiers) | **Accepted.** §10 adds Let in now, Unlink while let in, channel-popout cards, the two missing `linkStream` answers, a side-panel focus switch, Window focused, Socket down/Reconnect, Clock +60 s, Store blip and the AdminAccess states. |
| U11 | minor | M7 names one timer hop; the fixed 60 ms delay can lose | MM master `sidebar_right.tsx:165-177`, `a11y_utils.ts:46-60`, `a11y_controller.ts:909-925`; `away.ts:566-617` | **Accepted.** M7 corrected; the bar is focused by the factored `startFocusGuard`; `LOCK_FOCUS_DELAY_MS` dropped. |
| U12 | minor | 14 px input makes iOS zoom; autofocus raises the phone keyboard | MM `root.html:6`; `_mobile.scss` (master :2021-2026, v9.3.0 :2299-2304) | **Accepted.** 16 px below 768 px (M12); no autofocus on `mobile` or coarse pointers; R14. |
| U13 | minor | The bar's #555 edge on black is under 3:1 | `styles.ts:43-45, 55-58` | **Accepted.** Border `--vntv-muted` (#777, about 4.7:1); lab looks at rest. |
| U14 | minor | `.vntv-relink` is an icon button and cannot carry words | `styles.ts:5330-5346` | **Accepted.** New shared `.vntv-textkey`; named in §4.7 so `check-styles.py` sees it. |
| U15 | minor | Screen-reader flow misses the reason and the way in; plate hidden; reveal reads badly | design §4.5 | **Accepted.** `aria-describedby` covers line, hint and status, and the region the viewer row; the plate is an `<h2>`; Show/Hide changes its label without `aria-pressed` (the footer too). |
| U16 | minor | Focus moves on swaps are too strong or missing | design §4.6, §5 | **Accepted.** One swap rule in §4.1: move focus only from inside the old root or `body`; lab checks with focus in the message box. |
| U17 | minor | Pending never expires and the open lock does not follow it | design §7 | **Accepted.** 10-minute stamp, cleared by a plain opening, and the lock subscribes. |
| U18 | minor | `openLock` does nothing in a channel popout, or for a let-in person; linked to let-in leaves the editor open | `clips.ts:686-691`; `clipLogic.ts:1077-1087`; `away.ts:61-72` | **Accepted.** Two targets (lock, or the let-in TokenBar); `main` and `channel-popout`; `followAccess` handles linked to let in; the editor's `watch_only` copy. |
| U19 | minor | The lock does not fit a popped-out window | `popout.ts:1-18`; `Sidebar.tsx:110-112` | **Accepted.** PanelGate runs `fitPopoutWindow`; lab check. |
| U20 | minor | A media query measures the window, not the panel | `styles.ts:125, 1641, 5366` | **Accepted.** `@container vntv-lock (max-width: 340px)`. |
| U21 | minor | Escape wipes a pasted token | design §4.5 | **Accepted.** Escape clears only an error state. |
| U22 | minor | One lost-link line for three different causes | design §4.3, §4.9 | **Accepted.** Causes `self`, `unlinked` and `letOut`, each with its own line. |
| U23 | minor | `vntv-lock-out` has no `@keyframes` rule | design §4.7; `build/check-styles.py` checks no animation names | **Accepted.** Keyframes row added outside the reduced-motion block. |
| U24 | minor | Two existing settings change meaning; their help texts are not named | `plugin.json` `OpenPanel`, `AutoLinkByUsername` | **Accepted.** §9.6 gives the added sentences. |
