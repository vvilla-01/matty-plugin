# VillaNET Clips: implementation spec for v3.0.0 (revision 2)

This spec is written for the server and webapp implementers, who have not seen the research. §0 records each design decision and why it was made. This revision resolves the two reviews of v1; Appendix A lists every reviewer problem and what was done about it. Appendix B lists the owner's open questions, each with the default this spec implements. Paths are relative to `/home/user/Documents/Broadcast Box Plugin` unless stated.

The research left prototype code in `/tmp/claude-1000/-home-user-Documents-Broadcast-Box-Plugin/f246a2f2-7be9-4da0-8cf3-a9bade7355be/scratchpad/clips/`:
- `capture/proto/recorder.go`, `capture/proto/ring.go`
- `proto/internal/clip/*.go`
- `gaptest/main.go`, which checks the audio silence fill

These files are for reference only and may be deleted at any time. Everything needed to build the feature is written out in this spec.

> **As built (3.0.0, 2026-10-03).** Clips shipped in 3.0.0 from this spec plus `design/clips-addendum.md`, which wins where they differ (linked-only, sharing, clip tags). Where the build differs from the text below, a short *As built* note says so beside it: §0 Exact in-points, §2.4.5 step 1, §2.4.9, §2.14 and §4. The README's "Clips" section and `LOCKDOWN.md` describe what was built.

---

## 0. Decisions

| Topic | Decision | Why |
|---|---|---|
| Where media is captured | **On the server.** The plugin opens one receive-only WHEP session per live stream. | Proven end to end against a real BB v2.0.2: about 4% of one core and about 25 MB RSS per 6 Mbit/s stream, with no re-encode. Capturing in the browser would need a re-encode, upload 100+ MB, cover only what that tab had watched, and could not be trusted for votes. |
| Muxer | **mediacommon v2.9.5 `pmp4`**, with `TimeOffset` always 0 | It writes the header first in one pass and reads sample bytes lazily from disk. The same library supplies the H.264 and Opus helpers. Its two known defects are avoided or harmless:<br>• Its `avcC` omits the High-profile extension bytes. ffmpeg's own output did the same up to n4.0.<br>• A negative `TimeOffset` writes a bad edit list, so TimeOffset is never non-zero.<br>Round-tripping an ffmpeg H.264+Opus file decodes cleanly under `ffmpeg -xerror`. `Eyevinn/mp4ff` is the fallback. |
| Capture pipeline | **Per track, a reader goroutine that never touches the disk or the ring lock. One writer goroutine per recorder, fed through a bounded queue.** | On this host `net.core.rmem_max` is 212992 B, about 280 ms at 6 Mbit/s. A reader that blocks on `write()` loses packets, and it would also record late arrival times that look like a discontinuity. |
| A/V timing | **Arrival anchoring.** Each track's wall offset comes from the earliest arrival seen in that epoch's first 3 s. Discontinuities are detected from raw arrival times with a *persistent excess lag* rule (§2.6). | Measured error was +3 ms over 120 s. RTCP Sender Reports would map BB's NTP clock onto ours. A late burst after a network stall drains within a second, which separates it from BB's timestamp rewrite, where the excess lag persists. |
| Packet loss | **Keep the frames; the recorder never sends PLI or FIR** | OBS ignores PLIs anyway, and BB forwards subscriber PLIs with no rate limit. With NACK on a LAN, loss is close to zero. |
| Gaps where RTP and arrival agree (the publisher stalled 1–10 s) | **The epoch is kept.** Video freezes on the last frame until the next IDR. The audio gap is filled with Opus silence packets `F8 FF FE`. | A 3 s Wi-Fi hiccup should not throw away 2 minutes. In this research, `F8 FF FE` decoded to digital silence (peak 0) in ffmpeg and kept the audio timeline at 10.01 s. Letting the gap stretch the last sample's duration decoded to only 7.01 s, so the audio drifts out of sync. |
| B-frames | **Detected and reported as `unsupported`**, with an admin reason | `pmp4` is written with no composition offsets. OBS sets `bf=0` only through the WHIP service recommendations (`whip-service.cpp`), and the NVENC UHQ tune forces `bf=4`. |
| Ring location | `<FileSettings.Directory>/ceo.villa.villanet/ring/` | Needs no new setting. It is persistent, writable, never the bundle and never tmpfs. It is on the same filesystem as drafts and clips, so `os.Rename` is atomic. |
| Ring window | **The admin's Clip length L**, kept as its own value `ringWindow()` | Holds no more of anyone's stream than one press can take. Owner Q2's alternative (people extend to 5:00) only changes this value, plus an Extend control. |
| Ring size | **Sized from the measured bitrate.** Floor 12 Mbit/s, ×1.25 headroom, at most 1 GiB per ring and 4 GiB for all rings and tails together. Truncation is reported as truncation, never as "partial". | A fixed 12 Mbit/s cap silently shortened clips of 20 Mbit/s streams and then blamed the stream for having just started. |
| Ended and dropped streams | **The finished epoch is frozen as a read-only *tail* for 10 minutes, and it can be clipped.** | The end of a stream, or the moment before OBS dropped, is a prime clip moment. |
| Clip storage | Our own directory, served by our own authenticated route | The plugin API cannot delete stored bytes, so retention would never free space. A non-local file driver switches Clips off and says why. |
| KV layout | **One `clips` document** holding the clips, banked votes, holder, blocks, streamer opt-outs, score resets and the retention record. It carries a `rev` counter, and the cap is 500 clips or 1 MiB. **Every change is one CAS whose apply also recomputes the Best Clipper holder.** | No cross-document races. Settling is one write. The cache accepts only newer `rev`s. |
| Concurrent publishes | **Each draft has a state: `ready` → `publishing` → `published`.** Temp files get unique names and are opened with `O_EXCL`. Appending a clip id that already exists is a no-op. A failure undoes only what that request did. | Prevents double submits, two tabs, and a retry after a client-side network error. |
| Drafts | Kept 2 h, at most 3 per user and 20 server-wide | Lets someone clip now and trim after the stream. |
| Who may clip | **Someone watching that stream through VillaNET right now** (a counted session), **or the stream's manually linked streamer** | Otherwise back-to-back drafts would let a person follow a stream without ever being counted. |
| Click time | **The window is fixed when the request arrives,** before waiting for an export slot. A rate-limit token is spent only after the cheap checks pass, and it is refunded on `warming_up`, `busy` or `recording_failed`. | A queued click should still get the moment it asked for, and retrying during warm-up should not lock anyone out. |
| Exact in-points | **The file starts on the keyframe at or before the chosen start. The exact start is stored as `leadMs`**, and VillaNET's players start playback there. Durations leave out the lead. No edit lists anywhere. *As built: only the identity edit list `pmp4` writes for every track (one `elst` entry, media time 0, the whole duration; mediacommon v2.9.5 `pmp4/track.go` `marshalELST` with `TimeOffset` 0). It moves nothing, so the exact start still comes from `leadMs`.* | With OBS defaults, snapping the start would move it in 8-second steps. Firefox ignores the `media_time` of an edit list for video. |
| Codecs | **H.264 without B-frames, plus Opus.** AV1, VP9 and H.265 are offered in the SDP only so they can be detected, and report `unsupported`. | OBS WHIP defaults to H.264. |
| When recording happens | **Always**, for every live, non-24/7 stream whose streamer has not switched clipping off, up to 4 at once. **Slots are sticky.** A free slot goes to the most-watched stream waiting for one. | "Chat/viewers" includes someone who tuned in 10 s ago. A newcomer must never take a running recorder's slot and buffer. |
| Streamers | **Told while they are being recorded. Can switch clipping of their stream off. Can wipe the held minutes.** Can delete clips of their stream when their stream is manually linked. | Recording people needs notice and control. |
| 24/7 channels | Never recorded; there is no setting for it | They are third-party content, and are already excluded from stills and airtime. |
| Best Clipper threshold | 1 upvote | A small server; nobody holds it at 0. |
| Votes on deleted clips | **Expiry and streamer deletion bank the votes; clipper and admin deletion void them.** Only votes from active, non-bot accounts count. An admin can reset a clipper's score. | "All time" survives expiry. A clipper cannot delete a clip and keep its score. |
| Duplicate clips | **Refused** when more than half of the new clip overlaps a live clip by the same clipper of the same stream | Stops farming votes by publishing the same moment several times. |
| Best Clipper while Clips is off | **Hidden**; the holder is kept | Matches the boards, which disappear while the leaderboard is off. |
| Storage fairness | **Each person's share is max(1 GiB, limit ÷ 5)** of published clips | One person cannot stop publishing for everyone for 30 days. |
| Shortening retention | **Applies 24 h after the change** to clips it would remove. Lengthening applies at once. | A typo of 3 for 30 must not destroy clips within ten minutes. |
| Thumbnails | Made in the browser, logged at upload, removable by admins. **Anyone can report a clip.** | The server cannot prove a picture is a frame of the clip. |
| Snapshot updates from clip writes | **A clips-only republish from the cache, coalesced to at most one every 2 s. It never polls BB.** | `stills.go` deliberately avoids `refreshSoon` for the same reason. |
| Discovery | **A clapper button in the header, a one-line Clips row in the panel, and a "new" dot** (per viewer, in localStorage) | A header icon alone would starve the voting loop. A link route is deferred (Q15). |
| Simulcast `/api/layer` | Not called | Keeps LOCKDOWN's list of endpoints the plugin calls short. |
| Safari | **Not expected to play the sound** (Opus in MP4). The webapp checks video and audio separately. | The caniuse and MDN data list no Safari version that plays Opus in MP4. |
| Real-time WebRTC tests | **Run only with `VILLANET_E2E=1`, via `make e2e`**. They are not part of `make check`. | `make check` runs `go test -race` without `-short`, and real-time WebRTC under the race detector is flaky. |

---

## 1. Architecture and data flow

```
OBS --WHIP--> Broadcast Box (192.168.0.106:8080, media UDP 3478)
                 |  WHEP (one receive-only session per recorded stream, opened by the PLUGIN SERVER)
                 v
 Mattermost host (192.168.0.101) -- plugin process
   poller (every PollIntervalSeconds) --reconcile--> clipRecorders (sticky slots, opt-outs, tails)
   per track:  reader goroutine: ReadRTP -> arrival map -> samplebuilder -> clock/excess/gap/B-frame checks
                                   | bounded queue (1024 samples / 16 MiB)
               writer goroutine:  ring (disk segments + RAM index)  --stream ends / epoch breaks-->  tail (10 min)
 Viewer clicks ✂ -- POST /clips/drafts {handle, source} -> clickAt fixed, watch gate, pin [clickAt-L, clickAt]
                                                       -> export (no re-encode) -> drafts/<id>.mp4 (private, 2 h)
 Editor: preview (Range), trim (exact in via leadMs, frame-exact out), browser JPEG (PUT thumb), POST publish
                                                       -> draft state publishing -> trim/rename -> clips/<id>.mp4
                                                       -> one CAS on "clips" (entry + holder) -> republishClips()
 Everyone: Clips row / clapper -> dialog (GET /clips) -> viewer (GET /clips/video, Range) -> upvote (one CAS)
 Best Clipper = eligible all-time upvotes (live + banked) -> snapshot.bestClipper (only while Clips is on)
 Sweep (cluster job, 10 min): retention record, expire (one CAS, bank) -> delete files; orphans
```

**What the server does:**
- Runs one receive-only WHEP client per recorded stream. The stream key is used only on the server, through the existing `exchangeWHEP`.
- Keeps a rolling window of the last `L` seconds on disk, where L is the admin's clip length, 30–300 s.
- When a stream ends or an epoch breaks, keeps the last epoch for 10 minutes as a tail.
- On a click, cuts a lossless MP4 draft (H.264 + Opus, moov first) from the window that ends at the click.
- On publish, re-cuts the draft to the chosen in and out points. The file's in-point is the keyframe at or before the chosen start, with `leadMs` recording the difference. The out-point is frame-exact.
- Serves clips with `http.ServeContent`, so Range requests and seeking work.

**Limits, stated honestly.** The README and the admin copy repeat these.

1. **The Mattermost host must be able to exchange UDP with BB on port 3478.**
   - The recorder is a WebRTC peer. It uses no STUN or TURN, and it only pairs with the IPv4 addresses that `BroadcastBoxURL`'s host resolves to.
   - It fails if the host cannot exchange UDP with that address on BB's UDP mux port, if BB's `INTERFACE_FILTER` hides that address, or if `BroadcastBoxURL` points at a proxy instead of BB's LAN IP.
   - **When it fails, nothing can be clipped.** The ✂ says why, admins see a reason, and recording retries with backoff.
   - A Mattermost behind Docker NAT still works, through peer-reflexive candidates.
2. **The file of a clip starts on a keyframe.** VillaNET's players start at the exact chosen moment. Any other player would show up to one keyframe interval of lead-in: OBS's default "Keyframe Interval 0" is 250 frames, about 8.3 s at 30 fps. The README recommends 2 s, which also keeps files small.
3. **H.264 without B-frames only.** AV1, HEVC, VP9 and B-frame streams show "can't be clipped yet". Admins see the reason.
4. **Epochs.** An OBS reconnect, a timestamp discontinuity, a change of resolution, parameter sets or codec, or a slow disk starts a new epoch. A clip never spans two epochs. The previous epoch stays clippable for 10 minutes as a tail.
5. **A restart loses the rolling buffers, the tails and all drafts.** This covers a plugin restart, an upgrade and a server restart. Published clips survive.
6. **Local file storage only.** If FileSettings.DriverName is not `local`, Clips switches itself off.
7. **Safari is not expected to play the sound** (Opus in MP4). The webapp says so when it can tell.
8. **At most 4 streams are recorded at once.** A fifth live stream shows "Clipping is busy with other streams right now" until a slot frees.
9. **Simulcast publishers** may be recorded at whichever layer BB hands the recorder.
10. **Broadcast Box's own pages count the recorder as a viewer**, one per recorded stream. This includes BB's Current Viewers and the pop-out watch page. VillaNET's own counts and faces do not.
11. **Very high bitrates.** A ring holds at most 1 GiB, which is about 26 Mbit/s for a 5:00 clip. Above that, clips are shorter, and the editor and the admin view say so.
12. **A stream's ending stays clippable for 10 minutes** after it goes off, and only by people who were watching it.

---

## 2. Server

### 2.1 Files

New files, all in `package main` under `server/`:

| File | Responsibility |
|---|---|
| `clips.go` | Constants. The KV document type and its pure functions: expiry, scoring, `pickBestClipper`, counted votes, duplicate overlap, retention record. The cache (`clipsStore`). `clipsCAS` (decode, mutate, recompute holder, `rev++`, size check). Snapshot view builders. `republishClips`. |
| `clips_api.go` | Every `/api/v1/clips*` and `/api/v1/admin/clips*` handler, the error-code table, request validation and rate limiting. |
| `clips_files.go` | Resolving the storage root, creating and wiping directories, usage accounting per person and in total, free-space checks, `serveClipFile` (Range), thumbnail storage, unique temp files. |
| `clips_diskfree_unix.go` (`//go:build !windows`) and `clips_diskfree_windows.go` | `freeBytes(path) (free, total uint64, ok bool)` via `golang.org/x/sys/unix.Statfs` and `golang.org/x/sys/windows.GetDiskFreeSpaceEx`. |
| `clips_recorder.go` | The `clipRecorders` manager: reconcile, sticky slots, backoff, session-id registry, tails, `stopAll`, `wipe`. The `recorder`: pion PeerConnection lifecycle, the reader goroutines, watchdogs. |
| `clips_access.go` | The viewer registry and the watch gate (§2.4.9). Streamer checks: the manual link, opt-out. |
| `clips_media.go` | `arrivalClock`, the arrival map, the excess-lag detector, gap and B-frame detection, H.264 access units (Annex-B to AVCC, SPS/PPS, IDR), Opus samples, the silence fill. |
| `clips_ring.go` | The writer goroutine and the disk ring: segments, index, eviction, bitrate-sized cap, pins, epochs, freezing a tail, `snapshotRange`. |
| `clips_mux.go` | `exportDraft` (ring to MP4), `trimClip` (MP4 to MP4), `ticksToMs`, the codec string. |
| `clips_sweep.go` | The retention job (`cluster.Schedule`) and in-process housekeeping: draft TTL, drafts-directory scan, tail expiry, ring budget, free space, registry pruning. |
| `testdata/clip-h264-opus.mp4` | A 10 s test fixture (§2.19). |

Changed files:

| File | Change |
|---|---|
| `plugin.go` | New snapshot and apiStream fields, and new plugin fields: `clipsOnce`, `clipsState`, `recordersOnce`, `recordersState`, the limiters, `clipsJob *cluster.Job`, the republish timer. `OnActivate` and `OnDeactivate` changes. `OnConfigurationChange` calls `stopAll` (§2.14). `manifestVersion = "3.0.0"`. |
| `configuration.go` | New fields, `clipLengthSeconds()`, clamps in `normalize()`. |
| `defaults.go` | `sectionDefaults` entries. |
| `ratelimit.go` | New `func (r *rateLimiter) refund(key string)`, which adds one token back, capped at the burst. |
| `poller.go` | `pollOnce` changes:<br>• On all three publish paths, call `p.addClips(current)` right after `addCommunity`.<br>• In the **unconfigured** branch, call `p.recorders().stopAll(false)`.<br>• On the success path, after `decorate` and `recordStreamTime`, call `p.recorders().noteSessions(raw, now)`, then `p.recorders().reconcile(streams, cfg, now)`, then annotate the streams.<br>In `decorate`, remove the recorder's own sessions from `counted` (§2.4.7). |
| `api.go` | New route cases; `meResponse.ClipsAlert` and `ClipsStreamer`. In `handleWHEP`, after a successful exchange of a **non-peek** session and while `EnableClips` is on, call `p.recorders().noteViewer(sessionID, userID, handle, now)`. This happens whatever `ShowWatchers` says. *As built: whatever `EnableClips` says too (§2.4.9).* |
| `Makefile` | New `e2e` target: `VILLANET_E2E=1 $(GO) test -count=1 -run 'E2E' ./server/...`. `check` is unchanged. |
| `plugin.json` | New "Clips" section, version 3.0.0, and a description that mentions clips. |
| `go.mod` | New dependencies (§2.18). |

### 2.2 Settings

**`plugin.json`**: a new section inserted **after `airtime`**.

```json
{
  "key": "clips",
  "title": "Clips",
  "subtitle": "Let people keep the last few minutes of a stream, trim it and share it.",
  "settings": [
    {"key": "EnableClips", "display_name": "Let people clip streams", "type": "bool", "default": true,
     "help_text": "A ✂ on every playing stream keeps its last few minutes as a draft that person can trim, name and publish to Clips. While this is on, this server keeps a rolling recording of each live stream on its own disk (never 24/7 channels) by opening one extra playback session per stream to Broadcast Box over UDP. Broadcast Box's own pages count that session as a viewer; VillaNET's counts do not. Streamers who have linked their stream can switch clipping of it off, and wipe what is held. Switching this off stops recording at once; published clips stay until they expire."},
    {"key": "ClipLengthSeconds", "display_name": "Clip length", "type": "dropdown", "default": "120",
     "options": [{"display_name":"30 seconds","value":"30"},{"display_name":"1 minute","value":"60"},{"display_name":"1 minute 30","value":"90"},{"display_name":"2 minutes","value":"120"},{"display_name":"3 minutes","value":"180"},{"display_name":"4 minutes","value":"240"},{"display_name":"5 minutes","value":"300"}],
     "help_text": "How much one press keeps. People can trim a clip shorter, never longer. Each live stream's last this-many minutes are held on disk, about 45 MB per minute at 6 Mbit/s."},
    {"key": "ClipRetentionDays", "display_name": "Keep clips for (days)", "type": "number", "default": 30,
     "help_text": "Published clips are deleted this many days after they were published. Their upvotes keep counting toward Best Clipper. Lowering this removes the clips it makes too old only after 24 hours, so a mistake can be put right. 1 to 365."},
    {"key": "ClipStorageLimitGB", "display_name": "Space for clips (GB)", "type": "number", "default": 20,
     "help_text": "The most disk that published clips and drafts may use. One person's clips may use at most a fifth of it (at least 1 GB). Publishing is refused beyond either; nothing is deleted to make room. The rolling recordings are bounded separately (at most 4 GB)."},
    {"key": "ClipsAdmin", "display_name": "Clips on the server", "type": "custom"}
  ]
}
```

**`configuration` struct fields:**

```go
EnableClips        bool
ClipLengthSeconds  string
ClipRetentionDays  int
ClipStorageLimitGB int
```

`ClipsAdmin` is a custom setting with no field, like `PreviewPictures`.

**Normalisation.** There is one rule, in `func (c *configuration) clipLengthSeconds() int`:

