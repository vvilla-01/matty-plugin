# VillaNET Clips 3.0.0: build contract addendum

Written 2026-10-02 against the 2.11.0 tree, and revised the same day after three adversarial reviews (see the Review log at the end). Builders implement `design/clips-spec.md` (revision 2) **plus this addendum**. Where the two conflict, this addendum wins, and the owner rules in A§1 win over both. "§2.10" means a section of the spec; "A§3.2" means a section of this addendum. Project paths are relative to the project root, and line numbers are 2.11.0's.

**Evidence notation.** Every claim about Mattermost cites source, or says UNVERIFIED:
- `[v9 …]` is mattermost/mattermost@v9.0.0 and `[master …]` is @master 60328d1 (2026-10-02).
- `[v0.4.4 …]` is the pinned `server/public` module in `~/go/pkg/mod`.
- `[mobile …]` is mattermost-mobile@main e2b167b, and `[desktop …]` is mattermost/desktop@master 11feb09.
- `W/` stands for `webapp/channels/src/` and `S/` for `server/channels/`.

---

## 1. Owner rules

Victor's rules of 2026-10-01, verbatim. **They override `design/clips-spec.md` wherever they differ, including its Appendix B Q12 and Q15.**

1. The WHOLE Clips feature is for LINKED users only: clipping, publishing, viewing (listing and watching) and upvoting. "Linked" means the Mattermost user has linked their own stream through VillaNET's "Link Your Account" flow (server: links.go getLinkedKey/linkedKey, community_api.go requireLinkedStream; webapp: me.streamKey). Enforce it on the SERVER too: clip list, clip and thumbnail file serving, votes, and every other clip route. Unlinked people see the Link Your Account call to action instead of clips.
2. Who may clip: a linked account that is watching that stream in VillaNET right now (or was watching when it ended - the tail rule), or the stream's streamer.
3. Recording is ON for every live, non-24/7 stream, with a notice to the streamer, a switch to opt their stream out, and a way to wipe what is held (as the spec says).
4. Default clip 2 minutes, admin setting up to 5 minutes. One press captures the admin length; people can only trim it shorter. Clips kept 30 days by default (admin setting).
5. Best Clipper: ONE all-time holder, most upvotes in total, NO runner-up anywhere. Shown beside names in posts, in the profile popover and in the Trophies card. Expiry and a streamer's deletion keep a clip's votes; the clipper or an admin deleting it voids them. The trophy dialog is renamed "Trophies".
6. No download button.
7. SHARING (new; the spec deferred it as Q15 - now it is IN 3.0.0): a Share button posts the clip into the CURRENT channel as a custom post type (webapp registry.registerPostTypeComponent) - a clip card with title, clipper, streamer, length and a Watch button; plus a plain-text link in the post's message for mobile. Clicking Watch opens VillaNET's panel on that clip. Unlinked users get "link your account first" plus the Link Your Account call to action, and the card shows them NO thumbnail. Expired or deleted clips say so on the card.

All other Appendix B defaults stand: total upvotes; fixed admin length with trim only; votes banked on expiry/streamer delete and voided on clipper/admin delete; "Trophies" rename; recording on with opt-out + notice + wipe.

> **Amended 2026-10-05 by `design/lock-screen.md` (built into 3.0.0).** Rule 1's *viewing* (listing, opening and watching clips, and shared cards with their thumbnails) is now for linked people **and for people a system admin has let in** (System Console → VillaNET → Access). Everything else in Clips stays linked-only: clipping, the C key, drafts, Just ended, trimming, publishing, upvoting, reporting, sharing, deleting, and the streamer's switch and wipe. "Unlinked people see the Link Your Account call to action" becomes: locked people see the panel's lock screen, and let-in people see clips with no controls to act on them. The Link Your Account button and every in-panel link prompt are gone; unlocking with a bearer token *is* linking. In rule 7, a locked reader's card is drawn from the post alone, shows no thumbnail and makes no card request, and its Watch opens the lock screen with the clip waiting behind it. A let-in reader's card is a linked reader's card. "Linked" itself is narrower: both link entries must agree (`link_user_<u>` names K and `link_key_<K>` names u; `linkOf`, `server/links.go`), so a forward entry left behind by a race or a failed cleanup counts as no link. On who may *view* clips, the lock-screen design wins over this addendum; on everything else, this addendum stands. Q-B below now reads "somebody not linked (locked or let in)". Q-A and Q-C stand.

> **Amended 2026-10-13 by `design/3.2.1.md` (built into 3.2.1).** Rule 6 gives way to Victor's "Server toggle to allow/deny clip downloading": downloads are **off** unless an admin switches on **Let people download clips**; then linked people (never people let in) get a **Download** in the clip viewer only (never on shared cards or in the System Console), and a streamer can turn downloads of their own stream's clips off. The player's own `controlsList="nodownload"` stays.

**Owner answers to A§6, given by Victor on 2026-10-02.** They are final and override every "Default" in A§6:
- **Q-A: yes, full moderation.** Every system admin, linked or not, can list, watch and moderate clips in System Console. `/api/v1/admin/clips*` checks `isSystemAdmin` only. This is the one exception to rule 1; the panel stays linked-only for admins too.
- **Q-B: no.** Someone unlinked cannot delete their own clips or drafts; they relink first, or an admin deletes them. *(From the lock, 2026-10-05: "unlinked" is somebody not linked, whether locked or let in. A let-in person's clip views carry `canDelete: false`, and the delete route answers them 403 `watch_only`.)*
- **Q-C: yes.** Votes count only while the voter is linked; a holder who unlinks loses Best Clipper until they relink.
- **Q-D: yes** (the default stands). A shared clip is posted as the person who pressed Share.
- **Q-E: yes** (the default stands). Clips can be shared while Clips is switched off.
- **Q-F: no.** The shared post's message carries no title and no names.

**Clip tags (asked 2026-10-02):** a clip carries the game, movie or show its stream was tagged with, captured when it is cut and shown in its details and on the shared card, beside the clipper's own title (the only words the clipper adds). See A§4.14.

**Also in 3.0.0 (asked 2026-10-02, outside Clips): a Top Snooper badge.** One holder, the rank-1 person of the all-time Snoopers board (`snapshot.watchAllTime`), drawn as gold binoculars in the crown's style, shown beside names in posts, the profile popover and the Trophies card; no runner-up and no monthly one. It is built separately (webapp only: `badges.tsx`, `AwardBadges`, `AwardList`, `LeaderboardModal`). Clips work in those files must keep it, and Best Clipper sits beside it.

---

## 2. Spec corrections for 2.11.0

The spec was written before 2.9.0–2.11.0 shipped. Each row below is real drift; the right-hand column is what to build instead.

### 2.1 Server

| Spec | Spec assumes | 2.11.0 actually has | Build this |
|---|---|---|---|
| §2.1 poller.go, §2.12 | `addClips(current)` "right after `addCommunity`" on all three paths | On the failure path, `addCommunity` (`poller.go:130`) runs **before** the last streams are carried over (`:132-151`) and before `addNowPlaying` (`:157`), so `addClips` there would annotate an empty list | Call `p.addClips(current)` as the **last step before each `p.publish(current)`**: `poller.go:101`, `:163` and `:203`. |
| §2.1 poller.go, §2.4.5 | `noteSessions` and `reconcile` "after `decorate` and `recordStreamTime`", then annotate | Success path is now `decorate :177` → `recordStreamTime :180` → **`recordWatchTime :181`** (2.10.0) → `addCommunity :184` → **`addNowPlaying :188`** (2.10.0) → boards → `reconcileBadges :201` → `publish :203` | After `:181`, call `p.recorders().noteSessions(raw, now)` and then `p.recorders().reconcile(streams, cfg, now)`. `reconcile` only updates the manager. All `clip`/`clipFrom`/`clipTail` annotation happens inside `addClips`, from the manager's last state, so failed polls and `republishClips` annotate the same way. |
| §2.12 `addClips` | Writes into `current.Streams` | `getSnapshot()` hands out the live pointer, which readers hold without a lock. The failure path reuses `previous.Streams` (`poller.go:134`), and a `republishClips` shallow copy shares the array. | `addClips` builds a new slice (`make` + `copy`) before writing, as `addNowPlaying` (`nowplaying.go:257`, copy at `:283-290`) and `dropWatchersWhenOff` (`watchers.go:302`) do. Otherwise `-race` fails. |
| §2.4.7 | `withoutOwn` right after `counted := stills.counted(…)` | That line is `poller.go:281`. It feeds `Viewers` (`:290`) and `watchersOf` (`:385`). A second `stills.counted` call at `watchtime.go:207` credits Top Snoopers. | Insert `counted = p.recorders().withoutOwn(counted)` at `poller.go:282`. Leave `watchtime.go:207` alone: recorder sessions are never in the watcher store, so they are never credited. |
| §2.14 unconfigured branch | `stopAll(false)` | That branch (`poller.go:97-110`) runs on **every** poll while unconfigured, and `reconcile` calls `stopAll` whenever `problem` is set (for example a non-local file driver, where no root is ever resolved). The spec's `stopAll` wipes `ring/` and `drafts/` (§2.4.5). | `stopAll` must be idempotent and make no API call when nothing is running. `stopAll`, every wipe and every directory operation **return at once when the clips root is unresolved** (never started, or `problem` set). The root must be absolute; no path is ever built from an empty root (`filepath.Join("", "ring")` would be relative to Mattermost's working directory). Test: `TestStopAllTouchesNoFilesWithoutARoot`. |
| §2.10 limiters | Created in `OnActivate` as `Plugin` fields | No test calls `OnActivate`, so limiters made there are nil in tests, and `(*rateLimiter).allowAt` panics on a nil receiver. Stills keep their limiters in the lazily built store (`stills.go:257-265`, accessor `:268-274`). | Keep the capture, publish, vote, write and new **share** limiters (A§4.2) in the lazily built clips store. |
| §2.14 `OnActivate` | "After the existing setup, before `restartPoller`" | `plugin.go:464-522`: the initial `setSnapshot` is at `:506-510`, `activated = true` at `:512-514` and `restartPoller` at `:516` | Call `startClips()` **before `:512`**, so a concurrent `OnConfigurationChange` cannot `stopAll` half-way through it. The initial snapshot at `:506-510` gets `Clips`. |
| §2.14 `OnConfigurationChange` | "When `activated && previous != nil`" | `previous` (`plugin.go:597`) is nil on the first call, which comes before `OnActivate` (comment at `:585-596`). `restartPoller` (`:625`) sits inside `if activated {`. | Put the "Clips switched off" and "BB URL changed" checks inside `if activated {`, before `:625`, guarded by `previous != nil`. |
| §2.14 `OnDeactivate` | After `stopPoller` | `stopPoller` is at `plugin.go:549` | Call `stopClips()` right after `:549`, before `clearAllBadges` (`:553`). |
| §2.10 error shape | `{"error","code"}` | No `code` field exists anywhere. Errors are written by `writeJSON` (`api.go:78`) and `writeError` (`:84`). | Add `writeClipError(w, status, code)` to `clips_api.go`, and `Cache-Control: no-store` on every clip JSON response. |
| §2.10.6, §2.16 "manually linked" | Reuse the existing link check | `requireLinkedStream` (`community_api.go:111-127`) answers 403 with a planning sentence and no code, and a KV error with the community's 503 text | Use the new `requireClipsLink` (A§3.2). Never call `requireLinkedStream` from a clip route. |
| §2.8 "read fresh … the same rule as `mayShareStills`" | `mayShareStills` reads fresh | `mayShareStills` (`stills_admin.go:433-446`, via `ensureStillBlocks :394`) reads **once** and then serves a memory copy | Before every draft, publish, opt-out and wipe check, do an explicit `KVGet("clips")`. If it cannot be read, answer 503 `store`. Do not copy `mayShareStills`. |
| §2.10.2 step 5, title order | Cut, then `withoutOwnKey`, then redact, then cut again | 2.10.0's rule (`cleanTyped`, `community.go:205-217`) redacts **before** cutting, so a token pasted near the end cannot leave half a credential behind | Clean the title in this order, failing closed. **Do not call `withoutOwnKey`**: it reads through `getLinkedToken`/`getLinkedKey`, which turn a KV error into `""` (`links.go:60-64, :80-87`), so a read failure would skip the redaction. Instead: `t := strings.TrimSpace(model.SanitizeUnicode(title))`; read the token with `p.API.KVGet(kvLinkTokenPrefix + userID)` and, on an AppError, answer 503 `store` and publish nothing; then `t = redactStreamKey(t, token, "my stream")`, `t = redactStreamKey(t, key, "my stream")` with `key` the gate's return value (A§3.2 step 5), `t = redactStreamKey(t, draft.streamKey, "the stream")` (the third argument is the **replacement**, `poller.go:469`), and `t = cleanText(t, maxClipTitle)`. Test: `TestPublishRefusesWhenTokenUnreadable`. |
| §2.8 example | An unnamed stream is `"Stream ab12"` | `unnamedTitle` gives `"Stream "` plus **6** hex characters (`poller.go:495`) | Store `apiStream.Title` as it is. Tests must not expect 4 hex characters. |
| §2.4.5 slot order | Sort by `StreamStart` ascending | `StreamStart` is a string (`plugin.go:149`) | Parse it with `time.RFC3339Nano`, as `stats.go:309` does. An unparsable value sorts last. |
| §2.3 storage root | Read `p.API.GetConfig()` | `GetConfig` is used nowhere today; only `GetUnsanitizedConfig` is (`defaults.go:190`). `GetConfig` returns the sanitised config [v9 S/app/plugin_api.go:91-92], whose `Sanitize` blanks only salts and storage secrets [v9 server/public/model/config.go:4125-4131]. `DriverName` and `Directory` survive. | Use `GetConfig()`. Nil-check `FileSettings`, `DriverName` and `Directory`, and compare against `model.ImageDriverLocal`. Tests mock `GetConfig`. |
| §2.10.3 | `isSystemAdmin` for each `ClipView` | `airtime.go:112` makes one `HasPermissionTo` RPC per call | Call it once per request. |
| §2.10 thumbnail route | `writeImage`, with `private, max-age=86400` when `v` matches | `writeImage` (`brand.go:135-165`) answers 304 on a matching ETag before anything else | Run the gate first (A§3.2). Read the `.jpg` (≤ 200 KiB) into memory, then call `writeImage(…, "private, no-cache")` **whatever `v` is**. Every display then revalidates through the gate (owner rule 1); a 304 is cheap. |
| §2.8 halted document | "Recovery is manual" | `recordingHalt` (`watchtime.go:66-91`) has no reset. `quarantine` (`statsstore.go:119`) keeps the first copy. `setAside` (`community.go:421`) **deletes** the document. | Use `recordingHalt` plus `quarantine`, never `setAside`. A halt clears only on a plugin restart, so AdminClips and the README say: fix or delete the `clips` key, then disable and enable the plugin. |
| §2.13 cluster job | `cluster.Schedule(…)` | First use in this plugin. The package is in the pinned v0.4.4 (`pluginapi/cluster/job.go:115`, `MakeWaitForInterval :45`, `Close :222`). It writes KV keys `cron_clips_sweep` and `mutex_cron_clips_sweep`, starts a goroutine at once, and `Close` blocks on a running callback. | No `go.mod` change. Make the scheduler a replaceable field so tests call `sweepClips` directly, and keep the sweep cancellable. |
| §2.19 | "Inject time through a clock function" | There is none. The house pattern is an explicit `now time.Time` parameter (`recordStreamTime(…, now)`, `stills.claim(…, now)`). Tests that drive `pollOnce` use real time (`monthmedals_test.go:760`). | Pure functions take `now`. The clips store carries `now func() time.Time`, for handlers only. |
| §2.19 test harness | Use `kvBackedPluginWithFaults` | `stats_test.go:96-245` gives real CAS, and Log\* mocks for at most 8 key/value pairs (`:233-241`). It does **not** mock `GetConfig`, `GetUser`, `HasPermissionTo`, `PublishWebSocketEvent`, `KVList`, `CreatePost`, `GetChannel`, `GetChannelMember`, `HasPermissionToChannel`, `GetPost` or `UpdatePost`. | Add an `api.On(…)` for each of these in the tests that need it. About 25 existing tests drive `pollOnce` or `decorate` with Clips off and no `startClips`. So `withoutOwn`, `noteSessions`, `reconcile` and `addClips` must make no API call and log nothing on a never-started store, and the 60 s cache re-read never runs from `addClips`. No log line may carry more than 8 key/value pairs. |
| §2.4.9 `handleWHEP` | Note the viewer after a successful non-peek exchange | The exchange is at `api.go:684`; the peek/watchers block is at `:699-703` | Add a separate block after `:703`: `if !peek && cfg.EnableClips { p.recorders().noteViewer(sessionID, userID, handle, now) }`. It does not depend on `ShowWatchers` or `TopWatchers`, and it records every user (A§3.9). `p.httpClient` is nil unless `OnActivate` ran, so tests set it (the `watchers_test.go:443-452` pattern). *As built: `if !peek`, without `cfg.EnableClips`; see the note in A§3.4.* |
| (new) | — | `meResponse.SiteURL` is the **Broadcast Box** URL (`api.go:312`) | Mattermost's own URL, which share links need, is `*p.API.GetConfig().ServiceSettings.SiteURL` (A§4.3). |
| §2.18 | `ice/v4` v4.4.5 | `pion/webrtc v4.2.22` requires `ice/v4 v4.4.4`. mediacommon v2.9.5 declares `go 1.26.0`; the module is on `go 1.27.1`. `golang.org/x/sys v0.47.0` is indirect today, and `x/time` is absent. | Let `go mod tidy` resolve them; x/sys becomes direct. The module cache holds only `.mod`/`.info` files for webrtc and mediacommon, so the first tidy downloads (proxy.golang.org is reachable). |

### 2.2 Webapp

