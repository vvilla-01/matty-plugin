# Media lookups: IGDB, the shared Twitch application, remembered titles, and Twitch live

Build contract, written 2026-10-02. It covers Victor's requests of that day:

1. "Add ability to do a non-Steam game search so entries like Hytale and Vintage Story show up as
   well (they're not in the Steam store)." He chose IGDB, Twitch's game database. The admin
   registers one Twitch developer application and pastes its Client ID and Client Secret into the
   plugin settings, as with the TMDB token. Steam stays the first source for games, and IGDB fills
   in the games Steam does not have.
2. "Also cache game titles and movies to the plugin so the server doesnt keep searching online for
   the same game/movies/shows."
3. The same Twitch application will later also be used to let people link their Twitch accounts and
   show as live in VillaNET while they are live on Twitch. Victor's message that started this
   workflow run asked for that feature itself: "Add the ability for users to link their Twitch
   accounts so if they go live on Twitch, it'll show live on the Mattermost plugin".

**Phases.** Phase 1 is Parts A to D: the shared Twitch application, IGDB, and the remembered-titles
cache. It is built now, in the sandbox. Phase 2 is Part E, Twitch live, which is what the message that
started this run asked for. Part E depends only on Part A (the Twitch settings section,
`twitchCredentials` and the `twitchApp` token component) and on the existing poller and badge code. It
therefore **starts as soon as Part A and its tests pass**, in a sandbox of its own, while Parts B to D
carry on. Each phase's diff is taken separately (§D.4). Phase 1 never mentions or stubs Twitch live,
except that it ships a token component that Phase 2 reuses (§A.2).

**Where.** The build happens in the sandbox `/home/vvilla/Documents/Broadcast Box Plugin.igdb-sandbox`,
which is a full copy of 2.11.0. Its diff against `/home/vvilla/Documents/Broadcast Box Plugin.baseline-2.11.0`
is later patched onto the main tree, where 3.0.0 "Clips" is being built. Keep the diff focused: do not
reformat untouched code, and **do not change the version** (plugin.json and `manifestVersion` stay at
2.11.0; the release number is decided when the patch lands).

**Ground rules for every builder**

- Put the toolchains on PATH in every Bash call that builds:
  `export PATH="$HOME/.local/opt/go/bin:$HOME/.local/opt/node/bin:$PATH"`.
- Checks: `cd "/home/vvilla/Documents/Broadcast Box Plugin.igdb-sandbox" && TZ=UTC make check`.
  `TZ=UTC` is needed on this laptop because one birthday test assumes UTC.
- `webapp/node_modules` in the sandbox is a SYMLINK into the main tree. Never run `npm install` or
  `make lab` in the sandbox. Build the lab with:
  `cd "/home/vvilla/Documents/Broadcast Box Plugin.igdb-sandbox/webapp" && npx webpack --config lab/webpack.config.js && cd .. && python3 build/build-lab.py`.
- React 17 only. Do not use useId, useSyncExternalStore, useTransition, useDeferredValue,
  useInsertionEffect or createRoot. Make ids from a module counter. Element ids never contain `vntv-`.
- `webapp/src/styles.ts` is one JS template literal. Put no backticks anywhere in it, and no
  `\00b7`-style escapes. Never edit it by slicing between landmarks; add rules with exact,
  unique-anchor edits. Run `python3 build/check-styles.py` after every edit. The reduced-motion block
  stays last.
- Go style: plain-prose comments that explain WHY. Tests are table-driven where it fits, use
  plugintest mocks and httptest fakes, and must be deterministic under `-race`. **No real network
  call in any test, ever.** No `time.Sleep` to order events: inject a clock (§C.9).
- Never log, return to a browser, or put in an error string any secret: the client secret, the
  TMDB token, an access token, an authorization code, or an OAuth state.

Line numbers below were read from the sandbox on 2026-10-02.

---

## 0. The promises this keeps

These come from `server/media.go:20-38`, the `MediaLookup`/`TMDBToken` help in `plugin.json:184-197`,
the last paragraph of "What the plugin touches" in LOCKDOWN.md, and README.md's media sections. Each
one still holds after Phase 1:

| Promise | How it still holds |
|---|---|
| Media lookups happen only while **MediaLookup** is on, which is off by default. | IGDB is a third game source behind the same switch. Every IGDB call, and every token fetch made for IGDB, sits behind `mediaFlags().IGDB`, which requires `MediaLookup`. The remembered-titles cache is neither read nor written while MediaLookup is off (§C.7). |
| Lookups happen only on demand: a search, saving a plan or What's on? with a title, or a panel asking for art. | Unchanged. The token component fetches and validates only when a caller asks for a token. Nothing runs in `OnActivate`, `OnConfigurationChange` or a timer. |
| Only the SERVER contacts providers. Pictures are relayed, so browsers never contact a provider. | IGDB pictures come from `images.igdb.com` through the existing relay, which gets one more allowed host. The Twitch token and the secret never leave the server. |
| Every outside host is named in the setting help, the README and LOCKDOWN. | New hosts: `id.twitch.tv`, `api.igdb.com` and `images.igdb.com`. `www.igdb.com` is only an outbound link, like the IMDb and Steam links. Wording is in §C.10. |
| Secrets are hidden in the System Console, never sent to a browser, never logged, and left out of settings backups unless the admin ticks the box. | `TwitchClientSecret` is `"secret": true`, is in `settingsSecrets`, never appears in `/me`, and has a test that it never reaches a log (§A.3). The Client ID is not a secret: Twitch calls Client IDs public. |
| Nothing durable except a plan's text. | **This changes, on purpose.** Victor asked for a durable cache. Title text, search results and art addresses are kept in plugin KV, bounded at 2 MiB, and the help text, README and LOCKDOWN all say so. Picture bytes and access tokens stay in memory only (§C.1). |

---

## Part A. The shared Twitch application

### A.1 Settings

**A new section, "Twitch", placed directly after "Movies and games" in plugin.json.** The credentials
belong to the Twitch application, not to the movie lookup. IGDB uses them now, and Twitch live
(Part E) will add its own switch to this same section. Putting them under "Movies and games" would
tie them to MediaLookup in the admin's mind, and Phase 2 would then have to move them, which renames
nothing but confuses anybody who set them up. A separate section keeps one obvious home for one
application, so whichever feature needs Twitch, the admin is sent to the same place.

```json
{
    "key": "twitch",
    "title": "Twitch",
    "subtitle": "One Twitch developer application, shared by every VillaNET feature that uses Twitch. Today that is looking up games in IGDB, Twitch's game database.",
    "settings": [
        {
            "key": "TwitchClientID",
            "display_name": "Twitch application Client ID",
            "type": "text",
            "help_text": "The Client ID of a Twitch developer application. VillaNET uses this application to look up games in IGDB (Twitch's game database) that are not on Steam, such as Hytale and Vintage Story, and will use it for its Twitch features later. It is free: sign in at dev.twitch.tv/console with a Twitch account that has two-factor authentication on, open Applications > Register Your Application, give it a name, set OAuth Redirect URLs to http://localhost (IGDB does not use it; a later VillaNET Twitch feature will show you the address to add), pick any category, and set Client Type to Confidential. Then press Manage and copy the Client ID here. With both Twitch boxes filled in and Look up movies, shows and games on, the Mattermost SERVER also contacts id.twitch.tv (for an access token) and IGDB (api.igdb.com, and images.igdb.com for the pictures), only when somebody searches for a game while planning, saves a plan with an IGDB game picked, or a panel shows that game's art. Browsers never contact Twitch or IGDB. Leave both boxes empty and games come from Steam alone, exactly as before.",
            "default": ""
        },
        {
            "key": "TwitchClientSecret",
            "display_name": "Twitch application Client Secret",
            "type": "text",
            "secret": true,
            "help_text": "From the same application: press New Secret and copy it here. Pressing New Secret again makes a new secret and stops the old one working, so paste the new one here too. Hidden in the System Console once saved, never sent to a browser, never logged, and left out of settings backups unless you tick Include the passwords. Like every plugin setting, Mattermost keeps it in its configuration as plain text, so protect config backups accordingly. The access token Twitch issues for it is kept in memory only.",
            "default": ""
        }
    ]
}
```

The redirect address in Phase 1 is `http://localhost`. IGDB's own setup steps say "The OAuth Redirect
URL field is not used by IGDB. Please add 'localhost' to continue", and Twitch's getting-started guide
uses `http://localhost:3000`, so that address is accepted whatever the Mattermost server's own address
or scheme is. An earlier draft told the admin to register the Phase 2 callback here. That would block
an admin whose Mattermost is plain http on the LAN, and it tied Phase 1 to a Phase 2 route. A Twitch
application can hold several redirect URLs (the console adds them one at a time), so Phase 2's
`TwitchSetup` setting shows the exact callback to **add** (§E.2) and nothing has to be replaced.

**Server changes**

- `configuration.go`: add `TwitchClientID string` and `TwitchClientSecret string` directly after
  `TMDBToken`.
- `normalize()`: trim both. Turn a `TwitchClientSecret` equal to `redactedSetting` into `""`, as
  `TMDBToken` does (`configuration.go:398-408`). Do not reshape the Client ID any further.
- New method in `twitch_app.go`:
  ```go
  // twitchCredentials is the Twitch application the admin set up, or false
  // when either half is missing. Not tied to any feature's switch: each
  // caller decides whether its own feature is on.
  func (c *configuration) twitchCredentials() (twitchCredentials, bool)
  ```
- `defaults.go`: add a `// Twitch` group after `// Movies and games` with `"TwitchClientID": ""` and
  `"TwitchClientSecret": ""`. `TestTheSectionDefaultsMatchTheManifest` checks this list.
- `settings.go`: `settingsSecrets = []string{"BasicAuthPassword", "MumblePassword", "TMDBToken", "TwitchClientSecret"}`.
  Extend the comment to say why: a Twitch client secret is a password to an outside account, and
  Twitch's terms say to keep it secret. Add one line to `settingsNotIncluded`:
  `"Remembered movie, show and game titles — a cache, rebuilt by looking them up again"`.
  No test pins that list (checked).
- `settings_test.go`: update the exact lists at `:153`, `:166`, `:505` and `:616`, and `fullConfig()`
  at `:82-83`. `TestTheBackupAndTheSettingsPageAgreeOnEverySetting` and
  `TestEverySecretSettingIsMarkedSecret` then pass unchanged. Kept and Omitted come out sorted, so
  `TwitchClientSecret` goes last.

**Webapp changes** (`AdminSettingsBackup.tsx`)

- Line 82: the label becomes `'Include the passwords and keys (Basic Auth, Mumble, TMDB token and Twitch client secret)'`.
  The current label is already out of date, because TMDB is a third secret.
- Line 86-88: replace "passwords" with "passwords and keys" in both sentences.
- The "Not in the file" sentence (around `:70-72`) gains ", the remembered movie and game titles"
  before "and who has linked which stream key".

### A.2 The token component: `server/twitch_app.go`

This is one standalone component that knows how to hold a Twitch **app access token**, which comes
from the client-credentials flow. IGDB calls it now, and the Phase 2 Twitch-live poller calls it
later. It does not check MediaLookup or any other feature switch. Each caller gates itself.

```go
// Where Twitch issues tokens. A variable only so tests can point it at a
// fake: nothing a test runs ever leaves the machine.
var twitchIDBase = "https://id.twitch.tv"

type twitchCredentials struct {
    clientID string
    secret   string
}

var (
    errTwitchNotSetUp    = errors.New("no Twitch application is set up")
    errTwitchRefused     = errors.New("Twitch refused the application's Client ID or Client Secret")
    errTwitchUnavailable = errors.New("Twitch did not issue a token just now")
)

// twitchApp holds the one app access token this server has for the admin's
// Twitch application, in memory only.
type twitchApp struct {
    plugin *Plugin
    now    func() time.Time // time.Now; tests replace it

    lock      sync.Mutex
    token     string
    issuedFor [32]byte  // creds.fingerprint() of the credentials it was issued for
    startedAt time.Time // when the fetch that produced it STARTED; a later-started fetch wins the slot
    expiresAt time.Time
    checkedAt time.Time // issued, or last confirmed by /oauth2/validate

    refusedFor   [32]byte  // credentials Twitch refused, and until when that is believed
    refusedUntil time.Time
    failedFor    [32]byte  // credentials whose last fetch failed for some other reason
    retryAt      time.Time

    // Keyed "token/"+hex(fp[:8]) and "validate/"+hex(fp[:8]): one of each per
    // set of credentials at a time, so a caller with a newly pasted secret
    // never joins a fetch made with the old one.
    flights flightGroup

    warnLock sync.Mutex
    warnedAt map[string]time.Time // fixed keys only: "twitch-credentials", "twitch-token"
}

// fingerprint is sha256(clientID + "\x00" + secret). Kept instead of the
// secret wherever credentials have to be compared later; IGDB's hold-back
// (§B.2) uses it too.
func (c twitchCredentials) fingerprint() [32]byte

// twitchApp is made on first use (p.twitchOnce sync.Once, p.twitchState
// *twitchApp in plugin.go beside mediaOnce/mediaState), so a server that
// never sets Twitch up holds none of it.
func (p *Plugin) twitchApp() *twitchApp

// appToken is a token for these credentials, fetching one only when none is
// held, the held one was issued for other credentials, or it is near expiry.
func (a *twitchApp) appToken(ctx context.Context, creds twitchCredentials) (string, error)

// invalidate forgets token if it is still the one held. Called by whoever got
// a 401 with it; a newer token fetched in the meantime is kept.
func (a *twitchApp) invalidate(token string)
```

Constants, in `twitch_app.go`:

| Name | Value | Why |
|---|---|---|
| `twitchTokenBudget` | 8 s | One fetch, on its own context, so the caller that started it going away does not fail the others waiting for it. |
| `twitchRenewBefore` | 1 h | Renew this close to expiry. Twitch discourages renewing early and prefers renewing on a 401, which we also do. An hour before a roughly 60-day expiry costs nothing. |
| `twitchValidateEvery` | 1 h | Twitch asks apps that keep an OAuth session to validate hourly. Whether that covers an app token is UNVERIFIED, so we validate, but only when a token is about to be used. |
| `twitchRefusedPause` | 5 min | After Twitch refuses the credentials, do not ask again with the same ones for this long. Without it, every keystroke in the planning form would be a token request. |
| `twitchFailurePause` | 15 s | After any other failure. |
| `twitchWarnEvery` | 15 min | The component's own warning throttle, the same length as `mediaWarnEvery` but not shared with it (see Dependencies below). |
| `twitchMaxLifetime` | 90 days | Clamp on `expires_in`. A missing or non-positive value counts as 24 h. |
| `maxTwitchAnswerBytes` | 64 KiB | Cap on reading any id.twitch.tv answer. |

**`appToken(ctx, creds)` step by step**

1. Without credentials, return `errTwitchNotSetUp`. If `ctx` is already done, return `ctx.Err()`.
2. Lock and compute `fp := creds.fingerprint()`. Keep only the fingerprint, never a copy of the
   secret. `fp8 := hex(fp[:8])` names this caller's flights.
   - If `fp == refusedFor` and now is before `refusedUntil`, return `errTwitchRefused` and make no
     request.
   - If `fp == failedFor` and now is before `retryAt`, return `errTwitchUnavailable`.
   - If a token is held, `issuedFor == fp`, and now is before `expiresAt - twitchRenewBefore`:
     - if `now - checkedAt < twitchValidateEvery`, return the token;
     - otherwise unlock and **validate** (step 3).
   - Otherwise unlock and **fetch** (step 4).
3. **Validate**, through `flights.do("validate/"+fp8)`:
   - Request: `GET {twitchIDBase}/oauth2/validate` with `Authorization: OAuth <token>`.
   - 200: parse `{"client_id","expires_in"}`. If `client_id` equals `creds.clientID`, set
     `checkedAt = now`, update `expiresAt` when `expires_in > 0`, and return the token. If the client
     ID differs, invalidate and go to step 4.
   - 401: `invalidate(token)` and go to step 4.
   - Any other answer, or a transport error: return the token anyway, and set
     `checkedAt = now - twitchValidateEvery + 5 min` so validation is tried again in 5 minutes. A
     validation hiccup must not stop lookups.
4. **Fetch**, through `flights.do("token/"+fp8)`, on `context.WithTimeout(context.Background(), twitchTokenBudget)`.
   Inside the flight, first re-check under the lock: if another flight just stored a good token for
   `fp`, return it. Note the time the fetch starts. The flight's value is the token together with the
   `fp` it was issued for, and a waiter whose own `fp` differs (which keyed flights should make
   impossible) fetches again, once.
   - Request: `POST {twitchIDBase}/oauth2/token` with
     `Content-Type: application/x-www-form-urlencoded`, and a body (never a query string, so the
     secret is never in a URL) of `client_id`, `client_secret` and `grant_type=client_credentials`.
     Send it through `sendMedia`, which sets the User-Agent and strips `*url.Error` (see Dependencies).
   - 200: decode `{"access_token","expires_in","token_type"}`. `access_token` must be 1 to 512
     printable ASCII bytes with no spaces. `token_type` must be `bearer` in any case. Store the token,
     `issuedFor = fp`, `startedAt`, `expiresAt = now + clamp(expires_in)` and `checkedAt = now`, clear
     the failure marks, and return it. **Exception:** when the held token came from a fetch that
     started later than this one (the admin changed the credentials while this fetch was out), keep
     the held one and only return this one to its own waiters. Old credentials' late answers must not
     displace the new ones'.
   - 400, 401 or 403: set `refusedFor = fp` and `refusedUntil = now + twitchRefusedPause`, warn once
     (key `"twitch-credentials"`), and return `errTwitchRefused`. A made-up Client ID was OBSERVED to
     give `400 {"status":400,"message":"invalid client"}`. A wrong secret is commonly reported as 403
     (UNVERIFIED).
   - Anything else (429, 5xx, a bad shape, or a transport error): set `failedFor = fp` and
     `retryAt = now + twitchFailurePause`, warn once (key `"twitch-token"`), and return
     `errTwitchUnavailable`.
5. `appToken` waits for the shared flight. The flight is bounded by its own 8 s budget, so the wait is
   bounded too.

**What the admin is told** (LogWarn at most once per `twitchWarnEvery`, which is 15 min, per key,
through the component's own `warnedAt`):

- `"twitch-credentials"`: *"Twitch refused the Twitch application's Client ID or Client Secret, so
  VillaNET cannot use Twitch (IGDB game lookups stop until this is fixed). In the Twitch developer
  console (dev.twitch.tv/console/apps) open the application, check the Client ID, press New Secret,
  and paste both into System Console > Plugins > VillaNET > Twitch."* Add the pair `"status", <code>`
  and nothing else.
- `"twitch-token"`: *"Twitch did not issue an access token just now; VillaNET will try again shortly."*
  Add `"status", <code>` when there was one, or `"error", <fixed string>` for a transport failure.
  Never pass the answer body.

**Never logged, never returned.** The secret, the token and the answer body appear in no log line, no
error string and no HTTP response. Errors are the three fixed values above, wrapped by callers with a
provider prefix (`"IGDB: "`).

**Not stored in KV.** KV outlives an uninstall and ends up in database backups. A token in memory
costs one fetch per plugin start and one per cluster node, and each counts towards Twitch's limit of
**25 active tokens per application** (from IGDB's FAQ). When too many are issued, Twitch cancels the
oldest ones. A node whose token was cancelled gets a 401, refetches, and carries on, so the limit
heals itself. Do not revoke on deactivate: it would add an outside call to every plugin stop, for
little benefit.

**Cluster.** Each node holds its own token. That is fine for any cluster this plugin will meet.

**For Phase 2.** The Helix poller calls `appToken` the same way, and on a 401 calls `invalidate` and
retries once.

**Dependencies.** `twitch_app.go` **may** call `sendMedia`, and so use `mediaHTTPClient` with its
User-Agent, its 6 s timeout and its same-host redirect rule. That client is already the plugin's one
client for services outside the admin's network, and its `*url.Error` stripping is wanted here too.
Moving it into a neutral file would only add churn to `media.go`, which this patch already changes
heavily. `twitch_app.go` **must not** read `MediaLookup`, `mediaFlags`, `mediaStore` or `warnMediaOnce`,
so switching lookups off never changes what the token component does for another feature. Tests may
share the media fake.

### A.3 Tests: `server/twitch_app_test.go`

Use the media fake (§D.1), which gains a route table entry for `/oauth2/token` and `/oauth2/validate`,
and records the method, the `Client-ID` header, `Content-Type` and the body. Drive time with a fake
clock assigned to `p.twitchApp().now`.

| Test | Asserts |
|---|---|
| `TestTheAppTokenIsFetchedOnceAndReused` | Two calls make one POST. The body has exactly `client_id`, `client_secret` and `grant_type=client_credentials`; the query is empty; the Content-Type is form. |
| `TestEverybodyWaitingForATokenSharesOneFetch` | Deterministic, with no sleeps. The token handler signals on `entered` and then blocks on `release`. Goroutine 1 calls `appToken`. After `<-entered`, start goroutines 2 to 20. The flight is registered before the handler runs and stays registered until release, so they join it or find the stored token. Close `release`. All 20 get the same token, and there is exactly one POST. |
| `TestTheTokenIsRenewedNearItsExpiry` | `expires_in: 10800` (3 h), so renewal starts at +2 h. At +30 min, no request. At +61 min, one validate (which answers the remaining `expires_in`) and no POST. At +2h01m (less than 1 h left), one new POST. |
| `TestA401LeadsToANewToken` | `invalidate(old)` then `appToken` makes a second POST. `invalidate(old)` after a newer token is held keeps the newer one. |
| `TestTheTokenIsValidatedHourly` | After 61 min: a GET to `/oauth2/validate` with `Authorization: OAuth tok`. A 200 reuses the token. In the next case, a 401 makes a new POST. A client ID mismatch makes a new POST. A 500 keeps the token and validates again 5 minutes later. |
| `TestRefusedCredentialsAreReportedOnceAndNotRetriedAtOnce` | A 400 `invalid client` gives `errTwitchRefused` and one LogWarn. Another call within 5 min gives the same error with no request and no second warning. At +6 min, one new request. New credentials make a request at once. |
| `TestATransientFailurePausesBriefly` | 500, then a call within 15 s makes no request; at +16 s, one request. |
| `TestAStrangeTokenAnswerIsNotKept` | Table: missing `access_token`; `token_type: "mac"`; a token with a space; `expires_in` missing (kept with a 24 h life). |
| `TestChangingTheCredentialsDropsTheToken` | The token is held, the secret changes, and the next call makes a POST with the new secret. Second case, during a fetch: the handler for the old secret blocks on `release`; a caller with the new secret, started after `<-entered`, makes its own POST with the new `client_secret` and gets its own token rather than joining the old flight. Close `release`: the old fetch's late answer does not replace the new token (a third call makes no POST and returns the new token). |
| `TestNothingIsFetchedWithoutCredentials` | Empty ID or secret: `errTwitchNotSetUp` and zero requests. |
| `TestTheSecretAndTokenNeverReachALog` | Mock every `Log*` with a capture. Run refusal, failure, validation and success. No captured argument and no returned error string contains the secret, the token, or `client_secret`. |

---

## Part B. IGDB as the third game source

### B.1 Refs, flags and stored titles (`server/media.go`)

**Ref format: `igdb:<id>`**, with an id of 1 to 10 digits, normalised through `ParseUint` like the
others. `igdb:0069547` becomes `igdb:69547`.

```go
const (
    mediaProviderTMDB  = "tmdb"
    mediaProviderSteam = "steam"
    mediaProviderIGDB  = "igdb"
    mediaSourceIGDB    = "IGDB"
)

mediaRefPattern = regexp.MustCompile(`^(?:tmdb:(movie|tv)|(steam|igdb)):([0-9]{1,10})$`)

type mediaRef struct {
    provider string // mediaProviderTMDB, mediaProviderSteam or mediaProviderIGDB
    kind     string // movie, tv or game
    id       string
}
```

- `parseMediaRef`: group 1 gives provider `tmdb` with that kind. Group 2 gives provider `steam` or
  `igdb` with kind `game`. Group 3 is the id. `steam:tv:1`, `tmdb:game:1`, `igdb:movie:1`, `igdb:` and
  `igdb:x` are refused.
- `newMediaRef(kind, id)` keeps its signature and callers: provider is `steam` when the kind is game,
  otherwise `tmdb`. Add `newIGDBRef(id int64) (mediaRef, bool)`, with the same bounds as today
  (id must be above 0 and at most 9999999999).
- `String()`: tmdb gives `"tmdb:"+kind+":"+id`; everything else gives `provider+":"+id`.
- New `source()`: tmdb gives `"TMDB"`, steam gives `"Steam"`, igdb gives `"IGDB"`.
- `category()` is unchanged, because it goes by kind.
- `checked()` (`media.go:318-324`): replace the derived source with `if m.Source != ref.source() { return nil }`.
- `mediaLinkPrefixes` gains `"https://www.igdb.com/games/"`.

**Flags**

```go
type mediaFlags struct {
    Movies bool `json:"movies"`
    Games  bool `json:"games"`
    IGDB   bool `json:"igdb"` // games also come from IGDB
}
// IGDB: c.MediaLookup && credentials present (twitchCredentials ok).

// allowsRef is allows by kind, except that an IGDB title also needs IGDB.
// Used everywhere a REF is gated: planMedia, handleMediaImage.
func (f mediaFlags) allowsRef(ref mediaRef) bool
```

- `allows(kind)` and `unavailable(kind)` are unchanged, because games still need only MediaLookup.
- `planMedia` (`media.go:1000`) and `handleMediaImage` (`media_api.go:97`) switch to `allowsRef(ref)`.
- `/me` carries the new `igdb` flag. Update `TestMeSaysWhichTitlesCanBeLookedUp`.

**Stored titles.** `scheduleMedia` gets **no new field**. An IGDB title is `Source: "IGDB"` with a
`https://www.igdb.com/games/<slug>` link, so `media_test.go:762` (which asserts the marshalled prefix)
still passes. An older build reading an `igdb:` ref fails `parseMediaRef`, so the plan shows without
its title rather than an error, and a CAS re-marshal on the older build keeps the ref string.
Document that in the release notes.

### B.2 Requests (`server/igdb.go`, new)

```go
var (
    igdbAPIBase   = "https://api.igdb.com/v4"
    igdbImageBase = "https://images.igdb.com/igdb/image/upload"
)
```

`mediaImageHosts` gains `"images.igdb.com"`. Both URLs are package variables so tests can point them at
the fake.

```go
// igdbPost asks IGDB one Apicalypse query. Every call is a POST with the query
// as the body, signed with the Twitch application's app access token.
func (p *Plugin) igdbPost(ctx context.Context, cfg *configuration, endpoint, body string, into any) error
```

IGDB's state in `mediaStore`, beside the Steam and TMDB limiters:

```go
igdbLimiter *rateLimiter // newRateLimiter(igdbCallsPerMinute, time.Minute), asked with allowAt("igdb", store.now())

// igdbStarts are the start times of the last igdbCallsPerSecond requests, a
// ring. A request may start only when the oldest of them is at least a second
// old: exactly IGDB's "4 requests per second" over ANY one-second window. A
// token bucket (rateLimiter) would let 4 go at once and then refill, which is
// about 7 inside one second.
igdbLock   sync.Mutex
igdbStarts [igdbCallsPerSecond]time.Time
igdbNext   int

// The hold-back: while now < igdbHeldUntil and the credentials' fingerprint is
// igdbHeldFor, igdbPost answers igdbHeldErr without contacting id.twitch.tv or
// IGDB at all.
igdbHeldUntil    time.Time
igdbHeldFor      [32]byte
igdbHeldErr      error  // errTwitchRefused or errIGDBResting
igdbRefusedToken string // a freshly fetched token IGDB still refused; never invalidated (memory only, never logged)

igdbSlots chan struct{} // make(chan struct{}, igdbConcurrent)
```

`errIGDBResting = errors.New("IGDB did not answer properly just now, so it is left alone for a few seconds")`.

1. Without credentials, return `errors.New("IGDB: no Twitch application is set up")`. Callers only get
   here when `flags.IGDB` is true, so this is defensive.
2. **Held back?** If `store.now()` is before `igdbHeldUntil` and `creds.fingerprint() == igdbHeldFor`,
   return `fmt.Errorf("IGDB: %w", igdbHeldErr)` with no request of any kind. New credentials end the
   hold-back at once.
3. **Budget**, with `now := store.now()`: first `igdbLimiter.allowAt("igdb", now)` (120 per minute),
   then the per-second window (`now.Sub(igdbStarts[igdbNext]) >= time.Second`; if so, record `now`
   there and advance `igdbNext`). If either says no, return `fmt.Errorf("IGDB: %w", errMediaBusy)`.
   IGDB allows 4 requests a second and 8 open at once, and its agreement forbids going over. We keep
   under half of IGDB's per-minute rate, as for Steam. Both checks use the store's clock, so the tests
   drive them with the fake clock and never sleep.
4. `token, err := p.twitchApp().appToken(ctx, creds)`. On error, return `fmt.Errorf("IGDB: %w", err)`.
5. Take a slot from `mediaStore.igdbSlots` with a `select` on `ctx.Done()`, and release it after the
   response body is read. `MaxConnsPerHost` does not bound HTTP/2 streams, so this slot is what keeps
   us under IGDB's 8 open requests.
6. Request: `POST igdbAPIBase+"/"+endpoint` with `strings.NewReader(body)` and these headers:
   - `Client-ID: <client id>` (exactly this capitalisation)
   - `Authorization: Bearer <token>`
   - `Content-Type: text/plain`
   - `Accept: application/json`, set by `getMediaJSON`
   `getMediaJSON` already takes a prepared request, so it needs no change. If `NewRequest` fails,
   return a fixed string.
7. Status:
   - **401**, on the first attempt, with a token that is not `igdbRefusedToken`:
     `p.twitchApp().invalidate(token)` and **retry once** from step 3 (the retry is a real request
     and takes its own place in the budget; the 4-a-second window leaves room for it).
   - **401 on the retry, a 401 with `igdbRefusedToken`, or any 403:** do **not** invalidate the token.
     A token fetched moments ago that is still refused, or a 403, means the token is not what is wrong,
     and minting another would only spend one of
     the application's **25 active tokens** (IGDB FAQ 4: going over "starts to inactivate older
     tokens", which would cancel other cluster nodes' tokens and Phase 2's). Set the hold-back:
     `igdbHeldUntil = now + twitchRefusedPause` (5 min), `igdbHeldFor = fp`,
     `igdbHeldErr = errTwitchRefused`, `igdbRefusedToken = token`. Then
     `warnMediaOnce("igdb-token", "IGDB refused VillaNET's Twitch access token (401/403), so games not on Steam cannot be looked up. Check the Client ID and Client Secret in System Console > Plugins > VillaNET > Twitch.")`,
     and return `IGDB: errTwitchRefused`. So each token is invalidated by IGDB at most once, and while
     IGDB keeps refusing, the only new tokens are the one after the first 401 and whatever the token
     component's own hourly validation decides.
   - **400**: `warnMediaOnce("igdb-query", "IGDB refused a query VillaNET built (400 Bad Request). If this keeps happening, IGDB may have changed its fields and games not on Steam cannot be looked up until the plugin is updated.")`.
     Typed text cannot cause it any more (§B.3 keeps only a safe set of characters).
   - 404 and 410 become `errMediaNotFound` as today (`apiStatus`).
   - **429, any 5xx, a transport error, or IGDB's own request timing out** (the client's 6 s, or a
     context **deadline**; a context that was **cancelled** is the caller going away and sets nothing):
     set the hold-back for `igdbFailurePause` (30 s) with
     `igdbHeldErr = errIGDBResting`, and return the error as a `mediaStatusError` or the transport
     error. Neither is ever cached as missing. The pause stops a slow or failing IGDB from holding
     every keystroke up (§B.4).
8. Wrap every error as `fmt.Errorf("IGDB: %w", err)`.

**One defensive type.** IGDB returns a reference as a bare number unless it is expanded, and as an
object when it is. Any reference field we read that might come back either way uses:

```go
// igdbRef is an IGDB reference whether it arrives as 12 or as {"id":12,...}.
type igdbRef struct{ ID int64 }
func (r *igdbRef) UnmarshalJSON(raw []byte) error
```

```go
type igdbImage struct{ ImageID string `json:"image_id"` }
type igdbNamed struct{ Name string `json:"name"` }
type igdbLink struct {
    Type igdbRef `json:"type"`                           // websites
    Source igdbRef `json:"external_game_source"`         // external_games
    UID  string  `json:"uid"`
    URL  string  `json:"url"`
}
type igdbGame struct {
    ID                int64      `json:"id"`
    Name              string     `json:"name"`
    Slug              string     `json:"slug"`
    URL               string     `json:"url"`
    Summary           string     `json:"summary"`
    Storyline         string     `json:"storyline"`
    FirstReleaseDate  int64      `json:"first_release_date"`
    Cover             *igdbImage `json:"cover"`
    Artworks          []igdbImage `json:"artworks"`
    Screenshots       []igdbImage `json:"screenshots"`
    Genres            []igdbNamed `json:"genres"`
    Themes            []igdbRef   `json:"themes"`
    GameType          *struct{ ID int64 `json:"id"`; Type string `json:"type"` } `json:"game_type"`
    GameStatus        *struct{ ID int64 `json:"id"`; Status string `json:"status"` } `json:"game_status"`
    VersionParent     igdbRef    `json:"version_parent"`
    ParentGame        igdbRef    `json:"parent_game"`
    ExternalGames     []igdbLink `json:"external_games"`
    Websites          []igdbLink `json:"websites"`
    InvolvedCompanies []struct {
        Developer bool       `json:"developer"`
        Company   *igdbNamed `json:"company"`
    } `json:"involved_companies"`
}
```

Use only the new field names: `game_type`, `game_status`, `external_game_source`, and `type` on
websites. IGDB ended the transition period for the old enum names on August 31 (the docs give
"February 18 to August 31" without a year; probably 2025).

**The ids are documented, not guesses.** IGDB's migration notes say "All current enum values will
remain the same in the new table structure" and "Only the field names are changing, not the values
they contain" (the one stated exception is age-rating categories, which we do not read). The enum
tables in the same docs give: game type `main_game` 0, `dlc_addon` 1, `expansion` 2, `bundle` 3,
`standalone_expansion` 4, `mod` 5, `episode` 6, `season` 7, `remake` 8, `remaster` 9, `expanded_game` 10,
`port` 11, `fork` 12, `pack` 13, `update` 14; game status `released` 0, `alpha` 2, `beta` 3,
`early_access` 4, `offline` 5, `cancelled` 6, `rumored` 7, `delisted` 8; external game source `steam` 1;
website type `steam` 13. Only the `type`/`status` **text** of the new tables is not shown in the docs,
so the code accepts either the text or the id.

### B.3 Search

```go
const igdbSearchLimit = 16 // raw rows asked for; at most 8 are kept after filtering

const igdbSearchFields = "name,first_release_date,cover.image_id,game_type.type,parent_game,version_parent," +
    "external_games.external_game_source,external_games.uid,external_games.url,websites.type,websites.url"
```

**Request body**, exactly. `%s` is `igdbSearchText(query)`:

```
search "%s";
fields name,first_release_date,cover.image_id,game_type.type,parent_game,version_parent,external_games.external_game_source,external_games.uid,external_games.url,websites.type,websites.url;
where version_parent = null & themes != (42) & (game_type = (0,4,8,9,10,11) | game_type = null);
limit 16;
```

- `igdbSearchText` keeps only a **known-safe set** of characters, because IGDB does not document its
  string grammar at all (its own docs even write `search “zelda”;` with curly quotes, so whether “ ” ‘ ’
  also end a string is unknown):
  - apostrophes (`'` and `’`) are removed without leaving a space, as `mediaQueryKey` does, so
    "Baldur's Gate" is sent as `Baldurs Gate` (IGDB's own example searches `"Assassins Creed"`, and
    its search is by similarity, so nothing is lost);
  - letters (`unicode.IsLetter`), digits (`unicode.IsDigit`), spaces and `- : . , & ! ? +` are kept;
  - **everything else** becomes a space: `"`, `\`, `;`, curly quotes, brackets, `*`, control
    characters and the rest;
  - then whitespace is collapsed and trimmed.

  If fewer than 2 runes are left, IGDB is not asked for this query: it contributes an empty list, as
  if it were off, and nothing is remembered for it.
- `version_parent = null` is IGDB's own documented way to leave editions out. `themes != (42)` is its
  documented way to leave out erotic games. The `game_type` group uses the documented ids (§B.2 note)
  and IGDB's documented `(a,b)` "any of" and grouping syntax, so that names with many DLCs still fill
  all 16 rows with playable games. Untyped rows are let through and judged by rule 2 below.

**Kept rows** (`igdbPlayable(g)`), in IGDB's relevance order. The `where` clause already does most of
this; the checks guard against IGDB changing behaviour:

1. Game type. Normalise `game_type.type` by lower-casing it and turning every run of non-alphanumerics
   into `_` (so "Main Game" becomes `main_game`). When it is present, keep only `main_game`,
   `standalone_expansion`, `remake`, `remaster`, `expanded_game` and `port`. When the text is empty but
   an id is present, keep ids `{0, 4, 8, 9, 10, 11}`. This drops DLC, bundles, mods, episodes,
   seasons, forks, packs and updates.
2. **Only when the row has no game type at all**, a non-zero `parent_game` drops it. IGDB describes
   `parent_game` as "If a DLC, expansion or part of a bundle, this is the main game or bundle", but
   the reverse arrays (`remakes`, `remasters`, `ports`, `expanded_games`) can only be filled from it,
   so remakes, remasters and ports carry a `parent_game` too. When a type is present, rule 1 has
   already decided, and a kept remake must not be dropped here.
3. `version_parent` set drops the row.
4. A missing ref or an empty `cleanText(plainText(name), 120)` drops the row.
5. A row whose `mediaQueryKey(title)` (§C.6) plus year equals an earlier kept row's drops it, so ports
   with the same name collapse into one.

**A Steam link is recorded, not acted on here.** `igdbSteamApp(g)` returns a Steam app id when:

- any `websites[].url` or `external_games[].url` matches
  `^https?://store\.steampowered\.com/app/([0-9]{1,10})(?:[/?#]|$)`; or
- any `external_games[]` row has `external_game_source` 1 (documented as Steam) and a `uid` of 1 to 10
  digits.

The id goes on the result as `SteamApp` (§C.2) and `mergeGames` decides (§B.4). An earlier draft
dropped every IGDB row with a Steam link, on the theory that Steam's search would list it. It does not
list games Steam no longer sells. OBSERVED 2026-10-02: `storesearch` with `cc=US` for "forza horizon 4"
lists only Forza Horizon 5 add-ons, and for "spec ops the line" lists nothing, although both still have
Steam links in IGDB. Those games are "not in the Steam store" exactly as Victor meant.

**Each kept row becomes:**

- `mediaResult{Ref: "igdb:<id>", Kind: "game", Title, Year, Label: "Game"}`, with `SteamApp` set as
  above.
- `Year` is `time.Unix(first_release_date, 0).UTC().Year()` when it is at least 1000 and at most 2999
  and `first_release_date > 0`; otherwise `""`.
- Its art hint is `mediaArt{Poster: cover.image_id}` when the id matches `igdbImageID`
  (`^[A-Za-z0-9_-]{1,64}$`).

Stop after 8 kept rows.

### B.4 Merging with Steam

`searchMedia(kind="game")` asks Steam (always, since `flags.Games` is required to get here) and IGDB
(when `flags.IGDB`, and `igdbSearchText` leaves at least 2 runes) **at the same time**, each through
`providerSearch` (§C.4), under the existing 8 s `mediaSearchBudget`.

**A slow IGDB does not hold Steam's answer.** Once Steam has answered successfully, wait for IGDB at
most `igdbGrace` (1.5 s, a package variable so tests can shorten it) more, then answer with Steam alone,
as if IGDB were off and with no warning. IGDB's search keeps running in its flight, on its own context
(§C.4), so its answer still lands in L1 and L2 and the next keystroke merges it. When Steam failed,
wait for IGDB until the budget ends. After an IGDB timeout, 429 or 5xx, the hold-back in §B.2 skips
IGDB for 30 s, so for that time game searches cost exactly what Steam costs.

| Steam | IGDB | Answer |
|---|---|---|
| ok | ok, off, held back, or still running after the grace | `mergeGames(steam, igdb, normal)`, with an empty IGDB list in the last three cases |
| failed | ok | IGDB results only. HTTP 200. |
| ok | failed | Steam results only. HTTP 200. |
| failed | failed, off or held back | The error, so 502 with the existing sentence and the existing per-request LogWarn. |

**Logging a partial failure.** It is the admin's business only when it keeps happening, so
`warnMediaOnce("search-partial-"+provider, "A title search was answered without one of its sources", "provider", "Steam" or "IGDB", "error", err.Error())`,
at most once per 15 minutes per provider (two fixed keys). Nothing at all is logged for
`errTwitchNotSetUp`, `errTwitchRefused`, `errIGDBResting` or `errMediaBusy`, which are either reported
elsewhere already (the Twitch component's and `igdbPost`'s own warnings) or expected. The total
failure (502) keeps today's LogWarn per request, which `TestAFailedSearchSaysSoWithoutGivingAnythingAway`
pins.

```go
// mergeGames: Steam first, IGDB filling in, at most maxMediaResults.
func mergeGames(steam, igdb []mediaCachedResult, normalQuery string) []mediaCachedResult
```

1. **Leave to Steam only what Steam listed.** Drop an IGDB result when its `SteamApp` equals the id of
   a Steam result **in this answer**, or when its `mediaQueryKey(Title)` equals that of any Steam
   result (Steam results carry no year, so the comparison is on the title alone; this catches IGDB
   records with no Steam link). Every other IGDB result is kept, **including ones with a Steam link
   that Steam did not list**: delisted and region-locked games, which Steam's search never lists
   whatever is typed. One consequence, accepted: a game Steam still sells but did not list for a
   short prefix can appear as an IGDB entry for that prefix, and as a Steam entry once more is typed.
   Either way it appears once per list, and either ref resolves.
2. **Exact matches first.** Take out the results whose `mediaQueryKey(Title) == normalQuery`: Steam's
   in Steam order, then IGDB's. Call them E. The rest are S′ and I′.
3. Let `r = 8 - len(E)`. Then `nI = min(len(I′), max(r - len(S′), min(3, r)))` and
   `nS = min(len(S′), r - nI)`.
4. The answer is `E ++ S′[:nS] ++ I′[:nI]`.

So Steam keeps 5 of 8 places when both are full, IGDB fills whatever Steam leaves, and a game typed in
full appears first wherever it came from. Worked cases, which the test table must include:

- 8 Steam and 3 IGDB give 5 + 3.
- 2 Steam and 8 IGDB give 2 + 6.
- 8 Steam and 0 IGDB give 8.
- "hytale" with 0 Steam and Hytale from IGDB gives Hytale only.

### B.5 Details

`mediaDetailsFor` (`media.go:777-784`) switches on `ref.provider`: tmdb uses movie or tv as today,
steam uses `steamGame`, and igdb uses `igdbGame`. The default case is NotFound, never Steam.

**Request body** for `igdbGame(ctx, cfg, ref)`:

```
fields name,slug,url,summary,storyline,first_release_date,cover.image_id,artworks.image_id,screenshots.image_id,genres.name,themes,game_type.type,game_status.status,involved_companies.developer,involved_companies.company.name;
where id = <ref.id>;
limit 1;
```

- An empty array means `errMediaNotFound`. So does a game with theme 42, and so does an empty title.
- Do **not** apply `igdbPlayable` here. A ref the search handed out must keep resolving even if IGDB
  later re-types it.
- `Title`: `cleanText(plainText(name), maxMediaTitle)`.
- `Year`: as in search.
- `Overview`: `overviewOf(summary, storyline)`.
- `Facts`: `factLine(first two genre names…, first company with developer:true)`, the same shape as
  Steam's. For Vintage Story that gives `Indie · Anego Studios`. Each genre name goes through
  `igdbGenreName`: a name ending in a parenthesised abbreviation of 2 to 6 capital letters
  (`^(.+) \(([A-Z]{2,6})\)$`) becomes the abbreviation, so IGDB's "Role-playing (RPG)" (the name its
  own docs show for genre 12) reads `RPG`, "Real Time Strategy (RTS)" reads `RTS` and "Turn-based
  strategy (TBS)" reads `TBS`. Every other name is kept as IGDB wrote it ("Hack and slash/Beat 'em
  up", "Card & Board Game"); `factLine` already caps the line at 100 runes. Table test:
  `TestIGDBGenreNamesAreShortened` over those five names.
- `Link`: `g.URL` when it matches `^https://www\.igdb\.com/games/[a-z0-9-]{1,120}$`, else
  `"https://www.igdb.com/games/"+slug` when `slug` matches `^[a-z0-9-]{1,120}$`, else return
  `errors.New("IGDB: the answer was not the expected shape")`.
- `Source`: `"IGDB"`.
- `art`: `mediaArt{Poster: cover image_id, Backdrop: first artworks image_id, else first screenshots image_id}`,
  each validated with `igdbImageID`.
- `changing` (§C.3) is true when there is no `first_release_date`, or it is in the future, or the
  normalised `game_status.status` is one of `alpha`, `beta`, `early_access` or `rumored`, or, when that
  text is empty, the `game_status` id is one of the documented 2, 3, 4 or 7.

`mediaDetails` gains two unexported fields: `fetchedAt time.Time` and `changing bool`. Every provider
function sets `changing`:

- TMDB movie: add `Status string \`json:"status"\``. Changing when status is not `Released`, or the
  release date is empty or in the future.
- TMDB TV: add `Status`. Changing when it is `Returning Series`, `In Production`, `Planned` or `Pilot`.
  That gives a separate life (§C.3).
- Steam: add `ComingSoon bool \`json:"coming_soon"\`` inside `release_date`. Changing when true.

For IGDB, `mediaArt` fields hold IGDB `image_id`s: Poster is the portrait cover and Backdrop is the
wide picture. Say so in the comment on `mediaArt`, which also gains JSON tags because it is now
stored (§C.2).

### B.6 Pictures

`pictureAddresses` switches on `ref.provider`:

| Size | IGDB address(es), best first | Shape |
|---|---|---|
| thumb | `t_cover_small` of Poster | portrait 90×128 |
| cover | `t_cover_big` of Poster | portrait, fits within 264×374 (Hytale's came back 264×352) |
| backdrop | `t_screenshot_big` of Backdrop, then `t_720p` of Poster | wide (OBSERVED 940×529 for a 1920×1080 source); the fallback is portrait |

```go
// igdbImageURL is "" unless id is the shape IGDB's are, so nothing but an id
// is ever appended to the image host.
func igdbImageURL(size, id string) string // igdbImageBase + "/t_" + size + "/" + id + ".jpg"
```

- The first pass of `loadMediaPicture` (`media.go:1112-1114`) builds `steamLibraryURL` only when
  `ref.provider == steam`, not when the kind is game.
- `fetchMediaPicture` is unchanged. It already enforces https, the host list, 2 MiB, the byte sniff,
  and no SVG.
- Do not hard-code pixel sizes anywhere. The CDN's real sizes differ from IGDB's table.

**Old art addresses.** IGDB deletes replaced images after 30 days, and Steam's capsule addresses
change their content hash when the art changes. So when **at least one** address from a title's
details was tried and every address tried came back NotFound, Refused or NotImage, and those details
were fetched more than 24 h ago (`mediaRefreshArtAfter`), `loadMediaPicture` calls
`refreshMediaDetails` once. A title whose details list no address at all for this size (a TMDB title
with no poster or backdrop) is not refreshed: there was nothing old to replace, and refreshing it would
cost a details call per size per day for nothing. That function skips
L1 and L2, runs under flight key `"details-fresh/"+ref`, writes through, and treats a NotFound
exactly as §C.4 does (the second one, an hour later, is believed). Then try the addresses not
tried yet. This applies to every provider.

### B.7 Credentials absent, MediaLookup off

- **No credentials:** `flags.IGDB` is false. Game search asks Steam only, so the merge receives an
  empty IGDB list, which is exactly 2.11.0's behaviour. Stored `igdb:` plans keep their text. Their
  pictures get a 404 (`allowsRef`), and `planMedia` returns nil for a new `igdb:` pick. The webapp
  never offers one, because no search returns it.
- **MediaLookup off:** no search, no plan lookup, no picture, no token fetch, and no cache read or
  write. That is zero outside requests, which `TestNothingIsLookedUpWhenTheFeatureIsOff` must assert
  with credentials **set**.

### B.8 Webapp

`src/mediaRefs.ts` (new):

```ts
export type MediaSource = 'TMDB' | 'Steam' | 'IGDB';
export function mediaSourceOf(ref: string): MediaSource | '';   // by prefix tmdb: / steam: / igdb:
export function artShape(ref: string): 'wide' | 'tall';         // 'wide' only for steam: refs
```

- `types.ts`:
  - `Me.media` becomes `{movies: boolean; games: boolean; igdb?: boolean}`, with the comment: games
    also come from IGDB when the admin set up a Twitch application.
  - The `MediaInfo` comments name IGDB: the ref example `igdb:113360`, the link "IMDb, TMDB, the Steam
    store or IGDB", and the source "'TMDB', 'Steam' or 'IGDB'".
  - Update the `client.ts:429` comment the same way.
- `TitleSearch.tsx`:
  - New prop `igdb?: boolean`, passed by `ScheduleControls` and `WhatsOn` as `Boolean(me?.media?.igdb)`.
  - Heading (line 297): `kind === 'game' ? (igdb ? 'Games on Steam and IGDB' : 'Games on Steam') : 'Movies and shows on TMDB'`.
  - `source` (line 163) for games becomes `igdb ? 'Steam or IGDB' : 'Steam'` in the empty note
    ("Nothing on Steam or IGDB called “…”."). The failure note reads
    `${igdb ? 'Steam and IGDB' : 'Steam'} did not answer just now.`
  - `MediaThumb` adds `data-art={artShape(result.ref)}`. The waiting placeholders keep `data-kind`
    only, so they stay wide for games.
  - `metaOf`: `[label, year, mediaSourceOf(ref) === 'IGDB' ? 'IGDB' : ''].filter(Boolean).join(' · ')`.
    That labels IGDB results in the list, which the Twitch Developer Agreement's attribution clause
    (§VII.C) asks for.
- `MediaProfile.tsx`:
  - `LINK_HOSTS` gains `'https://www.igdb.com/'`.
  - `linkLabel` returns `'IGDB'` for an igdb.com link.
  - Change the attribution comment at `:269` to say TMDB and IGDB both ask for attribution. The line
    itself (`Data from ${media.source}`) already works.
- `PopoverLive.tsx:91` and `:180`: add `data-art={artShape(media.ref)}` beside `data-kind`.
- `WhatsOn.tsx` (ChipArt uses `cover` for games), `UpNext.tsx` (backdrop) and `NowPlaying.tsx` need
  no change. IGDB's cover is already portrait. Update the comment at `NowPlaying.tsx:18` to "its IMDb,
  Steam or IGDB page".
- `styles.ts`: two additive rules, each placed directly after the rule it refines. Do not change any
  existing selector.
  ```css
  /* IGDB's game art is box art, portrait like a poster, rather than Steam's wide capsule. */
  .vntv-suggest-thumb[data-kind="game"][data-art="tall"],
  .vntv-picked-thumb[data-kind="game"][data-art="tall"] {
      width: 34px;
      height: 51px;
  }
  ```
  goes after the rule at `styles.ts:4419-4424`, and
  ```css
  .vntv-factrow-mark.vntv-factrow-cover[data-kind="game"][data-art="tall"] {
      width: 30px;
      height: 45px;
  }
  ```
  goes after `:5565-5568`. Find wherever `vntv-picked-thumb` is rendered (the picked row in
  `TitleSearch`) and give it `data-art` too.

### B.9 Every two-provider assumption, and what happens to it

The numbers refer to the code map in the research ("Media lookups today", §10).

| # | Place | Change |
|---|---|---|
| 1 | `media.go:28-29, 37-38` header | Rewrite: three providers (IGDB for games Steam lacks, through the admin's Twitch application), and what is remembered durably (Part C). |
| 2 | `:45-46` sources | Add `mediaSourceIGDB` and the provider constants. |
| 3 | `:100-106` budgets | Add `igdbCallsPerMinute = 120`, `igdbCallsPerSecond = 4` and `igdbConcurrent = 4`, with a comment citing IGDB's 4/s and 8-open limits. |
| 4 | `:114-117` bases | Add `igdbAPIBase` and `igdbImageBase` (and `twitchIDBase` in twitch_app.go). |
| 5 | `:124-133` image hosts | Add `images.igdb.com`. |
| 6-8 | ref pattern, parse and `String()` | §B.1. |
| 9-10 | flags and `unavailable` | Add `IGDB` and `allowsRef`. The sentences are unchanged. |
| 11 | link prefixes | Add igdb.com/games/. |
| 12 | `checked()` source | Use `ref.source()`. |
| 13 | `mediaArt` | JSON tags; the comment explains the IGDB meaning. |
| 14 | `mediaStore` limiters | Add the IGDB limiter, per-second window, hold-back and slots (§B.2), plus `now func() time.Time`. |
| 15 | `tmdbGet`/`steamGet`/`getMediaJSON` | Unchanged; `igdbPost` is new. |
| 16 | `searchMedia` | Rewritten around `providerSearch` and the merge (§B.4, §C.4). |
| 17 | `searchTMDB`/`searchSteam` | Return `([]mediaCachedResult, error)` and stop writing hints themselves; `providerSearch` does that. |
| 18 | `mediaDetailsFor` switch | By provider; the default is NotFound. |
| 19 | detail functions | Add `changing`; `igdbGame` is new. |
| 20 | `pictureAddresses` | By provider. |
| 21 | `steamLibraryURL` first pass | Only for steam refs. |
| 22 | `yearOf` comment | Add IGDB's Unix date, which has its own helper. |
| 23-25 | comments in `media_api.go`, `community_api.go`, `community.go:538`, `nowplaying.go:501`, `api.go:208-209`, `plugin.go:296` | Say "TMDB, Steam or IGDB"; ref examples gain `igdb:113360`. |
| 25a | `webapp/src/components/PopoverLive.tsx:167` comment, `webapp/lab/media.ts:3` and `:62` comments | "its IMDb, Steam or IGDB page"; the lab's stand-in is "for TMDB, Steam and IGDB", and an IGDB game's thumb and cover are portrait box art. |
| 26-29 | config, defaults, secrets, plugin.json | Part A. |
| 30 | `media_test.go` | Swaps in the fake (§D.1); ref strictness table; flags table; source table. |
| 31 | `settings_test.go` | §A.1. |
| 32 | `nowplaying_test.go` fixtures | Unchanged unless a test now fails. Add one IGDB What's on? case (§D.1). |
| 33-41 | webapp | §B.8 and §A.1. |
| 42-44 | lab | §D.2. |
| 45-46 | README and LOCKDOWN | §C.10. |

---

## Part C. Remembered titles: the durable cache

### C.1 What is remembered, where, and what is not

| Thing | Where | Why |
|---|---|---|
| Search results, per provider and normalised query, with each result's art hint | KV shards | This is exactly "the server keeps searching online for the same game/movie/show". |
| A title's details (`scheduleMedia` text plus `mediaArt` addresses), per ref | KV shards | After a restart, every plan on screen costs one details call per title today. With this, it costs none: only the image GET. |
| "No such title", per ref | KV shards, with a short life | So a deleted or adult ref does not cost a provider call on every panel. |
| Picture bytes | **Memory only (unchanged L1)** | There are hundreds of 10–200 KB images, which would be 24 MiB in KV. The plugin API has no file write or delete, and writing files directly works only on the "local" file driver (S3 servers get nothing). The main tree's Clips work adds `resolveClipsRoot`, which a later release can reuse under `<Directory>/ceo.villa.villanet/media/`. Pictures come from CDNs and do not use the providers' API budgets, and with durable art addresses a picture after a restart costs one CDN GET and no API call. Victor decides (Question 1). |
| The Twitch app access token | **Memory only** | §A.2. KV outlives an uninstall, and a token there would sit in every database backup. |

**Storage: plugin KV, 16 fixed shard documents, `mediacache_00` to `mediacache_0f`.** A shard, once
written, is **never deleted**, not even by the admin's "Forget remembered titles" or when it turns out
unreadable: both of those CAS-write an empty shard, `{"v":1,"entries":{}}`, instead (§C.8, §C.10).
Reasons, from the storage research:

- Per-entry keys would lengthen `kvKeysWithPrefix`, which walks the **whole** KV namespace on every
  poll on every node (`links.go:181-212`, called from `reconcileBadges`). At 2,000 entries that is
  about 10 more SELECTs per poll per node.
- That walk pages with `OFFSET` over keys sorted by `PKey` (verified in Mattermost v9.0.0
  `sqlstore/plugin_store.go`, `List`: `OrderBy("PKey").Limit(limit).Offset(offset)`). A key **deleted**
  ahead of the page being read moves every later key back one place, and the key on the page boundary
  is silently missed (`links.go:173-179` says so). `mediacache_` sorts before `prevstatus_`,
  `quarantine_` and `stats_month_`, which `reconcileBadges` and the airtime export walk. An insert
  only shifts keys forward, which at worst returns one twice, and the walk de-duplicates.
- Sixteen keys that are never deleted add at most one page to that walk and never make it miss
  anything.
- One big document would make every write a CAS of the whole cache on one row.

The `mediacache_` prefix collides with nothing in the sandbox or the main tree (Clips adds `clips`,
`cron_clips_sweep` and `mutex_cron_clips_sweep`).

### C.2 Layout (`server/media_cache.go`, new)

```go
const (
    kvMediaCachePrefix   = "mediacache_"   // + two hex digits, 00..0f
    mediaCacheShards     = 16
    mediaCacheVersion    = 1
    maxMediaShardBytes   = 128 << 10       // per shard as stored; 2 MiB in all
    maxMediaShardEntries = 256             // per shard; 4,096 in all
    maxMediaKeptBytes    = 16 << 10        // one entry larger than this is not kept
)

// mediaShard is one of the sixteen documents.
type mediaShard struct {
    Version int                   `json:"v"`
    Entries map[string]*mediaKept `json:"entries"`
}

// mediaKept is one remembered answer.
type mediaKept struct {
    Type    string              `json:"t"`                 // "search", "title" or "missing"
    At      int64               `json:"at"`                // fetched from the provider, unix ms
    Until   int64               `json:"until"`             // believed until, unix ms
    Used    int64               `json:"used"`              // last written or (coarsely) served, unix ms
    Gone    int64               `json:"gone,omitempty"`    // title: when the provider first said NotFound for it, unix ms (§C.4)
    Results []mediaCachedResult `json:"results,omitempty"` // search
    Info    *scheduleMedia      `json:"info,omitempty"`    // title
    Art     *mediaArt           `json:"art,omitempty"`     // title
}

// mediaCachedResult is a search result with the art its search described,
// so a remembered search still needs nothing but the pictures themselves.
// Neither Art nor SteamApp is ever sent to a browser: searchMedia strips both
// and answers []mediaResult.
type mediaCachedResult struct {
    mediaResult
    Art      mediaArt `json:"art"`
    SteamApp string   `json:"steam,omitempty"` // IGDB results only: the Steam app id IGDB links it to (§B.3, §B.4)
}

type mediaArt struct {
    Poster   string `json:"poster,omitempty"`
    Backdrop string `json:"backdrop,omitempty"`
    Capsule  string `json:"capsule,omitempty"`
    Header   string `json:"header,omitempty"`
}
```

**Entry keys.** These are never logged and **never contain typed text**, because KV keys appear in
Mattermost's own server logs on a failure:

- Search: `"s:" + provider + ":" + hex(sha256(provider + "\x00" + normalQuery))[:32]`
- Title or missing: `"t:" + ref.String()`, for example `t:igdb:113360`. A title and its missing
  marker share a key, so one replaces the other.

**Shard:** `fmt.Sprintf("%s%02x", kvMediaCachePrefix, sha256(entryKey)[0]&0x0f)`.

The **typed query is never stored in clear** in KV. It is held in clear only in memory (the L1 key),
as today.

A stored search also keeps the result titles. Those are the provider's text, not anything typed.

### C.3 Lifetimes

`Until = At + life`. Lifetimes are decided when the entry is written.

| Entry | Life | Constant |
|---|---|---|
| Search with at least one result (TMDB, Steam) | 7 days | `mediaKeepSearch` |
| Search with at least one result (IGDB) | 3 days | `mediaKeepIGDBSearch` |
| Search with no results (any provider) | 1 day | `mediaKeepEmptySearch` |
| Title, released and settled (TMDB, Steam) | 30 days | `mediaKeepTitle` |
| Title, IGDB | 7 days | `mediaKeepIGDBTitle` |
| TV series still running | 7 days | `mediaKeepRunningShow` |
| Title `changing` (unreleased, early access, coming soon, no date) | 3 days | `mediaKeepChanging` |
| Missing ("no such title") | 1 day | `mediaKeepMissing` |
| Hard limit: never served, even as a fallback, and dropped on the next write to its shard | 90 days after `At` | `mediaKeepStaleLimit` |

IGDB lives are shorter on purpose. IGDB's FAQ welcomes local caching, but the Twitch Developer
Agreement (schedule 1 §C) mentions a 24-hour cache unless authorised otherwise, and asks for cached
data to be updated and deletions honoured. Question 2 asks Victor which reading to follow.

> **As built (3.0.0).** Victor kept 3 days for an IGDB search and 7 for an IGDB title, and for IGDB
> entries those lives are the hard limit too: an IGDB entry past its own `Until` is never served,
> not even as the fallback when IGDB fails (§C.4), and it is dropped the next time its shard is
> written. Other providers keep the 90-day fallback. IGDB's cover art on disk follows its title's
> 7 days the same way. Test: `TestIGDBDataIsNeverServedPastItsDays`.

### C.4 Reading and writing: two layers

**L1** is today's `memoryCache` instances, each with today's TTL. Their values change:

- `searches` becomes `*memoryCache[[]mediaCachedResult]`, keyed `provider + "\x00" + normalQuery`.
  Because a hit now carries its art, a hit from L1 or L2 re-puts the hints. That fixes the 2.11.0
  quirk where hints were never refreshed by a cached search.
- `details` keeps `*mediaDetails`. A **nil** value now means "missing" and is put for `mediaMissTTL`
  (10 min). **Both** L1 checks in `mediaDetailsFor`, the early one before the flight
  (`media.go:764-766`, which today returns `cached, nil` directly) and the one inside the flight, turn
  a held nil into `errMediaNotFound`. Otherwise a held nil would come back as `(nil, nil)` and
  `planMedia` would panic at `info := details.info` (`media.go:1015`). The existing check after the
  flight (`media.go:797-800`) stays.

**L2** is the shards. It is read only when L1 misses, and written through after a successful provider
answer. **Neither layer is touched on the poll path.**

When L1 is filled from L2, its TTL is `min(L1 default, max(1 min, Until - now))`.

```go
// providerSearch: one provider's answer for one normalised query.
func (p *Plugin) providerSearch(ctx context.Context, cfg *configuration, provider, query, normal string) ([]mediaCachedResult, error)
```

1. L1 hit: re-put the hints and return.
2. `flights.do("search/"+provider+"/"+normal)`. This is new: two people typing the same thing now
   share one fetch. A **search** flight runs on its own context,
   `context.WithTimeout(context.Background(), mediaSearchBudget)`, not on the first caller's. So the
   person who started it typing on (the browser drops the old request) does not fail everybody
   waiting for it, and an IGDB answer that arrives after §B.4's grace still fills the cache. The
   caller waits for the flight or for its own `ctx`, whichever ends first.
   1. Check L1 again.
   2. Read L2. A fresh entry goes into L1 with its hints and is returned. A stale entry younger than
      `mediaKeepStaleLimit` is remembered as `fallback`.
   3. Ask the provider: `searchTMDB`, `searchSteam` or `searchIGDB`.
   4. Error: when there is a `fallback`, put it in L1 for `mediaMissTTL` and return it
      (**stale-if-error**). Otherwise return the error.
   5. Success: put it in L1 for `mediaSearchTTL` and put the hints. **Write L2** (best effort, §C.8)
      only when `normal` has at least `mediaKeepMinRunes` (3) runes. Two-letter prefixes are typing on
      the way to something, are asked again rarely, and would only churn the shards.
3. If `normal` is empty because the query was all punctuation, use L1 only, under the key
   `"\x01" + strings.ToLower(query)`, with no L2.

`mediaDetailsFor(ctx, cfg, ref)` follows the same order: L1, then the flight
`"details/"+ref` (L1 again, then L2, then the provider), then:

- **Provider NotFound, nothing remembered** (no entry, a `missing` entry, or a title older than
  `mediaKeepStaleLimit`): write a `missing` entry to L2, put nil in L1, and return `errMediaNotFound`.
- **Provider NotFound over a remembered title: believed only the second time, at least an hour
  later.** One NotFound is not enough to throw away a title that was fine for up to 30 days. Steam's
  `appdetails` answers `success:false` for reasons that are not deletions (regional availability under
  `cc=US`, intermittent failures), and `steamGame` turns that into NotFound (`media.go:952-954`).
  - The title has no `Gone` mark: set `Gone = now` with a conditional update (§C.8: only if the entry
    still has the same `At`; never inserts). Serve the remembered details, and put them in L1 for
    `mediaMissTTL`.
  - `Gone` is less than `mediaGoneConfirm` (1 h) ago: serve them the same way, and write nothing.
  - `Gone` is at least 1 h ago: write a `missing` entry, which replaces the title, put nil in L1, and
    return `errMediaNotFound`.

  So IGDB's deletions are still honoured, within about an hour of the provider first saying so, and a
  successful answer in between replaces the entry and with it the mark.
- **Fresh `missing` entry in L2:** `errMediaNotFound` without asking the provider.
- **Provider error** with a stale title younger than 90 days: return the stale details, and put them
  in L1 for `mediaMissTTL`.
- **Success:** L1 for `mediaDetailsTTL`, L2 with the life from §C.3.

The details flight still runs on the first caller's context, as today.

**Validating what is read.** Anything from L2 is checked as if a stranger wrote it:

- A search result must `parseMediaRef` to its own provider with a non-empty title, and a non-empty
  `SteamApp` must be 1 to 10 digits, or the whole entry counts as a miss.
- A title entry must pass `Info.checked() != nil`, with `Info.Ref` equal to the key's ref, or it
  counts as a miss.
- Art is checked where it is used: `tmdbImageURL` and `igdbImageURL` check its shape, and the host
  list checks Steam's addresses.

**Touch.** When an entry served from L2 has `Used` older than `mediaTouchEvery` (3 days), one
best-effort **conditional update** (§C.8) sets `Used = now` on that entry, and only if the shard read
inside the update still holds the entry with the same `At`. If the entry is gone (an admin pressed
Forget) or differs (another node refreshed it), the touch writes nothing. It never inserts and never
writes back the copy it served. This keeps titles in daily use out of LRU eviction, without a write per
read.

### C.5 The search request path, in full

In `handleMediaSearch`, the order and the meter are unchanged: the link is checked, then the 40 per
minute meter (which still counts cache hits; Question 7), then the flags. Then
`searchMedia(ctx, cfg, kind, text)`:

- `normal := mediaQueryKey(text)`.
- Movie: `providerSearch(tmdb)`, then strip the art and return `[]mediaResult`.
- Game: run Steam and, when `flags.IGDB`, IGDB concurrently through `providerSearch`, wait as §B.4
  says (IGDB's grace), then apply the table in §B.4, then strip the art and `SteamApp`.

> **Amended in 3.1.0 (security pass).** Before the link check comes one more: the request must carry
> `X-Requested-With: XMLHttpRequest` (`madeByScript`, `media_api.go`), or it is answered 403 "Search
> from the panel." with nothing read and no search spent. It comes after the lock's access gate
> (`lock-screen.md` §3.5). The panel sends the header on every request (`client.ts`, `baseHeaders`).
> A Markdown image in a post cannot: every reader's browser fetches it as a GET with their own
> session, Mattermost checks no CSRF token on a GET, and its image proxy is off by default. Answered
> for such an image, a search asked a provider in the reader's name, spent their 40 a minute and the
> budget the providers share, was remembered, and made every title it listed `refShownHere`, so that
> anybody could have its pictures fetched. Test: `TestAnImageInAPostCannotSearchForTitles`.
>
> The same release narrows who can have a picture fetched that the server does not hold
> (`mayFetchPictureFor`). It used to be a title `refShownHere` knows (a plan, What's on? on a live
> card, a search's results) or anything at all for somebody linked, on the grounds that somebody who
> can plan could search for any title anyway. The image in a post undoes that reasoning, since the
> person whose session comes with the GET is not the person asking. Now somebody linked passes only
> for a title `refShownToLinked` knows: a clip's tag (clips are for linked people), or their own
> What's on? that has not aired. What's on? is read from memory, and the clips from this node's copy,
> which is read again at most once a minute. The link is asked last, so a title nobody shows is
> refused without the link being read. Test:
> `TestOnlyATitleShownHereFetchesAPicture`.

### C.6 Typed slightly differently: normalisation

**Normalisation** is the cache key, for both L1 and L2:

```go
// mediaQueryKey is how a query or a title is compared: case, punctuation and
// spacing do not make a different search. Apostrophes vanish rather than
// split a word, so "Assassin's" and "Assassins" are one.
func mediaQueryKey(text string) string
// lower-case; drop ' and ’; every other rune that is not a letter or digit
// becomes a space; collapse spaces; trim.
```

- `"Vintage-Story!"`, `" vintage  story"` and `"VINTAGE STORY"` are one entry.
- No diacritic folding: `golang.org/x/text` is only an indirect dependency, and must not become a
  direct one in this patch.
- What is **sent** to the provider stays the sanitised text the person typed, as today.

**No narrowing.** An earlier draft answered longer typing ("vintage s") from a shorter search already
in memory ("vint") when that shorter answer looked complete. That rested on every provider's search
being prefix-monotone, and nothing supports it: IGDB documents its search as "results are sorted by
similarity to the given search string", and neither TMDB's search nor Steam's `storesearch` documents
how it matches. A narrowed answer would have been cached and served to everybody, so whoever typed
the shorter text first would have quietly shaped everyone's longer results. Victor asked that the
same title not be searched again, which the exact-query L1 and L2 entries already do. Narrowing, the
completeness flags, TMDB's `total_results` and Steam's 10-item rule are all left out.

### C.7 MediaLookup off, uninstall, backup

- **Off: the cache is neither read nor written, and not deleted.** Pictures 404 exactly as today, even
  when held in memory, because `media_api.go:96-102` stays. Plans keep their stored text. That is
  "existing plans' art keeps working the way it does today": today, switching off removes the art the
  moment the admin saves, so the panel looks the same for everyone. Serving remembered art while off
  would make "off" mean "off, except for what we already have", which is harder to explain. Since
  pictures are not durable, it would also mean only whatever happens to be in memory.
- **Per provider:** IGDB entries are read and served only while `flags.IGDB`, and TMDB entries only
  while `flags.Movies`. The gates sit before the cache, exactly where they are today.
- **Uninstall:** the shards stay in the database, as all plugin KV does. The help text, the admin card,
  the README and LOCKDOWN say so, and say to press "Forget remembered titles" first when removing the
  plugin for good. Forgetting empties all sixteen documents rather than deleting them (§C.1), so what
  is left is sixteen rows of about twenty bytes each, holding no titles.
- **Settings backup:** never carries the cache. The backup code makes no KV calls, and the file lists
  it under "not included" (§A.1).

### C.8 Writing, bounds, cluster and failures

```go
// keepMedia writes one entry into its shard. Best effort: a failure is
// warned about once in a while and never fails the lookup that caused it.
func (p *Plugin) keepMedia(entryKey string, kept *mediaKept)
// updateKeptMedia changes one entry in place, and only if the shard still
// holds it with this At: touch (Used) and the NotFound mark (Gone). It never
// inserts, never writes back a copy read earlier, and never prunes.
func (p *Plugin) updateKeptMedia(entryKey string, at int64, change func(*mediaKept))
// recallMedia reads one entry, fresh or stale, or false.
func (p *Plugin) recallMedia(entryKey string) (*mediaKept, bool)
```

`keepMedia` is called only with something a provider has just answered. `updateKeptMedia` is the only
other writer of an existing entry. A node that served an older L2 copy can therefore never put that
copy back: not after an admin's Forget, and not over a fresher entry another node has just written.

**Write loop** (its own small loop, not `casUpdate`, so failures are warned once per 15 min rather
than logged per request; `mediaCacheAttempts = 2`):

1. `raw := KVGet(shard)`. An AppError means warn once and give up.
2. Decode.
   - Absent: `keepMedia` starts a new `{v:1}`; `updateKeptMedia` gives up, since there is nothing to
     update.
   - `v` greater than 1: **give up without writing**. A newer build owns it, and during an upgrade a
     mixed cluster must not thrash.
   - Undecodable: `KVCompareAndSet(shard, raw, emptyShard)`, where `emptyShard` is
     `{"v":1,"entries":{}}`. It is not deleted, so the key count stays constant (§C.1). Then
     `warnMediaOnce("media-cache", "A remembered-titles document could not be read and was emptied; titles in it will be looked up again")`,
     then give up.
3. `keepMedia`: set the entry, with `Used = now`. An entry whose JSON is over `maxMediaKeptBytes` is
   not kept. `updateKeptMedia`: if the entry is absent or its `At` differs, give up without writing;
   otherwise apply `change`, and skip step 4.
4. **Prune** (`keepMedia` only).
   - Drop entries whose `At` is older than 90 days.
   - Then, while there are more than 256 entries, or the encoded shard is over 128 KiB, drop one entry,
     chosen in this order: **searches before missing markers before titles**, and within a kind the
     smallest `Used`, then the smallest `At`, then the key, so the result is deterministic. Typing
     makes many search entries, and a title's details are what Victor most wants kept, so searches
     can never push a scheduled title out while a search is left to drop.
   - Measure each entry's encoded size once rather than re-encoding the shard per drop. Verify the
     final encoding and repeat if it is still over.
5. `KVCompareAndSet(shard, rawOrNil, encoded)`. A nil old value means insert only if absent. On
   `false`, go round once more (from step 1, so `updateKeptMedia` re-checks `At` against what is
   there now).
6. Never write an empty value. Over RPC an empty slice arrives as nil, which deletes the key. An empty
   shard is the non-empty bytes `{"v":1,"entries":{}}`.

**Reads:** one `KVGet`. A `v` greater than 1 is a miss. Undecodable data is handled as in write
step 2. An AppError is a miss and a warning once. **A KV problem never fails a search, a plan or a
picture.** The provider is simply asked.

**Cluster.** Every node has its own L1, and all nodes share L2. A node writes what it fetched. Another
node finds it on its own next L1 miss, possibly a moment late from a read replica, which is harmless.
CAS retries absorb two nodes writing one shard. "Forget" clears this node's L1 and empties every
shard. Other nodes keep their L1 until it expires, which is at most 6 h for details. The admin card
says so. A provider answer that was already in flight when the admin pressed Forget is still written
when it lands. That is fresh data, not something brought back.

**Warning keys** added to `warnMediaOnce`: `"media-cache"`, `"igdb-token"`, `"igdb-query"`,
`"search-partial-steam"` and `"search-partial-igdb"`. They are fixed strings, so the map stays bounded.

### C.9 Clock

`mediaStore.now func() time.Time` (default `time.Now`) is used for every `At`, `Until`, `Used`, `Gone`
and freshness decision in `media_cache.go`, for `mediaDetails.fetchedAt`, and for IGDB's budget and
hold-back (§B.2). Tests replace it. L1 keeps
using real time, as `memoryCache` does today. No test may depend on L1 expiring.

### C.10 The admin card, and what the documents say

**Route.** `/api/v1/admin/media/cache` in `api.go`, beside the other admin routes. It needs
`isSystemAdmin`, otherwise 403 "Only a system admin can see or forget remembered titles."

- `GET` reads the 16 shards, only when asked:
  ```json
  {"titles": 12, "searches": 40, "missing": 2, "bytes": 81234, "limitBytes": 2097152,
   "byProvider": {"tmdb": 20, "steam": 25, "igdb": 9}, "oldest": 1759400000000}
  ```
  `bytes` is the sum of the stored shard sizes. `oldest` is the smallest `At`, or 0 when empty.
  Search entries count under the provider in their key, and titles under their ref's provider.
- `DELETE` **empties** each of the 16 shard keys that exists: read it, then
  `KVCompareAndSet(shard, raw, {"v":1,"entries":{}})`, going round once more on `false`. It never runs
  `KVDelete` and **never `KVDeleteAll`**, which would wipe airtime. Deleting would shift the
  OFFSET-paged key walk (§C.1). A shard that does not exist is left absent, so Forget never creates
  rows. A shard of any version, or an undecodable one, is emptied all the same, because the admin
  asked. Then it clears this node's `searches`, `details`, `hints` and `pictures`. It answers
  `{"ok": true, "forgotten": <entries before>}`. If a shard could not be emptied after two tries, or a
  KV call failed, it answers 500 "Some remembered titles could not be forgotten just now. Try again."
  and still clears this node's memory.
- Any other method: 405.
- This route works whether or not MediaLookup is on. It makes no outside request.

**Setting.** A custom setting at the end of the "media" section:

```json
{"key": "MediaCache", "display_name": "Remembered titles", "type": "custom",
 "help_text": "What VillaNET has looked up, kept so the same search or title is not looked up again for days."}
```

It has no configuration field. The settings test already allows custom settings that edit nothing.
Register it in `webapp/src/index.tsx` as `registerAdminConsoleCustomSetting('mediacache', AdminMediaCache, {showTitle: true})`.

**`webapp/src/components/AdminMediaCache.tsx`** (new; follow `AdminAirtime` and `AdminStills`):

- It loads `GET` on mount and shows one line:
  "Remembering 12 titles and 40 searches (79 KB of 2 MB), the oldest from 3 Sep."
  When empty: "Nothing remembered yet."
- Under that, in the quiet style: "They are a cache: forgetting them only means the next search asks
  TMDB, Steam or IGDB again. They stay in the Mattermost database if the plugin is removed, so forget
  them first if you are removing it for good (that leaves sixteen empty entries behind). Other servers
  in a cluster keep what they hold in memory for up to six hours."
- A **Forget remembered titles** pill becomes **Forget them all?** on the first press. A second press
  within 5 s sends `DELETE`, then shows "Forgot N." and reloads the counts. Errors show in
  `vntv-notice-error`.
- `client.ts` gains `fetchMediaCache()` and `forgetMediaCache()`.

**plugin.json, MediaLookup help text.** Replace the provider sentence and add the cache:

> …This makes the Mattermost SERVER contact outside services: The Movie Database (TMDB,
> api.themoviedb.org and image.tmdb.org) for movies and shows, the Steam store
> (store.steampowered.com and its steamstatic.com image servers) for games, and, when a Twitch
> application is set up in the Twitch section, IGDB (api.igdb.com and images.igdb.com, signed in
> through id.twitch.tv) for games that are not on Steam. It does so only when somebody searches
> while planning, or saves a plan with a title picked, and to fetch the pictures for those titles.
> Browsers never contact any of them: the art is relayed through the plugin. What it looks up (the
> titles, short descriptions and where their pictures are, never the pictures themselves) is
> remembered in the plugin's store in the Mattermost database for days or weeks, so the same title
> is not looked up again; see Remembered titles below. Off by default: while it is off the plugin
> makes no outside requests at all beyond Broadcast Box and Mumble, and switching it off again
> restores exactly that.

**README.md**

- Settings table (`:211-212`): the lookup row says "TMDB, Steam and (with a Twitch application) IGDB".
  Add two rows:
  `| Twitch application Client ID | *(blank)* | With the secret, lets game lookups also search IGDB for games that are not on Steam. See [Movies and games](#movies-and-games-optional). |`
  and
  `| Twitch application Client Secret | *(blank)* | Hidden once saved; left out of settings backups unless you include the passwords. |`
- "Movies and games" (`:626-656`):
  - The opening says games come from Steam "and, when an admin has set up a Twitch application, from
    **IGDB**, for games Steam does not sell (Hytale, Vintage Story)".
  - The thumbnails sentence becomes "portrait posters and box art, Steam's wide capsules".
  - The profile link becomes "its **IMDb** page, **Steam** store page or **IGDB** page".
  - New paragraph, **IGDB**: how to register the application (the steps in §A.1, with
    `http://localhost` as the redirect URL); that Steam stays first, IGDB fills in, and a game Steam
    lists for the same search is left to Steam, while games Steam no longer sells (delisted ones)
    come from IGDB; that IGDB results say "IGDB" in the list and the profile; and that the access
    token is kept in memory only.
  - New paragraph, **Remembered titles**: what is kept and for how long (the §C.3 table in prose);
    that pictures and tokens are not kept; that it is bounded at 2 MB; that while lookups are off
    nothing is read or added; the admin card; and that it survives uninstall.
- `:681` ("only outbound connection…"): "TMDB, Steam and IGDB".
- `:326` (the popover's profile link, "its IMDb or Steam page"), `:1361-1366` (Live now's title
  row: "a poster, or Steam's wide capsule for a game" becomes "a poster or box art, or Steam's wide
  capsule", and "IMDb or Steam page" becomes "IMDb, Steam or IGDB page"), `:608` and `:2134-2135`
  ("reaches TMDB or Steam", "call TMDB or Steam") all name IGDB too.
- `:2094`: no change in substance; the exception stays the one optional feature.
- Security posture (`:2108-2119`):
  - `:2110` is reworded, because IGDB receives the text in a POST body: "Somebody's search text goes to
    TMDB or Steam as a query parameter, or to IGDB in the body of the query, and nothing else of
    theirs does." At `:2113` (and `:1369`), "either provider" becomes "any of them".
  - The budget becomes "(Steam 30, TMDB 120 and IGDB 120 calls a minute, IGDB at most 4 a second and
    4 at once)".
  - Add: "The Twitch client secret and the access token it earns are never logged, never sent to a
    browser, and the token is never written to the database; the secret, like every plugin setting,
    is kept by Mattermost in its configuration as plain text."
  - Add: "Remembered searches are filed under a hash rather than the words, so they do not appear in
    Mattermost's logs; they are not tied to who searched." It does **not** claim more. The hash is an
    unsalted SHA-256 of the normalised query, which a list of game titles reverses, and a remembered
    search's results show what it was for anyway. The hash exists to keep typed text out of
    Mattermost's own log lines, which name KV keys on a failure.
- Optional, since the paragraph is being edited anyway: the per-user metering line (`:2140-2143`)
  gains "title searches 40 per minute, title pictures 120 per minute (only when not already held)".
- The KV section (`:1283-1288`) gains one sentence: the remembered-titles documents
  (`mediacache_00` to `mediacache_0f`) are a cache, also survive removal, and are cleared from the
  admin card.

**LOCKDOWN.md**, "What the plugin touches", last paragraph:

> Off the LAN it calls the Mumble server, when voice or the chat bridge is on, and, **only if an
> admin switches on "Look up movies, shows and games", which is off by default**, TMDB
> (`api.themoviedb.org`, `image.tmdb.org`), the Steam store (`store.steampowered.com` and its
> `steamstatic.com` image hosts) and, if a Twitch application is also set up, IGDB (`api.igdb.com`,
> `images.igdb.com`) with its sign-in at `id.twitch.tv`. Those lookups are made by the Mattermost
> server, never by a browser, and only when somebody searches while planning a stream, saves a plan
> with a title picked, or a panel shows that title's art. With the setting off, none of them happens.
> What was looked up is remembered in the Mattermost database (plugin key-value store, at most
> about 2 MB, no pictures, no credentials); it stays there if the plugin is removed unless an admin
> forgets it first from the settings page.

---

## Part D. Tests, lab, build plan

### D.1 Go tests

**The fake** (`media_test.go:56-111`). `newMediaFake` also swaps `igdbAPIBase` to `fake.URL+"/v4"`,
`igdbImageBase` to `fake.URL+"/igdb/image/upload"` and `twitchIDBase` to `fake.URL`, and restores them.
`mediaImageHosts` is already replaced wholesale with the fake host. `seenRequest` gains `method`,
`clientID` (the `Client-ID` header), `contentType` and `body` (read in full for POSTs). **A new base
URL that is not swapped here would reach the real network. Grep for `https://` in non-test server
files after building.**

New helpers:

- `fake.igdbToken(token string, expiresIn int)` routes `/oauth2/token`.
- `fake.igdbGames(handler)` routes `/v4/games`. It parses the body, so a test can answer by
  `search "…"` or `where id = N`.
- `twitchConfig()` gives `MediaLookup: true` with test credentials `testTwitchClientID` and
  `testTwitchClientSecret`. The secret is a distinctive string for the never-logged test.
- `restart(p)` builds a fresh Plugin on the same KV map and the same `*kvFaults`, for "after a
  restart" tests. Its memory is empty and its KV is the same.
- `igdbGrace` is set to a few milliseconds in the tests about the grace. That is safe because the
  IGDB handler in those tests blocks on a channel, so it cannot answer inside the grace however slow
  the machine is.

**The KV harness has to be extended first** (`stats_test.go:96-230`). As it stands, the cache tests
would pass whatever the code does:

- The `KVCompareAndSet` mock ignores `faults.writeFails`, never increments `faults.writes`, and always
  returns a nil AppError (`stats_test.go:185-207`). `KVCompareAndDelete` ignores faults too. Every
  shard write goes through `KVCompareAndSet`.
- `communityPlugin` discards the `*kvFaults` (`community_test.go:29`), and `mediaPlugin` is built on
  it (`media_test.go:173-189`), so no media test can reach the faults.

Changes:

1. `KVCompareAndSet` and `KVCompareAndDelete` honour `writeFails[key]`: they return `false` with an
   injected AppError, as `KVSetWithOptions` does. A successful one increments `writes[key]`. Before
   this, nothing counted CAS writes and no test failed a CAS key: the only `failWrite` callers are on
   `kvStillsBlocked` and `kvWatchTotals`, written through `KVSetWithOptions` and `KVSet`, and no test
   counts `writes` on `kvSchedule`, `kvNowPlaying` or the birthday key. Run the whole suite anyway.
2. Add an `onWrite func(key string)` hook beside `onRead`, called under the lock by `KVSet`,
   `KVSetWithOptions`, `KVCompareAndSet`, `KVCompareAndDelete` and `KVDelete`.
3. Add `communityPluginWithFaults` and `mediaPluginWithFaults(t, cfg) (*Plugin, map[string][]byte, *kvFaults)`.
   `communityPlugin` and `mediaPlugin` become thin wrappers, so existing callers do not change.
4. **Sanity assertion**, in the cache tests that assert "nothing written": the same test first runs
   one ordinary search with lookups on and asserts that `writes` rose on a `mediacache_` key. Only
   then does it assert zero with lookups off. That way a harness that cannot see writes fails loudly
   instead of passing.

**Fixtures**, from the research (OBSERVED on igdb.com; the `game_type` text is a stand-in, so also
test the id-only shape):

```json
[{"id":113360,"name":"Hytale","first_release_date":1768262400,
  "cover":{"id":1,"image_id":"cobc4t"},"game_type":{"id":0,"type":"Main Game"},
  "websites":[{"id":1,"type":1,"url":"https://hytale.com"}]}]
```
```json
[{"id":69547,"name":"Vintage Story","slug":"vintage-story","url":"https://www.igdb.com/games/vintage-story",
  "first_release_date":1534377600,"summary":"…","cover":{"id":2,"image_id":"co2j6g"},
  "genres":[{"id":32,"name":"Indie"}],"game_status":{"id":4,"status":"Early Access"},
  "involved_companies":[{"id":3,"developer":true,"company":{"id":4,"name":"Anego Studios"}}],
  "websites":[{"id":5,"type":15,"url":"https://tyronx.itch.io/vintage-story"}]}]
```

Steam `storesearch` for "hytale" and for "vintage story" answers `{"total":0,"items":[]}` (OBSERVED).

**IGDB** (`igdb_test.go`)

| Test | Asserts |
|---|---|
| `TestHytaleAndVintageStoryComeFromIGDB` | A game search "hytale" returns `igdb:113360` "Hytale" "2026" "Game". One token POST, one `/v4/games` POST with headers `Client-ID`, `Authorization: Bearer …` and `Content-Type: text/plain`, and the body exactly as in §B.3. |
| `TestGamesComeFromSteamFirstAndIGDBFillsIn` | The merge table in §B.4, including the exact-match promotion and the four worked cases. |
| `TestIGDBGamesSteamListedAreLeftToSteam` | Only for games **Steam returned in this answer**: an IGDB row whose Steam website URL, external-games Steam URL, or source-1 uid names a Steam result's app id is dropped, and so is one with the same title as a Steam result. `SteamApp` is parsed from each of the three shapes. |
| `TestIGDBKeepsGamesSteamNoLongerSells` | An IGDB row with a Steam link (`store.steampowered.com/app/1293830/`, Forza Horizon 4) whose app is **missing** from Steam's answer is kept, after Steam's rows, with an `igdb:` ref. Steam answering "forza horizon 4" with only add-ons for another game (as OBSERVED) still lists Forza Horizon 4 from IGDB. After a restart, the remembered IGDB search still carries `SteamApp`, and the merge gives the same answer. |
| `TestEditionsDLCAndBundlesAreLeftOut` | A table over type text and id: DLC, bundle, mod and pack are dropped; main game, standalone expansion, remake, remaster, expanded game and port are kept, **including a remake, remaster or port with `parent_game` set**; a row with no game type is kept unless `parent_game` is set. The request body carries the `game_type` group exactly as in §B.3. |
| `TestWhatSomebodyTypesCannotBreakOutOfTheIGDBQuery` | A table: `he said "x"; fields *;\` arrives as `search "he said x fields";`; `“zelda”` and `‘zelda’` arrive as `search "zelda";`; `;fields *;` arrives as `search "fields";`; `Baldur's Gate 3` arrives as `search "Baldurs Gate 3";`; `C++: Origins!` keeps `C++: Origins!`. A query of only quotes or only `;*` makes no IGDB request and remembers nothing. |
| `TestWithoutTwitchCredentialsGamesComeFromSteamOnly` | Zero `/oauth2/*` and zero `/v4/*` requests; `/me` says `igdb:false`; the results are Steam's alone. |
| `TestNothingIsLookedUpWhenTheFeatureIsOff` (extend) | Credentials set and MediaLookup off: zero requests to any route, including the token. |
| `TestOneProviderFailingStillAnswersWithTheOther` | Steam 500 with IGDB ok gives 200 with IGDB results and one LogWarn naming Steam; the reverse too; both failing gives 502. **Asked twice, told once:** a second partial failure within 15 min logs nothing more. IGDB refused (`errTwitchRefused`), held back, or busy logs nothing at all. |
| `TestASlowIGDBDoesNotHoldSteamsAnswer` | The IGDB handler blocks on `release`; Steam answers. The search returns Steam's rows alone after the (shortened) grace, with HTTP 200 and no warning. Close `release`, then call `providerSearch` for IGDB with the same query: it joins or finds the finished flight, `/v4/games` was asked **once**, and the next merged answer includes IGDB's row. |
| `TestAFailingIGDBIsLeftAloneForAMoment` | IGDB answers 503: a second search within 30 s (fake clock) makes no `/v4/games` request and answers with Steam's rows; at +31 s, IGDB is asked again. Same for a 429. |
| `TestAnIGDBTitleIsPlannedWithItsDetails` | Planning `igdb:69547` stores Source "IGDB", the igdb.com link, Year "2018", Facts "Indie · Anego Studios", and the overview. |
| `TestIGDBPicturesComeFromItsImageHost` | The thumb is served from the search hint, from `/igdb/image/upload/t_cover_small/co2j6g.jpg`, with no details call. The cover uses `t_cover_big`. A backdrop with no artwork or screenshot falls back to `t_720p` of the cover. |
| `TestAnIGDB401FetchesANewTokenOnceAndRetries` | 401 then 200: two token POSTs, two games POSTs, success. |
| `TestIGDBRefusingEveryTokenDoesNotChurnTokens` | IGDB answers 401 to everything. First search: two token POSTs, two games POSTs, `errTwitchRefused`, one `igdb-token` warning. A second search at once: **no** request of any kind (2 token POSTs in total, not 4). At +6 min (fake clock): one games POST with the held token, still 401, and **no** new token POST, because that token was already refused once. New credentials end the hold-back at once. A 403 holds back without any token POST after it. |
| `TestIGDBIsKeptToItsBudget` | Fake clock through `store.now`. Four calls at t0 go; the fifth at t0+999 ms is refused as busy without a request; at t0+1 s one more goes. The 401 retry fits in the window (three calls, then a 401 retry, is four). Per minute: advance the clock 250 ms per call, so the window never binds. The bucket (120 burst, refilling 2 a second) then first refuses the **240th** call, at +59.75 s, as busy and without a request. No sleeps. |
| `TestIGDBRefsAreStrict` | `igdb:113360` and `igdb:0069547` (normalised to `igdb:69547`) are accepted. `igdb:`, `igdb:movie:1`, `IGDB:1` and `igdb:12345678901` are refused. Add these to the existing table at `:518-558`. |
| `TestStoredTitlesAreCheckedOnTheWayOut` (extend) | An igdb ref with source "Steam" is dropped. An igdb ref with an igdb.com link and source "IGDB" is kept. |
| `TestMeSaysWhichTitlesCanBeLookedUp` (extend) | The `igdb` flag across MediaLookup and credentials. |
| `TestWhatsOnCanPickAnIGDBGame` | What's on? with `igdb:113360` under the game icon stores IGDB media, looked up once. |

**Remembered titles** (`media_cache_test.go`)

| Test | Asserts |
|---|---|
| `TestARememberedSearchNeedsNoProviderAfterARestart` | Search "heat", `restart`, search "heat" again: zero new requests. Thumbnails after the restart need only the image GET, not details. |
| `TestARememberedTitleNeedsOnlyItsPicturesAfterARestart` | Plan Elden Ring, `restart`, ask for its cover: no `/api/appdetails` call, one image GET. |
| `TestRememberedEntriesGoStale` | A clock table: a TMDB search is fresh at +6d23h and asked again at +7d1h; an IGDB search uses 3d; a `changing` title 3d; a running show 7d; a missing marker 1d; an empty search 1d. |
| `TestAStaleEntryIsServedWhenTheProviderFails` | Past `Until` with the provider answering 500: the stale answer, no 502. Past 90 days: 502. |
| `TestATitleTheProviderNoLongerHasIsForgotten` | Remembered, stale, then 404: the title is still served and gets a `Gone` mark. At +30 min, 404 again: still served, no write. At +1h05m, 404: the entry becomes `missing`, the next request in the day asks nobody, and a plan saved meanwhile gets no media. A 200 between the two 404s replaces the entry and clears the mark. A title never remembered becomes `missing` at the first 404. |
| `TestAMissingTitleIsNotAskedForTwice` | A NotFound ref planned twice within 10 min: nil media both times, no panic, one provider request (the held nil goes through both L1 checks). |
| `TestOldArtAddressesAreRefreshed` | Remembered details at +2d with every art address 404: one fresh details call, then the new address is fetched. At +1h: no refresh. A TMDB title with no poster and no backdrop asked for its backdrop at +2d: **no** details call. |
| `TestTheCacheStaysWithinItsBounds` | Fill one shard (use keys known to land in shard 00) past 256 entries and past 128 KiB: searches go first, least recently used first; titles are dropped only when no search is left; the encoded shard is at most 128 KiB; an oversize entry is not kept. A two-letter search is never written to KV. |
| `TestATouchNeverBringsAnythingBack` | (a) Serve a title from L2 at +4d (so it is due a touch) on a node whose L2 read happened before an admin's DELETE: the shard stays empty. (b) Another node rewrote the entry with a newer `At`: the touch leaves that entry exactly as it is. (c) Same `At`: only `Used` changes. |
| `TestNothingTypedIsStoredInTheClear` | After searches for "vintage story" and "heat", no KV key or value contains "vintage story" or "heat" as a query. Titles are allowed in result entries, so assert on the search entries' keys and on the absence of the query text outside `results[].title`. Every KV key written is `mediacache_0x`. |
| `TestANewerOrUnreadableShardIsNotWrittenOver` | `{"v":2}` is read as a miss and the bytes stay identical after a search. Garbage is replaced by the empty shard `{"v":1,"entries":{}}` (the key still exists), warned once (two occurrences give one warning), and the search still answers. |
| `TestTheCacheIsNeitherReadNorWrittenWhileLookupsAreOff` | With `mediaPluginWithFaults`: first, with lookups on, one search raises `writes` on a `mediacache_` key (the sanity assertion, §D.1 harness). Then seed a shard and switch off: no `KVGet` and no write of any kind on any `mediacache_` key (count them through `onRead` and `onWrite`). |
| `TestKVTroubleNeverFailsALookup` | Read faults and write faults (now honoured by CAS) on every shard: searches, plans and pictures all still work from the providers, and the KV warning is logged once. |
| `TestNormalisedQueriesShareOneEntry` | "Vintage-Story!" then "vintage  story" give one provider request. "vintage s" after "vintage story" **is** asked (no narrowing). |
| `TestRememberedTitlesAreForAdminsToSeeAndForget` | A non-admin gets 403 on GET and DELETE. As admin, GET counts by type and provider; DELETE leaves every existing shard key **present** and holding `{"v":1,"entries":{}}`, creates no key that did not exist, makes no `KVDelete` or `KVDeleteAll` call, clears L1 (the next search goes to the provider), and answers `forgotten`. A write fault on one shard gives 500. |
| `TestSearchesAndDetailsShareOneFetch` | Two concurrent searches for one new query make one provider request. Use the gate pattern from §A.3, not sleeps. |

**Existing tests to re-run and adjust only where caching legitimately changes a count:**
`TestSearchesAreRememberedForAWhile`, `TestPicturesAreRelayedFromTheProviders`,
`TestAProviderSayingSlowDownIsNotRememberedAsMissing`, `TestTheServerKeepsToABudgetWithEachProvider`,
`TestManyPanelsAskingForOnePictureMakeOneFetch`, `TestChangingAPlanOnlyLooksUpATitleThatChanged`,
`TestTheCachesStayWithinTheirBounds` (the L1 value types changed), and
`TestAFailedSearchSaysSoWithoutGivingAnythingAway`. A 429 must still never become `missing`. Every
test that builds a media plugin gets the harness's `KVCompareAndSet` and `KVCompareAndDelete`, which
`kvBackedPluginWithFaults` mocks, honouring faults once the harness change above is made. The admin-route
test needs a system admin: `mediaPlugin` mocks `HasPermissionTo(anything, manage_system)` as `false`,
and testify answers with the **first** matching expectation, so a later, more specific `On` would never
be used. `mediaPluginWithFaults` therefore takes the admin user ids as an argument and registers
their expectation before the catch-all.

**Settings**: §A.1 lists, plus `TestTwitchCredentialsAreNormalised` (trim; the placeholder secret
becomes empty; an ID with spaces around it is trimmed).

### D.2 Lab and webapp states

- `lab/media.ts`:
  - Add IGDB entries **Hytale** (`igdb:113360`, 2026, "RPG · Simulator · Hypixel Studios" — the facts
    line as the server writes it once `igdbGenreName` has shortened "Role-playing (RPG)" — source
    IGDB, link `https://www.igdb.com/games/hytale`) and **Vintage Story** (`igdb:69547`, 2018,
    "Indie · Anego Studios", link `…/vintage-story`). Add at least one more IGDB-only game so that one
    query mixes Steam and IGDB. Lab-only ids are fine for that one.
  - `searchCatalogue('game')` follows the server: Steam entries first, then IGDB through the same
    merge rule (copy the small function; it is lab code; the lab's catalogue has no delisted games,
    so the title comparison is all of step 1 it needs).
  - Fix the comments at `:3` and `:62` (§B.9 row 25a).
  - `artOf` draws IGDB art tall: thumb 90×128, cover 264×352, backdrop 889×500.
- `lab/mocks/client.ts`:
  - `lookupAllows`: `steam:` needs `media.games`, `igdb:` needs `media.games && media.igdb`, and
    everything else needs `media.movies`.
  - Kind from a ref: `steam:` or `igdb:` means game (`:647`).
  - The movie-unavailable sentence becomes the server's: "Movies and shows cannot be looked up on
    this server yet."
  - Mock `fetchMediaCache` and `forgetMediaCache`.
- `lab/world.ts`:
  - `me.media: {movies: true, games: true, igdb: true}`.
  - Add one seeded plan: `planned('Hytale first look', 'Erin', 'u-erin', 2 * HOUR, 'game', 'igdb:113360')`.
- `lab/index.tsx`:
  - "lookups off" switches all three flags.
  - A new chip **no IGDB** (`me.media.igdb` false): game search shows Steam only with the heading
    "Games on Steam", and the Hytale plan's art is not relayed (it shows the stand-in).
  - Render `<AdminMediaCache/>` in the admin area beside the others.

**States a person must be able to see in the lab:**

1. "hy" as a game: Hytale with a tall thumb and the meta "Game · 2026 · IGDB".
2. A mixed Steam and IGDB list: wide and tall thumbs together, aligned.
3. A picked IGDB title: tall picked thumb.
4. The Hytale plan: Up next card backdrop, a profile with a portrait cover, "IGDB ↗", and "Data from
   IGDB".
5. PopoverLive rows with a tall factrow cover.
6. "no IGDB".
7. "lookups off".
8. The admin card: counts, empty, the forget confirmation, and an error.

### D.3 Build plan: Part A first, then three tracks in parallel

**Step 0, Part A (one agent, in the igdb sandbox).** `twitch_app.go` and `twitch_app_test.go`;
configuration, defaults, `settings.go` (`settingsSecrets` and both `settingsNotIncluded` lines) and their
tests; plugin.json's Twitch section; and the `AdminSettingsBackup.tsx` texts (§A.1). Then
`TZ=UTC make check` and `go test -race -count=3 ./server/...`. Then take the checkpoint (§D.4).
Part A goes first and alone, so that the checkpoint holds Part A and nothing half-written.

After the checkpoint, three tracks run at once. None touches another's files or sandbox.

**Phase 1 server builder** (igdb sandbox) owns `server/**`, `plugin.json`, `README.md` and
`LOCKDOWN.md`. Order:

1. The KV harness extension (§D.1), with the full suite still passing, before any cache test is
   written.
2. §B.1, the ref and flag plumbing, with the tests updated.
3. `igdb.go` with search, details, pictures, the budget window and the hold-back, and `igdb_test.go`.
4. `media_cache.go`: the shard layer, `providerSearch` and the details layering, the conditional
   updates, and the admin route. Then `media_cache_test.go`.
5. Help texts, README and LOCKDOWN (§C.10).
6. `TZ=UTC make check`, `go test -race -count=3 ./server/...`, and a grep that every `https://`
   literal in non-test server code is swapped by the fake or is a link prefix.

**Phase 1 webapp builder** (igdb sandbox) owns `webapp/src/**` and `webapp/lab/**`. Order:

1. `mediaRefs.ts`, then the `types.ts` and `client.ts` comments and calls.
2. TitleSearch, ScheduleControls, WhatsOn, MediaProfile and PopoverLive.
3. `styles.ts`, the two rules, then `check-styles.py`.
4. `AdminMediaCache.tsx` and its registration.
5. The lab (§D.2).
6. `npx tsc`, `python3 build/check-react.py` and `check-styles.py`, then build the lab with the command
   at the top of this document and look at every state in §D.2 (see the "Webapp preview harness" memory).

**Phase 2** (twitch-live sandbox, §D.4) is built as §E.8 says.

**Contract between the Phase 1 builders**, and nothing else:

- `/me.media.igdb`.
- Refs `igdb:<id>`.
- `MediaInfo.source === 'IGDB'` with links under `https://www.igdb.com/games/`.
- `mediaResult` unchanged.
- The admin route's JSON (§C.10).
- Image sizes: IGDB thumb and cover are portrait, backdrop is wide.

**Shared lists.** Phase 1 and Phase 2 both add to a few lists: the routes in `api.go`, the fields in
`plugin.go`, `sectionDefaults`, `client.ts`, `types.ts`, the lab's world and chips. Each phase keeps its
additions in one contiguous block per list, headed by a one-line comment naming the feature, so that
patching one diff on top of the other rejects as few hunks as possible.

**Integration of Phase 1** (one agent, after both Phase 1 builders finish):

1. `TZ=UTC make check` in the igdb sandbox.
2. Build the lab and walk the §D.2 list.
3. Diff the sandbox against the baseline. Confirm that only the files named in this contract changed,
   the version is still 2.11.0, and no `node_modules` path appears.
4. Write a short note of anything UNVERIFIED that needs Victor's real Twitch application: the
   `game_type.type` and `game_status.status` texts (the ids are documented), that the grouped
   `game_type` filter is accepted together with `search`, a wrong-secret status from id.twitch.tv,
   that `http://localhost` is accepted as the redirect URL, and Hytale's and Vintage Story's live
   records.

### D.4 Checkpoint and the two diffs

**The checkpoint**, right after step 0 passes:

- `cp -a ".../Broadcast Box Plugin.igdb-sandbox" ".../Broadcast Box Plugin.twitch-partA"`: a frozen
  reference. Nothing is ever built or edited in it.
- `cp -a ".../Broadcast Box Plugin.igdb-sandbox" ".../Broadcast Box Plugin.twitch-live-sandbox"`: where
  Phase 2 is built. `cp -a` keeps `webapp/node_modules` a symlink into the main tree. Every rule at the
  top of this document applies there too: never `npm install` or `make lab`, and build the lab with the
  same command, run from that sandbox.

**The diffs:**

- Phase 1: the igdb sandbox against `.baseline-2.11.0`. It includes Part A.
- Phase 2: the twitch-live sandbox against `.twitch-partA`. It is Part E alone, reviewed on its own.

**Landing** onto the main tree: Phase 1 first, then Phase 2. Then come three wording changes that
neither phase can make alone, because Phase 1 rewrites the same paragraphs (§E.8):

- the MediaLookup help sentence;
- README `:2094`;
- LOCKDOWN's "What the plugin touches" paragraph.

Rejected hunks in the shared lists are resolved by hand, and then `TZ=UTC make check` is run.
Victor's request, Twitch live, therefore waits only on Part A, not on IGDB or the cache.

---

## Part E. Twitch live (Phase 2)

> **SUPERSEDED - do not build this part.** Victor answered the Twitch-live questions on 2026-10-02 differently
> from the defaults below: Connect with Twitch (OAuth), live status AND playback inside VillaNET, and Twitch
> time counting toward airtime and medals. The build contract for Twitch live is `design/twitch-live.md`,
> which reuses Part A of this document (the Twitch section, the two credentials and `twitch_app.go`)
> unchanged and replaces Part E entirely, including its questions.


Victor's request: people link their Twitch accounts, and when they go live on Twitch, they show as
live in VillaNET. It uses the same Twitch application, the same `twitchApp` token component, and its
own switch.

### E.1 What it does

- A signed-in member presses **Link Twitch** in the panel footer. Twitch asks them to approve
  "VillaNET" with no permissions. Afterwards VillaNET knows their Twitch user id and login, and nothing
  else. No user token is kept.
- While the admin's **Show people live on Twitch** is on, the server asks Twitch's API once a minute
  which linked members are live.
- A member live on Twitch gets a card in **Live now**, after the people streaming on Broadcast Box and
  before the 24/7 channels. The card shows their name, the Twitch stream title, the category, the
  viewer count, how long they have been live, and a relayed preview picture. Its button is
  **Watch on Twitch ↗**, which opens `https://www.twitch.tv/<login>` in a new tab. There is no player
  inside the panel (Question 9).
- They wear the live badge (the custom status) like a Broadcast Box streamer, while the badge setting
  is on.
- Somebody live on both shows only their Broadcast Box card.

### E.2 Settings (the Twitch section)

- `EnableTwitchLive` (bool, default **false**), display name "Show people live on Twitch". Help text:
  > Lets people link their Twitch account from the panel, and shows a card in Live now while they are
  > live on Twitch. Uses the Twitch application above: add the OAuth Redirect URL shown below to it
  > (keep http://localhost too, or not; IGDB does not use either). The Mattermost SERVER asks Twitch (api.twitch.tv, with tokens from id.twitch.tv) once a
  > minute which linked people are live, only while this is on and somebody has linked, and fetches
  > their Twitch preview pictures (static-cdn.jtvnw.net) to relay them. A browser contacts Twitch only
  > when its person presses Link Twitch or Watch on Twitch. VillaNET keeps each person's Twitch user
  > id and name, never a Twitch password or token. Off by default.
- `TwitchSetup` (custom). It shows the exact OAuth Redirect URL to **add** to the application (an
  application can hold several; Phase 1 registered `http://localhost`), built by the server from
  `ServiceSettings.SiteURL` + `/plugins/ceo.villa.villanet/api/v1/twitch/callback`, with a Copy button.
  It shows a warning, and linking stays off, when the Site URL is empty or is not `https://` (the one
  exception is an `http://localhost` or `http://127.0.0.1` Site URL, for a test server). Twitch is
  reported to accept plain-http redirect URLs only for localhost (UNVERIFIED), and an authorization code
  should not cross a network in clear anyway. It also shows how many people have linked, with
  **Unlink everybody** (two-step). Admin route: `GET`/`DELETE /api/v1/admin/twitch`.
- Defaults: add `"EnableTwitchLive": false` to `sectionDefaults`. No new secret.
- Settings backup: `settingsNotIncluded` gains "Which person has linked which Twitch account". Links
  are per-person data, like stream links. The "Not in the file" sentence in `AdminSettingsBackup.tsx`
  (`:70-72`, already extended in Part A) gains ", which Twitch account each person linked" before "and
  who has linked which stream key".

### E.3 Linking

**Verified in Mattermost v9.0.0 source** (`server/channels/app/plugin_requests.go:128-212`): a plugin
GET request carrying the session cookie gets `Mattermost-User-Id`, and no CSRF check applies to GET.
The session cookie is set with no SameSite attribute unless embedded (`app/login.go:285-331`), so
browsers treat it as Lax and send it on a top-level navigation back from id.twitch.tv. **But** the
Mattermost desktop app opens id.twitch.tv in the system browser, which may not be signed in to
Mattermost. The flow therefore has a session path and a code path:

1. `POST /api/v1/twitch/link/start` (signed in; `twitchLinkLimiter`, 5 per 15 min per user, like
   linking a stream).
   - Refuse unless `EnableTwitchLive`, credentials are set and the Site URL passes the `https` rule
     in §E.2.
   - Make `state` from 32 random bytes in hex. Write
     `KVSetWithExpiry("twitch_state_"+state, {"userId","at"}, 600)`.
   - Answer `{authorizeUrl}`:
     `https://id.twitch.tv/oauth2/authorize?response_type=code&client_id=…&redirect_uri=<callback>&scope=&state=…&force_verify=true`.
   - **The webapp never calls `window.open` after an `await`.** Safari, and Firefox after a delay,
     block a window opened outside the click that asked for it. The first press of **Link Twitch**
     sends this POST, and the pill then becomes a real link, `<a href={authorizeUrl} target="_blank"
     rel="noopener noreferrer">Continue on Twitch ↗</a>`, with the line "Twitch opens in a new tab;
     come back here when it says done". Pressing a real link is always allowed. In the desktop app it
     opens the system browser, which is exactly the case the code path below is for. The link is
     dropped after 10 minutes, when the state expires, and the pill returns. A GET route that
     redirects to Twitch was considered and rejected: in the desktop app it would open in a browser
     without the Mattermost session and could not tell who is linking.
   - `scope=` is empty because no permission is needed to read one's own user record. Twitch's docs
     mark scope as required, and whether an empty one is accepted is UNVERIFIED. If Twitch refuses it
     in the live check, use `scope=openid`.
2. `GET /api/v1/twitch/callback?code&state` (or `?error&state`). This is **served before the
   `Mattermost-User-Id` check in `ServeHTTP`**, like `/static/`.
   - The state must exist. Take it with `KVCompareAndDelete`, so it is single-use. A missing, used or
     expired state gets a 400 page with **no outside call**, so an unauthenticated caller can never
     make the server contact Twitch.
   - `error=access_denied` gives a page saying nothing was linked.
   - Exchange the code: `POST id.twitch.tv/oauth2/token` with `grant_type=authorization_code`, `code`,
     the same `redirect_uri`, and the credentials in a form body.
   - Then `GET https://api.twitch.tv/helix/users` with `Client-Id` and `Bearer <user token>`, which
     returns the token's own user: `id`, `login`, `display_name`.
   - Then revoke the token (best effort): `POST id.twitch.tv/oauth2/revoke` with `client_id` and
     `token`. The user token and refresh token are never stored and never logged.
   - If the request carries `Mattermost-User-Id` **equal to** the state's user: link at once. The page
     says "Twitch account <name> is now linked to VillaNET. You can close this tab."
   - If it carries a **different** user: refuse ("You started this as another Mattermost account"), and
     do not link.
   - If there is **no session** (the desktop-app case): write
     `KVSetWithExpiry("twitch_pending_"+userId, {id, login, name, code, at}, 600)`. `code` is 8
     characters from `ABCDEFGHJKMNPQRSTUVWXYZ23456789`. The page shows "Type this code into VillaNET to
     finish: K7QM-4XRP".
   - **Why the code path is safe.** An attacker who starts a link for themselves and tricks a victim
     into approving it would have the victim's Twitch account linked to the attacker's Mattermost
     account. The session check stops that, and so does the code path, because the code appears only
     in the victim's browser, never in the attacker's panel.
   - Callback pages are fixed HTML with no script, `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'`,
     `Cache-Control: no-store` and `Referrer-Policy: no-referrer`. **Everything Twitch supplied** that
     a page shows (the display name, the login) goes through `html.EscapeString`, and so does the
     `error_description` if one is ever shown. Test it with a display name of `<script>x</script>`.
3. `POST /api/v1/twitch/link/confirm {code}` (signed in; the same limiter). Compare in constant time.
   After 5 wrong tries, delete the pending entry.
4. `DELETE /api/v1/twitch/link` (signed in; never metered, like taking back a plan) unlinks.

**Stored:** one document, `twitch_links`, written with `casUpdate`:
`{"v":1,"users":{"<mmUserId>":{"id":"…","login":"…","name":"…","at":ms}}}`.

- At most 500 people.
- A Twitch id belongs to one Mattermost user. A new verified link takes it from anyone else, as
  `setLink` does for stream keys.
- The login is checked against `^[a-z0-9_]{1,25}$`.
- One document, not per-user keys, so the poller reads one key and `kvKeysWithPrefix` is not
  lengthened.
- State and pending keys carry an expiry and are few. They are invisible once expired, though the
  rows linger until Mattermost's daily cleanup.

### E.4 The poller

`twitch_live.go` runs a goroutine with the same lifecycle as voice (`voiceCancel`/`voiceDone`). It is
started or stopped from `OnActivate` and `OnConfigurationChange` when
`EnableTwitchLive && credentials` changes.

Every `twitchPollInterval` (60 s):

1. Read `twitch_links`. With nobody linked, clear the live map and make no request.
2. In batches of 100: `GET https://api.twitch.tv/helix/streams?type=live&first=100&user_id=…&user_id=…`
   with `Client-Id` and `Bearer <appToken>`.
   - 401: `invalidate`, then retry once.
   - 429: skip this round. Twitch's app bucket is 800 points a minute per Client ID, and one request is
     one point.
3. Map `user_id` to the Mattermost user. Keep, per user: login, display name, title (`cleanText`,
   140 runes), `game_name` (60 runes), `viewer_count`, `started_at`, and the `thumbnail_url` template.
   The template is accepted only if its host is `static-cdn.jtvnw.net`.
4. When a login or name changed, update the links document (best effort).
5. A failure keeps the last known map for up to 3 rounds, then empties it, as the Broadcast Box poll
   does.
6. When the set of live users or their titles changes, call `p.refreshSoon()`.

Every cluster node polls for itself, which is one request a minute each. The variables
`twitchAPIBase = "https://api.twitch.tv/helix"` and `twitchImageHosts = {"static-cdn.jtvnw.net"}` are
swapped by the fake in tests.

### E.5 Snapshot, /me and pictures

- `snapshot.Twitch []apiTwitchStream \`json:"twitch,omitempty"\``. It is filled in `pollOnce` from the
  in-memory map on every branch, with no network, and leaves out users who have a non-24/7 Broadcast
  Box card in `current.Streams`.
  ```go
  type apiTwitchStream struct {
      UserID      string `json:"userId"`
      Username    string `json:"username"`
      DisplayName string `json:"displayName"` // Mattermost name, as the Live now cards name people
      Login       string `json:"login"`       // checked ^[a-z0-9_]{1,25}$; the watch link is built from it
      Name        string `json:"name"`        // Twitch display name
      Title       string `json:"title"`
      Game        string `json:"game,omitempty"`
      Viewers     int    `json:"viewers"`
      StartedAt   string `json:"startedAt"`   // RFC 3339
      Thumb       string `json:"thumb"`       // version: started_at plus the 5-minute slot
  }
  ```
- `/me.twitch`: `{enabled, linked?: {login, name}, pending?: true}`. `enabled` means
  `EnableTwitchLive` is on, credentials are set, and the Site URL passes the `https` rule in §E.2.
- `GET /api/v1/twitch/thumbnail?user=<mmUserId>&v=…`:
  - Only for a user currently in the live map; otherwise 404.
  - Fetch the template with `{width}x{height}` replaced by `440x248`, through the same checks as
    `fetchMediaPicture`, against `twitchImageHosts`. Both `fetchMediaPicture` and
    `followMediaRedirect` hard-code `mediaImageHosts` today (`media.go:455-481`, through
    `mediaImageURLAllowed`). Phase 2 therefore parameterises them:
    - `fetchMediaPicture(ctx, address, hosts map[string]bool)`, with existing callers passing
      `mediaImageHosts`;
    - `imageURLAllowed(u, hosts)`;
    - `followMediaRedirect` keeps a redirect inside **the set its first URL belongs to**. It looks the
      first URL up in `pictureHostSets = []map[string]bool{mediaImageHosts, twitchImageHosts}`; when
      the URL is in neither, it applies today's same-host rule for API calls.

    A Twitch picture can then never be redirected to a TMDB or Steam host, nor the other way round.
  - Hold it in memory for 2 min per user.
  - Answer with `private, max-age=60`.
  - Metered per user at 120 per minute, only on a miss.
- `reconcileBadges` (`poller.go:546-553`): when `EnableLiveBadge && EnableTwitchLive`, add the users in
  the Twitch live map to `live`. The offline grace then applies unchanged.
- **Not included:** airtime, Top Snoopers, watchers, preview stills, Clips (3.0.0), and What's on?.
  The card shows Twitch's category instead.

### E.6 Webapp

- `TwitchCard.tsx` in Live now (`Sidebar.tsx`, after the `streams.map(LiveCard)`). It shows a "on
  Twitch" text tag (no Twitch logo, which avoids brand-guideline questions), the relayed picture, the
  title, the category, "N watching on Twitch", and how long it has been live. Its only action is the
  external link. The Live now count includes these cards.
- The profile popover (`PopoverLive.tsx`) gets a "Live on Twitch" row with the link.
- The footer: while `me.twitch.enabled`, a small **Link Twitch** pill when not linked; "Twitch:
  <name> · Unlink" when linked; and the code box ("Type the code Twitch's page showed you") while
  pending. Place it beside the stream link controls, in the house style.
- Lab chips: **Twitch live** (two members live on Twitch, one of them also on Broadcast Box),
  **Twitch link pending**, **Twitch linked**, **Twitch off**.

### E.7 Tests (Phase 2)

| Area | Tests |
|---|---|
| Linking | The session path links. The no-session path gives a code, and the right code links. The wrong code 5 times clears it. A different session is refused. A reused, expired or unknown state is refused with zero outside requests. Twitch's refusal (`error=access_denied`) links nothing. The exchange failing links nothing. The user token is revoked and never logged. A new link takes a Twitch id from its previous owner. Login validation. 500 people at most. |
| Poller | Off or nobody linked makes zero requests. Batches of 100 (205 people make 3 requests). A 401 refetches the token and retries once. 429 skips the round. Failures keep the last known map for 3 rounds. A rename updates the document. A thumbnail template on another host is refused. |
| Snapshot | Somebody live on both shows only the Broadcast Box card. The badge is applied and removed through the grace. The marshalled snapshot contains no token, code or state. |
| Pictures | Only users live now. Only the allowed host. Size and sniff checks. |

### E.8 Build plan (Phase 2)

Built in the twitch-live sandbox (§D.4), starting from the Part A checkpoint. The server builder owns
`twitch_live.go`, the link routes, the poller lifecycle, the snapshot and badge changes, the picture
host-set parameterisation (§E.5), and plugin.json's Twitch section. It also owns README and LOCKDOWN,
in **new** paragraphs only: new hosts `api.twitch.tv` and `static-cdn.jtvnw.net`, and the polling. The
webapp builder owns `TwitchCard`, the footer controls (the two-step link, §E.3), the popover row,
`AdminTwitchSetup`, the `AdminSettingsBackup.tsx` sentence (§E.2) and the lab. The contract between
them is §E.5 and the routes in §E.3.

**Wording that changes once both phases have landed** (the landing step in §D.4, because Phase 1
rewrites these same paragraphs):

- The MediaLookup help text's "Off by default: while it is off the plugin makes no outside requests
  at all beyond Broadcast Box and Mumble" is no longer true once Show people live on Twitch is on. It
  becomes "…beyond Broadcast Box, Mumble and, if Show people live on Twitch is on, Twitch".
- README `:2094` names Twitch live as a second optional exception, off by default.
- LOCKDOWN's "What the plugin touches" paragraph adds: only if an admin switches on "Show people live
  on Twitch" (off by default), `api.twitch.tv` once a minute while somebody has linked, `id.twitch.tv`,
  and `static-cdn.jtvnw.net` for relayed previews. A browser contacts Twitch only when its person
  presses Continue on Twitch or Watch on Twitch.

Before release, check with Victor's real application:

- the empty scope is accepted;
- the redirect URL is accepted beside `http://localhost`, and whether a plain-http LAN address is
  refused;
- the code path works from the desktop app.

---

## Questions for Victor (the build uses the default until he says otherwise)

**Answered by Victor on 2026-10-02:** Q1 **keep cover art on disk too** (not memory only) - built AFTER this
patch lands in the main tree, as a follow-up: pictures under `<FileSettings.Directory>/ceo.villa.villanet/media/`
using the same root resolution and local-driver rule as Clips (`server/clips_files.go`), bounded in size, expiring
with their titles (IGDB art within IGDB's 7 days), cleared by "Forget remembered titles", and memory-only when the
file driver is not local. Q2 **7 days for an IGDB title, 3 days for a search** (the default). Q3-Q7 keep their
defaults. Q8-Q11 are about Twitch live and are superseded by `design/twitch-live.md` and its answers.


1. **Should picture files be remembered too, not just titles?** Default: **no**, pictures stay in
   memory. With the title kept, a picture after a restart costs one fetch from the image server and
   no lookup. Keeping the files belongs after Clips, which adds the file-storage code it would reuse,
   and works only with Mattermost's "local" file storage.
2. **How long may IGDB data be kept?** IGDB's FAQ encourages caching. The Twitch Developer Agreement
   mentions 24 hours unless authorised. Default: **7 days for an IGDB title and 3 days for an IGDB
   search**, with deletions honoured. Say "1 day" for the strict reading.
3. **Leave out games IGDB marks erotic?** Default: **yes** (`themes != (42)`). This is a work chat.
4. **Which IGDB entries count as games?** Default: main games, standalone expansions, remakes,
   remasters, expanded games and ports. DLC, editions, bundles, mods, episodes, seasons, packs and
   updates are left out.
5. **IGDB games that are also on Steam?** Default: **left to Steam whenever Steam lists them for the
   same search**, so they never appear twice and Steam's page is the link. Games Steam no longer sells
   (delisted, such as Forza Horizon 4 and Spec Ops: The Line) come from IGDB, because Steam's search
   never lists them.
6. **An admin "Forget remembered titles" button?** Default: **yes**, in Movies and games, with the
   counts.
7. **Should a search answered from memory still count towards the 40 searches a minute per person?**
   Default: **yes, unchanged**. It is simpler, and nobody types 40 a minute.
8. **Twitch live: who may link?** Default: **any signed-in member**, whether or not they have a
   Broadcast Box stream linked.
9. **Twitch live: watch inside the panel, or open Twitch?** Default: **open twitch.tv in a new tab**.
   An embedded player would load Twitch in everybody's browser, which the plugin has promised never to
   do without a click. A click-to-load player could come later.
10. **Twitch live: the live badge, airtime and boards?** Default: **badge yes; airtime and boards
    no**. They count VillaNET streaming.
11. **Twitch live: how often to ask Twitch?** Default: **once a minute**.

---

## Constants added (Phase 1)

| File | Name | Value |
|---|---|---|
| media.go | `mediaProviderTMDB/Steam/IGDB`, `mediaSourceIGDB` | `"tmdb"`, `"steam"`, `"igdb"`, `"IGDB"` |
| media.go | `igdbCallsPerMinute`, `igdbCallsPerSecond`, `igdbConcurrent` | 120, 4, 4 |
| media.go | `errIGDBResting` (var) | §B.2 |
| igdb.go | `igdbAPIBase`, `igdbImageBase` (vars) | `https://api.igdb.com/v4`, `https://images.igdb.com/igdb/image/upload` |
| igdb.go | `igdbSearchLimit` | 16 |
| igdb.go | `igdbGrace` (var, so tests can shorten it), `igdbFailurePause` | 1.5 s, 30 s |
| igdb.go | `igdbImageID`, `igdbSlugPattern`, `igdbLinkPattern`, `steamAppURLPattern` | §B.3, §B.5 |
| twitch_app.go | `twitchIDBase` (var) | `https://id.twitch.tv` |
| twitch_app.go | `twitchTokenBudget`, `twitchRenewBefore`, `twitchValidateEvery`, `twitchRefusedPause`, `twitchFailurePause`, `twitchWarnEvery`, `twitchMaxLifetime`, `maxTwitchAnswerBytes` | 8 s, 1 h, 1 h, 5 min, 15 s, 15 min, 90 d, 64 KiB |
| media_cache.go | `kvMediaCachePrefix`, `mediaCacheShards`, `mediaCacheVersion` | `"mediacache_"`, 16, 1 |
| media_cache.go | `maxMediaShardBytes`, `maxMediaShardEntries`, `maxMediaKeptBytes` | 128 KiB, 256, 16 KiB |
| media_cache.go | `mediaKeepSearch`, `mediaKeepIGDBSearch`, `mediaKeepEmptySearch` | 7 d, 3 d, 1 d |
| media_cache.go | `mediaKeepTitle`, `mediaKeepIGDBTitle`, `mediaKeepRunningShow`, `mediaKeepChanging`, `mediaKeepMissing`, `mediaKeepStaleLimit` | 30 d, 7 d, 7 d, 3 d, 1 d, 90 d |
| media_cache.go | `mediaTouchEvery`, `mediaRefreshArtAfter`, `mediaCacheAttempts`, `mediaKeepMinRunes`, `mediaGoneConfirm` | 3 d, 24 h, 2, 3, 1 h |

---

## Review log

An adversarial review, 2026-10-02. Each issue was checked against the sandbox code, IGDB's official API
docs (api-docs.igdb.com, fetched that day), and Mattermost v9.0.0 source, and then accepted (the design
above already carries the fix) or rejected. Parts of a fix that were rejected are said so.

| # | Issue | Severity | Decision | Reason |
|---|---|---|---|---|
| 1 | §B.3/§B.4: games delisted from Steam vanish from both sources | major | Accepted | OBSERVED 2026-10-02: `storesearch?cc=US` for "forza horizon 4" lists only Forza Horizon 5 add-ons, and for "spec ops the line" lists nothing. The Steam link is now kept as `SteamApp` (stored in L2) and judged in `mergeGames` against Steam's actual answer. New test `TestIGDBKeepsGamesSteamNoLongerSells`; the old test is renamed; Question 5 is reworded. |
| 2 | §B.2: repeated IGDB 401s mint a token per search | major | Accepted | IGDB FAQ 4: "25 active Access Tokens … going over this limit starts to inactivate older tokens". New: a hold-back keyed to the credentials, and a freshly fetched token that IGDB still refuses is never invalidated. The test asserts 2 token POSTs, not 4, and none after the pause. |
| 3 | §D.1: the KV fault harness makes the cache tests vacuous | major | Accepted | Confirmed: `stats_test.go:185-207` (CAS ignores faults and counts nothing), `community_test.go:29`, `media_test.go:173`. Extending the harness is now the server builder's first step, with a sanity assertion. Also noted: testify answers with the first matching expectation, so the admin test needs `mediaPluginWithFaults` to register admins first. |
| 4 | §C.4/§C.8: touch can resurrect forgotten entries or overwrite fresher ones | major | Accepted | `updateKeptMedia` changes an entry only if its `At` is unchanged, never inserts, and never writes back the copy it read. The `Gone` mark uses it too. Test `TestATouchNeverBringsAnythingBack`. |
| 5 | §C.6: narrowing assumes prefix-monotone provider search | major | Accepted; narrowing dropped | IGDB documents search as "sorted by similarity to the given search string"; nothing documents TMDB or Steam matching. Removed narrowing, the completeness flags, TMDB `total_results`, `steamSearchPage`, `mediaNarrowMinRunes` and both narrowing tests. The exact-query cache meets the request. |
| 6 | §B.4: a partial failure warns on every keystroke | minor | Accepted | Now `warnMediaOnce("search-partial-"+provider)`, and silent for not-set-up, refused, resting and busy. The 502 path keeps its per-request LogWarn, which `TestAFailedSearchSaysSoWithoutGivingAnythingAway` pins. |
| 7 | §A.2: "nothing may refer to media" contradicts using `sendMedia` | minor | Accepted, the exception option | The file may use `sendMedia` and `mediaHTTPClient`; it must not read `MediaLookup`, `mediaFlags`, `mediaStore` or `warnMediaOnce`; it has its own `twitchWarnEvery`. Moving `sendMedia` into a neutral file was rejected as churn in a file the patch already changes heavily. |
| 8 | §A.2: flights are not tied to the credentials | minor | Accepted | Flights are keyed `token/`/`validate/` + `hex(fp[:8])`. A fetch started later wins the slot, so an old fetch's late answer cannot displace the new token. `TestChangingTheCredentialsDropsTheToken` is extended. |
| 9 | §B.2: the 4/s token bucket allows about 7 a second; the test uses the real clock | minor | Accepted, different fix | Confirmed in `ratelimit.go` (burst plus continuous refill). The proposed `newRateLimiter(1, 250ms)` was rejected: it would refuse the 401 retry and any second search inside 250 ms. Instead, a ring of the last 4 start times gives exactly "4 in any second". Both limits run on `store.now`, so the test is deterministic. |
| 10 | §B.3: blacklist sanitising of search text | minor | Accepted; optional probe rejected | The docs write `search “zelda”;`. Now a whitelist: letters, digits, spaces and `- : . , & ! ? +`, with apostrophes removed as `mediaQueryKey` does. The 400 warning is reworded. A probe query was rejected: it adds an outbound request just for diagnosis. |
| 11 | §B.3: rule 2 overrides the keep list; the ids are documented, not UNVERIFIED | minor | Accepted | The docs give `parent_game`'s description, the migration note "All current enum values will remain the same", and the enum tables (Steam source 1, website 13). Rule 2 now applies only to untyped rows. The ids are marked documented, the `where` clause gains the `game_type` group (documented `(a,b)` and grouping syntax), and there is a `game_status` id fallback. |
| 12 | §A.3: the renewal test contradicts the algorithm | minor | Accepted | Now `expires_in` 10800: +30 min nothing, +61 min one validate, +2h01m one POST. |
| 13 | §C.1/§C.8/§C.10: deleting shards shifts the OFFSET key walk | minor | Accepted | Verified: v9.0.0 `sqlstore/plugin_store.go` `List` orders by `PKey` with `Offset`, and `links.go:173-179`. Forget and unreadable shards now CAS-write `{"v":1,"entries":{}}`, and Forget creates no keys. |
| 14 | §A.1: the Phase 1 redirect URL can block an http-only LAN admin | minor | Accepted | IGDB docs: "Please add 'localhost' to continue"; Twitch's getting-started uses `http://localhost:3000`. Phase 1 uses `http://localhost`; Phase 2 adds the callback and requires an https Site URL. |
| 15 | §B.9/§C.10: missed two-provider places; README `:2110` becomes false | minor | Accepted | Every line verified in the sandbox. `:2110` is reworded; `:326`, `:608`, `:1361-1366`, `:2113`, `:1369`, `:2134-2135`, `community.go:538`, `PopoverLive.tsx:167` and `lab/media.ts:3`/`:62` are added. |
| 16 | §B.4: a slow IGDB holds every game search | minor | Accepted | A 1.5 s grace once Steam has answered; search flights detached on their own budget, so IGDB still fills the cache; a 30 s hold-back after a timeout, 429 or 5xx. Two new tests. |
| 17 | §C.4: a held nil in L1 returns `(nil, nil)` and panics `planMedia` | minor | Accepted | Confirmed `media.go:764-766` and `:1015`. Both L1 checks map nil to NotFound. Test `TestAMissingTitleIsNotAskedForTwice`. |
| 18 | §C.4: one NotFound replaces a good title for a day | minor | Accepted, the `Gone` option | Confirmed `media.go:952-954` (`success:false` becomes NotFound). A NotFound is believed the second time, at least 1 h later; deletions are still honoured. |
| 19 | §C.1-§C.3: searches crowd out titles; write volume | minor | Partly accepted | Eviction now goes searches, then missing markers, then titles; searches under 3 runes stay out of KV. Rejected: an in-memory decoded-shard cache. L1 already answers repeats, and a second memory layer adds cross-node staleness to save one `KVGet`. |
| 20 | §B.6: the art-refresh trigger is vacuous for titles with no art | minor | Accepted | Confirmed: `pictureAddresses` drops empty addresses. At least one address must have been tried. Test case added. |
| 21 | §C.10: the README overstates key privacy | minor | Accepted | Reworded to "filed under a hash … not tied to who searched", and the design says why it claims no more. |
| 22 | Part E gaps (popup, https, help text, host sets, backup sentence, escaping) | minor | Accepted; (1) with a different fix | (1) The first press fetches the address; the pill then becomes a real link. A GET-redirect route was rejected because the desktop app opens it in a browser without the session. (2)-(6) are as proposed (§E.2, §E.3, §E.5, §E.8). |
| 23 | Phase 2 waits on IGDB and the cache, which it does not need | minor | Accepted | Part A is built first, then the checkpoint `.twitch-partA` and a Phase 2 sandbox, with Phase 2's diff taken against the checkpoint. The shared-paragraph wording is done at landing (§D.3, §D.4). |
| 24 | §B.5/§D.2: IGDB genre names are long; the lab misrepresents the line | minor | Accepted | IGDB's docs show "Role-playing (RPG)" for genre 12. `igdbGenreName` shortens a trailing "(ABC)", with a table test; the lab line now matches the server. |