| Input | Result |
|---|---|
| `strconv.Atoi(strings.TrimSpace(v))` fails (including empty) | 120 |
| v < 30 | 30 |
| v > 300 | 300 |
| Anything else | The nearest of {30, 60, 90, 120, 180, 240, 300}; a tie goes to the shorter |

So 999 → 300, 0 → 30, 75 → 60, 100 → 90 and `"abc"` → 120.

In `normalize()`:
- `ClipRetentionDays`: below 1 becomes 30; above 365 becomes 365.
- `ClipStorageLimitGB`: below 1 becomes 20; above 2000 becomes 2000.

`func (c *configuration) ringWindow() time.Duration` returns `clipLengthSeconds()` seconds. This is the single place to change if Q2 is answered the other way.

**`sectionDefaults`**, needed for Mattermost before 11.8:

```go
// Clips
"EnableClips":        true,
"ClipLengthSeconds":  "120",
"ClipRetentionDays":  30,
"ClipStorageLimitGB": 20,
```

`TestTheSectionDefaultsMatchTheManifest` must still pass. Do not add `ClipsAdmin`.

**Config changes.** See §2.14. `OnConfigurationChange` stops recorders directly when Clips is switched off or the BB URL changes. It does not wait for a successful poll.

### 2.3 Storage layout and bounds

**Root.** `root = <abs(FileSettings.Directory)>/ceo.villa.villanet/`
- Read `cfg := p.API.GetConfig()` and require `*cfg.FileSettings.DriverName == model.ImageDriverLocal` (`"local"`).
- `Directory` may be relative (`./data/`). Resolve it with `filepath.Abs` once in `OnActivate`; the plugin process inherits the server's working directory. Log the result at Info.
- Never use the plugin bundle directory or `os.TempDir()`.

Directories are created with mode 0750 and files with 0600.

| Path | Contents | Lifetime |
|---|---|---|
| `ring/<handle>/<epoch>/<seq>.seg` | Raw sample payloads of a live ring or a tail | Wiped on activate, on deactivate, when Clips is switched off, and on opt-out or wipe for that handle. A tail lasts 10 min. |
| `drafts/<id>.mp4`, `drafts/<id>.jpg` | Drafts | Wiped on activate and deactivate. 2 h TTL. Removed after a successful publish. |
| `drafts/.tmp-<id>-<rand>.mp4` | An export in progress | Removed on failure. Swept when older than 10 min. |
| `clips/<id>.mp4`, `clips/<id>.jpg` | Published clips | Until settled (§2.9) |
| `clips/.tmp-<id>-<rand>.mp4` | A trim in progress | Removed on failure. Swept when older than 1 h. |

Every temp file is opened with `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)`, where `<rand>` is 8 random hex characters. Nothing is fsynced. The ring never syncs either, and a power cut losing a clip published seconds earlier is acceptable.

**Unavailable states.** When the driver is not local, or a directory cannot be created or written, `clipsStore.problem` is set:
- Non-local driver: "Clips need Mattermost's File Storage System set to Local file system; they are switched off."
- Unusable directory: "Clips cannot write to <root>: <err>."

While `problem` is set:
- `snapshot.clips.enabled = false`.
- No recorders run.
- `/me.clipsAlert` carries the problem for admins.
- Existing clips are still listed and served, if the directory is readable.

**Constants** in `clips.go`:

```go
const (
    kvClips         = "clips"
    clipsDocVersion = 1

    clipLengthDefault = 120 // seconds; options 30..300
    clipMaxSeconds    = 300
    clipMinMS         = 5000

    maxRecorders            = 4
    maxTails                = 4
    tailKeep                = 10 * time.Minute
    ringSegmentTarget       = 6 * time.Second
    ringFloorBytesPerSecond = 1_500_000 // 12 Mbit/s: the least any ring is sized for
    ringBitrateHeadroom     = 1.25
    ringSlack               = 20 * time.Second // segment + GOP margin
    ringMaxBytes            = 1 << 30          // one ring or tail
    ringBudgetBytes         = 4 << 30          // all rings and tails together
    maxKeyframeGap          = 60 * time.Second // a single segment over the cap, or no IDR for this long -> new epoch

    writerQueueSamples  = 1024
    writerQueueBytes    = 16 << 20
    excessLimit         = 2 * time.Second // persistent excess lag that means a timestamp rewrite
    excessHold          = 2 * time.Second // ...for this long of arrivals
    gapMin              = time.Second     // a video tick gap this long is a publisher stall
    noVideoWatchdog     = 10 * time.Second

    draftTTL         = 2 * time.Hour
    draftTombstone   = 10 * time.Minute // expired/wiped/published drafts are remembered this long
    maxDraftsPerUser = 3
    maxDraftsTotal   = 20

    maxClipBytes      = ringMaxBytes
    maxClipsPerUser   = 30
    maxClips          = 500
    maxClipsDocBytes  = 1 << 20
    maxClipTitle      = 80 // runes
    userShareMinBytes = 1 << 30 // share = max(this, limit/5)
    duplicateOverlap  = 0.5

    exportSlots = 2
    exportWait  = 15 * time.Second

    freeFloorBytes   = 2 << 30 // refuse new recorders/drafts/publishes below this...
    freeFloorPercent = 5       // ...or below this % of the volume
    recorderStopFree = 1 << 30 // running recorders stop (state "full") below this

    viewerGrace  = 60 * time.Second  // a just-opened session counts before BB lists it
    viewerMemory = 15 * time.Minute

    retentionGrace        = 24 * time.Hour
    minBestClipperUpvotes = 1
    clipsSweepInterval    = 10 * time.Minute
    republishEvery        = 2 * time.Second
    orphanAge             = 24 * time.Hour
    draftOrphanAge        = 10 * time.Minute
    tmpAge                = time.Hour
)
```

**Resource bounds:**

| Resource | Bound |
|---|---|
| Ring disk per stream | About (L + segment + GOP) × bitrate: about 110 MB at L = 120 s and 6 Mbit/s. The cap is `(L + 20 s) × max(1.5 MB/s, 1.25 × measured)`, never above 1 GiB. |
| All rings and tails | At most 4 GiB. Tails are deleted oldest-first to stay under it. |
| Ring RAM per stream | Index of about 80 entries/s × 48 B, about 1.2 MB at 5 min. Plus 2 samplebuilders (about 0.5 MB each), the writer queue (≤ 16 MiB, normally near empty) and pion. Under 30 MB per recorder. |
| Drafts plus published clips | At most `ClipStorageLimitGB` together. One person's published clips are at most max(1 GiB, limit ÷ 5). Each clip is at most 1 GiB. |
| Exports and trims | 2 at once. They stream through `bufio.Writer` (1 MiB) plus one sample buffer, never the whole file in RAM. They never hold a ring's lock while doing IO. |
| CPU | About 4% of one core per 6 Mbit/s stream. Export and trim are disk-bound, about 20 ms per 20 s of 6 Mbit/s media. |

### 2.4 The recorder

#### 2.4.1 pion API and allowed addresses

The API is built from the allowed-IP set.

```go
m := &webrtc.MediaEngine{}
fb := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}}
// H264 exactly as BB v2.0.2 internal/webrtc/codecs/codecs.go PT 96. BB binds one PT per codec
// FAMILY by MIME type (track_multi_codec.go Bind), so one H264 entry suffices.
m.RegisterCodec(webrtc.RTPCodecParameters{PayloadType: 96, RTPCodecCapability: webrtc.RTPCodecCapability{
    MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
    SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", RTCPFeedback: fb}}, webrtc.RTPCodecTypeVideo)
// Detect-only (so an AV1/VP9/H265 publisher is reported "unsupported" instead of silence):
//   AV1 PT 45 (no fmtp); VP9 PT 98 "profile-id=0"; H265 PT 113 "level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST"
m.RegisterCodec(webrtc.RTPCodecParameters{PayloadType: 111, RTPCodecCapability: webrtc.RTPCodecCapability{
    MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}}, webrtc.RTPCodecTypeAudio)

ir := &interceptor.Registry{}
webrtc.ConfigureNack(m, ir)          // NACK generator; BB runs the responder
webrtc.ConfigureRTCPReports(ir)      // receiver reports
// NO TWCC, NO PLI interval interceptor. The recorder NEVER writes PLI/FIR.

se := webrtc.SettingEngine{}
se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
se.SetRemoteIPFilter(func(ip net.IP) bool { return allowed[ip.String()] }) // IPv4s of BroadcastBoxURL's host
api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
```

**Resolving `allowed`.**
- `allowed` is the set of IPv4 addresses of the host in `BroadcastBoxURL`. An IP literal is used as it is. A name is looked up with `net.DefaultResolver.LookupIP(ctx, "ip4", host)` and a 5 s timeout.
- Resolve **only** when the host differs from the one the API was built for, or at the start of a connection attempt.
- **When a lookup fails or comes back empty, keep the last good set.** Never stop a healthy recorder because of a resolver error.
- If a new attempt resolves to a different set, rebuild the API for new attempts only. Running recorders are left alone.
- If there has never been a good set, attempts fail with the admin reason "Broadcast Box URL resolves to no IPv4 address".
- This drops BB's `NAT_1_TO_1_IP` public candidate and every other address, so media flows only to the host the plugin already talks to.
- Create the PeerConnection with `api.NewPeerConnection(webrtc.Configuration{})`, with no ICE servers.

#### 2.4.2 Connecting (one recorder per handle, with its own context)

1. Add two recvonly transceivers, audio and video. `CreateOffer`, then `SetLocalDescription`, then wait for `webrtc.GatheringCompletePromise` with a 5 s timeout. There is no trickle; BB does not trickle either.
2. Call `p.exchangeWHEP(ctx15s, cfg, streamKey, offer)`, which returns `answer, sessionID`.
   - Register `sessionID` in the manager's `own` set **immediately**, before ICE completes.
   - If `sessionID == ""`, log Warn once per recorder: "Broadcast Box did not say which session the clip recorder opened; it will be counted as a viewer."
3. `SetRemoteDescription(answer)`. Wait up to 10 s for `PeerConnectionStateConnected`; otherwise the attempt fails.
4. In `OnTrack`, start the track's reader goroutine (§2.4.3):
   - `video/H264` and `audio/opus` are recorded.
   - Any other video MIME type means state `unsupported`, close, and retry in 5 min.
   - Every receiver drains RTCP (`receiver.ReadRTCP()` in a loop) so the interceptors run.
5. The state becomes `ready` when the first IDR with SPS/PPS is written to the ring. `epochStart` is that IDR's wall time and becomes the snapshot's `clipFrom`.

**Closing.**
- `pc.Close()`. BB v2.0.2 has no WHEP DELETE; closing ICE ends BB's session at once.
- Keep the session id in `own` for 60 s after closing, so a poll that still lists it does not count it.

#### 2.4.3 Pipeline: readers and the writer

**Reader goroutine, one per track.** It never touches the disk or `ring.mu`.

```
for {
    pkt, _, err := track.ReadRTP()            // err -> recorder closing
    arr := time.Now().UnixNano()
    arrivals.note(pkt.Timestamp, arr)        // earliest arrival per RTP timestamp (§2.6)
    sb.Push(pkt)
    for s := sb.Pop(); s != nil; s = sb.Pop() {
        a := arrivals.take(s.PacketTimestamp) // falls back to time.Now() if missing
        ext := clock.unwrap(s.PacketTimestamp)
        clock.anchor(ext, a)
        -> checks: B-frames, gap, excess lag (§2.6)   // may emit a cut or a state change
        -> AU handling (video) / packet (audio)      // §2.6
        -> enqueue ringOp{track, wall: clock.wall(ext), ticks: ext, key, payload, afterGap}
    }
}
```

**Enqueueing** is non-blocking: `select { case q <- op: default: overflow }`. An atomic `queuedBytes` counter is checked against `writerQueueBytes` before sending.

On **overflow**, the reader:
1. Emits `cut{reason: "disk too slow"}`.
2. Drops samples until the queue is below half full.
3. Waits for the next IDR before forwarding video again.

The admin reason "disk too slow" is logged at Warn, at most once a minute per stream.

**Writer goroutine, one per recorder.** It applies `ringOp`s to the ring (§2.5) under `ring.mu`, one op at a time. It handles `cut` ops by freezing the current epoch as a tail (§2.4.8) and starting a new epoch. It also measures the bitrate: bytes appended over the last 10 s, divided by 10.

#### 2.4.4 Watchdogs and epochs

A **cut** freezes the current epoch as a tail when it holds at least 5 s, then starts a new epoch, which begins at the next IDR. A **reconnect** is a cut followed by close and reconnect with backoff.

| Trigger | Action |
|---|---|
| ICE or connection state `disconnected` for more than 5 s, or `failed`/`closed` when not asked for | Reconnect |
| No video RTP for `noVideoWatchdog` (10 s) while the snapshot says the stream is live and not `paused` | Reconnect. While paused, wait. |
| Excess-lag discontinuity (§2.6): BB kept the WHEP session across a publisher reconnect and rewrote timestamps with no gap | Cut, at the last sample before the late stretch began. No reconnect. |
| **Audio died**: audio had flowed in this session, the stream's status has `AudioTracks > 0`, and no audio sample has arrived for 2 s while video flows. This is BB reusing the outgoing SSRC with the new publisher's sequence numbers, so pion's SRTP replay check drops all audio. It is also the backstop for reconnects shorter than `excessLimit`. | Reconnect |
| SPS or PPS bytes change | Cut. The new epoch starts at this IDR. |
| Video payload type or codec changes mid-session | Not H.264: `unsupported`, then close. Still H.264: cut. |
| B-frames detected (§2.6) | State `unsupported`, reason "B-frames (set B-frames to 0; do not use the NVENC UHQ tune)". Close, retry in 5 min. |
| Keyframes too rare: a single segment exceeds the cap, or no IDR for `maxKeyframeGap` | Wipe the epoch without a tail. State `starting` until the next IDR. Reason "keyframes too rare". |
| Writer queue overflow | Cut (§2.4.3) |

**Backoff** between attempts is 2 s, 5 s, 15 s, 60 s, then every 5 min. It resets after 2 min of `ready`.
- State `failed` is set after 2 consecutive attempts without reaching `ready`, and stays until a later attempt reaches `ready`.

#### 2.4.5 The manager

Signature: `p.recorders().reconcile(streams []apiStream, cfg *configuration, now time.Time) map[string]recView`.

It is called on the poller's success path after `decorate`, `recordStreamTime` and `noteSessions`. The off cases are also handled outside the poll (§2.14).

1. If `!cfg.EnableClips`, or `problem != ""`, or the store is halted, or free space is below `recorderStopFree`: `stopAll(dropDrafts: !cfg.EnableClips)` and return an empty map.
   - `stopAll` closes every recorder, wipes `ring/` (tails included), and clears the viewer registry when Clips is off.
   - *As built: `stopAll` never clears the viewer registry. Only `stopClips` (the plugin stopping) does; see §2.4.9.*
2. **Opt-outs.** A stream whose `UserID` has `LinkSource == "manual"` and is in the doc's `optOut` gets no recorder. If one is running: stop it, delete its ring and tail, and discard drafts cut from that handle (§2.10.6). Report `off`.
3. **Eligibility:** `!s.AlwaysOn && s.Handle != ""` and not opted out. A stream that becomes `AlwaysOn` is stopped and wiped, with no tail.
4. **Sticky slots.** A running recorder keeps its slot while its handle is eligible, whatever the snapshot order. Free slots (`maxRecorders − running`) go to eligible handles without a recorder, ordered by:
   1. `Viewers`, descending
   2. `StreamStart`, ascending
   3. `Handle`

   Eligible streams left over are reported `queued`.
   - Before a new recorder starts, check the free-space floor (`freeFloorBytes`, `freeFloorPercent`) and `ringBudgetBytes`, after deleting expired or oldest tails. If space is still short, report `full`.
5. **Missing handles.** A recorder whose handle is not in `streams` gets `missing++`. Reset `missing` when the handle is seen again.
   - Once `missing >= cfg.OfflineGraceChecks`, stop the recorder and freeze its epoch as a tail with reason `ended`. BB keeps a session alive while any subscriber exists, so the recorder must leave.
   - `missing` is only counted on the success path, so a BB hiccup never tears down live recorders.
6. Return `handle → {state, clipFrom, tailEnd}`:
   - `clipFrom` is the epoch start in epoch ms, set only when `ready`.
   - `tailEnd` is set when a tail is held for this handle.

The manager outlives the poller: `restartPoller` on every settings save must not drop the rings. It is created via `sync.Once`, like `stills()`. Its housekeeping goroutine runs on a 60 s ticker (§2.13).

#### 2.4.6 States reported to browsers

`apiStream.Clip` is one of these values:

| Value | Meaning | ✂ shows |
|---|---|---|
| `"starting"` | Connecting, or waiting for the first IDR | "Clipping starts in a few seconds" |
| `"ready"` | Recording; `clipFrom` is set | Ready, or ready with a "recorded so far" tag |
| `"failed"` | 2 or more failed attempts, retrying | "Clipping isn't working on this stream just now" |
| `"unsupported"` | Not H.264, or has B-frames | "This stream's video format can't be clipped yet" |
| `"full"` | Disk low, or the ring budget is used up | "The server is out of room for clipping just now" |
| `"queued"` | Over the recorder limit | "Clipping is busy with other streams right now" |
| `"off"` | The streamer has switched clipping off | "Dave has switched clipping off" |

The field is absent when the stream cannot be recorded at all: Clips off, a 24/7 channel, or no storage. The reason detail is admin-only (§2.10.1).

#### 2.4.7 Hiding the recorder from VillaNET's viewers and watchers

In `decorate`, immediately after `counted := stills.counted(source.Sessions, now)`:

```go
counted = p.recorders().withoutOwn(counted) // drops sessions whose ID is in the recorder registry
```

`Viewers: len(counted)` and `p.watchersOf(stream.Handle, counted)` both use the filtered list, so the recorder never adds 1 to VillaNET's count or to the "Others" stranger count.

**Broadcast Box still counts it.** BB's `session.GetStreamStatus` sets `ViewerCount: whepSessionsCount` from `len(s.WHEPSessions)` (`internal/webrtc/sessions/session/session.go`). BB's player and its broadcast page (`StreamStatus.tsx`, `currentViewerCount`) show that number. The README and LOCKDOWN say so (§4).

#### 2.4.8 Tails

**When a tail is made.** A tail is the frozen, read-only index and segment files of an epoch that ended:
- **`ended`**: the recorder stopped because the stream left the snapshot (§2.4.5 step 5), or after the no-video watchdog.
- **`dropped`**: a cut — excess-lag discontinuity, SPS/PPS or codec change, audio died, or disk too slow.
- **None** for: Clips off, wipe, opt-out, AlwaysOn, keyframes too rare, disk low, or an epoch shorter than 5 s.

**Rules:**
- One tail per handle. A newer tail replaces the older one, which is deleted at once, or when its pins are released.
- At most `maxTails` (4). Making a fifth deletes the oldest.
- When rings and tails together exceed `ringBudgetBytes`, tails are deleted oldest-first.
- A tail lives for `tailKeep` (10 min) after its `end`: the wall time at the end of its last video sample.
- A tail keeps, in memory only:
  - the stream key, never logged or sent, used only for title redaction;
  - the handle;
  - title, `UserID`, `LinkSource` and MOTD as captured;
  - `end` and `reason`.
- A draft cut from a tail uses the window `[end − L, end]`.

#### 2.4.9 The viewer registry and the watch gate (`clips_access.go`)

The registry exists **only for access control**. It is never shown, logged or returned, and it is kept apart from the watcher store.
- `noteViewer(sessionID, userID, handle, now)` is called from `handleWHEP` for non-peek sessions while Clips is on. It records `{userID, handle, openedAt}`.
- `noteSessions(raw, now)` runs on each successful poll. It sets `lastSeen = now` for every recorded session BB still lists under that handle, and stores `lastPollAt = now`.
- Entries are dropped `viewerMemory` (15 min) after `max(openedAt, lastSeen)`. Everything is dropped when Clips is switched off or the plugin deactivates.

> **As built (3.0.0).** `handleWHEP` notes every non-peek session **whether or not Clips is on**, and switching Clips off does **not** clear the registry; only the plugin stopping does, and each entry still goes 15 minutes after it was last known to be open. A panel that is already playing never offers again, so a registry kept only while Clips was on refused everybody already watching, with `not_watching`, when an admin switched Clips on or back on (`TestWatchingBeforeClipsIsOnStillCounts`). Nothing reads it while Clips is off. The registry is bounded at 4096 sessions, the stalest making room. The webapp also covers what the server cannot, a plugin restart: a live draft refused with `not_watching` while its tile is playing renews that tile's session once (`sessions.ts` `renewSession`) and asks again before showing the refusal (`clipLogic.ts` `renewBeforeRefusing`).