| Spec | Spec assumes | 2.11.0 actually has | Build this |
|---|---|---|---|
| §3.3 `ClipsRoot` | `registerRootComponent` | Root renders only inside the channel view (`ChannelController`) [v9 W/components/channel_layout/channel_controller.tsx:64], and in popped-out windows [master W/components/popout_controller.tsx, `<Pluggable pluggableName='Root'/>`]. It goes when the user leaves for a custom route (A§4.7). The System Console renders neither Root nor Global [v9 W/components/root/root.tsx:598-608 vs :721]. | Host it as described in A§4.8. |
| Opening the panel from code | — | The opener is private: `openSidebar` (`watching.ts:46`), set by `setSidebarOpener` (`index.tsx:239`) and called only by `watchStream` (`watching.ts:329-341`) | Export `openPanel()` from `watching.ts`: `openSidebar?.()`. Never dispatch RHS actions directly, because `away.ts` follows the store. |
| Link Your Account away from the panel | — | `openLink` is local state in `Sidebar.tsx:661-667` | Add `requestLinkForm()`, `takeLinkRequest()` and `useLinkRequest()` to `identity.ts`. `Sidebar` consumes a pending request on mount and on notify by running its own `openLink()`. `clips.ts` exports `askToLink()`, which calls `requestLinkForm()` and then, in the main window, `openPanel()`. |
| §3.2 `requestClip` | Built like `requestJSON` | `requestJSON` and `readError` are module-private (`client.ts:39-64`); they throw `new Error(message)`, which keeps neither status nor code | Write `requestClip<T>` **inside `client.ts`**, throwing `ClipRequestError{status, code}`. Map a `TypeError` to `network`, and rethrow an `AbortError` unchanged. |
| §3.2 lab mocks | Every export mocked | `surface.ts` (`lab/mocks/surface.ts:21-23`) type-checks every **value** export, so the `ClipRequestError` class counts. The mock `write()` rejects with a plain `Error` (`lab/mocks/client.ts:64-71`). | Mock every export, including the class. Clip writes in the mock reject with `new ClipRequestError(503, 'store')`, and every `instanceof` check imports the class from `'../client'`. |
| All clip HTTP | — | The lab swaps only `./client`, `../client`, `./whep` and `./manifest` (`lab/webpack.config.js:53-55`) | All clip HTTP lives in `client.ts`. **New components stay flat in `components/`**: an import of `'../../client'` is not swapped and would hit the network in the lab. |
| §3.4 `WaitingTile` | Gains a ✂ | Its props have no `handle` (`Player.tsx:235-273`), and its `Hud` has no row (`Hud.tsx:58-60`) | Add `handle` to its props (it is in scope at `Sidebar.tsx:1015-1020`). Update the `Hud.tsx:58-60` comment, and give the tile a row holding only the ✂. |
| §3.4 C key | `onKeyDown` on `.vntv-player` | The tile root has neither `onKeyDown` nor `tabIndex` (`Player.tsx:137-140`). A click on the picture focuses `.vntv-panel` (`tabIndex -1`, `Sidebar.tsx:869-880`), so a handler on the tile would fire only while a HUD control has focus. | Give `.vntv-player` `tabIndex={-1}` and handle C there. When `event.target === event.currentTarget`, also stop every other printable key, copying `Sidebar.tsx:892-902`. Suppress the focus outline on the tile itself, and check that `away.ts:577-592` still treats focus inside the panel as before. |
| §3.4 `HudButton` | Gains new props | Its props are only `label`, `onClick` and `children` (`Hud.tsx:502-514`) | Add the props as the spec says. The tag and the busy pulse show only while the HUD is awake (`styles.ts:1344-1363`). |
| §3.8 sound | "The panel's layout effect mutes it"; restore with `setAudio(prev)` if `isWatching(prev)` | `setAudio(null)` now mutes synchronously through `sessions.ts` (`installSessions :724-735`). Closing VillaNET stops the sessions but keeps the watch list, so `isWatching` alone is not enough. | Restore with `takeSound(prev)` (`sessions.ts:612`), and only if `getSessionInfo(prev)?.status === 'playing'` (`:671`). |
| §3.6 focus trap | Tab cycles inside the card | The existing selector (`HashtagSearch.tsx:218`) misses inputs, textareas and `video[controls]`, and knows nothing of `inert` | Extend it to those, and skip `[inert]` subtrees. |
| §3.5, §3.7 Escape | "Escape while dragging cancels the drag" | A capture-phase document listener that calls `stopPropagation` (`LeaderboardModal.tsx:417-434`) starves every React handler of Escape | Handle the drag cancel, and the viewer's "back", inside that same listener. |
| §3.7 pointer capture | The `ImageCropper` pattern | `ImageCropper.tsx:237-264` never releases capture | The trimmer calls `releasePointerCapture` itself when Escape cancels a drag. |
| §3.9 header button | A clapper left of the trophy | `.vntv-rhs-title` uses `justify-content: space-between` (`styles.ts:3396-3407`), so a third child would sit in the middle | Wrap the clapper and the trophy in one group (`SidebarTitle.tsx:43-68`). |
| §3.9 panel row | "Directly under the Live now section" | That section renders only when `streams.length > 0` (`Sidebar.tsx:1049-1101`), and it glides (`:441-520`) | Put the row after `:1101`, outside that condition, and include it in the glide so it does not jump. |
| §3.9 no-thumbnail picture | "As on LiveCard" | `Backdrop` is private and takes a `Stream` (`LiveCard.tsx:378-397`) | Export it, or add a variant that takes a user id. |
| §3.9 storage keys | `vntv.clips.seen`, `vntv.clips.sort` | The house prefix is `villanet.` (`LeaderboardModal.tsx:356` `villanet.trophiesTab`, `MiniPlayer.tsx:59`) | Use `villanet.clipsSeen` and `villanet.clipsSort`, each access in try/catch. |
| §3.11 own stream | `userId === me.userId && linkSource === 'manual'` | `ownOnAir` (`Sidebar.tsx:306`) matches `userId` only, and nothing in `src` or `lab` reads `linkSource` yet (`types.ts:18`) | Use the spec's test as written. The server still decides (A§3.1). |
| §3.12 Trophies | Rename the dialog; add a wide section | The rename **has already shipped** (`SidebarTitle.tsx:51-52`, `LeaderboardModal.tsx:537-562`), and the dialog has tabs (`TabId :347`, tab list `:445-448`, key validation `:453-456`, `contentOf :503-514`), with a "Clippers" tab already planned in the comment at `:398-400` | Build Best Clipper as a **third tab, "Clippers"**. Extend `TabId`, the remembered-key check and the switch. One holder, no runner-up. |
| §3.12 `awardsFor` | A new `clipper` kind | The renderer is a two-way ternary on `kind` (`AwardList.tsx:223-251`) | Make it a three-way switch, otherwise the board branch reads `award.board`. |
| §3.14 AdminClips Watch | `openClips(id)` | The console renders neither Root nor Global, so nothing would appear | Use an inline player in the row, served from `GET /admin/clips/video` (A§3.3), for every system admin, linked or not (Q-A, answered yes). It is `<video controls controlsList="nodownload" disablePictureInPicture playsInline preload="metadata">`, and no download link or helper appears anywhere in AdminClips (owner rule 6). |
| §3.1 `AdminClips` type | — | It clashes with the `AdminClips` component | Import it under an alias, as `AdminStills.tsx:4` does. |
| `mattermost.ts` | — | `State` has no `channels.currentChannelId` (`mattermost.ts:32-50`). `webappUtils()` is module-private and its `History` type has only `push` (`mattermost.ts:187`, `:200`). `mattermost.ts` imports `./manifest` and `./popout` (`:3-5`), so `check-away.mjs`, which transpiles one file and imports it from a data URL (`build/check-away.mjs:21-29`), cannot load it. | Add `currentChannelId()` and `shareTargetOf(channelId)` (A§4.10), which reads the store and calls the pure `shareLabel()` in `clipLogic.ts`. Export `replacePath(path)`: `webappUtils()?.browserHistory?.replace` when present, else `window.location.replace((window.basename \|\| '') + path)`; `History` gains `replace?`. |
| Lab | — | Victor's lab stream has no `linkSource` (`lab/world.ts:127`), so the streamer controls never show. The panel re-fetches `me` only when it remounts, which is driven by two identity keys (`lab/index.tsx:1508-1516`, `:1665-1668`). `LabMessage` renders `post.message` as plain text (`:1041-1051`). | Give Victor's stream `linkSource: 'manual'`. Add the clip `me` fields to **both** identity keys. Do not mount the clips host while the lab's Console is shown. See A§4.12 for `LabMessage`. |
| §3.15 styles | — | `.vntv` is a black, full-height column (`styles.ts:45-63`). The reduced-motion block (`styles.ts:6106` onward) **must stay last**. Element ids must not contain `vntv-`, because the style check reads them as class names (`LeaderboardModal.tsx:374-376`). | Put new reduced-motion rules **inside** that block. Content shown outside the panel needs its own reset (A§4.11). |
| `me === null` | — | `me` is null until `fetchMe` resolves (`index.tsx:158-165`; `identity.ts:31`), **and stays null after an in-app sign-in**. Webapp plugins initialise on the signed-out login page (`PluginsEnabled` is in the limited client config [v9 server/config/client.go:222, :288]; `initializePlugins` runs before any route renders [v9 W/components/root/root.tsx:299-307, :546]). Login then navigates in-app with `history.push(redirectTo)` [v9 W/components/login/login.tsx:632-633], and a plugin already loaded is not loaded again [v9 W/plugins/index.js:124-128]. So the one `fetchMe` in `initialize` got a 401, and only the panel's mount fetches `me` again (`Sidebar.tsx:192`). | Clip surfaces render nothing while `me` is unknown: no call to action and no request. `index.tsx` subscribes to Mattermost's store and, when `entities.users.currentUserId` changes to a different non-empty id (including from `''`), calls the coalesced `refreshIdentity()` and refetches the snapshot once. Lab row "signed in after load" (A§4.12); criterion A33. |
| Sidebar and `refreshIdentity()` | — | The Sidebar reads `const reader = me \|\| known` (`Sidebar.tsx:271`), with its own `me` fetched on mount only (`:62`, `:192-199`), so an identity refresh never reaches the panel until it remounts | The Sidebar follows identity changes: when `useIdentity()` returns an object other than its local `me`, it calls `setMe(known)`. (Its existing effect then calls `setIdentity` with the same object, which ends the loop.) Lab check: flip Linked to no while the panel is open; the ✂, the streamer controls and the Clips row switch at once. |

### 2.3 Fixtures, checks and tree

- **Fixtures already exist.** A parallel step created them on 2026-10-02 at 12:07. Use them, and regenerate only if a check fails:
  - `build/make-test-clip.sh`
  - `build/make-lab-clip.sh`
  - `server/testdata/clip-h264-opus.mp4`: H.264 High 320×180, 150 frames, 5 keyframes, time base 1/15360, no B-frames; Opus 2 ch, 501 packets.
  - `webapp/lab/media/sample-clip.mp4`: 120.0 s, 256×144, 12 fps, H.264 High, no B-frames, Opus, 415,867 B.
- **A stray `server/server`** (29 MB, 20 Sep) is not in the baseline and is not shipped by `dist`. Leave it alone.
- **Checks.** Always run `export PATH="$HOME/.local/opt/go/bin:$HOME/.local/opt/node/bin:$PATH"` and then `TZ=UTC make check` (one birthday test assumes UTC). The `e2e` target does not exist yet; add it as §2.1 says.
- **Lab size.** The inline `.mp4` adds about 555 KB to the lab HTML (917,744 B today). `build/build-lab.py:25-26` refuses `</script`, which base64 never contains.
- **`styles.ts` is one JS template literal.** No backticks anywhere in it, not even in comments, and no `\00b7`-style escapes. Run `python3 build/check-styles.py` after every edit, and look at the result in the lab.

---

## 3. Linked-only

### 3.1 What "linked" means in code

> **Amended 2026-10-05 (lock screen, built).** `linked(u)` is now `linkOf(u)` (`server/links.go`): `link_user_<u>` names K **and** `link_key_<K>` names u, and either read failing is an error, never a no. `streamerOf`, Best Clipper's eligibility lookups and the clip gate all use it. On the webapp, "linked" is `access === 'linked'` from `/me`, never `Boolean(me.streamKey)`.

**Server: `linked(u)`.**
- `key, err := p.linkedKey(u)` (`links.go:69`).
  - `err != nil`: unknown. Answer 503 `store`.
  - `key == ""`: not linked.
  - Otherwise linked, and `key` is their stream key.
- Only `POST /api/v1/me/link` creates a link (`handleLink`, `api.go:476` → `setLink`, `links.go:99`), after Broadcast Box has matched the pasted publish token.
- A link ends in two ways:
  - the person clears it (`clearLink`, `links.go:143`);
  - someone else proves the same key: `setLink` deletes the earlier owner's entries (`links.go:115-122`).
- A username auto-match (`LinkSource == "username"`, `resolveUser`, `poller.go:426`) is **not** a link.

**Server: the stream's streamer.**
- `streamTag(k) = hex(HMAC-SHA256(p.handleSecret, "villanet-clips-tag\x00" + normalizeKey(k)))[:32]`: 32 hex characters, written as its **own helper** in `clips_access.go`, never through `streamHandle`. The public handle is the same HMAC over `cleanStreamKey(k)` (`handles.go:56-67`, `poller.go:276, :285`), so without the domain prefix the tag of an already lower-case key (the usual case) would equal the handle every snapshot broadcasts. It is kept in KV only and never sent to a browser.
- `streamerOf(u, tag)` = `linked(u) && streamTag(key) == tag`.
- `setLink` removes the previous owner's `link_user_` entry, so exactly one person satisfies this at any moment.
- It replaces every "manually linked streamer" test in the spec, including each captured `LinkSource == "manual"` check (§2.4.9 rule 1, the §2.9 deletion table, §2.10.3 `ofMyStream`/`canDelete`, §2.10.6). A streamer who unlinked, or whose key someone else has since proven, loses streamer rights at once.
- The captured `streamerId`, `streamerLink` and `streamName` stay, for display only.

**Webapp.**
- `const linked = me ? Boolean(me.streamKey) : undefined`, with `me` from `useIdentity()` (`identity.ts:31`).
- The server fills `me.streamKey` from `getLinkedKey` (`api.go:311`, `links.go:60`), which swallows read errors. It is therefore a **hint**, and the server decides.
- `undefined` (not loaded yet) renders no clip surface and no call to action.

### 3.2 The server gate

> **Amended 2026-10-05 (lock screen §3.6, built).** `requireClipsLink` and `requireClipsLinkSeen` are replaced by one `requireClipsAccess(w, userID, watchOK, seen)` (`server/clips_access.go`). It answers linked callers with their key. It lets a let-in caller through with `key == ""`, but only on the five routes in `clipWatchRoutes` (GET `/clips`, `/clips/clip`, `/clips/cards`, `/clips/thumb`, and GET and HEAD `/clips/video`). Every other let-in request gets 403 `watch_only`, a locked caller gets 403 `locked`, and a store error gets 503 `store`. `not_linked` is never sent now; the webapp still reads it as `locked`, for a tab loaded before the upgrade. The 10-second memory is now the access cache's (`accessSeenOf`), and the gate shares the front gate's 2-second memory of refusals. A handler that acts never sees an empty key. The structure below (route, method, gate, then handler) stands.

```go
// clips_access.go
const notLinkedText = "Link your account first — clips are for people who have linked their stream."

// requireClipsLink is the gate in front of every user clip route. It returns the caller's stream key.
func (p *Plugin) requireClipsLink(w http.ResponseWriter, userID string) (string, bool) {
    key, err := p.linkedKey(userID)
    if err != nil {
        p.API.LogWarn("Could not read a stream link", "error", err.Error())
        writeClipError(w, http.StatusServiceUnavailable, "store")
        return "", false
    }
    if key == "" {
        writeClipError(w, http.StatusForbidden, "not_linked")
        return "", false
    }
    return key, true
}
```

**Dispatch.** Before its `switch`, `ServeHTTP` (`api.go:104`) sends every path that equals `/api/v1/clips` or starts with `/api/v1/clips/` to `p.serveClips(w, r, userID)`, and `/api/v1/admin/clips…` to `p.serveAdminClips`. Inside `serveClips`, every route runs in this order:

1. 401 when `Mattermost-User-Id` is missing (existing, `api.go:112-116`).
2. An unknown sub-path gets 404 "Unknown endpoint."; a wrong method gets 405 "Method not allowed." (existing texts).
3. For `POST /clips/drafts` only: `clickAt := time.Now()`.
4. `requireClipsLink`, on **every** route, with no exceptions (there is no take-back bypass; Q-B). It runs **before** body parsing, id validation, id lookups and limiters. An unlinked caller can therefore never tell an existing, expired, unknown or malformed id apart, and a refusal never spends a token (precedent: the media search, `api.go:204`).
5. The limiter, then the handler, with `key` passed in.

**Link cache for media and cards.** `GET /clips/video`, `GET /clips/thumb`, `GET /clips/draft/video` and `GET /clips/cards` may reuse a **positive** answer for 10 s per user (`linkSeen` in the clips store). One playback makes many Range requests, a channel can mount many cards, and each gate is a KVGet. Every other route reads fresh. Negative or failed reads are never cached, and `clipsLinkChanged(u)` (A§3.9) drops the user's entry.

**Admin routes.** `/api/v1/admin/clips*` need `isSystemAdmin`, otherwise 403 `{"error":"That is not allowed.","code":"forbidden"}`, checked before body parsing, id validation, lookups and limiters. They do **not** run the link gate: Victor answered Q-A on 2026-10-02 with full moderation for every system admin, linked or not. This is the only exception to owner rule 1, and it is confined to `/api/v1/admin/clips*` and the System Console's AdminClips; the panel's `/api/v1/clips*` routes gate admins like everyone else.

**HEAD requests.** A cookie-authenticated HEAD carries no CSRF token. Mattermost therefore passes it on without `Mattermost-User-Id`, and the existing 401 answers [v9 S/app/plugin_requests.go:160, :208-209]. `<video>` never sends HEAD.

### 3.3 Who may call what

