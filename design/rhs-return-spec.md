# VillaNET 2.11.0 design spec: bring the panel back after a reply, and keep the stream playing while it is away

Status: revision 2, ready to build. Target: plugin **2.11.0**. `min_server_version` stays 9.0.0. The owner runs v11 (Entry Edition).

Conventions:
- Mattermost paths are relative to `webapp/channels/src`, unless they start with `server/` or `webapp/platform/`.
- "v11" means tag `v11.0.1`. Upstream has no `v11.0.0` tag (the curl returned 404).
- Project paths are relative to `/home/user/Documents/Broadcast Box Plugin`.
- Project line numbers were read on 2026-10-01. Other work (2.10.0, Top Watchers) is editing the same files at the same time. If a cited line has moved, find it by the symbol named next to it.
- Every behaviour claim cites the line it rests on. Mattermost and Broadcast Box sources were curled into the scratchpad listed in Appendix B.

---

## 0. Decisions in one screen

1. **Auto-return.** VillaNET comes back by itself only if something took its place in the side panel. If the person closed VillaNET themselves, it stays closed.
   - **A thread replaced VillaNET directly.** VillaNET returns 1.5 s after the server confirms a reply sent from this tab, unless the person is still using the thread. It also returns, in the same frame, when the thread is closed (X or Ctrl+.).
   - **Another of Mattermost's own panels replaced it** (search, mentions, saved, pinned, files, channel info, members, edit history, post card), or the person moved from a thread into one of those. VillaNET returns when that panel is closed, never after a reply.
   - **Another plugin's panel replaced it.** Nothing returns.
   - **The person is still in the thread.** Any of these stops the pending return; the next reply or a close still brings VillaNET back:
     - text, an attachment or an open edit in the reply box;
     - typing, clicking, scrolling or pressing keys in the side panel during the 1.5 s;
     - a menu, emoji picker or dialog that has focus when the 1.5 s run out.
   - **"Back to thread" after an automatic return.** That thread then brings VillaNET back on close only.
   - **Phone layout (`mobileView`).** Nothing is armed and there is no mini-player.
2. **Focus.** An automatic return never puts focus in the message box.
   - After a reply-return, focus lands on the VillaNET panel itself, so one Tab reaches **Back to thread**.
   - After a close-return, focus goes where Mattermost would have put it.
3. **Playback survives.** Each watched stream's WebRTC session and its single `<video>` element live in a module-level pool (`sessions.ts`). Components lend a slot `<div>`. Going back into the panel does not reconnect.
4. **Mini-player.** A fixed overlay in a corner of `#post-list`, mounted with `registerGlobalComponent`, main window only.
   - It shows while VillaNET is displaced **and** something else is in the side panel.
   - It shows the stream that was in front when VillaNET was displaced (the *latched* stream). Muting it does not change the picture.
   - It never opens a new connection, except to pick up again the stream whose "Waiting for…" plate it was showing.
   - Only the latched stream is held while the mini-player is hidden: 4 min hold, then 60 s grace. Other watched streams get the 60 s grace only.
5. **The detour ends.**
   - If the side panel closes without a return (setting "never", another plugin's panel closed, phone layout), the mini-player goes and sessions end within 60 s.
   - A team or product switch turns the TV off at once, exactly as it does while VillaNET is showing.
6. **A deliberate close still turns the TV off.** That means VillaNET's icon, Ctrl+. while VillaNET is showing, or a team or product switch. Every session stops at once and the watch list is kept, as today.
7. **Opt-out.** Two radio settings under Settings › Plugin preferences › VillaNET (9.11+). Both default to on.
8. **Popouts.**
   - Channel popouts (11.8+) have their own side panel. There, auto-return works and a deliberate close stops sessions at once, but there is no mini-player.
   - The other popouts are unchanged.
9. **Not in 2.11.0:** picture-in-picture, the lift/dock glide animation, an admin-wide default for the settings, and riding Broadcast Box's publisher-reconnect (§16).

---

## 1. Mattermost facts this rests on

### 1.1 Why a reply removes VillaNET

**How a thread opens:**
- These all call `selectPostFromRightHandSideSearch`, which dispatches `SELECT_POST` with `previousRhsState = getRhsState(state)` (post_component.tsx v11:380-389; actions/views/rhs.ts v11:45-53, 119-121):
  - the reply arrow;
  - the dot-menu Reply;
  - "N replies";
  - `R` in the dot menu.
- These dispatch `SELECT_POST` with no previous state (post_component.tsx v11:339-352; permalink_view/actions.ts v11:185-189):
  - clicking a post body ("Click to open threads", on by default);
  - permalinks or notifications to a reply when CRT is on.
- Shift+Up on an empty centre box ends in the same `SELECT_POST` (use_key_handler.tsx v11:342-346).

**What `SELECT_POST {postId: root}` does to the store** (reducers/views/rhs.ts v11):
- `selectedPostId` becomes `root` (:20-23).
- `rhsState` becomes `null` (:169-176).
- `pluggableId` becomes `''` (:244-253), so Mattermost forgets *which* plugin was showing.
- `isSidebarOpen` becomes `Boolean(postId)` (:316-321).
- `isSidebarExpanded` is kept (:348-349).
- `rhsSuppressed` becomes false (rhs_suppressed.ts v11:24-29).

**Why VillaNET unmounts:**
- `sidebar_right.tsx` v11 renders a thread *instead of* `RhsPlugin` (:282-296) and renders nothing when closed (:273-275).
- So VillaNET's `Sidebar` unmounts, and `Player`'s cleanup calls `player.stop()` (Player.tsx:150-162).

**There is no way back for a plugin:**
- The Back arrow in the thread header renders only for search, mention, flag and pin (rhs_header_post.tsx v11:46-59, 89-115).
- `goBack` dispatches `RHS_GO_BACK {state: prevState || 'channel-info'}` (actions/views/rhs.ts v11:108-113). The reducer sets `rhsState` (:172-173) but leaves `pluggableId` at `''` (:244-253), so going back to `'plugin'` would render an empty plugin panel.

**CRT is not the cause and is not forced in v11:**
- The admin setting still has Disabled / default_on / default_off / always_on (server/public/model/config.go v11:92-95). `always_on` is only the unset default (:916-917). The setting is in System Console › Site Configuration › Posts › Threaded Discussions (admin_definition.tsx v11:2919-2978).
- Neither the reply handler (post_component.tsx v11:382-389) nor the render (sidebar_right.tsx v11:282-296) checks CRT.

### 1.2 State paths and actions used

The paths below are the same on 9.0.0, 9.11.0, 10.5.0, 10.11.0, 11.0.1, 11.11.0 and master. The reducer keys are at v9.0.0 :372-390 and v11 :426-448. `views` includes `rhsSuppressed` (reducers/views/index.ts v9.0.0:39, v11:38).

| Read | Path | Values / source |
|---|---|---|
| Panel kind | `state.views.rhs.rhsState` | `'plugin'`, `'search'`, `'mention'`, `'flag'`, `'pin'`, `'channel-files'`, `'channel-info'`, `'channel-members'`, `'edit-history'`, or `null` (constants v9.0.0:1018-1028, v11:1049-1059, master:1014-1024) |
| Which plugin | `state.views.rhs.pluggableId` | registration id, or `''` |
| Thread root | `state.views.rhs.selectedPostId` | id or `''`. Cleared by `POST_REMOVED` of the root (rhs.ts v11:26-30) |
| Post card | `state.views.rhs.selectedPostCardId` | id or `''` (rhs.ts v11:85-96) |
| Open | `state.views.rhs.isSidebarOpen` | Also set false by `TOGGLE_RHS_MENU`, `OPEN_RHS_MENU`, `TOGGLE_LHS` and `OPEN_LHS` (rhs.ts v11:324-331), which are phone-menu actions |
| Expanded | `state.views.rhs.isSidebarExpanded` | `SET_RHS_EXPANDED` sets it (:342-343). `UPDATE_RHS_STATE null` sets false (:346-347). `SELECT_POST` keeps it only with a postId (:348-349). `SUPPRESS_RHS` sets false (:354-355) |
| Threads page | `state.views.rhsSuppressed` | The RHS is hidden while true (selectors/rhs.ts v11:228-230; global_threads.tsx v11:63-80) |
| Phone layout | `state.views.browser.windowSize === 'mobileView'` | selectors/views/browser.ts v11:23-26 |
| Me | `state.entities.users.currentUserId` | |
| Team | `state.entities.teams.currentTeamId` | |
| Channel | `state.entities.channels.currentChannelId` | |
| Posts | `state.entities.posts.posts`, `state.entities.posts.postsInThread[root]` | postsInThread reducer at redux reducers/entities/posts.ts v11:942-985 |
| Settings | `state.entities.preferences.myPreferences['pp_ceo.villa.villanet--<name>'].value` | Key is `` `pp_${pluginId}`.slice(0, 32) `` (utils/plugins/preferences.tsx v9.11.0:4-6, v11:4-6). `pp_ceo.villa.villanet` is 21 characters, so it is not truncated |

**Fallback:** if `state.views.rhs` is missing, or `rhsState` is neither a string nor null, the controller stays inert. The result is today's behaviour: no auto-return, no mini-player, and sessions end by grace (§4.6).

| Dispatch | Shape | Source |
|---|---|---|
| Show VillaNET | `sidebar.showRHSPlugin` = `{type:'UPDATE_RHS_STATE', state:'plugin', pluggableId}` | actions/views/rhs.ts v9.0.0:211-217, v11:267-273; registry v11:949-969 |
| Restore expanded | `{type:'SET_RHS_EXPANDED', expanded:true}` | setRhsExpanded v9.0.0:473, v11:524. Already used by mattermost.ts:240 |
| Back to thread | `{type:'SELECT_POST', postId, channelId, timestamp: Date.now()}` | selectPost v9.0.0:496-503, v11:537-547. The optional v11 `previousRhsState` is omitted |

`ActionTypes` is `keyMirror`, so each type string is the name itself (constants v9.0.0:187, v11:191).

**Effects of `showRHSPlugin` while a thread is open:**
- `selectedPostId` becomes `''` (:31-32).
- `selectedChannelId` becomes `''` (:105-122). Mattermost therefore sees new panel content and moves focus (§1.5).
- `isSidebarOpen` becomes true (:318-319). Expansion is kept (:346-347).
- `rhsSuppressed` is cleared (rhs_suppressed.ts v11:19-23). **Never dispatch while `rhsSuppressed` is true**, or the RHS appears over the Threads page.

**Closing:**
- `closeRightHandSide` batches `UPDATE_RHS_STATE null` with `SELECT_POST ''` (rhs.ts v11:492-510). A batch notifies subscribers once.
- `toggleRHSPlugin` and `hideRHSPlugin` compare `pluggableId` (rhs.ts v11:298-322). So VillaNET's icon while a thread is open already *shows* VillaNET.

**Mattermost's own side effects** (sidebar_right.tsx v11):
- It collapses expansion on a channel change (:213-221).
- It closes the RHS on a team or product change (:223-229).
- It collapses on a click outside (§1.6).
- It moves and restores focus (§1.5).

### 1.3 Detecting "my reply landed"

- `submitPost` builds `pending_post_id: ${userId}:${time}` (actions/views/create_comment.tsx v11:52-62).
- `createPost` dispatches a placeholder `RECEIVED_NEW_POST {...newPost, id: pendingPostId}` (redux actions/posts.ts v11:232-243).
- On success, `receivedPost(created)` follows (:249-280).
- On failure, the placeholder is re-received with `failed: true`. It is removed instead if the root was deleted, town square is read-only, or a hook dismissed it (:284-302). A retry reuses the same pending id (:189).
- The reducer deletes `posts[pending_post_id]` when the real post arrives (reducers/entities/posts.ts v11:369-372). `postsInThread[root]` swaps the placeholder id for the real one (:942-985).
- Scheduled replies take a separate path with no placeholder (create_comment.tsx v11:92-111).
- Replies from other tabs or devices never pass through this tab as placeholders. So "a real post whose `pending_post_id` I saw as my own placeholder" means *this tab's* reply landed.

### 1.4 Mount points, popouts, render order

**`registerGlobalComponent`:**
- It renders at the app root beside the RHS (root.tsx v9.0.0:721-722, v11:495-497, master:476-479).
- It survives product switches. It is marked "INTERNAL: Subject to change without notice" (registry v9.0.0:1082-1086, v11:1127-1133).
- It never renders in popouts, because the `/_popout` route is matched first (root.tsx master:405-408).

**`registerRootComponent`:**
- It exists from 9.0 (registry v9.0.0:94, v11:128).
- It renders inside the channels view (channel_controller.tsx v11:87), and in every popout from 11.3 (popout_controller.tsx v11.3.0:43, v11.11.0:53).

**`registerUserSettings`:**
- It exists from 9.11 (registry v9.11.0:1236, v11:1264) and not in 9.0.0.
- A `radio` setting needs `name`, a non-empty `default` and `options` (types/plugins/user_settings.ts v11:65-107; utils/plugins/plugin_setting_extraction.tsx v9.11.0:6-61, 156-236).

**Popout routes:**

| Popout | Route | Source |
|---|---|---|
| Thread | `/_popout/thread/:team/:postId` | popout_controller.tsx v11.3.0:46 |
| RHS on 11.3 | `/_popout/rhs/:team/:identifier` | :50 |
| RHS from 11.6 | `/_popout/rhs/:team` | v11.6.0:55 |
| Help | `/_popout/help/:page?` | v11.8.0:68 |
| Channel, from 11.8 | `/_popout/channel/:team/(channels\|messages)/:identifier` | v11.8.0:60, v11.11.0:60 |

- **The channel popout renders Mattermost's own `SidebarRight`** (channel_popout.tsx v11.11.0:22, 81). VillaNET sits there in an ordinary side panel, and Reply replaces it exactly as in the main window (VillaNET's own note at webapp/src/popout.ts:20-23).
- The RHS popout (`.rhs-popout`) renders the plugin by id (rhs_plugin_popout.tsx v11.11.0:23-58). Popping out does not close the main window's panel (plugins/rhs_plugin/rhs_plugin.tsx v11.11.0:33-48).
- Mattermost's own popout check is `href.startsWith(${Client4.getUrl()}/_popout/)` (utils/popouts/popout_windows.ts master:196-198).

