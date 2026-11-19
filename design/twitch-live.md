# VillaNET Twitch live: build contract for 3.1.0 (revision 2)

Revision 2 (2026-10-02) applies the security and feasibility review. Every change is listed in the
**Review log** at the end, with the evidence that was checked.

Written 2026-10-02 for Victor's request of that day:

> Add the ability for users to link their Twitch accounts so if they go live on Twitch, it'll show
> live on the Mattermost plugin -- I think I can use the same Twitch API access to make that happen.

His answers to the follow-up questions are binding, and this contract is built on them:

- **Linking.** "Connect with Twitch": the person signs in on Twitch and approves it (the OAuth
  authorization code flow). That proves the channel is theirs, the way Link Your Account proves a
  Broadcast Box stream. No permission beyond identity is asked for. The admin adds VillaNET's callback
  URL to the Twitch application once.
- **Display.** The red LIVE badge by their name, a Live Now card (title, game, viewers, preview
  picture), AND the Twitch stream playing inside VillaNET in Twitch's embedded player. He accepted that
  viewers' browsers then load Twitch directly. That breaks the plugin's "browsers never contact anyone
  but this server" promise, so the change is stated honestly everywhere and sits behind its own admin
  switch, off by default.
- **Airtime.** Time live on Twitch counts toward the airtime boards and medals (all time, monthly, and
  past-month medals), merged with that person's Broadcast Box time.
- **Credentials.** The same Twitch developer application as the IGDB game lookups.

**How this relates to the other contracts.**

- `design/media-lookups.md` **Part A** (the shared Twitch application: the `twitch` section in
  plugin.json, `TwitchClientID`, `TwitchClientSecret`, and `server/twitch_app.go` with `appToken` and
  `invalidate`) is a dependency. It is reused unchanged. §3 lists the only additions.
- `design/media-lookups.md` **Part E** ("Twitch live, Phase 2") and its Questions 8 to 11 are
  **replaced by this document**. Victor answered those questions differently: playback inside the
  panel, and airtime that counts. Part E is not built. Where Part E made a choice this document keeps,
  the same name is used, so that nothing an admin has already been told changes. The main one is the
  callback address that Part A's help text already gives: `/plugins/ceo.villa.villanet/api/v1/twitch/callback`.
  Part E §D.4 says Phase 2 is built in the IGDB sandbox. It is not: §12 schedules it in the main tree.
- `design/clips-spec.md` with `design/clips-addendum.md` (3.0.0) is being built now in the main tree.
  This feature touches the same core files and is **built after Clips lands** (§12). §9 lists what
  Clips must refuse for Twitch streams.
- `design/lock-screen.md` (built into 3.0.0 on 2026-10-05) **locks the panel**. Nobody uses it until
  they unlock it with their own Broadcast Box bearer token (which is linking), or until a system admin
  lets them in to watch only. Victor answered on 2026-10-05 that **connecting Twitch is for linked
  people only**. That supersedes decision 13's "anyone signed in", which predates the lock. The
  amendments below are binding, and lock-screen §9.2 is their source.

> **Amended 2026-10-05 by `design/lock-screen.md` (binding for 3.1.0).**
> - **A Twitch connection never unlocks the panel and never lets anybody in.** Decision 11 and §4.8
>   gain "nor does it unlock the panel or let anybody in". A Twitch-only streamer is locked, like
>   anybody else with no bearer token. In code, "linked" is `linkOf` (both link entries agree,
>   `server/links.go`) and, on the webapp, `access === 'linked'` from `/me`, no longer
>   `linkedKey(u) != ""` or `Boolean(me.streamKey)`.
> - **Connect is linked-only** (decision 13, lock-screen Q7). This supersedes the earlier answer that
>   anyone signed in may connect. A let-in person cannot connect either: a connection makes them a
>   streamer whose Twitch live everybody sees, which is more than watching. A Twitch-only streamer
>   now needs a Broadcast Box token.
> - **Route classes** for the §4.1 routes, under the front gate (`routeClasses`, `server/access.go`,
>   where a comment already records this):
>
>   | Route | Class |
>   |---|---|
>   | `POST connect-ticket`, `GET connect` | **linked**. `connect` sits above the user-id check, so it runs `accessOf` itself, after its own session checks (§4.2). |
>   | `GET callback` | Open as a route, **but** after its session checks it runs `accessOf` with `connect`'s class. A state stays good for 10 minutes, so a let-out or an unlink inside that window must still stop the connection. A caller no longer allowed gets the "not allowed" page, and the state is thrown away. |
>   | `DELETE link`, `POST notice` | open (takebacks) |
>   | `GET preview` | watch (linked or let in) |
>   | `/admin/twitch*` | admin (skips the gate; `isSystemAdmin` first, as every admin route) |
>
>   `connect` and `callback` are matched above the gate, so the lock's `TestEveryRouteHasAnAccessClass`
>   (T1) must gain them on its allow-list, and nothing else.
> - **Disconnect stays open to everybody** (decision 10), the locked included. The lock screen shows a
>   small **Disconnect Twitch** text key ~~under its JUST WATCHING? row~~ when `/me` says the person is
>   connected, because Twitch's agreement requires deletion on request (DSA §VI.F). It comes after
>   Unlock in the lock's tab order. *Not built in 3.0.0: there is nothing to disconnect yet.* *Since
>   2026-10-06 (the simpler lock, `lock-screen.md`'s amendment of that day) it sits on a line of its own
>   under the lock's docked bar, or under the not-set-up sentence when there is no bar; after a press
>   by keyboard, focus goes to its words, never to the page's body, there and in the let-in footer.*
> - **`TwitchConnect` (§4.9)** renders for linked people only, in the linked footer bar's idiom (the
>   tidy-up of 2026-10-05 made the footer one bar whose tools open sheets, one at a time). It never
>   appears in the let-in footer's WATCH ONLY line, and never on the lock screen except as Disconnect.
>   The `.vntv-link-cta-row` and `.vntv-title-block` places named in §4.9 no longer exist.
> - **§6.** Twitch tiles exist only in an unlocked panel, so nothing more is needed there. A locked
>   reader never has tiles or the mini-player.
> - **Words.** The help text in §2.1 and the README text in §10.2 ("Anyone signed in can connect")
>   become "Anybody who has linked their stream can connect". "Link Your Account" is now "unlock
>   VillaNET with your bearer token" (see the README's "Unlocking VillaNET").
>
> *As built (2026-10-06, integration).* Somebody **let in** who still has a Twitch account connected
> (they connected while linked, and were unlinked since) gets the lock screen's small **Disconnect
> Twitch** key on a line of its own over the WATCH ONLY bar, never inside it and never Connect, so
> "Disconnect stays open to everybody" holds for them too. The Clips row is gone (removed 2026-10-05
> at Victor's request), so nothing here about it applies. The airtime card's §7.12 line is built.
>
> *As built (2026-10-06, fixes after review).* Where the build now differs from the text below:
> - **§5.2 "the first read".** "Skip, never guess" is kept for a KV error, which passes. A link
>   document that will not decode stays that way until Delete all, and Twitch credits nobody
>   meanwhile, so it reads as nobody connected: Broadcast Box is paid in full with no paid spans, and
>   the boards are Broadcast Box's alone (decision 9, §7.4). When it decodes again, the Twitch resync
>   epoch is armed, so the stretch Broadcast Box paid alone is never paid again by Twitch.
> - **§4.6 Delete all, step 2.** `twitch_links` is rewritten by compare-and-set, so a block saved at
>   the same moment is kept; a read the store refuses writes nothing (the block list is never lost).
> - **§6.4.** The panel's body keeps its scrollbar's room (`scrollbar-gutter: stable`): a classic
>   scrollbar wider than the 8 px hysteresis (Firefox's) made the tile flip between the player and the
>   plate as the body started and stopped scrolling. The stage is that much narrower everywhere.
> - **§6.6.** The theatre's Tab trap counts Twitch's frame, and a stop after the card hands Tab out of
>   the frame back to the top of the card.
> - **§4.9.** The sheet's own Disconnect keeps the sheet, and the tool, until its words have had their
>   six seconds, even when `/me` then gives the tool nothing to show; focus that loses its button to
>   a phase change goes to the sheet's words.

> **Amended 2026-10-06: Victor's decisions on the footer (binding for 3.1.0).** He answered the two
> Twitch items left open by the build: "Icon would be better -- I see the icon in other
> apps/plugins without issue", and "Takeover notice only to linked people."
>
> - **The footer bar's Twitch tool shows Twitch's Glitch instead of the word.** At a 304 px panel the
>   bar's five tools left the stream title "Arm…", and the word was the widest of them (39 px there,
>   55 px from 360 px of bar). The Glitch takes the same 34 px square as the tools beside it. This
>   supersedes, **for the footer tool only**, §1.3's trademark bullet ("no Twitch logo anywhere") and
>   review-log #27's "Not taken: a Twitch icon button". Measured in the lab with Victor's title,
>   "Arma most nights #arma3": at 304 px the title's room went from 44 to 49 px ("Arm…" to "Arma …",
>   three letters to four); at 400 px from 124 to 145 px (17 characters to 20).
> - **Everything else keeps the plain word "Twitch"**: the card's TWITCH tag, **Watch on Twitch ↗**,
>   **Also on Twitch ↗**, the tiles, the theatre and the popovers. The Developer Services Agreement
>   wants the word and a clear path back to Twitch on what is shown (§1.3, §VII), and those are where
>   Twitch's streams are shown. No Twitch purple anywhere.
> - **Where the Glitch comes from.** Simple Icons 16.34.0, `icons/twitch.svg` (CC0; its source is
>   brand.twitch.tv, brand colour `#9146FF`), `viewBox="0 0 24 24"`, and this path, unchanged:
>   `M11.571 4.714h1.715v5.143H11.57zm4.715 0H18v5.143h-1.714zM6 0L1.714 4.286v15.428h5.143V24l4.286-4.286h3.428L22.286 12V0zm14.571 11.143l-3.428 3.428h-3.429l-3 3v-3H6.857V1.714h13.714Z`.
>   It is `TwitchIcon` in `webapp/src/components/icons.tsx`, inline SVG in `main.js`, so showing it
>   fetches nothing from anywhere and `LOCKDOWN.md` is unchanged.
> - **Rules of use** (Twitch's brand rules, as this decision reads them). The path is never altered:
>   no reshaping, no outline, no rotation, no added shapes. It is scaled only as a whole (the tool
>   draws it at 14 px: the Glitch fills its square edge to edge, where the stroked icons beside it,
>   drawn at 16 and 17 px, leave a margin, so 14 px gives it their height and top line). It is one
>   solid colour, `fill="currentColor"`, in the bar's own grey like the other tools, white while
>   pointed at or open; Twitch's purple would be allowed, but only if the bar gave its tools their own
>   accents, which it does not. Nothing is drawn over it: the dot a connected account brings sits in
>   the button's own top right corner, clear of the mark by about 1.2 px, rather than on the icon as
>   the scissors' dot does, because the Glitch fills its square right out to that corner. Like the
>   scissors' dot it keeps inside the button's rounded corner, so on hover or with the sheet open its
>   ring sits on the lit square instead of biting a notch out of it. `build/check-twitch.mjs` holds
>   the path to the letter, the svg to those attributes, `TwitchTool` to drawing `TwitchIcon` and no
>   word, every tooltip to starting "Twitch: ", and the dot to its clearance and to the rounded
>   corner.
> - **The tool's name and tooltip.** The name (`aria-label`) is as before: "Connect Twitch",
>   "Twitch: connected as {name}" (", live now" while live) or "Twitch: not available". The tooltip
>   says Twitch and then where things stand, as the other tools' do: "Twitch: not connected. Connect
>   your Twitch account so you show as live here when you stream on Twitch.", "Twitch: connected as
>   {name} (twitch.tv/{login})" (" · live now" while live), or "Twitch: not available on this server.
>   Click to see why."
> - **The takeover notice stays shown to linked people only**, as built: over the linked footer bar
>   (§4.9, "Taken over"). Somebody let in or locked whose account was taken over is not told in the
>   panel; the account has already left them, so there is nothing for them to disconnect. The server
>   keeps the notice for up to 30 days (`twitchLostKeepMS`), so somebody who unlocks within that
>   time usually sees it then. It goes sooner when the taken channel's Twitch data is purged (§4.6,
>   step 5): when the account is disconnected from its new owner (by them, by an admin removing or
>   blocking them, by their connecting another account, or by the daily check finding the account
>   gone), or on Delete all. It also goes when they connect again or press **OK**, and it is not
>   shown while they are blocked. No code change.

Precedence: Victor's answers, then this document, then everything else.

**Notation.**

- `B file:line` is the untouched 2.11.0 tree (`/home/vvilla/Documents/Broadcast Box Plugin.baseline-2.11.0`).
- `M file:line` is the main tree as it stood on 2026-10-02, with Clips partly built. Line numbers in
  `M` will move as Clips lands, so builders find each place by the symbol named beside it.
- `[MM v9 …]`, `[MM master …]` and `[desktop …]` cite Mattermost and Mattermost Desktop source (§1.2).
- `[TW …]` cites an official dev.twitch.tv page, `[DSA …]` the Twitch Developer Services Agreement,
  `[LIVE]` something observed against Twitch's servers on 2026-10-02, `[COMM]` a forum answer that is
  not official, and `UNVERIFIED` means no confirmation was found.
- Twitch's documentation is paraphrased with exact citations rather than quoted. Identifiers,
  parameter names and limits are given exactly.

---

## 0. Decisions in one screen