**`mayClip(userID, handle, source)`** allows the request when any of these holds:
1. The user is the handle's **manually linked streamer**: the live stream's, or the tail's captured `UserID` with `LinkSource == "manual"`.
2. `source == "live"`, and the user has a session on that handle that was listed in the latest successful poll (`lastSeen == lastPollAt`), or was opened in the last `viewerGrace` (60 s).
3. `source == "tail"`, and the user has a session on that handle with `max(openedAt, lastSeen) >= tail.end − max(60 s, 2 × PollInterval)`. In other words, they were watching when it ended.

Otherwise the answer is 403 `not_watching`.

If BB returns no session id (BB v2.0.2 always sends `Location: /api/whep/<uuid>`), only the 60 s rule can apply. This is logged once at Warn.

### 2.5 The ring (`clips_ring.go`)

```go
type ringSample struct {
    wall  int64  // unix ns: arrivalClock mapping of this sample's RTP time
    ticks int64  // unwrapped RTP ts relative to the epoch base, track clock (90000 / 48000)
    dur   uint32 // track ticks, = next.ticks - ticks
    seg   uint32 // segment seq
    off   uint32 // payload offset in segment file
    size  uint32
    key   bool   // video only: contains NAL type 5
}
type ringSegment struct {
    seq   uint32
    f     *os.File // O_RDWR|O_CREATE|O_TRUNC, 0600; payloads appended, no headers
    start int64    // wall ns of its first (key) video sample
    bytes int64
    pins  int
}
type ring struct {
    mu             sync.Mutex
    dir            string // <root>/ring/<handle>/<epoch>
    epoch          int
    epochStart     int64 // wall ns of the epoch's first IDR
    sps, pps       []byte
    width, height  int
    codecs         string // "avc1.64001f,opus" (from SPS bytes 1..3, lowercase hex)
    video, audio   []ringSample
    pending        [2]*pendingSample // duration is known only when the next sample arrives
    segs           []*ringSegment
    bytes          int64
    window         time.Duration // ringWindow()
    capBytes       int64
    measuredBps    float64       // writer's 10 s average
    truncatedUntil int64         // wall ns; see eviction
    frozen         bool          // a tail: no appends, no eviction
    hasAudio       bool
}
```

**Appending.** The writer applies `addVideo(op)` and `addAudio(op)`.
- A sample is written to disk (`f.Write`, no fsync) when the *next* sample of the same track arrives, because only then is its `dur` known.
- **Gap freeze.** A video op marked `afterGap` gives the previous video sample a duration covering the gap: `dur = op.ticks − prev.ticks`. The video freezes there. The reader has already dropped non-IDR AUs after the gap until the next IDR (§2.6).
- Audio samples whose wall time is before `epochStart` are discarded. Audio gaps are kept as long durations and filled with silence at export (§2.7).

**Segment rotation.** A new segment starts on a video key sample once the current segment is at least `ringSegmentTarget` old by wall time. A segment therefore always starts on a keyframe and holds one or more GOPs.

**Cap.** On each rotation:

```
capBytes = min(ringMaxBytes, (window + ringSlack) × max(ringFloorBytesPerSecond, ringBitrateHeadroom × measuredBps))
```

At epoch start, before 10 s have been measured, use BB's reported `apiStream.Bitrate` ÷ 8 in place of `measuredBps`.

**Eviction** runs after each append, never on a pinned segment, never on a frozen ring:
- While `len(segs) >= 2 && segs[1].start <= now − window`, evict `segs[0]`. Nothing in it can start a draft.
- While `bytes > capBytes && len(segs) >= 2`, evict `segs[0]`. **If that segment's last sample is after `now − window`**, set `truncatedUntil = now + window` and record the admin reason "bitrate above N Mbit/s (clips shorter than L)".
- A single segment over `capBytes` means keyframes are too rare (§2.4.4).
- Eviction removes the matching prefix of `video` and `audio`, closes and removes the file, and subtracts its bytes.

**Freezing a tail.** Under `mu`:
1. Flush the pending samples with their last known duration.
2. If the cut names a sample, truncate the index after it. Bytes past that point stay in the segment file unused.
3. Set `frozen = true`.
4. Hand the ring to the manager's tail map. The recorder then starts a fresh ring in a new `<epoch>` directory.

**Deleting a ring or tail** closes and removes every unpinned segment. Pinned segments are removed when their pins are released. If the ring was a tail, it leaves the tail map.

**`snapshotRange(at time.Time, L time.Duration) (rangeSnap, error)`** runs under `mu`. For a live ring, `at` = the request's `clickAt`; for a tail, `at` = `tail.end`.
- **Start.** `vStart` is the first video sample with `key && wall >= at − L && wall <= at`. If there is none, or `at − epochStart < clipMinMS`, return `errWarmingUp`.
- **End.** `vEnd` is the last video sample already written with `wall < at`; pending samples are excluded. Require `sum(dur over vStart..vEnd) >= 5000 ms`, otherwise `errWarmingUp`.
- **Audio.** From the first audio sample with `wall >= wall(vStart) − 10 ms` to the last audio sample with `wall < wall(vEnd) + dur(vEnd)`.
- **Flags.**
  - `partial = epochStart > at − L`: the epoch began inside the window.
  - `truncated = truncatedUntil > at`, excluding `partial`.
- Return copies of the index slices, SPS/PPS, dimensions, codecs, the handle and the epoch, and **pin** every segment touched. The caller must `release()`, which unpins and then runs any deferred eviction or removal.
- The copy holds `mu` for at most a few milliseconds: about 24,000 entries at 5 min.

### 2.6 H.264, Opus and timing (`clips_media.go`)

**One helper for milliseconds** is used by the ring, the draft JSON, export and trim:

```go
func ticksToMs(ticks int64, timescale uint32) int64 { return ticks * 1000 / int64(timescale) } // floor
```

Draft durations and trim boundaries are always computed from **cumulative ticks**, never by summing per-sample milliseconds.

**Arrival map** (per track, in the reader):
- `note(ts, arr)` keeps the earliest arrival ns for each RTP timestamp.
- `take(ts)` returns and deletes it.
- If the map exceeds 4096 entries, clear it. A missing lookup then falls back to `time.Now()` at pop, which is conservative: later than the truth.
- This is needed because pion's `media.Sample` carries no arrival time. Its fields are `Data, Timestamp, Duration, PacketTimestamp, PrevDroppedPackets, Metadata, RTPHeaders`.

**`arrivalClock`**, one per track per epoch:

```go
type arrivalClock struct {
    rate        float64 // 90000 or 48000
    started     bool
    last        uint32
    ext         int64 // unwrapped ticks since the epoch's first sample
    anchored    bool
    offsetNs    int64 // wall(ext) = offsetNs + ext*1e9/rate
    anchorUntil int64 // arrival ns; the offset may move earlier only before this
}
func (c *arrivalClock) unwrap(ts uint32) int64 {
    if !c.started { c.started, c.last, c.ext = true, ts, 0; return 0 }
    c.ext += int64(int32(ts - c.last)); c.last = ts; return c.ext
}
func (c *arrivalClock) anchor(ext, arrivalNs int64) {
    cand := arrivalNs - int64(float64(ext)/c.rate*1e9)
    if !c.anchored { c.offsetNs, c.anchored, c.anchorUntil = cand, true, arrivalNs+3e9; return } // first sample sets it
    if arrivalNs < c.anchorUntil && cand < c.offsetNs { c.offsetNs = cand } // earliest arrival = least delayed
}
func (c *arrivalClock) wall(ext int64) int64 { return c.offsetNs + int64(float64(ext)/c.rate*1e9) }
```

A/V alignment comes only from these wall times. The residual skew measured in the research was a few milliseconds, constant, with no drift.

**Checks on each popped video sample**, in the reader. Here `dTicks = ext − prevExt` and `lag = arrival − wall(ext)`.

| Condition | Meaning | Action |
|---|---|---|
| `dTicks < 0 && dTicks > −90000` | B-frames: decode order is not presentation order | State `unsupported` with the B-frame reason (§2.4.4) |
| `dTicks <= −90000` or `dTicks == 0` repeatedly | Timestamps broke | Cut |
| `dTicks > gapMin` (90000) and the excess-lag rule below does not fire | The publisher stalled and the clocks agree | Mark the next forwarded sample `afterGap`. Drop non-IDR AUs until an IDR. If none arrives within `maxKeyframeGap`, treat it as keyframes too rare. |

**The excess-lag rule.** Keep `base` = the minimum `lag` over the last 60 s of video samples, in a monotonic deque. Then `excess = lag − base`:
- `excess < −excessLimit` on any sample means timestamps jumped forward with no matching wait. **Cut now.**
- `excess > excessLimit` on every sample across `excessHold` of arrival time means a timestamp rewrite: BB kept the WHEP session across an OBS reconnect, and the measured case was rtp 0 s against wall 5.6 s. The stream resumes at normal pace, so the excess never drains. **Cut**, at the last sample before the late stretch began; those ≤ 2 s are lost.
- A late burst after a network stall drains within about a second, so it is not a discontinuity.
- A genuine publisher pause moves RTP and arrival together, so its excess is about 0 and the epoch is kept with a freeze.
- A reconnect faster than 2 s with no audio would be missed. Its only effect is that the window ends up to 2 s before the click. With audio, the audio-died watchdog catches it.

**Video access units.**
- `samplebuilder.New(1024, &codecs.H264Packet{}, 90000, samplebuilder.WithMaxTimeDelay(time.Second))`.
- For each popped sample, `var au h264.AnnexB; au.Unmarshal(sample.Data)`, using mediacommon `pkg/codecs/h264`.
  - NAL type 7 is SPS and 8 is PPS. Drop 9 (AUD) and 12 (filler). Keep everything else.
  - `key` = the AU contains type 5.
  - While waiting for a key at the start of an epoch, drop non-key AUs and key AUs that came before any SPS/PPS.
- Payload is `h264.AVCC(keptWithoutSPSPPS).Marshal()`. Parameter sets go only in `avcC`.
- Width and height come from `h264.SPS.Unmarshal(sps)`, then `Width()` and `Height()`.
- A frame the samplebuilder drops for loss is simply missing; decoders conceal it.

**Audio.**
- `samplebuilder.New(64, &codecs.OpusPacket{}, 48000, samplebuilder.WithMaxTimeDelay(500*time.Millisecond))`.
- One sample is one Opus packet. Durations come from RTP differences.
- The last sample in a range gets the previous sample's duration, or 960 if unknown.

### 2.7 Export and trim (`clips_mux.go`): mediacommon v2.9.5 `pkg/formats/pmp4`

**`exportDraft(src *ring, clickAt time.Time, L time.Duration, draftID string, ctx) (*draft, error)`**

1. `snap, err := src.snapshotRange(at, L)`, where `at` = `clickAt` for a live ring and `tail.end` for a tail. **This happens before waiting for a slot**, so the window is the moment of the click. `defer snap.release()`.
2. Acquire an export slot. If none frees within `exportWait`, or `ctx` is cancelled, return `errBusy`.
3. Build the presentation:

```go
v := &pmp4.Track{ID: 1, TimeScale: 90000, Codec: &mp4codecs.H264{SPS: snap.sps, PPS: snap.pps}} // TimeOffset 0 ALWAYS
for _, s := range snap.video {
    s := s
    v.Samples = append(v.Samples, &pmp4.Sample{Duration: s.dur, IsNonSyncSample: !s.key, PayloadSize: s.size,
        GetPayload: func() ([]byte, error) { b := make([]byte, s.size); _, err := snap.file(s.seg).ReadAt(b, int64(s.off)); return b, err }})
}
a := &pmp4.Track{ID: 2, TimeScale: 48000, Codec: &mp4codecs.Opus{ChannelCount: 2}} // omitted if no audio samples
pres := pmp4.Presentation{Tracks: []*pmp4.Track{v, a}}
```

4. **Audio silence fill.** For each audio sample:
   - `pd := opus.PacketDuration2(payload)`, in 48 kHz ticks from mediacommon `pkg/codecs/opus`; use 960 if it returns 0.
   - If `s.dur > pd + 480`: let `n = (s.dur − pd) / 960`. Write the sample with `Duration = s.dur − n×960`, then `n` samples of `[]byte{0xF8, 0xFF, 0xFE}`, each with `Duration: 960`.
   - `F8 FF FE` is a 20 ms CELT packet that decodes to digital silence. It was verified in this research with ffmpeg: peak 0 across a 3 s fill, and the decoded audio timeline was 10.01 s, against 7.01 s when the gap was left as one long duration.
5. Write `drafts/.tmp-<id>-<rand>.mp4` (`O_EXCL`) through `bufio.NewWriterSize(f, 1<<20)`. `Flush`, `Close`, then `Rename` to `drafts/<id>.mp4`. On any error, remove the temp file.
6. The draft records:
   - `durationMs = ticksToMs(Σ video dur, 90000)`
   - `startAt = wall(vStart) / 1e6`
   - `requestedMs = L × 1000`
   - `partial`, `truncated`, `bytes`, `width`, `height`, `codecs`
   - `source` ("live" or "tail"), the handle, and the streamer info captured at the time

**`trimClip(draftPath, id string, inMs, outMs int64) (trimResult, error)`**

1. Open the draft. `var p pmp4.Presentation; p.Unmarshal(f)`. Payloads stay lazy, so keep `f` open until Marshal returns.
2. Video: compute `start(i) = ticksToMs(cumBefore(i), 90000)` and `end(i) = ticksToMs(cumBefore(i) + dur(i), 90000)`.
   - If `inMs < 0 || inMs >= outMs || outMs > duration + 50`: `errBadRange`.
   - `kIn` = the last sync sample with `start(kIn) <= inMs`. **`leadMs = inMs − start(kIn)`.**
   - `kOut` = the last sample with `start(kOut) < outMs`. OBS sends no B-frames, and B-frame streams are refused, so this is frame-exact.
   - `durationMs = end(kOut) − inMs`. If `durationMs < clipMinMS`: `errTooShort`.
3. Audio: samples with `start >= start(kIn) − 10` and `start < end(kOut)`, using `ticksToMs(cum, 48000)`. Silence is already filled.
4. **Full range.** If `kIn == 0 && kOut == last`, return `{rename: true, leadMs, durationMs}`. The caller renames the draft; no re-mux.
5. Otherwise build a new Presentation that reuses the same `*Sample` pointers, with `TimeOffset` 0. Marshal through bufio to `clips/.tmp-<id>-<rand>.mp4` (`O_EXCL`), close without fsync, and `os.Rename` to `clips/<id>.mp4`. Remove the temp file on any error.
6. Return `durationMs`, `leadMs`, `bytes` and `inMs`.

**Codec string:** `fmt.Sprintf("avc1.%02x%02x%02x", sps[1], sps[2], sps[3]) + ",opus"`, without `,opus` when there is no audio.

### 2.8 The KV document

There is one document, `clips`. Every write goes through `p.clipsCAS(mutate)`:
1. `casUpdate(kvClips, apply)`, which compares on the raw bytes.
2. Inside `apply`: decode, call `mutate(doc, now)`, recompute the holder (§2.9), `rev++`, encode, and refuse with `clip_cap` if the result is over `maxClipsDocBytes`.
3. A mutate that changes nothing returns nil and writes nothing.

`apply` makes no API calls; eligibility comes from a cache filled before the CAS (§2.9).

```json
{"v":1, "rev":4182,
 "clips":[{
   "id":"3f9a0c2e7b1d4a55",
   "clipperId":"<userId>",
   "streamerId":"<userId>",          // omitempty: stream not attributed
   "streamerLink":"manual",          // "manual" | "username" | omitted (apiStream.LinkSource)
   "streamName":"Dave",              // apiStream.Title at capture (never a key; unnamed = "Stream ab12")
   "streamNote":"Arma 3 ops night",  // omitempty; apiStream.MOTD at capture (already redacted)
   "handle":"<32 hex>",
   "title":"that jump",              // may be ""
   "createdAt":1790000000000,        // publish instant, ms
   "startAt":1789999950000,          // wall ms of the EXACT in-point
   "durationMs":45000,               // exact: out − in
   "leadMs":1400,                    // file starts this much before startAt (omitempty when 0)
   "width":1280, "height":720,
   "codecs":"avc1.64001f,opus", "bytes":33554432,
   "thumb":"a1b2c3d4",               // omitempty: first 8 hex of sha256(jpeg)
   "voters":{"<userId>":1790000100000},   // omitempty; value = vote instant ms
   "reports":{"<userId>":1790000200000}   // omitempty
 }],
 "banked":{"<userId>":{"up":12,"clips":3,"lastUpAt":1790000000000}},
 "holder":{"userId":"<userId>","since":1790000000000},
 "blocked":{"<userId>":{"by":"<adminId>","at":1790000000000}},
 "optOut":{"<userId>":1790000000000},
 "resets":{"<userId>":{"by":"<adminId>","at":1790000000000}},
 "retention":{"days":30,"prevDays":0,"changedAt":0}
}
```

- Entries are appended, newest last. Appending an `id` that is already present is a no-op.
- **`expiresAt` is not stored.** `clipExpiresAt(createdAt, effectiveRetention(doc.retention, cfg.ClipRetentionDays, now))` is a pure function (§2.13).
- File names derive from the id. No path is stored.

**Cache.** `clipsStore` holds the decoded doc and its raw bytes.
- It accepts a doc only if its `rev` is greater than the cached `rev`. Sources are the bytes each CAS wrote, the sweep's reads, and a re-read on any access when the cache is more than 60 s old.
- The snapshot and GET routes read the cache.
- **Blocks and opt-outs are read fresh** before every draft and publish, the same rule as `mayShareStills`, and they **fail closed**: if they cannot be read, the code is `store`.

**Unreadable document.** If `clips` will not decode:
- Set `halted = "the clips document could not be read"`, log Error once, and never overwrite it.
- While halted there are no drafts, publishes, votes, reports, deletes, sweeps or orphan deletion. GETs serve the last good cache, if any.
- `/me.clipsAlert` and AdminClips show it. Recovery is manual: an admin fixes or deletes the key.

### 2.9 Votes, settling and Best Clipper

**Eligibility.** `eligible(u)` is true when all of these hold:
- `GetUser` succeeds,
- `DeleteAt == 0`,
- `!IsBot`,
- `u` is not in `doc.blocked`.

User lookups are cached for 10 min. Before every CAS, fetch any user id in the doc that is not cached. A transient lookup error keeps the last known verdict; a user never seen before is treated as eligible until known.

**Counted votes.**

```
countedVotes(entry, doc) = |{ v ∈ entry.voters : eligible(v) && (doc.resets[entry.clipperId] absent || entry.voters[v] > resets.at) }|
```

The upvote count shown on a clip is the number of eligible voters. Resets affect only the Best Clipper score, not the displayed count.

**Vote** (`POST /clips/vote?id=` with `{"up":true|false}`, which is the desired state, never a toggle):
- Before the CAS, refuse bots with `forbidden`. Blocked users may still vote.
- Mutate:
  - The clip is missing, or `now >= expiresAt`: `clip_gone`.
  - `clipperId == voter`: `own_clip`.
  - If up and not already present, set `voters[voter] = now`. If down, delete it.
  - If nothing changed, return no change, which answers 200 with the current state.

**Settle** (`reasons map[id]reason`) is **one CAS**. For each id still present:
1. If the reason is `expired` or `streamer`:
   - `banked[clipper].up += countedVotes(entry)`
   - `banked[clipper].clips += 1`
   - `lastUpAt = max(lastUpAt, latest counted vote)`
2. Remove the entry.

After the CAS succeeds:
- `os.Remove` `clips/<id>.mp4` and `.jpg`, ignoring not-exist, and subtract usage.
- A crash between the CAS and the removal leaves orphan files, which the sweep removes (§2.13).

Ids already gone are skipped, so settle is idempotent.

**Scoring** (pure functions, unit-tested; "live" means entries in `doc.clips`):

```go
type clipperScore struct{ up, clips int; reachedAt int64 }
// score[u].up       = banked[u].up + Σ_{live entries by u} countedVotes(entry)
// score[u].clips    = banked[u].clips + #live entries by u
// score[u].reachedAt = max(banked[u].lastUpAt, latest counted vote on u's live entries)
func pickBestClipper(scores map[string]clipperScore, holder string, eligible func(string) bool) string
```

**The rule:**
- Candidates are eligible users with `up >= minBestClipperUpvotes` (1).
- If the holder is a candidate and `up(holder) == maxUp`, **the holder keeps it**.
- Otherwise, among `up == maxUp`, the earliest `reachedAt` wins, then the smallest userId.
- With no candidates, nobody holds it.
- Expired clips that have not been swept yet count as live; banking gives the same result.