**React and render timing:**
- React versions:
  - 9.0–11.0.x: React 17.0.2 with react-redux 7.2.4 (package.json v11.0.1:61, 70);
  - 11.1–11.11: React 18.2.0 with react-redux 9.2.0 (v11.11.0:76, 85);
  - master: React 19.2.8 (:77).
- Mattermost exposes `window.React`, `window.Redux` and `window.ReactRedux` (plugins/export.ts v11:96-100).
- Root modes:
  - legacy `ReactDOM.render` up to 11.0 (entry.tsx v11.0.1:55-56);
  - on 11.1–11.11, legacy unless `window.enableConcurrentReact` is set (entry.tsx v11.11.0:57-70);
  - on master, always `createRoot` (entry.tsx master:34-36).
- Plugins initialize after the app has mounted (`initializePlugins`, plugins/index.ts v11:87-107, called from root.tsx v11:107). So Mattermost's store subscription comes before any plugin listener.
  - On a legacy root, Mattermost's components re-render inside the dispatch, before a plugin's `store.subscribe` listener runs.
  - On a concurrent root, they re-render later.
  - **Nothing in this design may depend on which of those happens.**
- `uninitialize()` is called with no arguments, after `removeWebappPlugin` (plugins/index.ts v11:66, 208-212).

**F6 regions** (`a11y__region` with `data-a11y-sort-order`):
- post list 1 (channel_view.tsx v11:39-46);
- message box 2 (advanced_text_editor.tsx v11:773);
- RHS thread 3 (virtualized_thread_viewer.tsx v11:433-435);
- search results 3 (search_results.tsx v11:392-398);
- left sidebar 6;
- channel header 8 (channel_header.tsx v11:342-343).

In a browser the region shortcut is Ctrl/Cmd+F6. Plain F6 works only in the desktop app (utils/a11y_controller.ts v11:801-805).

**DOM landmarks:**
- `#post-list`:
  - The deferred placeholder (channel_view.tsx v11:37-48) and PostView's loading and real elements (post_view.tsx v11:84, 92) are **different nodes with the same id**. The node is replaced as a channel renders.
- `#app-content.app__content` (channel_view.tsx v11:183-185).
- The textbox ids (AdvancedTextEditorTextboxIds, utils/constants.tsx v11:792-798):
  - `#post_textbox`: the centre box, which creates root posts in the current channel;
  - `#reply_textbox`;
  - `#edit_textbox`.
- `.file-preview__container` (file_preview.tsx v9.0.0:138, v11:141, master:237).
- `#rhsCloseButton` (rhs_header_post.tsx v11:218-224).
- `#rhsContainer`: it begins with `SearchResultsHeader`, which holds VillaNET's title (plugins/rhs_plugin/rhs_plugin.tsx v11:16-31). VillaNET's title has the Top Streamers `<button>` (webapp/src/components/SidebarTitle.tsx:39-47).

### 1.5 How Mattermost moves focus in the side panel (10.5+)

`handleRHSFocus` (sidebar_right.tsx v11:142-189) runs on every update.

**When the panel opens or its content changes** (:146-158), and `rhsChannel` changing counts:
- It saves `previousActiveElement = document.activeElement` (:159).
- It schedules `setTimeout(0)`, which calls `focusElement(firstFocusable(#rhsContainer))` (:161-175).
- `focusElement` waits *another* `setTimeout(0)` before dispatching the a11y FOCUS event (utils/a11y_utils.ts v11:46-61).
- The controller then focuses the target. After keyboard input it uses `nextElement` with the focus ring; otherwise it calls `target.focus()` (utils/a11y_controller.ts v11:911-924).

**When the panel closes** (:176-188):
- With an origin element, `restoreOriginFocus` runs synchronously (a11y_controller.ts v11:395-409).
- Otherwise a `setTimeout(0)` restores `previousActiveElement`. That value will already have been overwritten if the panel reopens in the same task.

**Consequences:**
- A single plugin `setTimeout(0)` cannot run after Mattermost's second hop.
- An immediate reopen after a close ends with focus in VillaNET's header.
- `handleRHSFocus` exists on 10.5.0, 10.11.0, 11.0.1 and master, but not on 9.11.0 (grep count 2/2/2/2 vs 0).

### 1.6 Collapse on click outside

`sidebar_right.tsx` v11 adds a document `mousedown` listener (:191-194). When the RHS is open and expanded, a press inside `#root` but outside the RHS, the global header and the App Bar dispatches `setRhsExpanded(false)` (:234-245).

So a **mouse** Reply on a centre post collapses VillaNET *in its own dispatch, before* the click's `SELECT_POST`. Keyboard routes (R, Shift+Up) keep the expansion.

### 1.7 Popups that hold focus

- Mattermost's menus are `MuiPopover`s with class `a11y__popup`, and they focus their first item (components/menu/menu.tsx v11:255-294). The a11y controller finds popups by that class (a11y_controller.ts v11:700-703).
- The emoji picker is `role='dialog'` (emoji_picker/emoji_picker_tabs.tsx v11:38-41, 110-112).
- GenericModal sets `aria-modal='true'` and the class `a11y__modal` (webapp/platform/components/src/generic_modal/generic_modal.tsx v11:278-286).

### 1.8 VillaNET today (the code being changed)

**WhepPlayer** (whep.ts):
- `play(handle, video, onFailure, peekLease)` (:18) writes `video.srcObject` on every `ontrack` (:30-35).
- Failure is reported on `connectionState === 'failed'` (:42-49).
- There is a generation guard (:62-66).
- `stop()` stops the receiver tracks and closes the connection (:71-91).

**Player** (Player.tsx):
- `UNMUTE_VOLUME` :28; resume backoff :34-38; stall timeout 12 s :43;
- negotiation effect :73-166, which listens for `loadeddata` and `resize` (:130-131) and unmutes after pictures arrive (:123-128);
- audio layout effect :184-197; closing :199-204;
- `handlePause` :206-233, which calls a bare `video.play()` (:229);
- `takeAudio` :238-254; `applyVolume` :256-281;
- `<video>` :320-326; Mute calls `onAudio(null)` (:346); volume title :356;
- Retry :383-393; `WaitingTile` :415-435.

**Hud** (Hud.tsx):
- `Hud` is at :62. It renders the power button **before** its children's bar (:230-241). The power button is absolutely centred (styles.ts:1254-1270), so DOM order does not affect how it looks.
- `TileInfo` :266 can contain a ticker toggle and links. `HudButton` :474.

**Watch list** (watching.ts):
- It is module-level and survives remounts (:35-40). `MAX_WATCHING = 4` (:22). `MISSING_GRACE_MS = 45000` (:29). Observable pattern :46-59.
- `setAudio` :72-80.
- `reconcile` (:150-198):
  - releases the sound as soon as the audible stream is missing (:163-166);
  - **does not notify** when a stream first goes missing (:175-180, 190-192).
- It is called only from Sidebar (Sidebar.tsx:223-225).
- `resetWatching` (:224-230) clears listeners too. It is never called today.

**Panel** (Sidebar.tsx):
- `REFRESH_MS = 30000` :37; `MAX_REFRESH_MS` :43.
- Boot sweep state :71-76.
- `captureStillsWhileOpen` effect :84.
- Body class `vntv-rhs-open` :94-98, which hides Mattermost's close button (styles.ts:5208-5210).
- **The 30 s poll lives only in Sidebar** (:159-198, `tick` at :164).
- Root `.vntv.vntv-panel` :762-768; `.vntv-body` :771.
- Player or WaitingTile is chosen from the snapshot (`stream ?`) at :869-885.
- `NoSignalTile powerOn={booting}` :903-906.

**Server push:** the server pushes the snapshot only when it changes (server/poller.go:602-642, return at the `!changed` check).

**Stills** (stills.ts):
- `STALE_SNAPSHOT_MS = 75000` (:57-60). The tick returns when `snapshotAge() > STALE_SNAPSHOT_MS` (:150).
- `playing` map :64-78; counted `captureStillsWhileOpen` :104-126.
- `mayPeek` caps at `MAX_WATCHING` (:209-223); `moving()` :247-264; peek :304-365.

**Store** (store.ts): `snapshotAge` :22-24; `setSnapshot` :26-30; `subscribe` :32-38; `useSnapshot` :40-46.

**Registration** (index.tsx):
- open on load :164; registration :172; toggle :174; opener :178; closer :182;
- WebSocket :240-251; `uninitialize` :255-268.

**Cost of a reconnect:**
- one `/api/v1/whep` request against `newRateLimiter(60, time.Minute)` (server/plugin.go:466; server/api.go:266-275);
- a new Broadcast Box session, which asks the streamer for keyframes until one arrives.

**Chromium probe** (loopback VP8, indicative only):
- decoding continues with no element attached;
- reattaching to the live `MediaStream` gives a first frame in about 49 ms with no keyframe request;
- removing and re-inserting an element in the same task keeps it playing;
- removing it for longer leaves it paused until `play()`.

**HTML standard:**
- On removal the UA awaits a stable state; if the element is in a document again by then, nothing happens; otherwise the internal pause steps run (scratch media/html_media.html:2760-2772).
- Those steps queue the `pause` event as a separate task, and they clear the "can autoplay" flag (:2540-2560).

**Version:** `plugin.json:8` and `server/plugin.go:21` already say 2.10.0, which is the Top Watchers release. This feature is 2.11.0.

---

## 2. Behaviour rules (what people see)

| # | Situation | Result |
|---|---|---|
| R1 | VillaNET showing → a thread opens by any route → a reply from this tab is confirmed | 1.5 s later VillaNET is back, provided all of these hold: the reply box is empty; nothing is being edited; the person has not touched, scrolled or typed in the side panel during the 1.5 s; and no menu, picker or dialog has focus. The stream keeps playing and nothing re-tunes. Focus goes to the VillaNET panel (§3.7). A **Back to thread** strip is at the top of the panel (§7). |
| R2 | The detour is closed: thread X, Ctrl+., or X on search/pinned/saved/files/info/members | VillaNET returns in the same frame. Focus goes where Mattermost would have put it (§3.7). No strip. |
| R3 | Typing, clicking, scrolling or pressing keys in the side panel during the 1.5 s | The pending return is cancelled. It stays armed for the next reply (R1) or a close (R2). |
| R4/R5 | Text or attachments left in the reply box, an edit box open, focus on something in the thread other than the empty reply box, or focus in a menu, picker or dialog when the 1.5 s run out | No return after that reply. The next reply or a close can still return. |
| R6 | The reply fails | No return, and the failure stays visible. A retry that succeeds counts as landed (R1). |
| R7 | Another thread or panel opens during the 1.5 s | The pending return is cancelled. Thread to thread stays armed for replies; anything else leaves only return-on-close. |
| R8 | Emoji reaction, follow thread, a post from the centre box, a scheduled reply, a slash command | Not a trigger. None of them creates a reply placeholder (§1.3). |
| R9 | VillaNET brought back by hand (icon, Watch in the profile popover, Back on the mini-player) | It docks and everything is disarmed. Mattermost places focus as it does for any opening. |
| R10 | Another plugin's panel takes the slot | Disarmed for the rest of the detour. The mini-player stays **while that panel is showing**. When it closes, the mini-player goes and sessions end within 60 s. |
| R11 | VillaNET was closed before the thread opened | Nothing happens. It never reopens a panel the person closed (README "Opening the panel by itself"). |
| R12 | The Threads page (`rhsSuppressed`) | Nothing is dispatched and the mini-player hides. The latched stream is held up to 4 min + 60 s, the others for 60 s. When the person leaves the page, Mattermost restores VillaNET itself, because its `rhsState` is unchanged. |
| R13 | Phone-sized layout | No arming and no mini-player. Sessions get the 60 s grace only. |
| R14 | Team or product switch during a detour | The TV goes off: every session stops at once and the mini-player goes. This is the same as a team switch while VillaNET is showing (§0.6). The watch list is kept. |
| R15 | Popouts | In channel popouts (11.8+), R1–R14 apply without the mini-player: displaced sessions get the 60 s grace, and a deliberate close stops them at once. In RHS, thread and help popouts nothing changes. |
| R16 | Multiple tabs | Each tab is independent. Only the tab that sent the reply returns. |
| R17 | Thread closed with "Never, I'll open VillaNET myself" | No return. The mini-player goes and sessions end within 60 s. |
| R18 | **Hide** on the mini-player | The mini-player goes for the rest of this detour and the watch list is kept. Sessions end within 60 s unless VillaNET comes back first. Auto-return is unaffected. |
| R19 | **Back to thread** after an R1 return | The thread reopens. Its next reply does not close it; closing it brings VillaNET back. |
| R20 | *(Added 2026-10-05, `design/lock-screen.md` §4.9.)* The reader is **locked** (neither linked nor let in to watch) | No return after a reply or a closed panel, and no mini-player: nothing of theirs is there to bring back, and the lock would only cover the thread they were using, with no Back to thread to undo it. `awayLogic.arm` sets neither `replyRoot` nor `returnOnEnd`, and `latchCandidate` gives nothing, unless the access is linked or let in. For people who can watch, nothing changes. |