This replaces the Who column of §2.10 and the whole of §2.16. "Gate" means `requireClipsLink`. Everything else in §2.10 (requests, success bodies and other errors) stands.

> **Amended 2026-10-05 (lock screen, built).** The table gains a **Let in** column, and **Unlinked** is renamed **Locked**:
> - **Let in:** GET `/clips`, `/clips/clip`, `/clips/cards`, `/clips/video` (and HEAD) and `/clips/thumb` answer as they do for a linked user. Every `ClipView` then has `canVote`, `canReport`, `canDelete` and the new `canShare` all false, even for an admin and for a clip they made while linked. Every other route answers 403 `watch_only`.
> - **Locked:** every route answers 403 `locked` (formerly `not_linked`).
> - `ClipView` gains `canShare` (`!watchOnly && !bot`), and the webapp hides Share whenever it is false.

| Method + path | Linked user | Unlinked user |
|---|---|---|
| `GET /clips` | The list (§2.10) | 403 `not_linked` |
| `GET /clips/clip?id=` | `ClipView`, or 410 | 403 `not_linked` |
| **new** `GET /clips/cards?ids=<id>,…` | `{"cards":{"<id>":ClipCard\|null}}` (below) | 403 `not_linked` |
| `DELETE /clips/clip?id=` | Clipper: votes void. `streamerOf` the clip's `streamTag`: votes banked. A linked admin: votes void. Anyone else: 403 `not_allowed` | 403 `not_linked`, the clipper included (Q-B) |
| `GET /clips/video?id=` | 200 or 206 | 403 `not_linked` |
| `GET /clips/thumb?id=&v=` | 200 or 304, always `private, no-cache` | 403 `not_linked`, never 304 |
| `POST /clips/vote?id=` | As §2.9 | 403 `not_linked` |
| `POST /clips/report?id=` | As §2.10.5 | 403 `not_linked` |
| **new** `POST /clips/share?id=` | A§4.2 | 403 `not_linked` |
| `GET /clips/drafts` | Own drafts | 403 `not_linked` |
| `POST /clips/drafts` | `mayClip` (A§3.4) | 403 `not_linked` |
| `DELETE /clips/draft?id=` | Owner | 403 `not_linked`, the owner included (Q-B) |
| `GET /clips/draft/video?id=`, `PUT /clips/draft/thumb?id=`, `POST /clips/draft/publish?id=` | Owner | 403 `not_linked` |
| `PUT /clips/streamer` | Switches **their linked stream**, `streamTag(key)` | 403 `not_linked` (the spec said `forbidden`) |
| `POST /clips/wipe?handle=` | `streamerOf` the stream behind that handle (live or tail), or a linked admin; otherwise 403 `not_allowed` | 403 `not_linked` |