| # | Question | Decision | Why (section) |
|---|---|---|---|
| 1 | How does a person prove a Twitch channel is theirs? | OAuth authorization code flow with `scope=openid`, `force_verify=true` and a `nonce`. The Connect window can only be started with a single-use **connect ticket** that the panel gets by a CSRF-protected POST, so a link, an embedded image or another site cannot start it. `state` is random, single use, kept on the server for 10 minutes and bound to the Mattermost user, and the callback also requires that same user's interactive session (not a bot, token or OAuth-app session). The code is exchanged on the server. The returned ID token's `nonce` must match the state's, which ties the code to the state it was issued for. Helix Get Users with the new token says who it belongs to (and must agree with the ID token's `sub`), and the token is revoked straight away. | Victor's answer; Twitch's docs; the account-linking CSRF attack (§4) |
| 2 | What is kept? | Per person: the Twitch user id, login, display name and when they connected. **Never** a token, refresh token, code or e-mail. Plus their Twitch airtime (decision 9). | Minimum data; Twitch's terms (§4.5) |
| 3 | How does the Connect window open? | Two presses. The first shows what connecting means and fetches the ticket; the second (**Continue on Twitch**) calls `window.open(…, 'noopener')` on a **plugin** route (`/api/v1/twitch/connect?t=<ticket>`), which redirects to Twitch. In the desktop app that opens an in-app popup that shares the Mattermost session, so no code-typing fallback is needed (unlike media-lookups Part E). The panel always goes to Waiting, because `window.open` returns null both with `noopener` and in the desktop app. | [desktop webContentEvents.ts] (§4.2, §4.9) |
| 4 | How is "live" detected? | Helix Get Streams with the shared app token, up to 100 `user_id` per call, once a minute, only while the switch is on and somebody is connected. No EventSub. | Cards need title, game, viewers and picture, which only Get Streams gives (§5.1) |
| 5 | How does a Twitch stream appear? | As an ordinary `apiStream` with `source: "twitch"`, an **empty** `StreamKey`, and a handle that is an HMAC over a domain-separated Twitch id. It is merged into the stream list on **all three** poll paths from a store that survives poller restarts. Broadcast Box polling never waits on Twitch. | The watch list, badge, popover and boards all work by handle and `userId` (§5.3, §5.4) |
| 6 | Live on Broadcast Box and Twitch at once? | One card in Live now, the Broadcast Box one, with an "Also on Twitch ↗" link. The Twitch entry stays in the snapshot marked `coveredBy`, so a Twitch tile somebody is watching keeps playing, and the cover only changes after the usual offline grace. One badge. Airtime counted once. | Same person, usually the same picture twice (§5.6, Q1) |
| 7 | Preview picture | Fetched by the **server** from `static-cdn.jtvnw.net` on demand, kept in memory for 2 minutes, relayed. Browsers never fetch Twitch pictures. It follows **Live preview pictures**. | Keeps the lockdown promise for everything except Connect and the player (§5.7) |
| 8 | Playback inside VillaNET | A plain `<iframe>` of `player.twitch.tv`, behind **Play Twitch streams inside VillaNET** (off by default). Twitch's JavaScript player is not used: Mattermost's page CSP blocks it, and the only way round that, a plugin-served wrapper page, would run Twitch's code with the viewer's Mattermost session (§6.1, Q6). The player plays inline when its slot is at least 400×300, which needs a panel about 440 px wide (Mattermost allows that only in windows 1201 px and wider). Otherwise watching opens a VillaNET overlay, the "theatre", in the same click. Sound moves by reloading the player with a different `muted`. Twitch streams never go into the mini-player, preview stills, faces, Top Snoopers or Clips. | §6 |
| 9 | Airtime | A separate, deletable **Twitch ledger** keyed by Twitch user id, in the same format as the Broadcast Box tables. Boards, card medals and past-month medals rank **people**: Broadcast Box rows plus Twitch rows per person. **Broadcast Box is authoritative**: it is always credited in full, exactly as in 2.11.0, and "paid spans" make the Twitch ledger give back any second Broadcast Box also paid, so a simulcast is counted once and a disconnect never removes Broadcast Box time. Time from before the person connected never counts. | Victor's answer; Twitch's deletion rule (§7) |
| 10 | Disconnecting | The link goes at once, and the person's Twitch hours are purged (queued and retried until done) by a KV-only worker that runs **whether or not the switch is on**. Disconnect is always offered to a connected person, on the lock screen too (amended 2026-10-05). Past-month medals are recalculated. | [DSA §VI.F, Schedule 1] deletion on revocation or request (§4.6, Q2) |
| 11 | What a Twitch connection is NOT | It is not the Broadcast Box "linked" status. It does not unlock planning, joining plans, What's on?, the Top Snoopers board, or anything in Clips, **nor does it unlock the panel or let anybody in** (amended 2026-10-05, lock screen). | Clips rules; Victor's linked-only rules are about Link Your Account (§4.8, Q4) |
| 12 | Release | 3.1.0, built in the main tree after Clips (3.0.0) and the IGDB Phase 1 patch have landed. | Same core files (§12) |
| 13 | Who may connect, and admin control | **Amended 2026-10-05: linked people only** (lock-screen Q7, Victor's answer), signed in interactively, unless an admin **blocks** them (Disconnect and block). An admin can mark a connected channel **24/7**: it shows tagged 24/7, gives no badge and earns no airtime, like an always-on stream key. An allow-list mode is a question for Victor (Q9). | Broadcast Box publishers are admin-provisioned; Twitch connections are not (§4.5, Q9) |

---

## 1. Evidence this rests on

Everything below was checked on 2026-10-02. Full page copies were saved in
`/tmp/claude-1000/-home-vvilla-Documents-Broadcast-Box-Plugin/7553f2d7-302c-4aee-b0c9-4cacc3c4eaa9/scratchpad/twitch/`.
**That folder can vanish**, so everything a builder needs is restated here.

### 1.1 Twitch

| Id | Fact | Source |
|---|---|---|
| T1 | **Authorize.** `GET https://id.twitch.tv/oauth2/authorize` with `client_id`, `redirect_uri` (must be a registered one), `response_type=code`, `scope` (required, space-separated, URL-encoded), `state` (optional but strongly encouraged against CSRF; random and unique per request; returned unchanged; a mismatch means ignore the response), and `force_verify` (makes Twitch ask again). Approval redirects with `?code=…&scope=…&state=…`. Refusal redirects with `?error=access_denied&error_description=…&state=…`. | [TW authentication/getting-tokens-oauth/#authorization-code-grant-flow]; local copy `authentication_getting-tokens-oauth.txt:180-235` |
| T2 | **Exchange.** `POST https://id.twitch.tv/oauth2/token`, form-encoded, with `client_id`, `client_secret`, `code`, `grant_type=authorization_code` and the identical `redirect_uri`. The answer has `access_token`, `refresh_token`, `expires_in`, `scope[]` and `token_type`, plus `id_token` when `openid` was asked for. The OIDC discovery document lists `token_endpoint_auth_methods_supported: ["client_secret_post"]`. | [TW same page :241-278]; [LIVE] `oauth2/.well-known/openid-configuration` |
| T3 | **Scope.** The docs mark `scope` required and say to ask only for what the app's calls need. The discovery document's `scopes_supported` is `["openid"]`, and Twitch's own Go OIDC sample asks for `openid` only. Whether an empty `scope=` is accepted is UNVERIFIED. **This build sends `scope=openid`.** | [TW authentication/scopes]; [DSA Schedule 1 §A]; [LIVE] |
| T4 | **Whose token.** Helix Get Users with a user token and no parameters returns that token's user (`id`, `login`, `display_name`). A token issued to another client fails with 401, because Helix checks that `Client-Id` matches the token. | [TW api/reference/#get-users]; [TW api/guide] |
| T5 | **Revoke.** `POST https://id.twitch.tv/oauth2/revoke`, form body `client_id` and `token`; 200 on success. Whether this also kills the refresh token, and whether the app then disappears from the person's Twitch Connections page, is UNVERIFIED. | [TW authentication/revoke-tokens] |
| T6 | **Sessions.** An app that *keeps* an OAuth session must validate it at start and hourly. VillaNET keeps none: the user token lives for the length of one request. | [TW authentication/validate-tokens] `:22-26` |
| T7 | **Redirect URIs** are a list in the developer console. Forum reports show an exact string match (path and trailing slash matter), and HTTPS is required except for `localhost`. | [TW authentication/register-app]; [COMM forum 6950, 42753, 38739, 30063] |
| T8 | **Get Streams.** `GET https://api.twitch.tv/helix/streams`. An app token is enough. Up to 100 `user_id` (repeat the parameter), `type=live`, `first` up to 100 (default 20). People who are not live are left out. The page warns that duplicates or missing entries can appear because the list moves. Fields used: `id`, `user_id`, `user_login`, `user_name`, `game_name` (empty if no category), `type`, `title`, `viewer_count`, `started_at` (RFC 3339 UTC), and `thumbnail_url` with a `{width}x{height}` placeholder. `is_mature` is deprecated and always false. | [TW api/reference/#get-streams]; `api_reference.txt:19394` on |
| T9 | **Get Users** takes up to 100 `id`, with an app token. IDs are opaque strings; logins can change, so everything is keyed on `id`. | [TW api/reference/#get-users]; [TW api/guide] |
| T10 | **Helix rules.** Send `Authorization: Bearer <token>` and `Client-Id`. The rate limit is a token bucket per Client ID, reported in `Ratelimit-Limit`, `Ratelimit-Remaining` and `Ratelimit-Reset` (epoch seconds). 429 means wait. A 401 means fetch a new token. Unknown fields must be ignored. | [TW api/guide#twitch-rate-limits]; [TW authentication] |
| T11 | **Embed requirements.** 1.1: the site uses SSL. 1.2: `parent` is required. 1.3: only Twitch-approved player elements, and nothing may obscure the player. 1.4: minimum sizes. Twitch may revoke embedding for a non-compliant domain. | [TW embed] `embed.txt:30-40` |
| T12 | **Iframe embed.** `https://player.twitch.tv/?channel=<login>&parent=<host>` with one `parent` per domain. `autoplay` defaults to true and needs the minimum size and visibility; mobile always needs a tap. `muted` sets only the *initial* state (default false). Width minimum 400, height minimum 300, `allowfullscreen`. The one quote: "Embedded video windows must be at least 400x300 pixels." | [TW embed/video-and-clips] `:18, :42-99` |
| T13 | **The JavaScript player** (`setMuted`, `setVolume`, the `PLAYING` and `PLAYBACK_BLOCKED` events) exists only through Twitch's script `player.twitch.tv/js/embed/v1.js`. | [TW embed/video-and-clips] `:143-366` |
| T14 | **How `parent` is enforced** [LIVE]: the player answers with `Content-Security-Policy: frame-ancestors https://<each parent>`. `parent=localhost` or `127.0.0.1` works on any scheme and port. An IP address gives `InvalidParent`. A port, a scheme or a wildcard in `parent` gives `InvalidCharInParent`. Twitch staff: no port can be given, and 443 is assumed for non-local hosts. So **a Mattermost served on a custom port, over http, or at an IP address can never embed Twitch.** | [LIVE]; [COMM staff, forum 25588] |
| T15 | **What Twitch's own script puts on its iframe**, a template for a hand-built one: `allow="autoplay; fullscreen"`, `allowfullscreen`, `scrolling="no"`, `frameborder="0"`, and `sandbox="allow-modals allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox"` (no top navigation). It also appends `referrer=<full page URL>`, which a hand-built iframe leaves out. | [TW-CODE player_v1.js] |
| T16 | **Hosts the player references**: `assets.twitch.tv`, `gql.twitch.tv`, `static-cdn.jtvnw.net`, `usher.ttvnw.net` and video segment hosts, `spade.twitch.tv` (analytics), `passport.twitch.tv`/`id.twitch.tv`, `edge.ads.twitch.tv`, Amazon advertising (`s.`, `c.` and `vaes.amazon-adsystem.com`), comScore (`sb.scorecardresearch.com`) and Nielsen (`secure-sts-prod.imrworldwide.com`). These are references in its code, not proof that each is contacted on every view. | [TW-CODE player.html and its 46 bundles] |
| T17 | **EventSub** `stream.online`/`stream.offline` carry no title, game, viewers or picture. Webhooks need a public HTTPS callback on 443. The WebSocket transport needs a user token. Conduits work with an app token but need shard management. | [TW eventsub pages] |
| T18 | **`nonce` and the ID token.** The OIDC authorization code flow takes an optional `nonce` on `/oauth2/authorize`, which Twitch strongly encourages against CSRF: random and unique per request, returned in the ID token's claims, and a mismatch means ignore the response. The token exchange then returns `id_token` beside the access token. Its default claims are `aud` (the client ID), `azp`, `exp`, `iat`, `iss` and `sub` (the user's id), plus `nonce` when one was sent. The discovery document gives `issuer: "https://id.twitch.tv/oauth2"`, and Twitch's own example token has a string `aud`. | [TW authentication/getting-tokens-oidc] `authentication_getting-tokens-oidc.txt:199-240, :281-330, :348-380, :440-450`; [LIVE] `oidc_discovery.json` |
| T19 | **Checking an ID token without its signature.** OpenID Connect Core §3.1.3.7: when the ID token is received directly from the token endpoint, TLS server validation may be used to validate the issuer instead of checking the signature. VillaNET receives it only that way. | [OIDC Core 1.0 §3.1.3.7] `adv/oidc-core.html:2594-2650` |
| T20 | **Twitch's code lifetime** (how long an unexchanged authorization code stays valid) is UNVERIFIED. The design no longer depends on it (§4.3). | — |

### 1.2 Mattermost and the desktop app

| Id | Fact | Source |
|---|---|---|
| M1 | The web app page's CSP is only `frame-ancestors …; script-src 'self' …`. There is no `frame-src`, `child-src` or `default-src`. So a cross-origin `<iframe>` is allowed, and a `<script>` from Twitch is blocked. The `<meta>` CSP from `webpack.config.js` `generateCSP()` is `script-src` only. Admins cannot add script hosts: `DeveloperFlags` only adds `unsafe-eval`/`unsafe-inline`, and `FrameAncestors` (10.11+) only extends `frame-ancestors`. | [MM v9 server/channels/web/handlers.go:241-252]; [MM master :253-262]; webpack.config.js |
| M2 | Every handler sends `Permissions-Policy: ""` (no restriction) and `Referrer-Policy: no-referrer`, and `root.html:8` adds `<meta name='referrer' content='no-referrer'>`. | [MM v9 handlers.go:231-233]; root.html |
| M3 | Plugin requests: the token comes from the Authorization header, else the `MMAUTHTOKEN` cookie, else `?access_token`. **A GET authenticated by cookie gets `Mattermost-User-Id` with no CSRF check.** A non-GET by cookie needs the CSRF token (the webapp's `baseHeaders()` already sends it). | [MM v9 server/channels/app/plugin_requests.go:136-210], re-read for this document |
| M4 | Before the plugin sees a request, Mattermost strips `Mattermost-User-Id`, `Mattermost-Plugin-ID`, the `MMAUTHTOKEN` cookie, the `Referer` header and the `access_token` query parameter. `code`, `state`, `scope`, `error` and `error_description` all reach the plugin. Mattermost logs the path, not the query; a reverse proxy's access log will contain the code. | [MM master plugin_requests.go:190-218] |
| M5 | `MMAUTHTOKEN` has no SameSite attribute (except the `MMEMBED` case), so browsers send it on the top-level redirect back from `id.twitch.tv`. | [MM v9 server/channels/app/login.go:284-330] |
| M6 | Mattermost adds no headers to plugin responses. The plugin sets its own Content-Type, CSP and caching. | [MM master server/channels/app/server.go:1084-1110] |
| M7 | `p.API.GetConfig().ServiceSettings.SiteURL` is not sanitized and may be `""`. Plugin routes live at SiteURL (including any sub-path) + `/plugins/ceo.villa.villanet/…`. | [MM v9 plugin_api.go:91-93]; [MM master app/channels.go:240-243] |
| M8 | **Desktop app.** `window.open(<SiteURL>/plugins/…)` opens the single trusted in-app popup: the main process creates a `BrowserWindow` with no `partition` (so the **default session**, with `MMAUTHTOKEN`), calls `popup.loadURL(…)` itself and then returns `{action: 'deny'}` to the renderer, so **`window.open` always returns null there** even though the popup is open. The handler is set on the whole view's `webContents` with `setWindowOpenHandler`, so it serves `window.open` from **any frame** in the view, including a cross-origin iframe allowed to open popups. A main-process `loadURL` is browser-initiated (it arrives with `Sec-Fetch-Site: none`). The popup may navigate anywhere. Its `will-redirect` only blocks redirects to internal **non-plugin** URLs, so the callback must answer with a page, not a redirect into the app. An `about:blank` popup (pluginsPopUps) is refused every later navigation, so "open blank, then navigate" is not usable. `window.open(<outside URL>)` opens the system browser, which has no Mattermost session. Server views allow http and https subframes with no host allow-list. A cross-origin fullscreen request asks Allow/Deny once per origin in views, and is denied silently in popups that are not views. Electron's default autoplay policy needs no gesture. Electron: returning `deny` cancels the window, and `window.open` returns `Window \| null`. | [desktop v6.3.0 src/app/views/webContentEvents.ts:225-284, :332-333]; [desktop pluginsPopUps.ts:55-72, :120-133]; [desktop webContentEventsCommon.ts:45-81]; [desktop permissionsManager.ts:149-245]; [electron docs window-open.md]; [electron web_contents_preferences.cc:130] |
| M9 | The right-hand panel is 400 px fixed on 9.0. From 9.1 it is resizable: at most **400 px** in windows 901 to 1200 px wide (the spacer beside it is 304 px there), at most 464 px from 1201 px, 776 px from 1681 px, and full width at 768 px and below. VillaNET widens it further only in windows from 1201 px (`rhsWidth.ts`). The panel body has 12 px of padding on each side (`styles.ts` `.vntv-body`), so a tile's stage is about 24 px plus any scrollbar narrower than the panel. | [MM master `_sidebar-right.scss:17-45`]; [MM resizable_sidebar/constants.ts]; `M webapp/src/rhsWidth.ts:1-40`; `M webapp/src/styles.ts` `.vntv-body` |
| M10 | One custom status per person. Every set and clear broadcasts a user update. Statuses are hidden everywhere when `EnableCustomUserStatuses` is off. | [MM master app/status.go:84-113]; custom_status selectors |
| M11 | KV: `KVSetWithExpiry` (plugin/api.go:984), `KVCompareAndDelete` (:970), `KVSetWithOptions` (:978), keys up to 150 runes (model/plugin_key_value.go:13). Expired rows are filtered on Get. | pinned `server/public@v0.4.4`; [MM master plugin_store.go:214] |
| M12 | **Amended 2026-10-05: now 11.11.0, with React 18.2.0; see §6.11.** `min_server_version` is **9.3.0** since Clips (`M plugin.json:9`, `M server/clips_share.go:1046-1049`), and plugins run on React 17 there. The web app's CSP at 9.3.0 is the same as M1 (`frame-ancestors …; script-src 'self' …`). | plugin.json; [MM v9.3.0 server/channels/web/handlers.go:239-256]; memory "Mattermost React version" |
| M13 | **Markdown images.** A post's `![x](url)` renders as a plain `<img src=url>`. Mattermost routes it through its image proxy only when `HasImageProxy` is set, and `ImageProxySettings.Enable` defaults to false. So a same-origin plugin URL in a post is fetched by every reader's browser **with their session cookie**, as a GET. | [MM v9 webapp utils/markdown/renderer.tsx:97-104]; [MM v9 utils/post_utils.ts:85-97]; [MM v9 model/config.go:3199-3202] |
| M14 | **How the caller signed in.** `ServePluginRequest` also accepts a personal access token, OAuth-app or bot token (Authorization header, or `?access_token=`, which it then deletes from the query). The plugin gets `c.SessionId`, and `p.API.GetSession(id)` returns the session, whose `IsIntegration()` is true for a bot, a personal access token or an OAuth app. | [MM v9 plugin_requests.go:136-147, :228-230]; pinned `server/public@v0.4.4` `model/session.go:251-266`, `plugin/api.go:211` |
| M15 | **Plugin routes carry no Mattermost CSP.** `/plugins/{id}/…` is registered straight on the router and wrapped only by Sentry, CORS and the rate limiter, so a page the plugin serves has whatever CSP the plugin sets. | [MM v9.3.0 server/channels/app/channels.go:214-217]; [MM v9.3.0 server.go:923-967] |

### 1.3 Twitch Developer Services Agreement (last modified 12/04/2024)

These are paraphrased, with line numbers in the saved `facts/dsa.txt`. Reading them for VillaNET is
a judgement, not legal advice. §14 puts the judgement calls to Victor.

- §VI.A (`:134`): "Twitch Data" includes insights derived from Twitch data. **Airtime earned on Twitch
  is Twitch Data.**
- §VI.C (`:138`): it may not be shared with third parties. Showing it to the server's own members is
  using it for them, not sharing.
- §VI.F (`:144`) and Schedule 1 (`:282`): delete a person's data when they revoke or narrow their
  authorization, or when they or Twitch ask.
- Schedule 1 (`:284`): do not keep copies of Twitch content or API data beyond a cache of 24 hours.
  Stream metadata and pictures here live for minutes. The link record is kept *while the person is
  connected*, as their own authorized data, and is refreshed daily while the switch is on (§4.7).
  While the switch is off VillaNET contacts Twitch for nothing, so names are not refreshed then;
  they are checked on the first pass after it is switched back on. Q3 asks Victor whether switching
  off should delete everything instead.
- Schedule 1 (`:246`, `:248`, `:250`): obtain consent for what is done with end users' information,
  and publish a privacy notice covering what is collected and how it is used, **displayed**, shared,
  stored and retained (§4.9's consent note and §10.2). Cookies must be SameSite, HttpOnly and Secure.
  **VillaNET sets no cookie for this**: the ticket and `state` live on the server.
- Schedule 1 (`:298`): all Twitch video must be shown through Twitch's embeddable player. **The Clips
  recorder must never touch a Twitch stream** (§9).
- §II.3: follow the documentation, and no reverse engineering. The plugin must not re-implement the
  `postMessage` protocol of Twitch's player script.
- §VII: show data fresh, with a clear path back to the source. Every Twitch card and tile carries
  **Watch on Twitch ↗** and the word "Twitch".
- Trademark guidelines: do not alter Twitch's logos, and do not use Twitch in an app name. **VillaNET
  shows the plain word "Twitch" and no Twitch logo anywhere.** *Superseded 2026-10-06 for the
  footer's Twitch tool only: Victor chose Twitch's Glitch there, unaltered and in one colour (the
  amendment at the top). Everywhere else the plain word stands.*

---

## 2. Settings and System Console

### 2.1 The section

Part A of media-lookups creates the section `"key": "twitch"` directly after "Movies and games", with
`TwitchClientID` and `TwitchClientSecret`. This feature adds three settings **after** those two and
rewrites the section's subtitle and one sentence of the Client ID help. Exact JSON:

```json
{"subtitle": "One Twitch developer application for everything VillaNET does with Twitch: looking up games in IGDB, Twitch's game database, and showing the people who connect their Twitch account while they are live there."}
```

In `TwitchClientID`'s help (`design/media-lookups.md:94` as Part A wrote it), make three
replacements, each of a whole phrase:

1. "and will use it for its Twitch features later" becomes "and for Show people live on Twitch
   below".
2. "(IGDB does not use it; a later VillaNET Twitch feature will show you the address to add)"
   becomes "(Show people live on Twitch needs it; IGDB does not; the exact address is shown below
   under Twitch connections)". (Part A's wording at the time of writing; if Phase 1 lands it with
   different words, replace whatever parenthesis tells the admin about the redirect URL.)
3. "Browsers never contact Twitch or IGDB." becomes "Game lookups never make a browser contact
   Twitch or IGDB; the two Twitch live switches below do, as their help explains."

Everything else in Part A's two help texts stays as written. Two help texts **outside** this section
also promise "no outside requests" and change with this feature (§10.1): `MediaLookup` and
`EnableVoice` (Mumble).

The three new entries, appended to the section's `settings` array after `TwitchClientSecret`:

```json
[
    {
        "key": "EnableTwitchLive",
        "display_name": "Show people live on Twitch",
        "type": "bool",
        "default": false,
        "help_text": "Lets people connect their Twitch account with a Connect Twitch button in the panel. While one of them is live on Twitch they wear the live badge, get a card in Live now that EVERY member sees, with their Twitch title, category, viewer count and preview picture, and their time live on Twitch counts toward the airtime boards and medals alongside their Broadcast Box time (never twice for the same minute). Anyone signed in can connect; under Twitch connections below you can disconnect and block a person, or mark a channel 24/7 so it gets no badge and no airtime. Twitch no longer marks mature streams, so VillaNET cannot hide them. This makes the Mattermost SERVER contact Twitch: id.twitch.tv (for the application's access token, and, once per connection, to exchange and then revoke that person's one-time sign-in) and api.twitch.tv (once per connection to confirm who connected; once a minute, only while somebody is connected, to see who is live; once a day to keep their names right). It fetches Twitch's preview pictures from static-cdn.jtvnw.net and relays them, so browsers never fetch them. It also makes ONE BROWSER contact Twitch: pressing Connect Twitch and then Continue on Twitch sends that person's own browser, in the window it opens, to id.twitch.tv and Twitch's sign-in pages to approve it. VillaNET keeps each connected person's Twitch user id, login, display name and how long they streamed on Twitch. It never keeps a Twitch password or token, and disconnecting deletes all of it. Needs the Client ID and Client Secret above, and the OAuth Redirect URL shown below added to that application once. Off by default. While it is off, nothing is asked of Twitch, nobody can connect and no Twitch card shows; people already connected can still disconnect."
    },
    {
        "key": "TwitchEmbed",
        "display_name": "Play Twitch streams inside VillaNET",
        "type": "bool",
        "default": false,
        "help_text": "Plays Twitch streams in the panel with Twitch's own embedded player instead of only linking to them. THIS CHANGES VILLANET'S PROMISE that browsers contact nobody but this Mattermost server. Somebody who presses play on a Twitch stream has their browser load Twitch's player straight from Twitch (player.twitch.tv). That player then talks to Twitch's own servers (its code names, for example, assets.twitch.tv, gql.twitch.tv, static-cdn.jtvnw.net, usher.ttvnw.net, passport.twitch.tv, id.twitch.tv, edge.ads.twitch.tv and Twitch's analytics at spade.twitch.tv) and to the advertising and audience-measurement services Twitch works with (Amazon's amazon-adsystem.com, comScore's scorecardresearch.com and Nielsen's imrworldwide.com). Twitch sees this server's address and the viewer's IP address and, if the viewer is signed in to Twitch in that browser, who they are (in the desktop app, connecting Twitch signs Twitch in there). Twitch may show ads in it. The player can play sound, go fullscreen, show browser dialogs and open windows of its own, which in the desktop app includes the app's built-in window for plugin pages; it cannot change or read the Mattermost page. VillaNET cannot list or limit what the player contacts. Nothing loads until somebody presses play on a Twitch stream, and Broadcast Box streams are not affected. Twitch only allows its player on a site served over https on the standard port (443) under a domain name, and at least 400 by 300 pixels, which in the side panel needs a window at least 1201 pixels wide; otherwise the stream plays in a larger window over the page. Off: Twitch streams show as cards with a Watch on Twitch link, which opens twitch.tv in a new tab. Needs Show people live on Twitch."
    },
    {
        "key": "TwitchSetup",
        "display_name": "Twitch connections",
        "type": "custom",
        "help_text": "The exact OAuth Redirect URL to add to the Twitch application, whether Twitch's player can run at this server's address, who has connected (with Disconnect, Disconnect and block, and 24/7 for each), who is blocked, and a way to delete everything VillaNET holds from Twitch. Works while Show people live on Twitch is off, so connections can always be removed."
    }
]
```

- `configuration.go`: add `EnableTwitchLive bool` and `TwitchEmbed bool` after Part A's two fields.
  `normalize()` needs nothing more.
- `defaults.go`: in the `// Twitch` group add `"EnableTwitchLive": false` and `"TwitchEmbed": false`.
  `TestTheSectionDefaultsMatchTheManifest` checks it.
- New helpers in `twitch_live.go`:
  ```go
  // twitchLiveOn: the switch is on and an application is set up. Everything
  // in this feature that contacts Twitch, shows a Twitch stream or credits
  // Twitch time asks this one question first, so a server with the switch
  // off makes no Twitch request. Taking things away does not ask it:
  // disconnecting, blocking and purging work whatever the switch says,
  // because a person's right to withdraw does not depend on an admin's
  // setting (§4.6). With the switch off, the only Twitch KV reads are one
  // read of twitch_links per /me and one at activation.
  func (c *configuration) twitchLiveOn() bool {
      _, ok := c.twitchCredentials()
      return c.EnableTwitchLive && ok
  }

  func (c *configuration) twitchEmbedOn() bool { return c.twitchLiveOn() && c.TwitchEmbed }

  // sameTwitchLive: nothing that changes what the Twitch loop does has changed.
  func sameTwitchLive(a, b *configuration) bool
  ```
  `sameTwitchLive` compares `EnableTwitchLive`, `TwitchClientID` and `TwitchClientSecret`.
  `TwitchEmbed` is not in it: changing that only changes the snapshot, which the poller restart
  republishes.

### 2.2 `OnConfigurationChange`, `OnActivate`, `OnDeactivate`

- `OnConfigurationChange`, inside the existing `if previous != nil` block (`B plugin.go:598-612`): if
  `!previous.twitchLiveOn() && cfg.twitchLiveOn()`, call `p.armTwitchResync(nowMillis())` (§7.5).
- Inside `if activated {` (`B plugin.go:624`), before `p.restartPoller()`: if
  `!sameTwitchLive(previous, &cfg)`, call `p.restartTwitchLive()`. `previous` may be nil on the first
  call, and nil counts as "changed".
- `OnActivate`: call `p.restartTwitchLive()` after `p.restartPoller()`. Then call
  `p.kickTwitchPurges()` (§4.6) **whatever the switch says**: it reads `twitch_links` once and starts
  the purge worker only if there is something to purge.
- `OnDeactivate`: call `p.stopTwitchLive()` and `p.stopTwitchPurges()` right after `stopPoller` (and
  Clips' `stopClips`), before `clearAllBadges`.
- `restartTwitchLive` stops the running loop, if any, and waits for it, the way `restartPoller` does.
  It then starts a new loop only when `cfg.twitchLiveOn()`. Otherwise it calls `store.clearLive()`,
  which drops the live list, the pictures and the failure state but keeps the cached link document.
  Turning the switch off therefore removes every Twitch card on the poll that the settings save
  triggers anyway. Badges come off after the usual grace.
- **Lock order** (the only one allowed, so nothing can deadlock): `store.runLock` (lifecycle), then
  `p.statsWriteLock`, then `store.lock`. Stopping the loop or the purge worker cancels its context and
  waits for it **with no stats lock held**. The loop and the worker take `statsWriteLock` only inside a
  step, and every KV read inside a step checks `ctx.Err()` first, so a cancel ends a purge of 80
  month tables within one KV read rather than holding up the System Console's Save.
  `OnConfigurationChange` waits for the loop only through `restartTwitchLive`, so this bounds that
  wait to one KV read or one Helix request (whose context is the loop's).

### 2.3 The callback address and the "can Twitch's player run here" check (server)

```go
// twitchCallbackURL is the redirect_uri, exactly as the admin must register it.
// problem is a sentence for the admin when there cannot be one.
func twitchCallbackURL(siteURL string) (address, problem string)

// twitchEmbedProblem says why Twitch's player cannot run on pages served at
// siteURL, or "" when it can.
func twitchEmbedProblem(siteURL string) string
```

`twitchCallbackURL`:

1. Trim spaces. If empty: problem "Mattermost's Site URL is not set (System Console > Environment >
   Web Server > Site URL), so Twitch has nowhere to send people back to."
2. `url.Parse`. A parse error, a query, a fragment or user info gives the problem "The Site URL could
   not be read as an address."
3. A scheme of `https` is fine. `http` is fine only when the host is `localhost`, `127.0.0.1` or `[::1]`
   (T7). Otherwise the problem is "Twitch only accepts an https:// callback address, and the Site URL
   is http://…".
4. The address is `strings.TrimRight(siteURL, "/") + "/plugins/" + manifestID + "/api/v1/twitch/callback"`.
   It is used **byte for byte** for both the authorize request and the exchange (T1, T2).

`twitchEmbedProblem` follows T14. It is shared in spirit with the webapp's `embedHostFor` (§6.2),
which judges the page actually loaded, not the Site URL:

- The host is `localhost` or `127.0.0.1`: "".
- The scheme is not https: "Twitch's player only runs on https:// pages."
- The port is set and is not 443: "Twitch's player only runs on the standard https port (443); this
  server uses :8065."
- The host is an IP literal (`net.ParseIP` of the bracket-stripped host is not nil): "Twitch's player
  does not run on a site reached by IP address."
- Otherwise "".

### 2.4 `TwitchSetup`: the System Console panel (`components/AdminTwitch.tsx`)

Registered with `registerAdminConsoleCustomSetting('TwitchSetup', AdminTwitch)` in `index.tsx`, beside
the others. It reads `GET /api/v1/admin/twitch` (§4.1) when shown, and again after each action. Rows:

1. **OAuth Redirect URL**: the address in a read-only monospace box with **Copy**, and under it "Add
   this exactly under OAuth Redirect URLs in your Twitch application (dev.twitch.tv/console/apps), then
   press Save there." When there is a callback problem, show the problem sentence in the error style
   instead of the box.
2. **Application**: "Client ID and Client Secret are set." or "Fill in the Client ID and Client Secret
   above first." Plus the last token or Helix problem, if any (§5.2).
3. **Twitch's player at this address**: "Can run here." or the `twitchEmbedProblem` sentence, with
   "Twitch streams will open on twitch.tv instead." Shown only while **Play Twitch streams inside
   VillaNET** is on.
4. **Last check**: "Asked Twitch 40 seconds ago: 2 of 7 connected people live." or the problem
   sentence. While the switch is off: "Show people live on Twitch is off: nobody can connect and
   nothing is asked of Twitch. Connected people below can still be disconnected."
5. **Connected people**: a list of Mattermost name, Twitch name (`twitch.tv/login`, an outbound link),
   since when, "live" when live, and "24/7" when marked. Each row has:
   - **Disconnect** (two clicks, `useTwoClicks`): `DELETE /api/v1/admin/twitch?user=<id>`;
   - **Disconnect and block** (two clicks): `DELETE /api/v1/admin/twitch?user=<id>&block=1`;
   - **24/7** (a checkbox): `POST /api/v1/admin/twitch/allday` with `{"userId", "allDay"}`. Its
     label: "24/7: tagged 24/7, no live badge, no airtime, like an always-on stream key."
   This section is shown, and works, whatever the switch says.
6. **Blocked**: the Mattermost names of blocked people, each with **Unblock**
   (`POST /api/v1/admin/twitch/block` with `{"userId", "blocked": false}`), and "Blocked people cannot
   connect a Twitch account." Hidden when nobody is blocked.
7. **Twitch hours**: "12 h 40 m recorded for 3 Twitch channels." If recording has halted, the halt
   reason and "Fix or delete the twitchair_… key, then disable and enable the plugin." If removals are
   waiting: "Removing the hours of 1 disconnected channel." **Whenever any Twitch `quarantine_` copy
   exists** (§4.5, §7.6): "A set-aside copy of damaged Twitch data still exists and may hold
   disconnected people's Twitch names and hours. Delete all Twitch data removes it."
8. **Delete all Twitch data** (two clicks, red): "Disconnects everybody and deletes every Twitch hour,
   name and id VillaNET holds, including set-aside copies. Broadcast Box hours are not touched." It
   calls `DELETE /api/v1/admin/twitch?all=1`. It works whatever the switch says. The block list is
   kept (it holds only Mattermost ids, no Twitch data); a separate **Clear the block list** link
   empties it.

### 2.5 Settings backup

`EnableTwitchLive` and `TwitchEmbed` are ordinary settings, so they are included. `settingsNotIncluded`
(`B settings.go:66-70`) gains `"Who has connected which Twitch account, who is blocked from it, and
their Twitch hours"`.

---

## 3. The shared Twitch application component (`server/twitch_app.go`)

**Reused from media-lookups Part A, unchanged:** `twitchIDBase`, `twitchCredentials{clientID, secret}`,
`(*configuration).twitchCredentials() (twitchCredentials, bool)`, `p.twitchApp()`,
`appToken(ctx, creds)`, `invalidate(token)`, the three errors, `sendMedia` with its User-Agent, and the
rule that nothing is logged. Twitch live never fetches a client-credentials token itself, never caches
one, and never reads the secret outside this file.

**Added by this feature, in the same file** (so the secret is still only read here):

```go
// Where Helix lives. A variable only so tests can point it at a fake.
var twitchAPIBase = "https://api.twitch.tv"

// maxHelixAnswerBytes caps any Helix answer: 100 streams is about 100 KiB.
const maxHelixAnswerBytes = 1 << 20

// twitchRate is what Helix said about the bucket on its last answer.
type twitchRate struct {
    limit, remaining int
    reset            time.Time // zero when not sent
}

// helixGet asks Helix with the app token. On a 401 it invalidates that token
// and asks once more with a fresh one (T10). The body is capped and returned
// for the caller to decode; a status other than 200 is an error that carries
// the status and nothing else.
func (a *twitchApp) helixGet(ctx context.Context, creds twitchCredentials, path string, query url.Values) ([]byte, twitchRate, error)

// exchangeCode trades an authorization code for a user access token and the
// ID token that came with it (T2, T18). The secret goes in the form body only.
// The refresh token is dropped where it is decoded and never leaves this
// function.
func (a *twitchApp) exchangeCode(ctx context.Context, creds twitchCredentials, code, redirectURI string) (accessToken, idToken string, err error)

// twitchIDClaims is what VillaNET reads from an ID token: nothing else.
type twitchIDClaims struct {
    Iss   string          `json:"iss"`
    Aud   json.RawMessage `json:"aud"`   // a string, or an array of one
    Exp   int64           `json:"exp"`
    Nonce string          `json:"nonce"`
    Sub   string          `json:"sub"`
}

// checkIDToken decodes the ID token's payload and checks it belongs to this
// connection. No signature check: the token came straight from Twitch's token
// endpoint over TLS in this same request, which OpenID Connect allows in place
// of the signature (T19). It returns the Twitch user id (sub).
func checkIDToken(idToken, clientID, nonce string, now time.Time) (string, error)

// maxIDTokenBytes caps the ID token before anything decodes it.
const maxIDTokenBytes = 8 << 10

// twitchIssuer is the issuer Twitch's discovery document names (T18). A claim
// value, not a host, so tests do not swap it.
const twitchIssuer = "https://id.twitch.tv/oauth2"

// whoseToken asks Helix whose a user token is (T4): GET /helix/users with that
// token as the bearer and our Client-Id. Exactly one user must come back.
func (a *twitchApp) whoseToken(ctx context.Context, creds twitchCredentials, userToken string) (twitchUser, error)

// revokeUserToken revokes a user token (T5). Best effort: a failure is warned
// once and otherwise ignored, because the token is already forgotten.
func (a *twitchApp) revokeUserToken(ctx context.Context, creds twitchCredentials, userToken string)

type twitchUser struct{ ID, Login, DisplayName string }
```

- Every request goes through `sendMedia`, so it gets the shared client, timeout and redirect policy.
  Helix calls send `Client-Id: <clientID>` and `Authorization: Bearer <token>`. The id.twitch.tv calls
  send forms with `Content-Type: application/x-www-form-urlencoded`. **No secret, token or code is ever
  put in a URL.**
- A Helix 429 returns `errTwitchSlowDown` (new, carrying `reset`). A 5xx returns `errTwitchUnavailable`.
  A 503 is retried once after 1 second, as T10 allows, through the injected clock's `after`, never
  `time.Sleep`.
- Validation of what comes back, **from every Twitch answer that carries these fields** (whoseToken,
  Get Streams' `user_id`/`user_login`/`user_name`, and Get Users in the daily check), through one
  helper `cleanTwitchUser(id, login, name) (twitchUser, bool)`: the ID matches `^[0-9]{1,20}$`; the
  login matches `^[a-z0-9_]{1,25}$` after `strings.ToLower`; the display name is `cleanText(…, 64)`
  and falls back to the login. A failure means that entry is ignored (a stream is left out of that
  read, a name is not updated), with a warning once. The access token from the exchange must be 1 to
  512 printable ASCII bytes with no space.
- `checkIDToken`, in order, each failure an error carrying only a code word (`shape`, `size`, `iss`,
  `aud`, `exp`, `nonce`, `sub`): at most `maxIDTokenBytes`; exactly three dot-separated parts; the
  middle one decodes with `base64.RawURLEncoding` and then as JSON into `twitchIDClaims`;
  `Iss == twitchIssuer`; `Aud` is the string `clientID` or an array whose only element is
  `clientID`; `Exp` is after `now - 5 min` (clock leeway); `Nonce` equals the expected nonce
  (`subtle.ConstantTimeCompare`); `Sub` matches `^[0-9]{1,20}$`.
- Warnings use Part A's warn-once keys, plus `"twitch-helix"`, `"twitch-revoke"` and
  `"twitch-idtoken"`. They carry `"status", <code>` or `"reason", <code word>` and nothing else.

If Part A has not landed in the main tree when this build starts, **stop**: §12 makes it a
prerequisite. Do not write a second token component.

---

## 4. Linking: Connect Twitch

### 4.1 Routes

> **Amended 2026-10-05 (lock screen).** Each route below has an access class under the front gate. `connect-ticket` and `connect` are for linked people only, and `callback` re-checks access. See the amendment block at the top.

| Route | Who | What |
|---|---|---|
| `POST /api/v1/twitch/connect-ticket` | Signed in, interactive session (§4.2). Not a GET, so a cookie caller must send the CSRF token (M3), which the webapp's `requestJSON` does. Metered by `twitchConnectLimiter`. | Issues a single-use connect ticket (§4.2). |
| `GET /api/v1/twitch/connect?t=<ticket>` | Signed in (cookie), interactive, holding a ticket. Matched **before** the generic `Mattermost-User-Id` check in `ServeHTTP` (`B api.go:107-114`), because it answers in HTML. | Spends the ticket, makes a `state` and redirects to Twitch (§4.2). |
| `GET /api/v1/twitch/callback` | Twitch sends the person's browser here. Matched before the generic check, like `/static/`. | Finishes linking (§4.3). |
| `DELETE /api/v1/twitch/link` | Signed in. Not metered, like taking back a plan. **Works whatever the switch says.** | Disconnects the caller (§4.6). |
| `POST /api/v1/twitch/notice` | Signed in. | Dismisses the caller's "your Twitch account was connected to someone else" notice (§4.5). |
| `GET /api/v1/twitch/preview?handle=&v=` | Signed in. | The relayed picture (§5.7). |
| `GET /api/v1/admin/twitch` | `isSystemAdmin`. Works whatever the switch says. | Status for TwitchSetup (§4.10). |
| `DELETE /api/v1/admin/twitch?user=<id>[&block=1]` / `?all=1` | `isSystemAdmin`. Works whatever the switch says. | Disconnects one person (and blocks them), or deletes everything (§4.6). |
| `POST /api/v1/admin/twitch/block` `{"userId", "blocked"}`; `POST /api/v1/admin/twitch/block` `{"all": true, "blocked": false}` | `isSystemAdmin`. | Blocks (which also disconnects) or unblocks one person; clears the block list (§4.5). |
| `POST /api/v1/admin/twitch/allday` `{"userId", "allDay"}` | `isSystemAdmin`. | Marks a connected channel 24/7 or not (§4.5). |

Every JSON answer from these routes sets `Cache-Control: no-store`. Every route that **adds** a
connection (`connect-ticket`, `connect`, `callback`) refuses an integration session (M14): it loads
`p.API.GetSession(c.SessionId)` and refuses when that fails or `session.IsIntegration()` is true
(a bot, a personal access token or an OAuth app), with the JSON error "Connect Twitch from the
VillaNET panel while signed in to Mattermost." or the page `signin`. Only an interactive web or
desktop session may link a Twitch account. Routes that take things away do not ask.

### 4.2 Starting a connection: the ticket, then `GET /api/v1/twitch/connect`

**Why a ticket.** The window has to be opened synchronously inside a click, or popup blockers stop
it, and an `await` first would lose the click. In the desktop app only a plugin address opens the
in-app popup that carries the session (M8). So the window must open a plugin GET. But a GET that a
cookie authenticates has no CSRF check (M3), and it can be fired without the person pressing
anything: a post containing `![x](…/api/v1/twitch/connect)` makes every reader's browser request it
with their session (M13); another site can navigate a person there at the top level (the session
cookie has no SameSite attribute, M5, so browsers treat it as Lax, which top-level GETs carry); and in
the desktop app any frame in the view, Twitch's own player and the ad frames inside it included, can
`window.open` a plugin address into the session-carrying popup (M8). Without a ticket each of those
would write a state, spend the person's connect allowance (five renders would lock every reader out
for 15 minutes) and send their browser to `id.twitch.tv` with their Twitch cookies, which breaks the
lockdown promise, without anyone pressing Connect. A single-use ticket that only a CSRF-protected
POST can obtain makes the GET do nothing unless VillaNET's own panel started it.

**`POST /api/v1/twitch/connect-ticket`.** The panel calls it on the **first** press of Connect
Twitch, while it shows the consent note (§4.9), so the ticket is ready before the second press. In
order:

1. No `Mattermost-User-Id`: 401. Not an interactive session (§4.1): 403.
2. `cfg.twitchLiveOn()` is false: 404 `{"error":"Connecting Twitch accounts is switched off on this server."}`.
3. `twitchCallbackURL(SiteURL)` has a problem: 503, with the problem sentence for admins and "An
   admin needs to finish setting up Twitch." for everybody else.
4. The caller is blocked (§4.5): 403 `{"error":"An admin has turned off Connect Twitch for your account."}`.
5. `twitchConnectLimiter` (5 per 15 minutes per user, kept in the lazily built Twitch store, like the
   stills limiters) refuses: 429 `{"error":"Too many tries. Wait a few minutes and press Connect Twitch again."}`.
   This is the only place the limiter is counted.
6. `ticket` = 32 bytes from `crypto/rand`, hex-encoded. `KVSetWithExpiry("twitch_ticket_"+userID,
   {"h": hex(sha256(ticket)), "s": c.SessionId, "at": nowMS}, 600)`. One ticket per person: a new one
   replaces the old. On failure, 503.
7. 200 `{"ticket": ticket, "expiresIn": 600}`, `Cache-Control: no-store`.

**`GET /api/v1/twitch/connect?t=<ticket>`.** The panel opens it with `window.open` on the second
press (§4.9). In order. **Steps 1 to 5 write nothing, count nothing and redirect nowhere**; the
first write is step 6, which only a valid ticket of the caller's own session reaches:

1. Write the HTML headers (§4.4) first.
2. **Fetch metadata.** If `Sec-Fetch-Dest` is present and is not `document`, or `Sec-Fetch-Site` is
   present and is neither `same-origin` nor `none` (`none` is the desktop app's main-process load,
   M8): 400 page `stale`. A cheap extra layer; browsers that send neither header rely on the ticket.
3. No `Mattermost-User-Id`, or not an interactive session (§4.1): 401 page `signin`.
4. `t` does not match `^[0-9a-f]{64}$`: 400 page `stale`.
5. `raw := KVGet("twitch_ticket_"+userID)`. Empty: 400 page `stale`. KV error: 503 page `store`.
   Decode it. `sha256(t)` must equal `h` (`subtle.ConstantTimeCompare`) and `s` must equal
   `c.SessionId`; otherwise 400 page `stale`, and the stored ticket is left alone (whoever sent a
   wrong one cannot spend the real one).
6. **Spend it**: `KVCompareAndDelete(key, raw)`. False: 400 page `stale`.
7. `cfg.twitchLiveOn()` is false: 404 page `off`. A callback problem: 503 page `setup` (admins see
   the problem sentence; everybody else "An admin needs to finish setting up Twitch."). Blocked: 403
   page `blocked`.
8. If the link document (§4.5) cannot be read: 503 page `store`. If it has 500 people and the caller is
   not one of them: 409 page `full`.
9. `state` and `nonce` = 32 bytes each from `crypto/rand`, hex-encoded (64 characters). Then
   `KVSetWithExpiry("twitch_state_"+state, {"u":userID,"at":nowMS,"r":redirectURI,"n":nonce}, 600)`.
   On failure, 503 page `store`.
10. 302 to `https://id.twitch.tv/oauth2/authorize?` + the parameters in this order:
    `response_type=code`, `client_id`, `redirect_uri`, `scope=openid`, `state`, `nonce`,
    `force_verify=true`. Build them with `url.Values` and keep the order with a builder, so tests can
    compare the string. The host is `twitchIDBase`, which tests swap.

A ticket appears in the popup's own address (and so in a reverse proxy's access log) for the moment
before the redirect. It is spent by then, bound to one session, and useless to anyone else.

### 4.3 `GET /api/v1/twitch/callback`

In order. Steps 1 to 6 make **no outside request**, so nobody can make the server call Twitch without
a live state of their own.

1. Write the HTML headers (§4.4).
2. `state` must match `^[0-9a-f]{64}$`. If not, 400 page `expired`.
3. `raw := KVGet("twitch_state_"+state)`. Empty: 400 page `expired`. KV error: 503 page `store`.
   Decode it. If it is undecodable, or `nowMS - at > 600000` (checked again, because
   `KVCompareAndDelete` does not filter expired rows), delete it and answer 400 page `expired`.
4. **Consume it**: `KVCompareAndDelete(key, raw)`. If false, someone else used it: 400 page `expired`.
   Every outcome after this point has used the state up.
5. No `Mattermost-User-Id`, or not an interactive session (§4.1, M14): 401 page `signin`. If
   `Mattermost-User-Id != record.u`: 403 page `otheraccount`. **This is the account-linking CSRF
   defence.** Otherwise an attacker could start a connect as themselves and trick a victim into
   approving it, which would attach the victim's Twitch account to the attacker's Mattermost account.
   Twitch's own `state` advice (T1) does not cover this on its own, because the state is bound to the
   attacker. The session check does. The interactive-session rule closes a further path: were Twitch
   ever to accept extra query parameters on the registered redirect URI (T7 is only forum-reported),
   an attacker could add `access_token=<their own token>` to an authorize link, and a victim with no
   Mattermost session would then complete the attacker's state.
6. `error` is present: if `error == "access_denied"`, 200 page `cancelled`. Otherwise 502 page
   `twitcherror`, with a warning carrying the `error` value **only when it matches
   `^[a-z_]{1,40}$`**, and the word `other` otherwise: whoever completes a callback chooses that
   parameter, so it must not put arbitrary text into the server log. `code` absent or longer than
   512 bytes: 400 page `expired`.
7. `cfg.twitchLiveOn()` is false: 404 page `off`. The caller is blocked: 403 page `blocked`.
8. On `context.WithTimeout(ctx, 15s)`:
   - `token, idToken := exchangeCode(creds, code, record.r)`. Failure: 502 page `twitcherror`.
   - `defer revokeUserToken(creds, token)`, run on a fresh 8 s context so a slow page still revokes.
   - `sub := checkIDToken(idToken, creds.clientID, record.n, now)` (§3). Failure: 502 page
     `twitcherror` (the deferred revoke still runs). **This binds the code to the state it was issued
     for.** Without it, a code that leaked unspent (the `signin`, `otheraccount`, `expired` and `off`
     pages all spend the state but not the code, and the code then sits in the address bar, the
     browser history and the proxy log) could be redeemed by any member with a fresh state of their
     own: they start Connect, swap their state into the leaked URL, and the callback would link the
     victim's Twitch account to them. With the nonce, a code from someone else's authorization fails
     here, and is spent and revoked in the process.
   - `who := whoseToken(creds, token)`. Failure, or `who.ID != sub`: 502 page `twitcherror`.
9. `setTwitchLink(userID, who, nowMS)` (§4.5). Its errors map to pages: `errTwitchFull` to 409 `full`,
   `errTwitchPurging` to 409 `purging`, `errTwitchBlocked` to 403 `blocked`, anything else to 503
   `store`.
10. Afterwards:
    - `store.noteLinks(doc)` updates the cached document;
    - `store.kick()` reads Twitch soon;
    - `p.kickTwitchPurges()` when step 9 queued a purge (a replaced account);
    - `p.refreshSoon()`;
    - `PublishWebSocketEvent(wsEventTwitchLink, {}, &WebsocketBroadcast{UserId: userID})`, and the same
      to the previous owner if the account was taken over (their panel then shows the takeover
      notice, §4.5, §4.9).
11. 200 page `connected` with `who.DisplayName` and `who.Login`, plus the sentence `takenover` when
    step 9 moved the account from somebody else.

The token, the ID token, the code, the nonce, the ticket and the state never appear in a log, an error
string, KV (apart from the state record's own nonce) or a page.

### 4.4 The HTML pages

One `html/template`, so every value is escaped, with headers:

```
Content-Type: text/html; charset=utf-8
Cache-Control: no-store
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-<hash>'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'
```

- The only script is the fixed text `setTimeout(function(){window.close()},1500)`. It is included only
  on `connected` and `cancelled`. `<hash>` is computed at package init from that exact text, so a
  change to it cannot leave the CSP stale. If `window.close` is refused, the page's own words still
  tell the person to close it (M8: whether close works in the desktop popup is UNVERIFIED).
- Styling is plain: system font, centred card, VillaNET red only on a thin top rule. No pictures, no
  outside fonts.

| Page | Status | Heading | Body |
|---|---|---|---|
| `connected` | 200 | Twitch connected | Connected as **{name}** (twitch.tv/{login}). You can close this window and go back to VillaNET. |
| `takenover` (added to `connected`) | | | It was connected to another Mattermost account before. It is connected to yours now. |
| `cancelled` | 200 | Not connected | You cancelled on Twitch, so nothing was connected. You can close this window. |
| `expired` | 400 | This link has run out | This Twitch connection was already used, or took longer than 10 minutes. Close this window and press Connect Twitch again. Do not share this page's address. |
| `stale` | 400 | Start from VillaNET | This window was not opened by Connect Twitch in the VillaNET panel, or it was opened twice. Close it and press Connect Twitch in the panel. |
| `signin` | 401 | Sign in first | Sign in to Mattermost in this browser, then press Connect Twitch in the VillaNET panel again. Do not share this page's address. |
| `otheraccount` | 403 | Started by another account | This connection was started while signed in to Mattermost as someone else. Close this window and press Connect Twitch again while signed in as yourself. Do not share this page's address. |
| `blocked` | 403 | Not allowed | An admin has turned off Connect Twitch for your account. |
| `off` | 404 | Twitch is switched off | Connecting Twitch accounts is switched off on this server. |
| `setup` | 503 | Not set up yet | An admin needs to finish setting up Twitch. (Admins also see the problem sentence from §2.3.) |
| `full` | 409 | No room | This server already has 500 Twitch accounts connected, which is as many as it keeps. |
| `purging` | 409 | Not just yet | That Twitch channel's hours are still being removed after it was disconnected. Try again in a minute. |
| `twitcherror` | 502 | Twitch did not confirm it | Twitch did not confirm the account just now. Close this window and try again in a minute. |
| `store` | 503 | Could not save | VillaNET could not save the connection just now. Try again in a minute. |

### 4.5 The link document

One KV document, `twitch_links`, always written with `casUpdate` (`B community.go:228-268`). The
poller needs every connected id on every read, which rules out per-user keys and a KV listing
(`B nowplaying.go:19-24` makes the same argument). The shape keeps Part E's field names:

```json
{
  "v": 1,
  "users": {
    "<mmUserId>": {"id": "141981764", "login": "twitchdev", "name": "TwitchDev", "at": 1727870000000,
                   "checked": 1727870000000, "gone": 0}
  },
  "purge": {
    "<twitchId>": {"user": "<mmUserId>", "at": 1727870000000}
  },
  "blocked": {"<mmUserId>": 1727870000000},
  "lost": {"<mmUserId>": {"login": "twitchdev", "at": 1727870000000}}
}
```

```go
type twitchLinks struct {
    V       int                         `json:"v"`
    Users   map[string]twitchLink       `json:"users"`
    Purge   map[string]twitchPurgeEntry `json:"purge,omitempty"`
    Blocked map[string]int64            `json:"blocked,omitempty"` // Mattermost user id -> when, ms
    Lost    map[string]twitchLost       `json:"lost,omitempty"`    // takeover notices, by previous owner
}
type twitchLink struct {
    ID      string `json:"id"`
    Login   string `json:"login"`
    Name    string `json:"name"`
    At      int64  `json:"at"`               // connected, ms
    Checked int64  `json:"checked"`          // names last confirmed with Get Users, ms
    Gone    int    `json:"gone,omitempty"`   // daily checks in a row that did not find the account
    AllDay  bool   `json:"allDay,omitempty"` // an admin marked this channel 24/7
}
type twitchPurgeEntry struct {
    User string `json:"user"`
    At   int64  `json:"at"`
}
// twitchLost tells a person that the Twitch account they had connected was
// proved by someone else and moved to them, so a hijack cannot go unnoticed.
type twitchLost struct {
    Login string `json:"login"`
    At    int64  `json:"at"`
}
```

**Invariants:** a Twitch id appears under at most one user; a user has at most one Twitch id; at most
`maxTwitchLinks` (500) users and 500 blocked people. An id under `purge` cannot be connected until its
purge finishes. A blocked user has no entry under `users`. `lost` entries older than 30 days are
dropped on every write.

**Blocking is by Mattermost user only.** Keeping a list of blocked *Twitch* ids would keep Twitch Data
about people after they withdrew, which the agreement does not allow (§1.3). A blocked person cannot
connect any Twitch account, which is what an admin needs: a Twitch channel can only be connected by
someone who can sign in to it.

**Who may connect.** Broadcast Box publishers are reserved profiles that the admin creates out of band
(`LOCKDOWN.md` §1, `STREAM_PROFILE_POLICY=RESERVED`), and always-on stream keys are kept off the badge
and the boards (`AlwaysOnStreamKeys`: "an always-on channel would otherwise win every board by simply
never stopping"). A Twitch connection bypasses the first, so the admin gets the same two levers
afterwards:

- **Disconnect and block** (`DELETE /api/v1/admin/twitch?user=<id>&block=1`, or
  `POST /api/v1/admin/twitch/block` with `blocked: true`): one `casUpdate` that disconnects (as §4.6)
  and sets `blocked[user] = nowMS`. Without it an admin's Disconnect is undone by the person
  reconnecting a minute later. Unblocking deletes the entry.
- **24/7** (`POST /api/v1/admin/twitch/allday`): sets `users[user].AllDay`. A 24/7 Twitch entry gets
  `AlwaysOn: true` in the snapshot (§5.3), so it is tagged 24/7 and sorted last, wears no badge
  (`reconcileBadges` skips `AlwaysOn`, `B poller.go:550`), gets no medal ranks, is never covered by a
  Broadcast Box card (§5.4), and earns no airtime (§7.2). Hours it earned before stay, as they do for
  a stream key added to Always-on stream keys. While it is 24/7 its session mark is kept current
  without crediting, the way a paused Broadcast Box stream's is (`B stats.go:352-363`), so clearing
  the flag never back-pays the hours it was 24/7.

Twitch's `is_mature` flag is deprecated and always false (T8), so mature streams cannot be filtered;
the help text says so and points at these levers. An allow-list mode (only listed people may connect)
is Q9.

**Unreadable document.** Bytes that will not decode are quarantined (`quarantine`,
`B statsstore.go:119`) and **never overwritten**. The apply function returns `errStore`, so Connect
answers `store`, the poller shows no Twitch cards, and the admin sees "The list of Twitch connections
could not be read and was set aside (quarantine_twitch_links). Delete all Twitch data to start
again." It is not `setAside`: dropping it would orphan Twitch hours that then could not be found to
purge.

**`setTwitchLink(userID, who, nowMS)`**, the apply function, given the stored bytes:

1. `nil` means an empty v1 document. A decode failure means quarantine and return `errStore`.
2. If `blocked[userID]` exists, return `errTwitchBlocked`. If `purge[who.ID]` exists, return
   `errTwitchPurging`.
3. `previous := owner(who.ID)`. If it exists and is not `userID`, delete `users[previous]` and set
   `lost[previous] = {who.Login, nowMS}`. **A verified proof wins**, as `setLink` does for stream keys
   (`B links.go:114-122`). The channel's Twitch hours stay with the channel: the ledger is keyed by
   Twitch id (§7.1), exactly as Broadcast Box hours stay with a stream key when someone else proves
   it. The `lost` notice makes a takeover visible to the person who lost the channel (§4.9), so a
   hijack cannot go unnoticed. Also delete `lost[userID]`: connecting again answers any notice of
   one's own.
4. `old := users[userID]`. If `old.ID != ""` and `old.ID != who.ID`, the person is replacing one
   Twitch account with another: `purge[old.ID] = {userID, nowMS}`. The old account is no longer
   connected to anyone, so its data goes.
5. If `users[userID]` does not exist and `len(users) >= maxTwitchLinks`, return `errTwitchFull`.
6. `users[userID] = {who.ID, who.Login, who.DisplayName, nowMS, nowMS, 0}`. Return the encoding.

### 4.6 Disconnecting, and purging

**`DELETE /api/v1/twitch/link`** and the admin's `?user=`, **whatever the switch says**: `casUpdate`.
If the user has no entry, change nothing and answer 200 `{"ok":true}`, so the route is idempotent
(with `block=1` it still sets `blocked`). Otherwise delete `users[user]` and set
`purge[id] = {user, nowMS}`. Then:

- `store.noteLinks(doc)`, and `store.forgetLive(id)`, so the very next poll has no card for them;
- `p.kickTwitchPurges()`;
- `store.kick()` (a no-op while the loop is not running) and `p.refreshSoon()`;
- the WebSocket event to that user.

A store failure answers 503 `{"error":"VillaNET could not disconnect Twitch just now. Try again in a moment."}`.

**Why withdrawal does not depend on the switch.** The agreement requires deleting a person's data
when they withdraw or ask (§VI.F, `facts/dsa.txt:144`). If Disconnect and the purge needed the switch,
then while an admin had it off, or had cleared the credentials, a person could neither see nor press
Disconnect, and an admin's Disconnect would remove the link but leave the hours indefinitely. Purging
is KV-only, so it needs no Twitch traffic and keeps the "nothing is asked of Twitch while off"
promise.

**The purge worker** (`p.kickTwitchPurges()`, `p.stopTwitchPurges()`, in `twitch_links.go`). One
goroutine at most, with its own context and `done` channel under `store.runLock`, started by a kick
when none is running: from `OnActivate`, a disconnect, a takeover or replacement, the daily gone check
and Delete all. It does not run inside the Twitch loop, so it works with the loop stopped. It loops:
read `twitch_links` (a KV error: wait 60 s on the injected clock and retry); if `purge` is empty,
exit; if the Twitch ledger is halted (§7.6), exit (the halt clears only on a restart, whose
`OnActivate` kicks it again); otherwise run **one purge step** for the oldest entry, holding
`statsWriteLock`, then go round. A step that fails on a KV error waits 60 s before the next try.
Tests drive `purgeStep(ctx, now)` directly. For one `twitchId`:

1. Every month table by name, `twitchair_month_YYYY-MM` from `firstPossibleMonth` to the current
   month. A KV read of every month, as `readEndedPodiums` already does, and for the same reason
   (`B monthmedals.go:233-247`): a listing can miss a key. `ctx.Err()` is checked before each read.
   Remove the id where present, and write only the tables that changed. A read error stops the step,
   which is tried again after 60 s. An undecodable table halts the Twitch ledger (§7.6).
2. `twitchair_totals`: remove the id.
3. `twitchair_sessions`: remove the id.
4. `twitchair_paid`: under `purge[id].user`, remove the spans whose `tw` is this id (§7.3). Spans
   Broadcast Box paid, and spans of a Twitch account the person has connected since, stay.
5. `casUpdate` the links document to delete `purge[id]`, and any `lost` entry naming the purged
   login (`twitchPurgeEntry` gains `Login string \`json:"login"\``, set when the purge is queued).
6. `p.forgetMonthPodiums()`, and log at info level "Removed a disconnected Twitch channel's hours" with
   `"user", <mm user id>`.

**Quarantine copies are not purged per person.** A `quarantine_twitchair_…` or
`quarantine_twitch_links` copy holds bytes that would not decode (`B statsstore.go:113-137` keeps only
the first copy, never overwritten), so one person's rows cannot be taken out of it. While any such
copy exists, TwitchSetup says so (§2.4 row 7), and the README's privacy notice says that a set-aside
copy is removed only by Delete all Twitch data (§10.2).

**`DELETE /api/v1/admin/twitch?all=1`** ("Delete all Twitch data"), whatever the switch says. In this
order, which is §2.2's lock order:

1. Take `store.runLock`. Stop the Twitch loop and the purge worker and wait for both, **with no stats
   lock held** (each ends within one KV read or one request, §2.2).
2. Take `statsWriteLock`. Delete `twitch_links` (keeping only its `blocked` map, rewritten as a fresh
   document that holds nothing else), `twitchair_totals`, `twitchair_sessions`, `twitchair_paid`,
   every `twitchair_month_*` by name, and the `quarantine_` copies of each of those names, plus every
   `twitch_state_*` and `twitch_ticket_*` key a `KVList` finds (they expire anyway). Release it.
3. Clear the store (live list, pictures, cached links) and the Twitch ledger halt.
4. If `cfg.twitchLiveOn()`, start the loop again. Release `store.runLock`.
5. `forgetMonthPodiums()` and `refreshSoon()`.
6. Broadcast the WebSocket event to everybody (`&model.WebsocketBroadcast{}`), so open panels refetch
   `/me`.
7. Answer `{"ok":true}`.

Clearing the block list is the separate `POST /api/v1/admin/twitch/block {"all": true, "blocked": false}`.

### 4.7 Keeping names fresh, and accounts that disappear

- Each Get Streams answer carries `user_login` and `user_name`. If they pass `cleanTwitchUser` (§3)
  and differ from the stored values, `casUpdate` them (this is rare). A value that fails the check is
  never written; the stored one stays.
- Once per pass, if any link has `checked` older than 24 hours, call Get Users `?id=` for up to 100 of
  them, the oldest first. Returned (and passing `cleanTwitchUser`): update login and name,
  `checked = now`, `gone = 0`. Not returned: `gone++` and `checked = now`. At `gone >= 2` (two daily
  checks in a row), disconnect and purge that person, as §4.6 does, and log "A connected Twitch
  account no longer exists on Twitch; disconnecting it". This keeps displayed data fresh (§VII) and
  honours Twitch reporting an account deleted (Schedule 1 `:288`). Whether Get Users leaves out
  suspended accounts is UNVERIFIED, which is why two misses a day apart are needed.
- **The login that drives the player and every Watch on Twitch link is never read from the link
  document.** It comes from the same Get Streams entry that produced the card's title and picture
  (§5.3). Otherwise a failed compare-and-set, or a rename while the switch was off, would give a card
  one person's title and picture and a player tuned to whoever now owns the old login (Twitch
  eventually releases old names to other people).

### 4.8 What a Twitch connection grants, and what it does not

> **Amended 2026-10-05 (lock screen).** Nor does it unlock the panel or let anybody in. "Linked" is now `linkOf` (both link entries agree) on the server and `access === 'linked'` on the webapp. A Twitch-only streamer sees the lock screen, not a Link Your Account call to action, which no longer exists. See the amendment block at the top.

**It grants:** the live badge while live on Twitch, a Live now card, playback in the panel when the
admin allows it, and Twitch time on the airtime boards and medals.

**It does not grant the Broadcast Box "linked" status.** In code, "linked" stays
`linkedKey(u) != ""` (`B links.go:69`), and on the webapp `Boolean(me.streamKey)`. Those decide:

- planning, editing and joining plans, and the media search behind them (`requireLinkedStream`);
- What's on?;
- counting on, and appearing on, the Top Snoopers board;
- **everything in Clips** (`requireClipsLink`, `identity.ts`), per Victor's linked-only rules.

A Twitch-only streamer sees the Link Your Account call to action wherever they do today. Q4 asks
Victor whether he wants that to change.

### 4.9 The panel: `components/TwitchConnect.tsx`

> **Amended 2026-10-05 (lock screen, and the panel tidy-up).** Linked people only. The "not linked" state below cannot occur: somebody not linked has the lock screen or the let-in footer, and neither offers Connect. The footer is now one bar whose tools open sheets (`Sidebar.tsx`, `Sheet`), so Connect Twitch belongs in that idiom. A locked but connected person gets only **Disconnect Twitch** on the lock screen. See the amendment block at the top. **Amended 2026-10-06:** the tool in the bar is Twitch's Glitch, not the word, with the tooltips in the amendment of that date at the top; the Taken over notice stays linked-only.

Rendered in the link footer of `Sidebar.tsx` in **both** states, because a Twitch-only streamer never
has `me.streamKey`. The footer is pinned to the bottom of the panel (`.vntv-footer`,
`flex: 0 0 auto`), so every line it gains is taken from the player, and most viewers never stream.
So the resting states are **one compact line or less**, and the explanation appears only once
somebody asks to connect:

- **not linked:** a link-button **Connect Twitch** (`.vntv-twitch-connect`) inside the existing
  `.vntv-link-cta-row`, after Link Your Account, separated by a middle dot;
- **linked:** the same link-button, or the Connected line, as the last line of the
  `.vntv-title-block`, after the relink controls.

It renders nothing unless `me.twitch` is present. For a non-admin it renders nothing while
`me.twitch.available` is false **and** there is no `me.twitch.login` (and nothing at all while
`me.twitch.blocked`). An admin then sees one muted line: "Connect Twitch is not available:
{problem}". A person who is connected always sees the Connected line and **Disconnect**, whatever the
switch says (§4.6).

| State | What shows |
|---|---|
| Not connected | Only the **Connect Twitch** link-button. Its `title`: "Connect your Twitch account so you show as live here when you stream on Twitch." |
| Confirm | Entered on the first press. A small box `.vntv-twitch-confirm` in the footer, with the consent note `.vntv-twitch-note`: "**Connect your Twitch account?** While you are live on Twitch, everyone here sees your Twitch title, category, viewers and picture, and your time live on Twitch counts on the airtime boards. VillaNET keeps your Twitch name and those hours, never a password or token. Disconnect removes them. Continuing opens Twitch, where you sign in and approve." Then **Continue on Twitch** (primary) and **Cancel**. The first press also calls `fetchConnectTicket()`; until it answers, Continue is disabled and reads "Getting ready…". A refusal shows its `error` sentence in place of the buttons, with **Close**. The ticket is fetched again if the box has been open for 9 minutes. |
| Waiting | Entered on **Continue on Twitch**, always, whatever `window.open` returned (see below). "Finish on Twitch in the window that opened." and a **Cancel** link-button that returns to Not connected. After 4 s, if this window has not lost focus (no `blur` event since the press) and no `twitch_link` event has arrived, a second line: "No window? Allow pop-ups for this site, then press Connect Twitch again." |
| Connected | `.vntv-twitch-who`, one line: "Twitch: {displayName}", an outbound link `twitch.tv/{login} ↗` (`target="_blank" rel="noopener noreferrer"`), a small "live" dot if `me.twitch.live`, then **Disconnect** (`.vntv-twitch-disconnect`, `useTwoClicks`: "Disconnect Twitch? Press again"). While `!me.twitch.available` it adds a muted "(Twitch is switched off here)". |
| Disconnect failed | "Could not disconnect Twitch. Try again in a moment." |
| Just disconnected | For 6 s: "Disconnected. Your Twitch hours have been removed from the boards (minutes you were also live on Broadcast Box stay). Removing VillaNET in Twitch's Settings > Connections does not disconnect you here; this button does. To remove VillaNET from your Twitch account too, use those Connections as well." |
| Taken over | When `me.twitch.lost` is set, above whichever state applies: "Your Twitch account {login} was connected to another Mattermost account on {date}. If that was not you, tell an admin." with **OK**, which calls `dismissTwitchNotice()`. |

**Continue on Twitch:** inside the click handler, with the ticket already in hand,
`openConnectWindow(twitchConnectURL(ticket))` (§6.2's `twitchEmbed.tsx`, so the lab can replace it),
which is `window.open(url, '_blank', 'popup=yes,width=520,height=760,noopener')`. Two facts decide the
rest:

- **With `noopener`, `window.open` returns null** in every browser, so blocked pop-ups cannot be
  detected from its value; the 4-second hint replaces that. `noopener` is used because otherwise
  Twitch's sign-in pages would hold a `window.opener` reference to the Mattermost tab, which they
  could navigate to another site.
- **In the desktop app it returns null anyway** (M8): the app opens its own popup and denies the
  renderer's window. Treating null as "blocked" would tell desktop users to allow pop-ups while the
  Twitch window was open, and each retry would reload that window and start another state.

So the panel cannot hold or close the window. The callback page closes itself where the browser
allows it, and otherwise says to close it (§4.4). While Waiting:

- call `refreshIdentity()` on the `twitch_link` WebSocket event, on `window` focus, and every 3 s, for
  at most 5 minutes;
- when `me.twitch.login` appears, switch to Connected.

### 4.10 `/me`, the WebSocket event, and the admin status

`meResponse` gains:

```go
// Twitch: present whenever the switch is on, and whenever the caller has a
// connection or a takeover notice even while it is off, so Disconnect can
// always be reached (§4.6). Problem is for system admins only.
Twitch *meTwitch `json:"twitch,omitempty"`

type meTwitch struct {
    Available   bool        `json:"available"`             // the switch, credentials and a callback address
    Login       string      `json:"login,omitempty"`
    DisplayName string      `json:"displayName,omitempty"` // Twitch's display name
    Live        bool        `json:"live,omitempty"`        // in the store's live list right now
    Blocked     bool        `json:"blocked,omitempty"`     // an admin blocked this person
    Lost        *twitchLost `json:"lost,omitempty"`        // their account moved to someone else
    Problem     string      `json:"problem,omitempty"`     // system admins only
}
```

`/me` reads `twitch_links` directly with one KV read, not from the cache, so a connect shows at once.
It does so **whatever the switch says**; this one read is the only thing `/me` does for Twitch while
the switch is off. A read error leaves `Login` empty and sets `Problem` for admins (only while the
switch is on).

`Problem` for admins, the first that applies:

1. the callback problem (§2.3);
2. "Twitch refused the application's Client ID or Client Secret. Fix them in the Twitch section." (from
   `errTwitchRefused`);
3. "Twitch is asking VillaNET to slow down; checking again at {HH:MM} UTC.";
4. "Twitch could not be reached; the last good check was {N} minutes ago." (after 3 failed passes);
5. the unreadable-document sentence (§4.5);
6. the Twitch-hours halt (§7.6).

The panel shows it to admins the way it shows `voiceAlert`.

**WebSocket:** `wsEventTwitchLink = "twitch_link"`, which reaches the webapp as
`custom_ceo.villa.villanet_twitch_link` with an empty payload. `index.tsx` registers a handler that
calls `refreshIdentity()`.

**`GET /api/v1/admin/twitch`:**

```json
{
  "enabled": true, "embed": true, "credentials": true,
  "callbackUrl": "https://chat.example.com/plugins/ceo.villa.villanet/api/v1/twitch/callback",
  "callbackProblem": "",
  "embedProblem": "",
  "lastTry": 1727870000000, "lastGood": 1727870000000, "problem": "",
  "people": [{"userId": "…", "name": "Erin", "login": "erin_streams", "twitchName": "Erin_Streams",
              "since": 1727000000000, "live": true, "allDay": false}],
  "blocked": [{"userId": "…", "name": "Frank", "since": 1727000000000}],
  "hours": {"channels": 3, "seconds": 45600, "halted": "", "purging": 0, "quarantined": false}
}
```

`people` and `blocked` are sorted by name. `hours` comes from one read of `twitchair_totals`;
`quarantined` is whether any Twitch `quarantine_` key exists (one `KVGet` per name in §4.6's list).
Twitch ids are not sent. The route answers whatever the switch says.

### 4.11 Logging

Allowed: user ids, HTTP status codes, Twitch's `error` value when it matches `^[a-z_]{1,40}$` (else
the word `other`), ID-token code words (§3), counts. Never: the client secret, any token, the ID
token, the code, the state, the nonce, the ticket, `error_description`, or the full callback URL. The
test in §11.1 mocks every `Log*` and checks.

---

## 5. Live status

### 5.1 Polling, not EventSub

Get Streams is the source of truth. EventSub's stream events carry no title, game, viewers or picture
(T17), so the cards would need Get Streams anyway. Webhooks need Mattermost reachable from the
internet on 443. The WebSocket transport needs stored user tokens, which this design refuses to keep.
Conduits would only make the badge appear about a minute sooner, at the cost of shard management.
EventSub, including `user.authorization.revoke`, is out of scope (§15).

One call covers 100 connected people, once a minute: a few hundred points an hour against a bucket
whose documented example is 800 a minute (T10).

### 5.2 The Twitch store and its loop (`server/twitch_live.go`)

`p.twitchLive()` is built on first use (`twitchLiveOnce sync.Once`, `twitchLiveState *twitchStore` in
`plugin.go`, beside `stillsOnce`; not `twitchOnce`, which Part A uses for `twitchApp`). It outlives
poller and loop restarts.

```go
type twitchLive struct {          // one live stream, as Get Streams last described it
    TwitchID, Login, Name string
    Title, Category       string  // cleaned (§5.3)
    Viewers               int
    StartedAt             string  // RFC 3339, as Twitch sent it
    Thumb                 string  // the thumbnail_url template, host-checked (§5.7)
    Missed                int     // good reads in a row that did not list it
}

type twitchStore struct {
    now   func() time.Time        // time.Now; tests replace it
    after func(time.Duration) <-chan time.Time

    lock      sync.Mutex
    live      map[string]twitchLive // by Twitch id
    links     *twitchLinks          // cached document, nil before the first read
    lastTry   time.Time
    lastGood  time.Time
    fails     int
    nextAt    time.Time             // backoff or rate-limit wait
    problem   string                // admin sentence, "" when fine
    rate      twitchRate
    creditAt  int64                 // last Twitch airtime pass, ms (evidence, §7.2)
    pictures  map[string]*twitchPicture
    connectLimiter, previewLimiter *rateLimiter

    runLock sync.Mutex
    cancel  context.CancelFunc
    done    chan struct{}
    kickCh  chan struct{}           // capacity 1, coalesced like refreshCh
}
```

Constants:

| Name | Value | Why |
|---|---|---|
| `twitchReadEvery` | 60 s | Helix is cached for about a minute ([COMM]), so asking more often learns nothing. |
| `twitchKickGap` | 10 s | The shortest gap between reads when a connect or disconnect asks for one. |
| `twitchKeepFor` | 5 min | Failed reads keep the last good list this long. After that it is dropped, because a card nobody can confirm is worse than none. |
| `twitchMissedReads` | 1 | Get Streams can briefly leave out a stream that is live (T8). One good read without it is forgiven. |
| `twitchBatch` | 100 | T8. |
| `twitchReadBudget` | 20 s | A whole pass, every batch included. |
| `twitchBackoffMax` | 10 min | Failures back off 60 s, 120 s, 240 s and so on, up to this. |
| `twitchRateFloor` | 5 | With fewer points than this left, wait for `Ratelimit-Reset`, capped at 10 min. |
| `twitchNamesEvery` | 24 h | §4.7. |
| `twitchGoneChecks` | 2 | §4.7. |
| `maxTwitchLinks` | 500 | Five calls a minute at most. |

**One pass, `store.step(ctx, cfg, now)`.** The goroutine only calls `step` on a timer and on a kick,
and tests call `step` directly with a fake clock and fake hosts.

1. If `!cfg.twitchLiveOn()`: `clearLive()` and return. (The loop is not running then anyway.)
2. Read `twitch_links` and cache it. On an error, keep the old cache and continue. On an undecodable
   document, quarantine it, clear the live list, set the problem, and return.
3. If now is before `nextAt`: skip to step 7.
4. If the cache has nobody connected: the live list is empty, no request is made, and skip to step 7.
5. Ask Get Streams for the connected ids, sorted, in batches of 100: `GET /helix/streams` with
   `type=live&first=100` and `user_id=…` repeated. Each call goes through `helixGet` (which handles a
   401 once). `ctx.Err()` is checked before each batch.
   - All batches answered 200: this is a **good read**. Keep entries with `type == "live"` whose
     `user_id` is connected and whose `user_id`, `user_login` and `user_name` pass
     `cleanTwitchUser` (§3; an entry that fails is left out of this read, so its card cannot carry an
     unchecked login), and ignore duplicates (T8: the first one wins). Merge into `live`: a
     listed stream is stored with `Missed = 0`. An unlisted stream already known gets `Missed++` and is
     kept while `Missed <= twitchMissedReads`. Set `lastGood = now`, `fails = 0`, `problem = ""`,
     `nextAt = now + twitchReadEvery`. Update `rate` from the last answer. If
     `rate.remaining < twitchRateFloor`, set `nextAt = rate.reset` (capped at 10 min).
   - A 429 (`errTwitchSlowDown`): `nextAt = reset` (capped, at least 60 s), with the slow-down problem.
     The list is unchanged.
   - `errTwitchRefused`, `errTwitchUnavailable`, a transport error or a bad shape: `fails++`,
     `nextAt = now + min(60 s × 2^(fails-1), 10 min)`, and a problem once `fails >= 3`. The list is
     unchanged.
   - A pass is all or nothing: if one batch fails, no batch's answer is used, so nobody vanishes
     because their batch failed.
6. After a good read only: `p.recordTwitchTime(listed, cfg, now)` (§7.2), with `listed` being exactly
   the streams this read returned, never those kept by grace. Then update names that changed (§4.7).
7. Housekeeping: if `now - lastGood > twitchKeepFor`, empty `live`. Drop pictures whose stream is not
   in `live`. Run the names check if one is due (§4.7). Purges are not run here: the purge worker
   does them, whether or not this loop runs (§4.6).

Steps 6 and 7 take `statsWriteLock` (for `recordTwitchTime`) and `casUpdate` the links document; they
follow §2.2's lock order and check `ctx.Err()` between KV reads.

The poller never waits on any of this. It reads the store under `lock`, copying what it needs.

**Store methods the rest of the code calls** (each takes `lock` briefly, and none makes a Twitch
request):

| Method | Used by | Does |
|---|---|---|
| `liveNow(now) []twitchLive` | `twitchStreams` | A copy of the entries with `Missed <= twitchMissedReads`, empty once `now - lastGood > twitchKeepFor`. |
| `owners(cfg) (map[string]string, error)` | boards, medals, `twitchStreams` | Twitch id to Mattermost user id, from the cached document; see "the first read" below. |
| `peopleIfOn(cfg) (map[string]bool, error)` | `recordStreamTime` | Connected Mattermost user ids (24/7 channels included, so paid spans stay exact when the flag is cleared), or an empty map and no error when `!cfg.twitchLiveOn()`; see "the first read" below. |
| `twitchLiveFor(userID) bool` | `/me`, `handleSetNowPlaying` | Whether that person's Twitch id is in `liveNow`. |
| `noteLinks(doc)` | connect, disconnect, names | Replace the cached document after a successful write. |
| `forgetLive(twitchID)` | disconnect, takeover | Drop one entry and its picture now. |
| `clearLive()` | switch off, Delete all | Drop the live list, pictures, failure and rate state (keeps the cached document unless Delete all). |
| `kick()` | connect, disconnect | Ask for a pass no sooner than `lastTry + twitchKickGap`; coalesced, never blocks. |
| `status() twitchStatus` | `/me`, admin route | `lastTry`, `lastGood`, `problem`, live count, for display. |

**The first read.** The cached document is nil until the loop's first step, and `OnActivate` starts
the poller before the loop. Without care, the first Broadcast Box poll after every start (the one that
backdates a restart's worth of hours) would see nobody connected: it would credit the whole backlog
with no paid spans, and the Twitch pass would then credit the same stretch again, while the boards and
past-month medals of that poll would leave out every Twitch row and push the wrong medals to every
client. So `owners` and `peopleIfOn`, when the switch is on and the cache is nil, **load
`twitch_links` themselves**: check under `lock`, release it, do one `KVGet` and decode, then take
`lock` again and store it unless the loop got there first. A KV error or an undecodable document is
returned as an error, and the callers treat it as "skip, never guess": `recordStreamTime` skips the
whole pass (its marks are not advanced, so nothing is lost), and `addBoards` carries the previous
snapshot's boards, ranks and medals for that poll, as the failure path does (§5.4). Quarantine stays
the loop's job (§5.2 step 2).

### 5.3 A Twitch stream in the snapshot

`apiStream` gains (in `plugin.go`, after `NowPlaying`):

```go
// Where the stream is: "" for Broadcast Box, "twitch" for Twitch. Absent on
// every Broadcast Box stream, so older panels, the lab and every existing
// test see exactly what they saw before.
Source string `json:"source,omitempty"`

// For a Twitch stream: whose channel, for the player and the Watch on Twitch
// link (a public name, checked ^[a-z0-9_]{1,25}$), and the category Twitch
// lists it under.
TwitchLogin    string `json:"twitchLogin,omitempty"`
TwitchCategory string `json:"twitchCategory,omitempty"`

// The Twitch user id. Never sent: the handle stands for it.
TwitchID string `json:"-"`

// Which relayed preview of a Twitch stream is current (§5.7).
PreviewVersion string `json:"previewVersion,omitempty"`

// On a Broadcast Box card: this person is ALSO live on Twitch, at this
// login. Their Twitch card is left out of Live now (§5.6).
AlsoTwitch string `json:"alsoTwitch,omitempty"`

// On a Twitch entry: the handle of the same person's Broadcast Box card that
// stands in for it in Live now. The entry stays in the list, so a Twitch tile
// somebody is watching keeps playing (§5.6).
CoveredBy string `json:"coveredBy,omitempty"`
```

`snapshot` gains:

```go
// Whether Twitch streams can appear at all, and whether they may play in the
// panel. Here rather than on /me because it is the same for everybody and has
// to reach open panels when an admin changes it.
TwitchOn    bool `json:"twitchOn,omitempty"`
TwitchEmbed bool `json:"twitchEmbed,omitempty"`
```

**`p.twitchStreams(cfg, now) []apiStream`** builds this poll's Twitch entries. It returns a new slice
or nil. It makes no Twitch request and no KV read, apart from the link and token reads used for
redaction below.

- `nil` at once when `!cfg.twitchLiveOn()`. **This is the first statement**, so the ~25 existing tests
  that drive `pollOnce` or `decorate` see no new API call.
- For each entry in the store's `live`, in Twitch-id order:

| Field | Value |
|---|---|
| `StreamKey` | **`""`, always.** Nothing that relays to Broadcast Box can be handed a Twitch stream. `recordStreamTime` already skips an empty key (`B stats.go:304-307`). |
| `Handle` | `p.twitchHandle(id)` = hex(HMAC-SHA256(handleSecret, `"villanet-twitch\x00"` + id))[:32]. Its own helper in `handles.go`, never `streamHandle`. The prefix keeps it apart from every Broadcast Box handle. Empty when there is no handle secret, and then the entry is skipped, as `decorate` skips an empty handle. |
| `Source` | `"twitch"` |
| `UserID`, `Username`, `DisplayName` | From `GetUser(owner)`. A failed lookup, or a deactivated account (`DeleteAt != 0`), skips the entry this poll. |
| `LinkSource` | `"twitch"` (`linkSourceTwitch`) |
| `Title` | `displayNameOf(user)`, as every person's card is named |
| `MOTD` | `cleanText(Title from Twitch, 200)`. Then, as `decorate` does for a note written outside the panel (`B poller.go:332-346`): if the owner has a Broadcast Box link, `redactStreamKey` their publish token and then their key, replacing each with the card title. Then cut to 200 runes again. |
| `TwitchCategory` | `cleanText(game_name, 80)`, which may be empty |
| `TwitchLogin`, `TwitchID` | **From the `twitchLive` entry of the same good read** (`Login`, `TwitchID`), already checked by `cleanTwitchUser`; `TwitchLogin` is checked against `^[a-z0-9_]{1,25}$` once more here, and an entry that fails is skipped this poll. Never from the link document (§4.7). |
| `AlwaysOn` | `true` when the link is marked 24/7 (§4.5): tagged 24/7, sorted last, no badge, no ranks, no airtime. |
| `Viewers` | `viewer_count`, clamped to 0 or more. **It is Twitch's whole audience**, and the webapp always says so (§5.8). |
| `StreamStart` | `started_at` if it parses with `time.RFC3339Nano`; otherwise empty |
| `PreviewVersion` | When `cfg.LivePreviews` and a template is held: `"<started_at unix seconds>-<now.Unix()/300>"` |
| `Paused`, `Bitrate`, tracks, `WatchURL`, stills, `Watchers`, `Others`, clip fields | Zero or absent |
| `CoveredBy` | Set by `mergeStreams` (§5.4), never here |

### 5.4 Merging on the three poll paths (`poller.go`)

```go
// mergeStreams adds this poll's Twitch entries to the Broadcast Box ones.
// With no Twitch entries it returns bb untouched (same slice, same order), so
// a server without Twitch publishes exactly what it did before.
func (p *Plugin) mergeStreams(bb, twitch []apiStream) []apiStream

// broadcastBoxOnly is a new slice of the Broadcast Box entries of a
// published list, with AlsoTwitch cleared, for a poll that carries the last
// list forward.
func broadcastBoxOnly(streams []apiStream) []apiStream

// twitchCover remembers, per person, whether their Twitch entry is covered by
// a Broadcast Box card, and how many polls in a row the evidence has said
// otherwise. Guarded by its own mutex; reset when the poller restarts.
type twitchCover struct {
    covered bool
    streak  int
}
```

`mergeStreams`, when `twitch` is not empty:

1. Make a new slice and copy `bb` into it. **Never write into `bb` or `twitch` themselves**, because
   on the failure path they share arrays with the published snapshot, which readers hold without a
   lock.
2. For each Twitch entry: if its handle equals any Broadcast Box handle, drop it and warn once. That
   can only happen with a hostile origin.
3. **Cover, with hysteresis.** For each Twitch entry that is not 24/7, `want` is whether the same
   `UserID` has a Broadcast Box entry with `!Paused && !AlwaysOn` in this list. The first time a
   person's Twitch entry is seen, `covered = want`. After that, `covered` changes only once `want`
   has disagreed with it on `cfg.OfflineGraceChecks` polls in a row (a poll where they agree resets
   the streak). While `covered`: the Twitch entry **stays in the list** with
   `CoveredBy = <that Broadcast Box handle>` (on a copy), and the Broadcast Box entry gets
   `AlsoTwitch = TwitchLogin` (on the copy) (§5.6). People with no Twitch entry this poll are dropped
   from the cover map. So a simulcaster's OBS reconnect or a short pause no longer swaps their Live now
   card between two handles every poll.
4. Append the Twitch entries and sort with `streamOrder`.

Readers that list **cards** skip covered entries (Live now, `liveHandles`, `PopoverLive`, the
"somebody live" checks; §5.9, §8). Readers that work **by handle** ignore the flag: the watch list,
its reconcile, `TwitchPlayer` and the theatre. So a Twitch tile somebody is watching keeps its stream
when the person starts their Broadcast Box stream, instead of turning into a waiting tile, dropping
the sound and closing 45 s later.

`streamOrder` is a total order. It keeps `decorate`'s grouping (`B poller.go:407-417`) and puts Twitch
people after Broadcast Box people, as Part E did:

1. group: attributed Broadcast Box = 0, Twitch = 1, unattributed Broadcast Box = 2, 24/7 = 3;
2. `strings.ToLower(Title)`;
3. `Handle`.

**Path A, Broadcast Box not configured** (`B poller.go:97-110`):

```go
now := time.Now().UTC()
twitch := p.twitchStreams(cfg, now)
unconfigured := &snapshot{Streams: nonNil(twitch), Configured: false, Error: err.Error(), FetchedAt: nowMillis(),
    LivePreviews: cfg.LivePreviews, TwitchOn: cfg.twitchLiveOn(), TwitchEmbed: cfg.twitchEmbedOn()}
p.addCommunity(unconfigured)
if cfg.twitchLiveOn() {
    // A server that streams only through Twitch still has hours and boards.
    p.addNowPlaying(unconfigured, cfg, now, false)
    if cfg.EnableLeaderboard { p.addBoards(unconfigured, cfg, now) }
    if cfg.TopWatchers { p.addWatchBoards(unconfigured, cfg, now) }
}
p.dropWatchersWhenOff(unconfigured, cfg)
// (Clips) p.addClips(unconfigured, cfg, now) stays the last step before publish
p.publish(unconfigured)
p.reconcileBadges(p.getSnapshot().Streams, cfg, now)
```

With the switch off this is exactly 2.11.0's path plus two `false` fields.

**Path B, the Broadcast Box read failed** (`B poller.go:115-173`). Replace `current.Streams =
previous.Streams` with `current.Streams = broadcastBoxOnly(previous.Streams)`, keep the rest of the
carry-over block as it is, and **after** the block (whether or not it ran) add:

```go
current.Streams = p.mergeStreams(current.Streams, p.twitchStreams(cfg, now))
```

Then `addNowPlaying(…, false)` as today, and **with Twitch on only**:

```go
if cfg.twitchLiveOn() {
    // Twitch time keeps being credited while Broadcast Box is unreachable, so
    // the boards are rebuilt rather than carried: otherwise a Twitch card
    // would keep a crown that no board explains once the carry-over ends.
    if cfg.EnableLeaderboard { p.addBoards(current, cfg, now) }
    if cfg.TopWatchers && !carried { p.addWatchBoards(current, cfg, now) }
}
```

where `carried` is whether the carry-over block ran this poll. `addBoards` writes ranks onto
`current.Streams`, which is safe because `broadcastBoxOnly` and `mergeStreams` made a new slice. With
Twitch off this path is exactly 2.11.0's. After the third failure the Broadcast Box cards empty as
they do today, and the Twitch cards stay, with boards that explain their medals. `dropWatchersWhenOff`,
(Clips) `addClips`, `publish` and `reconcileBadges(current.Streams, …)` follow unchanged, so they all
see the Twitch entries. Set `TwitchOn` and `TwitchEmbed` on `current`.

**When `owners` cannot be read** (§5.2, "the first read"), `addBoards` on any path leaves the boards as
the previous snapshot had them: `AllTime`, `Month`, `MonthLabel`, `MonthMedals`, and each card's ranks
copied by handle. One poll later it tries again.

**Path C, success** (`B poller.go:175-203`):

```go
streams := p.decorate(raw, cfg)
now := time.Now().UTC()
p.recordStreamTime(streams, cfg, now)        // Broadcast Box entries only
p.recordWatchTime(raw, streams, cfg, now)    // Broadcast Box entries only
// (Clips) p.recorders().noteSessions(raw, now); p.recorders().reconcile(streams, cfg, now)
streams = p.mergeStreams(streams, p.twitchStreams(cfg, now))
current := &snapshot{Streams: streams, …, TwitchOn: cfg.twitchLiveOn(), TwitchEmbed: cfg.twitchEmbedOn()}
// addCommunity, addNowPlaying(settle), addBoards, addWatchBoards, checkBirthdays,
// reconcileBadges(streams …), (Clips) addClips, publish: unchanged, and all see the merged list.
```

**`mergeStreams` runs after Clips' `recorders().reconcile`** (`design/clips-addendum.md` §2.1 puts
that call right after `recordWatchTime`), so the recorder is never handed a Twitch entry at all. §9's
`Source == ""` eligibility check stays as a second guard.

Twitch airtime is **not** credited here. It is credited from Twitch's own reads (§7.2), so neither
source's evidence is advanced by the other's poll.

**Republishing between polls is not needed.** A Twitch change reaches panels on the next Broadcast Box
poll (15 s by default), and Twitch's own data lags more than that.

### 5.5 The live badge

`reconcileBadges` (`B poller.go:532-618`) is unchanged. A Twitch entry has `UserID` set and is neither
paused nor 24/7, so it badges its owner as soon as it is in the slice, and it is in the slice on all
three paths. The offline grace runs as it does, counted per Broadcast Box poll.

What makes Twitch flicker-free:

- a missed read is forgiven (`twitchMissedReads`);
- failed reads keep the list for `twitchKeepFor`;
- a Twitch outage longer than that drops the cards, and the badges come off after the grace.

A person live on both wears one badge. Live on Twitch while their Broadcast Box stream is paused, they
still wear it, because the Twitch entry is in the list either way (covered or not; §5.4). It is the
same emoji and the same `LiveStatusText`. A channel an admin marked 24/7 gives no badge.

### 5.6 Live on Broadcast Box and Twitch at once

- **Cards:** the Twitch card is left out of Live now while the person has a Broadcast Box card that is
  neither paused nor 24/7, after the hysteresis of §5.4 step 3 (so for the first
  `OfflineGraceChecks` polls of a simulcast both cards show). The Broadcast Box card carries
  `alsoTwitch` and shows "Also on Twitch ↗". That matches Part E's default. Q1 asks Victor.
- **Badge:** one.
- **Airtime:** each second once (§7.3).
- **A Twitch tile being watched** keeps playing when its card is covered: the entry is still in the
  snapshot with `coveredBy`, and the watch list works by handle (§5.4). It closes only when the person
  stops streaming on Twitch, through the watch list's usual 45-second missing grace.

### 5.7 Preview pictures (`server/twitch_preview.go`)

Relayed by the server, so the lockdown promise holds for pictures whatever the embed switch says.
It follows **Live preview pictures**: when that is off, a Twitch card shows the person's own picture,
as a Broadcast Box card does.

- **The template** (`thumbnail_url`) is accepted from Get Streams only if it parses, is https, its host
  equals `twitchImageHost` (a variable, `"static-cdn.jtvnw.net"`, swapped in tests), its path starts
  with `/previews-ttv/`, and it contains `{width}x{height}` exactly once. Otherwise it is not stored,
  with a warning once.
- **`GET /api/v1/twitch/preview?handle=<32 hex>&v=<version>`:**
  1. Find a snapshot entry with `Source == "twitch"` and that handle; also require
     `cfg.twitchLiveOn() && cfg.LivePreviews`. Otherwise 404, `Cache-Control: no-store`.
  2. If the store holds a picture for that Twitch id fetched less than `twitchPreviewHold` (2 min) ago,
     serve it.
  3. Otherwise, if `previewLimiter` (120 per minute per user, counted only on a miss) allows it, fetch
     it:
     - replace the placeholder with `640x360`;
     - `GET` through `sendMedia` with `Accept: image/webp,image/jpeg,image/png`, on an 8 s context;
     - the answer must be 200, and the final URL (`response.Request.URL`) must still have the path
       prefix `/previews-ttv/`. An offline channel redirects to `/ttv-static/404_preview-…` on the same
       host [LIVE], which the shared client's same-host rule would follow, and that counts as "no
       picture";
     - read at most `maxTwitchPreviewBytes` (256 KiB);
     - `imageType(data)` must be jpeg, png or webp.

     Store `{data, type, at}`. A failure gives 404 `no-store`.
  4. Serve with `Content-Type` from the sniff and `X-Content-Type-Options: nosniff`. Use
     `Cache-Control: private, max-age=300` when `v` equals the entry's current `PreviewVersion`, and
     `private, no-cache` otherwise.
- **Memory:** at most `maxTwitchPreviews` (100) pictures. Dropped when the stream leaves the live list.
  Never older than 2 minutes when served. Never written to KV. Well inside the agreement's 24-hour
  cache rule.

### 5.8 The Live now card (`LiveCard.tsx`)

A card with `stream.source === 'twitch'`:

- **Picture:** `snapshot.livePreviews && stream.previewVersion ? twitchPreviewURL(handle, previewVersion)`,
  else the existing `Backdrop` (the person's picture). Never `stillURL`.
- **Tags:** the existing Live tag (or the existing 24/7 tag when an admin marked the channel 24/7,
  §4.5), then a plain-word tag **Twitch** (`.vntv-card-tag-twitch`) in VillaNET's own colours. No
  Twitch logo and no Twitch purple. Never Paused.
- **Audience:** never faces. The eye and the count with `title="{n} watching on Twitch"`, and the text
  "{n} on Twitch" (number with `toLocaleString()`). Shown even at 0, as "0 on Twitch".
- **Text:** the note line is `motd` (Twitch's title) through the existing `linkify`. Under it,
  `nowPlaying` when the server set one (§8 row 20). Otherwise, when `twitchCategory` is set, a chip
  `.vntv-twitch-category` with the category name and `title="Twitch category"`.
- **Medals:** as on any card, from `allTimeRank`/`monthRank`, which are now by person (§7.9).
- **Link:** always, `Watch on Twitch ↗` (`.vntv-twitch-out`), an `<a href="https://www.twitch.tv/{login}"
  target="_blank" rel="noopener noreferrer">`. This is the attribution and path back to the source that
  §VII asks for.
- **Activating the card** goes through `watchOrOpen` (§6.9).
- **Never on a Twitch card:** Snooper binoculars, the ✂, clip tags, the Paused overlay.

A Broadcast Box card with `alsoTwitch` adds a small `Also on Twitch ↗` link (the same anchor, built
from `alsoTwitch`) in its meta line.

### 5.9 Other readers of the stream list

| Where | Change |
|---|---|
| `PopoverLive.tsx:42` (`find` by `userId`) | Skip `coveredBy` entries. Prefer the entry without `source`, then the Twitch one. The meta line for Twitch reads "Live on Twitch · {duration} · {n} watching on Twitch". **Watch** goes through `watchOrOpen`. A Broadcast Box entry with `alsoTwitch` adds the "Also on Twitch ↗" link. |
| `Sidebar.tsx` `liveHandles` (`M :612`) | One handle per person: skip `coveredBy` entries, then prefer the Broadcast Box handle (no `source`). |
| `Sidebar.tsx` Live now list (`M :1178-1193`) | Skip `coveredBy` entries. |
| `UpNext.tsx:572`, `:624-626` | `watchOnly(handle)` becomes `watchOrOpen(stream, 'only')`. |
| `Sidebar.tsx` Live now `onToggle` (`M :1191`) | `watchOrOpen(stream, 'toggle')` (it still calls `closeTile` when already watching). |
| `Sidebar.tsx` Live now `onWatchOnly={watchOnly}` (`M :1190`), used by the card body click and the card's Watch (`LiveCard.tsx:204`, `:301`) | `(handle) => watchOrOpen(streamOf(handle), 'only')`, so a Twitch card's body click opens twitch.tv or the theatre exactly as its toggle does. |
| `index.tsx` `openPanel 'live'` | Unchanged: a Twitch stream is somebody live. |
| `Sidebar.tsx` "Broadcast Box is not set up yet" (`M :993`) | While `snapshot.twitchOn`, shown to admins only. Unchanged otherwise. |
| `Sidebar.tsx` `snapshot.error` | Unchanged. It is Broadcast Box's error, and the Twitch problem goes to admins through `/me` (§4.10). |
| Awards in post headers, `AwardBadges`, `AwardList`, `LeaderboardModal` | No change: the rows now come one per person (§7.8). `LeaderboardModal` adds `title="incl. {d} on Twitch"` on the hours cell when `twitchSeconds > 0`. |

### 5.10 The JSON contract (`types.ts`)

```ts
export type Stream = {
    // … existing fields …

    // Where it is: absent for Broadcast Box, 'twitch' for Twitch. A Twitch
    // stream plays in Twitch's own player (twitchLogin), never over WHEP.
    source?: 'twitch';
    twitchLogin?: string;
    twitchCategory?: string;
    previewVersion?: string;

    // On a Broadcast Box card: also live on Twitch, at this login.
    alsoTwitch?: string;

    // On a Twitch entry: left out of Live now in favour of this Broadcast
    // Box card. Tiles and the watch list ignore it.
    coveredBy?: string;
};

export type Leader = { /* … */ twitchSeconds?: number };

export type Snapshot = { /* … */ twitchOn?: boolean; twitchEmbed?: boolean };

export type Me = {
    // … existing fields …
    twitch?: {
        available: boolean; login?: string; displayName?: string; live?: boolean;
        blocked?: boolean; lost?: {login: string; at: number}; problem?: string;
    };
};

export type AdminTwitch = { /* exactly the JSON in §4.10 */ };
```

`client.ts` additions (and every one mocked in `lab/mocks/client.ts`, or `surface.ts` fails the build):

```ts
export const fetchConnectTicket = (): Promise<{ticket: string; expiresIn: number}> =>
    requestJSON('/api/v1/twitch/connect-ticket', {method: 'POST'});
export const twitchConnectURL = (ticket: string): string =>
    `${PLUGIN_BASE}/api/v1/twitch/connect?t=${encodeURIComponent(ticket)}`;
export const unlinkTwitch = (): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/twitch/link', {method: 'DELETE'});
export const dismissTwitchNotice = (): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/twitch/notice', {method: 'POST'});
export const twitchPreviewURL = (handle: string, version: string): string =>
    `${PLUGIN_BASE}/api/v1/twitch/preview?handle=${encodeURIComponent(handle)}&v=${encodeURIComponent(version)}`;
export const fetchAdminTwitch = (): Promise<AdminTwitch> => requestJSON('/api/v1/admin/twitch');
export const adminUnlinkTwitch = (userId: string, block = false): Promise<{ok: boolean}> =>
    requestJSON(`/api/v1/admin/twitch?user=${encodeURIComponent(userId)}${block ? '&block=1' : ''}`, {method: 'DELETE'});
export const deleteAllTwitch = (): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/admin/twitch?all=1', {method: 'DELETE'});
export const adminBlockTwitch = (userId: string, blocked: boolean): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/admin/twitch/block', {method: 'POST', body: JSON.stringify({userId, blocked})});
export const adminClearTwitchBlocks = (): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/admin/twitch/block', {method: 'POST', body: JSON.stringify({all: true, blocked: false})});
export const adminTwitchAllDay = (userId: string, allDay: boolean): Promise<{ok: boolean}> =>
    requestJSON('/api/v1/admin/twitch/allday', {method: 'POST', body: JSON.stringify({userId, allDay})});
```

---

## 6. Playing a Twitch stream inside VillaNET

### 6.1 What is possible

- **Twitch's JavaScript player is not used, and must not be.** On Mattermost's own page it cannot
  load: the page CSP is `script-src 'self'` (M1), and no admin setting adds a host. Bundling a copy
  of `v1.js` is not permitted by anything Twitch publishes, and re-implementing its `postMessage`
  protocol is the reverse engineering §II.3 forbids. **It could be loaded another way**: plugin
  routes carry no Mattermost CSP (M15), so the plugin could serve a same-origin wrapper page whose
  own CSP allows `player.twitch.tv/js/embed/v1.js`, and frame that. That would give the documented
  `setMuted`/`setVolume` and `PLAYBACK_BLOCKED`, and no reload when the sound moves. **It is refused**
  because the wrapper must be same-origin with Mattermost for Twitch's `frame-ancestors` check to
  pass (an opaque, sandboxed origin would not match `https://<host>`), so Twitch's script, and
  whatever it loads, would run in Mattermost's origin with the viewer's session: it could read the
  `MMCSRF` cookie, call Mattermost's API as the viewer, and reach `parent.document`. Q6 puts this
  alternative to Victor with its cost, so that nobody "fixes" the reloads this way unawares.
  So **only the plain cross-origin `<iframe>` embed is used** (T12).
- What that means:
  - no `setMuted` or `setVolume`;
  - no `PLAYING` or `PLAYBACK_BLOCKED` events;
  - no way to know whether pictures are arriving, or whether the viewer unmuted inside Twitch's own
    controls;
  - `muted` can only be chosen when the player loads (T12);
  - moving or removing an iframe reloads it (HTML's iframe removal steps).
- **Where it can run:** https, port 443, a DNS name, or localhost (T14). At least 400×300, and nothing
  over it (T11, T12).
- **Mattermost allows the frame** (M1), with an empty Permissions-Policy (M2), so the iframe carries
  `allow="autoplay; fullscreen"`. The desktop app allows http and https subframes, and asks once per
  origin for fullscreen (M8).

### 6.2 Can this page embed? (`webapp/src/twitchLogic.ts`, pure)

```ts
export type EmbedHost = {ok: true; host: string} | {ok: false; reason: string};

// From the page's own location, because the parent Twitch checks is the
// frame's real ancestor, whatever the Site URL says.
export function embedHostFor(loc: {protocol: string; hostname: string; port: string}): EmbedHost;
```

The rules are T14 and §2.3:

1. `hostname` is `localhost` or `127.0.0.1`: ok.
2. `protocol !== 'https:'`: "Twitch's player only runs on https:// pages."
3. `port` is neither `''` nor `'443'`: "Twitch's player only runs on the standard https port (443)."
4. `hostname` is an IPv4 literal, or contains `:`, or starts with `[`: "Twitch's player does not run
   on a site reached by IP address."
5. Otherwise `{ok: true, host: hostname.toLowerCase()}`.

`twitchEmbed.tsx`, which the lab swaps (§11.3), is the one module through which the webapp touches
anything outside Mattermost. It exports:

```ts
export function embedHost(): EmbedHost;               // embedHostFor(window.location)
export const TwitchFrame: React.FC<TwitchFrameProps>; // §6.3
// The Continue on Twitch window (§4.9): window.open(url, '_blank',
// 'popup=yes,width=520,height=760,noopener'). Its value is not used.
export function openConnectWindow(url: string): void;
// A link the viewer chose: window.open(url, '_blank', 'noopener,noreferrer').
export function openOutside(url: string): void;
```

A Mattermost page that is itself framed by another site (Teams) is not supported, because `parent`
would need every ancestor (§15).

### 6.3 The iframe

```ts
// twitchLogic.ts (pure): the only way a player address is made. Built with
// URLSearchParams, parameters in this order:
// https://player.twitch.tv/?channel=<login>&parent=<host>&muted=<true|false>&autoplay=true
export const TWITCH_PLAYER_PREFIX = 'https://player.twitch.tv/?';
export function frameURL(login: string, host: string, muted: boolean): string | null; // null for a bad login

// twitchEmbed.tsx
export type TwitchFrameProps = {
    login: string; host: string; muted: boolean;
    width: number; height: number; reloadKey: string; title: string;
};
```

The real `TwitchFrame` renders an `<iframe>` **only** when `frameURL` returned a string that starts
with `TWITCH_PLAYER_PREFIX`, and nothing otherwise. It has no `srcDoc` path at all: a `srcdoc` frame
is same-origin with Mattermost, and with `allow-scripts allow-same-origin` it could reach
`parent.document` and remove its own sandbox, so that combination must never exist in the production
bundle. The lab's mock `TwitchFrame` renders its stand-in in its own element (§11.3). Rendered as:

```tsx
<iframe
    key={reloadKey}            // `${handle}|${muted ? 'm' : 's'}|${reloads}`: a new key reloads it
    className='vntv-twitch-frame'
    src={url}                  // frameURL(…), prefix-checked
    width={width}
    height={height}
    allow='autoplay; fullscreen'
    allowFullScreen={true}
    sandbox='allow-modals allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox'
    referrerPolicy='strict-origin'
    scrolling='no'
    frameBorder={0}
    title={title}              // `Twitch player: ${stream.title}`
/>
```

- `login` must match `^[a-z0-9_]{1,25}$`, checked by `validLogin` in `twitchLogic.ts`. Anything else
  renders the plate `blocked` and builds no URL.
- The `sandbox` and `allow` lists are Twitch's own (T15), without top navigation. **What that lets the
  player do**, and what the README says (§10.2): play sound and autoplay; go fullscreen; show browser
  dialogs (`alert`/`confirm`/`prompt`, through `allow-modals`, which frames nested inside the player,
  ad frames included, inherit); open windows of its own, which are **not** sandboxed
  (`allow-popups-to-escape-sandbox`), and in the desktop app that includes the session-carrying
  in-app popup for any `/plugins/` address (M8). It cannot navigate the Mattermost page (no
  `allow-top-navigation`) or read it (cross-origin), and nothing in Mattermost or VillaNET acts on its
  `postMessage`s (Mattermost's own listener checks the origin, `MM v9 utils/browser_history.tsx:24-26`,
  and VillaNET has no message listener). Dropping `allow-modals` is a §13.2 check, weighed against the
  agreement's rule against limiting the player.
- **`referrerPolicy='strict-origin'`.** Mattermost's page sends no referrer at all (M2). Whether
  Twitch's player needs one is UNVERIFIED (Appendix A). `strict-origin` sends only
  `https://<host>/`, which the `parent` parameter already tells Twitch, and never the team or channel
  path. Twitch's own script would have sent the full page URL (T15). If Victor's check (§13.2) shows the
  player works with `no-referrer`, change this one constant (`TWITCH_REFERRER_POLICY` in
  `twitchEmbed.tsx`) to that.
- **Nothing** is positioned over the iframe: no HUD, no tuning screen, no noise, no plate, no focus
  ring overlay. The tile's controls live in a bar above it (§6.4). This is T11's "not obscured" rule.

### 6.4 The tile: `components/TwitchPlayer.tsx`

`Sidebar.tsx` renders `stream.source === 'twitch' ? <TwitchPlayer …/> : <Player …/>` with the same
props: `stream`, `audible`, `onAudio`, `onClose` and `closing`. The tile wrapper gets
`data-source="twitch"`, and **a Twitch tile always spans the whole grid row**
(`.vntv-tile[data-source="twitch"] { grid-column: 1 / -1; order: 1; }`), so it gets the panel's full
width whatever the grid's column count. `order: 1` puts Twitch tiles after Broadcast Box tiles, so in
the two-column grid (`@container (min-width: 620px)`) a list like [Broadcast Box, Twitch, Broadcast
Box] does not leave a half-row gap; with an odd number of Broadcast Box tiles one half-row gap is
unavoidable. **The DOM order is never changed** to achieve this, because moving an iframe in the DOM
reloads it.

```
.vntv-twitch-tile
├── .vntv-twitch-bar      name · [Twitch] · "{n} on Twitch"  ……  [🔈/🔊] [↻] [Play bigger] [Watch on Twitch ↗] [×]
└── .vntv-twitch-stage    (measured)  → <iframe> | plate
```

**Size.** The stage's width is measured with a `ResizeObserver` in a layout effect, falling back to
`window` resize. `twitchLogic.ts`:

```ts
export const TWITCH_MIN_W = 400;
export const TWITCH_MIN_H = 300;
// The extra width needed to switch a small tile to inline: a resize that
// wobbles around 400 must not flip between the two and reload the player.
// Only upwards, because Twitch's minimum is a floor that may never be broken.
export const TWITCH_INLINE_HYSTERESIS = 8;
// null when the player may not run at this width. wasInline is the tile's
// current mode, so leaving inline happens below 400 and entering it at 408.
export function inlineSize(width: number, wasInline: boolean): {w: number; h: number} | null {
    if (width < TWITCH_MIN_W + (wasInline ? 0 : TWITCH_INLINE_HYSTERESIS)) return null;
    const w = Math.floor(width);
    return {w, h: Math.max(TWITCH_MIN_H, Math.round(w * 9 / 16))};
}
```

At exactly 400 px wide the player is 400×300, and Twitch's player letterboxes 16:9 inside it. The
grid's `--vntv-tile-max` height cap does not apply to a Twitch tile.

**What that means in practice.** The stage is the panel less 24 px of body padding, any scrollbar and
the tile border (M9), so inline play needs a panel about **440 px** wide. Mattermost allows that only
in windows 1201 px and wider (where VillaNET's `rhsWidth.ts` widens the panel); below that the panel
is at most 400 px and a Twitch stream always plays in the theatre. The help text, the README and
§13.2 say so, and §6.9 opens the theatre directly in that case rather than adding a tile that needs a
second click.

**Modes,** checked in this order:

| Mode | When | Stage shows | Bar buttons |
|---|---|---|---|
| `off` | `!snapshot.twitchEmbed` | Plate: "Twitch streams open on Twitch." | Watch on Twitch, × |
| `blocked` | `embedHost()` not ok, or a bad login | Plate: "Twitch's player can't run on this address." For admins, plus the reason. | Watch on Twitch, × |
| `theatre` | The theatre is open for this handle | Plate: "Playing in the bigger player." with **Bring it back** (closes the theatre) | Watch on Twitch, × |
| `small` | `inlineSize(width, mode === 'inline') === null` | Plate: "Too narrow for Twitch's player, which needs 400 × 300." | Play bigger (if `theatreSize` allows), Watch on Twitch, × |
| `inline` | Otherwise | The iframe | Sound, Reload, Play bigger (if allowed), Watch on Twitch, × |

**The plate** (`.vntv-twitch-plate`) is the relayed preview (when `livePreviews` and `previewVersion`),
or the person's picture, darkened, with the sentence and its buttons over the **picture**. It never
sits over an iframe: in a plate mode there is no iframe.

**Bar controls:**

- **Sound** (`aria-pressed={audible}`, label "Sound (reloads Twitch's player)"):
  `onAudio(audible ? null : handle)`.
- **Reload** (label "Reload Twitch's player"): increments `reloads`. Because nothing can be detected
  through a cross-origin frame, this is the tile's only Retry.
- **Play bigger:** `openTheatre(handle)` from `twitch.ts`, and inside the same click `onAudio(handle)`,
  so the bigger player starts with sound.
- **×** closes the tile, as on `Player`.

The bar is in normal flow (not absolutely positioned), always visible, and its focus rings stay inside
the bar. It must fit a 400 px stage: the name truncates with an ellipsis (`min-width: 0`,
`text-overflow: ellipsis`), the "{n} on Twitch" count is hidden when the tile is narrower than 480 px
(a container query on `.vntv-twitch-tile`), and the five buttons are icon-only, each with
`aria-label` and `title` carrying the words above.

### 6.5 Sound

- The rule stays "one stream holds the sound": the watch list's `getAudio()` (`watching.ts:83`).
  `takeSound` and `setAudio` already work for a handle with no pool session (`sessions.ts:612-630`
  falls through to `setAudio`, and the Twitch tile is in the watch list).
- An inline Twitch iframe loads with `muted = getAudio() !== handle`. **A change of the sound owner
  reloads it**, because `muted` is in its key. Taking the sound reloads it unmuted, inside the click,
  so the page has a user gesture and the iframe has `allow="autoplay"`. Losing the sound reloads it
  muted. The bar's tooltip says so, and the README warns that a reload can replay Twitch's pre-roll ad.
- Broadcast Box tiles are muted through `applyAudioToAll` as today, the moment the Twitch tile takes
  the sound.
- **What cannot be enforced:**
  - a viewer can unmute inside Twitch's own controls, and VillaNET cannot see it, so two streams can
    then be audible;
  - a browser can refuse autoplay with sound in the frame (Firefox's per-site setting), and Twitch's
    player then shows its own "click to unmute".

  The README says both. There is no `blocked` state for Twitch tiles, because nothing reports one.
- New tiles start muted, as Broadcast Box tiles do (`watching.ts` never hands the sound to a tile on
  its own).

### 6.6 The theatre: `components/TwitchTheatre.tsx`

A VillaNET overlay that gives the player room when the panel cannot.

- **State:** module store in `twitch.ts`: `getTheatre(): string | null`, `openTheatre(handle)`,
  `closeTheatre()`, `useTheatre()` (the `useObservable` pattern; no React 18 APIs).
- **Rendering:** rendered by the `TwitchPlayer` whose handle it is, through
  `createPortal(…, document.body)`, as `LeaderboardModal` does (`M LeaderboardModal.tsx:757`). The
  root is `className='vntv vntv-modal vntv-twitch-theatre'`, so it reuses the modal's backdrop and
  reset. A mouse-down that starts and ends on the backdrop closes it. Escape closes it, using the
  capture-listener pattern in `LeaderboardModal.tsx:417-434`. Focus is trapped with `focusTrap.ts`.
  Closing returns focus to the **Play bigger** button.
- **Card:** `role="dialog" aria-modal aria-label="Twitch player: {title}"`. A header bar with the name,
  the Twitch tag, **Watch on Twitch ↗** and a Close button. Under it the iframe, sized by:
  ```ts
  export const THEATRE_MAX_W = 1280;
  export const THEATRE_CHROME = 120;   // header and margins, px
  // null when even the whole window is too small for Twitch's minimum.
  export function theatreSize(innerW: number, innerH: number): {w: number; h: number} | null {
      const w = Math.min(innerW - 32, THEATRE_MAX_W);
      const h = Math.min(Math.round(w * 9 / 16), innerH - THEATRE_CHROME);
      return w >= TWITCH_MIN_W && h >= TWITCH_MIN_H ? {w, h} : null;
  }
  ```
  It is recomputed on window resize. If it becomes null, the theatre closes.
- **Lifetime:** one theatre at a time. It closes when its tile unmounts: the panel going away (a
  thread), the tile closing, or the stream ending. The theatre's iframe is a different element from
  the tile's, so opening the theatre removes the tile's iframe (mode `theatre`). Only one Twitch
  player per stream ever runs.
- **Sound:** the theatre's iframe uses the same `muted = getAudio() !== handle` rule and key.
- **Phones:** a phone-sized window (`theatreSize` null) gets no Play bigger, only Watch on Twitch.

### 6.7 Grid, mini-player, leaving the panel, popouts, fullscreen

- **Grid:** Twitch tiles count toward `MAX_WATCHING` (4), like any watched handle. The leave animation
  (`data-leaving`, position only) never removes the element, so the player keeps running while it
  slides out.
- **Pool:** `sessions.ts` `mayCreate` (`:426-430`) also requires the stream not to be Twitch, so
  `mountVideo` never makes a `<video>` or a WHEP exchange for a Twitch handle. (The server refuses one
  too, §8.)
- **Mini-player and leaving the panel:** a Twitch stream is **never** held or shown in the mini-player.
  This does **not** hold by itself: `latchCandidate` (`M awayLogic.ts:377-383`) returns the sound
  holder whenever it is watched, without asking whether it has a pool session, so a Twitch tile with
  the sound would be latched, the Broadcast Box tile would get no mini-player, and `hold()`
  (`M away.ts:322-327`) would do nothing because the latched handle has no session, so the Broadcast
  Box stream would be torn down. So:
  - `twitch.ts` keeps `isTwitchHandle(handle)`, backed by a set of every handle it has seen with
    `source === 'twitch'` (filled from each snapshot and never emptied while the handle is in the
    watch list), so a Twitch stream that has just left the snapshot still counts as Twitch;
  - `latchCandidate` (pure, in `awayLogic.ts`) gains a fourth parameter,
    `excluded: (handle: string) => boolean`, and first narrows the list:
    `const playable = watching.filter((h) => !excluded(h))`; the sound holder is returned only if it
    is in `playable`, otherwise the first connected, otherwise the first of `playable`. `away.ts`
    `candidate()` (`M :309`) and `relatch()` (`M :731`) pass `isTwitchHandle`;
  - `MiniPlayer.tsx` returns nothing when `isTwitchHandle(latched)`, as a second guard (testing the
    snapshot entry alone would miss a stream already gone from the snapshot, which `MiniPlayer` still
    shows, `M MiniPlayer.tsx:177-178`);
  - `check-away.mjs` gains the case "the sound on a Twitch handle plus a connected Broadcast Box
    handle latches the Broadcast Box handle".

  When a thread takes the panel, the Twitch iframe unmounts and stops, and coming back loads it again.
  Keeping a Twitch player playing hidden would break T11 ("not obscured") and the agreement's autoplay
  terms.
- **Popouts:** VillaNET in Mattermost's RHS popout window (`popout.ts`) is on the same host, so `parent`
  is the same and nothing changes.
- **Fullscreen:** the player's own button (`allowfullscreen`). VillaNET's frame fullscreen is not
  offered on a Twitch tile. The desktop app asks once for `https://player.twitch.tv` in views (M8).

### 6.8 What a Twitch tile never has

| Feature | Why |
|---|---|
| Clips ✂ and clip tags | §9; Schedule 1 `:298` |
| Preview stills (`stills.ts`) | A cross-origin frame cannot be drawn to a canvas, and Twitch content must not be copied. `stills.ts` `candidates` (`M :170`) adds `&& !stream.source`. |
| Faces and "Others" | No server-visible session to count. |
| Top Snoopers credit | It cannot be observed by the server. A browser heartbeat could be faked. The `TopWatchers` help says so. |
| HUD, Tuning screen, No Signal noise, Paused overlay | They would cover the player (T11), and their signals do not exist. |
| Volume slider | Twitch's own control is inside the player. |
| VillaNET's frame fullscreen | The player has its own. |

### 6.9 Starting to watch: `watchOrOpen` (`twitch.ts`)

```ts
export function canWatchInPanel(stream: Stream, snapshot: Snapshot): boolean {
    return stream.source !== 'twitch' ||
        (Boolean(snapshot.twitchEmbed) && embedHost().ok && validLogin(stream.twitchLogin || ''));
}

// What a card, the popover or Up next does when somebody asks to watch.
export function watchOrOpen(stream: Stream, how: 'toggle' | 'only' | 'add'): void;
```

- **When `canWatchInPanel` is true:** call the existing `toggleWatching`, `watchOnly` or `watchStream`
  as before. Then, for a Twitch stream that is now being watched, if `inlineSize(stageWidth(), false)`
  is null and `theatreSize(innerWidth, innerHeight)` is not, **open the theatre in the same click**
  (`openTheatre(handle)` and `onAudio(handle)`, as Play bigger does), so a narrow panel never needs a
  second click and the sound still has its user gesture. `stageWidth()` is the width a full-row tile
  would get, kept in `twitch.ts` from a `ResizeObserver` on the grid (or the panel body when nothing is
  watched). The tile itself shows the `theatre` plate.
- **Otherwise:** `openOutside(channelURL(login))` (§6.2), where `channelURL(login)` is
  `https://www.twitch.tv/${login}`. In the desktop app that opens the system browser (M8). This is a
  link the viewer chose, which the lockdown promise has always allowed.

### 6.10 Fallbacks

| Situation | What happens |
|---|---|
| The admin's switch is off | Cards, badge and airtime still work. Watching opens twitch.tv. |
| Mattermost on a custom port, http or an IP | `embedHost()` fails. Cards open twitch.tv. TwitchSetup tells the admin why (§2.4). |
| A reverse proxy adds a CSP with `frame-src` or `child-src` | Browsers enforce both policies, the frame is blocked, and the tile shows Twitch's or the browser's error inside the stage. The README's lockdown note tells the admin to check with the `curl -sI` line in §10.3. Watch on Twitch ↗ stays in the bar. |
| Twitch refuses the domain (a `parent` error) | Twitch's player shows its own error inside the frame. VillaNET cannot detect it, so Watch on Twitch ↗ is always in the bar. |
| The desktop app | The iframe is allowed, autoplay works, and fullscreen asks once (M8). Connect uses the in-app popup (M8); `window.open` returns null there, which §4.9 expects. The popup uses the default session, so signing in to Twitch there also signs the app's embedded players in to Twitch (the help text and README say so). |
| A window narrower than 1201 px | The panel is at most 400 px, so Twitch plays in the theatre, opened by the same click (§6.4, §6.9). |
| Mobile app | Mattermost's mobile app does not run webapp plugins. N/A. |

### 6.11 React 17

> **Amended 2026-10-05 (Mattermost 11.11 only).** `min_server_version` is now 11.11.0, and plugins
> run on Mattermost 11.11's React 18.2.0, on a legacy root (`ReactDOM.render`) unless the server's
> `EnableConcurrentReact` feature flag is on. The lab carries React 18.2.0 and mounts both ways
> (**React root**), and `build/check-react.py` holds the pin to what Mattermost ships at
> `min_server_version`. React 18.2's APIs exist, but the code's own patterns (id counters, hand-made
> subscriptions) stay unless a new API simplifies something real. React 18.2 uses the same iframe prop
> names as 17. **Rechecked at v11.11.1 on 2026-10-05** (`server/channels/web/handlers.go:251-267`,
> `webapp/channels/webpack.config.js:313-321`): the page CSP is still `frame-ancestors 'self' …;
> script-src 'self'…` with no `frame-src`, `child-src` or `default-src`, so a `player.twitch.tv`
> iframe is allowed and Twitch's script is blocked. M3/M4 (`plugin_requests.go:173-290`: token
> order, stripped headers and `access_token`, CSRF skipped for GET and for non-cookie auth, a failed
> CSRF check leaves the request unauthenticated), M5 (`login.go:299-308`, no SameSite), M9
> (`_sidebar-right.scss:14-45`, `resizable_sidebar/constants.ts`), M13 (`ImageProxySettings.Enable`
> defaults false, `config.go:4064-4067`), M14 (`session.go:264-266`) and M15 (`channels.go:240-243`)
> hold unchanged. Mattermost Desktop's latest release is still v6.3.0, so M8 stands.
> The rest of this section is as written for 9.3.0; read "React 17" as "React 18.2.0".

The minimum server is 9.3.0 since Clips (M12), and plugins still run on React 17 there.
`createPortal` and `ResizeObserver` (a browser API) are fine. Do not use `useId`,
`useSyncExternalStore` or the other APIs listed at the top of media-lookups.md. Iframe attributes
use React 17 names: `allowFullScreen`, `frameBorder`, `referrerPolicy`, `scrolling`; `allow` and
`sandbox` pass through as given. `build/check-react.py` must still pass, and the lab carries
React 17.0.2.

---

## 7. Airtime

### 7.1 Data: a separate, deletable Twitch ledger

| KV key | Shape | Keyed by |
|---|---|---|
| `twitchair_totals` | `statTable` (`B stats.go:26-31`) | Twitch user id |
| `twitchair_month_YYYY-MM` | `statTable` | Twitch user id |
| `twitchair_sessions` | `sessionMarks` (`B stats.go:33-49`) | Twitch user id |
| `twitchair_paid` | `map[mmUserID][]paidSpan` (§7.3) | Mattermost user id |

**Why not inside `stats_totals`.** Twitch hours are Twitch Data and must be deletable on request
(§1.3). The Broadcast Box tables are built never to lose anything: their activation backup "only ever
grows" and a restore keeps the larger figure per key (`B statsstore.go:12-36`, `:324-423`). A deleted
Twitch row would come back on the next restore. A key namespace inside them would also have to get
past `validStreamKey` on import (`B airtime.go:393-394`) and could collide with a Broadcast Box name.
A separate ledger leaves the Broadcast Box history and its guarantees exactly as they are. It is not
under `stats_`, for the same reason the watching tables are not (`B plugin.go:117-119`).

`statEntry.Title` holds the person's display name, as Broadcast Box rows do. Keys are Twitch ids,
which are not secret. The admin airtime export, import, backup and restore stay **Broadcast Box only**
(§7.12).

### 7.2 Recording Twitch time: `recordTwitchTime(listed []twitchLive, cfg, now)`

Called by the Twitch loop after a good read (§5.2 step 6), never by `pollOnce`. It mirrors
`recordStreamTime` (`B stats.go:218-477`). Same order, same refusals:

1. Return if `!cfg.EnableLeaderboard`, if `!cfg.twitchLiveOn()`, or if the Twitch ledger is halted
   (§7.6).
2. Lock `statsWriteLock`.
3. Load `twitchair_totals` and `twitchair_sessions` with `loadStats` and `loadSessions` semantics,
   using the Twitch keys:
   - an undecodable table is quarantined and halts **only** the Twitch ledger (`twitchHaltOrSkip`,
     modelled on `watchHaltOrSkip`, `B watchtime.go:92-110`);
   - a KV error skips this pass.
4. **Evidence of watching** is Twitch's own: `observedAt = max(lastObserved(marks), store.creditAt)`,
   then `store.creditAt = nowMS`. Nothing here reads or moves `p.lastPollAt`, which stays Broadcast
   Box's evidence (`B plugin.go:429-433`).
5. Load `twitchair_paid` (§7.3). A KV error skips the pass. An undecodable document is quarantined and
   replaced with an empty one, which is the same as a restart, as `loadSessions` does.
6. For each listed stream, with `key = TwitchID` and `owner` = its Mattermost user from the cached link
   document (skip it if there is none):
   - Parse `StartedAt`, compute `elapsed`, and skip one that is unparsable or in the future.
   - A new mark, or one with a different `Start`:
     `Credited = max(claimable(elapsed, nowMS, observedAt), secondsFrom(start, link.At))`, where
     `secondsFrom` is the seconds between the stream's start and the moment the person connected,
     when that is later. **Time from before someone connected never counts** (§7.5).
   - Resync, `maxCreditPerPoll`, and the `maxTrackedStreamers` cap work exactly as the Broadcast Box
     loop does them. The resync epoch is `p.twitchResyncEpoch(nowMS)` (§7.5).
   - A link marked 24/7 (§4.5) is treated as the Broadcast Box loop treats a paused stream: the mark
     is set to `elapsed`, nothing is credited, and nothing is marked paid.
   - The interval is `[start+Credited, start+elapsed)`. Credit only its **unpaid parts** (§7.3), each
     through `months.creditSpan` on the Twitch month prefix. Add the parts' seconds to the totals.
     Record each credited part as paid **by this Twitch id** (`withPaid(…, twitchID)`); parts already
     paid keep their payer. Advance the mark to `elapsed` either way.
7. Marks not seen for 30 minutes are swept, as in `B stats.go:418-440`.
8. Write in this order: totals, then months, then `twitchair_paid`, then marks. A failure at any step
   stops the later ones, which is the same "replay rather than lose" rule as `B stats.go:442-476`.
   After the months (and before their error is checked, as 2.11.0 does), call
   `p.forgetMonthPodiums()` if `months.reachesBeforeMonth(now)`. **Not** `reachesBefore(monthBucket(now))`:
   `reachesBefore` compares whole KV key strings (`B stats.go:191-199`), `monthBucket` gives
   `stats_month_YYYY-MM`, and every `twitchair_month_…` key sorts after every `stats_month_…` key, so
   that check would never fire for Twitch. `reachesBeforeMonth(now)` compares against
   `m.prefix + now.UTC().Format("2006-01")`; the Broadcast Box and watching callers switch to it too,
   which is the same comparison they make today.

### 7.3 Paid spans: each second paid once per person, Broadcast Box first

When Broadcast Box and Twitch both carry the same person at the same moment, a sum per person would
pay that minute twice. Marks are per source, so they cannot express "once per person per second". A
small per-person record of what has already been paid, and **by whom**, can.

**Broadcast Box is authoritative.** Its time is always credited in full, exactly as 2.11.0 does, and
only the Twitch ledger ever gives seconds back. The first version of this design let whichever pass
ran first keep a shared second. Because Twitch reads once a minute up to *now* while Broadcast Box
polls every 15 s, each Twitch read would have taken the seconds since the last Broadcast Box poll
(about 7.5 s a minute, roughly 12% of every simulcast, and hours after a restart or an outage) into
the deletable Twitch ledger only. Disconnecting, switching Twitch off, or Delete all Twitch data would
then have removed time the person really streamed on Broadcast Box, and the Broadcast Box export and
backup would never have had it.

```go
// paidSpan is a stretch of wall-clock time, in Unix seconds, [From, To), that
// has already been credited to a person. Tw is the Twitch id that was paid
// for it, or "" when Broadcast Box paid for it.
type paidSpan struct {
    From int64  `json:"f"`
    To   int64  `json:"t"`
    Tw   string `json:"tw,omitempty"`
}

// unpaidParts is [from, to) less everything in paid, in order. Pure.
func unpaidParts(from, to int64, paid []paidSpan) []paidSpan

// twitchPaidParts is the parts of [from, to) that a Twitch id paid for, in
// order, each carrying its Tw. Pure.
func twitchPaidParts(from, to int64, paid []paidSpan) []paidSpan

// withPaid records [from, to) as paid by tw ("" for Broadcast Box). Whatever
// it overlaps is overwritten (only Broadcast Box ever overwrites: the Twitch
// pass adds only unpaid parts), and spans that touch with the same payer
// merge. Pure.
func withPaid(paid []paidSpan, from, to int64, tw string) []paidSpan

// prunePaid drops spans ending before cutoff and, beyond max, the oldest. Pure.
func prunePaid(paid []paidSpan, cutoff int64, max int) []paidSpan

// debitSpan takes [from, to) back from key, split across the months it
// covers like creditSpan, never below 0 in any table. Pure apart from the
// month tables it loads.
func (m *monthTables) debitSpan(key string, from, to time.Time) error
```

| Constant | Value | Why |
|---|---|---|
| `twitchPaidKeep` | 31 h | Nothing can be credited further back than `maxCreditPerPoll` (30 h, `B plugin.go:79`), so older spans can never matter. |
| `maxPaidSpans` | 96 | Per person. A day of separate sessions, with payers alternating, fits. |

**Both passes use it, but only for people with a Twitch connection, and only while the switch is on.**
Which Broadcast Box streams count as a connected person's is decided by **one** function shared with
the boards: `boardOwner(stream)` is the `UserID` only when `stream.LinkSource == linkSourceManual`,
because the boards attribute only manually linked keys (`identityResolver.user` reads `link_key_`
only, `B stats.go:497-511`). A stream matched by username (`AutoLinkByUsername`, `B poller.go:431-437`)
stays the board row `"k:"+key`, so splitting its seconds with the person's `"u:"` Twitch row would
put half a simulcast on each of two rows.

- **Twitch pass** (§7.2 step 6): credits only `unpaidParts`, and records them with `Tw = twitchID`.
- **Broadcast Box pass** (`recordStreamTime`): before the loop,
  `people, err := p.twitchLive().peopleIfOn(cfg)` (an empty map when the switch is off); an error
  skips the whole pass (§5.2, "the first read"). For each stream whose `boardOwner` is in `people`:
  1. credit the interval **in full**, exactly as today;
  2. find `twitchPaidParts` of the interval: seconds the Twitch ledger already holds for this person
     (a Twitch read that landed first, or a Broadcast Box backlog after a restart or an outage);
  3. for each such part whose `Tw` is still this person's connected Twitch id and is not being
     purged, `debitSpan` it from `twitchair_month_…` and subtract it from `twitchair_totals[Tw]`,
     clamped at 0 (a part of another id is that id's to lose in its purge);
  4. `withPaid(…, from, to, "")`, which turns those parts into Broadcast Box's.

  `twitchair_paid` is loaded only when a stream is in `people`, and `twitchair_totals` and the Twitch
  months only when step 2 found something; all of it before anything is written, so a KV error still
  skips the whole pass. Write order: Broadcast Box totals, Broadcast Box months, Twitch totals, Twitch
  months, `twitchair_paid`, then the Broadcast Box marks. The same "replay rather than lose" rule
  applies: a failure stops the later writes, the marks are not advanced, and the worst case is the
  existing one of one poll interval counted twice (or, if only the Twitch months failed, one interval
  taken back from Twitch twice, clamped at 0). Both `forgetMonthPodiums` checks
  (`reachesBeforeMonth`) run, one per month set.
- **While the Twitch ledger is halted** (§7.6), step 3 is skipped and step 4 still runs. No new Twitch
  time is paid while halted, so the only double count possible is the last Twitch read before the
  halt (at most a minute).
- **With the switch off, or nobody connected, `recordStreamTime` does exactly what 2.11.0 does and
  reads no new key.** A test pins this (§11.1).

**Why this is correct in every order.**

- **Same minute:** Twitch at 10:01:00 pays `[10:00:45, 10:01:00)` (the part since Broadcast Box's last
  poll). Broadcast Box at 10:01:00.2 credits `[10:00:45, 10:01:00.2)` in full, takes 15 s back from
  Twitch, and records it as its own. Each second is counted once, and it is a Broadcast Box second.
- **After a restart:** both sources backdate. Broadcast Box `[10:00, 11:00)` and Twitch
  `[09:40, 11:00)`: if Twitch runs first it pays 80 minutes and Broadcast Box then takes back 60;
  if Broadcast Box runs first Twitch pays only the 20 outside it. Either way: 60 on Broadcast Box,
  20 on Twitch, 80 in all.
- **Broadcast Box down for 10 minutes** while Twitch was credited: the Broadcast Box backlog on
  recovery credits those minutes to Broadcast Box and takes them back from Twitch.
- **Disconnect after a 10-minute simulcast:** the purge removes the Twitch ledger, and the person
  keeps all 10 minutes, on Broadcast Box.
- **Nothing is ever paid twice**, apart from the replay and halt cases above. Time can be missed only
  if a span older than 31 hours were somehow replayed, which `maxCreditPerPoll` rules out.

### 7.4 What this does not change

The Broadcast Box tables keep their keys, schema v1, backup, restore, export, import, quarantine and
halt, and every second Broadcast Box saw is credited to them in full, exactly as in 2.11.0, whatever
Twitch does. A person's two sources stay apart on disk. They meet only when the boards are built
(§7.8). Simulcast seconds are Broadcast Box seconds, so they are in the export and the backup, and
deleting Twitch data never removes them.

### 7.5 Backdating rules

- **Before connecting:** a mark never reaches back before `link.At` (§7.2). Connecting mid-stream
  starts the count at the moment of connecting, even after a restart, when `claimable` alone would
  allow the whole session.
- **Switching Twitch on:** `OnConfigurationChange` arms `twitchResyncAt` (`armTwitchResync`), modelled
  on `armStatsResync` (`B statsstore.go:451-476`) with its own field. Sessions running at that moment
  earn nothing for the time before it, once each, through the mark's `Synced` field.
- **Switching the leaderboard back on:** the existing `statsResyncAt` applies to Twitch marks too.
  `twitchResyncEpoch` returns the later of the two epochs.

### 7.6 Halting and quarantine

- `p.twitchAirHalt recordingHalt` (the type at `B watchtime.go:66-91`) is separate from `statsHalted`
  and `watchHalt`. **A damaged Twitch ledger never stops Broadcast Box hours being recorded**, and the
  reverse holds too.
- It clears only on a plugin restart, or through **Delete all Twitch data**.
- While halted, nothing is credited, debited or purged; a disconnect still removes the link, and its
  purge waits for the next activation (§4.6). Admins see the reason (§4.10, §2.4).
- The quarantine copy itself is Twitch Data that cannot be purged per person (§4.6), which TwitchSetup
  and the README say.

### 7.7 Purging

§4.6. Purges and the deletion of everything both call `forgetMonthPodiums`, so past-month medals are
recalculated on the next poll.

### 7.8 Boards by person

`addBoards` (`B poller.go:211-245`) builds every board from **rows by person**. One row per person,
not per key:

```go
// boardRow is one line on a board: a person, or a stream nobody has linked.
type boardRow struct {
    id      string      // "u:" + user id, or "k:" + normalised stream key
    user    *model.User // nil for a stream nobody has linked
    seconds int64
    twitch  int64       // of seconds, how many came from Twitch
    sortBy  string      // display name for a person; the stored title for a stream (as rankRows)
    title   string      // what the board shows
}

// boardRows merges one Broadcast Box table and, when tw is not nil, one Twitch
// table. owners maps Twitch id to Mattermost user id (the cached links).
func (p *Plugin) boardRows(bb, tw statTable, cfg *configuration, resolve *identityResolver, owners map[string]string) []boardRow
```

- **Broadcast Box rows** with seconds > 0: `resolve.user(key)` (`B stats.go:497-511`). A person becomes
  `"u:"+id`. Otherwise the row is `"k:"+key`, titled as `leaderboard` titles it today (override, never
  the key itself, "Unnamed stream").
- **Twitch rows** with seconds > 0: `owners[id]` and then `resolve.person(uid)`. A row with no owner
  (a purge still pending) or no account is **left out**. `owners` comes from `owners(cfg)` (§5.2),
  which loads the document itself on the first poll; on an error the previous boards are carried
  (§5.4).
- Rows with the same id are summed. `twitch` is the Twitch part.
- **Order:** seconds descending, then `strings.ToLower(sortBy)`, then `id`. A total order, as
  `rankRows` requires (`B stats.go:588-593`).
- `tw` is nil, and Twitch hours are left off every board, while `!cfg.twitchLiveOn()`. The ledger is
  kept and shows again when the switch comes back. Q3 asks Victor.

`leaderboard(rows []boardRow, limit int) ([]leaderEntry, int)` replaces the table-taking version.
`leaderEntry` gains `TwitchSeconds int64 \`json:"twitchSeconds,omitempty"\``. `AllTimeTotal` and
`MonthTotal` count rows, which is now people.

**This changes one existing behaviour on purpose, with or without Twitch.** Two Broadcast Box keys that
resolve to the same person now share one row. In 2.11.0 that showed one person twice, and every
webapp `.find` by `userId` picked the first. Only a stream key that is currently linked resolves
(`setLink` drops the old `link_key_`), so this only arises from hand-made KV data. The medals test
pinned to it changes (§7.10).

### 7.9 Medals on the cards

`rankByKey` becomes `rankByRow(rows []boardRow) map[string]int`, giving the top `podiumSize` places by
row id. For each card that is not 24/7:

- **Broadcast Box:** id `"u:"+u.Id` when `resolve.user(normalizeKey(StreamKey))` is someone, else
  `"k:"+normalizeKey(StreamKey)`;
- **Twitch:** id `"u:"+UserID`.

Use the same resolver as the boards in the same poll, so a card and the board can never disagree.

### 7.10 Past-month medals

`monthmedals.go` keeps the podiums by stream key, cut to 3 when read, and merges by user afterwards
(`B monthmedals.go:40-43`, `:138-173`, `:282-288`). That cannot merge two sources before the cut.
Change:

- The cache holds, per ended month, the whole tables: `monthTables{month string; bb, tw statTable}`.
  `tw` is read only while `cfg.twitchLiveOn()`, and the cache records `withTwitch`. A cache read
  without Twitch counts as stale once the switch is on. Months are still read by name from
  `firstPossibleMonth` (`readEndedPodiums`), Twitch months under `twitchair_month_`. The retry,
  generation and quarantine rules stay as they are.
- Each poll, `monthMedalTally(now, resolve, owners, twitchOn)` builds `boardRows(bb, twIfOn, …)` for
  each ended month, takes the first `podiumSize` rows, and gives Gold, Silver and Bronze to rows with a
  `user`, as today. Row ids are resolved each poll, so linking later still earns past medals
  (`B monthmedals.go:33-36`).
- Memory: tables are bounded by `maxTrackedStreamers` each. A few hundred rows in all is the realistic
  case.
- The extra cost per poll is nothing new: every Broadcast Box key in a month table is also in
  `stats_totals`, which the all-time board has just resolved through the same resolver.
- `TestMonthMedalsMergeTwoKeysOfOnePerson` (`B monthmedals_test.go:398-425`) keeps its name. Its
  expectation becomes Dave with Gold in 2026-05, 2026-06 and 2026-07, and Bob with Silver in all three
  (in July Dave's two keys add up to 1400 against Bob's 100). Its comment says why.

### 7.11 What the boards show

- One line per person: their name, total hours (Broadcast Box plus Twitch), and crowns and medals as
  before.
- When `twitchSeconds > 0`, the hours cell has `title="incl. {duration} on Twitch"`. The Trophies card
  says nothing else about sources.
- A Twitch-only streamer is a person like any other on the airtime boards.
- They are not on the Snoopers board, which counts only people with a Broadcast Box link (§4.8).

### 7.12 Admin airtime, export and backup

- `AdminAirtime`'s counts stay Broadcast Box only.
- Its panel gains one muted line, "Twitch hours are kept separately; see Twitch connections.", when
  the switch is on.
- The export, import, backup and restore never include the Twitch ledger. A file taken off the server
  would make Twitch Data impossible to delete when a person disconnects, and re-importing it would
  resurrect deleted hours. The README says that Twitch hours are not in the export, and why, and that
  minutes live on both at once are Broadcast Box minutes and so are in it (§7.3).

---

## 8. Everything that assumes "a stream is a Broadcast Box stream"

Rows follow the code map's §4 numbering where one exists. "Guard" means the code must check
`stream.Source == ""`, or `!stream.source` on the webapp.

| # | Place | Assumption | Decision |
|---|---|---|---|
| 1 | `poller.go` path A | No Broadcast Box means no streams, boards or badges | Twitch cards, boards and badges on path A while the switch is on (§5.4). |
| 2 | `poller.go` path B | The carry-over covers the whole list, and the boards are carried, not rebuilt | `broadcastBoxOnly` plus a fresh merge; Twitch cards survive the third failure; with Twitch on the boards are rebuilt (§5.4). |
| 3 | `poller.go` path C | The list is `decorate(raw)` | Merge after `recordWatchTime` and Clips' `recorders().reconcile` (§5.4). |
| 4 | `decorate` live/viewers/handle | All Broadcast Box | Twitch entries are built by `twitchStreams`, never by `decorate`. |
| 5 | `resolveUser`, `LinkSource` | Manual or username | New value `"twitch"`. Readers: `nowplaying.go:306`, `:433` (row 20) and Clips (§9). |
| 6 | MOTD, StreamNotes, default MOTD | Broadcast Box's note | The Twitch title, with the owner's token and key redacted. StreamNotes never apply. |
| 7 | `WatchURL` | Broadcast Box public site | Never set on Twitch. The link is built from `twitchLogin`. |
| 8 | Stills and watchers in `decorate` | Broadcast Box sessions | Not reached for Twitch. |
| 9 | Ranks by key | | By person (§7.8, §7.9). |
| 10 | `reconcileBadges` | | Unchanged; the merged slice (§5.5). |
| 11 | `streamKeyForHandle` (`B handles.go:73-85`) | Any handle maps to a key | Guard: skip entries with `Source != ""`. Twitch keys are empty anyway, which `handleWHEP` already treats as not live. |
| 12 | `handleWHEP` (`B api.go:642-709`) | Every handle is playable over WHEP | Refused through row 11 (404 "That stream is not live."). Test: a Twitch handle gives 404 and no request to Broadcast Box. |
| 13 | Watching and Top Snoopers | BB sessions; BB link | Unchanged by construction (they read the raw Broadcast Box listing, `M watchtime.go:185-230`). Help text updated (§10.1). |
| 14 | `/me` `streamKey`, `linkedLive`; `linkedKeyIsLive` (`B api.go:712-722`) | Broadcast Box | Guard, and return false when the wanted key is `""`. Otherwise `linkedKeyIsLive("")` would match a Twitch entry's empty key whenever a link read failed (`getLinkedKey` turns errors into `""`). |
| 15 | `recordStreamTime` | Broadcast Box marks and evidence | Credits exactly as before. For manually linked, Twitch-connected people while the switch is on, it also records paid spans and takes overlapping seconds back from the Twitch ledger (§7.3). |
| 16 | Owner lookup by `link_key_` | | Broadcast Box rows only; Twitch rows through the link document (§7.8). |
| 17 | `monthmedals.go` | Podiums by key | §7.10. |
| 18 | Airtime import | Keys look like stream keys | Untouched: Twitch is not in the export (§7.12). |
| 19 | `previewable` (`B stills.go:724-736`); `stills.ts:170`, `:197` | Every non-24/7 stream may have a still | Guard on both sides. Twitch has its own relayed picture (§5.7). |
| 20 | `nowplaying.go` | Only `LinkSource "manual"` is on air, or shows What's on? | `settleNowPlaying`'s `onAir` (`B :304-309`) and `nowPlayingFor`'s What's on? branch (`B :433`) accept `"manual"` **or `"twitch"`**. A Broadcast Box-linked person's What's on? shows on their Twitch card and settles on its end. `handleSetNowPlaying`'s `Aired` (`B :564`) is `linkedKeyIsLive(key) \|\| p.twitchLive().twitchLiveFor(userID)`. Setting What's on? still needs the Broadcast Box link (§4.8). |
| 21 | Planning and media search gates | Streamer = BB-linked | Unchanged (§4.8, Q4). |
| 22 | Clips | Recordable = not 24/7 | §9. |
| 23 | "Configured" and `error` | Mean Broadcast Box | Unchanged. The panel hides the "not set up" notice from non-admins while `twitchOn` (§5.9). |
| 24 | `types.ts` `Stream` | No source | §5.10. |
| 25 | `sessions.ts` | `<video>` and WHEP | `mayCreate` guard; Twitch tiles are a different component (§6.4, §6.7). |
| 26 | `Player.tsx`, `Hud.tsx` | An overlay may cover the picture | `TwitchPlayer` has no overlay (§6.3). |
| 27 | `LiveCard.tsx` | Still source, tags, viewers | §5.8. |
| 28 | `MiniPlayer.tsx` | The element can move | Twitch never goes there (§6.7). |
| 29 | `liveHandles`, `PopoverLive` | One live stream per person | Skip `coveredBy`, then prefer Broadcast Box (§5.9). A covered Twitch entry stays in the snapshot (§5.4, §5.6). |
| 30 | Link footer | The only link is the token | `TwitchConnect` in both states (§4.9). |
| 31 | Lab | Every stream is Broadcast Box | §11.3. |
| 32 | Docs | "Browsers never contact anyone but this server" | §10. |
| 33 | `ownOnAir` (`M Sidebar.tsx:346`) | Any source | Unchanged: a person live on Twitch is on air, so the What's on? chip behaves as it does when they are live on Broadcast Box. |
| 34 | `channelpics.go`, `presence.go`, `publish`, `watching.ts`, `popout.ts`, `whep.ts` | | No change. |
| 35 | `ClipsModal.tsx` `anybodyLive` (`M :289`, feeding the "Press ✂ on a stream…" hint at `:378`) | Anybody live can be clipped | `snapshot.streams.some((s) => !s.alwaysOn && !s.source)`: a Twitch-only live person is not "somebody live" for clipping. |
| 36 | `ClipViewer.tsx` `take`/`giveBack` (`M :74-90`) | The sound holder has a pool session | `take` still takes the sound from a Twitch holder (its player reloads muted: one reload). `giveBack` restores a Twitch holder knowingly when `isTwitchHandle(before)` and it is still watched, accepting a second reload; it does not require `getSessionInfo(before)?.status === 'playing'` for it, which a Twitch handle never has. If the browser then refuses sound without a click, Twitch's player shows its own unmute button. |
| 37 | Live now card body click (`onWatchOnly={watchOnly}`, `M Sidebar.tsx:1190`; `LiveCard.tsx:204`, `:301`) | Watching is always possible | Through `watchOrOpen(stream, 'only')` (§5.9). |
| 38 | `index.tsx` `uninitialize` (`M :401-402`) | One WebSocket handler | Also unregisters the `twitch_link` handler. |
| 39 | README "only outbound connection" sentences (`M README.md:682`, `:2133-2135`) and the "Airtime is the one thing here that cannot be rebuilt" section (`M README.md:1288`) | Broadcast Box, Mumble and lookups only; one airtime source | §10.2. |
| 40 | Live now list, `liveHandles`, `PopoverLive`, `anybodyLive` and any other "one card per person" reader | One entry per person | Skip `coveredBy` entries (§5.4). Tiles, the watch list and sessions ignore the flag. |

---

## 9. Clips (3.0.0): what it must refuse

These go in whether Clips lands before this or with it. The recorder (`clips_recorder.go`) is not
written yet, so these are build instructions, not fixes.

1. **Recorder eligibility** (spec line 424, `!s.AlwaysOn && s.Handle != ""`) becomes
   `s.Source == "" && !s.AlwaysOn && s.Handle != ""`. A Twitch stream never takes a slot, never opens a
   WHEP session, and is never written to disk (Schedule 1 `:298`).
2. **`addClips`**, and every later per-stream annotation (`clip`, `clipFrom`, `clipTail`), skips
   `Source != ""`. The scissors key off `stream.clip`, so they stay away from Twitch tiles, and
   `TwitchPlayer` has no ✂ anyway.
3. **Clip tails** come from the recorder, so Broadcast Box only.
4. **`requireClipsLink`, `identity.ts` and `streamTag`** stay Broadcast Box only (§4.8). A Twitch
   connection never satisfies them.
5. **`noteViewer`** runs only from `handleWHEP`, which Twitch never reaches.

Test: `TestTheRecorderNeverTakesATwitchStream`, in the Clips test file. It feeds a snapshot with a
Twitch entry and expects no slot and no WHEP call.

---

## 10. Words

### 10.1 plugin.json

- §2.1's three settings and the subtitle and help edits.
- **Every "no outside requests" promise outside the Twitch section** becomes true again with the
  switches on (Victor's condition: state the change honestly everywhere):
  - `MediaLookup` help (`M plugin.json:204` today; `design/media-lookups.md:1240-1251` after Phase 1):
    "Off by default: while it is off the plugin makes no outside requests at all beyond Broadcast Box
    and Mumble, and switching it off again restores exactly that." becomes "Off by default: while it
    is off the plugin makes no outside requests for lookups; beyond Broadcast Box and Mumble it then
    contacts only Twitch, and only if Show people live on Twitch is on. Switching it off again
    restores exactly that." (This is the edit Part E had planned, `media-lookups.md:1795-1799`, which
    this document replaces and so must carry.)
  - `EnableVoice` help (`M plugin.json:360`): "This is the only outbound connection the plugin makes
    besides Broadcast Box itself." becomes "Besides Broadcast Box, this is the plugin's only outbound
    connection unless an admin switches on Look up movies, shows and games or Show people live on
    Twitch."
  - Whatever Phase 1 has made of these two sentences by the time this is built, the result must name
    Twitch, which §11.1's words test checks.
- `EnableLiveBadge` help: replace "Only affects users who have linked their stream key." with "Only
  affects people who have linked their stream key, or connected their Twitch account while Show people
  live on Twitch is on."
- `EnableLeaderboard` help: append "While Show people live on Twitch is on, time live on Twitch by
  people who connected it counts too, added to their Broadcast Box time, and a minute spent live on
  both counts once."
- `TopWatchers` help: append "Watching a Twitch stream is never counted: VillaNET cannot see it."
- `LivePreviews` help: append "Twitch streams' pictures come from Twitch and are relayed by this
  server; this switch covers them too."
- The `footer`: "Streamers can link their stream key to their Mattermost account from the **Link my
  stream** button inside the plugin, and connect their Twitch account with **Connect Twitch**."

### 10.2 README.md

A new section **"Twitch (optional)"**, after "Movies and games (optional)". Draft, to be written in the
house voice:

> **Off unless an admin switches it on.** People who stream on Twitch can connect their Twitch account
> with **Connect Twitch** in the panel's footer. While they are live there they wear the live badge,
> have a card in Live now with their Twitch title, category, viewer count and picture, and their time
> live on Twitch counts toward the airtime boards and medals.
>
> **Setting it up.**
> 1. Register a Twitch application (see the Twitch section of the settings).
> 2. Paste its Client ID and Client Secret.
> 3. Add the OAuth Redirect URL that **Twitch connections** shows to the application. It is
>    `<Site URL>/plugins/ceo.villa.villanet/api/v1/twitch/callback`, and Twitch needs it to be https.
> 4. Switch on **Show people live on Twitch**.
>
> **Connecting.** Press Connect Twitch, read what it means, then press Continue on Twitch. A small
> window opens at Twitch, where you sign in and approve VillaNET. It asks for no permissions; it only
> proves the channel is yours. If Twitch shows the wrong account, sign out of Twitch in that window
> first. One Twitch account per person. If somebody else proves the same account, it moves to them,
> as a stream key does, and you see a notice in the panel saying so. If a Twitch connection fails,
> do not post the window's address anywhere: it can carry a one-time sign-in from Twitch.
>
> **What everybody sees, and what VillaNET keeps** (the privacy notice Twitch asks for). While you
> are live on Twitch, **everyone on this server** sees your Twitch title, category, viewer count and
> preview picture in Live now, your live badge, and your Twitch login (in the Watch on Twitch link);
> your Twitch hours count on the airtime boards and medals beside your Broadcast Box hours. VillaNET
> keeps your Twitch user id, login and display name, when you connected, and how long you have been
> live on Twitch since. The token Twitch issues is used once, to ask Twitch who you are, and revoked
> at once. VillaNET never keeps a Twitch password, token or e-mail address, and sets no cookie for
> this. Your stream's title, category, viewer count and picture are held in memory for a few minutes
> at most. **Disconnect** deletes all of it, including your Twitch hours, so the boards and past
> medals are recalculated without them; a minute you were live on Broadcast Box and Twitch at once is
> a Broadcast Box minute, so you keep it. Disconnect works even while an admin has Twitch switched
> off. **Removing VillaNET in Twitch's Settings > Connections does not disconnect you here** (VillaNET
> keeps no token, so it cannot see that); press Disconnect as well. One exception to "deletes all of
> it": if VillaNET ever found its Twitch data damaged, it set aside a copy for the admin, which may
> still hold your Twitch name and hours until the admin uses Delete all Twitch data (the admin's
> Twitch connections panel says when such a copy exists). Nothing is shared with anyone outside this
> Mattermost server.
>
> **What an admin controls.** Anyone signed in can connect. An admin can disconnect a person, block
> them from connecting again, or mark a channel 24/7 (tagged 24/7, no live badge, no airtime), in
> System Console > Twitch connections. Twitch no longer marks mature streams, so VillaNET cannot hide
> them; blocking is the remedy.
>
> **Watching inside VillaNET** (only with **Play Twitch streams inside VillaNET** on). The stream
> plays in Twitch's own player. Your browser then talks to Twitch and the services Twitch uses,
> including advertising and audience measurement, and Twitch may show ads. Twitch sees this server's
> address and your IP address and, if you are signed in to Twitch in that browser, who you are. In
> the desktop app, connecting Twitch signs Twitch in inside the app, so its players then know you
> too. Twitch's player needs a site on https at the standard port and a space at least 400 by 300,
> which in the side panel means a panel about 440 px wide: Mattermost allows that only in windows at
> least 1201 px wide. In a narrower window, watching a Twitch stream opens it in a larger player over
> the page in the same click. Moving the sound to or from a Twitch tile reloads Twitch's player,
> which can replay an ad. VillaNET cannot see Twitch's own mute button, so a Twitch player you
> unmuted yourself stays audible. Twitch streams are never kept playing in the small player while a
> thread has the panel. They cannot be clipped, give no preview stills, show no faces, and do not
> count on Top Snoopers.
>
> **What a Twitch connection is not.** It is not Link Your Account. Planning, What's on?, Top Snoopers
> and Clips stay for people who have linked a Broadcast Box stream.
>
> **Live on both?** You get one card, the Broadcast Box one, with an Also on Twitch link, one badge,
> and each minute counted once.

Also update:

- "How it works": a bullet saying that with Show people live on Twitch on, the server also asks
  `api.twitch.tv` once a minute and relays Twitch pictures, and that with Play Twitch streams inside
  VillaNET on, a viewer's browser loads Twitch's player.
- **`M README.md:682`** (Mumble): "This is the plugin's only outbound connection besides Broadcast Box
  itself — and, only if an admin switches lookups on, TMDB and Steam." becomes "This is the plugin's
  only outbound connection besides Broadcast Box itself — and, only if an admin switches them on, the
  title lookups (TMDB, Steam and IGDB) and Twitch live (Twitch)."
- **`M README.md:2133-2135`** ("Out of the box the plugin makes **no external network requests** at
  runtime beyond Broadcast Box and, when voice is on, Mumble. The one optional exception is Movies
  and games, off by default."): the optional exceptions become [Movies and games] and [Twitch], both
  off by default, followed by: "**Browsers contact somebody other than this server in exactly two
  cases, both off by default:** (1) the person who presses Connect Twitch and then Continue on
  Twitch, in the window that opens, reaches `id.twitch.tv` and Twitch's sign-in pages; (2) a viewer
  who presses play on a Twitch stream while Play Twitch streams inside VillaNET is on loads Twitch's
  player, which talks to Twitch and its advertising and measurement partners."
- `M README.md:358`: "The plugin server only ever talks to **Broadcast Box URL**, which should be a
  LAN address." becomes "For Broadcast Box, the plugin server only ever talks to **Broadcast Box
  URL**, which should be a LAN address." (Its other outbound connections, Mumble, the lookups and
  Twitch, are listed under the security notes.)
- "Airtime is the one thing here that cannot be rebuilt" (`M README.md:1288`): a paragraph that
  Twitch hours are a separate ledger, deletable by design (Twitch's terms), not in the export or the
  backup, and that simulcast minutes are Broadcast Box minutes, so they are.
- "Security posture":
  - "Nothing user-supplied reaches an outbound request path": Twitch ids and logins come from Twitch's
    own answers and are checked before use, and the callback's code goes only to `id.twitch.tv`.
  - New bullet: "Connecting Twitch cannot be started by a link, an image in a post or another site:
    the window needs a single-use ticket that only the panel can get, through a CSRF-protected
    request. It cannot be used to attach someone else's account either. The state is single-use, ten
    minutes, server-side, and bound to the user who started it; the return must carry that same
    user's interactive session (a bot, a personal access token or an OAuth app cannot connect); and
    Twitch's ID token must carry the nonce issued with that state, so a code that leaks cannot be
    redeemed by anyone else. No Twitch token outlives the request that received it."
  - New bullet, exactly: "**Twitch's player, when an admin allows it, runs in a frame with Twitch's
    own permissions:** it can play sound, autoplay, go fullscreen, show browser dialogs, and open
    windows that are not sandboxed (in the desktop app, including the app's built-in window for
    plugin pages, which carries the Mattermost session). It cannot navigate or read the Mattermost
    page, and nothing acts on messages it posts. It is sent only this server's origin as a referrer.
    Twitch sees this server's address and the viewer's IP address and, if the viewer is signed in to
    Twitch in that browser (in the desktop app, connecting Twitch signs it in), who they are."
- "How a person gets a red dot", "Airtime and medals" (by person, Twitch counted once, Broadcast Box
  first, past medals), "Live now", "The player" (Twitch tiles, the 1201 px window rule), "Top
  Snoopers" (Twitch not counted).

### 10.3 LOCKDOWN.md

In "What the plugin touches", after the Movies and games paragraph:

> **Twitch, only if an admin switches on "Show people live on Twitch"** (off by default). The
> Mattermost server then contacts `id.twitch.tv` (the application's access token; and, once per
> connection, exchanging that person's one-time code and then revoking the token it gave) and
> `api.twitch.tv` (once per connection to ask whose the token is; once a minute while anybody is
> connected, to see who is live; and once a day for their names). It fetches preview pictures from
> `static-cdn.jtvnw.net` and relays them. Twitch sends the person's browser back to
> `/plugins/ceo.villa.villanet/api/v1/twitch/callback` on the Site URL. The reverse proxy's access log
> will record the one-time code in that URL. It can be redeemed only once, within minutes, and only
> together with the state it was issued for (the ID token's nonce must match that state's), by the
> Mattermost user who started it; a leaked code is useless to anybody else. With the switch off the
> server asks Twitch for nothing, and people already connected can still disconnect (which touches
> only the plugin's store).
>
> **Browsers contact Twitch in exactly two cases, both off by default.**
>
> 1. **Connect Twitch** (under "Show people live on Twitch"). The person who presses Connect Twitch
>    and then Continue on Twitch has their own browser, in the window that opens, reach
>    `id.twitch.tv` and Twitch's sign-in pages. That window cannot be opened by a link, an image in a
>    post or another site: it needs a single-use ticket that only the panel can get.
> 2. **Twitch's player** (only if an admin ALSO switches on "Play Twitch streams inside VillaNET"). A
>    viewer who presses play on a Twitch stream loads Twitch's own player from `player.twitch.tv`.
>    Its code names, for example, `assets.twitch.tv`, `gql.twitch.tv`, `static-cdn.jtvnw.net`,
>    `usher.ttvnw.net` and video segment hosts, `spade.twitch.tv` (analytics), `passport.twitch.tv`,
>    `id.twitch.tv`, `edge.ads.twitch.tv`, Amazon advertising (`s.`, `c.` and
>    `vaes.amazon-adsystem.com`), comScore (`sb.scorecardresearch.com`) and Nielsen
>    (`secure-sts-prod.imrworldwide.com`). These are examples taken from the player's code, not a
>    complete or guaranteed list: the plugin cannot list or limit what the player contacts. Twitch
>    sees this server's address and the viewer's IP address and, if the viewer is signed in to Twitch
>    in that browser (in the desktop app, connecting Twitch signs it in), who they are. The frame can
>    play sound, go fullscreen, show browser dialogs and open unsandboxed windows (in the desktop
>    app, including the built-in window for plugin pages, which carries the session); it cannot
>    navigate or read the Mattermost page.
>
> Apart from these two Twitch cases, browsers talk only to this server. Twitch's player only runs on
> https at port 443 under a name, so on `chat.example.com:8065` it never loads. Check that the proxy
> adds no `frame-src` or `child-src` of its own:
>
> `curl -sI https://<Site URL>/ | grep -iE 'content-security|permissions-policy|x-frame'`
>
> Nothing about Broadcast Box is ever sent to Twitch, and no Twitch stream passes through Broadcast
> Box, the WHEP relay or the Clips recorder.

### 10.4 Code comments

- `media.go`'s header (`B :20-37`, already rewritten by IGDB Phase 1): the opening sentence stops
  claiming to be "the one part of the plugin that talks to anybody the admin did not point it at", and
  points at `twitch_live.go` for the other. If it says browsers never contact a provider, it adds
  "for lookups; Twitch live is the exception, see twitch_oauth.go and twitchEmbed.tsx".
- `twitchEmbed.tsx`'s block states that it is the only webapp module that sends a browser outside
  Mattermost, and the two cases (§10.3).
- Every new file opens with a plain-prose block that says why it exists and what it promises (the
  `media.go` and `statsstore.go` style).
- `twitch_live.go`'s block states the hosts, the schedule and the "nothing while off" rule.
- `twitch_oauth.go`'s block states why there is a ticket (§4.2), the CSRF reasoning in §4.3 step 5,
  why the nonce is checked (§4.3 step 8), and why only interactive sessions may connect.
- `twitch_links.go`'s block states why withdrawal and purging do not depend on the switch (§4.6).
- `twitch_air.go`'s block states why the ledger is separate (§7.1), why paid spans exist, and why
  Broadcast Box always wins a shared second (§7.3).

---

## 11. Tests and the lab

All Go tests are deterministic under `-race`:

- No real network: a fake `httptest.Server` serves `/oauth2/*`, `/helix/*` and `/previews-ttv/*`. It
  extends media-lookups' `newMediaFake` (§D.1 there), which swaps `twitchIDBase`, and also swaps
  `twitchAPIBase` and `twitchImageHost`.
- No `time.Sleep`: the store's `now` and `after` are injected, and `step` is called directly.
- `kvBackedPluginWithFaults` (`B stats_test.go:96-245`) for real CAS. Each test adds an `api.On(…)` for
  the calls it needs, and no log line carries more than 8 pairs.

After building, grep every non-test server file for `https://`. Each literal must be swapped by the
fake, be a link prefix (`https://www.twitch.tv/`), or be the player host the webapp builds.

### 11.1 Go tests

**`twitch_app_test.go` (additions)**

| Test | Asserts |
|---|---|
| `TestTheCodeExchangeSendsTheSecretInTheBodyOnly` | Form body exactly `client_id`, `client_secret`, `code`, `grant_type=authorization_code`, `redirect_uri` (byte-identical); empty query. The ID token comes back; the refresh token does not. |
| `TestCheckIDToken` (table) | Good token passes and returns `sub`. Each of: too big, two parts, bad base64, bad JSON, wrong `iss`, `aud` another client, `aud` an array of two, expired beyond the leeway, wrong nonce, missing nonce, non-numeric `sub` fails with its code word. |
| `TestWhoseTokenUsesTheUserTokenAndOurClientID` | `Authorization: Bearer <user token>`, `Client-Id`; zero or two users is an error. |
| `TestHelixRetriesOnceAfterA401` | 401, invalidate, new token, one retry, then success. A second 401 is an error. |
| `TestHelix429CarriesTheReset`, `TestHelix503IsRetriedOnce` | |
| `TestNoTwitchSecretTokenOrCodeIsLogged` | Every `Log*` captured across ticket, connect, exchange, whose, ID-token failure, revoke, refusal and failure. No argument contains the secret, the token, the ID token, the code, the nonce, the ticket or the state. A callback whose `error` is 10 KB long and contains a newline logs `other`. |

**`twitch_oauth_test.go`**

| Test | Asserts |
|---|---|
| `TestTheTicketIsIssuedOnlyByPOSTToAnInteractiveSession` | POST gives a 64-hex ticket and a `twitch_ticket_<user>` row (hash, session id, expiry 600). A session whose `Props["type"]` is `UserAccessToken`, a bot session and an OAuth-app session get 403 and no row. Off, callback problem, blocked: the right refusal, no row. The sixth ticket in 15 minutes gets 429. |
| `TestConnectRedirectsToTwitchWithAStateBoundToTheUser` | With a valid ticket: 302 `Location` equals the expected string exactly (parameter order, `scope=openid`, `nonce`, `force_verify=true`, callback from SiteURL with a sub-path). KV `twitch_state_<64 hex>` with expiry 600 and `u`, `at`, `r`, `n`. The ticket row is gone. |
| `TestConnectWithoutAValidTicketDoesNothing` (table) | No `t`; a malformed `t`; an unknown `t`; a ticket of another session; a spent ticket; `Sec-Fetch-Dest: image` (with a valid ticket); `Sec-Fetch-Site: cross-site` (with a valid ticket): 400 page `stale`, **no** `twitch_state_` write, **no** limiter count, **no** `Location` header, no outside request; in the two fetch-metadata rows the ticket is still there afterwards. `Sec-Fetch-Site: none` with `Sec-Fetch-Dest: document` (the desktop popup) is accepted. |
| `TestConnectPages` (table) | Off, no credentials, empty or http SiteURL, http://localhost (allowed), signed out, an integration session, blocked, full, store failure: the right page and status, no state written, no outside request. |
| `TestCallbackMakesNoRequestWithoutALiveState` (table) | Bad format, unknown, expired (the `at` check), already consumed: 400 `expired` and zero fake requests. |
| `TestTheStateIsSingleUse` | Two callbacks with one state: the second is `expired`, and one exchange in total. |
| `TestCallbackFromAnotherAccountIsRefusedAndUsesTheState` | Session B with state A: 403 `otheraccount`, no exchange, state deleted. |
| `TestCallbackWithoutASessionLinksNothing` | 401 `signin`, no exchange. Also with an integration session (a `UserAccessToken` session for the right user): 401 `signin`, no exchange. |
| `TestACodeFromAnotherStateIsRefused` | Victim V starts a connect (state SV, nonce NV); the fake Twitch issues code CV whose ID token carries NV. Attacker A starts their own (SA, NA) and calls the callback with `state=SA&code=CV` in A's session: one exchange, `nonce` mismatch, 502 `twitcherror`, revoke called, **no link written**, no takeover. |
| `TestTheIDTokenMustAgreeWithGetUsers` | ID token `sub` X, Helix `/users` answers Y: `twitcherror`, revoke called, nothing written. |
| `TestCancelledOnTwitch` | `error=access_denied`: 200 `cancelled`, no exchange. |
| `TestCallbackLinksAndRevokes` | Exchange, then Helix `/users`, then revoke (in this order); the link document written; the WebSocket event to the user; no KV value anywhere contains the token or the code (scan the fake KV map). |
| `TestATokenForAnotherClientLinksNothing` | Helix 401 for the user token: `twitcherror`, revoke still called, nothing written. |
| `TestCallbackPagesAreSafe` | CSP (with the script hash matching the fixed text), `no-store`, `nosniff`, `no-referrer`; a display name `<img src=x>` comes out escaped. |
| `TestAVerifiedProofTakesTheAccountOver` | A had X, B proves X: A removed, B linked, `lost[A]` holds X's login and the time, A's `/me` carries `twitch.lost`, the WebSocket event to both, page has `takenover`, X's ledger rows untouched. `POST /api/v1/twitch/notice` as A clears it; a `lost` entry 31 days old is dropped on the next write. |
| `TestABlockedPersonCannotConnect` | Disconnect and block: link gone, purge queued, `blocked[user]` set; the ticket POST answers 403; a callback already in flight answers 403 `blocked`; unblock lets them connect again. Delete all keeps the block list; Clear the block list empties it. |
| `TestA247ChannelGetsNoBadgeAndNoAirtime` | `allDay` on: the entry has `AlwaysOn`, sorts last, is never covered, gets no ranks, no badge, no Twitch credit and no paid spans; clearing it does not back-pay the 24/7 stretch. |
| `TestReplacingYourTwitchAccountPurgesTheOldOne` | `purge[old]` set; the old ledger rows gone after one `step`. |
| `TestAChannelBeingPurgedCannotBeConnected` | 409 `purging`. |
| `TestAnUnreadableLinkDocumentIsNeverOverwritten` | Bytes quarantined; connect gives `store`; the document is unchanged. |

**`twitch_links_test.go`:** `TestDisconnectIsIdempotent`,
`TestDisconnectDropsTheCardOnTheNextPoll`, `TestPurgeIsRetriedAfterAKVError`,
`TestPurgeWaitsWhileTheLedgerIsHalted`, `TestNamesAreRefreshedDaily`,
`TestAnAccountGoneTwiceIsDisconnected`, `TestDeleteAllTwitchData` (every key above and its quarantine
copies; the WebSocket broadcast to all; Broadcast Box tables untouched; the block list kept), and:

- `TestWithdrawalWorksWithTheSwitchOff`: switch off (and, separately, credentials cleared): `/me`
  carries `twitch: {available: false, login, displayName}` for a connected person and nothing for
  anyone else; `DELETE /api/v1/twitch/link` and the admin's Disconnect work; `OnActivate` with a
  queued purge runs it to the end with zero fake Twitch requests.
- `TestAPurgeKeepsOtherPayersSpans`: the person's Broadcast Box spans and a newer Twitch id's spans
  survive the purge of the old id.
- `TestARenameWhoseWriteFailsNeverMisdirectsThePlayer`: Get Streams lists a new login, the
  `casUpdate` of `twitch_links` fails: the card's `twitchLogin` is the new one (from the read), and
  the stored one is unchanged. A `user_login` that fails `^[a-z0-9_]{1,25}$` leaves the stream out of
  that read and is never written.
- `TestStoppingDuringAPurgeDoesNotDeadlock` (`-race`): the purge worker blocked mid-purge on a KV read
  that waits for its context; Delete all and `OnConfigurationChange` (switch off) both return; the
  worker exits on cancel. Same with the Twitch loop blocked inside `recordTwitchTime`.

**`twitch_live_test.go`**

| Test | Asserts |
|---|---|
| `TestNothingIsAskedWhileOffOrNobodyIsConnected` | Zero fake requests, and `pollOnce` makes no new API call with the switch off. |
| `TestGetStreamsIsBatchedByAHundred` | 205 connected people make 3 calls; each has ≤100 `user_id`, `type=live`, `first=100`; ids sorted. |
| `TestAFailedBatchFailsTheWholePass` | |
| `TestAStreamMissedOnceIsKept`, `TestAStreamMissedTwiceIsDropped` | |
| `TestFailedReadsKeepTheListForFiveMinutes`, `TestBackoffDoubles`, `TestRateLimitWaitsForReset` | |
| `TestTwitchStreamsCarryNoStreamKey` | Marshal a snapshot with a Twitch entry: `StreamKey` empty; no Twitch id, token, state or code in the JSON; handle ≠ any Broadcast Box handle; `twitchHandle` is stable. |
| `TestTwitchEntriesAppearOnAllThreePaths` | Unconfigured, failing (1st and 4th failure) and success all list the Twitch card, and the badge stays on across Broadcast Box failures. |
| `TestMergeNeverWritesIntoThePublishedSnapshot` | Run with `-race`, reading the published slice concurrently. |
| `TestWithoutTwitchTheSnapshotIsByteIdentical` | Golden: the same inputs with the switch off give the same JSON as before the change. The fixture has **no person with two linked stream keys**, because §7.8 deliberately merges those (T1 says so). |
| `TestLiveOnBothShowsOneCardAndAlsoTwitch` | After `OfflineGraceChecks` polls the Twitch entry carries `coveredBy` and stays in the list; the Broadcast Box card carries `alsoTwitch`. A paused Broadcast Box stream uncovers it only after the same grace; a one-poll Broadcast Box gap never uncovers it. |
| `TestTwitchBoardsAreRebuiltWhileBroadcastBoxIsDown` | Twitch on, Broadcast Box failing past the carry-over: `AllTime` and the Twitch card's ranks come from `addBoards`, so a crown on a Twitch card is always on a board. Twitch off: path B exactly as 2.11.0. |
| `TestSwitchingOffDropsTheCardsAtOnce` | And the badges come off after the grace. |
| `TestTheTwitchTitleIsCleanedAndRedacted` | |
| `TestWHEPRefusesATwitchHandle`, `TestStillsRefuseATwitchHandle`, `TestLinkedKeyIsLiveIgnoresTwitchAndEmptyKeys` | |
| `TestWhatsOnShowsOnATwitchCardAndSettles` | |

**`twitch_air_test.go`**

| Test | Asserts |
|---|---|
| `TestPaidSpanArithmetic` (table) | `unpaidParts`, `withPaid` and `prunePaid` on overlaps, touching edges, containment, empty, `max`. |
| `TestTwitchTimeIsCreditedFromGoodReadsOnly` | Grace-kept streams earn nothing. |
| `TestASimulcastIsCountedOnce` (table) | Same poll, either order; after a restart with different starts; a Broadcast Box backlog after an outage. Totals and months both. In every row the shared seconds end up in the **Broadcast Box** tables, and the Twitch tables hold only Twitch-only seconds. |
| `TestASimulcastSurvivesDisconnecting` | Simulcast 10 minutes (Twitch reads landing between Broadcast Box polls), then disconnect and purge: the person keeps 10 minutes on the boards, all of it Broadcast Box, and the Broadcast Box export holds it. |
| `TestTheFirstPollAfterARestartDoesNotDoubleCount` | A fresh store (cache nil), a Broadcast Box backlog of 60 minutes and a Twitch backlog of 80 overlapping it: 80 in all, whichever pass runs first. A KV error on the first links read skips the Broadcast Box pass (marks not advanced) and carries the previous boards. |
| `TestPaidSpansFollowTheBoardsOwnership` | A Broadcast Box stream matched by username (`linkSourceUsername`) is neither debited nor recorded in paid spans; a manually linked one is. |
| `TestTwitchTimeInAnEndedMonthForgetsThePodiums` | Twitch time crossing midnight on the 1st calls `forgetMonthPodiums`; so does a debit into an ended month. |
| `TestTimeBeforeConnectingNeverCounts` | Including after a restart (`creditAt` zero, no marks). |
| `TestSwitchingTwitchOnResyncs` | |
| `TestBroadcastBoxRecordingIsUnchangedWithTwitchOff` | The existing stats tests pass untouched, and `twitchair_paid` is never read (a KV mock that fails the test on that key). |
| `TestADamagedTwitchLedgerHaltsOnlyTwitch` | Broadcast Box still records. |
| `TestBoardsAreByPerson` | Broadcast Box plus Twitch rows give one row with `twitchSeconds`; ties in a total order; an unlinked Broadcast Box stream keeps its own row; an orphan Twitch row is left out. |
| `TestTwitchHoursHideWhileTheSwitchIsOff` | |
| `TestCardMedalsAreByPerson` | |
| `TestPastMonthMedalsMergeBeforeTheCut` | A person third on Broadcast Box and fourth on Twitch can win Gold. |
| `TestTheMonthsAreReadAgainWhenTwitchIsSwitchedOn` | |
| (changed) `TestMonthMedalsMergeTwoKeysOfOnePerson` | §7.10. |

**`twitch_preview_test.go`:** host, path and placeholder checks; a redirect to `/ttv-static/` means no
picture; size cap; sniff; served only for a listed handle with LivePreviews on; cache headers by
version; held 2 minutes; dropped when the stream ends; limiter only on a miss.

**`twitch_admin_test.go`:** admin gating on all three; the status JSON for each SiteURL case and the
embed problems; no Twitch id in the answer.

**Settings:** `TestTheSectionDefaultsMatchTheManifest` and the settings backup lists (§2.5).

**Words** (`server/docs_test.go`, `TestEveryNoOutsideContactPromiseMentionsTwitch`): reads
`../plugin.json` (every `help_text`), `../README.md` and `../LOCKDOWN.md`, splits them into sentences,
and fails for any sentence matching `(?i)never contact|no outside requests|no external network
requests|only outbound connection|talk(s)? only to this server` that does not mention "Twitch".
A short exempt list in the test carries each exception with its reason (for example README's
"Browsers never contact Broadcast Box at all", which is about Broadcast Box only and stays true).

### 11.2 Webapp checks

- `build/check-twitch.mjs`, run by `make check-webapp` (and so `make check`) beside `check-away.mjs` and built the same way (one
  file transpiled and imported from a data URL, so `twitchLogic.ts` imports nothing), tests:
  - `embedHostFor`: https:443 name ok; `:8065` refused; http refused; IPv4, IPv6 and `[::1]` refused;
    localhost on any port and scheme ok; `127.0.0.1` ok;
  - `inlineSize` at 399, 400, 407, 408, 640 and 1000, from both modes (entering inline needs 408,
    leaving it happens below 400);
  - `theatreSize` on phone, 1280×720 and 900×500;
  - `validLogin` and `channelURL`;
  - `frameURL`: every result starts with `TWITCH_PLAYER_PREFIX`, has exactly the four parameters in
    order, encodes the host, and is null for a bad login (`a b`, `A"`, 26 characters, empty).
- `build/check-away.mjs` gains §6.7's case: the sound on a Twitch handle plus a connected Broadcast
  Box handle latches the Broadcast Box handle; only Twitch handles watched latches nothing.
- `npx tsc`, `python3 build/check-react.py`, `python3 build/check-styles.py` after every `styles.ts`
  edit.
- `styles.ts` rules (memory "Editing styles.ts"): one template literal, no backticks, no octal escapes,
  exact unique-anchor edits, every new `vntv-` class has a rule, new reduced-motion rules **inside**
  the last block.
- New classes: `vntv-twitch-tile`, `-bar`, `-stage`, `-frame`, `-plate`, `-connect`, `-confirm`,
  `-note`, `-who`, `-disconnect`, `-lost`, `-category`, `-out`, `-theatre`, plus
  `vntv-card-tag-twitch` and `.vntv-tile[data-source="twitch"]` (with `order: 1`).

### 11.3 The lab

**Plumbing.**

- `lab/webpack.config.js` gains `swap(/^\.{1,2}\/twitchEmbed$/, 'mocks/twitchEmbed.tsx')`.
- `lab/mocks/surface.ts` gains the same every-export check for `twitchEmbed`.
- The mock's `embedHost()` returns `{ok: true, host: 'lab.localhost'}` unless the lab toggle "Twitch's
  player blocked here" is on.
- The mock's `TwitchFrame` renders its stand-in **in its own element**, never as a `srcdoc` iframe
  with `allow-same-origin`: a `<div class="vntv-lab-twitch-standin">` (or, if a frame is wanted, an
  iframe whose `sandbox` has no `allow-same-origin`) with "Twitch player (stand-in)", the channel,
  "muted" or "SOUND", a load counter keyed on `reloadKey`, and a slow CSS bar, so reloads and sizes
  can be seen. The lab is a file with no server, and Twitch would refuse it anyway.
- The mock's `openConnectWindow(url)` opens nothing: it starts the lab's Connect simulation, which
  flips the world to connected after 1.5 s (or fails, or never answers, by toggle), and with the
  toggle "Pop-up blocked" it does nothing at all, so the 4-second hint shows. The toggle "Desktop
  app" makes it behave as the desktop app does (the window "opens", nothing is returned). Nothing in
  the lab ever opens a real window.
- The mock's `openOutside(url)` shows a lab toast "Would open {url}" instead of opening it.
- `lab/mocks/client.ts`:
  - `twitchPreviewURL` returns an inline picture (reuse the lab media's stills);
  - `fetchConnectTicket` answers a fixed ticket after 300 ms (or a refusal sentence, by toggle);
    `twitchConnectURL` builds the address as the real one does (the mock window never loads it);
  - `unlinkTwitch` and `dismissTwitchNotice` change the world;
  - `fetchAdminTwitch`, `adminUnlinkTwitch`, `deleteAllTwitch`, `adminBlockTwitch`,
    `adminClearTwitchBlocks` and `adminTwitchAllDay` act on the world.
- `world.ts` gains:
  - a `twitchStream(name, userId, opts)` builder (`source: 'twitch'`, `twitchLogin`, `twitchCategory`,
    `viewers`, `previewVersion`);
  - `me.twitch`, `snapshot.twitchOn` and `snapshot.twitchEmbed`;
  - `twitchSeconds` in `snapshotOf`'s by-person ranking;
  - the toggles below. Add the `me.twitch` fields to both lab identity keys (`lab/index.tsx`, see
    clips-addendum A§2.2) so the panel refetches.

**States to look at,** each a chip or toggle:

1. Twitch off (the default world): nothing new anywhere.
2. Twitch on, not connected: the Connect Twitch link-button in both footer states (one line, no
   note); Confirm with the consent note and "Getting ready…"; a ticket refusal; Waiting, then
   Connected; Waiting with "Pop-up blocked" (the 4-second hint); Waiting with "Desktop app" (no hint
   once the simulated window takes focus); connect fails.
3. Connected: the name, the outbound link, the live dot; Disconnect with two clicks; the
   just-disconnected message; Connected while the switch is off ("Twitch is switched off here", and
   Disconnect still works); the takeover notice with OK; blocked (nothing shows).
4. Erin live on Twitch: the card with picture, Twitch tag, "1,234 on Twitch", category chip, Watch on
   Twitch ↗, medals.
5. Victor live on Broadcast Box and Twitch: one card with Also on Twitch; the popover link.
6. Embed off: a card click "opens" twitch.tv (the lab shows a toast instead of opening it).
7. Embed on, wide panel: an inline tile at 16:9, never below 300 tall; the bar; sound handoff between a
   Broadcast Box tile and a Twitch tile (the stand-in's label and load counter change); Reload.
8. Narrow panel (304 and 400 px): a card click opens the theatre in the same click; the tile's
   plate, Play bigger, the theatre with focus trap, Escape and backdrop close, Bring it back.
9. Player blocked here: the plate and the admin reason.
10. A thread takes the panel while a Twitch tile has the sound: no mini-player for it, and a
    Broadcast Box tile still gets one (this is the case `latchCandidate` used to get wrong, §6.7).
11. Twitch API down: the admin notice in the panel.
12. Broadcast Box not configured, Twitch on: Twitch cards and boards; the "not set up" notice only as
    admin.
13. Trophies: a merged row with the "incl. … on Twitch" tooltip.
14. System Console > Twitch connections: callback URL with Copy, each problem, the people list with
    Disconnect, Delete all.
15. Reduced motion on: nothing new animates.
16. **Simulcast handoff:** watching Erin's Twitch tile, then she starts her Broadcast Box stream: the
    tile keeps playing (no reload, no waiting tile), Live now shows both cards for the grace and then
    only the Broadcast Box card with Also on Twitch; she pauses Broadcast Box for one poll: nothing
    swaps.
17. A theatre whose stream ends: the theatre closes with its tile, and focus returns sensibly.
18. **Dragging the panel so the stage crosses 400 px** (the stage from 440 down to 396 and back, in
    2 px steps): the tile switches to `small` below 400 and back to inline only at 408, with one
    reload each way, never a flicker.
19. The two-column grid (776 px) with [Broadcast Box, Twitch, Broadcast Box] watched: the two
    Broadcast Box tiles share the first row and the Twitch tile spans the second; the bar at 400 px
    truncates the name and hides the count.
20. System Console > Twitch connections with the switch off: the people list, Disconnect, Disconnect
    and block, 24/7, the Blocked list, the set-aside-copy line.

Look at every state with the preview harness (memory "Webapp preview harness") at panel widths 304,
400, **464** (the realistic width in a 1201-1680 px window, and the narrowest where inline play
works) and 776, in light and dark Mattermost themes for the System Console panel.

---

## 12. Build plan

### 12.1 Prerequisites (all three, in this order)

1. **Clips 3.0.0 is complete in the main tree** and `TZ=UTC make check` passes there. Twitch live
   touches `poller.go`, `plugin.go`, `api.go`, `stills.go`, `nowplaying.go`, `stats.go`,
   `monthmedals.go`, `Sidebar.tsx`, `LiveCard.tsx`, `PopoverLive.tsx`, `types.ts`, `client.ts`,
   `styles.ts`, `index.tsx`, `LeaderboardModal.tsx` and the lab, all of which Clips is editing.
2. **IGDB Phase 1 (media-lookups Parts A to D) is patched onto the main tree.** That gives
   `twitch_app.go`, the `twitch` section, the two credential settings, `sendMedia`'s fake and the
   changes to `media.go`.
3. **media-lookups Part E is not built**, in the sandbox or anywhere. This document replaces it.

### 12.2 Phase T1: two builders in parallel, in the main tree

Neither builder touches the other's files. The contract between them is §2.1 (setting keys), §4.1
(routes), §4.10 (`/me`, the WebSocket event, the admin JSON) and §5.10 (the stream JSON).

**Checks while two builders work at once.** The Makefile is split (`M Makefile:80-92`): `check` is
`check-server` plus `check-webapp`, so that one half's work in progress does not fail the other
(the Clips addendum found two parallel builders cannot each pass the full `make check`, review row
24 there). Each builder runs **its own half** with `TZ=UTC`: `TZ=UTC make check-server` or
`make check-webapp`. The full `TZ=UTC make check` runs only at the join points: the end of T1, and
T2.

**Server builder** owns `server/**`, `plugin.json` and the `Makefile` (as in the Clips addendum,
which gives the Makefile to the server agent). Order:

1. `twitch_app.go` additions and tests (§3), including `checkIDToken` and `cleanTwitchUser`.
2. `configuration.go`, `defaults.go`, `settings.go`, plugin.json settings and the other help edits
   (§2, §10.1), and `docs_test.go`'s words test (§11.1), which fails until T2 has written README and
   LOCKDOWN, so it is added with a `t.Skip` naming T2, removed in T2.
3. `twitch_links.go` (the document, block, 24/7, notices, the purge worker) and `twitch_oauth.go`
   (ticket, connect, callback), the `ServeHTTP` routing (§4), with tests.
4. `twitch_live.go`: the store, `step`, the first read in `owners`/`peopleIfOn`, the lifecycle hooks
   and lock order in `plugin.go`, and `twitchStreams`. `handles.go` gets `twitchHandle` and the
   `streamKeyForHandle` guard. Tests.
5. `poller.go`: `mergeStreams` with the cover map, the three paths (§5.4), after Clips'
   `recorders().reconcile`. Guards in `api.go` (`linkedKeyIsLive`, routes, `/me`), `stills.go`
   (`previewable`), `nowplaying.go` (row 20), and Clips (§9). Tests.
6. `twitch_air.go`, then `stats.go` (paid spans with payers, `debitSpan`, `reachesBeforeMonth`,
   `boardOwner`, `boardRows`, `leaderboard`, `rankByRow`), `poller.go` `addBoards`, `monthmedals.go`
   (§7). Tests, including the changed medals test.
7. `twitch_preview.go` and `twitch_admin.go`, with tests.
8. The `Makefile` line that runs `node build/check-twitch.mjs` in `check-webapp`, beside
   `check-away.mjs` (the webapp builder writes the script).
9. `TZ=UTC make check-server`, `go test -race -count=3 ./server/...`, and the `https://` grep.

**Webapp builder** owns `webapp/src/**`, `webapp/lab/**` and `build/check-twitch.mjs` (and the new
case in `build/check-away.mjs`). Order:

1. `types.ts`, `client.ts` and `lab/mocks/client.ts` (§5.10).
2. `twitchLogic.ts` and `build/check-twitch.mjs`; `twitchEmbed.tsx` (`embedHost`, `TwitchFrame`,
   `openConnectWindow`, `openOutside`) with its lab swap and surface check; `twitch.ts` (theatre
   store, `isTwitchHandle`, `stageWidth`, `watchOrOpen`).
3. `TwitchConnect.tsx` in `Sidebar.tsx`; the `index.tsx` WebSocket handler (and its unregistering)
   and `TwitchSetup` registration; `AdminTwitch.tsx`.
4. `LiveCard.tsx`, `PopoverLive.tsx`, `UpNext.tsx`, Sidebar's `liveHandles`, Live now list,
   `onToggle`, `onWatchOnly` and notices (§5.8, §5.9); `ClipsModal.tsx` and `ClipViewer.tsx` (§8 rows
   35-36); `LeaderboardModal` tooltip.
5. `TwitchPlayer.tsx` and `TwitchTheatre.tsx`; the grid branch in `Sidebar.tsx`; guards in
   `sessions.ts` (`mayCreate`), `stills.ts`, `awayLogic.ts`/`away.ts` (§6.7) and `MiniPlayer.tsx`.
6. `styles.ts` (with `check-styles.py` after every edit).
7. The lab (§11.3). `TZ=UTC make check-webapp` (which runs `tsc`, `check-react.py`,
   `check-styles.py`, `check-away.mjs` and, once the server builder's Makefile line is in,
   `check-twitch.mjs`; until then run it by hand), build the lab and walk every state.

### 12.3 Phase T2: one agent

1. README, LOCKDOWN and the `media.go` header (§10). Remove the words test's `t.Skip`.
2. Version **3.1.0** in `plugin.json` and `manifestVersion`.
3. `TZ=UTC make check` (the full one), `make lab`, and walk §11.3 again on the integrated tree.
4. `make dist-linux`.
5. Write the list of §13.2 checks for Victor.

### 12.4 Phase T3: Victor, on the real server (§13.2)

---

## 13. Acceptance criteria

### 13.1 Before release (lab and tests)

| # | Criterion |
|---|---|
| T1 | With the switch off, the snapshot, the KV namespace and outbound requests are exactly 2.11.0/3.0.0's (the golden test and zero fake requests), **except** §7.8's one-row-per-person merge, which applies with or without Twitch (the golden fixture therefore has no person with two linked keys). The only Twitch KV access with the switch off is one read of `twitch_links` per `/me` and one at activation; `/me` gains a `twitch` field only for a person who is connected or has a takeover notice. |
| T2 | Connect: nothing happens without a ticket from the POST (no state, no limiter count, no redirect), the 302 is exact, the state is single-use and bound, the ID token's nonce must match it, the callback refuses another account and integration sessions, no token, ID token, code, nonce or ticket is logged, and a revoke is always attempted. |
| T3 | Twitch entries appear on all three poll paths, and Broadcast Box never waits on Twitch. |
| T4 | Live on both: one card in Live now (after the grace), a watched Twitch tile keeps playing, one badge, each second credited once and to Broadcast Box. |
| T5 | Boards, card medals and past-month medals are by person, with Broadcast Box plus Twitch. |
| T6 | Disconnect removes the card on the next poll and all Twitch hours within one purge-worker pass (or as soon as the ledger is readable), **whether or not the switch is on**; no Broadcast Box second is lost. |
| T7 | No Twitch handle can reach WHEP, stills, the recorder or the mini-player. |
| T8 | The iframe is never covered, never smaller than 400×300, carries exactly §6.3's attributes, and is only ever made from a `frameURL` with the player prefix (no `srcdoc` in the production bundle). |
| T9 | Every lab state in §11.3 renders correctly at 304, 400, 464 and 776 px. |
| T10 | `TZ=UTC make check` is green at the join points, and `check-twitch.mjs`, the new `check-away.mjs` case and the words test are in it. |
| T11 | Every "browsers never contact" / "no outside requests" sentence in plugin.json, README and LOCKDOWN names Twitch or is on the words test's exempt list. |
| T12 | An admin can disconnect, block, unblock and mark 24/7 with the switch on or off. |

### 13.2 On Victor's real server (UNVERIFIED until then)

1. Register `https://<site>/plugins/ceo.villa.villanet/api/v1/twitch/callback`. Connect in Chrome,
   Firefox and the desktop app. The desktop popup must carry the session (M8) and close or say to close
   it; the panel must sit in Waiting (not a pop-up warning) while it is open. Note whether
   `scope=openid` shows any permission text on Twitch's consent screen, and whether the callback page
   manages to close itself (with `noopener` a browser may refuse).
1a. Post `![x](https://<site>/plugins/ceo.villa.villanet/api/v1/twitch/connect)` in a test channel,
   and open `…/twitch/connect` in a new tab by hand: neither may open Twitch or count against Connect
   (the `stale` page, or nothing).
2. After connecting, check whether VillaNET appears in Twitch's Settings > Connections (T5). If it
   does, the Disconnect message stays as written.
3. Go live on Twitch: the card within about two minutes; the badge; the picture; the category.
4. With the embed switch on, at port 443: the player plays inline. **Check the referrer.** If the
   player shows an error with `strict-origin`, try `no-referrer` and `origin` and report which works.
   If it works with `no-referrer`, switch the constant (§6.3).
5. Sound handoff between a Broadcast Box tile and a Twitch tile. Firefox's autoplay setting.
   Fullscreen in the desktop app (the Allow dialog once).
5a. Optional: whether the player still works with `allow-modals` taken out of `sandbox`. Report it;
   the build keeps Twitch's own list unless Victor decides otherwise, because the agreement asks that
   the player not be limited.
6. A 1000 px-wide window (a panel of at most 400 px): a card click opens the theatre at once. A
   1300 px window with the panel at about 464 px: the player plays inline.
7. `curl -sI` from §10.3 on the proxy.
8. Simulcast for ten minutes: the boards rise by ten minutes, not twenty, and the Broadcast Box
   export (Airtime admin) shows the ten minutes.
9. Disconnect: the Twitch-only hours leave the boards on the next poll after the purge, and the
   simulcast minutes stay. Then switch Show people live on Twitch off with somebody still
   connected: they still see Disconnect, and it works.

---

## 14. Questions for Victor (the build uses the default until he says otherwise)

**Answered by Victor on 2026-10-02:** Q1 one card, the Broadcast Box one with "Also on Twitch"; Q4 no, a Twitch
connection is NOT "linked" (only Link Your Account unlocks Clips, planning, What's on? and Top Snoopers); Q9 anyone
signed in may connect, with admin disconnect/block and 24/7; Q8 release 3.1.0, built right after 3.0.0 lands.
The other questions keep their defaults (Q2 disconnect purges Twitch hours, as Twitch's agreement requires; Q3 hours
kept but off the boards while the switch is off; Q5 What's on?/plan first, else Twitch's category; Q6 plain embed,
sound moves by reload; Q7 README section plus the Connect note). Earlier, binding answers: Connect with Twitch
(OAuth), playback inside VillaNET, and Twitch time counting toward airtime and medals.


1. **Live on both at once:** one card or two? **Default: one**, the Broadcast Box card with an "Also on
   Twitch ↗" link. Two cards would show the same person twice, usually the same picture.
2. **Disconnecting deletes that person's Twitch hours**, so past-month medals they won partly on
   Twitch can move. **Default: yes, delete.** Twitch's agreement says to delete a person's data when
   they withdraw. Keeping them would need Twitch's permission or a different reading of the terms.
3. **Twitch links and hours while "Show people live on Twitch" is off:** **Default: kept**, the hours
   left off the boards and shown again when it is switched back on; nobody can connect, people can
   still disconnect, and the daily name check does not run (it would contact Twitch), so names are
   refreshed on the first pass after it is back on. **Delete all Twitch data** removes everything
   for good. The alternative is that switching off deletes all Twitch data at once, which is simpler
   to explain under Twitch's terms but loses everybody's Twitch hours on an accidental switch-off.
4. **Should a Twitch connection count as "linked"** for planning, What's on? and the Top Snoopers
   board? **Default: no.** Those stay for Link Your Account, as Clips does by your rule. A Twitch-only
   streamer gets the badge, a card, playback and airtime.
5. **What a Twitch card says is on:** **Default:** the What's on? choice or the current plan when there
   is one (only Broadcast Box-linked people can set those), otherwise Twitch's category.
6. **Sound on a Twitch tile reloads Twitch's player**, which can replay an ad, because VillaNET only
   uses Twitch's plain embed, whose sound can be chosen only when it loads. Mattermost's page blocks
   Twitch's player script. The plugin *could* get round that by serving a page of its own that loads
   Twitch's script and framing that, which would move the sound without a reload; but that page has
   to be on Mattermost's own address, so Twitch's code would run with the viewer's Mattermost session
   and could act as them in Mattermost (§6.1). **Default: plain embed, and keep VillaNET's sound
   button on Twitch tiles anyway.** Alternatives: Twitch tiles that always start muted, with sound
   only from Twitch's own control; or the wrapper page, accepting that risk (not recommended).
7. **Privacy notice:** Twitch's agreement asks for a publicly available privacy notice. **Default:** the
   README section and the consent note shown when somebody presses Connect Twitch. If you need it on a
   public web page, say where.
8. **Version:** **Default: 3.1.0**, built after Clips (3.0.0) and the IGDB patch land.
9. **Who may connect a Twitch account.** None of your answers said. **Default: anyone signed in
   (interactively), with admin Disconnect and block and a per-channel 24/7 flag** (§4.5), which mirror
   the controls Broadcast Box already has (reserved profiles, Always-on stream keys). The alternative
   is an **allow-list mode**: a setting under Twitch connections listing who may connect, with
   everybody else seeing no Connect Twitch at all. That fits a server where only a few known people
   stream; it is a small addition (one list in the links document and one check at the ticket POST
   and the callback).

(media-lookups.md's Questions 8 to 11 are settled by your answers where they spoke: playback is
inside the panel behind a switch, airtime counts, and the poll runs once a minute. Who may connect is
not settled by them, which is why it is Q9.)

---

## 15. Limits and out of scope

- **EventSub**, including `user.authorization.revoke`: not in 3.1.0 (§5.1). VillaNET keeps no token,
  so it cannot see someone removing it in Twitch's Connections. The Disconnect button (which works
  whatever the switch says) and the daily account check are the ways out. The README's privacy notice
  says in so many words that removing VillaNET in Twitch's Connections does not disconnect you here,
  and the just-disconnected message repeats it (§4.9, §10.2).
- **A Mattermost HA cluster:** each node polls Twitch, holds its own pictures and token (media-lookups
  §A.2), and would credit Twitch time on each node. That is the same single-server assumption the
  airtime tables already make (`B statsstore.go:165-191`).
- **Mattermost framed by another site** (Teams): `parent` would need every ancestor. Not supported.
- **IGDB art for Twitch categories** (Get Games `igdb_id`): later.
- **Clips of Twitch streams:** never (Schedule 1 `:298`).
- **Twitch chat** in the panel: not asked for.

---

## Appendix A: UNVERIFIED items, and how the build copes with each

| Item | How the build copes |
|---|---|
| Whether an empty `scope=` works | Not used; `scope=openid`. |
| Whether `scope=openid` shows anything on the consent screen | Harmless either way; §13.2 step 1 records it. |
| How many redirect URLs an application may have | One is all VillaNET needs. |
| Whether revoke also kills the refresh token, and removes the app from Connections | The refresh token is never kept. The Disconnect copy tells people about Connections. §13.2 step 2. |
| Whether `force_verify` lets the person switch Twitch account | The README says to sign out of Twitch in the window first. |
| What the player shows below 400×300 | Never rendered that small. |
| Whether the player needs a referrer under Mattermost's `no-referrer` | `strict-origin` by default (origin only, which `parent` already reveals). §13.2 step 4 decides. |
| Whether the desktop plugin popup carries the session, and whether `window.close` works there | Verified in desktop source that it uses the default session and that `window.open` returns null there (M8); §4.9 always goes to Waiting. The callback also refuses cleanly (`signin`), and the page tells people to close it. |
| Whether a `noopener` popup may close itself after navigating through Twitch | The page's words tell people to close it; §13.2 step 1 records it. |
| Twitch's authorization code lifetime (T20) | Irrelevant since revision 2: a code can only be redeemed with the nonce of the state it was issued for (§4.3). |
| Whether Twitch matches redirect URIs exactly (T7, forum only) | The callback refuses integration sessions, so an appended `access_token` cannot stand in for the person (§4.3 step 5). |
| Whether the player works without `allow-modals` | Twitch's list is kept; §13.2 step 5a records it. |
| Which hosts the player actually contacts | The help text and LOCKDOWN say VillaNET cannot list or limit them, and name the ones seen in its code. |
| Cache headers on a live thumbnail | VillaNET holds pictures 2 minutes and versions them per 5-minute slot. |
| How long Get Streams lags | Cards may appear 1 to 3 minutes after going live. The README says "within a couple of minutes". |
| Whether a reconnect keeps the same stream `id` | Not used. Marks key on `started_at`, as Broadcast Box marks key on `streamStart`. |
| The rate-limit bucket size | Read from the `Ratelimit-*` headers; at most 5 calls a minute anyway. |
| Whether Get Users leaves out suspended accounts | Two daily misses before disconnecting. |
| All readings of the Developer Agreement | §1.3 and Q2, Q3 and Q7 put them to Victor. |

---

## Review log

Revision 2, 2026-10-02. Thirty issues from two lenses (security-lockdown, feasibility-ux). Each was
checked against the cited source before it was acted on: Mattermost v9.0.0/v9.3.0/master and
Desktop v5.13.7/v6.3.0 source saved under the scratchpad's `twitch/review` and `twitch/adv`, the
pinned `server/public@v0.4.4`, Twitch's OIDC and OAuth pages, the Developer Services Agreement
(`facts/dsa.txt`), OpenID Connect Core, and the 2.11.0 baseline and main-tree code. **Accepted: 30.
Rejected: 0.** Where a fix was taken in a different form from the one proposed, the row says how and
why.

| # | Sev. | Lens | Issue | Verified against | Verdict | What changed |
|---|---|---|---|---|---|---|
| 1 | major | security | `GET /twitch/connect` has side effects and can be fired by a Markdown image, a top-level navigation from another site, or any frame in a desktop view (Twitch's player included); the meter becomes a lock-out lever and the browser contacts Twitch unasked | `pr-v9.0.0.go:160` (CSRF only for non-GET); v9 `renderer.tsx:97-104`, `post_utils.ts:85-97` (plain `<img>`, proxy off by default); desktop v6.3.0 `webContentEvents.ts:225-284, :332-333` (handler on the whole webContents; `loadURL` in the popup); `pluginsPopUps.ts:55-72, :120-133` | Accepted, adapted | §4.1, §4.2 rewritten: single-use ticket from a CSRF-protected `POST /connect-ticket` (hash and session id in KV, 10 min, one per person); the GET spends it before any state write or redirect and otherwise answers `stale` with no write, no count and no `Location`; fetch-metadata layer added. **Adapted:** the ticket is fetched on the first press of a two-step Connect (§4.9) rather than prefetched on every render, which avoids a KV write per panel mount for people who never connect; the limiter moves to the POST as proposed. Tests in §11.1; real-server check 1a. New facts M13. |
| 2 | major | security | The authorization code is not bound to the state, so a leaked unspent code can be redeemed by any member with a fresh state of their own | Twitch OIDC page `:199-240` (nonce), `:281-330` (id_token from the code exchange), `:348-380` (nonce claim); discovery `issuer`; OIDC Core §3.1.3.7 (TLS in place of signature) | Accepted | `nonce` in the state record and the authorize URL; `exchangeCode` returns the ID token; `checkIDToken` (§3) requires `iss`, `aud`, `exp`, `nonce`, `sub`, and `sub` must equal Get Users' id (§4.3 step 8). Takeover notice for the previous owner (`lost`, §4.5, §4.9). "Do not share this page's address" on `signin`, `otheraccount`, `expired`. LOCKDOWN sentence rewritten (§10.3). Facts T18-T20; tests `TestACodeFromAnotherStateIsRefused`, `TestCheckIDToken`, `TestTheIDTokenMustAgreeWithGetUsers`. |
| 3 | major | security | "Browsers never contact…" and "no outside requests" sentences stay false once either Twitch switch is on; "the one exception" ignores Connect | `media-lookups.md:94`, `:1240-1251`, `:1795-1799`; `M plugin.json:204`, `:360`; `M README.md:148, :358, :682, :2133-2135` | Accepted | Exact replacements in §2.1 (Client ID help), §10.1 (MediaLookup, EnableVoice), §10.2 (README :358, :682, :2133), §10.3 (LOCKDOWN names **two** browser cases). Words test `TestEveryNoOutsideContactPromiseMentionsTwitch` (§11.1), T11. Also found and fixed README :358, which the reviewer did not list. |
| 4 | major | security | Disconnect and purging depend on the switch; while off a person cannot even see Disconnect and admin disconnects leave hours indefinitely | `facts/dsa.txt:144` (§VI.F) and `:282`; design's own `/me`, loop and purge wiring | Accepted | Purge worker independent of the Twitch loop, kicked from `OnActivate` whatever the switch (§2.2, §4.6); disconnect, admin disconnect, Delete all and TwitchSetup work while off; `/me` carries `twitch` for a connected person while off (§4.10, §4.9); `twitchLiveOn` comment and T1 relaxed to one `twitch_links` read per `/me` and one at activation. Q3 now also asks whether switching off should delete everything. |
| 5 | major | security | No admin control over who appears; a looped 24/7 Twitch "live" wins every board; an admin Disconnect is undone by reconnecting; mature streams cannot be filtered; "anyone may connect" was presented as settled | `M plugin.json:149` (AlwaysOnStreamKeys help); `LOCKDOWN.md:10, :24`; T8 (`is_mature` deprecated); B `poller.go:550` (badges skip AlwaysOn) | Accepted | Disconnect and block (by Mattermost user only, so no Twitch ids are kept after withdrawal) and a per-channel 24/7 flag (AlwaysOn: no badge, no ranks, no airtime, mark kept current like a paused stream) in §4.5, §2.4, §4.1, §5.3, §7.2; help text and README say so. Allow-list mode is **Q9**; the closing note of §14 no longer claims who-may-connect is settled. |
| 6 | minor | security | Connect and callback accept a personal access token, OAuth-app or bot session | v9 `plugin_requests.go:147, :228-230`; v0.4.4 `model/session.go:251-266`, `plugin/api.go:211` | Accepted | Every route that adds a connection loads `GetSession(c.SessionId)` and refuses `IsIntegration()` (§4.1, §4.2, §4.3 step 5). Fact M14; tests in the ticket and callback tables. |
| 7 | minor | security | Desktop `window.open` of a plugin URL returns null, so "Blocked" would show while the popup is open; without `noopener` Twitch's pages hold `window.opener` | desktop v6.3.0 `webContentEvents.ts:272-284`; Electron `window-open.md` (deny cancels; returns `Window \| null`) | Accepted | §4.9: always Waiting; a 4-second focus-based hint replaces Blocked; `noopener` with a WHY; no popup handle is kept. M8 corrected. Merged with #16. |
| 8 | minor | security | Disclosures incomplete: what Twitch learns (domain, IP, signed-in identity; desktop Connect signs the default session in), what the frame may do, LOCKDOWN names fewer hosts than the help, and "confirm who connected" was attributed to id.twitch.tv | desktop v6.3.0 `webContentEvents.ts:233-243` (no partition); `facts/player_v1.js` sandbox list; v9 `browser_history.tsx:23-26` (origin check); design §3 (`whoseToken` is Helix) | Accepted | TwitchEmbed and EnableTwitchLive help (§2.1), README security bullet written out exactly (§10.2), LOCKDOWN names the same example hosts as the help, marked as examples from the player's code (§10.3); powers listed in §6.3; attribution fixed. Dropping `allow-modals` is optional real-server check 5a, Twitch's list kept by default. |
| 9 | minor | security | Quarantine copies outlive Disconnect; the README promised Disconnect deletes everything and did not say Twitch's Connections page does not disconnect | B `statsstore.go:113-137` (first copy kept, never overwritten) | Accepted | TwitchSetup row 7 says when a Twitch quarantine copy exists (`hours.quarantined`); README qualifies "deletes all of it" and states the Connections point; just-disconnected message repeats it (§4.6, §4.9, §10.2, §15). A per-person purge of undecodable bytes is impossible, so Delete all remains the way to remove them. |
| 10 | minor | security | The login used for `channel=` and Watch on Twitch came from the link document, not the read that produced the card; logins written back were not validated | design §5.3, §4.7, §3 | Accepted | `cleanTwitchUser` applies to whoseToken, Get Streams and Get Users (§3); `TwitchLogin` comes from the same good read and is checked again (§5.3); invalid values are never written (§4.7). Test `TestARenameWhoseWriteFailsNeverMisdirectsThePlayer`. |
| 11 | minor | security | The callback's `error` value, chosen by whoever completes the callback, was logged verbatim | design §4.3 step 6, §4.11 | Accepted | Logged only when it matches `^[a-z_]{1,40}$`, else `other`; test extended with a 10 KB value containing a newline. |
| 12 | minor | security | `frameSource` allowed `srcDoc` in production; a srcdoc frame with `allow-scripts allow-same-origin` can unsandbox itself | design §6.3 | Accepted | Production type is a `TwitchFrame` component that only renders `frameURL(…)` with the `https://player.twitch.tv/?` prefix; no srcdoc path exists; the lab's stand-in renders in its own element without `allow-same-origin` (§6.2, §6.3, §11.3); prefix asserted in `check-twitch.mjs`; T8. |
| 13 | minor | security | The Connect note said what is kept, not who sees it | `facts/dsa.txt:246-248` | Accepted | The consent note in the Confirm step (§4.9) says everyone sees title, category, viewers and picture and that the hours count on the boards; mirrored in the README privacy notice (§10.2). |
| 14 | major | feasibility | Paid spans moved simulcast seconds into the deletable Twitch ledger (about 12% of every simulcast, hours after an outage), so disconnecting or switching off removed real Broadcast Box time | B `stats.go:218-477`; `defaults.go:31` (15 s polls); design §7.2-§7.4 | Accepted | §7.3 rewritten: Broadcast Box credited in full as in 2.11.0; spans record their payer; the Broadcast Box pass takes overlapping seconds back from the Twitch ledger (`debitSpan`, clamped at 0); stated write order and replay cost; halted-ledger behaviour; §7.4, §7.12 and README updated. Tests `TestASimulcastSurvivesDisconnecting` and the extended simulcast table. |
| 15 | major | feasibility | The first poll after a restart saw no connected people (nil cache), double-counting backdated simulcast time and dropping Twitch rows from boards and medals | design §5.2, §2.2; B `stats.go` backdating, `plugin.go:79` | Accepted | `owners(cfg)` and `peopleIfOn(cfg)` load `twitch_links` themselves when the cache is nil; an error skips the Broadcast Box pass and carries the previous boards (§5.2, §5.4, §7.8). Test `TestTheFirstPollAfterARestartDoesNotDoubleCount`. |
| 16 | major | feasibility | Twitch handles are not kept out of the mini-player by construction: `latchCandidate` returns the sound holder without a session check | `M awayLogic.ts:377-383`; `M away.ts:309, :322-327, :731`; `M MiniPlayer.tsx:177-178` | Accepted | `latchCandidate` gains an `excluded` predicate (pure, testable in `check-away.mjs`); `away.ts` passes `isTwitchHandle`, backed by a seen-handle set; MiniPlayer guard uses the same (§6.7). New `check-away.mjs` case; lab state 10 explained. |
| 17 | major | feasibility | Desktop `window.open` returns null, so every desktop Connect would show "blocked" | desktop `webContentEvents.ts` (v5.13.7 and v6.3.0); Electron docs | Accepted | Same fix as #7 (always Waiting, hint after 4 s); the open goes through the lab-swappable `openConnectWindow` (§4.9, §6.2). `isDesktopApp` is not needed once null is never treated as blocked. |
| 18 | major | feasibility | Dropping the Twitch entry while the person has a live Broadcast Box card breaks a watched Twitch tile and makes simulcasters' cards flicker | `M Sidebar.tsx:1097-1115` (WaitingTile when the stream is gone), `:1179-1193`; `M watching.ts:179-186` | Accepted | The Twitch entry stays with `coveredBy`; only card readers skip it; hysteresis of `OfflineGraceChecks` polls each way (§5.3, §5.4, §5.6, §5.9, §5.10, §8 row 40). Tests and lab state 16. |
| 19 | minor | feasibility | Several lab states could not be produced (window.open calls not swappable; no simulcast, theatre-ends or resize states) | `M lab/webpack.config.js:60-62` | Accepted | `openConnectWindow` and `openOutside` live in `twitchEmbed.tsx`, mocked with "Pop-up blocked" and "Desktop app" toggles and toasts; lab states 16-20 added (§6.2, §11.3). |
| 20 | minor | feasibility | 400×300 inline needs a panel of about 440 px, which Mattermost allows only in windows of 1201 px or more; a card click at narrower widths needed a second click | `M styles.ts:573-580` (12 px padding); MM master `_sidebar-right.scss:17-45`; `rhsWidth.ts` | Accepted | Stated plainly in §6.4, help text and README; M9 corrected (400 px max below 1201, not 304); `watchOrOpen` opens the theatre in the same click (§6.9); upward-only hysteresis (408 in, below 400 out) because Twitch's floor may not be broken; lab width 464; T9. |
| 21 | minor | feasibility | §8 missed BB-only assumptions: ClipsModal's `anybodyLive`, ClipViewer's give-back, the card body click, README :682, `uninitialize` | `M ClipsModal.tsx:289, :378`; `M ClipViewer.tsx:74-90`; `M Sidebar.tsx:1190`, `LiveCard.tsx:204, :301`; `M README.md:682, :1288`; `M index.tsx:401-402` | Accepted | §8 rows 35-40, §5.9 `onWatchOnly` row, §10.2 README edits. ClipViewer restores a Twitch sound holder knowingly (one reload) rather than leaving two sounds playing. |
| 22 | minor | feasibility | `reachesBefore(monthBucket(now))` can never fire for `twitchair_month_…` keys | B `stats.go:69, :128-136, :191-199` | Accepted | `reachesBeforeMonth(now)` using `m.prefix`, adopted by all callers (§7.2 step 8, §7.3). Test `TestTwitchTimeInAnEndedMonthForgetsThePodiums`. |
| 23 | minor | feasibility | Paid spans used `UserID` (username matches included) while the boards attribute only manual links, splitting one person across two rows | B `poller.go:425-440`; B `stats.go:497-511` | Accepted | One `boardOwner(stream)` (manual links only) shared by paid spans and the boards (§7.3). Test `TestPaidSpansFollowTheBoardsOwnership`. |
| 24 | minor | feasibility | Build plan stale: min server 9.3.0, split Makefile, Makefile ownership, order relative to `recorders().reconcile` | `M plugin.json:9`; `M clips_share.go:1046-1049`; `M Makefile:80-92`; `clips-addendum.md:50`; v9.3.0 `handlers.go:239-256` (CSP unchanged) | Accepted | M12 and §6.11 say 9.3.0; §12.2 runs `check-server`/`check-webapp` per builder and the full check at join points; the server builder owns the Makefile line; `mergeStreams` runs after `recorders().reconcile` (§5.4). |
| 25 | minor | feasibility | §6.1's reason for not using Twitch's JS player was incomplete: a plugin-served wrapper page could load it | v9.3.0 `channels.go:214-217`, `server.go:923-967` (no CSP on plugin routes) | Accepted | §6.1 restated (possible, refused because it would run Twitch's code with the viewer's Mattermost session); fact M15; Q6 offers the alternative with its cost. |
| 26 | minor | feasibility | "Under statsWriteLock and with the loop's lock held" could deadlock; restarts could hold up Save; purges ignored the context | design §4.6, §5.2, §2.2 | Accepted | One lock order (`runLock`, then `statsWriteLock`, then `store.lock`); stop and wait with no stats lock held; `ctx.Err()` between KV reads (§2.2, §4.6, §5.2). Test `TestStoppingDuringAPurgeDoesNotDeadlock`. |
| 27 | minor | feasibility | A permanent two-line Connect row in the pinned footer takes height from the player for everyone | `M Sidebar.tsx:1229-1330`; `M styles.ts:4994-5000, :5068-5072` | Accepted, adapted | Resting states are one link-button or one line (§4.9); the explanation shows only in the Confirm step, which also carries #13's consent and fetches #1's ticket. **Not taken:** a Twitch icon button beside the relink key, because §1.3's trademark reading puts no Twitch logo anywhere. *Superseded 2026-10-06 for the footer tool only: Victor chose Twitch's Glitch over the word (the amendment at the top).* |
| 28 | minor | feasibility | Twitch tiles spanning the row leave half-row gaps in the two-column grid; the bar overflows at 400-440 px | `M styles.ts:620-672` (no `grid-auto-flow`) | Accepted | `order: 1` on Twitch tiles (visual only; the DOM is never reordered because moving an iframe reloads it); the bar truncates, hides the count below 480 px, and uses icon-only buttons with labels (§6.4). Lab state 19. |
| 29 | minor | feasibility | On path B after the carry-over, Twitch cards kept crowns no board explained | B `poller.go:114-170` | Accepted | With Twitch on, path B rebuilds the boards (and the watch boards once the carry-over has ended); `carryTwitchRanks` removed; Twitch off is unchanged (§5.4). Test `TestTwitchBoardsAreRebuiltWhileBroadcastBoxIsDown`. |
| 30 | minor | feasibility | T1 (byte-identical with the switch off) contradicted §7.8's deliberate merge of two keys of one person | design §7.8, §7.10; B `monthmedals_test.go:398-425` | Accepted | T1 reworded with the §7.8 exception; the golden fixture has no person with two linked keys (§11.1, §13.1). |