---

## 3. Detection and return state machine

### 3.1 Modules

- `webapp/src/awayLogic.ts` is **pure** and imports nothing. It holds `panelOf`, `step`, `arm` and `shouldReturnAfterLanded`, and is unit-tested by `build/check-away.mjs`.
- `webapp/src/away.ts` holds everything else: the store subscription, timers, DOM guards, dispatches, holds, latching, the focus guard, the observable `useAway()`, and `cameBack()`.

### 3.2 Classification

```ts
export type RhsSlice = {rhsState: string | null; pluggableId: string; selectedPostId: string;
    selectedPostCardId?: string; isSidebarOpen: boolean; isSidebarExpanded: boolean};
export type Panel = 'villanet' | 'closed' | 'thread' | 'own' | 'plugin' | 'empty';

export function panelOf(r: RhsSlice, ours: string): Panel {
    if (!r.isSidebarOpen) return 'closed';
    if (r.rhsState === 'plugin') {
        if (r.pluggableId === ours) return 'villanet';
        return r.pluggableId === '' ? 'empty' : 'plugin';        // '' = goBack to a forgotten plugin
    }
    if (r.rhsState === null) {
        if (r.selectedPostId) return 'thread';
        return r.selectedPostCardId ? 'own' : 'empty';            // empty = root removed (rhs.ts v11:26-30)
    }
    return 'own';   // search, mention, flag, pin, files, info, members, edit-history (has selectedPostId but is not a thread)
}

export type World = {rhs: RhsSlice; suppressed: boolean; mobile: boolean;
    teamId: string; path: string; channelId: string};
```

- `path` is the first URL segment after `window.basename` (see mattermost.ts:202-206).
- `channelId` is `state.entities.channels.currentChannelId`.

### 3.3 State

```ts
export type Away =
  | {phase: 'idle'}
  | {phase: 'docked'}
  | {phase: 'away';
     cause: 'own' | 'plugin' | 'suppressed';
     returnOnEnd: boolean;        // return when the detour is closed or emptied
     replyRoot: string | null;    // return after a landed reply in this root
     noReplyFor: string | null;   // reopened by "Back to thread": close-return only
     teamId: string; path: string; channelId: string;
     expanded: boolean;           // VillaNET's expansion as the detour began (§3.4)
     latched: string | null;      // the stream the mini-player shows (§6.2)
     hidden: boolean};            // Hide pressed on the mini-player
export type Settings = {returnAfter: 'reply' | 'close' | 'never'; mini: 'on' | 'off'};
export type Ctx = {ours: string; settings: Settings; now: number;
    hint: {root: string; until: number} | null;   // set by backToThread() (§3.8)
    latch: string | null};                        // away.ts's latch candidate (§6.2), computed before step()
```

- The initial state on install is `villanet ∧ ¬suppressed` → `docked`; anything else → `idle`.
- Settings are re-read at fire time:
  - `returnAfter === 'never'` cancels every return;
  - `'close'` cancels reply-returns;
  - `mini === 'off'` means no holds and no mini-player.

### 3.4 Transitions

`step(prev, before, after, ctx)` returns `{next, effects, hintUsed}`. Notation:
- `P = panelOf(after.rhs, ctx.ours)`, `S = after.suppressed`, `M = after.mobile`;
- `A` is `prev` when it is away;
- `root = after.rhs.selectedPostId`.

**Rows are evaluated top to bottom; the first match wins.**

`arm(keepLatched?)`:
```
hinted = P === 'thread' && ctx.hint && ctx.hint.root === root && ctx.now < ctx.hint.until
{phase:'away', cause:'own',
 returnOnEnd: ctx.settings.returnAfter !== 'never',
 replyRoot:   P === 'thread' && ctx.settings.returnAfter === 'reply' && !hinted ? root : null,
 noReplyFor:  hinted ? root : null,
 teamId, path, channelId: from `before`,
 expanded:    before.rhs.isSidebarExpanded,      // see note below
 latched:     keepLatched ?? ctx.latch,
 hidden:      false}
```

**Note on `expanded`:** a mouse Reply has already been collapsed by Mattermost's click-outside rule (§1.6), so `before` reads false. That is accepted as the person's own action; only keyboard-opened detours restore expansion.

| # | From | Condition | To | Effects |
|---|---|---|---|---|
| 1 | idle | P=villanet, ¬S | docked | — |
| 2 | idle | otherwise | idle | — |
| 3 | docked | P=villanet, ¬S | docked | — |
| 4 | docked | P=villanet, S | away{cause suppressed, returnOnEnd false, replyRoot null, noReplyFor null, latched ctx.latch, …} | hold |
| 5 | docked | P=closed | idle | stopAll (icon, Ctrl+., team or product switch, phone menu) |
| 6 | docked | M (P is thread, own or plugin) | idle | — (slots unmount; 60 s grace) |
| 7 | docked | P=thread | arm() | hold |
| 8 | docked | P=own | arm() (replyRoot is null by construction) | hold |
| 9 | docked | P=plugin | away{cause plugin, returnOnEnd false, replyRoot null, latched ctx.latch, …} | hold |
| 10 | docked | P=empty | docked | — (unreachable in practice) |
| 11 | away | P=villanet, ¬S | docked | cancelPending, release, markDocked |
| 12 | away | P=villanet, S | unchanged | — |
| 13 | away | `after.teamId ≠ A.teamId` or `after.path ≠ A.path` | idle | cancelPending, stopAll |
| 14 | away | M | idle | cancelPending, release |
| 15 | away | P∈{closed, empty}, A.returnOnEnd, ¬S | unchanged | return{expand: A.expanded ∧ after.channelId = A.channelId} |
| 16 | away | P∈{closed, empty} | idle | cancelPending, release |
| 17 | away | P=plugin | A with cause plugin, returnOnEnd false, replyRoot null | cancelPending |
| 18 | away | A.cause = plugin (P is thread or own) | unchanged | — |
| 19 | away | A.cause = suppressed, ¬S (P is thread or own) | arm(A.latched) | cancelPending |
| 20 | away | A.cause = suppressed, S | unchanged | — |
| 21 | away | P=thread, root ≠ before's root, A.replyRoot ≠ null | replyRoot = root; but if root = A.noReplyFor or the hint matches root: replyRoot null, noReplyFor root | cancelPending, clearPlaceholders |
| 22 | away | P=thread, root ≠ before's root, A.replyRoot = null | unchanged, except a hint matching root sets noReplyFor = root | cancelPending |
| 23 | away | P=own, A.replyRoot ≠ null | replyRoot = null | cancelPending |
| 24 | away | otherwise (same panel; e.g. expansion toggled) | unchanged | — |

`hintUsed` is true when `hinted` decided a row. away.ts clears the hint after any `step` that changes `P`, and in any case after 2 s.

**Effects (implemented in away.ts):**
- `hold`: if `settings.mini === 'on'`, `¬M`, this window has a mini host (§3.8) and `latched` has a session, call `holdSession(latched, HOLD_MS)`. **Nothing else is held.** away.ts also re-applies this hold whenever the mini-player goes from visible to hidden while still away (except Hide), so each hidden spell is bounded (§4.6).
- `release`: release every hold this controller made. Each release, like each hold expiry, runs the pool's `maybeGrace`.
- `stopAll`: `stopAllSessions('closed')`, then `release`.
- `cancelPending`: clear the reply-return timer and its engagement listeners; `returningUntil = 0`.
- `markDocked`: `dockedFromAwayAt = performance.now()` (read by `cameBack()`, §5).
- `clearPlaceholders`: reset the `seen`/`mine` sets of §3.5.
- `return{expand}`: see §3.6.

**Re-latching (away.ts, outside `step`):**
- While away, if `latched` leaves `watching`, recompute the latch candidate (§6.2).
- Move the hold to the new handle when the hold conditions apply.
- Clear the mini-player's "was waiting" mark (§6.2).

### 3.5 Reply landed

This runs only when away, cause own, `replyRoot !== null`, P=thread on that root, and ¬S.

```
listener hot path, while replyRoot is set:
  ids = state.entities.posts.postsInThread[replyRoot]
  if (ids === lastIds) return; lastIds = ids
  for id in ids not in seen: seen.add(id); p = posts[id]; if !p continue
     if (p.id === p.pending_post_id && p.user_id === me) mine.add(p.id)      // this tab's placeholder
     else if (p.pending_post_id && mine.has(p.pending_post_id) && !p.failed) LANDED
  failed:true and removed placeholders are ignored; a retry reuses the id, so it can still land.

LANDED:
  pending = setTimeout(fire, RETURN_DELAY_MS = 1500); returningUntil = now + 1500 (drain line)
  engagement listeners (document, capture), removed by cancelPending or fire:
    'input' whose target is #reply_textbox,
    'pointerdown', 'wheel' or 'keydown' whose target is inside #sidebar-right
      → cancelPending (stay armed: replyRoot and returnOnEnd unchanged)

fire:
  remove listeners
  guard = shouldReturnAfterLanded(away, world, settings, root)
          // pure: still away, cause own, replyRoot === root, P=thread on root, ¬S, ¬M, returnAfter === 'reply'
  a = document.activeElement
  draftEmpty   = #sidebar-right #reply_textbox value.trim() === ''
                 && no #sidebar-right .file-preview__container && no #sidebar-right #edit_textbox
  popupFocused = a?.closest('.a11y__popup, .a11y__modal, [role="dialog"], [aria-modal="true"]')   // §1.7
  focusOk      = !a || a === body || !#sidebar-right.contains(a) || a.id === 'reply_textbox'
  if (!guard) cancelPending
  else if (!draftEmpty || popupFocused || !focusOk) stay armed
  else:
     target = (a && a !== body && !#sidebar-right.contains(a)) ? a : 'panel'
     undo = {root, channelId: posts[root].channel_id, channelName, shownAt: now}
     show(); startFocusGuard(target)                       // §3.7
```

**Keeping the listener cheap:**
- Compare by identity only: `views.rhs`, `views.rhsSuppressed`, `views.browser.windowSize`, `entities.teams.currentTeamId` and `entities.channels.currentChannelId`.
- Check `postsInThread[replyRoot]` only while armed.
- `combineReducers` replaces a slice only when it changed.

### 3.6 Close-return (`return{expand}`)

```
Promise.resolve().then(() => {               // never dispatch inside the listener
  re-read the world
  guard = panelOf ∈ {closed, empty} && !rhsSuppressed && !mobile
          && teamId and path unchanged && returnAfter !== 'never'
  if (!guard) { endAway(); return }          // idle + release: the mini-player goes, sessions get the 60 s grace
  a = document.activeElement
  target = (a && a !== body && !#sidebar-right.contains(a)) ? a              // Mattermost already restored it (§1.5)
         : (focusAtDisplacement?.isConnected ? focusAtDisplacement : 'panel')
  show(); if (expand) dispatch {type:'SET_RHS_EXPANDED', expanded:true}
  startFocusGuard(target)
})
```

`focusAtDisplacement` is `document.activeElement`, recorded by the listener at the docked → away transition when it is outside `#sidebar-right` and not `body`. Mattermost moves focus only in timers, so this is the same element Mattermost saved as `previousActiveElement` (sidebar_right.tsx v11:159).

### 3.7 Focus guard

Mattermost moves focus into new side-panel content in two timer hops. On a close it restores focus first (§1.5). A plugin timer therefore cannot reliably run last. Instead, the guard waits for Mattermost's own focus move and corrects it:

```
startFocusGuard(target: HTMLElement | 'panel'):
  deadline = 600 ms
  abandon the guard on any 'pointerdown' or 'keydown' (document, capture): the person acted
  on 'focusin' (document, capture) whose target is inside #sidebar-right: setTimeout(apply, 0)
  at the deadline: apply(), then remove all listeners
  apply():
    el = (target === 'panel' || !target.isConnected) ? #sidebar-right .vntv-panel : target
    if (document.activeElement === el) return
    if (!activeElement || activeElement === body || #sidebar-right.contains(activeElement))
        el.focus({preventScroll: true})
```

- The VillaNET panel root gets `tabIndex={-1}` so that it can take focus. The next Tab stop after it is the undo strip's **Back to thread** (§7). Stray keystrokes there do nothing, unlike the centre box, which would post them into the channel, or the Top Streamers button, which would open its dialog.
- Stray keystrokes only do nothing there because the root stops them. Mattermost's centre editor listens for `keydown` on `document` and moves focus into `#post_textbox` for any single printable key, or Ctrl/Cmd+V, pressed while focus is on anything but a TEXTAREA or INPUT (`use_textbox_focus.tsx` `focusTextboxIfNecessary` and `post_utils.ts` `shouldFocusMainTextbox` on 10.x and 11.x; `advanced_create_post.tsx` on 9.x). The root's `onKeyDown` therefore calls `stopPropagation()` for exactly those keys when `event.target === event.currentTarget`; React handles them at Mattermost's root container, below `document`. Tab, F6 and modified shortcuts are untouched. The root is also `role='region'` with `aria-label='VillaNET'`, so the focus move is read out by name.
- On 9.11, Mattermost does not move focus, so the deadline applies the target.

### 3.8 Public API of away.ts

```ts
export type WindowKind = 'main' | 'channel-popout' | 'other-popout';
export function windowKind(): WindowKind;
//   path after window.basename starts with '/_popout/channel/' → 'channel-popout'
//   starts with '/_popout/' or document.querySelector('.rhs-popout') → 'other-popout'
//   otherwise 'main'
export function installAway(deps: {store: Store; sidebarId: string; show: () => void;
    miniHost: boolean}): () => void;
//   'other-popout': a no-op (VillaNET is not in a SidebarRight there; closing the window ends it)
//   'channel-popout': installed with miniHost false (no holds, no mini-player)
export type AwayView = {phase: 'idle'|'docked'|'away'; cause?: string; panel: Panel;
    suppressed: boolean; mobile: boolean; channelId: string; latched: string | null;
    hidden: boolean; returningUntil: number;
    undo: null | {root: string; channelId: string; channelName: string; shownAt: number}};
export function useAway(): AwayView;          // observable, the watching.ts:46-59 pattern
export function getAway(): AwayView;
export function backToVillaNET(): void;       // deps.show()
export function backToThread(): void;         // sets hint {root, until: now + 2000}, then dispatches SELECT_POST
export function hideMini(): void;             // hidden = true, release (R18)
export function switchOffAll(): void;         // stopAllSessions('power') + clearWatching(); away stays
export function cameBack(): boolean;          // phase === 'away' || performance.now() - dockedFromAwayAt < 1000
export function announce(text: string): void; // through the mini host's live region; a no-op without a host
```

**`backToThread` guards:** the root still exists in `posts` with `delete_at === 0`, `!rhsSuppressed`, and `!mobile`. If any fails, the button is not rendered.

### 3.9 Edge cases (complete list)

| Case | Outcome |
|---|---|
| Thread opened while VillaNET closed | idle stays idle (R11) |
| VillaNET icon or Ctrl+. while VillaNET showing | docked → idle, and sessions stop |
| VillaNET icon while a thread is open | `toggleRHSPlugin` shows it (rhs.ts v11:310-322); away → docked; no reconnect |
| Thread to thread (Reply on another post) | the root is retargeted (row 21) and the pending return cancelled. After Back to thread, it stays close-only |
| Thread → search → Back to results → thread | cause own, replyRoot null; returns on close only |
| Search started from VillaNET's "Open in Mattermost search" | own detour; close returns |
| VillaNET's hashtag Jump to a reply (the CRT permalink opens the thread) | thread detour, reply-return armed |
| Root deleted while armed | `POST_REMOVED` clears `selectedPostId` (rhs.ts v11:26-30), so P=empty and VillaNET returns |
| goBack to `'plugin'` with `pluggableId ''` | P=empty; VillaNET returns into the otherwise empty panel |
| Channel switch during a thread | the thread stays and it is still armed. Mattermost collapses expansion. Expansion is restored only within the armed channel. The mini-player re-measures (§6.3) |
| Keyboard-opened thread from an expanded VillaNET | the reply-return keeps expansion (rhs.ts v11:346-347); the close-return restores it in the same channel |
| Mouse Reply from an expanded VillaNET | Mattermost collapses first (§1.6); VillaNET comes back collapsed |
| Reply posted from the Threads page | ignored while suppressed |
| Same thread replied from another tab or device | no placeholder in this tab, so no return |
| Reply fails, then retried | lands on the retry and returns |
| Return timer fires while a draft is present, or focus is in the thread or a popup | stays armed |
| Settings changed mid-detour | read at fire time. Closing with "never" ends the detour (row 15, then §3.6 guard, then idle) |
| Another plugin's panel closed | row 16: idle, release |
| Team or product switch while away | row 13: idle, stopAll |
| Window narrowed to phone size while away | row 14: idle, release |
| Plugin disabled mid-detour | `uninstall` clears timers and listeners; Mattermost removes the Global component (plugins/index.ts v11:208-212) |
| Unrecognised store shape | inert |

---

## 4. Media session pool (`webapp/src/sessions.ts`)

### 4.1 Model

- There is **one `<video>` element per session**, created and owned by the pool.
- Components render an empty slot (`<div className='vntv-video-slot'>`). The pool *moves* the element into the most recently mounted slot for that handle.
- **No change to `whep.ts`.** The pool calls `player.play(handle, session.video, fail)`, and `ontrack` writes the pool's element (whep.ts:30-35). The lab mock and the stills peek (stills.ts:304-365) are untouched.
- There is never more than one element per stream, so no audio handoff is needed and two elements cannot play the same sound.

### 4.2 Record and constants

```ts
export const GRACE_MS = 60000;        // no slot, no holds → stop
export const HOLD_MS  = 240000;       // the latched stream only (§3.4 hold)
const STALL_TIMEOUT_MS = 12000;       // moved from Player.tsx:43
const RESUME_BACKOFF_MS = [250, 1000, 4000]; const RESUME_BUDGET_WINDOW_MS = 60000;   // Player.tsx:34-38
const DETACH_PAUSE_MS = 1000;         // a pause this soon after a detach is the detach's own (§4.4)
export const UNMUTE_VOLUME = 0.6;     // Player.tsx:28

type Session = {
  handle: string; video: HTMLVideoElement; player: WhepPlayer;
  status: 'connecting' | 'playing' | 'failed'; error: string;
  volume: number; blocked: boolean; quiet: boolean;
  slot: HTMLElement | null; detached: boolean; detachedAt: number;
  holds: Map<number, number>; grace: number; stall: number;
  resumes: number; lastPause: number; resumeTimer: number; version: number;
};
```

### 4.3 API

```ts
export function installSessions(): () => void;          // subscribes to watching changes (onWatchingChange)
export function mountVideo(handle: string, slot: HTMLElement, opts?: {create?: boolean}): () => void;
                                                         // call in useLayoutEffect; create defaults to true
export function holdSession(handle: string, ms?: number): () => void;   // no-op if no session
export function sweepMissing(liveHandles: string[]): void;              // stop sessions not in the snapshot
export function retrySession(handle: string): void;
export function takeSound(handle: string): void;        // INSIDE a click: unmute and play synchronously, then setAudio
export function setSessionVolume(handle: string, v: number): void;      // Player.applyVolume semantics
export function quietSession(handle: string, quiet: boolean): void;     // tile switching off
export type SessionInfo = Readonly<{status; error; volume; blocked; version}>;
export function getSessionInfo(handle: string): SessionInfo | null;
export function useSessionInfo(handle: string): SessionInfo | null;     // observable, per-session version
export function stopSession(handle: string, reason: string): void;
export function stopAllSessions(reason: string): void;
export function sessionCount(): number;
```

### 4.4 Lifecycle

**Create:**
- Only inside `mountVideo`, and only if all of these hold:
  - `opts.create !== false`;
  - `getWatching().includes(handle)`;
  - `getSnapshot().streams` contains `handle` (the same truth the panel renders a Player on, Sidebar.tsx:869);
  - `sessionCount() < MAX_WATCHING`.
- Otherwise there is no session. The caller renders its "no session" state (§5 Player, §6.2).
- To create: make the element (`autoplay`, `playsInline`, `muted`, class `vntv-video`), add listeners, then `negotiate()`.

**`negotiate()`:**
- status `connecting`, error `''`;
- `video.muted = true`, `video.volume = s.volume`;
- start the stall timer;
- `player.play(handle, video, fail).catch(fail)`.

**`loadeddata` / `resize`:**
- clear the stall timer; status `playing`;
- `offerFrames(handle, video)` if connected;
- `applyAudio` (§4.5).

**`playing`:** `s.detached = false`.

**`pause`:**
- Ignore it if the session is stopping, if `!video.isConnected`, if `s.detached`, or if `performance.now() - s.detachedAt < DETACH_PAUSE_MS`. Removing an element queues its own `pause` task (§1.8, HTML standard), and that task can arrive after the element is already in its next slot.
- Otherwise run the backoff from Player.tsx:206-233, resuming with `playSafely` (§4.5), never a bare `play()`.
- When the budget is spent: status `failed`, error `Playback keeps stopping. Retry to reconnect.`

**`fail(reason)`:**
- clear the stall timer; `withdrawFrames`; status `failed`, error `reason`;
- if there is no slot and no hold, stop.

**`mountVideo(handle, slot, opts)`:**
- create if allowed, or return a no-op;
- cancel grace;
- `slot.appendChild(video)`, which moves it if it is elsewhere; `s.slot = slot`;
- `applyAudio`;
- if paused and not failed, `playSafely`;
- if playing, `offerFrames`;
- return `unmount`.

**`unmount`:**
- If `s.slot === slot`: `video.remove()`, `s.slot = null`, `s.detached = true`, `s.detachedAt = performance.now()`, `withdrawFrames`, `maybeGrace()`.
- If the element has already moved, do nothing. The order of mounts and unmounts is therefore irrelevant.

**Holds:**
- `maybeGrace`: if there is no slot and no hold, `grace = setTimeout(() => stop('grace'), GRACE_MS)`.
- `holdSession`: add a hold that expires after `ms` and cancel grace. It returns a release function.
- **Hold expiry runs exactly the release code:** delete the hold, then `maybeGrace`.

**`stop(reason)`:**
- clear every timer;
- `player.stop()`; `video.srcObject = null`; `video.remove()`; remove listeners;
- `withdrawFrames`; delete the session; notify.

**Watching listener** (synchronous, installed in `initialize` before any component subscribes, so it runs first):
- stop every session whose handle is no longer in `watching`;
- then `applyAudio` for all sessions.

**`sweepMissing(live)`:**
- Called by the reconciler after every snapshot, in every phase.
- It stops every session whose handle is not in `live` (reason `missing`). This matches today, where the WaitingTile replaces the Player and the Player's cleanup stops the connection (Sidebar.tsx:869-885, Player.tsx:150-162).
- When the stream returns, the next mount negotiates afresh, with its own stall watchdog.

**`retrySession`:**
- `player.stop()` then `negotiate()`. The same element is kept, and the generation guard (whep.ts:62-66) still protects against stale answers.
- If there is no session, Player retries by remounting (§5).

### 4.5 Audio rule (exactly one stream audible)

**`applyAudio(s)`.** Let `want = getAudio() === s.handle && !s.quiet && !s.blocked`.
- If `getAudio() !== s.handle`: `muted = true`, `blocked = false`.
- Else if `!want`: `muted = true` (quiet, or blocked).
- Else: unmute (volume 0.6 if it is 0), and `playSafely` if connected and paused.

**`playSafely`.** Call `video.play()`. On `NotAllowedError` while unmuted: re-mute, set `blocked = true`, notify, and `play()` muted.

**`blocked` is cleared only** by `takeSound` (inside a click) or by the sound moving to another stream. A refusal is therefore never undone by the `resize` events a WebRTC receiver keeps firing (Player.tsx:130-131), or by watching notifications or remounts.

`getAudio()` stays the person's *intent*, and `blocked` is the reality. All UI uses `audible = getAudio() === handle && !info.blocked`, so no tile claims to have the sound when it does not (README "The tile that said it had the sound and did not").

**`takeSound` runs inside the gesture**, as Player.tsx:238-254 does:
1. `volume = s.volume || 0.6`, `muted = false`, `blocked = false`;
2. `play()` if paused;
3. then `setAudio(handle)`, which mutes all others through the synchronous listener.

### 4.6 Bounds