**Holder recompute** happens inside every `clipsCAS`. If the winner differs from `doc.holder.userId`, set `holder = {winner, now}` or remove it. After a successful write, log Info `"Best Clipper changed" from=… to=… upvotes=…`. Because the holder lives in the same bytes as the votes, a recompute can never see a half-settled state.

Also recompute through a no-op `clipsCAS` (it writes only if the holder changes):
- on activate,
- after each sweep, which also catches deactivations,
- after a block, unblock or score reset.

**Deletion rules:**

| Who deletes | Allowed when | Votes |
|---|---|---|
| The clipper | Always | Void (stop counting) |
| The streamer | `streamerId == user && streamerLink == "manual"`. Username matching is not trusted, as with `claimedToken`. | Banked: the clipper keeps them |
| System admin | `isSystemAdmin` | Void |
| Expiry | `now >= expiresAt` | Banked |

When more than one applies, the reason is chosen in this order: admin (when the requester is an admin and not the clipper), then clipper, then streamer.

**Duplicate check** at publish: refuse with 409 `duplicate` when a live entry has the same `clipperId` and `handle` and

```
|[s1, s1+d1] ∩ [s2, s2+d2]| / min(d1, d2) > duplicateOverlap
```

where `s` is `startAt` and `d` is `durationMs`.

**Score reset** (admin): `banked[u] = {0, 0, 0}` and `resets[u] = {by, at: now}`. Votes cast on u's live clips before `at` stop counting toward the score. Log Info with `admin_id` and `user_id`.

### 2.10 HTTP API

Everything sits under `/plugins/ceo.villa.villanet`. Existing auth applies: `Mattermost-User-Id`, otherwise 401.
- JSON responses carry `Cache-Control: no-store`.
- Errors are `{"error":"<fixed text>","code":"<code>"}`, written by `writeClipError(w, status, code)`. The texts are fixed (§2.10.7), never upstream text.
- A wrong method gets 405 "Method not allowed." (existing).
- Ids must match `^[0-9a-f]{16}$` and handles `^[0-9a-f]{32}$`, otherwise 400 `bad_request`.
- Routes use the query-parameter style of the existing switch.

**Rate limiters**, created in `OnActivate`:

```go
p.clipCaptureLimiter = newRateLimiter(5, 10*time.Minute)  // spent after the cheap checks; refunded on warming_up/busy/recording_failed
p.clipPublishLimiter = newRateLimiter(10, time.Hour)      // spent after the cheap checks; refunded on busy/store
p.clipVoteLimiter    = newRateLimiter(20, time.Minute)
p.clipWriteLimiter   = newRateLimiter(30, time.Minute)    // deletes, thumbs, reports, streamer settings, wipes, admin writes
```

There is no limiter on the video, thumbnail or list GETs: one playback makes many Range requests.

| Method + path | Who | Limiter | Request | Success | Errors |
|---|---|---|---|---|---|
| `GET /api/v1/clips` | Anyone signed in | — | — | 200 `{"clips":[ClipView]}`, newest first, excluding expired | 503 `store` (halted, no cache) |
| `GET /api/v1/clips/clip?id=` | Anyone | — | — | 200 `ClipView` | 400, 410 `clip_gone` |
| `DELETE /api/v1/clips/clip?id=` | Clipper, manual streamer, admin | write | — | 200 `{"ok":true}` (also if already gone) | 403 `not_allowed`, 429, 503 `store` |
| `GET /api/v1/clips/video?id=` | Anyone | — | Range | 200/206 `video/mp4` (§2.11) | 404, 410 `clip_gone` |
| `GET /api/v1/clips/thumb?id=&v=` | Anyone | — | — | 200 `image/jpeg` via `writeImage`; `private, max-age=86400` when `v` matches, else `no-cache` | 404 |
| `POST /api/v1/clips/vote?id=` | Anyone, not a bot | vote | `{"up":bool}` | 200 `{"upvotes":n,"voted":bool}` | 403 `own_clip`/`forbidden`, 410, 429, 503 `store` |
| `POST /api/v1/clips/report?id=` | Anyone, not a bot, not the clipper | write | — | 200 `{"ok":true}` (idempotent) | 403, 410, 429, 503 |
| `GET /api/v1/clips/drafts` | Self | — | — | 200 `{"drafts":[Draft]}`, own only | — |
| `POST /api/v1/clips/drafts` | Anyone, subject to the watch gate | capture | `{"handle":"<32hex>","source":"live"\|"tail"}` | **201** `Draft` | See the order below |
| `DELETE /api/v1/clips/draft?id=` | Owner | write | — | 200 `{"ok":true}` (idempotent) | 409 `publishing` |
| `GET /api/v1/clips/draft/video?id=` | Owner only | — | Range | 200/206 `video/mp4` | 404 `draft_gone` (unknown or not yours), 410 `draft_gone`/`draft_wiped` |
| `PUT /api/v1/clips/draft/thumb?id=` | Owner | write | Raw `image/jpeg`, ≤ 200 KB, validated by `checkStill` | 200 `{"ok":true}` | 400 `bad_thumb`, 413, 404, 410, 409 `publishing` |
| `POST /api/v1/clips/draft/publish?id=` | Owner | publish | `{"title":string,"inMs":int,"outMs":int}` | **201** `ClipView`, or **200** `ClipView` when already published | See the order below |
| `PUT /api/v1/clips/streamer` | A manually linked user | write | `{"allow":bool}` | 200 `{"allowed":bool}` | 403 `forbidden` (not manually linked), 503 `store` |
| `POST /api/v1/clips/wipe?handle=` | That handle's manual streamer, or an admin | write | — | 200 `{"ok":true,"draftsRemoved":n}` | 403 `not_allowed` |
| `GET /api/v1/admin/clips` | Admin | — | — | 200 `AdminClips` | 403 |
| `POST /api/v1/admin/clips/block` | Admin | write | `{"userId":string}` | 200 `{"ok":true,"draftsRemoved":n}` | 403, 400 |
| `DELETE /api/v1/admin/clips/block?userId=` | Admin | write | — | 200 `{"ok":true}` | 403 |
| `POST /api/v1/admin/clips/reset-score` | Admin | write | `{"userId":string}` | 200 `{"ok":true}` | 403, 400 |
| `DELETE /api/v1/admin/clips/thumb?id=` | Admin | write | — | 200 `{"ok":true}` | 403, 410 |
| `DELETE /api/v1/admin/clips/reports?id=` | Admin | write | — | 200 `{"ok":true}` | 403, 410 |

#### 2.10.1 Draft creation: checks in order

`clickAt := time.Now()` is taken **when the request arrives**, before anything else.

1. Validate the body (≤ 1 KiB, handle, source): 400 `bad_request`.
2. `EnableClips` off: 403 `clips_off`.
3. `problem` set, or halted: 503 `storage_unavailable` or `store`.
4. Fresh doc read: the caller is blocked, 403 `blocked`; unreadable, 503 `store`.
5. Resolve the source in the recorder manager, never through `streamKeyForHandle`:
   - `live`: the handle must be in the current snapshot and not `AlwaysOn`, otherwise 404 `not_live`.
   - `tail`: a tail must be held for the handle with `until > now`, otherwise 410 `tail_gone`.
6. The streamer has opted out: 403 `streamer_off`.
7. `mayClip` (§2.4.9): 403 `not_watching`.
8. Recorder state, for `live` only:
   - `starting`: 409 `warming_up`.
   - `failed`, `full` or `queued`: 503 `recording_failed`.
   - `unsupported` or `off`, or no recorder: 409 `not_clippable`.
9. Draft caps (3 per user, 20 in total): 409 `draft_cap`.
10. Free-space floor, or usage + estimate over the limit: 507 `storage_full`. The estimate is `L × measuredBps`.
11. **Take a capture token**: 429 `rate_limited` if there is none.
12. `snapshotRange(at, L)`, which pins. `errWarmingUp`: **refund** the token, 409 `warming_up`.
13. Wait for an export slot (§2.7): `errBusy` means release, refund, 503 `busy`. Any IO error means release, refund, 503 `recording_failed`.
14. Export, then 201.
15. Log Info "A clip draft was made" with `user_id`, `draft_id`, `stream_handle`, `source`, `duration_ms`.

The draft keeps server-only fields:
- The stream key (`json:"-"`): held in memory only for title redaction, never logged.
- Streamer id, link source, name and note at capture time.
- Owner, `state`, and a `cancel` func.

**Draft states**, changed only under `drafts.mu`:

| State | Meaning |
|---|---|
| `ready` | Can be edited, deleted, given a thumbnail or published |
| `publishing` | A publish is running. Delete, thumb, wipe-discard and TTL expiry wait or skip; a second publish gets 409 `publishing`. |
| `published{clipId}` | Kept as a tombstone for `draftTombstone` |
| `expired` / `wiped` | Kept as a tombstone for `draftTombstone` |

#### 2.10.2 Publish: checks in order

1. Validate the id and a body ≤ 8 KiB of JSON: 400.
2. Look up the draft under `drafts.mu`:
   - Unknown, or another user's: 404 `draft_gone`.
   - Tombstone `expired`: 410 `draft_gone`. Tombstone `wiped`: 410 `draft_wiped`.
   - `published{clipId}`: **200 with the current `ClipView`**, or 410 `clip_gone` if it has since gone.
   - `publishing`: 409 `publishing`.
3. `EnableClips` off: 403 `clips_off`.
4. Fresh doc read: blocked, 403 `blocked`; unreadable, 503 `store`.
5. Title, in this order:
   1. `cleanText(title, maxClipTitle)`
   2. `p.withoutOwnKey(user, title)`
   3. `redactStreamKey(title, draft.streamKey, "the stream")`
   4. `cutRunes` to 80 again

   An empty title is allowed.
6. Range, using the draft's duration (§2.7): 400 `too_short` or `bad_range`.
7. Duplicate check (§2.9): 409 `duplicate`.
8. Caps: the caller has 30 live clips, or there are 500 in total: 409 `clip_cap`.
9. Estimate the bytes as `draft.bytes × (outMs − start(kIn)) / durationMs`.
   - The caller's live clip bytes plus the estimate exceed their share: 507 `user_storage_full`.
   - Total usage plus the estimate exceeds `ClipStorageLimitGB`, or the free-space floor would be crossed: 507 `storage_full`.
10. **Take a publish token**: 429 `rate_limited` if there is none.
11. **Set `state = publishing`** under `drafts.mu`. This is the gate. If the state changed since step 2, refund the token and answer as step 2 would.
12. Export slot. On `errBusy`: state back to `ready`, refund, 503 `busy`.
13. `trimClip`, or for a full range `os.Rename(drafts/<id>.mp4, clips/<id>.mp4)`. The clip id is the draft id. Remember what this request did, `{renamed | trimmed}`.
14. If `drafts/<id>.jpg` exists, rename it to `clips/<id>.jpg` and set `thumb`. Remember that it was moved.
15. `clipsCAS`: append the entry, a no-op if the id is present, and recompute the holder.
16. **On any failure in steps 13–15, undo only what this request did:**
    - `renamed`: rename `clips/<id>.mp4` back to `drafts/<id>.mp4`.
    - `trimmed`: remove `clips/<id>.mp4`.
    - Thumb moved: rename it back.

    Then state back to `ready`, refund the token, and answer 503 `store`, or `busy` for a slot failure. The draft is still playable and can be published again.
17. **On success:**
    - Remove `drafts/<id>.mp4` if it still exists (the trim case) and any leftover `drafts/<id>.jpg`, ignoring not-exist. Subtract the draft's bytes from usage and add the clip's.
    - `state = published{id}`.
    - `p.republishClips()`.
    - Log Info "A clip was published" with `user_id`, `clip_id`, `stream_handle`, `duration_ms`, `bytes`, `lead_ms`.
    - Answer 201.

Blocking a user while their publish runs does not stop it; the block was checked when the publish started.

#### 2.10.3 JSON shapes

```jsonc
// ClipPerson
{"userId":"u1","username":"bob","displayName":"Bob","name":"Bob"}  // name = what to print; unattributed stream: only "name"
// ClipView
{"id":"3f9a0c2e7b1d4a55","title":"that jump",
 "createdAt":1790000000000,"expiresAt":1792592000000,"startAt":1789999950000,
 "durationMs":45000,"leadMs":1400,"width":1280,"height":720,"codecs":"avc1.64001f,opus","bytes":33554432,
 "thumb":"a1b2c3d4",                      // omitted when none
 "clipper":ClipPerson,"streamer":ClipPerson,"streamNote":"Arma 3 ops night",
 "upvotes":14,"voted":true,"mine":false,"ofMyStream":false,
 "canVote":true,"canDelete":false,"canReport":true,"reported":false,
 "reports":0}                             // "reports" only in AdminClips
// Draft
{"id":"…","handle":"…","source":"live","state":"ready","createdAt":ms,"expiresAt":ms,"startAt":ms,
 "durationMs":118000,"requestedMs":120000,"partial":false,"truncated":false,
 "width":1280,"height":720,"codecs":"avc1.64001f,opus","bytes":88000000,"hasThumb":false,
 "streamer":ClipPerson,"streamNote":"…","clipId":"…"}   // clipId only when state=published
// AdminClips
{"problem":"","halted":"","root":"/opt/mattermost/data/ceo.villa.villanet",
 "usedBytes":0,"limitBytes":21474836480,"shareBytes":4294967296,"freeBytes":0,"totalBytes":0,"ringBytes":0,
 "recorders":[{"handle":"…","title":"Dave","state":"ready","heldSeconds":120,"bitrate":6000000,"truncated":false,"reason":""}],
 "tails":[{"handle":"…","title":"Dave","reason":"dropped","endedAt":ms,"until":ms,"heldMs":118000}],
 "drafts":2,
 "clips":[ClipView],                      // canDelete true for all; reported ones first
 "people":[{"userId":"…","username":"…","displayName":"…","clips":4,"bytes":180000000,"upvotes":31,"banked":12}],
 "blocked":[{"userId":"…","username":"…","displayName":"…","blockedAt":ms,"by":"<adminId>"}],
 "retention":{"days":3,"pendingRemovals":41,"removalAt":ms},  // pendingRemovals 0 when nothing waits
 "bestClipper":BestClipperView|null}
```

- Names are resolved at read time with `GetUser` and `displayNameOf`, using a per-request cache, so a rename shows everywhere.
- `canVote = !mine && !viewerIsBot`. `canDelete` follows the deletion table in §2.9. `canReport = !mine && !viewerIsBot`.
- `ofMyStream` is true when `streamerId == viewer && streamerLink == "manual"`.
- `recorders[].reason` is admin-only free text, one of:
  - the ICE or WHEP failure,
  - "Broadcast Box URL resolves to no IPv4 address",
  - "not H.264 (AV1)",
  - "B-frames (set B-frames to 0; do not use the NVENC UHQ tune)",
  - "recorder limit",
  - "disk low",
  - "disk too slow",
  - "keyframes too rare",
  - "bitrate above N Mbit/s (clips shorter than 2:00)",
  - "the streamer switched clipping off".

#### 2.10.4 Blocking

Blocking a user:
- Discards their `ready` drafts and deletes the files.
- Refuses their new drafts and publishes.
- Makes them ineligible for Best Clipper.
- Leaves their published clips alone; admins delete those one by one.
- Logs Info with `admin_id` and `user_id`.

#### 2.10.5 Reports and pictures

- `POST /clips/report` sets `reports[user] = now` on the entry.
- Admins see reported clips first in AdminClips, and `clipsAlert` says "N clips have been reported".
- `DELETE /admin/clips/reports` clears a clip's reports.
- `DELETE /admin/clips/thumb` deletes `clips/<id>.jpg` and clears `thumb`. Cards fall back to the streamer's blurred picture.
- A draft thumbnail upload is logged at Info: "A clip picture was uploaded" with `user_id`, `draft_id`, `bytes`. The clip id equals the draft id, so it can be traced.
- Report, clear and remove are logged at Info with the acting user's id.

#### 2.10.6 Streamer controls

**`PUT /clips/streamer {allow}`** is for a user with a manual link (`me.streamKey` set through `/api/v1/me/link`).
- `allow:false` sets `optOut[user] = now`. It takes effect at once: call `p.recorders().optOut(userID)`, which stops that stream's recorder, deletes its ring and tail, and discards every draft cut from its handle, by any owner, as `wiped`. Then `republishClips()`.
- `allow:true` deletes the entry, and recording resumes at the next poll.
- Log Info.

**`POST /clips/wipe?handle=`** is for that handle's manual streamer or an admin:
1. Cut the live ring with no tail: a new epoch at the next IDR.
2. Delete the handle's tail.
3. Cancel in-flight exports from that handle.
4. Discard its drafts, by any owner, as `wiped`. A draft in `publishing` has its context cancelled; if its CAS already succeeded, it is a published clip and stays.
5. Log Info "A stream's held recording was wiped" with `user_id`, `stream_handle`, `drafts_removed`.

Published clips are not affected; the streamer deletes those from Clips.

#### 2.10.7 Error codes

The server text is fixed. The webapp has its own copy, keyed by `code` (§3.13).

| code | HTTP | Server text |
|---|---|---|
| `clips_off` | 403 | Clipping has been switched off. |
| `blocked` | 403 | An admin has stopped you making clips. |
| `forbidden` | 403 | That is not allowed. |
| `not_allowed` | 403 | Only the person who made it, the streamer or an admin can do that. |
| `own_clip` | 403 | You can't upvote your own clip. |
| `not_watching` | 403 | Tune in to this stream to clip it. |
| `streamer_off` | 403 | The streamer has switched clipping off. |
| `not_live` | 404 | That stream is not live right now. |
| `not_clippable` | 409 | This stream can't be clipped. |
| `warming_up` | 409 | Clipping starts a few seconds after a stream does. Try again in a moment. |
| `draft_cap` | 409 | You already have 3 drafts. Publish or discard one first. |
| `clip_cap` | 409 | There is no room for another clip. Delete an old one first. |
| `duplicate` | 409 | You have already published most of this moment. |
| `publishing` | 409 | This clip is already being published. |
| `draft_gone` | 404/410 | This draft has expired. |
| `draft_wiped` | 410 | The streamer wiped this part of their stream. |
| `tail_gone` | 410 | The end of that stream is no longer held. |
| `clip_gone` | 410 | This clip has gone. |
| `too_short` | 400 | A clip has to be at least 5 seconds long. |
| `bad_range` / `bad_request` | 400 | That request did not make sense. |
| `bad_thumb` | 400 | That picture is not usable. |
| `rate_limited` | 429 | Too many at once. Wait a moment and try again. |
| `recording_failed` | 503 | That stream could not be clipped just now. |
| `busy` | 503 | The server is busy cutting other clips. Try again in a moment. |
| `storage_unavailable` | 503 | Clips are not set up on this server. |
| `store` | 503 | Clips could not be saved just now. Try again. |
| `storage_full` | 507 | The server is out of room for clips. |
| `user_storage_full` | 507 | You are using your share of clip space. Delete one of your clips first. |

### 2.11 Serving clip files

```go
func serveClipFile(w http.ResponseWriter, r *http.Request, path, downloadName string) {
    f, err := os.Open(path) // 404 if missing
    st, _ := f.Stat()
    h := w.Header()
    h.Set("Content-Type", "video/mp4")
    h.Set("X-Content-Type-Options", "nosniff")
    h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
    h.Set("Cache-Control", "private, no-cache")
    h.Set("Content-Disposition", `inline; filename="`+downloadName+`"`) // villanet-clip-<id>.mp4
    bw := &bufferedResponse{ResponseWriter: w, buf: bufio.NewWriterSize(w, 512<<10)}
    http.ServeContent(bw, r, downloadName, st.ModTime(), f) // Range, If-Range, HEAD, 206
    bw.buf.Flush()
}
```

- The buffering matters: each `Write` through the plugin RPC costs two RPC calls (`plugin/http.go`).
- Mattermost forwards the request headers and proxies the status and response headers, so 206 works. Acceptance A6 checks this on the real server.
- The server's Read and Write timeouts are 300 s. A `<video>` element simply re-requests by Range.

### 2.12 Snapshot and `/me`