**`DELETE /clips/clip`, exactly:**
1. 405 for a wrong method.
2. The gate.
3. 400 when the id is malformed.
4. The write limiter.
5. Missing, or `now >= clipExpiresAt(entry)`: 200 `{"ok":true}`. An expired entry is left to the sweep, which banks it (owner rule 5).
6. `isSystemAdmin` and not the clipper: settle with reason `admin`.
7. `clipperId == caller`: settle with reason `clipper`.
8. `streamerOf(caller, entry.streamTag)`: settle with reason `streamer` (put off with 503 `store` while a voter's verdict is unknown, A§3.4).
9. Otherwise 403 `not_allowed`.

**Settle and expiry.** Inside settle's CAS, an entry with `now >= clipExpiresAt` is settled with reason `expired`, whatever reason was asked for, so a deletion racing expiry banks the votes. Test: `TestClipperDeletingAnExpiredClipBanksIt`.

**`ClipCard`** (for `GET /clips/cards`) is `{"id","title","createdAt","durationMs","thumb"?,"clipper":ClipPerson,"streamer":ClipPerson}`: no voters, no upvotes, no viewer flags.
- `ids` holds 1 to 25 distinct ids of 16 hex, comma-separated; anything else is 400 `bad_request` (after the gate).
- It reads the clips cache under the §2.8 rules (no per-id KV read). It makes no `isSystemAdmin` call. Names come from the 10-minute user cache of A§3.4, with one `GetUser` per miss.
- A missing or expired id maps to `null`. Halted with no cache: 503 `store`. `Cache-Control: no-store`. No limiter, like the other GETs.

**Admin routes** (`isSystemAdmin` only, no link gate; Q-A answered yes). The existing ones from §2.10 stand. New:

| Method + path | Limiter | Success | Errors |
|---|---|---|---|
| `GET /api/v1/admin/clips/video?id=` | — | 200/206 `video/mp4`, served as in §2.11 | 403, 404, 410 `clip_gone` |
| `GET /api/v1/admin/clips/thumb?id=&v=` | — | 200 `image/jpeg`, `private, no-cache` | 403, 404 |
| `DELETE /api/v1/admin/clips/clip?id=` | write | 200 `{"ok":true}` (also if already gone); settle reason `admin` (votes void) | 403 |
| `POST /api/v1/admin/clips/wipe?handle=` | write | 200 `{"ok":true,"draftsRemoved":n}`, as the §2.10.6 wipe | 403, 400 |

### 3.4 Changed checks

**`mayClip(u, handle, source)`** replaces the list in §2.4.9:
0. Linked: the gate has already passed.
1. `streamerOf(u, streamTag(k))`, where `k` is the live stream's key, or the key the tail captured.
2. `source == "live"`: u has a session on that handle that was listed in the latest poll, or opened within `viewerGrace`.
3. `source == "tail"`: as §2.4.9 rule 3.

Otherwise the answer is 403 `not_watching`.

**`noteViewer`** keeps recording **every** non-peek session while Clips is on, linked or not.
- The link is judged at click time, so someone who links mid-stream can clip at once instead of being told to tune in again.
- The registry stays memory-only, kept 15 minutes and never shown, as the README already discloses.

> **As built (3.0.0).** Every non-peek session is noted **whether or not Clips is on**, and switching Clips off no longer clears the registry; only the plugin stopping does (spec §2.4.9 note). Otherwise everybody already watching was refused `not_watching` when an admin switched Clips on. The README discloses the registry, as built, under "Who is watching", "Top Snoopers" and "Security posture"; before 3.0.0's docs pass it did not, whatever this section assumed. The `EnableClips` help text in `plugin.json` does not mention it yet.

**§2.10.1 draft creation.**
- Step 0 is `clickAt` and step 0a is the gate, both before step 1.
- Step 6 reads `doc.optOut[streamTag(k)]`.
- The draft also keeps `streamTag` (server-only).

**§2.10.2 publish.**
- The gate is the new step 0, **before** the draft lookup. Otherwise step 2 would hand an unlinked caller a `ClipView`.
- Step 5 uses the order in A§2.1.
- The new entry takes `streamTag` from the draft.

**Eligibility** replaces §2.9's. `eligible(u)` is true when all of these hold:
- `GetUser` succeeds;
- `DeleteAt == 0`;
- `!IsBot`;
- u is not in `doc.blocked`;
- **u is linked now** (`linkedKey(u)` non-empty).

It is cached:
- One cache holds the user and link verdict for 10 minutes.
- Before each CAS, look up every id in the document that is not cached: one `GetUser` and one KVGet each.
- A transient error keeps the last known verdict. A user never successfully looked up is not counted (fail closed).
- `clipsLinkChanged(u)` drops u's entry.

**Unknown verdicts never move the title or lose votes.** Fail-closed counting would otherwise combine badly with the tie-keeps-holder rule (§2.9) and the recompute at activation, when the cache is cold: one failed lookup of the holder would hand the title to a tied clipper for good.
- If any user id in the document (clipper, voter or holder) has never been looked up successfully, the CAS keeps `doc.holder` unchanged.
- A settle with reason `expired` or `streamer` whose entry has such a voter is put off: the sweep retries on its next run, and a streamer's delete answers 503 `store`.
- Other mutations (votes, reports, deletes that void) proceed. The displayed upvote count may briefly leave the unknown voter out.
- Tests: `TestUnknownVerdictNeverMovesTheHolder`, `TestBankingWaitsForKnownVoters`.

What follows from it:
- Votes count only while the voter is linked (Q-C).
- A holder who unlinks loses Best Clipper at the next recompute; the score of their **live** clips is kept and comes back on relink.
- Banking freezes the counted votes at the moment of settling. So a vote whose voter is unlinked when the clip expires, or when its streamer deletes it, is not banked and does not come back on relink (Q-C states this consequence).
- Unlinked people cannot vote.

**Deletion** replaces §2.9's table:

| Who deletes | Route | Link needed | Votes |
|---|---|---|---|
| The clipper | `DELETE /clips/clip` | Yes (Q-B) | Void |
| `streamerOf` the clip's stream | `DELETE /clips/clip` | Yes | Banked: the clipper keeps them |
| A linked system admin, from the panel | `DELETE /clips/clip` | Yes | Void |
| A system admin, from the console | `DELETE /admin/clips/clip` | No (Q-A answered yes) | Void |
| Expiry, or any deletion of an entry already past expiry | sweep or settle | — | Banked |

When several apply, the order is unchanged: admin (when the requester is an admin and not the clipper), then clipper, then streamer.

### 3.5 KV document changes (§2.8)

```jsonc
"clips":[{ /* every §2.8 field, plus: */
  "streamTag":"<32 hex>",      // streamTag(key) at capture (A§3.1; not the handle). KV only; never in a response.
  "shares":["<postId>", …]     // omitempty. Every share post of this clip, at most 10; an 11th share is refused (A§4.2). KV only.
}],
"optOut":{"<streamTag>":{"by":"<userId>","at":1790000000000}}   // was keyed by user id
```

**The opt-out is keyed by stream, not by person.**
- Unlinking does not quietly restart recording of a stream its streamer switched off.
- Relinking to another stream does not carry the setting over. The new stream starts allowed (owner rule 3) and shows the notice.
- Whoever proves that key next sees it off and may switch it on. This follows `setLink` (`links.go:115`): a verified token beats any earlier claim.

§2.4.5 step 2 becomes: a stream is off when `optOut[streamTag(s.StreamKey)]` exists, whatever its `LinkSource`. `p.recorders().optOut(tag)` stops and wipes the stream whose key has that tag.

### 3.6 Snapshot and `/me` (§2.12)

- **`bestClipRef` shrinks to `{"id"}`**, with no title and no upvotes. The webapp type becomes `bestClip?: {id: string}` (§3.1). Linked readers fetch the title **and** the upvotes with `GET /clips/clip?id=` (the `ClipView`'s `title` and `upvotes`).
- **`clips`, `clipTails` and `apiStream.clip`/`clipFrom`/`clipTail` stay in the shared snapshot.**
  - It is broadcast to every user (`poller.go:657-659`) and served by `GET /streams` (`api.go:119-124`), and making these fields per-user would need per-user pushes.
  - They carry counts, timestamps, states, stream titles that are already on the cards, and a hash.
  - Unlinked clients read only `clips.count`, and `clips.retentionDays` for the post card's expiry rule (A§4.9 state 2).
- **`bestClipper` stays for everyone**, for the badge, the popover and the Trophies holder line. It is an award, like the crowns and medals, and names a person and a score, never clip content.
- **`me.clipsStreamer`** is `{"allowed":bool}` for **every linked user**. `allowed` is true when no opt-out exists for `streamTag(key)`. Absent for everyone else.
- **`me.clipsAlert`**: for admins, linked or not. It carries counts and states, never clip content, and points every admin to System Console, where clips can be reviewed with or without a link (Q-A).

### 3.7 Error codes and copy

> **Amended 2026-10-05 (lock screen, built).** Two codes are added. `locked` (403): "VillaNET is locked. Unlock it with your bearer token.", which the webapp words "VillaNET is locked." `watch_only` (403): "You are let in to watch only. Link your stream to do that.", which the webapp words "You can watch, but not do that." `not_linked` is no longer sent, and the webapp words it as `locked`. None of the "Link your account first…" copy below is shown any more, and the [Link Your Account] actions are gone. The editor's copy for all three codes is now "Your stream is no longer linked. Link it again to publish this draft (kept until {time}).", with [Link your stream] and [Later] and no Discard. Its link action calls `openLock()`, which opens the let-in footer's bearer token bar for somebody let in, and the lock for anybody else. Any refusal with `locked`, `watch_only` or `not_linked` makes the page read `/me` again, at most once every 2 s.

**Added to §2.10.7:**

| code | HTTP | Server text |
|---|---|---|
| `not_linked` | 403 | Link your account first — clips are for people who have linked their stream. |
| `cannot_post` | 403 | You can't post in that channel. |
| `share_cap` | 409 | This clip has already been shared in 10 places. |

- `forbidden` is no longer used by the streamer switch. It stays for bots and for non-admins on admin routes.
- The webapp adds `'not_linked' | 'cannot_post' | 'share_cap'` to `ClipErrorCode` (§3.1).

**Added to §3.13:**

| code / context | Copy | Action |
|---|---|---|
| `not_linked`, default | "Link your account first — clips are for people who have linked their stream." | [Link Your Account] |
| `not_linked`, for one clip: card Watch, Trophies Watch, `openClips(id)`, a clip link | "Link your account first to watch this clip." | [Link Your Account] |
| `not_linked`, editor open | "Your account is no longer linked. Link it again to publish this draft — it is kept until 21:40." | [Link Your Account] [Discard] [Later] |
| `cannot_post` | "You can't post in that channel." | — |
| `share_cap` | "This clip has already been shared in 10 places, which is the most it can be." | — |
| Shared (connected-workspace) channel | "Clips can't be shared into a shared channel" (title of the disabled Share button) | — |
| `rate_limited`, share | "That's a lot of sharing in a row. Try again in a few minutes." | — |
| `clip_gone`, share | "This clip has gone, so it can't be shared." | — |
| `network`, share | "The clip could not be shared just now." | [Try again]. Safe: the server de-duplicates for 60 s. |
| No current channel | "Open a channel to share this clip." (title of the disabled Share button) | — |

**Any `not_linked` response, anywhere,** calls `refreshIdentity()` once (coalesced `fetchMe().then(setIdentity)`), so every surface switches to the call to action. This also catches someone whose link was silently replaced by another person's token. The panel switches too, because the Sidebar follows identity changes (A§2.2).

### 3.8 Webapp surfaces

> **Amended 2026-10-05 (lock screen §5–§7, built).** The **locked view** (`LockedView`), the scissors' link row, the clapper's and Clips row's locked labels, the Clippers tab's call to action, the card's in-card link row and the Link Your Account button are all removed, because a locked person never sees the panel. What replaces this table:
> - **Locked** (system admins included): the panel is the lock screen. The header is the wordmark alone, with no clapper and no trophy. `ClipsHost` renders nothing, and no `/clips*` request is made. A shared card is drawn from the post, with no thumbnail; its Watch and picture open the lock, and the clip opens once it is unlocked. A thread popout has no panel, so there the card reads "Open VillaNET in the main window to unlock it." A clip link lands on the lock with the clip waiting. The streamer controls never show.
> - **Let in:** the Clips row (no drafts, no "just ended") and the clapper (no draft count) are shown. The dialog has the list and the viewer, but no Just ended, no Drafts and no Yours. The upvote is a static count, and Report, Delete and Share are absent. There is no ✂, and C does nothing. Clippers' Watch works. The empty list reads "No clips yet." with no invitation. A live stream matched to them by username and kept for clipping gets one line, "Your stream is being kept for clipping. Link it to switch that off.", with no switch.
> - **Linked:** as below, except that the upvote is now a thumbs up (on each card in the list and in the viewer), and the streamer controls live in the footer bar's "Clips of your stream" sheet, opened by the scissors.
> - AdminClips is unchanged: full moderation for every system admin, whatever their own panel shows.

"Unlinked admin" means a system admin who has not linked a stream.

| Surface | Linked | Unlinked | Unlinked admin |
|---|---|---|---|
| ✂ on a live tile or WaitingTile, and the C key | As §3.4 | Rendered in the same places, under the same render condition (§3.4 Placement). **Every row of the §3.4 state table is replaced by one enabled row**: label and title "Link your account to clip this stream"; a click or C calls `askToLink()`. No disabled state shows. When `linked === undefined`, the ✂ is not rendered at all | As unlinked |
| ClipEditor | As §3.6 | Never opens. If the link goes while it is open (identity changes, or any call returns `not_linked`), it becomes **locked**: the editor copy from A§3.7, with the stage, trimmer, title and PUBLISH inert | As unlinked |
| Header clapper (`SidebarTitle`) | As §3.9 | Shown when `clips.count > 0`. No pill and no dot. aria-label "Clips — link your account to watch them". A click opens the locked dialog | As unlinked |
| Panel Clips row | As §3.9 | Shown when `clips.count > 0`: "CLIPS · 23", no NEW. aria-label "Clips, 23 — link your account to watch them". A click opens the locked dialog | As unlinked |
| ClipsModal | As §3.9, plus Share (A§4.10) | **Locked view** (below), **whenever `linked === false`, whatever `DialogState` says**. On the switch to locked it pauses and unloads any `<video>` (`pause()`, `removeAttribute('src')`, `load()`) and drops the cached list and drafts. No `fetchClips`, no `refreshDrafts`, no thumbnail requests | As unlinked |
| `openClips(id)` | Opens the viewer | Locked view with the per-clip text | As unlinked |
| Vote, Report, Delete, Share in the viewer | As spec. Delete as admin only when the admin is linked | Unreachable | Unreachable |
| Trophies › Clippers tab | Badge, name, "142 upvotes · 9 clips", then "Best clip: “that jump” ▲31 [▶ Watch]". The title **and** the ▲ count come from the `ClipView` of `GET /clips/clip?id=bestClip.id`, fetched when the tab opens; the line is hidden on 410 or any error | Badge, name and score, then "Link your account to watch their clips." [Link Your Account]. When `linked === undefined`, neither line | As unlinked |
| AwardBadges, AwardList, PopoverLive | Badge and row | **The same**: it is a public award | The same |
| Streamer controls (§3.11) | As spec, acting on their linked stream | Nothing, **except** a live, recorded stream attributed to them by username (`userId === me.userId`, `linkSource === 'username'`, `clip` set): a read-only notice above Link Your Account | As unlinked |
| Link Your Account button (`Sidebar.tsx:1178-1191`) | — | Tooltip: "Link the stream you publish to your Mattermost account, so the panel knows it is yours — and so you can plan streams in Up next, join other people’s, and make, watch, upvote and share clips" | The same |
| AdminClips | Full (a linked admin). [Watch] plays inline from `/admin/clips/video` | — (the console needs `manage_system`) | **Full, as for a linked admin** (Q-A answered yes): AdminClips lists, plays inline and moderates. The panel itself stays locked for them until they link |
| `clipsAlert` banner | Admins | — | Shown, as for a linked admin |
| Shared-clip post card | A§4.9 | A§4.9 | As unlinked |
| Requests made | `fetchClips`, `refreshDrafts` and thumbnails as the spec says; `refreshDrafts` on host mount; `fetchClipCards` for post cards | **None** to `/clips*` | None to `/clips*`; AdminClips calls `/admin/clips*` as for a linked admin (Q-A) |

**The locked view** reads "CLIPS NEED A LINKED ACCOUNT" / "Clips are for people who have linked their stream." / "Link your account to clip streams you're watching, and to watch, upvote and share clips." [Link Your Account]. Nothing else: no list of the person's own clips (Q-B), and no admin line.

**The username-attributed notice** reads: "Your stream is kept for clipping: its last 2:00 is held on this server so linked viewers can clip it. Link your account to switch that off or wipe it." [Link Your Account].

**State that changes.**
- `DialogState` (§3.3) gains `{view: 'locked'; clipId?: string}`. ClipsModal also renders the locked view whenever `linked === false`, whatever `DialogState` says (table above), and returns to the stored state when `linked` becomes true.
- `refreshDrafts()` and `fetchClips()` run only while linked, and run again when `linked` becomes true.
- Lab check: open the list, then the viewer playing a clip, and flip Linked to no. The dialog locks, the video stops and DevTools shows no further clip request.

### 3.9 Unlinking, relinking and other edge cases

> **Amended 2026-10-05 (lock screen, built).** Unlinking now also locks the panel, or leaves watch only for somebody who is on the let-in list. A system admin can unlink somebody from System Console → Access (`POST /api/v1/admin/unlink`), which runs the unlink path below for them, `clipsLinkChanged` included. Published clips stay listed and watchable for linked **and let-in** readers. Going from linked to let in closes the clip editor and drops drafts from the page; the drafts stay on the server for their two hours, as below.

| Event | What happens |
|---|---|
| u unlinks | **Drafts** are kept until their 2 h TTL, unreachable; relinking (to any key) within 2 h gives them back, and a publish already running finishes. **Published clips** stay listed and watchable for linked users. u can neither see nor delete them until they relink; any system admin can delete them meanwhile from System Console (Q-A, Q-B). u **loses streamer rights** over clips of their old stream at once. **Votes u cast** stop counting until u relinks, and are lost for good on clips that are banked meanwhile (Q-C). **Best Clipper**: a holder who unlinks loses it at the next recompute, and the score of their live clips is kept. **Opt-out**: stays on the stream. |
| u relinks to a different stream | Streamer rights move to the new stream's clips. Votes count again. The new stream starts allowed and shows the notice. |
| Someone else proves u's key | For u this is the same as unlinking; `setLink` does it with no notice (`links.go:115-122`). The new owner gets streamer rights and the opt-out switch, and sees the stream as it was left. |
| An admin who has never linked | In the panel, an ordinary unlinked user (locked). In System Console, full moderation: `/admin/clips*` checks `isSystemAdmin` only (Q-A answered yes). Switching Clips off in the plugin settings still stops recording at once. |
| Clips switched off | The gate still applies. Published clips stay listable, watchable, votable and shareable by linked users, as §2.3 says. |

**`clipsLinkChanged(u)`** is called:
- from `handleLink` after a successful unlink (after `refreshSoon`, `api.go:502`) and after a successful link (`:545`);
- from `setLink` for the superseded owner, after the deletes at `links.go:115-122` succeed.

It drops u's eligibility and link-cache entries, then arms a holder recompute: a no-op `clipsCAS`, on a goroutine, which never blocks the link response. It does nothing when the clips store was never started. Other cluster nodes catch up within the 10-minute cache.

### 3.10 Spec text that changes

Read these sections through the rule above; the builder follows this list.

- **§0**:
  - Add the row "Who may use Clips: linked people only, on every `/api/v1/clips*` and `/api/v1/admin/clips*` route (A§3)".
  - "Who may clip": A§3.4.
  - "Streamers": opt-out per stream.
  - "Votes on deleted clips": only linked voters count.
  - "Thumbnails": "Anyone can report" becomes "linked people can report".
  - "Discovery": Share and the clip link are in 3.0.0 (A§4).
- **§1**:
  - The flow line "Everyone: Clips row / clapper…" becomes "Linked people:".
  - Limit 12: "by linked people who were watching".
- **§2.1**:
  - New file `clips_share.go` (A§4).
  - `clips_access.go` gains `requireClipsLink`, `streamerOf`, `streamTag`, the link cache and the eligibility cache.
  - `clips_api.go` gains the share route, the cards route, the four new admin routes, which check `isSystemAdmin` only (Q-A answered yes).
  - `api.go` and `links.go` gain the `clipsLinkChanged` calls.
- **§2.2**: replace the `EnableClips` help text with: "A ✂ on every playing stream keeps its last few minutes as a draft that person can trim, name, publish to Clips and share into a channel. Making, watching, upvoting and sharing clips is for people who have linked their stream with Link Your Account. While this is on, this server keeps a rolling recording of each live stream on its own disk (never 24/7 channels) by opening one extra playback session per stream to Broadcast Box over UDP. Broadcast Box's own pages count that session as a viewer; VillaNET's counts do not. Streamers who have linked their stream can switch clipping of it off, and wipe what is held. Switching this off stops recording at once; published clips stay until they expire."
- **§2.3**: "Existing clips are still listed and served" becomes "…to linked users".
- **§2.4.5** step 2, **§2.4.9**, **§2.8**, **§2.9**, **§2.10**, **§2.10.1**, **§2.10.2**: A§3.3–A§3.5.
- **§2.10.3**:
  - `ofMyStream` is `entry.streamTag == streamTag(viewerKey)`, and `canDelete` follows A§3.4.
  - `bestClipRef` becomes `{id}`.
  - `ClipCard` is added (A§3.3).
  - `streamTag` and `shares` are never serialised.
- **§2.10.6**:
  - `PUT /clips/streamer` acts on `streamTag(key)`.
  - `not_linked` replaces `forbidden`.
  - Admins wipe through `POST /admin/clips/wipe`.
- **§2.10.7**, **§3.13**: A§3.7.
- **§2.12**: A§3.6.
- **§2.13**: settling marks shared posts gone (A§4.5).
- **§2.15**: add the share and marker log lines (A§4.2, A§4.5).
- **§2.16**: replaced by A§3.3.
- **§2.17**: the plugin now creates posts as the sharing user, edits their props when a clip goes, and may withdraw a share it could not record (A§4). No new network peers: shares into shared (connected-workspace) channels are refused (A§4.2 step 7).
- **§3.1**, **§3.2**: add the fields and functions in A§3.7 and A§4.10. `BestClipper.bestClip` becomes `bestClip?: {id: string}`. Add `export type ClipCard = {id: string; title: string; createdAt: number; durationMs: number; thumb?: string; clipper: ClipPerson; streamer: ClipPerson}` and `fetchClipCards(ids: string[]): Promise<{cards: Record<string, ClipCard | null>}>`, plus `adminClipVideoURL(id)`, `adminClipThumbURL(id, v)`, `adminDeleteClip(id)`, `adminWipeRecording(handle)`. `fetchClips()` keeps the spec's signature.
- **§3.3**: A§3.8, A§4.8.
- **§3.4**: A§3.8, A§2.2.
- **§3.6**: the locked editor state.
  - The published copy becomes "“that jump” is in Clips until 31 Oct." (drop "for everybody").
  - The published actions become [Watch it] [Share in Town Square] [Done] (A§4.10).
- **§3.9**–**§3.12**: A§3.8.
- **§3.14**: any system admin, linked or not (Q-A answered yes). Watch is inline (A§2.2).
- **§3.15**: A§4.11. **§3.16**: A§4.12.
- **§4 README**:
  - "Making a clip", "Clips and voting", "Best Clipper", "For streamers", moderation and "Security posture" all say linked-only, admins included.
  - Add "Sharing a clip" (A§4), including the phone landing-page sentence and the gone-state limits of A§4.6.
  - "Security posture" also says: VillaNET checks clip posts only while it is running; a `custom_villanet_clip` post made through the REST API while VillaNET is disabled is not checked, and renders as a card once it is back (A§4.4).
  - Moderation: a halt needs a restart (A§2.1).
- **§5**: replaced by A§5.
- **§6**: reword A3 ("A linked user who is not watching…"; an unlinked user gets the Link call to action), A5 ("every other linked user's"), A7, A8, A9, A12, A21 and A22 accordingly. Add A25–A33 (A§5.3).
- **§7**: remove item 3. The link route is in 3.0.0 as `/plug/ceo.villa.villanet/clip/<id>` (A§4.6).
- **Appendix A**:
  - P1 [streamer consent]: "streamer" means the current link (A§3.1).
  - P2 [watch gate]: gains the link as step 0.
- **Appendix B**:
  - Q12 and Q15 are overridden by owner rules 2 and 7.
  - Q4: the opt-out is per stream.
  - Q6 is done.
  - Q7: no download button; `controlsList="nodownload"` stays, and no download helper exists anywhere (do not use `downloadJSON`, `client.ts:89`).
  - Q10: add "linked now".

### 3.11 Go tests (linked-only)

Add to §2.19:
- `TestEveryClipRouteRefusesUnlinked`: table-driven over **every** route and method under `/api/v1/clips` (share, cards and draft routes included, and the clipper's own delete and the owner's own draft discard). There are no exceptions among these. Admin routes are not in this table: they check `isSystemAdmin` only (Q-A answered yes; see `TestAdminRoutesNeedAnAdminNotALink`).
  - Each answers 403 `not_linked` with `Cache-Control: no-store`.
  - None reads the `clips` key, opens a file or spends a limiter token.
  - A malformed id gets the same 403, not 400.
  - A thumbnail request with a matching `If-None-Match` gets 403, not 304.
  - Publishing an already-published draft gets 403, not 200.
- `TestNotLinkedComesBeforeLookup`: unknown, expired, existing and malformed ids give byte-identical answers to an unlinked caller, the clip's own clipper included.
- `TestLinkReadErrorIsStoreNotNotLinked`.
- `TestUnlinkedVotesStopCounting`, `TestUnlinkedHolderLosesBestClipper`, `TestRelinkRestoresVotesAndHolder`.
- `TestUnknownVerdictNeverMovesTheHolder`, `TestBankingWaitsForKnownVoters` (A§3.4).
- `TestClipperDeletingAnExpiredClipBanksIt` (A§3.3).
- `TestCardsRoute`: at most 25 ids; a gone id maps to `null`; no voters, upvotes or viewer flags; no `HasPermissionTo` call.
- `TestStreamerRightsFollowTheCurrentLink`:
  - after someone else proves the key, the old owner cannot delete, wipe or opt out, and the new owner can;
  - a key in different case still matches.
- `TestOptOutIsKeyedByStreamAndSurvivesUnlink`.
- `TestAdminRoutesNeedAnAdminNotALink`: a non-admin, linked or not, gets 403 `forbidden` on every `/admin/clips*` route, before any id check, lookup or limiter; an unlinked admin and a linked admin can both list, watch, see thumbnails, delete, block and (from Phase 4) wipe (Q-A answered yes).
- `TestDraftsSurviveUnlinkButAreUnreachable`.
- `TestSnapshotCarriesNoClipContent`: a snapshot with clips (title `SECRET-TITLE`), drafts, **a live stream and a tail, all with a lower-case key**, contains no title, thumbnail id, voter id, post id, stream key or stream tag.
- `TestStreamTagIsNotTheHandle`: for a lower-case key, `streamTag(k) != streamHandle(k)`.
- `TestMediaLinkCacheExpires`: positive answers only, 10 s, the cards route included, cleared by `clipsLinkChanged`.
- `TestPublishRefusesWhenTokenUnreadable` (A§2.1), `TestStopAllTouchesNoFilesWithoutARoot` (A§2.1).
- `TestNoteViewerRecordsUnlinkedSessions`: someone who links mid-stream can clip without tuning in again.
- Update `TestWatchGate` (link step 0; the streamer by current link). Rename `TestOnlyManualStreamersMayOptOutOrWipe` to `TestOnlyTheLinkedStreamerMayOptOutOrWipe`.

---

## 4. Sharing

### 4.1 The flow

```
Viewer or editor: [Share in Town Square] → "Click again to post it in Town Square" → POST /clips/share?id= {channelId}
  server: gate → checks (A§4.2) → CreatePost as the user, type custom_villanet_clip, props + a fixed plain-text message
          → MessageWillBePosted checks the one-time nonce → post saved → id appended to entry.shares (at most 10)
Channel: every reader's webapp renders ClipPostCard from the post
  linked reader:   GET /clips/cards?ids=… (batched) → title, names, thumbnail; Watch → panel + clip viewer
  unlinked reader: text from props only, no picture, no request; Watch → "Link your account first…" + [Link Your Account]
Mobile, Threads list, notifications, email: the fixed message ("🎬 VillaNET clip · 0:45" and the link), no title or names;
  the link opens /plug/ceo.villa.villanet/clip/<id> in Mattermost
Clip expires → settle → each shared post gets props.villanet_clip_gone "expired" → every card says so
Clip deleted → settle → "deleted", and the title, names and user ids are removed from the props
```

### 4.2 `POST /api/v1/clips/share?id=<16 hex>`

**Request:** JSON `{"channelId":"<26-character Mattermost id>"}`, at most 1 KiB.

**Response:** 201 `{"postId":"…","channelId":"…"}`, or 200 with the same shape for a repeat (step 11).

**Limiter:** `newRateLimiter(10, 10*time.Minute)`, kept in the clips store.

**Checks, in order:**

| # | Check | Answer |
|---|---|---|
| 1 | 401 and 405 (existing) | |
| 2 | `requireClipsLink` | 403 `not_linked`, or 503 `store` |
| 3 | `id` matches `^[0-9a-f]{16}$`; the body is valid JSON; `model.IsValidId(channelId)` | 400 `bad_request` |
| 4 | The caller is a bot (`GetUser`, `IsBot`) | 403 `forbidden` |
| 5 | The store is halted with no cache | 503 `store` |
| 6 | The clip is not in the cache, or `now >= expiresAt` | 410 `clip_gone` |
| 6b | The cached entry already holds 10 `shares` (and step 11 does not apply) | 409 `share_cap`. No token is spent. |
| 7 | `GetChannel(channelId)` fails, `channel.DeleteAt != 0`, or `channel.IsShared()` [v9 server/public/model/channel.go:59, :370-371] | 403 `cannot_post` |
| 8 | `GetChannelMember(channelId, userID)` fails | 403 `cannot_post` |
| 9 | `!HasPermissionToChannel(userID, channelId, model.PermissionCreatePost)` | 403 `cannot_post` |
| 10 | A DM (`model.ChannelTypeDirect`) whose other member (from `channel.Name`, `<id>__<id>`; a self-DM has none) fails `GetUser` or has `DeleteAt != 0` | 403 `cannot_post` |
| 10b | A DM or GM while `*GetConfig().TeamSettings.RestrictDirectMessage == model.DirectMessageTeam`: the members (DM: from the name; GM: `GetChannelMembers`) must have at least one team in common, from `GetTeamsForUser` for each. This mirrors master's `CheckIfChannelIsRestrictedDM` [master S/app/channel.go:4555-4570], applied on every version. A read error is 503 `store`. | 403 `cannot_post` |
| 11 | The same user shared the same clip into the same channel within 60 s (in memory, per node) | 200 with that earlier post. Nothing is posted. |
| 12 | Take a share token | 429 `rate_limited` |
| 13 | Create the post (A§4.3) | An AppError with a 4xx status: refund, 403 `cannot_post`. Any other: refund, 503 `store`. Both log Warn "Could not post a shared clip" with `user_id`, `clip_id`, `channel_id`, `error`. |
| 14 | `clipsCAS`: append the post id to `entry.shares`. | **Every share post must be tracked**, so the marker can reach it. If the entry has gone meanwhile: `DeletePost` the new post, refund, 410 `clip_gone`. If `shares` already holds 10 (a race past 6b): `DeletePost`, refund, 409 `share_cap`. If the CAS fails: `DeletePost`, refund, 503 `store`, and log Warn "Could not remember a shared clip post". A failed `DeletePost` logs Warn "Could not withdraw a shared clip post" with `post_id`. |
| 15 | Log Info "A clip was shared" with `user_id`, `clip_id`, `channel_id`, `post_id` | 201 |

- **Sharing works while Clips is switched off** (Q-E), like watching and voting.
- **Blocked clippers may share**, as they may vote.
- **No `republishClips`**: `shares` is not part of `clips.version`.

> **As built (3.0.0), A§4.2.**
> - **Step 0, an attempt limiter.** Right after the gate, before step 3, a share, a draft and a publish each take a token from one limiter shared by the three routes, 60 a minute per person, refused or not (`clipAttemptsPerMinute`, `tooManyClipAttempts`): 429 `rate_limited` before any KV read or Mattermost call. The step 12 token is unchanged.
> - **Step 6b, deleted posts free share slots.** When the entry holds 10 `shares`, the route asks Mattermost which of them still exist (`GetPost`; only a 404 counts as gone), at most once a minute per clip per node (`shareRecountEvery`), and a deleted post no longer counts toward the cap. The ids it found gone are dropped from `shares` in step 14's CAS.
> - **Step 10b counts members as master does** (`countsForTeamRule`): a member who is deactivated or no longer exists is not counted, nor a bot Mattermost lets message anybody (system-owned, owned by the sharer, or owned by a plugin installed here [master S/app/bot.go:391-430]). With nobody left to count it answers `cannot_post`.
> - **Not built:** a per-sharer limit on a clip's 10 slots. Any linked person can fill them; the slots exist so every share can be marked, not to ration sharing.

**Why steps 7–10 are needed.**
- The plugin's `CreatePost` checks neither membership, nor permission, nor archive state. It calls `CreatePostMissingChannel` [v9 S/app/plugin_api.go:636-639; S/app/post.go:119-131]. The archive check exists only in the REST path, `CreatePostAsUser` [v9 S/app/post.go:69, :82-83].
- `HasPermissionToChannel` exists from server 5.3 [v0.4.4 plugin/api.go:1035-1036]. It honours channel-scheme (moderation) roles, but checks neither archive state nor membership, and a system admin passes for any channel [v9 S/app/authorization.go].
- A DM with a deactivated user is blocked only by the webapp [v9 W/components/channel_view/channel_view.tsx:98-108].
- Master also refuses shared DMs and GMs inside `app.CreatePost` [master S/app/post.go:173-174], but nothing on 9.x–10.x, and nothing on master, refuses a shared **channel**. A share there would sync the post, its props and the link to another Mattermost server, whose users can never be linked here. Step 7 refuses it.
- From 11.x the REST path refuses posts in a DM or GM whose members share no team when `RestrictDirectMessage` is "team" [master S/app/post.go:59-66]; plugin `CreatePost` skips that check [master S/app/plugin_api.go:900-925], and 9.0–10.11 have no such check at all (absent from `app/post.go` at v9.11.0, v10.0.0, v10.5.0 and v10.11.0). Step 10b applies it everywhere.

### 4.3 The post

```go
const clipPostType = "custom_villanet_clip"

post := &model.Post{
    UserId:    userID,      // the person who pressed Share (Q-D); never the bot
    ChannelId: channelID,   // a root post, never a reply
    Type:      clipPostType,
    Message:   clipShareMessage(siteURL, view),
}
post.SetProps(model.StringInterface{ /* the keys below */ })
created, appErr := p.API.CreatePost(post) // no lock is held across this call (A§4.4)
```

**What Mattermost does with it** (all verified in source):
- It accepts any existing `UserId` (it only checks that the user exists) [v9 S/app/post.go:221-230], and any `custom_` type; `Post.IsValid` enforces the prefix and caps props at 800,000 runes [v9 server/public/model/post.go, `PostCustomTypePrefix`, `PostPropsMaxRunes`].
- It adds `from_plugin: "true"` [v9 S/app/plugin_api.go:637]. Master re-adds it through `CreatePostFlags.FromPlugin`.
- It produces normal unreads and notifications [v9 S/app/post.go `handlePostEvents` → `SendNotifications`; S/app/notification.go:153].
- The Mumble bridge does not relay it, because of `from_plugin` (`bridge.go:317-318`).
- The sharer's own channel is marked read locally but not on the server [v9 W/actions/new_post.ts:104-110], so their other devices show it unread. That is acceptable.

**Props.** Props are visible to every channel member, linked or not, and any client can forge them (A§4.4). They therefore carry only what an unlinked reader's card may show, and every card treats them as an untrusted hint.

| Key | Type | Value |
|---|---|---|
| `villanet_clip_id` | string | The clip id, 16 hex |
| `villanet_clip_title` | string | `entry.title`, already cleaned at publish; may be `""` |
| `villanet_clip_duration_ms` | number | `durationMs` |
| `villanet_clip_created_at` | number | `createdAt`, in ms |
| `villanet_clip_clipper_id` | string | The clipper's user id |
| `villanet_clip_clipper` | string | `ClipView.clipper.name` at share time |
| `villanet_clip_streamer_id` | string | Omitted when the stream was not attributed |
| `villanet_clip_streamer` | string | `ClipView.streamer.name` at share time |
| `villanet_clip_gone` | string | Absent at first. Later `"expired"` or `"deleted"`, written only by the server (A§4.5) |
| `villanet_share_nonce` | string | Exists only between `CreatePost` and the hook, which strips it (A§4.4). *As built: the hook sets it to `""`; it stays on the stored post, empty (A§4.4 note).* |

**Never in props:** the thumbnail id or URL, the handle, the stream key or tag, voters, upvotes, the stream note, `type`, `attachments`. Do not rely on `from_plugin`: master strips it on update.

**Message.** The message carries **no user-controlled text**: no title and no names. The message is what the mobile app, the Threads list, pushes, email and Mattermost's message search see, and the plugin never rewrites it (a message change sets `EditAt` and shows "(edited)" [v9 S/app/post.go:673-677]). So a deleted clip's title cannot outlive it there, and a title or name cannot notify, link or format. The title and names live only in props, which the marker clears on deletion (A§4.5). Props are never scanned for mentions; only the message and `attachments` text are [v9 S/app/notification.go:1173-1187], and `attachments` is never set (A§4.4).

```go
func clipShareMessage(siteURL string, v ClipView) string
// line 1: "🎬 VillaNET clip · " + m:ss(v.DurationMs)
// line 2: "Watch it: " + strings.TrimRight(siteURL, "/") + "/plug/ceo.villa.villanet/clip/" + v.ID
//         left out when SiteURL is unset or empty; log Warn once per activation:
//         "Shared clips carry no link because Mattermost's Site URL is not set."
```

For example:

```
🎬 VillaNET clip · 0:45
Watch it: https://chat.example.com/plug/ceo.villa.villanet/clip/3f9a0c2e7b1d4a55
```

- `siteURL` is `*p.API.GetConfig().ServiceSettings.SiteURL`, nil-checked. It is never `meResponse.SiteURL`, which is Broadcast Box's URL.
- On mobile, an unknown `custom_*` post renders through the normal body, with the message as Markdown [mobile app/components/post_list/post/post.tsx:373-410; body/body.tsx:193-195], so the link is tappable.
- The global Threads list [v9 W/components/threading/global_threads/thread_item/thread_item.tsx:249-250], pushes and email show this message too. Whether the title should also be in the message is Q-F.

**Link previews.** With default settings the server does not fetch its own LAN address: link previews use the untrusted HTTP client [v9 S/app/post_metadata.go:663], which refuses reserved addresses (192.168.0.0/16 among them) and the server's own IPs unless they are listed in `AllowedUntrustedInternalConnections` [v9 server/platform/services/httpservice/httpservice.go:55-100; client.go:75]. So no preview is made. If an admin allows it, the preview is Mattermost's own page shell, which says nothing about the clip, and it shows only in search results [v9 W/components/post/post_component.tsx:447-464] and possibly in the mobile app; never under the card in a channel, because a registered plugin post type skips the additional content [v9 W/components/message_with_additional_content.tsx:24-35]. Nothing to build: no `attachments` fallback (it would put user text where mobile renders it and where mentions are parsed).

### 4.4 Forged and edited cards

**The threats** (verified in source):
- Any user can create a `custom_*` post through REST; only `system_` types are refused [v9 S/app/post.go:77-78].
- Before master, `from_plugin` is user-settable [v9 server/public/model/post.go `SanitizeProps`].
- On any post, `props.type` overrides which component renders it [v9 W/components/post_view/post_message_view/post_message_view.tsx:117-128].
- The author can patch props: `PostPatch.Props`, then `PatchPost` → `UpdatePost(…, false)`, which replaces props [v9 S/app/post.go:846-848, :679-684].

**Hook 1: `MessageWillBePosted(c, post) (*model.Post, string)`.** This hook runs for posts made by plugins, this one included [v0.4.4 plugin/hooks.go:159-160; v9 S/app/post.go:304-305].
1. When `post.Type != clipPostType` and `post.GetProp("type") != clipPostType`: return `nil, ""`. These are cheap string compares, with no API call and no lock.
2. When `props.type == clipPostType` on any other post: delete that prop and return the post.
3. When `post.Type == clipPostType`:
   - if `villanet_share_nonce` is a nonce this process issued in the last 30 s, consume it (single use), delete the prop and return the post;
   - otherwise **downgrade, do not reject**: set `post.Type = ""`, delete every `villanet_*` prop and `props.type`, and return the post. A forged card, or a copy, arrives as an ordinary message showing its plain text.
   - Why not reject: Mattermost's own copy paths re-create posts through `app.CreatePost`, which runs this hook. Master's Move Thread copies the root post that way and aborts the whole move on a refusal [master S/app/post.go:3492-3515; the hook at :410; a refusal becomes 400 "Post rejected by plugin. …", guarded_hooks.go:139-145]. So a thread whose root is a shared clip could never be moved. Shared Channels sync arrives through `CreatePost` too.
4. Nonces are 16 hex from `newID()`, kept in a `sync.Map` with an expiry and pruned on insert. The CreatePost RPC goes to this node's server, which runs this node's hooks, so they live in process.

**Hook 2: `MessageWillBeUpdated(c, newPost, oldPost) (*model.Post, string)`.** Returning nil rejects the update [v0.4.4 plugin/hooks.go:165-176; v9 S/app/post.go:701-707], so it returns nil **only** in step 1a. It also runs for the plugin's own `UpdatePost` [v9 S/app/plugin_api.go:723-724], and for pin and unpin, which go through `PatchPost` → `UpdatePost(…, false)` with the message unchanged [v9 S/api4/post.go:1013-1016; S/app/post.go:846-848]. `UpdatePost` clones the stored post, so `Type` can never change [v9 S/app/post.go:671].
1. When `oldPost.Type == clipPostType`:
   - a. If `newPost.Message != oldPost.Message`: return `nil, "Shared clip posts can't be edited."`. The message is VillaNET's fixed text (A§4.3); an author who swapped the "Watch it:" link would mislead mobile, Threads, email and search readers while web readers saw a genuine card. Pins still pass.
   - b. **Restore by content.** For each protected key (every `villanet_*` key, plus `type`, `attachments`, `from_webhook`, `override_username`, `override_icon_url`, `override_icon_emoji`, `webhook_display_name`): delete it from `newPost`, then copy it from `oldPost` when `oldPost` has it. Other props (for example Mattermost's `mentionHighlightDisabled`) pass.
   - c. **Apply a pending mark.** If `pendingMarks` (A§4.5) holds an entry for this post id with the same `villanet_clip_id`, apply it whoever triggered the update: set `villanet_clip_gone` to its value and, for `"deleted"`, delete `villanet_clip_title`, `villanet_clip_clipper`, `villanet_clip_clipper_id`, `villanet_clip_streamer` and `villanet_clip_streamer_id`. The entry is **not** consumed: applying it twice changes nothing, so an author's patch that lands first cannot steal the mark, and a slow `UpdatePost` cannot outlive it.
   - d. Return `newPost`.
2. Otherwise, when `newPost.props.type == clipPostType`, delete it.
3. Return `newPost`.

**Cost and safety.**
- Both hooks run for every post and edit on the server: one plugin RPC each.
- Neither may take a lock that the share handler or the marker holds across `CreatePost`/`UpdatePost`, because the hooks run while those calls wait. `pendingMarks` is a `sync.Map`.

**Card side.** The card renders only when `post.type === clipPostType` (A§4.9), so a `props.type` override from an older post, or a remote one, never shows a card.

> **As built (3.0.0), A§4.3–A§4.4.**
> - **Hook 1 never deletes; it overwrites.** Mattermost decodes `MessageWillBePosted`'s answer into the very post it sent [v9 server/public/plugin/client_rpc.go:641; master :1128], and gob sends no zero field and merges map entries into the map already there. A prop deleted in the hook would come back, and so would a type set to `""`. So: a share's `villanet_share_nonce` is set to `""` (and stays on the stored post, empty); a forged or copied card keeps a non-empty type, **`custom_villanet_clip_copy`** (`clipPostCopyType`), for which no webapp registers a component, so every client draws the message, and every `villanet_*` prop and `props.type` is set to `""`; an ordinary post claiming the card has `props.type` set to `""`. A `props.type` that is not a string at all also counts as a claim, since Mattermost's webapp up to 9.11 uses it as a key unchecked.
> - **Hook 2 restores everything but one prop.** From 9.3 its answer is decoded into a post of its own [v9.3.0 server/public/plugin/client_rpc.go:681-688], so deletion works there and a nil refuses (hence `min_server_version` 9.3.0). Instead of a list of protected keys, the new post gets **all** of the old post's props back, so no prop its author adds survives — **except `remove_link_preview: "true"`**, which only takes something away. The post's `FileIds` (10.11 and later let an edit add files) and `EditAt` are put back too, so an edit that changed nothing shows no "(edited)". A message edit is refused as specified.

**Limitation: only while VillaNET runs.** Mattermost's REST API accepts any `custom_` type and refuses only `system_` [v9 S/app/post.go:77-80], and hooks run only for active plugins. A `custom_villanet_clip` post made while VillaNET is disabled, crashed or being upgraded is not checked, and renders as a card once VillaNET is back. Its props are the forger's text, shown to unlinked readers under the card's eyebrow, beside the forger's own name as author. Linked readers see only server data (A§4.9 state 9), and the clip, if real, is still gated. Nothing in a post can tell such a card from a genuine one, so this is stated rather than built: the README's security posture says it (A§3.10), and A31 is checked while VillaNET runs.

### 4.5 Marking shared posts gone

- **Capture.** `settle(reasons)` (§2.9) collects the `shares` of the entries it removes. The collection is reset at the start of each `apply`, because apply can run up to 8 times, and is kept from the attempt that wrote.
- **Mark.** After the CAS succeeds and the files are removed, `go p.markSharesGone(items)` runs. The value is `"expired"` for reason `expired`, and `"deleted"` for `clipper`, `streamer` and `admin`.
- **For each post id:**
  1. `GetPost(id)`. Skip it on error: deleted posts are not returned.
  2. Skip it unless `post.Type == clipPostType` and `villanet_clip_id` equals the clip.
  3. Skip it if it is already marked: `villanet_clip_gone` set and, for `"deleted"`, none of the five scrubbed keys present.
  4. `pendingMarks.Store(id, {clipId, value})`. `pendingMarks` is a `sync.Map`; entries older than 10 minutes are pruned on insert, as a safety net only.
  5. Apply the mark to the fetched post exactly as Hook 2 step 1c does: set `villanet_clip_gone`; for `"deleted"`, delete `villanet_clip_title`, `villanet_clip_clipper`, `villanet_clip_clipper_id`, `villanet_clip_streamer` and `villanet_clip_streamer_id`. `villanet_clip_id`, `_duration_ms` and `_created_at` stay. Then `UpdatePost(post)`.
  6. `GetPost(id)` again. If the mark is in place, stop. Otherwise go back to step 5, at most 3 attempts in all. This covers an author's update on another cluster node, whose hook has no pending mark and could overwrite the mark (the store's `Update` has no version check [v9 S/store/sqlstore/post_store.go `Update`]).
  7. `pendingMarks.Delete(id)`.
- **Expiry keeps the title and names** in the props (owner rule 7: "Expired … clips say so on the card"). Deletion by the clipper, an admin or the streamer removes them, so a deleted clip's title is gone from every share post: the message never had it (A§4.3), and the props no longer do.
- **How Mattermost treats the update** [v9 S/app/post.go:628-707]:
  - The message is unchanged, so `EditAt` does not move and no "(edited)" mark appears (`:673-677`).
  - Props are replaced, because `safeUpdate` is false (`:679-684`).
  - Open clients get `post_edited` and re-render the card.
  - Archived channels refuse the update (`:667-669`).
  - The store keeps the pre-update copy as an edit-history row [v9 S/store/sqlstore/post_store.go `Update`, "mark the old post as deleted"]. Only the post's author can fetch edit history [v9 S/api4/post.go:567-570], and the webapp opens it only from an "(edited)" mark, which these posts never get.
- **Errors** are logged at Debug. The marker logs one Info line, "Shared clip posts marked", with `clip_id`, `posts` and `reason`.
- **Limits.**
  - Every share post is tracked: at most 10 per clip, and an 11th share is refused (A§4.2 steps 6b and 14).
  - **Archived channels** refuse the update, so a share in a channel archived later keeps its old props: an expired clip's card falls back to the client's expiry rule (A§4.9 state 2), and a deleted clip's title and names stay in that post's props. This is a known limit.
  - A clip that expires while the plugin is disabled is marked at the first sweep after it comes back.

> **As built (3.0.0), A§4.5.** The marker cannot mark a post on **Mattermost 11.x** in a direct or group message whose members no longer share a team while `RestrictDirectMessage` is "team": 11.x refuses every update there [v11.1.0 S/app/post.go:710], as every version does in an archived channel. Neither can be marked; such a post keeps its props (a deleted clip's title and names included), the card's own expiry rule still covers an expired clip, and a post left unmarked after the three attempts is logged at **Info**, "A shared clip post could not be marked", with `post_id`, `clip_id` and the mark, so an admin can see which posts still carry what they carried. The scrubbed keys include the tag's two props (A§4.14).

### 4.6 What the link opens, and why

| Option | Web and desktop | Signed out (email, a phone's browser) | Mobile app | Verdict |
|---|---|---|---|---|
| **`/plug/ceo.villa.villanet/clip/<id>`** via `registerCustomRoute` | In-app navigation: only `/plugins`, `/files` and `/help` open a new tab [v9 W/utils/url.tsx:175-199]. The desktop app handles `data-link` links in-app too [desktop src/app/views/webContentEvents.ts:212-213] | The route is a plain `Route`, not a `LoggedInRoute` [v9 W/components/root/root.tsx:701-712; master :458-468], so our component sends the visitor to `/login?redirect_to=…`, and login pushes that path back [v9 W/components/login/login.tsx:632-633] | Not a recognised deep link, so it opens in the phone's browser [mobile app/utils/deep_link/index.ts:341-347, :394; app/utils/url/index.ts:228-229], and continues as signed out | **Chosen.** No team in the URL, works for DMs, and needs no server page |
| `/<team>/ceo.villa.villanet/clip/<id>` via `registerNeedsTeamRoute` | In-app | `LoggedInRoute` → login → back [v9 W/components/logged_in/logged_in.tsx:119] | Browser, as above | Rejected. DMs and GMs have no team. For a reader who is not in the named team, `TeamController` **tries to join them to it** [v9 W/components/team_controller/team_controller.tsx:169-203]. Renamed teams break old links. |
| A plugin-served page, `/plugins/ceo.villa.villanet/…` | Opens a new browser tab on web [v9 W/utils/url.tsx:175-199] and a popup window on desktop [desktop src/app/views/webContentEvents.ts:226] | `/login?redirect_to=/plugins/…` ends at "team not found", because the SPA treats `plugins` as a team name [v9 W/components/login/login.tsx:632-633; W/components/root/root.tsx:714-718] | Browser, with a sign-in dead end | Rejected. It needs its own HTML, CSP and sign-in handling. |

**Limitations, stated honestly.** The Mattermost mobile app cannot run webapp plugins, so tapping the link on a phone opens Mattermost in the phone's browser. There the person signs in once, and VillaNET opens on the clip in Mattermost's mobile web layout.
- **The landing page.** On the first visit from a phone browser, Mattermost shows its landing page before any route renders [v9 W/components/root/root.tsx:319-331]. Choose **View in Browser**. **View in App** hands `mattermost://<host>/plug/ceo.villa.villanet/clip/<id>` to the app [v9 W/components/linking_landing_page/linking_landing_page.tsx:42-48, :179], which cannot open it: the path is not a deep link it knows, and its dots fail the server-URL pattern [mobile app/utils/deep_link/index.ts:256-273, :394], so the app shows "This link you are trying to open is invalid." [mobile app/routes/+native-intent.ts:32-41; app/utils/deep_link/index.ts:445-451]. The page appears once per browser (`setLandingPageSeen`). Admins can turn it off: on 10.2 and later (not 10.0 or 10.1) with Enable Desktop App Landing Page [v10.11 W/components/root/root.tsx:195-199]; on 9.x only by clearing the app download links (the `landing` check at root.tsx:319-329). The README's "Sharing a clip" carries the same sentence.
- **The gone state on mobile.** Only the web card shows that a clip has expired or been deleted. The mobile app, the Threads list, pushes and email show the fixed message (A§4.3), which never changes; the link then opens VillaNET's "This clip has gone" dialog after sign-in. The README says so.

### 4.7 `ClipLinkRoute` (`components/ClipLinkRoute.tsx`)

**Registration.** `if (typeof registry.registerCustomRoute === 'function') registry.registerCustomRoute('clip', ClipLinkRoute)`, in the main window only. This serves `/plug/ceo.villa.villanet/clip`; the route is not exact, so `/clip/<id>` matches too [v9 W/plugins/registry.ts:954-966; W/components/root/root.tsx:701-712]. Add the method to the `Registry` type (`index.tsx:42-88`).

**What it does, once, on mount:**
1. Parse the id from `window.location.pathname` with `/\/plug\/ceo\.villa\.villanet\/clip\/([0-9a-f]{16})\/?$/`. With no match: `replacePath('/')`, and stop.
2. **Signed out** (`currentUserId()` is `''`, `mattermost.ts:95`): `replacePath('/login?redirect_to=' + encodeURIComponent('/plug/ceo.villa.villanet/clip/' + id))`.
   - Webapp plugins load without a session [v9 S/api4/plugin.go:37, `APIHandler`], and routes render only after plugins have initialised [v9 W/components/root/root.tsx:546].
   - If the plugin is disabled, the path falls through to `/:team` with the team "plug", and Mattermost's own `LoggedInRoute` makes the same login redirect [v9 W/components/logged_in/logged_in.tsx:119].
3. **Signed in:** `refreshIdentity()` if `me` is null, then `openClipFromLink(id)` (in `clips.ts`). It sets the dialog to `{view: 'clip', id, direct: true}` (`{view: 'locked', clipId: id}` when unlinked), and sets `panelOnArrival`.
4. `replacePath(team.name ? '/' + team.name : '/')`, with `team = currentTeam()`. `currentTeam()` always returns an object, with `name` possibly `''` (`mattermost.ts:99-104`), so test the name.
   - `/<team>` redirects to the last channel viewed in that team [v9 W/components/channel_layout/center_channel/center_channel.tsx:123; index.ts:49-57].
   - `/` sends a signed-in user to their default team [v9 W/components/root/root_redirect/root_redirect.tsx].
5. `installClipArrival(store)` waits until `currentChannelId()` is not empty (at most 15 s), then calls `openPanel()` once.

**Navigation** uses `replacePath(path)`, the new export of `mattermost.ts` (A§2.2; built in Phase 0). `webappUtils()` itself stays private.

**While it waits** (a few milliseconds): `<div className='vntv vntv-cliplink' role='status'>Opening the clip in VillaNET…</div>`. The Pluggable already places it in grid area `center` [v9 W/components/root/root.tsx:707-709].

Someone in no team gets Mattermost's team picker and no dialog. That is acceptable.

### 4.8 Where the clip dialog lives (`ClipsRoot`)

| Window | Registration | Renders on |
|---|---|---|
| Main (`windowKind() === 'main'`, `away.ts:61`) | `registerGlobalComponent(ClipsRoot)` when it exists [v9 W/plugins/registry.ts:1086, marked INTERNAL], otherwise `registerRootComponent` | Global: every route in the app body, including `/plug/…` and other products [v9 W/components/root/root.tsx:721]. Root: the channel view only. |
| Popped-out windows | `registerRootComponent(ClipsRoot)` | `popout_controller` renders Root and not Global [master W/components/popout_controller.tsx] |
| System Console | none renders | AdminClips hosts its own inline player (A§2.2) |

- This mirrors the mini-player's registration (`index.tsx:209-218`).
- A module-level host counter makes sure only the first mounted host renders, which covers the lab mounting a host too.
- Dialog state is module-level, so a Root fallback shows a pending dialog when the channel view mounts.

**`watchSharedClip(id)`** opens the dialog in the window where it is called. In the main window it also calls `openPanel()`, the same opener the profile popover's Watch uses (`watching.ts:329-341`). In a popped-out window (11.3+), only the dialog opens. **UNVERIFIED:** what Mattermost does when the main window asks to show a plugin panel that is popped out; it is whatever the popover's Watch already does today.

### 4.9 The card (`components/ClipPostCard.tsx`)

**Registration.** `if (typeof registry.registerPostTypeComponent === 'function') registry.registerPostTypeComponent('custom_villanet_clip', ClipPostCard)`, in every window kind. Add it to the `Registry` type.

**What Mattermost does with it** [v9 W/plugins/registry.ts:290-295; W/components/post_view/post_message_view/post_message_view.tsx:117-128]:
- It receives exactly `{post, compactDisplay, isRHS, theme}`, and replaces the message body.
- It renders in the centre channel, the RHS thread, search results, Recent mentions, Pinned, Saved and permalink previews, and also in Edit history and its Restore dialog with older props [v9 W/components/post_edit_history/edited_post_item/edited_post_item.tsx:159-161]; those open only from an "(edited)" mark, which these posts never get (A§4.4).
  - **Permalink previews:** plugin post types return before the `ShowMore` wrapper that applies `maxHeight` [v9 post_message_view.tsx:117-128 vs :140-145; the preview passes 105 in v9, post_message_preview.tsx:202-205, and 200 on master, :212], so the card is never clipped. Keep it about 99 px tall so the preview stays compact.
  - The global Threads list shows the message instead.
  - **"Enable Post Formatting" off:** Mattermost shows `post.message` as plain text and never mounts the card [v9 post_message_view.tsx:113-115]. Nothing to build; the fixed message (A§4.3) reads well as plain text.
- `isRHS` is true in both the RHS and search, so the card cannot tell those two apart.
- **Clicks.** Mattermost through v10.11 wraps plugin post types in no error boundary (master does, in `PluggableErrorBoundary`). Inside a permalink preview, Mattermost's handler looks only at `e.target.tagName`: a click whose target is anything other than `A`, `IMG`, `BUTTON` or `I` follows the permalink [v9 W/components/post_view/post_attachment_container/post_attachment_container.tsx:48-69; master :68-88], and on v9 an `svg` target throws a TypeError there, because its `className` is an `SVGAnimatedString`. So **every control is a real `<button>`, and nothing inside a button, and no `svg` anywhere in the card, receives pointer events**: `.vntv-postclip button *, .vntv-postclip svg { pointer-events: none; }`. A click on the ▶ glyph, the placeholder clapper, the duration tag or the eyebrow icon then targets the button or a plain element. Lab check (A§4.12): clicking those inside a mock permalink-preview container logs no navigation.

**Structure.**
- `ClipCardBoundary`, a copy of `Quiet` (`MiniPlayer.tsx:118`), wraps the card. On a render error it shows the fallback.
- The fallback is `<p className='vntv-postclip-fallback'>{post.message}</p>`: plain text, no Markdown.

**State.** A pure, import-free `cardView()` in `clipLogic.ts` works it out; the first match wins:

| # | Condition | State | Picture | Text | Actions |
|---|---|---|---|---|---|
| 1 | `post.type !== 'custom_villanet_clip'`, or `villanet_clip_id` is not 16 hex, or a prop has the wrong type | `fallback` | — | The message as plain text | — |
| 2 | `props.villanet_clip_gone === 'expired'`, **or** no `villanet_clip_gone`, `snapshot.clips` known, and `created_at + retentionDays × 86,400,000 <= now` | `gone-expired` | Placeholder, dimmed | Title line from props; "This clip has expired." | — |
| 3 | `props.villanet_clip_gone === 'deleted'` | `gone-deleted` | Placeholder, dimmed | "This clip was deleted." and nothing else: no title, no names | — |
| 4 | `linked === undefined` | `unknown` | Placeholder, **no request** | From props | [▶ Watch]: `refreshIdentity()`, then acts as 5 (linked false) or calls `watchSharedClip(id)` (linked true); if `me` still cannot be read, shows "Couldn't check this clip just now." |
| 5 | `linked === false` | `unlinked` | Placeholder clapper and duration tag; **no `<img>` and no request** | From props | [▶ Watch] replaces the action row with `role='status'` "Link your account first to watch this clip." and [Link Your Account] (`askToLink()`), and focus moves to that button |
| 6 | Linked, fetch in flight | `loading` | Placeholder with a slow pulse | From props | [▶ Watch] |
| 7 | Linked, the cards route said `null` | `gone` | Placeholder, dimmed | "This clip has gone — it expired or somebody deleted it." and nothing else: no title, no names | — |
| 8 | Linked, fetch failed (network or 503) | `error` | Placeholder | From props, plus a muted "Couldn't check this clip just now." | [▶ Watch] (the dialog handles errors) |
| 9 | Linked, `ClipCard` in hand | `live` | `<img src={clipThumbURL(id, card.thumb)} alt='' loading='lazy'>`; with no `thumb`, or on `onError`, the streamer's `Backdrop` (A§2.2) | From the **`ClipCard`**, not the props | [▶ Watch]: `watchSharedClip(id)` |

The state 2 expiry rule lets every reader, unlinked ones included, see expiry with no request even when a post was never marked (an archived channel, or a marker that failed). `cardView()` takes `now` and `retentionDays` as inputs. During the 24 h grace after an admin lowers retention (§2.13), it may say "expired" up to a day early; that is accepted.

**The text block** (states 2, 4, 5, 6, 8 and 9; states 3 and 7 show only their sentence), top to bottom:
1. The eyebrow: a clapper icon and "VILLANET CLIP".
2. The title: the clip title, or, when it is empty, "{streamer}, 3 Oct 21:04" formatted locally from `created_at` (as §3.9).
3. The meta line: "Clipped by {clipper} · {streamer}'s stream · 0:45". When `snapshot.bestClipper.userId` is the clipper, `ClipperBadge` at size 16 sits after the clipper's name. Upvotes are not shown.

**Data** (`clips.ts`: `useSharedClip(id, linked)`):
- It fetches only when `linked === true`. Ids are queued for 50 ms and sent as **one** `fetchClipCards(ids)` request (`GET /clips/cards`, at most 25 ids per request; more are split). A module map of results plus an in-flight set means one id makes one request, however many cards show it.
- **No version trigger**: the card shows no upvotes, so a vote anywhere must not make every client refetch every mounted card. An entry is refetched only:
  - on mount, when it is older than 10 minutes;
  - on Watch;
  - when `snapshot.clips.count` or `snapshot.clips.newest` decreases (a clip went), after a random 0–3 s delay so clients do not all ask at once.
- A `gone` (`null`) result is never refetched.
- The module map is an LRU of about 200 entries.
- 403 `not_linked`: `refreshIdentity()`, and the entry is dropped.
- When `me === null` and `currentUserId() !== ''` (signed in after load, A§2.2), the first mounted card calls `refreshIdentity()` once.
- The card never fetches the list.
- A `post_edited` push re-renders the card with new props, so states 2 and 3 apply at once, for every reader.
- `uninitialize` clears the map.

**Accessibility.**
- The root has `role='group'` and `aria-label` "VillaNET clip: “that jump”, 45 seconds, clipped by Bob from Dave's stream". State 2 adds ", expired". States 3 and 7 name nothing: "VillaNET clip, deleted" and "VillaNET clip, gone".
- The picture is a `<button tabIndex={-1} aria-hidden='true'>` that does what Watch does. It shows no picture and does nothing in states 1–3 and 7.
- The visible Watch is a `<button>` reading "▶ Watch", with `aria-label` "Watch “that jump” in VillaNET" ("Watch Dave's clip in VillaNET" when there is no title).
- Focus is visible (A§4.11). Key handling is Mattermost's own: Space on a `BUTTON` is spared [v9 W/utils/post_utils.ts:225-263].

**Layout.**
- A row of a 16:9 picture and the text, wrapping onto two lines under about 340 px.
- The picture is 144×81, or 112×63 when `compactDisplay`.
- The card is at most 480 px wide and about 99 px tall, so a permalink preview, which does not clip it, stays compact.

### 4.10 The Share button (`components/ShareClipButton.tsx`)

**Where it appears:**
- the viewer's action row in ClipsModal, before Report and Delete;
- the editor's `published` phase: [Watch it] [Share in Town Square] [Done].

It does not appear in AdminClips (the console has no channel), and it is unreachable for unlinked people.

**Target:** `currentChannelId()` (new in `mattermost.ts`, reading `entities.channels.currentChannelId` [v9 W/packages/mattermost-redux/src/selectors/entities/common.ts:27-28]), read through a store subscription.
- In the global Threads view it is `''` [v9 W/actions/views/lhs.ts:37-39].
- In the RHS popout (master, 11.3+) it is the channel from `?channel=` at the moment of popping out, and does not follow the main window [master W/components/rhs_popout/rhs_popout.tsx:30, :36-38]. The label names it, so the person sees where the clip goes. The server re-checks permission either way.

**Label.** The rules live in a pure `shareLabel({kind, displayName, otherName, shared})` in `clipLogic.ts`, which `check-away.mjs` can load (A§2.2). `shareTargetOf(channelId)` in `mattermost.ts` reads the store and calls it.

| Channel | Label |
|---|---|
| A shared (connected-workspace) channel, `shared: true` in the store | `aria-disabled`, titled "Clips can't be shared into a shared channel" (the server refuses it too, A§4.2 step 7) |
| Open or private | "Share in {display_name}" |
| DM | "Share with {other person's display name}" (`profileOf` and `displayNameOf`, `mattermost.ts:110, :151`) |
| GM | "Share in this group message" |
| Not in the store | "Share in this channel" |
| `''` | `aria-disabled`, titled "Open a channel to share this clip" |

Names longer than 32 characters are cut with "…" (also in `shareLabel`), and the full name goes in `title`.

**Behaviour:**
1. Two clicks (the AdminStills CONFIRM pattern: 4000 ms, with a 500 ms minimum before the second click counts; `AdminStills.tsx:26-27, :200-213`). The first click changes the label to "Click again to post it in Town Square".
2. `shareClip(id, channelId)`, while the button reads "Sharing…" and is disabled.
3. On success: a `role='status'` line "Shared in Town Square.", and the button reads "Shared" and stays disabled until the target channel changes.
4. Errors: a `role='alert'` line with A§3.7's copy; `not_linked` calls `refreshIdentity()`.

**Client:** `export const shareClip = (id: string, channelId: string): Promise<{postId: string; channelId: string}>` in `client.ts`, through `requestClip`.

### 4.11 Styles

**The post card renders in Mattermost's theme, not in the dark panel.** Its root is `className='vntv vntv-postclip'`, so the `--vntv-*` variables and Oswald are available. One rule, `.vntv.vntv-postclip`, resets what `.vntv` sets (`styles.ts:45-63`): `display: flex; flex-wrap: wrap; height: auto; overflow: hidden; background; color`.

Colours use only Mattermost's theme variables, which are set for every theme [v9 W/utils/utils.tsx:478-533]:

| Part | Value |
|---|---|
| Card background | `rgba(var(--center-channel-color-rgb), 0.04)` |
| Border | `1px solid rgba(var(--center-channel-color-rgb), 0.16)`, radius 8px |
| Text | `var(--center-channel-color)` |
| Meta and eyebrow | `rgba(var(--center-channel-color-rgb), 0.75)`: about 5:1 on Denim white, and higher on Onyx |
| Watch | `background: var(--button-bg); color: var(--button-color)`; on hover, `rgba(var(--button-bg-rgb), 0.88)` |
| Focus | `box-shadow: 0 0 0 2px var(--center-channel-bg), 0 0 0 4px var(--button-bg)` |
| Picture area | `#000` in every theme, with the placeholder clapper in white at 40% |
| Duration tag | White on `rgba(0, 0, 0, 0.72)` |
| VillaNET red | Only on the eyebrow's clapper icon, never on text |

**New classes,** each with a rule in `styles.ts`:
- Card: `vntv-postclip`, `-pic`, `-thumb`, `-placeholder`, `-dur`, `-body`, `-eyebrow`, `-title`, `-meta`, `-actions`, `-watch`, `-note`, `-link`, `-gone`, `-fallback`, plus `[data-compact="true"]` and `[data-state="loading"]`.
- Share: `vntv-share`, `vntv-share-status`.
- Link route: `vntv-cliplink`.

The loading pulse stops under `prefers-reduced-motion`, with its rule **inside** the existing last block.

### 4.12 Lab

- **Theme.** A "Centre theme" control, Denim · Onyx, sets these on the sample area:
  - Denim: `--center-channel-bg #ffffff`, `--center-channel-color #3f4350`, `--button-bg #1c58d9`, `--button-color #ffffff`, `--link-color #386fe5`, `--error-text #d24b4e`.
  - Onyx: `#090a0b`, `#dddfe4`, `#386fe5`, `#ffffff`, `#5d89ea`, `#da6c6e`.
  - Each `-rgb` variant too.
  - These values are copied from [v9 W/packages/mattermost-redux/src/constants/preferences.ts:83-105, :195-217]. This follows the existing light-popover toggle (`lab/shell.html:538-546`).
- **Messages.** `LabMessage` (`lab/index.tsx:1041-1051`) renders `ClipPostCard` when `post.type === 'custom_villanet_clip'`, passing `{post, compactDisplay, isRHS: false, theme}`.
- **A "Shared clips" block** under "Elsewhere in Mattermost" seeds one post per state:
  - live with a thumbnail, and live without;
  - `villanet_clip_gone` expired, and deleted;
  - a gone clip (its id is not in the world, so the cards route says `null`);
  - a post never marked whose `created_at` is past retention (the state 2 expiry rule);
  - a forged ordinary post with `props.type` set;
  - a post whose props have wrong types;
  - "Crash the card": a post object whose `props` getter throws, to show the boundary.
  - One card inside a mock permalink-preview container whose click handler copies Mattermost's tag test (A§4.9 Clicks) and logs "navigate"; clicking the ▶ glyph, the placeholder, the duration tag and the eyebrow icon must log nothing.
  - Toggles: `compactDisplay`, and "narrow (RHS, 360px)".
- **Linked** row: yes · no · unknown · signed in after load. It drives the card's states 4, 5 and 9; "signed in after load" starts with `me` null and a signed-out store, then sets `currentUserId`, and the cards must reach state 9 without the panel being opened (A§2.2).
- **Clip fetch** row: ok · slow · fails (503) · network · gone (410). It drives `fetchClipCards` for the cards, `fetchClip` for the Trophies best-clip line and the viewer, so states 6, 7 and 8 and the hidden Trophies line can all be seen.
- **Share** row: ok · cannot post · share cap · rate-limited · gone · gone on arrival (the step 14 withdrawal) · network · no current channel · shared channel · DM label · GM label.
  - The mock `shareClip` appends a post to the lab store's `currentChannelId`, with the type, props and message built by a TypeScript copy of A§4.3's template.
  - It honours the 60 s repeat rule.
- **"Open a clip link"** renders `ClipLinkRoute` in the lab centre, with a fake `browserHistory` that logs `replace()`. It must end with the dialog open on that clip and the lab panel opened. A "signed out" variant shows the login URL it would go to.
- **Other new rows** (the §3.16 rows stand):
  - "Admin: no · linked · not linked" (both admin variants get the full AdminClips; the not-linked one still sees a locked panel);
  - "Streamer: not linked · username-attributed · allowed · off" (replaces "Streamer controls").

### 4.13 Tests

**Go** (`clips_share_test.go`):
- `TestShareNeedsLinkAndChannelPermission`: a table over A§4.2 steps 2–10b, including a shared channel (`Shared: true`), a DM and a GM with no common team while `RestrictDirectMessage` is "team", and the same with "any".
  - Each refusal makes no `CreatePost` call and spends no token.
  - On success, `CreatePost` is called once with `UserId` = the caller, `Type` = `custom_villanet_clip`, exactly the props keys in A§4.3 and a nonce present.
- `TestShareMessageCarriesNoUserText`: with the title ``"@channel **x** http://e.x `y` \n z"`` and names containing `@here`, the message is exactly line 1 plus the link line; the title and names appear only in props; there is no link line when SiteURL is empty.
- `TestShareIsIdempotentWithinAMinute`.
- `TestShareRateLimitedAndRefundedOnFailure`.
- `TestSharesAreCappedAtTen`: the 11th share answers 409 `share_cap` with no `CreatePost` and no token; a race past step 6b ends in `DeletePost`, a refund and 409.
- `TestShareIsWithdrawnWhenItCannotBeTracked`: the entry settled meanwhile gives `DeletePost`, a refund and 410; a CAS error gives `DeletePost`, a refund and 503.
- `TestForgedClipPostsAreDowngraded`:
  - no nonce: the post is returned with `Type` `""` and no `villanet_*` props or `props.type`, never rejected;
  - a valid nonce passes, is stripped, and cannot be reused;
  - an expired nonce downgrades;
  - `props.type` on an ordinary post is stripped;
  - any other post returns `nil, ""` with no API call.
- `TestClipPostPropsCannotBeEdited`:
  - a message edit is refused (`nil` and the reason); a pin (message unchanged) passes;
  - every protected key is restored, and `attachments`, `override_username` or `from_webhook` added by the author are removed;
  - a pending mark is applied whoever triggers the update: an author's patch that lands between `pendingMarks.Store` and the marker's `UpdatePost` carries the mark, and the marker's own update then changes nothing;
  - a patch carrying `villanet_clip_gone` on a clip with no pending mark is reverted;
  - apart from a message edit, the hook never returns nil.
- `TestSettledClipsMarkTheirSharedPostsGone`:
  - expiry writes `expired` and keeps the title and names; clipper, streamer and admin deletion write `deleted` and remove the five scrubbed keys;
  - the message is unchanged;
  - a mark lost to a concurrent update is retried (at most 3 attempts), and `pendingMarks` is empty afterwards;
  - posts of another type or another clip id are untouched;
  - a `GetPost` error is skipped.

**Lab** (every state reachable, checked by eye in both themes):
- every card state 1–9, plus compact and narrow, and the permalink-preview click check;
- every Share label and result;
- the link route, signed in (linked and unlinked) and signed out;
- "signed in after load", and the ClipsModal lock when Linked flips to no.

**`build/check-away.mjs`** gains `clipLogic.ts`, which imports nothing. It asserts `cardView()` (the state 2 expiry rule included), the ✂ state table (§3.4 plus A§3.8, with the single unlinked row), the trimmer clamps (§3.7) and `shareLabel()`'s rules, including the 32-character cut, on plain inputs.

### 4.14 The stream's tag on a clip (owner request, 2026-10-02)

Victor asked: "When a user clips a video and the stream is tagged with a game/etc, have that show up in the clip details when sharing as well as a note/title that the clipper chooses." Asked which words, he chose **just the title**: the clipper's own words stay the editor's title ("What happened?", 80 characters, may be empty). There is no separate note, and no message added at share time.

**What the tag is.** `apiStream.NowPlaying` (`nowplaying.go:82`, `plugin.go:199`): the streamer's "What's on?" pick, or else the movie, show or game of their plan that is on now. It is `{category, title, media?}`, where `media` is a `scheduleMedia` (`media.go:283`). A stream with no tag gives a clip with no tag.

**Capture.** The tag is taken at the same moment as the stream's other captured details (§2.10.1, the draft's "streamer info captured at the time"):
- `live`: from the current snapshot entry for the handle, at `clickAt`.
- `tail`: from the tail's captured details. Phase 4 adds `nowPlaying` to what a tail keeps (§2.4.8 "A tail keeps").
- It is cleaned like the title: `redactStreamKey(t, draft.streamKey, "the stream")`, then `cleanText(t, maxMediaTitle)`.
- Stored **compact**, because the `clips` document has a 1 MiB cap (§2.8) and 500 clips: `{"category","title","media"?: {"ref","kind","title","year"?,"link"?,"source"}}`. `overview` and `facts` are dropped. `media` passes `checked()` on the way in and again on the way out; when it fails, the tag keeps its category and title and loses `media`. (When the IGDB work lands, `checked()` learns its third source; nothing here changes.)

**Where it goes.**

| Place | Field | Notes |
|---|---|---|
| Draft (server-only and the `Draft` JSON) | `tag?` | Shown in the editor |
| Clip entry in the `clips` document | `tag` (omitempty) | Set at publish from the draft; never edited afterwards |
| `ClipView`, `ClipCard`, AdminClips rows | `tag?` | Same compact shape |
| Share post props (A§4.3) | `villanet_clip_tag` (string, the tag title), `villanet_clip_tag_kind` (`movie`, `tv` or `game`; from `media.kind`, else `movie` for the category `movie` and `game` for `game`) | Omitted with no tag. **Never in the message** (Q-F). Protected by Hook 2 like every `villanet_*` key. **Scrubbed on deletion** with the title and names (A§4.5 step 5 and Hook 2 step 1c gain these two keys); kept on expiry |

**How it reads in the webapp.**
- **Editor context line** (§3.6 item 4): "From Dave's stream · Hytale · 21:02:17 – 21:04:17".
- **Clip grid card** (§3.9 item 4): a tag chip under the title, styled like the Live now card's What's on? tag. The card's `aria-label` gains it: "Play that jump — Hytale, clipped by Bob from Dave's stream, 45 seconds, 3 days ago, 14 upvotes".
- **Viewer** (§3.9): a line under the title, "Hytale · Game · 2026". When `media` is present it is a button that opens the existing media profile (`MediaProfile`), as `NowPlaying` does.
- **Shared post card** (A§4.9): the chip after the title line. Linked readers get it from the `ClipCard`, unlinked readers from the props. It is also part of the card's `aria-label`, and it is never shown in states 3 and 7.
- **AdminClips**: in the row's meta line.
- No tag: nothing is shown and nothing is left empty.

**Tests.** `TestDraftCapturesTheStreamsTag` (live, and tail once Phase 4 lands), `TestClipTagIsCompactAndChecked` (no overview or facts; a bad `media` is dropped and the title kept), `TestShareCarriesTheTagButNotInTheMessage`, `TestDeletedClipScrubsTheTag`. Lab: sample clips tagged with a game (Hytale), a movie and nothing; a tagged draft in the editor; tagged post cards for linked and unlinked readers.

**Phases.** Server Phase 2 captures and stores it, adds it to the views and adds the two share props and their scrub to `clips_share.go` (Phase 3's file). Server Phase 4 adds it to tails. Webapp Phase 2 shows it in the editor, the grid cards and the viewer; webapp Phase 3 shows it on the post card.

---

## 5. Build plan

### 5.1 Rules

- **The project is not a git repository.** `server/` is one Go package (`package main`), so **never run two agents in `server/` at once**: every file compiles together, and nothing can merge two sets of edits.
- **Webapp: one agent at a time too.** `styles.ts`, `clips.ts`, `index.tsx` and `lab/index.tsx` are shared by every phase.
- **One server agent and one webapp agent may run in parallel**, against the JSON in §2.10–§2.12 as amended by A§3–A§4, and the types in §3.1 as amended.
- **File ownership.**
  - Server agent: `server/**`, `plugin.json`, `go.mod`, `go.sum`, `Makefile`, `build/*.sh`.
  - Webapp agent: `webapp/**`, `artwork/**`, `build/check-away.mjs`.
  - `README.md`, `LOCKDOWN.md` and `design/STATUS.md` are edited only in Phase 6.
- **Before each phase**, take a copy of the tree for diffs (as `Broadcast Box Plugin.baseline-2.11.0` was), leaving out `webapp/node_modules` and `dist`.
- **Checks, per agent.** `make check` runs both halves (go vet and test, then tsc, the style, version and away checks), so one agent's half-finished edits would fail the other's gate. In Phase 0 the server agent, who owns the `Makefile`, adds:
  - `check-server`: `go vet`, `go test -race ./server/...`, the gofmt test and `build/check-version.py` (it reads `plugin.json` and `server/plugin.go`, both server-owned);
  - `check-webapp`: `build/check-react.py`, `tsc --noEmit`, `build/check-styles.py` and `node build/check-away.mjs`;
  - and `check` becomes `check-server check-webapp`, unchanged in effect.
  Until those targets exist, the webapp agent runs the same four commands by hand.
- **Each phase ends** with the agent's own target passing (`TZ=UTC make check-server`, or `make check-webapp`), after `export PATH="$HOME/.local/opt/go/bin:$HOME/.local/opt/node/bin:$PATH"`, and, for the webapp, with the lab opened and looked at (A§2.3). The full `TZ=UTC make check` runs at **join points**: after both agents have finished Phases 1, 3 and 5. Each agent's pre-phase baseline copy covers only the files it owns.
- **Builders stop after Phase 6 (docs) and hand over the bundle.** Deploying to 192.168.0.101 and testing against Broadcast Box on 192.168.0.106 (Phase 7) happen only after Victor says go, and with him present. Every release so far was tested in the lab only (`design/STATUS.md`).
- **Every build prompt carries the owner rules (A§1) verbatim.**

### 5.2 Phases

| Phase | Server (one agent) | Webapp (one agent, in parallel) |
|---|---|---|
| **0. Contract** | `configuration.go` (fields, `clipLengthSeconds()`, `ringWindow()`, clamps in `normalize`). `defaults.go` (a "Clips" group). `plugin.json` (the Clips section after `airtime`, with a `ClipsAdmin` custom setting; version 3.0.0). `plugin.go:21` `manifestVersion = "3.0.0"`. `ratelimit.go` `refund`. `clips.go` (constants, document and view types, the error table including `not_linked`, `cannot_post` and `share_cap`). `clips_access.go` (`requireClipsLink`, `streamTag`). `clips_api.go` (`serveClips`/`serveAdminClips` with every route of A§3.3, the cards route and every admin route included, stubbed after the gate as 503 `storage_unavailable`; admin routes after `isSystemAdmin` only, with no link gate (Q-A answered yes); `writeClipError`). The `api.go` prefix dispatch. A `snapshot.clips` stub. The `Makefile` targets `e2e`, `check-server` and `check-webapp` (A§5.1). Tests: settings, `TestEveryClipRouteRefusesUnlinked` and `TestAdminRoutesNeedAnAdminNotALink`. | `types.ts` (§3.1 plus A§3.7, A§3.10 and A§4: `ClipCard`, `bestClip?: {id}`, the new error codes). `client.ts` (`requestClip`, `ClipRequestError`, every §3.2 export plus `shareClip`, `fetchClipCards`, the admin URLs and calls). `lab/mocks/client.ts` (every export; `surface.ts` passes). `lab/world.ts` fields. The `.mp4` wiring (`lab/webpack.config.js`, `lab/types.d.ts`). `mattermost.ts` (`currentChannelId`, `shareTargetOf`, `replacePath`). `watching.ts` `openPanel`. `identity.ts` (`requestLinkForm`, `takeLinkRequest`, `useLinkRequest`, `refreshIdentity`). The `Registry` type additions. `clipLogic.ts` and `check-away.mjs`. |
| **1. Clips exist, linked-only** | `clips.go`: document, `clipsCAS`, `rev` cache, scoring with linked-now eligibility, settle (capturing `shares`), views, `republishClips`. `clips_files.go`. `clips_api.go`: list, get, cards, video, thumb, vote, report, delete (A§3.3 order), the admin list, the new admin video, thumb and delete routes, and the existing admin writes; user routes behind the gate, admin routes behind `isSystemAdmin` only (Q-A answered yes). The admin wipe waits for Phase 4, with the recorders and drafts it wipes. `clips_sweep.go` (cluster job, retention). `clips_access.go`: link cache, `streamerOf`, eligibility cache with the unknown-verdict rule (A§3.4). `plugin.go`: fields, `startClips`/`stopClips` placed per A§2.1, snapshot fields. `poller.go`: `addClips` before each `publish`. `api.go`: `me` fields. `api.go` and `links.go`: `clipsLinkChanged`. A test-only helper that publishes the fixture as a clip. The A§3.11 tests that do not need drafts. | `icons.tsx`. `badges.tsx` `ClipperBadge`, plus `artwork/badges/clip_best.svg` and `.png`. `clips.ts`. `ClipsRoot` hosted per A§4.8. `ClipsModal` (list, viewer, and the locked view whenever `linked === false`). `SidebarTitle` (the clapper group). `Sidebar` (the Clips row in the glide, `clipsAlert`, the link-request consumer, the new tooltip, following identity changes). `index.tsx`: the store subscription that refreshes identity on sign-in (A§2.2). `LeaderboardModal` (the Clippers tab). `AwardBadges`, `AwardList`. `AdminClips` with its inline player, full for every system admin, linked or not (Q-A answered yes). `index.tsx` registrations and `uninitialize`. `styles.ts`. Lab rows. |
| **2. Drafts** | §5 Phase 2 as written, plus: the gate on the draft routes (A§3.4), `streamTag` captured on drafts and entries, the title order from A§2.1, and the opt-out read by stream tag. | §5 Phase 2 as written, plus: the unlinked ✂ call to action, the C key via the tile's `tabIndex={-1}` (A§2.2), the locked editor, the corrected sound handoff (`takeSound`), the published copy fix, and a placeholder slot for Share. |
| **3. Sharing** | `clips_share.go`: the route (A§4.2, with the share cap and the withdrawal on step 14), message and props (A§4.3), nonce, both hooks (A§4.4: downgrade, restore by content, `pendingMarks`), the 60 s repeat map, the share limiter. Settle's `markSharesGone` with the scrub and the verify-and-retry (A§4.5). A§4.13 tests. | `ClipPostCard.tsx`, `ClipLinkRoute.tsx`, `ShareClipButton.tsx`. `clips.ts` (`useSharedClip`, `watchSharedClip`, `openClipFromLink`, `installClipArrival`, `askToLink`). Share in the viewer and the editor. `registerPostTypeComponent` and `registerCustomRoute` in `index.tsx`. Card styles. Lab (A§4.12). |
| **4. Recording** | §5 Phase 3 as written, with the A§2.1 placements (`noteSessions`/`reconcile` after `:181`, `withoutOwn` at `:282`, the `noteViewer` block after `api.go:703`), the opt-out by `streamTag`, the streamer endpoints on the current link, and the admin wipe. `make e2e` tests. | §5 Phase 3 as written, plus the username-attributed notice and the per-stream opt-out copy. |
| **5. Finish** | `TZ=UTC make check-server`, `make e2e`, `make dist-linux` | `make check-webapp`, `make lab`; a visual pass at 320 px, 400 px, popped out and phone width, with the post card in Denim and in Onyx. Then the join point: `TZ=UTC make check` |
| **6. Docs** | README and LOCKDOWN (§4, as amended by A§3.10): the plugin now posts, edits and may withdraw posts on people's behalf (Share), and both hooks check every post and edit, while it runs. Update `design/STATUS.md` ("tested in the lab only"). Then **stop and hand over** the bundle. | — |
| **7. The real host (only after Victor says go, with him present)** | Deploy to 192.168.0.101 and run §6 plus A§5.3 against Broadcast Box on 192.168.0.106. | Chromium (desktop app), Firefox, and the Mattermost mobile app for the shared-post message and link. Safari if available (expect no sound). |

Phase 3 depends only on Phase 1's clips and Phase 2's editor, so it can come before the recorder: pion and the end-to-end tests are the long pole. Webapp phase N may run against mocks before server phase N lands; the join points (A§5.1) are where the two halves meet.

### 5.3 New acceptance criteria

These are added to §6. Each is checked on the real deployment (Phase 7) unless marked.

- **A25.** As an unlinked user, every `/api/v1/clips*` route answers 403 `not_linked`, the clip files, the cards route and the user's own clips and drafts included; curl with that user's cookie to check. The panel shows the Link Your Account call to action wherever clips would be, and DevTools shows no request to `/clips*` at all.
- **A26.** Unlinking and relinking behave as A§3.9:
  - the holder changes within one sweep;
  - drafts come back on relink within 2 h;
  - the opt-out stays on the stream.
- **A27.** In System Console → Plugins → VillaNET, a system admin with or without a link can list, watch, delete, block and wipe (Q-A answered yes). A non-admin gets 403 `forbidden` on every `/api/v1/admin/clips*` route (curl). The same unlinked admin's panel still shows the Link Your Account call to action and makes no `/clips*` request.
- **A28.** Share posts one card, authored by the sharer, in the current channel within 1 s:
  - a double click, or a retry within 60 s, posts once; an 11th share of one clip is refused with the `share_cap` copy;
  - Share is disabled in a shared channel;
  - on the mobile app, the post reads as A§4.3's fixed message, with no title or names, and its link opens VillaNET on the clip after a browser sign-in. On a phone browser's first visit, the landing page's **View in Browser** leads to the clip, and **View in App** shows the app's "This link you are trying to open is invalid." (A§4.6). Record whether a link preview appears (A§4.3 expects none).
- **A29.** An unlinked reader's card shows title, clipper, streamer and length, makes **no** request to `/clips*` (DevTools), and its Watch shows "Link your account first to watch this clip." with Link Your Account. A linked reader's channel of several cards makes one `GET /clips/cards` request, and a vote elsewhere makes none.
- **A30.** When a shared clip is deleted, every open card says "This clip was deleted." at once, and neither the card, nor its `aria-label`, nor the post's props (API `GET /api/v4/posts/{id}`), nor the message carries the title or names any more. An expired clip says "This clip has expired." within 10 minutes. On the web card, neither shows "(edited)".
- **A31.** While VillaNET is running, a `custom_villanet_clip` post made through `POST /api/v4/posts` arrives as an ordinary message with no card. Patching a shared post's props changes nothing the card shows, and editing its message is refused. Moving a thread whose root is a shared clip (where the server offers Move Thread) succeeds, and the copy is an ordinary message.
- **A32.** Watch on a card opens VillaNET and the clip in the main window. In a popped-out channel or thread window (11.3+), it opens the clip in that window. The card is readable in Denim and in Onyx (and in the lab).
- **A33.** Sign out, then sign in with a password (an in-app sign-in, no page reload): a linked user's card shows state 9 with its thumbnail without the panel being opened, and the Best Clipper badge and Clips row appear. Also checked in the lab ("signed in after load").

---

## 6. Questions for the owner

**Answered on 2026-10-02 (A§1): Q-A yes (full moderation for every system admin in System Console), Q-B no, Q-C yes, Q-D yes, Q-E yes, Q-F no.** The text below is kept as the record of the options.

Each question has a default, and the default is what the build implements. **Every default complies with the owner rules (A§1)**; an alternative that relaxes a rule is built only if Victor explicitly says yes.

- **Q-A. May a system admin who has not linked a stream use the clip moderation in System Console?**
  - Default: **no.** Owner rule 1 covers "every other clip route", so every `/api/v1/admin/clips*` route runs the gate after `isSystemAdmin`, and an unlinked admin's AdminClips shows "Link your account to review clips" and the call to action. Switching Clips off in the plugin settings still stops all recording without a link.
  - Alternative 1: the moderation writes that show no clip content (delete by id, block, unblock, reset score, clear reports, remove picture, wipe a stream's recording) work without a link; the list, video and thumbnail stay gated.
  - Alternative 2: an unlinked admin may also list and watch clips in the console.
- **Q-B. May someone who has unlinked still delete their own clips and drafts?**
  - Default: **no.** They relink first, or ask a linked admin. There is no list of their own clips while unlinked, and no route skips the gate.
  - Alternative: a DELETE-only take-back, with no list: `DELETE /clips/clip` by its clipper and `DELETE /clips/draft` by its owner skip the gate. If chosen, it must be built narrowly:
    - the bypass is decided by a cache lookup that never triggers the 60 s re-read (§2.8), so the `clips` key is not read before the gate;
    - an unlinked non-clipper gets byte-identical 403 `not_linked` answers for existing, expired, unknown and malformed ids; an unlinked non-owner of a draft gets 404 `draft_gone`, as in the spec's `TestDraftsArePrivate`;
    - an expired entry is never settled by the take-back (A§3.3 settle rule), so its votes bank;
    - `TestEveryClipRouteRefusesUnlinked` excludes exactly these two cases, a new `TestTakeBackBypassIsNarrow` covers them, Phase 0 stubs them before the gate, and A25 names them.
- **Q-C. Do votes count only while the voter is linked, and does a holder who unlinks lose Best Clipper until they relink?**
  - Default: **yes to both**, as Top Snoopers counts only linked people. The scores of live clips are kept.
  - Consequence: a vote on a clip that expires, or is deleted by its streamer, while that voter is unlinked is not banked, and does not come back on relink. This trims owner rule 5's "expiry keeps a clip's votes" to the votes that count at that moment.
  - Alternative: a vote counts once cast (and every such vote banks).
- **Q-D. Does a shared clip appear as a post by the person who pressed Share?**
  - Default: **yes**.
  - Alternative: post it as the VillaNET bot, "shared by Bob". That is less personal, and the bot would first have to join each channel.
- **Q-E. May clips be shared while Clips is switched off?**
  - Default: **yes.** Published clips stay watchable and votable until they expire, so sharing them stays possible too.
- **Q-F. Should a shared post's plain-text message (what the mobile app, the Threads list, pushes, email and search show) carry the clip's title and names?**
  - Default: **no.** The message is "🎬 VillaNET clip · 0:45" and the link; the title and names live only in the card's props, which are cleared when the clip is deleted (A§4.3, A§4.5). Owner rule 7 asks only for "a plain-text link in the post's message for mobile".
  - Alternative: include them (inside a code span, so they cannot notify). Deletion must then rewrite the message, which sets "(edited)" on the post, and A30's "no (edited)" would apply to the web card only.

---

## Review log

Three adversarial reviews (lenses: security-linked, mattermost-facts, buildability) raised 39 issues on 2026-10-02. Each was checked against the 2.11.0 tree or Mattermost source (mattermost@v9.0.0, @v10.11.0, @master 60328d1, mattermost-mobile@main e2b167b) before the decision.

| # | Lens | Severity | Problem | Decision | Reason |
|---|---|---|---|---|---|
| 1 | security-linked | major | Q-A/Q-B defaults let an unlinked admin list and watch clips, and an unlinked user list their own clips (rule 1 breach) | Accepted | Defaults are now rule-compliant: every user and admin clip route runs the gate, `mine=1` and YOUR CLIPS are gone, and relaxations are only Q-A/Q-B alternatives. This goes further than proposed: content-free admin writes are gated too, since rule 1 says "every other clip route" |
| 2 | security-linked | major | A deleted clip's title and names stay in every share post's message and props; shares past 10 are never marked; the aria-label keeps the title | Accepted | Verified `UpdatePost` sets `EditAt` on a message change (v9 post.go:673). The message is now fixed text with no user strings; the marker removes the title, names and user ids on deletion; an 11th share is refused; states 3 and 7 name nothing |
| 3 | security-linked | major | Every vote changes `clips.version`, so every client refetches every mounted card, unbatched, each fetch costing a KV read and RPCs | Accepted | Added batched `GET /clips/cards` (at most 25 ids, link cache, no `isSystemAdmin`, no viewer flags); dropped the version trigger; refetch on mount after 10 min, on Watch, or when a clip goes; LRU of 200 |
| 4 | security-linked | minor | `ownUpdates` can be consumed by an author's concurrent patch; authors can add `attachments` and override props; the message link can be swapped | Accepted | Hook 2 restores protected keys by content, applies a non-consumed pending mark whoever triggers the update, and refuses message edits (pins still pass, v9 api4/post.go:1013-1016) |
| 5 | security-linked | minor | Shares into shared (connected-workspace) channels send clip data to another server | Accepted | Verified `Channel.IsShared` (v9 channel.go:370) and that no version refuses shared channels (master refuses only DMs and GMs). Step 7 refuses them, and Share is disabled there |
| 6 | security-linked | minor | `streamTag` equals the public stream handle for lower-case keys | Accepted | Verified handles.go:56-67 and poller.go:276, :285. `streamTag` is now a domain-separated HMAC in its own helper, and the tests use lower-case keys |
| 7 | security-linked | minor | Title redaction through `withoutOwnKey` fails open when a KV read fails | Accepted | Verified links.go:60-64, :80-87. The token is read strictly (503 `store` on error), the key comes from the gate, and a new test covers it |
| 8 | security-linked | minor | The take-back bypass contradicts the gate test and Phase 0, and the `mine` parsing is split between two places | Accepted | There is no bypass by default (Q-B), so the gate test holds with no exceptions. Q-B's alternative spells out the narrow rules and `TestTakeBackBypassIsNarrow` |
| 9 | security-linked | minor | The forgery defence works only while the plugin runs | Accepted (part) | The limitation is now stated in A§4.4, the README and A31. The `post=<id>` "foreign" check is rejected: it helps only linked readers, who already see server data, never the unlinked readers who see forged props |
| 10 | mattermost-facts | major | `me` stays null after an in-app password sign-in, so cards stay unknown | Accepted | Verified: plugins load on the login page (client.go:288), login uses `history.push` (login.tsx:632-633), and plugins are not reloaded (plugins/index.js:124-128). A store subscription now refreshes identity and the snapshot; added a lab row and A33 |
| 11 | mattermost-facts | minor | In a permalink preview, a click on a button's child element follows the permalink, and an svg target throws on v9 | Accepted | Verified post_attachment_container.tsx:48-69. Button descendants and every svg in the card get `pointer-events: none`, with a lab check |
| 12 | mattermost-facts | minor | The landing-page question is answerable now: View in App is a dead end | Accepted | Verified root.tsx:319-331, linking_landing_page.tsx:42-48 and :179, and the mobile parser (the dots fail the server pattern, giving the invalid-link alert). UNVERIFIED replaced; A28 checks both buttons |
| 13 | mattermost-facts | minor | Link previews: the server refuses its own LAN address, plugin post types never show embeds, and the `attachments` fallback is harmful | Accepted | Verified post_metadata.go:663, httpservice.go:55-100 (192.168.0.0/16 reserved) and message_with_additional_content.tsx:24-35. Paragraph replaced, fallback deleted |
| 14 | mattermost-facts | minor | Rejecting a nonce-less clip post breaks Move Thread and Shared Channels sync | Accepted | Verified master post.go:3492-3515 and guarded_hooks.go:139-145. Hook 1 now downgrades the post to an ordinary message; A31 and the test are updated |
| 15 | mattermost-facts | minor | The one-use `ownUpdates` token can be taken by the wrong update, or expire during a slow update | Accepted | Merged with #4: a non-consumed pending mark, plus verify-and-retry for a lost update from another node. The proposed cache-only rule is not used, because another node's cache can be up to 60 s stale |
| 16 | mattermost-facts | minor | With `RestrictDirectMessage` set to "team", plugin `CreatePost` skips master's restricted-DM check | Accepted | Verified master post.go:59-66 and channel.go:4555-4570; the check is absent from v9.11 to v10.11. Step 10b mirrors it on every version, with tests |
| 17 | mattermost-facts | minor | Mobile, Threads, push and email never show that a shared clip has gone | Accepted | Stated in A§4.6 and the README (the documentation option). Rewriting the message would add "(edited)", so it is only the Q-F alternative |
| 18 | mattermost-facts | minor | Render facts are wrong or missing: permalink `maxHeight`, post formatting turned off, edit history | Accepted | Verified post_message_view.tsx:113-145 and the preview heights 105 (v9) and 200 (master). Bullets rewritten; edit history is noted as unreachable, since `EditAt` never moves |
| 19 | buildability | blocker | The Q-A/Q-B defaults break owner rule 1 (same as #1) | Accepted | Same fix as #1: rule-compliant defaults everywhere, and Victor decides any relaxation (A§6 preamble) |
| 20 | buildability | major | `streamTag` equals the handle, so the snapshot test would fail (same as #6) | Accepted | Same fix as #6. The test uses a lower-case key with a live stream and a tail |
| 21 | buildability | major | Unlinked readers' cards miss the gone state: shares past 10, a step-14 CAS failure, archived channels; step 14's value is unspecified | Accepted | An 11th share is refused; a share that cannot be tracked is withdrawn (`DeletePost`, refund, 410/409/503); a client-side expiry rule uses `created_at` and `retentionDays`; archived channels are listed as a limit |
| 22 | buildability | major | The clipper's fast path voids the votes of an expired clip not yet swept | Accepted | DELETE now runs gate, then expiry (200; the sweep banks it), then the reasons. Settle banks any entry past expiry whatever reason was asked; new test |
| 23 | buildability | major | Fail-closed eligibility can move the title for good, or bank too few votes | Accepted | Unknown verdicts keep `doc.holder` and put off banking settles (the sweep retries; a streamer delete gets 503). Two tests added |
| 24 | buildability | major | Two parallel agents cannot each pass the full `make check` | Accepted | Verified the Makefile. `check-server` and `check-webapp` are added in Phase 0, and the full check runs at join points after Phases 1, 3 and 5 |
| 25 | buildability | minor | Server and webapp types disagree: `bestClip`, `MyClipRow`, the Trophies ▲ | Accepted | `bestClip?: {id}` and `ClipCard` are typed in A§3.10; the Trophies title and ▲ come from the `ClipView`. `MyClipRow` is gone with Q-B |
| 26 | buildability | minor | The gate test conflicts with the take-back answers, and a malformed id gets 400 before the gate | Accepted | No take-back by default; the gate runs before id validation, so a malformed id gets 403; the test lists every route. Q-B's alternative states its unlinked answers |
| 27 | buildability | minor | `check-away` cannot load `shareTargetOf` from `mattermost.ts` | Accepted | Verified check-away.mjs:21-29 and mattermost.ts:3-5. A pure `shareLabel()` goes in `clipLogic.ts`, and `shareTargetOf` calls it |
| 28 | buildability | minor | `refreshIdentity()` never reaches the panel's local `me` | Accepted | Verified Sidebar.tsx:62, :192 and :271. The Sidebar now adopts a changed identity; lab check added |
| 29 | buildability | minor | An open ClipsModal does not lock when the link goes | Accepted | It shows the locked view whenever `linked === false`, unloads the video and drops the cache; lab check added |
| 30 | buildability | minor | State 4's Watch points at the wrong states | Accepted | It now acts as state 5, or calls `watchSharedClip`, with an error line when `me` cannot be read |
| 31 | buildability | minor | A deleted clip's title stays in the message and props (same as #2) | Accepted | Same fix as #2. The one remaining choice, a title in the message, is Q-F for Victor |
| 32 | buildability | minor | The lab cannot reach card state 8, the hidden Trophies line, or a share that is gone on arrival | Accepted | Verified that the lab world has only `latencyMs` and `failWrites`. Added the "Clip fetch" row and a "gone on arrival" share |
| 33 | buildability | minor | It is not said whether the unlinked ✂ or the disabled states take priority | Accepted | For an unlinked user, every §3.4 row is replaced by one enabled call-to-action row; `undefined` renders no ✂ |
| 34 | buildability | minor | The admin wipe is in Phase 1, before drafts and recorders exist | Accepted | It is now built in Phase 4 only |
| 35 | buildability | minor | The AdminClips inline player lacks the no-download attributes | Accepted | The attributes and the no-download rule are now stated (A§2.2) |
| 36 | buildability | minor | `stopAll` could wipe relative paths when no root is resolved | Accepted | Directory operations return at once without an absolute root; new test |
| 37 | buildability | minor | There is no exported navigation helper, and the team test is always true | Accepted | Verified mattermost.ts:99-104, :187 and :200. `replacePath` is exported in Phase 0, and the code tests `team.name` |
| 38 | buildability | minor | Q-C does not disclose what it does to banked votes | Accepted | The consequence is added to Q-C, A§3.4 and A§3.9; the default is unchanged, for Victor to confirm |
| 39 | buildability | minor | Builders would deploy to the real host without Victor's go | Accepted | Verified STATUS.md ("tested in the lab only"). Builders stop after the Phase 6 docs; Phase 7 happens only with Victor present |

**Totals:** 39 accepted (#9 in part), 0 rejected outright. The rejected parts are #9's foreign-post check and #15's cache-only rule.