| Bound | Value |
|---|---|
| Sessions | at most `MAX_WATCHING` (4), always a subset of `watching`. Peeks are separate and capped (stills.ts:209-223) |
| Deliberate close, team/product switch, tile closed, stream missing from the snapshot | stop at once |
| Any stream without a slot and without a hold (panel unmounted, mini setting off, phone, popout, non-latched streams while away) | ≤ `GRACE_MS` = 60 s invisible |
| The latched stream while away | visible in the mini-player: no bound, because it is being watched. Each hidden spell: ≤ `HOLD_MS + GRACE_MS` = 5 min |
| Hide, or the detour ending without a return | ≤ 60 s |
| Plugin disable or upgrade | 0: `uninitialize` stops everything |

- **Bandwidth:** never more streams than the panel already had open.
- **Viewers:** held sessions still count as viewers on Broadcast Box, which counts by peer connection (README "The player"). They also keep the person's face on those streams' Live Now cards (README "Who is watching"). They would also count as watch time if Top Watchers measures open sessions. All of this lasts at most the bounds above.

### 4.7 Other module changes

**`watching.ts`:**
- `onWatchingChange(listener)`: a subscription outside React.
- `clearWatching()`: empties the list and the audio.
- `installReconciler(isActive: () => boolean)`: subscribes to `store.ts` snapshots. On each snapshot:
  - **always** call `sweepMissing(handles)`;
  - **only while `isActive()`** (away.ts phase is not `idle`), call `reconcile(handles)`, then notify a snapshot-version observable that the mini-player reads;
  - only while active, keep one timer for the earliest `missingSince + MISSING_GRACE_MS` and reconcile again when it fires.
- While VillaNET is closed, the watch list is kept, as today.

**`store.ts`:**
- `pollWhileShown(): () => void`, counted like `captureStillsWhileOpen` (stills.ts:104-126).
- When the count goes 0 → 1 it fetches at once, then every `REFRESH_MS`, backing off on failure up to `MAX_REFRESH_MS`. That logic moves from Sidebar.tsx:159-198, with its constants (:37, :43).
- Both Sidebar and MiniPlayer call it.

**`stills.ts`:**
- Add `video.isConnected` to `moving()` (:247-264) as a safeguard.
- Reword the `STALE_SNAPSHOT_MS` comment (:57-60) to "VillaNET polls every 30 s while the panel or the mini-player is showing".

**`Hud.tsx`:**
- New optional `powerLast?: boolean`. When it is set, the children's bar is rendered **before** the power button in the DOM.
- The power button is absolutely centred (styles.ts:1254-1270), so only the tab order changes. Tiles keep today's order.

**`index.tsx` `uninitialize`** order:
1. `uninstallAway()`
2. `uninstallReconciler()`
3. `stopAllSessions('unload')`
4. `uninstallSessions()`
5. `resetWatching()`
6. the existing teardown (:263-267)

Keep the unsubscribe functions on `this`. `uninitialize` gets no registry argument (plugins/index.ts v11:212).

---

## 5. Components and files

| File | New/changed | What |
|---|---|---|
| `webapp/src/sessions.ts` | new | §4 |
| `webapp/src/awayLogic.ts` | new, pure | §3.2-3.4, `shouldReturnAfterLanded` |
| `webapp/src/away.ts` | new | §3.5-3.8, latching, focus guard, `cameBack()` |
| `webapp/src/miniLayout.ts` | new, pure | §6.3 maths: `(anchor, rhsRect, corner) → {mode, x, y, w, h}` |
| `webapp/src/userSettings.ts` | new | schema (§8), `returnSetting()`, `miniSetting()` |
| `webapp/src/components/MiniPlayer.tsx` | new | `MiniPlayerHost`: a local error boundary that renders null on error, always renders the announcer, and renders `MiniPlayer` when visible (§6.1) |
| `webapp/src/components/ReturnStrip.tsx` | new | undo strip (§7) |
| `webapp/src/components/Player.tsx` | changed | See the Player row details below |
| `webapp/src/components/Sidebar.tsx` | changed | See the Sidebar row details below |
| `webapp/src/components/Hud.tsx` | changed | `powerLast` (§4.7) |
| `webapp/src/store.ts` | changed | `pollWhileShown` (§4.7) |
| `webapp/src/index.tsx` | changed | See the index row details below |
| `webapp/src/mattermost.ts` | changed | export `pluginPreference(name, fallback)` built on `preference` (:63); `isMobileView()` |
| `webapp/src/watching.ts`, `stills.ts` | changed | §4.7 |
| `webapp/src/motion.ts` | changed | `RETURN_DELAY_MS = 1500`, `UNDO_MS = 30000`, `MINI_FADE_MS = 180`, `SNAP_MS = 240` |
| `webapp/src/styles.ts` | changed | §9 |
| `build/check-away.mjs` | new | §13.1 |
| `Makefile` | changed | `check` runs `node build/check-away.mjs` |
| `plugin.json` (:8), `server/plugin.go` (`manifestVersion`, :21) | changed | 2.11.0 (enforced by `build/check-version.py`) |
| `README.md` | changed | §15 |
| `webapp/lab/*`, `webapp/lab/mocks/whep.ts` | changed | §12 |

**Player.tsx changes:**
- `<video>` (:320-326) becomes `<div className='vntv-video-slot' ref={slotRef}/>` with `useLayoutEffect(() => mountVideo(handle, slotRef.current!), [handle, mountKey])`.
- Status, error and volume come from `useSessionInfo`.
- `takeAudio` → `takeSound`; `applyVolume` → `setSessionVolume`; the `closing` effect → `quietSession`.
- Retry → `info ? retrySession(handle) : setMountKey(k + 1)`.
- If `useSessionInfo(handle) === null` after mount, render a Plate "Not connected" with Retry, never a permanent Tuning.
- Remove the negotiation effect, the pause handler and `offerFrames`/`withdrawFrames`; they move to the pool.
- Fullscreen is unchanged.

**Sidebar.tsx changes:**
- `useState(() => !reducedMotion() && !cameBack())` at :71. No boot sweep when the panel comes back from a detour, whatever the render order (§1.4).
- The poll effect (:159-198) becomes `React.useEffect(pollWhileShown, [])`. The `fetchMe` part stays.
- Add `tabIndex={-1}` on the root (:762-768).
- Put `<ReturnStrip/>` first inside `.vntv-body` (:771).
- The reconcile effect (:223-225) stays; it is harmless and silent.

**index.tsx changes:**
- `Registry` gains `registerGlobalComponent?`, `registerRootComponent?` and `registerUserSettings?`.
- After :172 add, in order:
  1. `installSessions()`
  2. `installReconciler(() => getAway().phase !== 'idle')`
  3. `installAway({store, sidebarId: sidebar.id, show: () => store.dispatch(sidebar.showRHSPlugin as never), miniHost})`
  4. `registerUserSettings?.(USER_SETTINGS)`
- Mount `MiniPlayerHost` with Global when available. Otherwise use Root, which renders nothing unless `windowKind() === 'main'`. Otherwise there is no mini-player.
- `miniHost` is `windowKind() === 'main'` and a host was registered.
- Teardown per §4.7.

---

## 6. Mini-player

### 6.1 Visibility

The mini-player is shown when **all** of these hold:
- this is the main window and a host is mounted;
- `away.phase === 'away'` and `!away.hidden`;
- `away.panel` is `thread`, `own` or `plugin`, so something else is in the side panel, and `!suppressed`;
- `miniSetting() === 'on'` and `!mobile`;
- `away.latched !== null`, and the latched stream either has a session, or is waiting (`waitingFor(latched)`), or is the stream coming back after this mini-player showed its Waiting plate (§6.2);
- `#post-list` exists and there is room for at least bar mode.

Otherwise it renders nothing apart from the announcer. Its slot unmounts and the element pauses, so it falls silent. The hold or grace bounds the session (§4.6).

**It never takes focus when it appears.**

*Added 2026-10-05 (`design/lock-screen.md` §7).* It is never shown to a locked reader: its gate (`MiniPlayerGate` in `MiniPlayer.tsx`) shows nothing unless the reader may watch, and the watch list is emptied when access is lost and on sign-out.

### 6.2 Content

**Latched stream.** This is the picture. It is set at displacement to:
- `getAudio()` if that is watched;
- else the first watched handle with a session;
- else `getWatching()[0]`;
- else null.

It changes only when the latched handle leaves `watching` (re-latch, §3.4). So:
- Mute in the mini-player only mutes;
- when the audible stream goes missing, the mini-player shows its Waiting plate. It does not jump to another stream (watching.ts:163-166 releases the sound immediately, which is why the picture must not follow the sound).

**"+N" pill.** The number of other watched streams. It is non-interactive and `aria-hidden`. The count is in the region's `aria-describedby` text.

**States:**
- waiting (`waitingFor(latched)`): Plate "No Signal / Waiting for {name}";
- connecting: `<Tuning/>`;
- failed: Plate with the session error and a Retry pill (`retrySession`);
- playing.

**Creating a session.** The mini-player calls `mountVideo(latched, slot, {create: wasWaiting})`.
- `wasWaiting` is true only when the previous render of this mini-player showed the Waiting plate for this same handle. That is the panel's behaviour today when a stream comes back.
- In every other case the mini-player adopts an existing session or shows nothing. It never reconnects a stream nobody is looking at.

**Data:** `useAway`, `useWatching`, `useAudio`, `useSnapshot` (so the waiting state updates on snapshots, not only on watching changes) and `useSessionInfo(latched)`.

**While visible:** effects call `captureStillsWhileOpen` (stills.ts:104-126) and `pollWhileShown` (§4.7). Preview stills therefore keep flowing during a quiet spell longer than `STALE_SNAPSHOT_MS`.

### 6.3 Layout (`miniLayout.ts`, pure)

**Anchor:**
- the `#post-list` rect. If `#sidebar-right` overlaps it (the expanded case), the right edge is `min(anchor.right, rhs.left)`;
- **insets:** top 52 px (clears the toast row: 10 px margin plus 32 px tall), sides 16 px, bottom 12 px. On the right, add the scrollbar width of the first scrollable descendant (`offsetWidth - clientWidth`), defaulting to 8 px.

**Modes:**
- **Picture.** `w = clamp(240, round(0.30*W), 384)`, `h = w*9/16 + 34`.
  - If `h > 0.45*H`, set `w = floor((0.45*H - 34)*16/9)`.
  - Use it if `w ≥ 240` and `W ≥ w + 32`.
- **Bar.** `w = clamp(240, round(0.45*W), 360)`, `h = 44`. Use it if `W ≥ w + 32` and `H ≥ 108`.
- **Hidden** otherwise.

**Corner:** `tr` (default), `tl`, `br` or `bl`, from localStorage `villanet.miniCorner` (in try/catch, as rhsWidth.ts does).

**Re-measuring:**
- on window resize;
- on every away notification, including when `channelId` changes;
- `ResizeObserver` on `#post-list` and on `#sidebar-right` when present;
- a `MutationObserver` on `#app-content` (`childList`, `subtree`), throttled to one requestAnimationFrame. When `document.getElementById('post-list')` is a different node than the observed one (channel_view.tsx v11:37-48; post_view.tsx v11:84, 92), re-attach the ResizeObserver and re-measure.

**DOM:** mode changes are **CSS only**. The structure stays the same, so the slot never remounts.

**Stacking:** `position: fixed`, `z-index: 19`. That is above the post list and toast, below `#sidebar-right` (20) and below menus and modals.

**File drag:** while a file is dragged over the window, set `data-file-drag`, which gives `opacity: .3; pointer-events: none`.

### 6.4 Structure, controls and order

```
<section role="region" class="vntv vntv-mini a11y__region" data-a11y-sort-order="4"
         aria-label="VillaNET player" aria-describedby="villanet-mini-N-desc" data-mode=…>
  <div class="vntv-mini-bar">           Back to VillaNET (skull) · TileInfo          ← first in DOM
  <div class="vntv-mini-screen">        slot · Tuning/Plate · "+N" · Turn sound on ·
                                        Hud powerLast { Listen/Mute · Volume (w ≥ 300) · spacer · Move · Hide } · Switch off
  <div class="vntv-mini-controls">      Listen/Mute · Move · Hide                    ← bar mode only
  <div class="vntv-mini-drain">
  <span class="vntv-sr-only" id="villanet-mini-N-desc">…and N more streams waiting in VillaNET</span>
```

**Picture mode:**
- The root is a column, and the name line is placed under the screen with CSS `order`. It never uses a positive tabindex.
- The `.vntv-mini-controls` cluster is `display: none`.
- The HUD is the reused `Hud`, so it wakes and sleeps exactly as on a tile.
- **Tab order:** Back, the ticker toggle or links in TileInfo when present, Turn sound on (when shown), Listen/Mute, Volume, Move, Hide, Switch off. The destructive control is last.