```go
// snapshot (same for every viewer; nothing formatted)
Clips       *clipsView      `json:"clips,omitempty"`        // always set once activated
BestClipper *bestClipperView `json:"bestClipper,omitempty"` // only while EnableClips AND somebody holds it
ClipTails   []clipTailView  `json:"clipTails,omitempty"`    // tails ≥ 5 s, newest first

type clipsView struct {
    Enabled       bool   `json:"enabled"`          // new clips can be made now (EnableClips && no problem && !halted)
    LengthSeconds int    `json:"lengthSeconds"`
    RetentionDays int    `json:"retentionDays"`
    Count         int    `json:"count"`            // live published clips
    Newest        int64  `json:"newest,omitempty"` // createdAt of the newest live clip
    Version       string `json:"version,omitempty"` // first 16 hex of sha256 over "id|title|upvotes|thumb\n" of live clips in doc order; "" when none
}
type bestClipperView struct {
    UserID      string       `json:"userId"`
    Username    string       `json:"username,omitempty"`
    DisplayName string       `json:"displayName,omitempty"`
    Upvotes     int          `json:"upvotes"`
    Clips       int          `json:"clips"`
    Since       int64        `json:"since"`
    BestClip    *bestClipRef `json:"bestClip,omitempty"` // holder's most-upvoted live clip; tie = newest
}
type bestClipRef struct{ ID string `json:"id"`; Title string `json:"title"`; Upvotes int `json:"upvotes"` }
type clipTailView struct {
    Handle  string `json:"handle"`
    Title   string `json:"title"`
    UserID  string `json:"userId,omitempty"`
    Reason  string `json:"reason"`  // "ended" | "dropped"
    EndedAt int64  `json:"endedAt"` // ms
    Until   int64  `json:"until"`   // ms
    HeldMs  int64  `json:"heldMs"`  // min(L, epoch length)
}

// apiStream
Clip     string `json:"clip,omitempty"`     // starting|ready|failed|unsupported|full|queued|off
ClipFrom int64  `json:"clipFrom,omitempty"` // epoch start, ms; only with clip=="ready"
ClipTail int64  `json:"clipTail,omitempty"` // until (ms) of a tail held for this live handle
```

- `p.addClips(current)` is called on the unconfigured, failure and success paths of `pollOnce`, right after `addCommunity`. It reads only the cache and the recorder manager, so it never fails or blocks.
- `clipFrom` changes once per epoch and tails change rarely, so neither causes churn.

**`p.republishClips()`.** Every clip write ends with this, **never with `refreshSoon`**. It is coalesced: when called, arm a timer for the next `republishEvery` boundary unless one is already armed. When the timer fires:
1. Copy the current snapshot's struct (a shallow copy, keeping `FetchedAt`).
2. `addClips(&copy)`.
3. `p.publish(&copy)`.

It never polls BB. If a newer poll has published in between, `publish` discards the copy, because `FetchedAt` is older, and that poll already carried the change from the cache.

**`meResponse` additions:**
- `ClipsAlert string json:"clipsAlert,omitempty"`, for admins only. It joins with " · " whichever of these apply:
  - `problem` or `halted`;
  - "Clips cannot reach Broadcast Box's media port from this server: <reason>", when every eligible stream's recorder has been `failed` for 5 min or more;
  - "N clips have been reported";
  - "Keep clips for was lowered: N clips will be removed <date time>".
- `ClipsStreamer *struct{Allowed bool} json:"clipsStreamer,omitempty"`, only for users with a manual link.

### 2.13 Retention and housekeeping

**Retention record.** These are pure functions in `clips.go`:

```go
// The record the sweep would write now. Used by read routes (provisionally) and by the sweep (persisted).
func effectiveRetention(rec retentionRecord, cfgDays int, now int64) retentionRecord {
    if rec.Days == 0 { return retentionRecord{Days: cfgDays} }            // first record: no grace
    if rec.Days == cfgDays { return rec }
    inGrace := rec.ChangedAt > 0 && now < rec.ChangedAt+retentionGraceMs
    prev := rec.Days
    if inGrace && rec.PrevDays > prev { prev = rec.PrevDays }
    changedAt := now                                                       // lowering: grace starts now
    if cfgDays > rec.Days { if inGrace { changedAt = rec.ChangedAt } else { changedAt, prev = 0, 0 } } // raising keeps any running grace
    return retentionRecord{Days: cfgDays, PrevDays: prev, ChangedAt: changedAt}
}
func clipExpiresAt(createdAt int64, rec retentionRecord) int64 {
    exp := createdAt + int64(rec.Days)*dayMs
    if rec.ChangedAt > 0 && rec.PrevDays > rec.Days && createdAt < rec.ChangedAt {
        exp = max(exp, min(createdAt+int64(rec.PrevDays)*dayMs, rec.ChangedAt+retentionGraceMs))
    }
    return exp
}
```

Lowering 30 → 3 removes nothing for 24 h, and setting it back within that time keeps everything. Raising applies at once.

**Cluster job.** In `OnActivate`:

```go
p.clipsJob, _ = cluster.Schedule(p.API, "clips_sweep", cluster.MakeWaitForInterval(clipsSweepInterval), p.sweepClips)
```

It runs on one node at a time under a KV lock. Call `p.clipsJob.Close()` in `OnDeactivate`.

**`sweepClips`** returns at once if the store is halted or `problem` is set.
1. Read `clips` fresh from KV.
2. If `effectiveRetention(doc.retention, cfg, now) != doc.retention`, persist it, in the same CAS as step 3.
3. Settle every entry with `now >= clipExpiresAt` (reason `expired`) **in one CAS**. Delete their files.
4. Orphans, **only if step 1 read the doc successfully**:
   - `clips/*.mp4` and `*.jpg` whose id is not in the doc and whose mtime is older than `orphanAge` (24 h);
   - `clips/.tmp-*` older than `tmpAge`.
5. Recount usage by walking `clips/` and `drafts/`. Recompute the holder with a no-op `clipsCAS`.
6. Log Info "Clips swept" with `expired`, `orphans` and `bytes`, only when something changed.

**Read routes** treat `now >= clipExpiresAt` as gone (410) at once, whatever the sweep's timing.

**In-process housekeeping** runs per node, on a 60 s ticker in the recorder manager. It:
- Expires `ready` drafts older than `draftTTL` and deletes their files. Drafts in `publishing` are skipped.
- Drops tombstones older than `draftTombstone`.
- **Scans `drafts/`.** It deletes `.mp4` and `.jpg` files whose id is not an in-memory `ready` or `publishing` draft and whose mtime is older than `draftOrphanAge` (10 min), and `.tmp-*` files older than 10 min.
- Deletes tails past `until`, and the oldest tails while rings and tails exceed `ringBudgetBytes`.
- Checks free space against `recorderStopFree`.
- Prunes the viewer registry.
- Rebuilds the recorder health used by `/me`.

### 2.14 Lifecycle

**`OnActivate`**, after the existing setup and **before** `restartPoller`:
1. Create the limiters.
2. `p.startClips()`:
   1. Resolve the root and create the directories.
   2. **Wipe `ring/` and `drafts/`.**
   3. Load the doc, halting if it will not decode.
   4. Compute usage.
   5. Schedule the job and start housekeeping.
   6. Recompute the holder.

   Failures set `problem` and never fail activation.

**`OnConfigurationChange`**, only when `activated && previous != nil`, before the existing `restartPoller`:
- `previous.EnableClips && !cfg.EnableClips`: `p.recorders().stopAll(true)`. This stops every recorder, wipes rings and tails, discards drafts and clears the viewer registry. *As built: everything but the last; the viewer registry is kept (§2.4.9).*
- `BroadcastBoxURL` changed: `p.recorders().stopAll(false)`, because the rings and tails belong to the old origin. Drafts are kept.
- `ClipLengthSeconds` changed: running recorders take the new window at the next rotation.

**Unconfigured poll branch:** `p.recorders().stopAll(false)`.

**`OnDeactivate`**, after `stopPoller()`:
1. `p.stopClips()`:
   - Stop every recorder: close the PeerConnections and wait for the reader and writer goroutines, up to 5 s in total.
   - Cancel in-flight exports and trims through their contexts.
   - Stop housekeeping and the republish timer.
   - `clipsJob.Close()`.
   - Wipe `ring/` and `drafts/`. Clear the viewer registry.
2. Published clips and KV are untouched.

**Uninstall.** Files stay under `<data>/ceo.villa.villanet/`. The README tells admins to delete that directory by hand.

### 2.15 Logging

Never log a stream key; use `stream_handle` or `keyPrefix`.

| Level | Events |
|---|---|
| Info | Recorder ready (handle, epoch, width×height, codec); recorder stopped (why); tail kept (handle, reason, held); draft made; clip published; clip deleted (by, reason); picture uploaded; clip reported; reports cleared; picture removed; expiry sweep summary; Best Clipper change; block, unblock, score reset; opt-out or opt-in; wipe; root path on activate |
| Warn | A recorder attempt failed (once per state change, with the ICE or WHEP error); a cut (discontinuity, audio loss, disk too slow; at most 1 per minute per stream); free space low; session id unknown |
| Error | The doc cannot be read (halted) |
| Debug | Per-sample statistics; samplebuilder `PrevDroppedPackets`; writer queue high-water mark |

### 2.16 Permissions summary

| Action | Who |
|---|---|
| Clip a live stream | A signed-in, non-blocked user who is watching it now, or its manual streamer |
| Clip a tail (ended or dropped) | A signed-in, non-blocked user who was watching when it ended, or its manual streamer |
| See, edit or publish a draft | Its owner only |
| Watch and list clips | Any signed-in user |
| Upvote, report | Any signed-in non-bot user, not on their own clip |
| Delete a clip | The clipper, the stream's manually linked streamer, or a system admin |
| Switch clipping of a stream off | Its manually linked streamer |
| Wipe a stream's held recording | Its manually linked streamer, or a system admin |
| Block, unblock, reset a score, remove a picture, clear reports, see the admin view | System admin (`isSystemAdmin`) |

### 2.17 LOCKDOWN constraints honoured

- BB endpoints used: only the existing `POST /api/whep`, plus media to BB's UDP mux port on its LAN address.
- Not used: `/api/admin/*`, `/api/layer`, STUN, TURN, or any other host.
- Stream keys never leave the server. KV, logs and JSON carry only the handle.
- The viewer registry is in memory only and is never shown.

### 2.18 Dependencies

Add these to `go.mod`, then run `go mod tidy`. Tidy must add `abema/go-mp4 v1.7.3`; `go get` alone misses its go.sum entry.

```
github.com/pion/webrtc/v4 v4.2.22
github.com/pion/interceptor v0.1.49
github.com/pion/rtp v1.10.5
github.com/pion/rtcp v1.2.18
github.com/pion/ice/v4 (as resolved by tidy; v4.4.5 at research time)
github.com/bluenviron/mediacommon/v2 v2.9.5
golang.org/x/sys (becomes direct, for free-space checks)
```

- All are MIT or BSD licensed and pure Go. `CGO_ENABLED=0` builds work for all five Makefile targets.
- The linux-amd64 binary grows from about 21.6 MB to about 26.7 MB (about 9.9 MB gzipped).
- `golang.org/x/time` jumps to v0.14.0; that is expected.

### 2.19 Go tests

`make check` runs `go test -race` without `-short`, so **every test it runs must be deterministic and must not depend on real-time timing**.

**Fixture.** `build/make-test-clip.sh` (dev-only, needs ffmpeg) generates `server/testdata/clip-h264-opus.mp4`, about 107 KB, and the file is committed:

```sh
ffmpeg -y -f lavfi -i "testsrc2=size=320x180:rate=15" -f lavfi -i "sine=frequency=440:beep_factor=4:sample_rate=48000" \
  -t 10 -c:v libx264 -profile:v high -preset veryslow -bf 0 -g 30 -keyint_min 30 -sc_threshold 0 -b:v 60k \
  -pix_fmt yuv420p -c:a libopus -b:a 16k -ac 2 -movflags +faststart server/testdata/clip-h264-opus.mp4
```

- `pmp4.Unmarshal` reads it as H.264 with 5 sync samples (timescale 15360) and Opus with 501 samples. Re-marshalled, it decodes cleanly under `ffmpeg -xerror`.
- Tests feed its samples into the reader checks, the writer and the ring with synthetic arrival and wall times, converting timescales.
- 30 fps and 29.97 fps cases use synthetic tick streams (3000 ticks; 3003 ticks).

Use the existing `kvBackedPluginWithFaults` and the plugintest mocks. Inject time through a clock function.

**Settings**
1. `TestClipLengthNormalisation` (999→300, 0→30, 75→60, 100→90, "abc"→120, ""→120). `TestClipSettingsAreClampedAndDefaulted` (retention, storage). Update `TestTheSectionDefaultsMatchTheManifest`.

**Storage**
2. `TestClipsNeedTheLocalFileDriver`; `TestClipsRootIsWipedOfRingAndDraftsOnActivate`.

**Ring and writer**
3. `TestRingStartsSegmentsOnKeyframesAndKeepsOnlyTheWindow`; `TestRingSizesToMeasuredBitrate` (20 Mbit/s at L=120 keeps the full 2:00); `TestByteCapEvictionIsTruncatedNotPartial`; `TestPinnedSegmentsSurviveEvictionUntilReleased`; `TestRingResetsEpochOnParameterSetChange`; `TestRingResetsWhenKeyframesAreTooRare`.
4. `TestSlowDiskDoesNotStallTheReader`: a writer blocked on a fake file fills the queue; the reader never blocks; a cut with "disk too slow" follows; arrival times are unaffected.

**Media and timing**
5. `TestAccessUnitsDropAUDAndFillerAndDetectIDR`; `TestArrivalClockAnchorsFromTheFirstSample` (wall is never near 1970); `TestArrivalClockUnwrapsAndTakesTheEarliestArrival`; `TestArrivalMapGivesEachSampleItsFirstPacket`.
6. `TestTimestampRewriteIsACut` (rtp +1 frame, arrival +5.6 s, normal pace after); `TestLateBurstIsNotACut` (3 s stall then catch-up); `TestForwardJumpIsACut`; `TestPauseWithAgreeingClocksKeepsTheEpoch` (the video freezes, non-IDR AUs after the gap are dropped); `TestBFramesAreReportedUnsupported`.
7. `TestAudioGapsAreFilledWithSilence` (Σ audio duration equals the input span; filler payloads are `F8 FF FE`).
8. `TestTicksToMsIsSharedAt30And2997fps`: trim at exactly `ticksToMs(cum at keyframe k)` gives `kIn == k` and `leadMs == 0`.

**Export and trim**
9. `TestDraftStartsOnFirstKeyframeInsideTheWindow`; `TestDraftIsNeverLongerThanTheClipLength`; `TestDraftAudioIsAlignedWithin10ms`; `TestDraftIsMoovFirstAndRoundTrips` (box order ftyp, moov, mdat; `pmp4.Unmarshal` sample count and bytes match the input); `TestDraftPartialFlag`; `TestWarmingUpUnderFiveSeconds`; `TestClickTimeIsFixedBeforeWaitingForASlot` (slots held; the draft ends at `clickAt`, not at slot time).
10. `TestTrimKeepsTheExactInAsLead`; `TestTrimOutIsFrameExact`; `TestTrimRefusesUnderFiveSeconds`; `TestFullRangePublishRenamesWithoutRemux`; `TestFailedTrimLeavesNoTempFile`; `TestTempNamesNeverCollide`.

**Publish**
11. `TestConcurrentPublishOfOneDraftMakesOneClip`: two goroutines; one gets 201, the other 409 `publishing` or 200 with the same clip; one entry; the file decodes.
12. `TestRetriedPublishAfterSuccessReturnsTheClip`; `TestDuplicateIdAppendIsANoOp`.
13. `TestTrimmedPublishLeavesNoDraftFile`; `TestStrayDraftFilesAreSwept`.
14. `TestFailedFullRangePublishPutsTheDraftBack`: inject a CAS failure after the rename; the draft still plays and publishes again.