**Bar mode:**
- The root is a row and the same `.vntv-mini-screen` becomes a 56×44 live thumbnail (`object-fit: cover`; at 78 px the name was left about 28 px). The element stays connected, so sound continues.
- The plate and the picture's Retry do not fit, so a waiting or failed stream shows "Waiting" or "No signal" under the name (`.vntv-mini-state`, full text in its tooltip), and while failed a **Retry** icon button (circular arrow) takes Move's place in the row.
- The HUD is `display: none`, so Switch off is reachable only in picture mode. The name gets whatever width is left, with an ellipsis.
- **Tab order:** Back, ticker, Listen/Mute, Move, Hide.

**Region:**
- `role='region'` with `aria-label`.
- Reached with Ctrl/Cmd+F6 in a browser and F6 in the desktop app (a11y_controller.ts v11:801-805). Sort order 4 puts it after the RHS (3) and before the left sidebar (6).
- Element ids use the `villanet-mini-` prefix and a module counter, never `vntv-` (build/check-styles.py reads `vntv-*` strings as classes).

**"Turn sound on"** shows on the picture when `blocked` and calls `takeSound`.

**Move:**
- Enter or Space cycles clockwise; arrow keys jump to the corner in that direction.
- **Drag:** press on the screen or name line, not on a control. Moving more than 6 px starts the drag (`data-dragging`, which hides the HUD by CSS). The player follows the pointer, clamped to the anchor. On release it snaps to the nearest corner over `SNAP_MS`. The press is followed with window `pointermove`/`pointerup`/`pointercancel` listeners added on pointerdown and removed on release, so a pull that leaves the player before its first move, or a release anywhere, still ends it.
- `touch-action: none`. The click is suppressed after a drag, for that task only: a touch moved past the tap slop brings no click, and the next tap must not be eaten.

**Back:** `backToVillaNET()`. Focus goes wherever Mattermost puts it for an opening.

**Hide:** `hideMini()`, then announce "Player hidden. Your streams are kept in VillaNET."

**Switch off:** `switchOffAll()`, then announce "Switched off."