**Votes, settling, Best Clipper**
15. `TestVotesAreDesiredStateAndIdempotent`; `TestSimultaneousVotesAreAllKept` (real goroutines against the mock's CAS); `TestNoSelfOrBotVotes`; `TestNoVotesOnExpiredClips`.
16. `TestSettleIsOneWriteAndOrphansAreSwept` (crash after the CAS: banked once, files removed by the sweep after `orphanAge`).
17. `TestBestClipperTieKeepsHolder`; `TestBestClipperFirstToReachWinsWithoutHolder`; `TestExpiredVotesStillCount`; `TestClipperAndAdminDeleteVoidStreamerDeleteBanks`; `TestDeactivatedUsersCannotHoldIt`; `TestBlockedUsersCannotHoldIt`; `TestIneligibleVotersDoNotCount`; `TestScoreResetIgnoresEarlierVotes`; `TestNobodyHoldsItAtZero`; `TestHolderSurvivesRestart`; `TestHolderIsRecomputedInTheSameWrite`; `TestBestClipperHiddenWhileClipsOff`; `TestDuplicateClipRefused`.

**Recorder, access and snapshot**
18. `TestRecorderSessionsAreNeitherViewersNorStrangers`.
19. `TestSnapshotCarriesNoStreamKeyAndNothingPerViewer` (marshal with clips, drafts, recorders and tails present; no key substring; `version` deterministic across map orders).
20. `TestDraftsArePrivate` (list, video, thumb, publish and delete by another user give 404); `TestDraftCapsAndRateLimits`; `TestLimiterRefundedOnWarmingUpAndBusy`; `TestBlockedUsersCannotDraftOrPublish`; `TestUnreadableDocFailsClosed`.
21. `TestWatchGate`: not watching gives 403; a session listed in the latest poll passes; one opened 30 s ago passes; a peek does not count; the manual streamer is exempt; the tail rule.
22. `TestSlotsAreStickyAndGoToTheMostWatched`; `TestResolverFailureKeepsRecorders`.
23. `TestEndedStreamCanBeClippedFromItsTail`; `TestDroppedEpochBecomesATail`; `TestTailsExpireAndAreBounded`.
24. `TestStreamerOptOutStopsRecordingAndWipes`; `TestWipeDiscardsDraftsOfThatStream`; `TestOnlyManualStreamersMayOptOutOrWipe`.
25. `TestPerPersonShare`.
26. `TestClipVideoServesRanges` (httptest: `Range: bytes=100-199` gives 206, `Content-Range` and 100 bytes; HEAD works).
27. `TestTitlesAreCleanedAndKeysRedacted` (own key and the clipped stream's key).
28. `TestVotesRepublishWithoutPolling` (no `fetchStatus` call; at most one publish per 2 s).
29. `TestReportsAndPictureRemoval`.

**Retention and lifecycle**
30. `TestSweepExpiresBanksAndDeletesFiles`; `TestRetentionShorteningWaits24Hours`; `TestRaisingDuringGraceKeepsGrace`; `TestSweepNeverDeletesFilesWhenTheDocIsUnreadable`; `TestOrphansOlderThanADayAreRemoved`; `TestUnreadableClipsDocHaltsWrites`.
31. `TestDeactivateStopsRecordersAndWipesRing`; `TestTurningClipsOffStopsRecordersWithoutAPoll`; `TestUnconfiguredStopsRecorders`.

**Real-time tests**, which are skipped unless `VILLANET_E2E=1` (`if os.Getenv("VILLANET_E2E") != "1" { t.Skip("set VILLANET_E2E=1 or run make e2e") }`):

32. `TestE2ERecorderOverLoopback`:
    - In-process: a pion publisher sends the fixture's H.264 and Opus in real time to a minimal fake WHEP origin. The origin is an httptest server whose `/api/whep` answers with a pion PeerConnection that forwards the tracks and sets `Location: /api/whep/<uuid>`.
    - The recorder uses `allowed = {127.0.0.1}` and `SetIncludeLoopbackCandidate(true)`, a test-only option.
    - Assert `ready` within 5 s (15 s when built with `-race`), a draft that decodes per `pmp4.Unmarshal`, and A/V start skew ≤ 40 ms.
33. `TestE2ERecorderReconnectsWhenAudioStops`: the fake origin stops audio while video flows. Expect a new epoch, `clipFrom` advancing, and a tail with reason `dropped`.

---

## 3. Webapp

All of this is React 17 only: no `useId`, `useSyncExternalStore` or `createRoot`. Ids come from a module counter, as in `UpNext.tsx`. All styling lives in `styles.ts` under `.vntv`. Portals use a root with `className="vntv vntv-modal"` (z-index 1060).

Rules for editing `styles.ts`:
- No backticks, even in comments.
- No `\00b7`-style escapes.
- Run `python3 build/check-styles.py` after every edit, and look at the result in the lab.

### 3.1 `types.ts` additions

```ts
export type ClipState = 'starting' | 'ready' | 'failed' | 'unsupported' | 'full' | 'queued' | 'off';
// Stream gains:
clip?: ClipState;
clipFrom?: number;          // ms, only with clip === 'ready'
clipTail?: number;          // ms until which a tail of this live handle is held
// Snapshot gains:
clips?: ClipsInfo;
bestClipper?: BestClipper;
clipTails?: ClipTail[];
// Me gains:
clipsAlert?: string;
clipsStreamer?: {allowed: boolean};

export type ClipsInfo = {enabled: boolean; lengthSeconds: number; retentionDays: number; count: number; newest?: number; version?: string};
export type ClipTail = {handle: string; title: string; userId?: string; reason: 'ended' | 'dropped'; endedAt: number; until: number; heldMs: number};
export type BestClipper = {userId: string; username?: string; displayName?: string; upvotes: number; clips: number; since: number;
    bestClip?: {id: string; title: string; upvotes: number}};
export type ClipPerson = {userId?: string; username?: string; displayName?: string; name: string};
export type Clip = {id: string; title: string; createdAt: number; expiresAt: number; startAt: number; durationMs: number; leadMs?: number;
    width: number; height: number; codecs: string; bytes: number; thumb?: string; clipper: ClipPerson; streamer: ClipPerson;
    streamNote?: string; upvotes: number; voted: boolean; mine: boolean; ofMyStream: boolean;
    canVote: boolean; canDelete: boolean; canReport: boolean; reported: boolean; reports?: number};
export type ClipSource = 'live' | 'tail';
export type ClipDraft = {id: string; handle: string; source: ClipSource; state: 'ready' | 'publishing' | 'published'; clipId?: string;
    createdAt: number; expiresAt: number; startAt: number; durationMs: number; requestedMs: number; partial: boolean; truncated: boolean;
    width: number; height: number; codecs: string; bytes: number; hasThumb: boolean; streamer: ClipPerson; streamNote?: string};
export type ClipErrorCode = 'clips_off'|'blocked'|'forbidden'|'not_allowed'|'own_clip'|'not_watching'|'streamer_off'|'not_live'
    |'not_clippable'|'warming_up'|'draft_cap'|'clip_cap'|'duplicate'|'publishing'|'draft_gone'|'draft_wiped'|'tail_gone'
    |'clip_gone'|'too_short'|'bad_range'|'bad_request'|'bad_thumb'|'rate_limited'|'recording_failed'|'busy'
    |'storage_unavailable'|'store'|'storage_full'|'user_storage_full'|'network';
export type AdminClips = {problem: string; halted: string; root: string; usedBytes: number; limitBytes: number; shareBytes: number;
    freeBytes: number; totalBytes: number; ringBytes: number;
    recorders: Array<{handle: string; title: string; state: ClipState; heldSeconds: number; bitrate: number; truncated: boolean; reason: string}>;
    tails: ClipTail[]; drafts: number; clips: Clip[];
    people: Array<{userId: string; username: string; displayName: string; clips: number; bytes: number; upvotes: number; banked: number}>;
    blocked: Array<{userId: string; username: string; displayName: string; blockedAt: number; by: string}>;
    retention: {days: number; pendingRemovals: number; removalAt?: number};
    bestClipper: BestClipper | null};
```

### 3.2 `client.ts` additions

Every one of these must also exist in `lab/mocks/client.ts`; `surface.ts` enforces that.

```ts
export class ClipRequestError extends Error { status: number; code: ClipErrorCode }  // thrown by requestClip()
// requestClip<T>(path, init): like requestJSON but parses {error, code}; fetch failure -> code 'network'
export const fetchClips = (): Promise<{clips: Clip[]}>
export const fetchClip = (id: string): Promise<Clip>
export const clipVideoURL = (id: string): string            // `${PLUGIN_BASE}/api/v1/clips/video?id=…`
export const clipThumbURL = (id: string, v: string): string // `${PLUGIN_BASE}/api/v1/clips/thumb?id=…&v=…`
export const voteClip = (id: string, up: boolean): Promise<{upvotes: number; voted: boolean}>
export const reportClip = (id: string): Promise<{ok: boolean}>
export const deleteClip = (id: string): Promise<{ok: boolean}>
export const fetchDrafts = (): Promise<{drafts: ClipDraft[]}>
export const createDraft = (handle: string, source: ClipSource): Promise<ClipDraft>
export const deleteDraft = (id: string): Promise<{ok: boolean}>
export const draftVideoURL = (id: string): string
export const uploadDraftThumb = (id: string, jpeg: Blob): Promise<void>  // PUT, Content-Type image/jpeg, baseHeaders()
export const publishDraft = (id: string, body: {title: string; inMs: number; outMs: number}): Promise<Clip>
export const setClipsAllowed = (allow: boolean): Promise<{allowed: boolean}>
export const wipeRecording = (handle: string): Promise<{ok: boolean; draftsRemoved: number}>
export const fetchAdminClips = (): Promise<AdminClips>
export const blockClipper = (userId: string): Promise<{ok: boolean; draftsRemoved: number}>
export const unblockClipper = (userId: string): Promise<{ok: boolean}>
export const resetClipperScore = (userId: string): Promise<{ok: boolean}>
export const removeClipThumb = (id: string): Promise<{ok: boolean}>
export const clearClipReports = (id: string): Promise<{ok: boolean}>
```

Non-GET requests use the existing `baseHeaders()`: X-Requested-With, plus X-CSRF-Token when `MMCSRF` exists.

### 3.3 `clips.ts` module store and `ClipsRoot`

`webapp/src/clips.ts` follows the `store.ts` and `watching.ts` pattern: listeners plus a `useObservable`-style hook.

```ts
type EditorState = null | {handle: string; source: ClipSource; streamName: string; lengthSeconds: number;
    phase: 'cutting' | 'ready' | 'publishing' | 'published' | 'error';
    draft?: ClipDraft; clip?: Clip; error?: ClipErrorCode; returnFocus?: HTMLElement | null};
type DialogState = null | {view: 'list'; filter?: 'all' | 'yours'} | {view: 'clip'; id: string; direct: boolean};
export function startClip(stream: {handle: string; title: string}, source: ClipSource, lengthSeconds: number, returnFocus: HTMLElement | null): void
export function editDraft(draft: ClipDraft): void
export function closeEditor(): void
export function openClips(id?: string, filter?: 'all' | 'yours'): void   // id => viewer, direct: true
export function closeClips(): void
export function refreshDrafts(): Promise<void>
export function markClipsSeen(newest: number): void  // localStorage 'vntv.clips.seen', try/catch
export function useClipsUnseen(info?: ClipsInfo): boolean
export const useClipEditor: () => EditorState; export const useClipsDialog: () => DialogState; export const useDrafts: () => ClipDraft[];
```

`components/ClipsRoot.tsx` is registered in `index.tsx` with `if (typeof registry.registerRootComponent === 'function') registry.registerRootComponent(ClipsRoot);`.
- It renders `<ClipEditor/>` when the editor is open and `<ClipsModal/>` when the dialog is open; otherwise nothing.
- It calls `refreshDrafts()` once on mount.
- Hosting at the root means the editor survives the panel being closed (Mattermost unmounts the RHS), serves every entry point, and works in Mattermost's pop-out window.

### 3.4 The ✂ button (`Player.tsx`, `Hud.tsx`)

**`HudButton` gains optional props:**
- `ariaDisabled?: boolean`: sets `aria-disabled`; click is a no-op.
- `title?: string`
- `keyShortcuts?: string`
- `tag?: string`: corner tag, class `vntv-hud-tag`.
- `busy?: boolean`: class `vntv-hud-busy`, which pulses and stops under `prefers-reduced-motion`.
- `buttonRef?`

**Placement.** On a live tile, in the HUD between `<span className='vntv-hud-spacer'/>` and the pop-out link, rendered when:

```ts
snapshot.clips?.enabled && !stream.alwaysOn && (stream.clip !== undefined || !!stream.clipTail)
```

**On `WaitingTile`**, whose stream has left the snapshot, the HUD gets a ✂ when `snapshot.clipTails` has an entry for its handle with `until > Date.now()`. It always uses `source: 'tail'`. The `WaitingTile` props gain `handle`.

**States.** L = `snapshot.clips.lengthSeconds`, formatted `m:ss`. `recorded = (Date.now() − clipFrom) / 1000`, recomputed every 1 s while the HUD is mounted. The browser's clock is trusted; the server is authoritative on the click.

The rules are checked top to bottom, and the first match wins:

| Condition | Source | Rendering | aria-label / title |
|---|---|---|---|
| Editor open in `cutting` for this handle | — | `ariaDisabled`, `busy` | "Clipping…" |
| `clip==='ready'`, recorded ≥ L | live | Normal | "Clip the last 2 minutes" / "Clip the last 2:00 (C)" |
| `clip==='ready'`, 5 ≤ recorded < L | live | Normal, `tag="0:47"` | "Clip the last 47 seconds — all that has been recorded so far" |
| Not ready with ≥ 5 s, and `clipTail` held | tail | Normal | "Clip the last 2:00 before the stream dropped" |
| WaitingTile with a tail | tail | Normal | "Clip the last 2:00 before Dave went off" |
| `clip==='starting'`, or `ready` with recorded < 5 | — | `ariaDisabled` | "Clipping starts in a few seconds" |
| `failed` | — | `ariaDisabled` | "Clipping isn't working on this stream just now" |
| `full` | — | `ariaDisabled` | "The server is out of room for clipping just now" |
| `queued` | — | `ariaDisabled` | "Clipping is busy with other streams right now" |
| `off` | — | `ariaDisabled` | "Dave has switched clipping off" |
| `unsupported` | — | `ariaDisabled` | "This stream's video format can't be clipped yet" |

The second title line, when enabled, reads: "In the editor, I and O set the start and end at the playhead."

**Click, and the C key:**
1. If `document.fullscreenElement`, call `document.exitFullscreen()` in the same handler; otherwise the portal would be hidden under the top layer.
2. `startClip(stream, source, L, buttonEl)`.

**C key.** `onKeyDown` on the tile root (`.vntv-player`) when all of these hold:
- the key is `c` or `C`;
- no modifier is held, so Ctrl or ⌘+C stays copy;
- the user is not composing (reuse `keys.ts`);
- the target is not a text input.

The handler **calls `event.preventDefault()` and `event.stopPropagation()`** (§3.5).

### 3.5 Keyboard rule: keep keys away from Mattermost's message box

Mattermost moves focus to the message box on almost any printable key:
- `webapp/channels/src/components/advanced_text_editor/use_textbox_focus.tsx` runs `document.addEventListener('keydown', focusTextboxIfNecessary)`.
- `utils/post_utils.ts` `shouldFocusMainTextbox` returns true for any printable key unless the focused element is an `INPUT`, a `TEXTAREA` or inside `[contenteditable="true"]`. It spares Space only on `BUTTON` and `A`.

So:

1. **Every key VillaNET acts on** — C on the tile, and Space, I and O in the editor — calls `preventDefault()` and `stopPropagation()` in its React handler.
   - React 17 listens on the root container, and on each portal's container: in v17.0.2, `preparePortalMount` calls `listenToAllSupportedEvents(portalInstance)`. Both are below `document`, so the event never reaches Mattermost's listener.
2. **The editor card and the Clips dialog card** have an `onKeyDown` in the bubble phase that calls `stopPropagation()`, without `preventDefault()`, for every printable keydown (`key.length === 1`, no Ctrl, Meta or Alt) coming from inside the card.
   - Never use a capture listener on `document` for this: it would also stop React's own handlers.
   - Escape keeps the existing capture-phase pattern from `LeaderboardModal.tsx`.
3. **Space** is handled by VillaNET only when focus is on the stage or a trimmer thumb. On a `BUTTON`, an `A`, an `INPUT`, or the native `<video controls>`, it keeps its native meaning.

### 3.6 The editor (`components/ClipEditor.tsx`)

**The dialog.**
- Portal on `document.body`: `.vntv-modal` plus a card `.vntv-modal-card.vntv-clipedit`, `max-width: 920px`.
- Below 600px wide it fills the screen: inset 0, no radius, actions in a sticky footer.
- `role="dialog"`, `aria-modal`, `aria-labelledby` the title "NEW CLIP", with a `ScissorsIcon` header icon.
- **Focus trap:** Tab and Shift+Tab cycle inside the card.
- Escape uses the capture-phase keydown with `stopPropagation`, as `LeaderboardModal` does.
- A mouse-down on the backdrop that both starts and ends there means "Later".

**Layout, top to bottom:**
1. A 16:9 stage holding the `<video>`. No native controls. `preload="auto"`, `playsInline`, starts muted, `src=draftVideoURL(id)`.
2. A big white round ▶ in the centre.
3. An on-picture bar: play/pause; "0:12 / 0:45" (position within the selection / selection length); a Loop toggle (`aria-pressed`, on by default); a mute toggle.
4. The context line: "From Dave's stream · “Arma 3 ops night” · 21:02:17 – 21:04:17". Times come from `startAt` plus offsets, in local time. For a tail, append " · before it went off" (`ended`) or " · before it dropped" (`dropped`).
5. Notes, when applicable:
   - `draft.partial`: "Only 0:47 had been recorded — the recording starts when the stream does."
   - `draft.truncated`: "Only 1:24 fitted — this stream's bitrate is higher than clips are sized for."
6. The trimmer (§3.7).
7. The title field: label "Title", placeholder "What happened?", `maxLength` 80, a counter shown from 60 characters. Enter publishes once `ready`.
8. A `role="status"` line (§ phases below).
9. Actions: [Discard] on the left; [Later] and [PUBLISH] on the right. PUBLISH is the primary button.

**Phases and copy:**

| Phase | UI |
|---|---|
| `cutting` | The stage shows `Screen` plus `Plate` titled "CUTTING", note "the last 2:00 of Dave" (reuse `NoSignal` pieces). **The title field is live and gets focus as soon as the dialog opens.** Only the stage, the trimmer and PUBLISH are `inert` (React 17 sets it via a ref attribute). Status: "Cutting the last 2:00 of Dave…" |
| `ready` | Paused on the in-point frame with the big ▶. Status: "Clip ready — 1:58." Focus stays where it is. |
| `publishing` | PUBLISH reads "Publishing…" and is disabled, so a double click is ignored on the client too. The fields and trimmer are inert. A thin indeterminate bar runs under the video, with the red gradient of `.vntv-lb-fill`. Status: "Publishing — cutting 0:45 and making its picture." After 10 s it adds: "Still working — longer clips take a moment." Escape and × are ignored. |
| `published` | The body becomes: "✔ PUBLISHED" / "“that jump” is in Clips for everybody until 31 Oct." / [Watch it] [Done]. Watch it means `closeEditor(); openClips(clip.id)`. Done returns focus to the tile's ✂ if it is still in the DOM, otherwise to `document.body`. |
| `error` | A `role="alert"` line with copy from §3.13, plus [Try again] where listed. The draft is kept wherever it still exists. |

**Publish after a network error.** If the publish request fails with `network`, the editor does not offer [Try again] straight away. It shows "Checking whether it was published…" and polls `fetchClip(draft.id)` every 2 s for up to 60 s (the clip id equals the draft id).
- Found: go to `published`.
- `clip_gone` (410), or a timeout: offer [Try again]. A retry is safe either way: the server answers 409 `publishing` or 200 with the clip.
- On `publishing` (409), keep polling the same way.

**Discard** takes two clicks:
- "Discard" becomes "Click again to discard" for 4000 ms, with a 500 ms minimum before the second click counts (the `AdminStills` CONFIRM pattern).
- Then `deleteDraft`, close, and `refreshDrafts()`.

**Later, ×, Escape or backdrop.** Close and keep the draft. The tooltip on × reads "Close — your draft is kept for 2 hours". Escape while dragging a handle cancels the drag instead.

**Publish click:**
1. Capture the thumbnail on a hidden second `<video muted preload="auto">` with the same src, seeked to `(inMs + min(500, (outMs − inMs) / 2)) / 1000`.
   - On `seeked`, `drawImage` onto a 480×270 canvas: black background, contain-fit.
   - `toBlob('image/jpeg', 0.72)`, then `uploadDraftThumb`.
   - A failure or a 4 s timeout skips the thumbnail silently.
2. `publishDraft(id, {title, inMs, outMs})`.
3. Then `refreshDrafts()`.

**Playback.**
- Playback stays inside [in, out], checked on `requestAnimationFrame` while playing.
- At `out`: if Loop is on, `currentTime = in`; otherwise pause.
- Pressing play while outside the selection starts from `in`.

**Playability**, tested on mount:
- `v = video.canPlayType('video/mp4; codecs="' + draft.codecs.split(',')[0] + '"')`
- `a = codecs has opus ? video.canPlayType('audio/mp4; codecs="opus"') : 'probably'`

| Result | Shown |
|---|---|
| `v === ''`, or the video fires `error` | "This browser cannot play this clip." Publishing stays possible. |
| `v` playable, `a === ''` | "This browser will play this clip without sound." It plays. |

**Dialog shortcuts** (§3.5), active only when focus is on the stage or a trimmer thumb:
- Space: play/pause.
- I / O: set the start or end at the playhead.

### 3.7 The trimmer (`components/ClipTrimmer.tsx`)

**Props:**

```ts
{durationMs; inMs; outMs; positionMs; onChange(in, out); onSeek(ms); onScrubStart(); onScrubEnd()}
```

**The track** has three focusable `role="slider"` thumbs (the WAI-ARIA multi-thumb pattern). Outside the selection the track is dimmed. There are no keyframe ticks: the start is exact.

| Thumb | aria-label / aria-valuetext | Values | Keys |
|---|---|---|---|
| Start | "Start of the clip" / "0:38 of 2:00" | 0 .. out − 5000, in 100 ms steps | ←/→ ±1 s; Shift+←/→ and PgUp/PgDn ±10 s; Home: 0 |
| Playhead | "Position" / "0:52" | 0 .. duration (the whole draft) | ←/→ ±1 s; Shift ±10 s |
| End | "End of the clip" / "1:23 — the clip is 45 seconds" | start + 5000 .. duration, in 100 ms steps | ←/→ ±1 s; Shift and PgUp/PgDn ±10 s; End: duration |

- `aria-valuemin` and `aria-valuemax` on each handle reflect the other handle, so the handles cannot cross.
- **Dragging** a handle pauses playback and scrubs the video to the handle's position. On release it stays paused there. Tapping the bare track seeks the playhead and never moves a handle.
- **Pointer handling:** `setPointerCapture` (the `ImageCropper.tsx` pattern). Handles are drawn about 12px wide with a 44×44 transparent hit area. The track has `touch-action: none`.
- **Readout:** "0:45 of 2:00".
- **Initial selection:** in = 0, out = duration. The trimmer only shrinks the clip.

### 3.8 Sound rule (reuse `watching.ts` `getAudio()` and `setAudio()`)

- **Play with sound in the editor or the viewer**, inside the click handler:
  1. `const prev = getAudio(); setAudio(null);` and remember `prev`.
  2. `video.muted = false; video.play()`.

  The panel's layout effect mutes the live tile synchronously in the same event: React 17 batches and flushes at the end of the discrete event.
- **Pause, Close, Done, Escape, Back:** in that handler, `video.pause()`, then `if (prev && isWatching(prev)) setAudio(prev)`.
- **While a clip plays with sound,** subscribe to `useAudio()`. If it becomes non-null (someone took the sound on a tile), pause the clip and forget `prev`.
- **Tooltip on the editor's ▶:** "The clip has the sound while it plays. Pause it and the stream gets it back."

### 3.9 The Clips section

**Header button (`SidebarTitle.tsx`).**
- A `ClapperIcon` button left of the trophy, styled as `.vntv-trophy`, with `aria-haspopup="dialog"`.
- Shown when `snapshot.clips && (snapshot.clips.enabled || snapshot.clips.count > 0 || drafts.length > 0)`.
- A red count pill `.vntv-clips-count` shows the number of drafts. A small dot `.vntv-clips-new` shows when `useClipsUnseen`.
- `aria-label`: "Clips", with " — you have 1 draft" / "… 2 drafts" and/or " — new clips" appended as they apply.
- Click: `openClips()`.

**Panel row (`Sidebar.tsx`).** A one-line button `.vntv-clips-row` directly under the Live now section, about 28px tall, styled like `.vntv-live-eyebrow`. It shows a clapper icon, "CLIPS", the count, a "NEW" tag when unseen, and a chevron.
- Shown when `snapshot.clips && (count > 0 || drafts.length > 0 || a tail of a non-live handle is held)`.
- `aria-label`: "Clips, 23, new clips since you last looked".
- Click: `openClips()`.

**Unseen state.**
- `useClipsUnseen(info)` is true when `info.newest > seen`, where `seen` comes from localStorage key `vntv.clips.seen`. Every access is in try/catch; if storage is unavailable, nothing is ever "new".
- Opening the dialog calls `markClipsSeen(info.newest)`.
- This is a per-viewer convenience only; nothing goes to the server.

**`components/ClipsModal.tsx`** is a portal dialog with a clapper header icon, the title "CLIPS" and a × button. It has a list view and a viewer view.

**List view, top to bottom:**
1. **Just ended**, only when a tail is held for a handle that is not live. One row per tail:
   - `ended`: "Dave went off 2 min ago — the last 2:00 is held for 8 more minutes." with [✂ Clip the end].
   - `dropped`: "Dave's stream dropped 40 s ago — …" with [✂ Clip before the drop].
   - The button calls `startClip({handle, title}, 'tail', L, button)`.
2. **Your drafts**, only if you have any. Heading: "YOUR DRAFTS · only you can see these, kept for 2 hours". Rows read "1:58 · Dave's stream · goes in 52 min", with [Edit] (`editDraft`) and [Discard] (two clicks). A draft in `publishing` reads "Publishing…" and has no buttons.
3. **Header line.** "23 clips · kept for 30 days · 2 new", followed by:
   - A sort pill pair **Top | New** (`aria-pressed`), remembered in localStorage `vntv.clips.sort` (try/catch). The default is New. Top means most upvotes, ties newest first.
   - A filter pill **Yours** (`aria-pressed`): clips you made, or of your stream (`mine || ofMyStream`).
   - Clips newer than the previous `seen` value get a small "NEW" tag on their card.
4. **Grid** `repeat(auto-fill, minmax(200px, 1fr))` of `ClipCard`. Each card has:
   - A play button (`.vntv-clipcard-play`) containing:
     - A 16:9 thumbnail `<img loading="lazy">`. With no thumbnail, the streamer's blurred picture backdrop, as on LiveCard.
     - A duration tag (`.vntv-clipcard-dur`) showing `durationMs`.
     - An expiry tag "GOES IN 2D" when ≤ 3 days remain.
     - A one-line title. An empty title shows "{streamer.name}, 3 Oct 21:04", formatted locally from `createdAt`.
     - A meta line: clipper Avatar + name + ClipperBadge (if they hold it) · "Dave's stream" · age.
   - As a **sibling** button (a button cannot contain another), the upvote pill (§3.10).
   - The card's `aria-label`: "Play that jump — clipped by Bob from Dave's stream, 45 seconds, 3 days ago, 14 upvotes".
5. **Loading:** three ghost cards. **Error:** "Clips could not be loaded just now." with [Retry].
6. **Empty:** a muted clapper, "No clips yet.", then "Press ✂ on a stream to keep the last 2 minutes of it." When nothing is live: "Clips can be made while somebody is live."

**Data.** On open, call `fetchClips()` and `refreshDrafts()`. While the dialog is open, refetch whenever `snapshot.clips.version` changes.

**Viewer** (a view inside the same dialog, not a second dialog):
- [‹ All clips] keeps the list's scroll position.
- Escape in the viewer goes back to the list. If it was opened directly (`direct`: from the trophy, the editor or the admin console), Escape closes.
- `<video controls controlsList="nodownload" playsInline preload="metadata" poster={thumb}>`.
  - **Lead-in:** on `loadedmetadata`, set `currentTime = (leadMs || 0) / 1000`. On `ended`, set `currentTime = leadMs / 1000` and stay paused. The native timeline still shows the lead-in, which is acceptable; the duration shown elsewhere is `durationMs`.
  - The sound rule (§3.8) applies on `play` with `!muted`.
  - Playability is checked as in §3.6.
- **Below the video:**
  - The title in capitals.
  - The upvote pill.
  - "Clipped by Bob 🎬 from Dave's stream" (with avatars).
  - "0:45 · 3 days ago · goes in 27 days" (or "goes tomorrow").
  - `streamNote`, if present.
  - [Report] when `canReport`. It takes two clicks: "Click again to report this clip to the admins". Afterwards it reads "Reported".
  - [Delete] when `canDelete`. It takes two clicks:
    - clipper or admin: "Click again — its 14 upvotes stop counting";
    - streamer: "Click again to delete — Bob keeps its upvotes".

    On success, go back to the list.
- **Gone** (410, or vanished on refetch): "This clip has gone — it expired or somebody deleted it."

### 3.10 Voting UI

- **Pill** `.vntv-upvote`: "▲ 14", with `aria-pressed={voted}` and a **constant** accessible name: "Upvote “that jump”, 14 upvotes" (no title: "Upvote Dave's clip, 14 upvotes"). It does not use a name that flips together with `aria-pressed`, following the rule in `Player.tsx`.
  - Updates optimistically and reverts on error. Errors show inline in a `role="status"` line using the §3.13 copy.
  - Sends the desired state (`!voted`).
- **Your own clip:** a static count `.vntv-upvote-static`, titled "Your clip — upvotes from others count toward Best Clipper".

### 3.11 Streamer controls (`Sidebar.tsx`, in the link footer)

Shown to users with `me.clipsStreamer`, which means a manual link, while `snapshot.clips?.enabled`. Their stream is the snapshot stream with `userId === me.userId && linkSource === 'manual'`.

- **Notice line** `.vntv-clips-streamer` with a ✂ icon:
  - Allowed and recording (`clip === 'ready'`): "Clips are on: the last 2:00 of your stream is kept on this server so viewers can clip it."
  - Allowed and not live: "When you're live, the last 2:00 of your stream is kept on this server so viewers can clip it."
  - Off: "Clipping of your stream is off."
- **Switch** "Allow clips of my stream" (`role="switch"`, `aria-checked`). It calls `setClipsAllowed`. Turning it off shows the status "Off. What was held is wiped, and drafts made from it are gone."
- **[Wipe the last 2:00]**, shown while live and recording. It takes two clicks: "Click again to wipe". It calls `wipeRecording(handle)`, then shows the status "Wiped. Drafts made from it are gone; published clips are not — delete those from Clips."
- **"3 clips of your stream · View"**, shown when any clip has `ofMyStream`. It opens `openClips(undefined, 'yours')`. This line needs the list, so it is filled from the last `fetchClips()` result and not from the snapshot.

Status lines use `role="status"`, errors `role="alert"`.

### 3.12 Best Clipper: art and placements

**`ClipperBadge` in `badges.tsx`** (`{size = 22}`, viewBox 0 0 128 128, `aria-hidden`). A gold clapperboard with no digit, drawn twice like the crown: a cream halo first, then ink.

```tsx
const CLAP_BODY = 'M16 58H112V108Q112 118 102 118H26Q16 118 16 108Z';
const CLAP_ARM  = 'M14 50L104 26L109 44L19 68Z';
// pass 1: <path d={CLAP_BODY} fill={METAL[1]} stroke={CREAM} strokeWidth={14} strokeLinejoin='round'/>
//         <path d={CLAP_ARM}  fill={INK}      stroke={CREAM} strokeWidth={14} strokeLinejoin='round'/>
// pass 2: same two paths with stroke={INK} strokeWidth={6}
// slashes: <path d='M38 43L50 58M62 37L74 52M86 31L98 46' stroke={CREAM} strokeWidth={9} strokeLinecap='round'/>
// play mark: <path d='M54 74L54 106L84 90Z' fill={INK} stroke={INK} strokeWidth={6} strokeLinejoin='round'/>
```

Add `artwork/badges/clip_best.svg` (the same drawing) and a PNG, per `artwork/badges/README.md`. The line-style `ScissorsIcon` is the *action*; the clapper means *clips*.

**Where it appears.** `snapshot.bestClipper` is absent while Clips is off, so every placement below disappears with it.
- **`AwardBadges.tsx`:** after the month badge, when `snapshot.bestClipper?.userId === userId`, render `<span role="img" aria-label="Best Clipper, all time" title="Best Clipper — the most upvoted clips, all time"><ClipperBadge size={size}/></span>`. The early return must also take it into account.
- **`AwardList.tsx` `awardsFor`:** a new kind `{kind: 'clipper'; key: 'best-clipper'; upvotes; clips; icon}`, placed after the two board rows and before past months. It renders "Best Clipper" / "All time · 142 upvotes on 9 clips" with `data-rank="1"` for the gold tint. `PopoverLive` already goes through `awardsFor`.
- **`LeaderboardModal.tsx`:**
  - Rename the dialog and its button to **"Trophies"**: the title, `aria-label` and the SidebarTitle button label (Q6).
  - Add a wide section after the boards and before Monthly medals. Heading "BEST CLIPPER"; hint "Most upvotes on their clips, all time · a tie keeps the holder".
  - Row: `ClipperBadge` at 30 + Avatar + name, "142 upvotes · 9 clips", then "Best clip: “that jump” ▲ 31" with [▶ Watch]. Watch means `onClose(); openClips(bestClip.id)`. The Watch part is hidden when there is no `bestClip`.
  - The "Nobody has streamed yet." empty state is only shown when Best Clipper is also absent.
- **`SidebarTitle` `anything`:** add `|| !!snapshot.bestClipper`.
- **Not on Live Now cards.** On clip cards and in the viewer, the badge sits beside the clipper's name.

### 3.13 Error copy

The webapp keys copy by code, falling back to the server's `error` text.

| code | Copy | Action |
|---|---|---|
| `recording_failed`, `busy` | "That stream could not be clipped just now." | [Try again] (capture) |
| `network` (capture) | "That stream could not be clipped just now." | [Try again] |
| `network` (publish) | "Checking whether it was published…", then per §3.6 | — |
| `warming_up` | "Clipping starts a few seconds after a stream does. Try again in a moment." | [Try again] |
| `not_watching` | "Tune in to this stream to clip it." For a tail: "Only people who were watching when it ended can clip it." | — |
| `streamer_off` | "{name} has switched clipping off." | — |
| `not_live` | "That stream isn't live any more." | — |
| `tail_gone` | "The end of that stream is no longer held — it's kept for 10 minutes." | — |
| `not_clippable` | "This stream can't be clipped." | — |
| `draft_gone` | "This draft has expired. Clip again while the moment is still in the last 2 minutes." | — |
| `draft_wiped` | "The streamer wiped this part of their stream, so this draft is gone." | — |
| `publishing` | "This clip is already being published." Keep polling per §3.6. | — |
| `rate_limited` | "That's a lot of clips in a row. Try again in a minute." For votes: "Slow down a little." | — |
| `draft_cap` | "You already have 3 drafts. Publish or discard one first." | [Open drafts] (`openClips()`) |
| `clip_cap` | "There's no room for another clip. Delete an old one first." | — |
| `duplicate` | "You've already published most of this moment." | [Open Clips] |
| `storage_full` | "The server is out of room for clips. Ask an admin." | — |
| `user_storage_full` | "You're using your share of clip space — delete one of your clips first." | [Your clips] (`openClips(undefined, 'yours')`) |
| `storage_unavailable` | "Clips aren't set up on this server." | — |
| `clips_off` | "Clipping has been switched off." | — |
| `blocked` | "An admin has stopped you making clips." | — |
| `too_short` | "A clip has to be at least 5 seconds long." | — |
| `store`, and publish failures in general | "The clip could not be published just now. Your draft is still here." | [Try again] |
| `clip_gone` | "This clip has gone — it expired or somebody deleted it." | — |
| `own_clip` | "You can't upvote your own clip." | — |
| Playback, no video | "This browser cannot play this clip." | — |
| Playback, no audio | "This browser will play this clip without sound." | — |

"2 minutes" and "2:00" in copy always come from `lengthSeconds`.

### 3.14 Admin console (`components/AdminClips.tsx`)

Registered with `registry.registerAdminConsoleCustomSetting('clipsadmin', AdminClips, {showTitle: true})`, next to `previewpictures`. It is modelled on `AdminStills`: it acts immediately with no Save button, and refreshes every 20 s.

**Header lines:**
- Problems first, `role="alert"`: `problem` or `halted`.
- Retention, when `pendingRemovals > 0`: "Keep clips for was lowered to 3 days: 41 clips will be removed at 21:04 tomorrow. Set it back before then to keep them."
- "23 clips · 1.8 GB of 20 GB (4 GB each at most) · 410 GB free · recordings 0.4 GB · the oldest goes in 2 days · Best Clipper: Bob (142)"
- "Stored in <root>"

**Recording now.** One row per recorder, for example: "Dave — 2:00 held · 6 Mbit/s", "Erin — 0:47 held", "Carol — failing: <reason>", "Judy — busy with other streams". Each row has a [Wipe] button (two clicks: "Click again to wipe what is held of Dave").

**Held after the end.** One row per tail: "Dave — went off 3 min ago, held until 21:14".

**Reported** clips are listed first, each with [Clear reports].

**One row per clip:** thumbnail, title, and "Bob (@bob) clipped Dave · 0:45 · 3 days ago · goes in 27 days · 38 MB · ▲14 · 2 reports". Then:
- [Watch]: `openClips(id)`.
- [Remove picture], shown when there is a thumbnail. Two clicks.
- [Delete]. Two clicks: "Click again to delete — 14 upvotes stop counting".
- [Block @bob]. Two clicks: "Click again to block @bob from clipping".

**People.** Sorted by bytes. "Bob (@bob) — 4 clips · 180 MB of 4 GB · ▲31 (12 banked)", with [Reset score]. Two clicks: "Click again — Bob's upvotes so far stop counting for Best Clipper".

**Blocked list.** "Blocked from clipping", with rows and [Unblock]. When empty: "Nobody is blocked."

Status lines use `role="status"`, errors `role="alert"`.

The panel also shows `me.clipsAlert` to admins, like `voiceAlert`: "Clips need attention: <alert> Only you can see this. Put it right in **System Console → Plugins → VillaNET**."

### 3.15 Icons and styles

**`icons.tsx`** gains `ScissorsIcon`, `ClapperIcon`, `UpvoteIcon`, `PlayIcon`, `PauseIcon` and `LoopIcon`, all stroking `currentColor` in the existing `base()` style.

**New classes.** Each needs a rule in `styles.ts`; `check-styles.py` enforces it.
- HUD: `vntv-hud-tag`, `vntv-hud-busy`, `.vntv-hud-button[aria-disabled="true"]` (40% opacity).
- Editor: `vntv-clipedit`, `-stage`, `-video`, `-bigplay`, `-bar`, `-time`, `-context`, `-note`, `-title`, `-count`, `-actions`, `-progress`, `-done`, `-status`.
- Trimmer: `vntv-trim`, `-track`, `-sel`, `-dim`, `-thumb`, `-thumb-start`, `-thumb-end`, `-head`, `-readout`.
- Dialog: `vntv-clips`, `-ended`, `-drafts`, `-draft`, `-head`, `-sort`, `-filter`, `-grid`, `-empty`, `-ghost`, `-count`, `-new`, `-btn`.
- Panel: `vntv-clips-row`, `vntv-clips-streamer`.
- Cards and viewer: `vntv-clipcard`, `-play`, `-thumb`, `-dur`, `-expiry`, `-new`, `-title`, `-meta`; `vntv-upvote`, `vntv-upvote-static`; `vntv-clipview`, `-video`, `-back`, `-title`, `-meta`, `-actions`.
- Trophy dialog: `vntv-lb-clipper`.
- Admin: `vntv-admin-clips*`.

**Styling rules:**
- Reuse the modal card colours (`#080103`, `--vntv-outline`, 12px corners), the Oswald capital headings and the leaderboard gradient.
- Motion respects `prefers-reduced-motion`.
- At phone width (≤ 600px): a 16px gutter and no horizontal scroll.

### 3.16 Lab (`webapp/lab`)

**Sample media.** A committed `webapp/lab/media/sample-clip.mp4`: about 416 KB, 120.0 s, 256×144 at 12 fps, H.264 High with a 2 s GOP and no B-frames, Opus stereo. It is made by `build/make-lab-clip.sh` (dev-only ffmpeg):

```sh
ffmpeg -y -f lavfi -i "testsrc2=size=256x144:rate=12,drawtext=text='%{pts\:hms}':x=6:y=6:fontsize=16:fontcolor=white:box=1:boxcolor=black@0.6" \
  -f lavfi -i "sine=frequency=440:beep_factor=4:sample_rate=48000" -t 120 \
  -c:v libx264 -profile:v high -preset veryslow -bf 0 -g 24 -keyint_min 24 -sc_threshold 0 -b:v 18k -maxrate 40k -bufsize 80k \
  -pix_fmt yuv420p -c:a libopus -b:a 8k -ac 2 -movflags +faststart webapp/lab/media/sample-clip.mp4
```

**Wiring it in:**
- `lab/webpack.config.js` gets the rule `{test: /\.mp4$/, type: 'asset/inline'}`, and `lab/types.d.ts` declares `module '*.mp4'`.
- `mocks/client.ts` converts the data URL to a blob URL once at load (`fetch(dataUrl).then(r => r.blob())`, then `URL.createObjectURL`). It falls back to the data URL until that resolves. A same-origin blob keeps the thumbnail canvas untainted.
- **The main plugin bundle must not import it.** Only lab mocks do.
- Mock thumbnails are canvas-generated data URLs: the stream colour, the title and a play mark.

**World additions (`lab/world.ts`):**

```ts
clips: Clip[]; drafts: ClipDraft[]; clipsEnabled: boolean; clipLength: number; retentionDays: number;
bestClipper: BestClipper | null; clipsSeen: number; tails: ClipTail[]; streamerAllowed: boolean | null;
recording: 'ready' | 'warming' | 'partial' | 'truncated' | 'failed' | 'unsupported' | 'full' | 'queued' | 'off' | 'dropped';
captureMode: 'ok' | 'slow' | 'fails' | 'rate' | 'cap' | 'expired' | 'not_watching' | 'wiped';
publishMode: 'ok' | 'fails' | 'expired' | 'full' | 'share_full' | 'duplicate' | 'network_then_ok';
noSound: boolean; retentionPending: number;
```

The seed has 6 sample clips across `LAB_USERS`: one by ME, one of ME's stream, one expiring in 2 days, one with an 80-character title, one with no thumbnail, and one with a lead-in of 1.4 s and one report.

**Mock behaviour:**
- `createDraft` resolves after `latencyMs` (3000 when `slow`) to a draft with `durationMs` 120000. With `partial`, it is 47000 with `partial: true`; with `truncated`, 84000 with `truncated: true`.
- Other capture modes throw `ClipRequestError` with the matching code.
- `network_then_ok` throws `network` once; the following `fetchClip` finds the clip.
- Votes, deletes, reports, resets and opt-out mutate the world, recompute `bestClipper` with the §2.9 rules, and bump `snapshot.clips.version` and `newest`.

**A new "Clips" row of lab controls:**

| Control | Values |
|---|---|
| Clips | off · no clips · 6 clips |
| Recording | warming 0:03 · partial 0:47 · truncated · ready · failed · unsupported · full · queued · off · dropped (tail) |
| Ended tail | none · Dave went off 2 min ago |
| Capture | ok · slow · fails · rate-limited · 3 drafts · draft expired · not watching · wiped |
| Publish | ok · fails · storage full · share full · duplicate · network then ok |
| Sound | Safari no sound |
| Best Clipper | none · Bob · Victor (me) |
| Streamer controls | not linked · allowed · off |
| Unseen clips | yes · no |
| Retention pending | 0 · 41 |
| Layout | long title · 2×2 grid |

Every state in §3.4–§3.14 must be reachable in the lab.

---

## 4. Docs and version

**Version.**
- `plugin.json` `"version": "3.0.0"` and `manifestVersion = "3.0.0"`; `check-version.py` must pass.
- Update the description: "…show a red LIVE badge…, clip the last few minutes of a stream, …".
- `min_server_version` stays 9.0.0. *As built: **9.3.0**. Sharing (addendum A§4.4) refuses edits to a shared clip's message from `MessageWillBeUpdated`, and before Mattermost 9.3 the hook's answer was decoded into the edited post, so a nil refusal was lost and the edit went through [v9.3.0 server/public/plugin/client_rpc.go:681-688]. `build/check-react.py` still keys the React 17 rule off this value.*

**README.** A new top-level section **"## Clips"** after "Live now", with these sub-sections:
- **Making a clip.** ✂, the C key, who may clip (people watching, and the streamer), what is recorded and where, and clipping the end of a stream within 10 minutes.
- **Trimming.** The start is exact in VillaNET. Files begin at the previous keyframe; recommend Keyframe Interval 2 in OBS to keep them small.
- **Clips and voting.** Including the Clips row, the "new" dot and reporting.
- **Best Clipper.** The §2.9 rules in plain words:
  - all time, and a tie keeps the holder;
  - expired clips keep counting, and so do clips a streamer deletes;
  - clipper and admin deletions stop counting;
  - only active, non-bot accounts' votes count;
  - blocked people cannot hold it;
  - admins can reset a score;
  - it is hidden while Clips is off.
- **For streamers.** The notice, "Allow clips of my stream", "Wipe the last 2:00", deleting clips of your stream. All of these need a manually linked stream.
- **How long clips are kept.** Retention; removal within 10 minutes of expiry; lowering the setting waits 24 hours; drafts last 2 h; tails 10 min; everything except published clips is lost on restart.
- **What it costs.** CPU about 4% per stream; disk per minute; up to 4 streams; recordings at most 4 GB; the storage limit and each person's share.
- **When clipping doesn't work:**
  - the Mattermost host needs UDP to BB on 3478;
  - the BB URL must be the LAN address;
  - H.264 only, **no B-frames** (set B-frames to 0, and don't use the NVENC UHQ tune);
  - local file storage only;
  - **Safari is not expected to play the sound**;
  - after an OBS reconnect, a new recording starts (the old one can be clipped for 10 min);
  - simulcast layers;
  - very high bitrates give shorter clips;
  - "disk too slow".
- **Moderation.** Blocks, reports, removing pictures, score resets, wipes.

Other README changes:
- **Settings** table: the four new settings.
- **Security posture:** a paragraph saying that:
  - clips are served only to signed-in users through the plugin, and drafts are private;
  - metadata carries handles, never keys, and titles are redacted;
  - the recorder is hidden from VillaNET's counts, but **Broadcast Box's own pages count it as one viewer per recorded stream**;
  - a memory-only record of who has a playback session on which stream is kept while Clips is on, only to check who may clip, and is never shown.
- **Notes and limitations:** the limits from §1.
- **Build from source:** the two ffmpeg dev scripts and `make e2e`.

**LOCKDOWN.md, "What the plugin touches":**
- Under `POST /api/whep`, add: "also one receive-only session per recorded stream while Clips is on, opened by the Mattermost server itself. It is not counted in VillaNET's viewer counts or faces; Broadcast Box's own pages count it as one viewer per recorded stream."
- A new row: "WebRTC media, UDP from the Mattermost host to the LAN address of `BroadcastBoxURL`, port 3478 (no STUN, no TURN, no other address) | recording the rolling buffer for Clips".
- A note: CT106 already allows UDP 3478; the Mattermost host's firewall must allow outbound UDP and its replies.
- State explicitly that `/api/layer` and `/api/admin/*` are not called.

---

## 5. Phased build order

Server and webapp work in parallel against the JSON in §2.10–§2.12 and the types in §3.1.

| Phase | Server | Webapp |
|---|---|---|
| **0. Contract** (both, first) | Route stubs answering 503 `storage_unavailable`; `snapshot.clips = {enabled:false, lengthSeconds, retentionDays, count:0}`; the new settings and defaults; version 3.0.0; `rateLimiter.refund`; `make e2e` target | `types.ts`, the `client.ts` functions, the `ClipRequestError` class, a mock client with every export (`surface.ts` passes), the lab world fields and sample media |
| **1. Clips exist** | Storage root, the single KV doc, `clipsCAS` with holder recompute, cache with `rev`, list/get/video/thumb/vote/report/delete, settle, Best Clipper (eligibility, resets, hidden while off), `addClips` on all publish paths, `republishClips`, the sweep job with the retention record, admin endpoints (blocks, reset, thumb, reports), per-person usage. A **test-only** helper publishes the fixture MP4 as a clip. | Icons, `ClipperBadge`, `clips.ts`, `ClipsRoot`, the Clips dialog (list, filters, viewer with lead-in, votes, report, delete, drafts section), SidebarTitle clapper with pill and dot, the panel Clips row, the trophy section and rename, AwardBadges, AwardList, AdminClips, panel `clipsAlert`, the keyboard rule (§3.5). All of it demoed in the lab. |
| **2. Drafts** | Writer and ring (fed from the fixture, no WebRTC yet), media checks (clock, excess, gaps, B-frames, silence fill), `exportDraft` with `clickAt`, `trimClip` with `leadMs`, the drafts and publish routes with the draft state machine and rollback, the watch gate (fed by fake sessions), the drafts-directory scan | The ✂ HudButton and C key, the editor (all phases, network re-check), the trimmer, thumbnail capture, the publish flow, the sound handoff, playability messages. Every capture and publish mode in the lab. |
| **3. Recording** | pion recorder, the reader/writer split, `reconcile` from the poller with sticky slots and opt-outs, `stopAll` from config changes and the unconfigured branch, `clip`/`clipFrom`/`clipTail` annotation, tails, session hiding in `decorate`, the viewer registry in `handleWHEP` and `noteSessions`, watchdogs and backoff, the streamer endpoints, recorder and tail rows in the admin view, the e2e tests | Recording states wired to the ✂ states, the tail ✂ on WaitingTile, "Just ended", streamer controls, a 2×2 grid check |
| **4. Finish** | `make check`, `make e2e`, `make dist-linux`, README, LOCKDOWN | `make check`, `make lab`; a visual pass in the lab (narrow 320px, 400px, popped out, phone width) |
| **5. On the real host** | Deploy to 192.168.0.101 and run the acceptance criteria against BB on 192.168.0.106 | Chromium (desktop app), Firefox; Safari if available (expect no sound) |

---

## 6. Acceptance criteria

Each is checked on the real deployment unless marked (lab) or (test).

**Recording and capture**
- **A1.** With Clips on and a person streaming from OBS (H.264, no B-frames), the stream reaches `clip:"ready"` within the keyframe interval plus 5 s. VillaNET's viewer count and watcher faces do not change because of the recorder. Broadcast Box's own viewer count goes up by one, as documented. The admin view shows "Dave — 2:00 held".
- **A2.** Clicking ✂, or pressing C in the tile, opens the editor immediately in CUTTING, with focus in the title field. Within 3 s at L = 2:00 and 6 Mbit/s, it shows a draft of at most the configured length, ending at the moment of the click, with audio in sync (no visible or audible offset; tests assert ≤ 40 ms).
- **A3.** A viewer who tuned in 10 s ago can still clip the full last 2:00. A signed-in user who is not watching gets "Tune in to this stream to clip it." The linked streamer can clip their own stream without watching it.
- **A4.** Trimming:
  - The start and end move freely in 100 ms steps.
  - The handles cannot cross or make a clip shorter than 5 s.
  - In VillaNET, the published clip starts playing at the chosen start (±1 frame), its last frame is within one frame of the chosen end, and its card shows out − in.
  - Every control works from the keyboard alone, with the stated `aria-valuetext`.

**Publishing and watching**
- **A5.** Publishing produces a clip, with a thumbnail, in every other user's Clips dialog within 2 s. Their Clips row and clapper show the "new" mark. An empty title shows "Dave, 3 Oct 21:04".
- **A6.** Clips play and **seek** in the Mattermost desktop app and in Firefox; 206 responses through the plugin route are confirmed in DevTools.
- **A7.** Drafts are invisible to everyone but their owner, including the video URL.
- **A13.** No stream key appears in any snapshot, API response, KV document, file name or log line (test, plus a grep of the logs). The plugin makes no network traffic except to BB's LAN IP (HTTP 8080 and UDP 3478) and the existing Mumble and opt-in lookups.
- **A20.** A double click on PUBLISH, or publishing the same draft from two tabs, gives exactly one clip with a playable file. A publish whose response was lost is recognised as published.

**Votes and Best Clipper**
- **A8.** Upvote and un-upvote work and survive a reload. You cannot upvote your own clip. Two users voting at the same instant both count (test). Voting causes no extra `/api/status` request to BB (BB's log).
- **A9.** Best Clipper:
  - The user with the most counted upvotes holds it, and a tie keeps the holder.
  - Expired clips keep counting, and so does a streamer's deletion.
  - Clipper or admin deletion removes those upvotes.
  - Votes from deactivated accounts don't count, and blocked people can't hold it.
  - The badge appears beside the holder's name in posts, in the profile popover and in the Trophies dialog, with no runner-up anywhere.
  - It disappears everywhere while Clips is off.

  (test plus manual)

**Settings and retention**
- **A10.** The admin's Clip length dropdown offers 0:30 to 5:00 only. A value of 999 set through config.json behaves as 300. A fresh or pre-11.8 install defaults to 2:00, 30 days retention, Clips on.
- **A11.** Retention:
  - A clip older than Keep clips for disappears from the list immediately (410) and from disk within 10 minutes. Its upvotes stay in Best Clipper.
  - Lowering the setting removes nothing for 24 hours, and the admin view says what will go and when.
  - Setting it back within 24 hours keeps everything (A23).

**Degraded conditions**
- **A12.** Turning Clips off removes every ✂ within one poll, **even while BB's status endpoint is failing**. It stops the recorders (no BB session left), empties `ring/` and discards drafts. Published clips remain watchable and votable.
- **A14.** Blocking the Mattermost host's UDP to BB:
  - The ✂ shows "Clipping isn't working on this stream just now".
  - The admin sees a reason in the panel and in System Console.
  - Live viewing is unaffected.
  - Unblocking recovers without a restart.
- **A15.** OBS stops and restarts within the grace period:
  - The recorder starts a new epoch, audio is present in later clips, and `clipFrom` moves to the reconnect.
  - The ✂ offers "Clip the last 2:00 before the stream dropped" until the new recording has 5 s.
  - A 3 s publisher stall with no reconnect does **not** reset the recording; the clip shows a brief freeze with silence.
- **A16.** Disabling and enabling the plugin, or upgrading it, leaves published clips, votes and the holder intact. Rings, tails and drafts are wiped.
- **A17.** With storage set to S3, the feature reports why it is off and nothing crashes.
- **A18.** `make check` passes (vet, `test -race`, gofmt, check-react, tsc, check-styles, check-version), and so does `make e2e`. Every webapp state in §3 is reachable in the lab (lab).

**New in this revision**
- **A19.** With focus anywhere in the panel, editor or Clips dialog, pressing C, I, O or Space never puts a character in the message box and never moves focus out of the dialog.
- **A21.** A manually linked streamer:
  - sees the notice while live;
  - after switching "Allow clips of my stream" off, sees every ✂ on their stream read "<name> has switched clipping off" within one poll, and the recorder's BB session is gone;
  - after "Wipe the last 2:00", sees drafts cut from their stream disappear for everyone, and the next draft starts after the wipe.
- **A22.** After a stream ends, someone who was watching can clip its last 2:00 from the No Signal tile or from "Just ended" in Clips, for 10 minutes.
- **A23.** Lowering Keep clips for from 30 to 3 and setting it back within 24 hours deletes nothing.
- **A24.** One person cannot publish beyond their share. Other people can still publish.

---

## 7. Out of scope for 3.0.0

1. **AV1 recording.** mediacommon muxes AV1; it would need pion's `codecs.AV1Depacketizer`, temporal-delimiter stripping and per-OBU key detection.
2. **B-frame streams.** This would need composition offsets in `pmp4` without its negative-offset edit-list defect, or a different muxer.
3. **A shareable link route or "Copy link"** (`registerNeedsTeamRoute`, available in Mattermost 9.0.0's registry as `this.id + '/' + route`). See Q15.
4. **Selecting a simulcast layer** via `/api/layer`.
5. **Safari sound.** It would need AAC (a re-encode), or WebM/CAF delivery. A Safari pass, with an mp4ff fallback if Safari rejects the `avcC`.
6. **Changing the clip length per person** ("Extend to 5:00"). See Q2.
7. **A separate BB defect found in this research.** After a publisher reconnect, BB reuses the outgoing audio SSRC with the new publisher's sequence numbers, and receivers drop the audio as SRTP replays (pion logged 476 "duplicated packet" drops). **Live viewers' browsers may lose sound after an OBS reconnect** until they retune. Worth checking and reporting upstream to Glimesh/broadcast-box.

---

## Appendix A: Review resolutions

Two reviews reused the same ids. The first review's ids have a suffix, as they were given. The second review's plain ids are listed with a short name in brackets.

| Problem | Resolution | Reason (one line) |
|---|---|---|
| P1-concurrent-publish | **Accepted** (§2.10.1–2, §2.7, §3.6) | A draft state machine under `drafts.mu`, `O_EXCL` temp names, idempotent append, and undoing only your own actions make a double or retried publish safe. |
| P2-draft-file-leak | **Accepted** (§2.10.2 step 17, §2.13) | Publish now removes the draft files, and housekeeping scans `drafts/` for strays, so nothing can leak. |
| P3-publish-failure-destroys-draft | **Accepted** (§2.10.2 step 16) | A full-range rename and the thumbnail are renamed back on failure, so "your draft is still here" is true. |
| P4-disk-io-in-rtp-path | **Accepted, adapted** (§2.4.3, §2.6) | Reader/writer split with a bounded queue and a "disk too slow" cut; no fsync on trims. The threshold is 2 s on a *persistent* excess-lag rule rather than a flat 3 s, because the reader no longer blocks and the audio watchdog backs up short reconnects. |
| P5-best-clipper-cross-doc-race | **Accepted** (§2.8, §2.9) | One document with `rev`. Settle is one CAS, and the holder is recomputed in the same apply, so counts can never mix states. |
| P6-bframes-endless-reset | **Accepted** (§2.4.4, §2.6, §4) | Negative sub-second tick deltas are reported `unsupported` with an actionable reason instead of resetting forever. |
| P7-byte-cap-truncates | **Accepted** (§2.5) | The cap follows the measured bitrate (floor 12 Mbit/s, ×1.25, ≤ 1 GiB), and truncation has its own flag, copy and admin reason. |
| P8-reconcile-only-on-success | **Accepted** (§2.14) | `stopAll` runs from `OnConfigurationChange` and the unconfigured branch. `missing++` still counts only on successful polls. |
| P9-timing-pseudocode | **Accepted** (§2.6, §2.7) | The first sample sets the offset; an arrival map by RTP timestamp supplies per-sample arrival; one `ticksToMs` helper is used everywhere; and there is a 29.97/30 fps test. |
| P10-gap-wipes-ring | **Accepted** (§2.5, §2.6, §2.7) | Gaps where the clocks agree keep the epoch: video freezes until the next IDR, and audio is filled with verified Opus silence (`F8 FF FE`). |
| P11-cannot-clip-the-ending | **Accepted**, merged with P3 [clip the ending] (§2.4.8, §3.4, §3.9) | Tails held for 10 min can be clipped by people who were watching. |
| P12-safari-claim | **Accepted** (§1, §3.6, §4) | The "Safari 17+" claim is dropped. Video and audio playability are checked separately, with a "without sound" message. |
| P13-recorder-slot-churn | **Accepted**, merged with P8 [recorder slots] (§2.4.1, §2.4.5) | Slots are sticky, and resolver failures keep the last good IP set and never stop healthy recorders. |
| P14-e2e-under-make-check | **Accepted** (§2.1 Makefile, §2.19) | Real-time tests run only with `VILLANET_E2E=1` via `make e2e`, with longer timeouts under `-race`. |
| P15-bb-ui-counts-recorder | **Accepted** (§1 limit 10, §2.4.7, §4) | Confirmed in BB source (`ViewerCount: len(s.WHEPSessions)`); the wording now says only VillaNET's counts exclude it. |
| P1 [streamer consent] | **Accepted, all three controls ship** (§2.10.6, §3.11) | A notice, "Allow clips of my stream" (default on) and "Wipe" are cheap and contain accidental leaks. They need a manual link, since an unlinked streamer has no account to act as; Q4 stays open on the default. |
| P2 [watch gate] | **Accepted, adapted** (§2.4.9) | Clipping requires a counted session, using a memory-only registry kept apart from the watcher store (whose "nothing kept when off" rule stays intact), plus a 60 s grace for just-opened sessions and the streamer exemption. |
| P3 [clip the ending] | **Accepted** (§2.4.8, §3.4, §3.9) | Tails are kept for both ended and dropped epochs, with a ✂ on WaitingTile, a tail fallback on a live tile, and "Just ended" in Clips. |
| P4 [Mattermost key capture] | **Accepted** (§3.5) | Verified in Mattermost master (`use_textbox_focus.tsx`, `shouldFocusMainTextbox`) and React 17.0.2 (`preparePortalMount`). Handled keys call preventDefault/stopPropagation, and cards stop printable keys in the bubble phase only. |
| P5 [storage exhaustion] | **Accepted** (§2.10.2 step 9, §3.14) | Each person's share is max(1 GiB, limit ÷ 5), with its own code and a per-person usage table, so no new setting is needed. |
| P6 [discovery] | **Accepted in part** (§3.9) | The panel Clips row and "new" dot ship. The link route and "Copy link" stay deferred (Q15), because they add center-channel navigation that has to be verified first. |
| P7 [coarse start] | **Accepted** (§2.7, §3.7, §3.9) | `leadMs` gives an exact start in VillaNET without edit lists, and the keyframe ticks and hint are dropped. |
| P8 [recorder slots] | **Accepted** (§2.4.5, §2.4.6) | Sticky slots; newcomers by viewers, then start time; a separate `queued` state with "busy with other streams" copy. |
| P9 [click time and limiter] | **Accepted** (§2.7, §2.10.1) | `clickAt` is pinned before the slot wait, tokens are spent after the cheap checks, and `rateLimiter.refund` exists. |
| P10 [length normalisation] | **Accepted** (§2.2) | A single rule: parse error → 120, clamp to 30–300, otherwise the nearest option (a tie goes to the shorter). |
| P11 [default vs fixed length] | **Adapted** (§0, §2.2 `ringWindow`, Q2) | The ring holds only L, so no more of anyone's stream is kept than a press can take. The window is its own value, so Q2's alternative is a small change rather than a re-architecture. |
| P12 [retention typo] | **Accepted** (§2.13, §3.14) | Lowering takes effect after 24 h through a stored retention record, and the admin view states what will be removed and when. |
| P13 [thumbnail abuse] | **Accepted** (§2.10.5, §3.9, §3.14) | Uploads are logged, admins can remove a picture, and anyone can report a clip, which raises an admin alert. |
| P14 [farming] | **Accepted, all five parts** (§2.9, §2.12) | Duplicate overlap refused; only eligible voters count; blocked people cannot hold it; admin score reset; hidden while Clips is off, matching the boards. |
| P15 [vote polls BB] | **Accepted** (§2.12) | Clip writes republish a clips-only copy from the cache at most every 2 s, never poll BB, and the vote limit is now 20/min. |
| P16 [aria-pressed] | **Accepted** (§3.10) | Constant name with `aria-pressed`, per the existing `Player.tsx` rule. |
| P17 [cutting focus] | **Accepted** (§3.6) | The title is live and focused during cutting, and a status line announces cutting and ready. |

---

## Appendix B: Open questions for the owner

Each question has a default, and the default is already what this spec builds.

1. **What does "most upvotes" mean for Best Clipper?**
   - Default: **total counted upvotes across all of a person's clips, all time.**
   - Alternative: whoever made the single most-upvoted clip.
2. **Is the admin's clip time what every press captures, with people only able to trim it down?**
   - Default: **yes.** The rolling recording holds only that much of each stream.
   - Alternative: people can extend up to 5:00. Cost: every live stream's last 5 minutes held on disk, plus an "Extend to 5:00" control. This changes `ringWindow()` to 300.
3. **Deletion and the trophy.**
   - Default: expired clips keep their upvotes, and a streamer deleting a clip of their stream lets the clipper keep them.
   - A clipper deleting their own clip, or an admin deleting any clip, removes its upvotes.
4. **Streamer control.**
   - Default: a manually linked streamer is told while recorded, can switch clipping of their stream off (clipping is allowed until they do), can wipe what is held, and can delete clips of their stream.
   - Unlinked streams can only be handled by admins (wipe, delete).
   - Alternative: clipping off until the streamer opts in.
5. **The Best Clipper badge beside names in chat posts?**
   - Default: **yes**, while Clips is on. It is one title held now, worn by at most one person.
6. **Rename the trophy dialog "Top Streamers" to "Trophies"**, since a clipper isn't a streamer?
   - Default: **yes**; the boards keep their own headings.
7. **Downloads.**
   - Default: the player hides its download option (`controlsList="nodownload"`). This deters downloads; it does not prevent them.
8. **Retention.**
   - Default: 1–365 days, no "keep forever", no per-clip "Keep" pin.
   - Lowering the setting waits 24 hours before removing anything.
9. **Drafts.**
   - Default: kept 2 hours, at most 3 per person, lost if the plugin restarts.
10. **Who counts for Best Clipper?**
    - Default: deactivated accounts, bots and blocked clippers can never hold it, and votes from deactivated accounts and bots don't count.
11. **Announce new clips in a channel?**
    - Default: **no.** It was not requested, and it would post on everyone's behalf. The panel row and the "new" dot do the announcing instead.
12. **Who may clip?**
    - Default: people watching that stream in VillaNET right now (or who were watching when it ended), and the streamer.
    - Alternative: anyone signed in.
13. **Best Clipper while Clips is switched off.**
    - Default: hidden, and the holder is kept, as the boards are when the leaderboard is off.
    - Alternative: keep showing it.
14. **Each person's share of clip space.**
    - Default: a fifth of "Space for clips", at least 1 GB, with no extra setting.
    - Alternative: a "Space per person (GB)" setting.
15. **A link to a clip** (a Mattermost route that opens the panel on that clip, so a clip can be pasted into chat)?
    - Default: **not in 3.0.0.**
16. **How long the end of a stream stays clippable.**
    - Default: **10 minutes** after it goes off or drops.