**Focus after Hide or Switch off:** if focus was inside the mini-player, move it to the element that had focus before focus entered the mini-player (recorded on the mini-player's first `focusin` from outside) if it is still connected; else to `#sidebar-right #reply_textbox` if present; else leave it. **Never `#post_textbox`.**

### 6.5 Motion

| Moment | Effect | Time |
|---|---|---|
| Appear | fade and scale from the corner | `MINI_FADE_MS` 180 |
| Mode change | crossfade | 180 |
| Corner snap | glide | `SNAP_MS` 240 |
| Return pending | 2 px `#c8102e` line draining right to left, `aria-hidden` | `RETURN_DELAY_MS` 1500 |
| Dock | panel mounts with no boot sweep and no tuning; the mini-player fades out | 180 |

With reduced motion everything is instant and the drain line is not drawn. Check `reducedMotion()` at the point of use (motion.ts:52-58).

---

## 7. Undo strip (`ReturnStrip`)

- **When:** only after an R1 return (`away.undo` set).
- **Where:** first child of `.vntv-body`. After R1, focus is on the panel root (§3.7), so **one Tab reaches the button**.
- **Text:** "Reply posted in {channel}." For DMs and group messages: "Reply posted."
- **Button:** pill **Back to thread**, which calls `backToThread()`. The reopened thread returns on close only (R19).
- **Stays until any of these:**
  - `UNDO_MS` (30 s) has passed with the strip neither hovered nor focused. While it is hovered or focused it never times out;
  - a click elsewhere in the panel (on the click, not the pointerdown: the strip going mid-press moves everything below it up by its height, and the release would then land on something else and the click be lost);
  - focus moves to another control in the panel, other than by a press, which the click covers;
  - the panel unmounts.
- **Announcement:** "Reply posted in {channel}. VillaNET is back. Back to thread is at the top of VillaNET." The strip mounts together with the panel, and a live region inserted with its text already in it is generally not announced, so the strip itself is not the live region. In the main window, `markDocked` says it through the mini host's always-mounted announcer. In channel popouts, which have no mini host, the strip keeps an always-rendered empty `role='status'` region and fills it 250 ms after the strip appears.

---

## 8. Settings (opt-out)

```ts
export const USER_SETTINGS = {
  id: PLUGIN_ID, uiName: 'VillaNET', icon: APP_BAR_ICON,
  sections: [
    {title: 'After replying to a message', settings: [{
      name: 'return_after_reply', type: 'radio', default: 'reply',
      helpText: "Only when a thread or another of Mattermost's own panels took VillaNET's place. If you closed VillaNET yourself, it stays closed.",
      options: [
        {value: 'reply', text: 'As soon as your reply is posted', helpText: 'Closing the thread brings it back too.'},
        {value: 'close', text: 'When you close the thread'},
        {value: 'never', text: "Never, I'll open VillaNET myself"},
      ]}]},
    {title: 'While something else is in the side panel', settings: [{
      name: 'mini_player', type: 'radio', default: 'on',
      helpText: 'Only while a stream is playing. It goes back into the panel without reconnecting.',
      options: [
        {value: 'on', text: 'Keep watching in a small player over the channel'},
        {value: 'off', text: 'Only watch in the side panel',
         helpText: 'The stream is let go a minute after something else takes the panel.'},
      ]}]},
  ],
};
```

- **Reading:** `pluginPreference('return_after_reply', 'reply')` and `pluginPreference('mini_player', 'on')`. An unknown stored value falls back to the default.
- **9.0–9.10:** `registerUserSettings` is absent, so both settings are on.

---

## 9. CSS (all in styles.ts, scoped `.vntv`)

**Rules for editing `styles.ts`:**
- No backticks, even in comments.
- No `\00b7`-style escapes.
- Never slice the file between landmarks.
- Run `build/check-styles.py` after every edit, and look at the lab.

| Class / selector | Purpose |
|---|---|
| `.vntv.vntv-mini` | `position: fixed; top: 0; left: 0; height: auto; z-index: 19; width: var(--vntv-mini-w); transform: translate3d(var(--vntv-mini-x), var(--vntv-mini-y), 0); border: 2px solid var(--vntv-outline); border-radius: 4px; box-shadow: 0 8px 24px rgba(0,0,0,.45)`. Overrides `.vntv`'s `height: 100%` and flex column (styles.ts:33-51) |
| `.vntv-mini[data-mode='picture'] .vntv-mini-bar` | `order: 2` (Back is first in the DOM, under the picture on screen) |
| `.vntv-mini[data-mode='picture'] .vntv-mini-controls` | `display: none` |
| `.vntv-mini[data-mode='bar']` | row layout, height 44 |
| `.vntv-mini[data-mode='bar'] .vntv-hud` | `display: none` |
| `.vntv-mini[data-snapping='true']` | `transition: transform var(--snap)` |
| `.vntv-mini[data-dragging='true']` | `cursor: grabbing`; hides `.vntv-hud` |
| `.vntv-mini[data-file-drag='true']` | `opacity: .3; pointer-events: none` |
| `.vntv-mini .vntv-screen` | `max-width: none; max-height: none` (screen caps at styles.ts:669-678) |
| `.vntv-mini-screen` | 16:9 picture; 78×44 in bar mode |
| `.vntv-mini-bar` | name line, 34 px; name `min-width: 0`, ellipsis |
| `.vntv-mini-back` | skull Back button (copies `.vntv-trophy` metrics, styles.ts:3227) |
| `.vntv-mini-more` | "+N" (same look as `.vntv-live-count`, styles.ts:1486) |
| `.vntv-mini-move`, `.vntv-mini-hide` | corner and hide buttons |
| `.vntv-mini-sound` | "Turn sound on" pill |
| `.vntv-mini-controls` | bar-mode cluster |
| `.vntv-mini-drain` | 2 px drain line; keyframes keyed on `RETURN_DELAY_MS` |
| `.vntv-video-slot` | `display: contents`, so the existing `.vntv-player video` rules (styles.ts:991-1015) keep matching the moved element |
| `.vntv-video` | `display: block; width: 100%; height: 100%; object-fit: contain; background: #000` |
| `.vntv-panel:focus` / `.vntv-panel:focus-visible` | `outline: none` / `outline: 2px solid var(--vntv-red); outline-offset: -2px` |
| `.vntv-return`, `.vntv-return-text` | undo strip (reuses `.vntv-pill`) |
| Less-motion section | drain line hidden; transitions off |

The announcer reuses `.vntv-sr-only` (styles.ts:4135-4142) with `role='status' aria-live='polite'`.

---

## 10. Copy deck

| Where | Text |
|---|---|
| Region label | VillaNET player |
| Back (label and tooltip) | Back to VillaNET |
| Speaker | Mute / Listen to this stream |
| Volume tooltip | Volume {n}% / Muted — drag to listen to this stream (same as Player.tsx:356) |
| Blocked pill | Turn sound on |
| Hide | Hide player · tooltip: Hide the player. Your streams stay in VillaNET. |
| Power | Switch off {name} / Switch off all {n} streams |
| Move | Move to another corner |
| "+N" description | 1 more stream waiting in VillaNET / {n} more streams waiting in VillaNET |
| Failure | {session error} · Retry |
| No session (tile) | Not connected · Retry |
| Undo strip | Reply posted in {channel}. / Reply posted. · Back to thread · (screen readers) VillaNET is back. Back to thread is at the top of VillaNET. |
| Announcements | VillaNET is still playing {name} in the corner of the channel. · …{name} and {n} more… · VillaNET is back in the side panel. · Player hidden. Your streams are kept in VillaNET. · Switched off. · Moved to the top right / top left / bottom left / bottom right. |
| Settings | §8 |

---

## 11. React 17 and compatibility constraints

**No React 18+ APIs:**
- No `useId`, `useSyncExternalStore`, `useTransition`, `useDeferredValue`, `useInsertionEffect` or `createRoot`. Use module-counter ids.
- `make check` already pins `@types/react` 17 and runs `build/check-react.py`.

**The code must also run on React 18.2 (11.1+) and 19 (master):**
- no `defaultProps` on function components;
- no `findDOMNode`;
- no string refs.

**Render order is not guaranteed (§1.4).** On legacy roots Mattermost re-renders inside the dispatch, before VillaNET's store listener; on concurrent roots it re-renders after. The design is order-free:
- the last slot to mount wins, and a stale unmount is a no-op;
- a hold arriving after a grace timer cancels it;
- detach pauses are ignored (§4.4);
- `cameBack()` is true both while the phase is still `away` and for 1 s after `markDocked`;
- focus is corrected from Mattermost's own `focusin`, not by racing its timers (§3.7).

**Other rules:**
- **Slots mount and unmount in `useLayoutEffect`.**
- **No react-redux.** Use observables in the watching.ts:46-59 pattern, so the lab needs no Provider.
- **Never dispatch inside `store.subscribe`.** Defer with a microtask and re-check the state first.
- **Mattermost wraps Global components in its error boundary**, which would print an error over the app. `MiniPlayerHost` therefore has its own boundary that renders null.

**The plugin stays polite:**
- It dispatches only `UPDATE_RHS_STATE` (through the registration), `SET_RHS_EXPANDED` (already done at mattermost.ts:240) and `SELECT_POST`.
- It does not change Mattermost's DOM or message rendering. It moves focus only as described in §3.7 and §6.4.
- It makes no network calls beyond today's. The 30 s poll also runs while the mini-player shows.
- Disabling the plugin removes every trace: the Global component goes, `removeStyles` runs, and sessions stop.

---

## 12. Lab changes (simulating a thread taking the panel)

### 12.1 `lab/mattermost.ts` (state near the top; `reduce` at about :73)

**Extend the state:**
- `views.rhs` gains `{rhsState: string|null, pluggableId, selectedPostId, selectedPostCardId, isSidebarOpen, isSidebarExpanded}`;
- `views.rhsSuppressed`;
- `entities.posts {posts, postsInThread}`;
- `entities.channels.currentChannelId`.

**Reduce exactly as Mattermost does:**
- **`UPDATE_RHS_STATE`:**
  - `pluggableId = state === 'plugin' ? action.pluggableId : ''`;
  - `selectedPostId = action.postId ?? ''`;
  - `isSidebarOpen = Boolean(state)`;
  - expanded kept only if `state`;
  - suppressed kept only if `state === null`.
- **`SELECT_POST`:**
  - `rhsState = null`, `pluggableId = ''`;
  - `isSidebarOpen = Boolean(postId)`;
  - expanded kept only if `postId`;
  - suppressed kept only if `postId === ''`.
- **`RHS_GO_BACK`:** `rhsState = action.state`, `selectedPostId = ''`; `pluggableId` and open are unchanged.
- **`SET_RHS_EXPANDED`, `SUPPRESS_RHS`, `UNSUPPRESS_RHS`.**
- **`RECEIVED_NEW_POST` (placeholder) and `RECEIVED_POST`:** replace the `postsInThread[root]` array identity, and delete `posts[pending_post_id]`.
- **`labBatch(actions)`:** reduce several actions and notify once, as `batchActions` does.

**Wiring:**
- VillaNET's lab id is `'lab-villanet'`.
- Replace `setSidebarOpener(() => undefined)` (lab/index.tsx, near the end) with a dispatch of the show action.
- **The lab's RHS renderer subscribes to the lab store before `installAway` is called**, matching Mattermost, whose Provider subscribes before plugins initialize (§1.4).
- Then call `installSessions()`, `installReconciler(...)` and `installAway({store, sidebarId: 'lab-villanet', show, miniHost: true})`.

### 12.2 `lab/index.tsx` stage (`.lab-stage`)

**A new `.lab-center` column:**
- a fake channel header;
- `<div id='post-list'>` with six sample posts, each with a Reply arrow;
- `#post-create` with `<textarea id='post_textbox'>`.
- A `mousedown` anywhere in `.lab-center` while expanded dispatches `SET_RHS_EXPANDED false`, as sidebar_right.tsx v11:234-245 does.
- A channel switch replaces the `#post-list` node (a fresh key) to exercise §6.3.

**The `.lab-rhs` slot renders the way sidebar_right.tsx does:**
- nothing if closed or suppressed;
- a thread if `selectedPostId` is set and the state is not edit history. `LabThread` has a header with `#rhsCloseButton`, the replies, `<textarea id='reply_textbox'>`, a Send button and a "…" menu rendered as `.a11y__popup`;
- `<Sidebar key={identity}/>` if the panel is ours;
- an "Another plugin's panel" placeholder;
- otherwise `LabOwnPanel` with an X and a Back-to-results control.

**`<MiniPlayerHost/>`** is mounted once at the lab root, outside the slot. It stands in for Global.

**A new "Side panel" chip row:**
- Reply (arrow, pushes the previous state)
- Open thread (post click)
- Type in reply
- Send reply (placeholder, then a real post after 400 ms)
- Reply fails (toggle)
- Reply from another device
- Close thread (batch close)
- Search
- Back to results
- Mentions
- Another plugin
- Threads page
- Switch team (changes `currentTeamId`, then batch close)
- Channel switch
- VillaNET icon (toggle semantics)
- Expanded
- Delay mini mount by one frame (reproduces the §4.4 detach pause)
- Freeze pushes (only `pollWhileShown` refreshes the snapshot; the stills test for §6.2)

The existing "Live now" go-offline and go-live controls (world.ts `goOffline` / `goLive`) exercise missing streams.

### 12.3 `lab/mocks/whep.ts` (`play` at :28, `stop` at :105)

- Count `plays`, `stops` and `live` on `window.__villanetLab`.
- Show them in the lab column next to "unmuted videos now / max ever", sampled every 50 ms from `document.querySelectorAll('video')` filtered on connected and not muted.
- The mock's on-screen seconds counter (:55, :82) restarts at 0.0 s on any reconnect. That is the continuity check.
- Optional, behind a chip: an oscillator audio track (the click supplies activation), and fail-mid-stream.

### 12.4 Lab limits

- The lab popout is the same window and the same module, so it cannot test separate windows or channel popouts.
- The browser pane's autoplay policy is permissive, so it cannot reproduce a refusal.
- **Mattermost's focus moves (§1.5) are not simulated.** Focus criteria are real-server only.

---

## 13. Tests and acceptance criteria

### 13.1 `build/check-away.mjs` (in `make check`; Node from `~/.local/opt/node/bin`)

**How:** transpile `webapp/src/awayLogic.ts` and `webapp/src/miniLayout.ts` with `webapp/node_modules/typescript`'s `transpileModule`, import them from a data URL, and assert with `node:assert`.

**Cover:**
- one case per row of §3.4, and one per row of §3.9 that can be expressed without DOM;
- `shouldReturnAfterLanded` truth tables;
- **Back to thread:** docked + hint(r) + `SELECT_POST r` → `away{replyRoot: null, noReplyFor: r, returnOnEnd: true}`. A landed reply in r → `shouldReturnAfterLanded` is false. Close → return effect.
- **Exits:**
  - team change → idle + stopAll;
  - path change → idle + stopAll;
  - close with `never` → idle + release;
  - plugin panel closed → idle + release;
  - mobile while away → idle + release;
  - mobile while docked plus a thread → idle.
- **Expansion:** `SET_RHS_EXPANDED false` then `SELECT_POST` (mouse) → `expanded: false`; `SELECT_POST` alone (keyboard) → `expanded: true`.
- **Latch:** kept across thread → search → thread, and across cause suppressed → arm.
- **miniLayout:** picture, bar and hidden at the boundary widths (picture min 240, bar min 240, `W ≥ w + 32`, `H ≥ 108`); the RHS overlap subtraction; all four corners.

There is no JS test runner in the project today (webapp/package.json has only build and dev scripts). This script is the whole of the new test infrastructure.

### 13.2 Acceptance criteria (lab first, then a real server)

| ID | Criterion |
|---|---|
| AC-1 | VillaNET playing; Reply, type, Enter. VillaNET is back 1.5 s ± 0.2 s after the real post replaces the placeholder. `plays` unchanged; the mock counter keeps counting. Real server: no new RTCPeerConnection in `chrome://webrtc-internals`, `pliCount`/`firCount` flat, `framesDecoded` continuous, no new `/api/v1/whep` request. |
| AC-2 | Thread X: VillaNET is back with no empty-RHS frame (a screenshot right after the click shows VillaNET); no reconnect. |
| AC-3 | Ctrl+. on the thread gives AC-2. A second Ctrl+. closes VillaNET, and `live` is 0 within 1 s. |
| AC-4 | Each of these during the 1.5 s cancels the return: typing in the reply box, clicking in the thread, scrolling the thread with the wheel, pressing an arrow key in the thread. The next send returns. |
| AC-5 | No return after the reply, but the close returns, in each case: text or an attachment left in the reply box; an edit box open; the emoji picker open; a post's "…" menu open; focus on a post in the thread. |
| AC-6 | Reply fails: no return. Retry succeeds: return. |
| AC-7 | "Reply from another device": no return. |
| AC-8 | Thread opened while VillaNET is closed: nothing happens. |
| AC-9 | Search, mentions, pinned, saved, files, info, members, edit history: the close returns and a reply does not. VillaNET's own "Open in Mattermost search" followed by close returns. |
| AC-10 | Another plugin: no return. The mini-player stays while that panel shows. Closing that panel removes the mini-player, and `live` is 0 by 60 s ± 1 s. |
| AC-11 | Threads page: no dispatch while suppressed, and the mini-player is hidden. Leaving within 4 min docks with no reconnect of the latched stream. |
| AC-12 | Team switch during a thread: no return; the mini-player goes; `live` is 0 within 1 s; the watch list is kept. |
| AC-13 | Phone-sized: no return, no mini-player, `live` reaches 0 by 60 s ± 1 s. |
| AC-14 | 11.3+: RHS, thread and help popouts never show the mini-player and never arm. The main window passes AC-1. |
| AC-15 | Settings: `never` gives no returns, and the mini-player goes on close. `close` gives no reply-returns. Mini `off` gives no mini-player and `live` 0 by 60 s after displacement. |
| AC-16 | At every sample, at most one connected, unmuted `<video>` in the page, including slow-to-tune handoffs. |
| AC-17 | Sessions never exceed 4. Non-latched streams: `live` drops to 1 by 60 s ± 1 s after displacement. Mini-player hidden while away: the latched stream ends by 5 min ± 1 s. Tile closed: its session stops immediately. Plugin disabled while the mini-player shows: no peer connection remains and no `vntv-` body class remains. |
| AC-18 | The mini-player never overlaps `#channel-header`, `#post-create` or `#sidebar-right`, including right after a channel switch to a channel with a bookmarks bar or banner. It switches picture → bar → hidden at the §6.3 widths. |
| AC-19 | Ctrl/Cmd+F6 (F6 in the desktop app) reaches the mini-player, announced as the region "VillaNET player". Tab order is as in §6.4, with Switch off last. Move keys cycle and jump. Its appearance never moves focus out of `#reply_textbox`. |
| AC-20 | Reduced motion: no drain line, no transitions. |
| AC-21 | After an R1 return the strip shows. It is still there after 6 s, and stays while hovered or focused. Back to thread reopens the thread; the next reply does not auto-return; closing returns. |
| AC-22 | Real server, real key presses, on 9.11, 10.11 and 11.x. After R1 with focus in the reply box: focus is on the VillaNET panel, not in `#post_textbox` and not on the Top Streamers button. One Tab reaches Back to thread. No stale focus ring is left on VillaNET's header. After R2 via Ctrl+. or X: focus is where it was before the thread opened. |
| AC-23 | Keyboard-opened thread (R in the dot menu, Shift+Up) from an expanded VillaNET: the reply-return stays expanded, and the close-return in the same channel re-expands. A mouse Reply comes back collapsed. The lab models the collapse on outside click. |
| AC-24 | A stream ends while away. The mini-player keeps showing it as "Waiting for {name}", even if it had the sound. If it returns within 45 s, the mini-player renegotiates and plays. After 45 s, the next watched stream shows if it is still connected; otherwise the mini-player goes. |
| AC-25 | Stills keep arriving from the mini-player (`/still` uploads continue while away), including with "Freeze pushes" on for more than 2 minutes. |
| AC-26 | `make check` (tsc on React 17, check-styles, check-react, check-version, check-away) is green. The lab shows no Crashed box. The console has no errors. |
| AC-27 | Real browsers: sound continues on handoff in Chrome, Firefox, Safari and the desktop app. Where it is refused, "Turn sound on" shows and works in one click, and the stream never shows "Playback keeps stopping" while blocked (including across resolution changes). |
| AC-28 | Two watched streams with the sound on B: pressing Mute in the mini-player leaves the picture on B and makes no new WHEP request. |
| AC-29 | Five quick thread open/close cycles in 60 s, with "Delay mini mount by one frame" on: no "Playback keeps stopping". |
| AC-30 | 11.8+ channel popout: R1 and R2 work there; no mini-player; closing VillaNET there leaves no peer connection within 1 s. |
| AC-31 | VillaNET closed while a watched stream is offline for 2 min, then reopened: the stream is still in the watch list (as today). |
| AC-32 | Hide: the mini-player goes for the rest of the detour, the watch list is kept, `live` is 0 by 60 s unless VillaNET comes back first, and auto-return still happens. |
| AC-33 | A watched stream goes offline while docked: its connection stops at once (`stops` +1), and it renegotiates when it comes back, as today. |
| AC-34 | A reply-return or close-return never shows the boot sweep, on legacy and concurrent roots (the lab uses `ReactDOM.render`; check 11.11 with `window.enableConcurrentReact = true` if available). |

---

## 14. Build order

1. **Pool, no visible change.**
   - `sessions.ts` (with the detach-aware pause, the `blocked` rule, hold-expiry grace and `sweepMissing`), the Player slot with its "no session" state, `installReconciler`, `clearWatching`, `onWatchingChange`, `pollWhileShown`, the `uninitialize` teardown.
   - A minimal docked → idle detector in `away.ts` (`stopAll` on close), installed in the main window **and channel popouts**.
   - Lab counters.
   - Gate: AC-16, AC-17 (tile and disable parts), AC-25, AC-29, AC-31, AC-33; tile close, Retry and fullscreen regressions.
2. **Auto-return.**
   - `awayLogic.ts`, `away.ts` (state machine, engagement, focus guard, popout kinds, `cameBack`), `check-away.mjs`, the lab Mattermost simulation and chips.
   - Gate: AC-1 to AC-13 without the mini-player, AC-22, AC-26, AC-30, AC-34.
3. **Mini-player.**
   - `MiniPlayer.tsx`, `miniLayout.ts`, latching, holds, Global/Root registration, Hud `powerLast`, CSS, the settings schema.
   - Gate: AC-14 to AC-20, AC-24, AC-27, AC-28, AC-32.
4. **Finish.**
   - Undo strip, expanded restore, announcements, drain line, drag, file-drag pass-through.
   - Gate: AC-21, AC-23.
5. **Release.**
   - README; version 2.11.0 in `plugin.json` and `server/plugin.go:21`; `make dist`.
   - The real-server matrix: 11.x as run by the owner; 9.11 and 10.11 if available; an 11.8+ server for channel popouts if available.

**Cut line if time runs short:** drag (keep the Move button) and the drain line. Glide and picture-in-picture are already out.

---

## 15. README section (draft, goes after "Watching more than one stream", currently README.md:1531)

> ## When a thread takes the panel
>
> Replying to a message opens Mattermost's thread in the side panel, in VillaNET's place, and Mattermost gives a thread no way back to a plugin — it forgets which plugin was there. Turning Collapsed Reply Threads off does not change that: replies open in the side panel either way.
>
> So VillaNET remembers for it. **Once your reply is posted, about a second and a half later, the thread closes and VillaNET is back**, still playing. **Closing the thread brings it back straight away**, and so does closing search, mentions, saved or pinned messages, files or channel info if one of those took its place. If you are still busy in the thread — typing, scrolling, a menu or the emoji picker open, or a draft left in the box — it waits for your next reply or for you to close the thread. After it comes back, a **Back to thread** button sits at the top of the panel; the keyboard lands on the panel itself, one Tab away from it, and never in the message box.
>
> **While the thread is open, the stream keeps going in a small player in a corner of the channel** — the stream that was in front, with its speaker, a button that hides the player, a power switch, and a VillaNET button that brings the panel back. Drag it, or use its corner button, to move it; it remembers the corner. Where there is no room it shrinks to a bar, and on a phone-sized window it steps aside. Going back into the panel does not reconnect anything: no tuning, no burst of keyframes asked of the streamer, no hit against the playback limit.
>
> Closing VillaNET yourself, with its icon or Ctrl+., still switches it off, as it always has, and so does changing team. The small player only appears while something else is in the side panel, never after you closed VillaNET. Another plugin's panel is left alone: VillaNET does not come back over it.
>
> Streams you are not looking at are let go after a minute; the one in the small player is kept for up to four minutes while the player is out of sight. Both behaviours can be switched off per person in **Settings › Plugin preferences › VillaNET** (Mattermost 9.11 and later; before that they are simply on). In a popped-out channel window (Mattermost 11.8 and later) VillaNET comes back the same way, without the small player.

**Also:**
- **"The lab":** add a paragraph on the Side panel chips and the plays/stops/live counters.
- **"Notes and limitations":**
  - this relies on Mattermost internals unchanged from 9.0 to master; if they change, VillaNET falls back to today's behaviour;
  - popped-out side-panel windows are unaffected;
  - streams kept connected out of sight still count as viewers, and still show the viewer's face on Live Now cards, for at most the times above.

**Release notes 2.11.0:**
- VillaNET comes back after you reply in a thread or close it.
- The stream keeps playing in a small corner player while something else has the side panel.
- Back into the panel without reconnecting.
- Two new personal settings.

---

## 16. Risks

1. **Mattermost internals.** The `views.rhs` shape, action names and DOM ids were checked unchanged from 9.0.0 to master. A future change would make the controller inert (safe) or mis-classify a panel. The table tests in `check-away.mjs` pin the assumptions.
2. **`registerGlobalComponent` is "INTERNAL".** Fallback: a Root component that renders only in the main window; otherwise no mini-player.
3. **Autoplay on re-insert.** A removal-paused element has lost its autoplay flag (§1.8), so `mountVideo` calls `playSafely`. A refusal shows as "Turn sound on". Safari and the desktop app need checking (AC-27).
4. **Invisible decode while held.** It costs bandwidth, viewer counts and faces: 60 s for non-latched streams, ≤ 5 min per hidden spell for the latched one. It is bounded and can be switched off.
5. **Decode without an element, and no keyframe request on reattach,** were measured only on loopback VP8 in Chromium. They must be confirmed against Broadcast Box with OBS (H.264) in Firefox, Safari and the desktop app (AC-1).
6. **Focus.** Mattermost 10.5+ moves focus in two timer hops and reopens steal the close restore. That is **confirmed in source** (§1.5) and mitigated by the focus guard (§3.7). Verify on 9.11, 10.11 and 11.x with real keys (AC-22). A future change to Mattermost's focus timing could leave focus in VillaNET's header, which is today's behaviour on manual opens.
7. **The F6 position** (sort order 4) is relative to the RHS's 3. The left sidebar's 6 is assumed stable.
8. **Engagement heuristics** may occasionally keep a thread open that the person had finished with. In that case the next reply or a close still returns, and the failure mode is today's behaviour.
9. **Not done:** riding Broadcast Box's publisher reconnect. Broadcast Box keeps WHEP sessions across a new publisher (media/bb/session_session.go:55-92, 132-139, 241-252). Today's stop-and-renegotiate is kept for its stall watchdog.

---

## 17. Owner open questions, with the defaults used

| # | Question | Default chosen |
|---|---|---|
| 1 | How soon after a reply should VillaNET come back? | 1.5 s after the server confirms it, unless you are still busy in the thread |
| 2 | Also come back after closing search, mentions, pinned, saved, files or channel info? | Yes; on close only, never on a reply |
| 3 | Come back over another plugin's panel? | No; the mini-player stays only while that panel shows |
| 4 | Mini-player on by default, and where? | On, top-right of the message list, movable |
| 5 | One picture or all of them? | One (the stream in front when VillaNET was pushed aside); others shown as "+N" and let go after a minute |
| 6 | How long to keep the pictured stream connected while the mini-player is out of sight? | 4 min hold plus 1 min grace |
| 7 | Phone-sized windows? | No auto-return and no mini-player |
| 8 | Does closing VillaNET with its icon, or changing team, still switch it off? | Yes, immediately, also while a thread has the panel |
| 9 | Server-wide admin default for the two settings? | No; per person only |
| 10 | Picture-in-picture? | Not in 2.11.0 |
| 11 | Come back even after the stream was switched off from the mini-player? | Yes (it is the panel coming back, not the stream) |
| 12 | "Back to thread" after an automatic return? | Yes; it stays until you move on, at least 30 s, and never vanishes while you point at it or are on it |
| 13 | Where should the keyboard land after an automatic return? | On the VillaNET panel, never in the message box |
| 14 | A Hide button on the mini-player that keeps your streams? | Yes |
| 15 | Ship in 2.11.0 (after Top Watchers in 2.10.0)? | Yes |

---

## 18. What the owner can be told today

- v11 does not force Collapsed Reply Threads. System Console › Site Configuration › Posts › Threaded Discussions still has Disabled (admin_definition.tsx v11:2919-2978). People only lose their own toggle when it is set to Always On (user_settings/display/index.ts v11:84).
- Turning it off would not stop Reply from replacing VillaNET (post_component.tsx v11:382-389; sidebar_right.tsx v11:282-296).
- Settings › Display › "Click to open threads" stops threads opening from a stray click on a message.
- Until 2.11.0 ships:
  - clicking the VillaNET icon while a thread is open brings VillaNET straight back (rhs.ts v11:310-322), though the streams reconnect;
  - on 11.3+, popping VillaNET out into its own window avoids the problem entirely.

---

## Appendix A. How each review problem was resolved

| ID | Decision | Reason (one line) |
|---|---|---|
| F1-noReplyFor-lost-on-arm | Accept | `backToThread` now leaves a 2 s hint that `arm()` consumes, giving `noReplyFor = root` (§3.3-3.4, check-away row, R19). |
| F2-focus-timer-order-wrong | Accept | Confirmed in sidebar_right.tsx v11:158-188 and a11y_utils.ts v11:46-61. Replaced by an order-independent focusin guard for both R1 and R2 (§1.5, §3.7, Risk 6). |
| F3-return-yanks-engaged-thread | Accept | Focus never goes to `#post_textbox`. Pointer/wheel/key engagement in the RHS cancels; focus in a popup, dialog or the thread blocks the return at fire time (§3.5, §1.7). |
| F4-away-never-ends | Accept | New exit rows: team/product → idle + stopAll; any other end without a return → idle + release. The mini-player shows only while P is thread, own or plugin (§3.4 rows 13-16, §6.1, R10/R14/R17). |
| F5-expanded-captured-after-collapse | Adapt | Mattermost's collapse on outside click is treated as the person's action; expansion is restored only for keyboard-opened detours, and the lab models the collapse (§1.6, §3.4 note, AC-23). |
| F6-detach-pause-spends-resume-budget | Accept | Confirmed by the HTML standard (media/html_media.html:2540-2560, 2760-2772). `detached`/`detachedAt` make the pause handler ignore detach pauses; there is a lab delay chip and AC-29. |
| F7-waiting-stop-never-fires | Accept | `sweepMissing` runs from the snapshot subscription after every snapshot, and the mini-player reads `useSnapshot` (§4.4, §4.7, §6.2, AC-33). |
| F8-popout-guard-too-broad | Accept | Confirmed: the channel popout renders SidebarRight from 11.8. Three window kinds; channel popouts run the controller without a mini host, so return and stopAll work (§1.4, §3.8, R15, AC-30). |
| P1-back-to-thread-rearms-reply-return | Accept (merged with F1) | Same defect and same fix: hint consumed by `arm()`, plus a check-away row. |
| P2-expanded-captured-after-mattermost-collapses | Adapt (merged with F5) | The time-window restore was rejected: re-expanding over a centre the person just clicked fights Mattermost's own rule. The lab now models the collapse so AC-23 tests the real sequence. |
| P3-no-snapshot-poll-while-away-kills-stills | Accept | Confirmed (stills.ts:150, poller.go push-on-change). The poll moves to a counted `pollWhileShown()` in store.ts, used by Sidebar and the mini-player (§4.7, AC-25). |
| P4-reconcile-silent-so-waiting-rule-never-fires | Accept | Stop on missing via `sweepMissing` (today's behaviour). The create gate uses snapshot presence, not `waitingFor`. A no-session Player shows Retry. Riding Broadcast Box's reconnect is noted, not done (Risk 9). |
| P5-mini-shown-stream-follows-audio-intent | Accept | The mini-player latches its stream at displacement and changes it only when that stream leaves the watch list (§6.2, AC-28). |
| P6-away-has-no-exit-zombie-mini | Accept | Exit rows as in F4. The mini-player adopts only; it creates a session only to resume a stream whose Waiting plate it was showing. A Hide control keeps the watch list (§6.1-6.4, R18, AC-32). |
| P7-applyAudio-undoes-blocked | Accept | `want` includes `!blocked`; only `takeSound` or the sound moving clears it; the backoff resumes through `playSafely` (§4.5, AC-27). |
| P8-dockedFromAway-flag-read-before-set | Adapt | The one-shot flag is replaced by `cameBack()` (phase still away, or docked from away < 1 s), which is correct under either render order. Lab subscription order matches Mattermost's (§5, §11, AC-34). |
| P9-close-return-steals-focus | Accept (merged with F2) | The close-return captures Mattermost's restored element (or the element focused at displacement) and the focus guard restores it (§3.6-3.7, AC-22). |
| P10-mini-tab-order-contradicts-hud-dom | Accept | Hud gets `powerLast`; Back comes first in the DOM with CSS `order`; `role='region'`; AC says Ctrl/Cmd+F6 (F6 in the desktop app) (§6.4, AC-19). |
| P11-undo-strip-timeout-unreachable-by-keyboard | Accept | The strip lasts ≥ 30 s, never times out while hovered or focused, is one Tab from the focused panel, and is announced via `role='status'` (§7, AC-21). |
| P12-popout-guard-drops-deliberate-close-stop | Accept (merged with F8) | The detector runs in channel popouts from build step 1. Other popouts have no RHS, and closing the window ends their sessions. |
| P13-reconciler-while-idle-loses-watch-list | Accept | `reconcile` runs from the reconciler only while away.ts is not idle. The release-note line was removed (§4.7, AC-31). |
| P14-invisible-holds-cost-and-faces | Accept | Only the latched stream is held; others get 60 s. Faces, viewers and watch time are documented (§4.6, §15, Risk 4). |
| P15-hold-expiry-does-not-schedule-grace | Accept | Hold expiry runs exactly the release code (`maybeGrace`), and the bounds are checked in AC-17 (§4.4). |
| P16-post-list-swapped-without-away-notification | Accept | Confirmed (channel_view.tsx v11:37-48; post_view.tsx v11:84, 92). Re-measure on `channelId` change, plus a rAF-throttled MutationObserver that re-attaches to a replaced `#post-list` (§6.3, AC-18). |
| X1 (not raised) version | Change | `plugin.json:8` and `server/plugin.go:21` are already 2.10.0 (Top Watchers), and the owner moved this feature to 2.11.0. |
| X2 (not raised) stale project line numbers | Change | Sidebar, styles and README lines moved under concurrent edits. They were re-read on 2026-10-01, and symbols are named beside them. |

---

## Appendix B. Sources

**Scratchpad:** `/tmp/claude-1000/-home-user-Documents-Broadcast-Box-Plugin/f246a2f2-7be9-4da0-8cf3-a9bade7355be/scratchpad/rhs/`
- `v9.0.0/`, `v9.11.0/`, `v10.5.0/`, `v10.11.0/`, `v11.0.1/`, `v11.11.0/`, `master/`, `ux/` (per-version channel_view, post_view, sidebar_right, a11y_controller), `rv/` (a11y_utils and a11y_controller v11, post_view, root, package.json), `rev/` (entry.tsx v11.0.1/v11.11.0/master, package.json v11.0.1), `spec/`.
- `media/`:
  - `html_media.html`, the HTML standard media section;
  - `popout_controller_v11_{2,3,6,8,11}_0.tsx`;
  - `bb/`, the Broadcast Box session code.
- `rev2/`: `menu_v11.0.1.tsx`, `emoji_picker_tabs` `ept_v11.0.1.tsx`, `generic_modal` `gm_v11.0.1.tsx`, `emoji_picker` `ep_v11.0.1.tsx`, fetched for this revision.

**Project:** files cited are under `/home/user/Documents/Broadcast Box Plugin/`. They were read on 2026-10-01; nothing in the project was edited.