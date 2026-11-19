package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Clips: a ✂ on a playing stream keeps its last few minutes as a draft, which
// the person who pressed it can trim, name and publish for other people to
// watch and upvote. The whole of it is for people who have linked their own
// stream, and the server enforces that on every route (see clips_access.go).
//
// This file holds what the rest of it is built on: the bounds, the one KV
// document every clip, vote and award lives in, the shapes the browser is
// handed, and the fixed error texts. A stream key appears in none of them. A
// stream is named by its public handle, and by a stream tag (streamTag) where
// the server alone needs to know whose it is.

const (
	kvClips         = "clips"
	clipsDocVersion = 1

	clipLengthDefault = 120 // seconds; one of clipLengthOptions
	clipMaxSeconds    = 300
	clipMinMS         = 5000 // the shortest a clip can be trimmed to

	// The settings page's other two numbers. Out of range means the default
	// below, or the most allowed above; see normalize.
	clipRetentionDefaultDays = 30
	clipRetentionMaxDays     = 365
	clipStorageDefaultGB     = 20
	clipStorageMaxGB         = 2000

	maxRecorders            = 4
	maxTails                = 4
	tailKeep                = 10 * time.Minute
	ringSegmentTarget       = 6 * time.Second
	ringFloorBytesPerSecond = 1_500_000 // 12 Mbit/s: the least any ring is sized for
	ringBitrateHeadroom     = 1.25
	ringSlack               = 20 * time.Second // segment + GOP margin
	ringMaxBytes            = 1 << 30          // one ring or tail
	ringBudgetBytes         = 4 << 30          // all rings and tails together
	maxKeyframeGap          = 60 * time.Second // a single segment over the cap, or no IDR for this long, starts a new epoch

	writerQueueSamples = 1024
	writerQueueBytes   = 16 << 20
	excessLimit        = 2 * time.Second // persistent excess lag that means a timestamp rewrite
	excessHold         = 2 * time.Second // ...for this long of arrivals
	gapMin             = time.Second     // a video tick gap this long is a publisher stall
	noVideoWatchdog    = 10 * time.Second

	draftTTL         = 2 * time.Hour
	draftTombstone   = 10 * time.Minute // expired, wiped and published drafts are remembered this long
	maxDraftsPerUser = 3
	maxDraftsTotal   = 20

	maxClipBytes      = ringMaxBytes
	maxClipsPerUser   = 30
	maxClips          = 500
	maxClipsDocBytes  = 1 << 20
	maxClipTitle      = 80      // runes
	userShareMinBytes = 1 << 30 // one person's share is the larger of this and a fifth of the limit
	duplicateOverlap  = 0.5

	exportSlots = 2
	exportWait  = 15 * time.Second

	// Attempts at a share, a draft or a publish, together, per person per
	// minute (attemptLimiter). Far above anybody clipping by hand, who also
	// meets the routes' own, smaller limits long before this one.
	clipAttemptsPerMinute = 60

	// The media core's own numbers (clips_ring.go, clips_media.go and
	// clips_mux.go), which the spec's list leaves out.

	// Room in the queue beyond writerQueueSamples that only the small notes
	// telling the writer about a cut may use.
	writerQueueReserve = 8
	// How far back the excess-lag rule looks for its least-delayed sample.
	excessBaseWindow = 60 * time.Second
	// How long after an epoch's first sample its clock may still move earlier.
	arrivalAnchorWindow = 3 * time.Second
	// The most RTP timestamps an arrival map remembers before it is cleared.
	arrivalMapLimit = 4096
	// Frames in a row with an unchanged timestamp that mean the clock broke.
	repeatedTimestampLimit = 3
	// An epoch shorter than this is not kept as a tail.
	tailMinHeld = 5 * time.Second
	// How far audio may start before or after the first frame of a draft or
	// clip; it is the half of an Opus packet that whole packets can reach.
	audioAlignSlack = 10 * time.Millisecond
	// The span the writer measures a stream's bitrate over.
	bitrateWindow = 10 * time.Second
	// Audio kept while an epoch waits for its first IDR (about 2.5 s).
	audioPrerollSamples = 128
	// Exports and trims write through a buffer this big.
	exportBufferBytes = 1 << 20
	// A trim may end this far past a draft's reported end, for rounding.
	trimEndSlackMS = 50

	videoClockRate    = 90000
	audioClockRate    = 48000
	opusFrameTicks    = 960  // 20 ms at 48 kHz
	defaultVideoTicks = 3000 // one frame at 30 fps, for a last frame whose length was never seen

	// Why an epoch ended early, as the writer reports it and the admin sees it.
	cutDiskTooSlow    = "disk too slow"
	cutRewritten      = "timestamps were rewritten (the publisher reconnected)"
	cutJumpedForward  = "timestamps jumped forward"
	cutWentBackwards  = "timestamps went backwards"
	cutStoppedMoving  = "timestamps stopped advancing"
	cutParameterSets  = "the video's parameter sets changed"
	cutCodecChanged   = "the video payload type changed"
	restartTooRare    = "keyframes too rare"
	restartWriteError = "the recording could not be written"
	restartWiped      = "the held recording was wiped"

	freeFloorBytes   = 2 << 30 // new recorders, drafts and publishes are refused below this...
	freeFloorPercent = 5       // ...or below this much of the volume
	recorderStopFree = 1 << 30 // and running recorders stop, as "full", below this

	viewerGrace  = 60 * time.Second // a session just opened counts before Broadcast Box lists it
	viewerMemory = 15 * time.Minute

	retentionGrace        = 24 * time.Hour
	minBestClipperUpvotes = 1
	clipsSweepInterval    = 10 * time.Minute
	republishEvery        = 2 * time.Second
	orphanAge             = 24 * time.Hour
	draftOrphanAge        = 10 * time.Minute
	tmpAge                = time.Hour

	dayMs            = int64(24 * time.Hour / time.Millisecond)
	retentionGraceMs = int64(retentionGrace / time.Millisecond)

	// How old this node's copy of the document may grow before a reader asks
	// the store again. Writes through this node keep the copy current on
	// their own; this is how the writes of other nodes arrive.
	clipsCacheMaxAge = time.Minute

	// What a clip's or a person's name is when the account behind it cannot be
	// read, and what a stream nobody named is called.
	unknownClipPerson = "Unknown user"
	unnamedClipStream = "Unnamed stream"
)

// Why a clip went, which decides what happens to its upvotes: kept for the
// clipper when it ran its course or its streamer took it down, gone with it
// when the clipper or an admin did.
const (
	clipGoneExpired  = "expired"
	clipGoneStreamer = "streamer"
	clipGoneClipper  = "clipper"
	clipGoneAdmin    = "admin"
)

// clipLengthOptions are the lengths the settings page offers, shortest first.
// Kept in step with ClipLengthSeconds's dropdown in plugin.json, which
// TestClipLengthNormalisation checks.
var clipLengthOptions = []int{30, 60, 90, 120, 180, 240, 300}

// What a live stream's recorder is doing, as apiStream.Clip tells the panel.
const (
	clipStateStarting    = "starting"
	clipStateReady       = "ready"
	clipStateFailed      = "failed"
	clipStateUnsupported = "unsupported"
	clipStateFull        = "full"
	clipStateQueued      = "queued"
	clipStateOff         = "off"
)

// Where a draft is cut from: the live recording, or the held end of a stream
// that has stopped, and why it stopped.
const (
	clipSourceLive = "live"
	clipSourceTail = "tail"

	clipTailEnded   = "ended"
	clipTailDropped = "dropped"
)

// clipsDoc is the one KV document behind Clips: every published clip, the votes
// on them, the votes banked from clips that have gone, the Best Clipper, and
// the admins' and streamers' switches. One document, so that a vote, a
// deletion and the award they move are always one write, and nothing can ever
// read a clip settled but its votes not yet banked.
type clipsDoc struct {
	V   int   `json:"v"`
	Rev int64 `json:"rev"`

	// Newest last. Appending an id that is already here changes nothing.
	Clips []clipEntry `json:"clips"`

	// Upvotes kept from clips that expired or that their streamer deleted, by
	// clipper. A clipper's or an admin's deletion banks nothing.
	Banked map[string]bankedScore `json:"banked,omitempty"`

	Holder *clipHolder `json:"holder,omitempty"`

	// People an admin has stopped making clips, by user id.
	Blocked map[string]clipMark `json:"blocked,omitempty"`

	// Streams whose streamer switched clipping off, by stream tag rather than
	// by person: unlinking does not quietly restart the recording of a stream
	// its streamer switched off, and relinking to another stream does not
	// carry the switch across to it.
	OptOut map[string]clipMark `json:"optOut,omitempty"`

	// Streams whose streamer turned downloads of their clips off (3.2.1,
	// design/3.2.1.md §1.3), by stream tag for the same reasons. Every clip
	// carries its stream's tag, so this reaches the clips already made as
	// well as every one to come.
	NoDownloads map[string]clipMark `json:"noDownloads,omitempty"`

	// Score resets, by clipper: votes cast before one stop counting toward
	// Best Clipper.
	Resets map[string]clipMark `json:"resets,omitempty"`

	Retention retentionRecord `json:"retention"`
}

// clipEntry is one published clip as stored. Never handed to a browser as it
// is: streamTag and shares are the server's alone, and what people see is
// built from it as a clipView or a clipCardView.
type clipEntry struct {
	ID        string `json:"id"`
	ClipperID string `json:"clipperId"`

	// Who the stream was attributed to when it was clipped, for display only.
	// Who may act as its streamer is decided by StreamTag and the current link.
	StreamerID   string `json:"streamerId,omitempty"`
	StreamerLink string `json:"streamerLink,omitempty"`
	StreamName   string `json:"streamName"`
	StreamNote   string `json:"streamNote,omitempty"`

	// The game, movie or show the stream was tagged with when it was cut,
	// from the draft; never edited afterwards. See clips_tag.go.
	Tag *clipTag `json:"tag,omitempty"`

	Handle    string `json:"handle"`
	StreamTag string `json:"streamTag"`

	Title      string `json:"title"`
	CreatedAt  int64  `json:"createdAt"`
	StartAt    int64  `json:"startAt"`
	DurationMs int64  `json:"durationMs"`
	LeadMs     int64  `json:"leadMs,omitempty"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Codecs     string `json:"codecs"`
	Bytes      int64  `json:"bytes"`
	Thumb      string `json:"thumb,omitempty"`

	// When each person voted for it, and reported it, in milliseconds.
	Voters  map[string]int64 `json:"voters,omitempty"`
	Reports map[string]int64 `json:"reports,omitempty"`

	// Every post this clip was shared in, so each can be told when it goes.
	Shares []string `json:"shares,omitempty"`
}

type bankedScore struct {
	Up       int   `json:"up"`
	Clips    int   `json:"clips"`
	LastUpAt int64 `json:"lastUpAt"`
}

type clipHolder struct {
	UserID string `json:"userId"`
	Since  int64  `json:"since"`
}

// clipMark is who did something and when: an admin blocking somebody or
// resetting their score, or a streamer switching their stream off.
type clipMark struct {
	By string `json:"by"`
	At int64  `json:"at"`
}

// retentionRecord is how long clips are kept, and what it was before it was
// last lowered, so that lowering it removes nothing for a day and a typo can
// be put right in time.
type retentionRecord struct {
	Days      int   `json:"days"`
	PrevDays  int   `json:"prevDays"`
	ChangedAt int64 `json:"changedAt"`
}

// clipsView is what every snapshot says about Clips. The same for everybody,
// like the rest of the snapshot: counts and settings, never a clip.
type clipsView struct {
	// Whether new clips can be made right now: switched on, with somewhere to
	// keep them, and a document that could be read.
	Enabled       bool   `json:"enabled"`
	LengthSeconds int    `json:"lengthSeconds"`
	RetentionDays int    `json:"retentionDays"`
	Count         int    `json:"count"`
	Newest        int64  `json:"newest,omitempty"`
	Version       string `json:"version,omitempty"`
}

// bestClipperView is the award, which is public like the crowns and medals: a
// person and a score. The clip it points at is only an id, which a reader who
// is linked can look up and one who is not cannot.
type bestClipperView struct {
	UserID      string       `json:"userId"`
	Username    string       `json:"username,omitempty"`
	DisplayName string       `json:"displayName,omitempty"`
	Upvotes     int          `json:"upvotes"`
	Clips       int          `json:"clips"`
	Since       int64        `json:"since"`
	BestClip    *bestClipRef `json:"bestClip,omitempty"`
}

type bestClipRef struct {
	ID string `json:"id"`
}

// clipTailView is the held end of a stream that has stopped, which the people
// who were watching it may still clip for a while.
type clipTailView struct {
	Handle  string `json:"handle"`
	Title   string `json:"title"`
	UserID  string `json:"userId,omitempty"`
	Reason  string `json:"reason"`
	EndedAt int64  `json:"endedAt"`
	Until   int64  `json:"until"`
	HeldMs  int64  `json:"heldMs"`
}

// clipPerson names somebody on a clip. Name is what to print; a stream nobody
// was attributed to has only that.
type clipPerson struct {
	UserID      string `json:"userId,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Name        string `json:"name"`
}

// clipView is one clip as a reader sees it, with what they may do to it: a
// linked reader, or somebody let in to watch, who may do nothing to it.
// Reports is only filled in for the admins' view.
type clipView struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	CreatedAt  int64      `json:"createdAt"`
	ExpiresAt  int64      `json:"expiresAt"`
	StartAt    int64      `json:"startAt"`
	DurationMs int64      `json:"durationMs"`
	LeadMs     int64      `json:"leadMs,omitempty"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	Codecs     string     `json:"codecs"`
	Bytes      int64      `json:"bytes"`
	Thumb      string     `json:"thumb,omitempty"`
	Clipper    clipPerson `json:"clipper"`
	Streamer   clipPerson `json:"streamer"`
	StreamNote string     `json:"streamNote,omitempty"`
	Tag        *clipTag   `json:"tag,omitempty"`
	Upvotes    int        `json:"upvotes"`
	Voted      bool       `json:"voted"`
	Mine       bool       `json:"mine"`
	OfMyStream bool       `json:"ofMyStream"`
	CanVote    bool       `json:"canVote"`
	CanDelete  bool       `json:"canDelete"`
	CanReport  bool       `json:"canReport"`
	CanShare   bool       `json:"canShare"`
	Reported   bool       `json:"reported"`
	Reports    *int       `json:"reports,omitempty"`

	// Whether this reader may download it (3.2.1, clipDownloadRefusal): the
	// admin's switch, a link, and its streamer's say. Never for somebody let
	// in, and never in the System Console's view.
	CanDownload bool `json:"canDownload"`
}

// clipCardView is what a shared clip's card shows a reader who may look at
// clips, linked or let in: enough to draw it, and from 3.2.0 its thumbs up
// (design/3.2.0.md §9.2). The count is the same for everybody, voters who
// count now, never who they are; whether this reader voted, and whether they
// may, are theirs alone, so the answer is per reader and never cached.
type clipCardView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`

	// When it goes, by the retention in force now (clipExpiresAt), as the
	// clip itself says it (clipView.ExpiresAt). A card in a reader's hands
	// can then say "expired" at the right moment without asking again and
	// without multiplying days itself. In the cards answer only: a shared
	// post's props, which a locked reader's card is drawn from, do not carry
	// it, since the retention can change after the post is made.
	ExpiresAt int64 `json:"expiresAt"`

	DurationMs int64      `json:"durationMs"`
	Thumb      string     `json:"thumb,omitempty"`
	Clipper    clipPerson `json:"clipper"`
	Streamer   clipPerson `json:"streamer"`
	Tag        *clipTag   `json:"tag,omitempty"`

	// displayedUpvotes: voters who count now; the same for everybody.
	Upvotes int `json:"upvotes"`

	// This reader has voted, which still shows for a let-in reader's vote
	// from when they were linked.
	Voted bool `json:"voted,omitempty"`

	// Linked, not a bot, and not their own clip.
	CanVote bool `json:"canVote,omitempty"`
}

// clipCardsResponse maps each id asked for to its card, or to null when that
// clip has gone.
type clipCardsResponse struct {
	Cards map[string]*clipCardView `json:"cards"`
}

// draftView is a draft as its owner sees it. Nobody else ever does.
type draftView struct {
	ID          string     `json:"id"`
	Handle      string     `json:"handle"`
	Source      string     `json:"source"`
	State       string     `json:"state"`
	CreatedAt   int64      `json:"createdAt"`
	ExpiresAt   int64      `json:"expiresAt"`
	StartAt     int64      `json:"startAt"`
	DurationMs  int64      `json:"durationMs"`
	RequestedMs int64      `json:"requestedMs"`
	Partial     bool       `json:"partial"`
	Truncated   bool       `json:"truncated"`
	Width       int        `json:"width"`
	Height      int        `json:"height"`
	Codecs      string     `json:"codecs"`
	Bytes       int64      `json:"bytes"`
	HasThumb    bool       `json:"hasThumb"`
	Streamer    clipPerson `json:"streamer"`
	StreamNote  string     `json:"streamNote,omitempty"`
	Tag         *clipTag   `json:"tag,omitempty"`
	ClipID      string     `json:"clipId,omitempty"`

	// For a draft cut from the held end of a stream: how the stream had
	// stopped, "ended" or "dropped".
	TailReason string `json:"tailReason,omitempty"`
}

// adminClipsResponse is the System Console's view of Clips on this server.
type adminClipsResponse struct {
	Problem    string `json:"problem"`
	Halted     string `json:"halted"`
	Root       string `json:"root"`
	UsedBytes  int64  `json:"usedBytes"`
	LimitBytes int64  `json:"limitBytes"`
	ShareBytes int64  `json:"shareBytes"`
	FreeBytes  int64  `json:"freeBytes"`
	TotalBytes int64  `json:"totalBytes"`
	RingBytes  int64  `json:"ringBytes"`

	Recorders []adminRecorderView `json:"recorders"`
	Tails     []clipTailView      `json:"tails"`
	Drafts    int                 `json:"drafts"`
	Clips     []clipView          `json:"clips"`
	People    []adminClipperView  `json:"people"`
	Blocked   []adminBlockedView  `json:"blocked"`
	Retention adminRetentionView  `json:"retention"`

	BestClipper *bestClipperView `json:"bestClipper"`
}

// adminRecorderView is one stream's recording. Reason is free text for admins
// only, saying why it is not recording when it is not.
type adminRecorderView struct {
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	State       string `json:"state"`
	HeldSeconds int    `json:"heldSeconds"`
	Bitrate     int64  `json:"bitrate"`
	Truncated   bool   `json:"truncated"`
	Reason      string `json:"reason"`
}

type adminClipperView struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Clips       int    `json:"clips"`
	Bytes       int64  `json:"bytes"`
	Upvotes     int    `json:"upvotes"`
	Banked      int    `json:"banked"`
}

type adminBlockedView struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	BlockedAt   int64  `json:"blockedAt"`
	By          string `json:"by"`
}

type adminRetentionView struct {
	Days            int   `json:"days"`
	PendingRemovals int   `json:"pendingRemovals"`
	RemovalAt       int64 `json:"removalAt,omitempty"`
}

// clipErrorCode is what the panel keys its own wording on. The server's text
// beside it is fixed, and never carries anything from Broadcast Box or the
// store, so an error can be shown to anybody.
type clipErrorCode string

const (
	clipErrClipsOff           clipErrorCode = "clips_off"
	clipErrBlocked            clipErrorCode = "blocked"
	clipErrForbidden          clipErrorCode = "forbidden"
	clipErrNotAllowed         clipErrorCode = "not_allowed"
	clipErrOwnClip            clipErrorCode = "own_clip"
	clipErrNotWatching        clipErrorCode = "not_watching"
	clipErrStreamerOff        clipErrorCode = "streamer_off"
	clipErrNotLive            clipErrorCode = "not_live"
	clipErrNotClippable       clipErrorCode = "not_clippable"
	clipErrWarmingUp          clipErrorCode = "warming_up"
	clipErrDraftCap           clipErrorCode = "draft_cap"
	clipErrClipCap            clipErrorCode = "clip_cap"
	clipErrDuplicate          clipErrorCode = "duplicate"
	clipErrPublishing         clipErrorCode = "publishing"
	clipErrDraftGone          clipErrorCode = "draft_gone"
	clipErrDraftWiped         clipErrorCode = "draft_wiped"
	clipErrTailGone           clipErrorCode = "tail_gone"
	clipErrClipGone           clipErrorCode = "clip_gone"
	clipErrTooShort           clipErrorCode = "too_short"
	clipErrBadRange           clipErrorCode = "bad_range"
	clipErrBadRequest         clipErrorCode = "bad_request"
	clipErrBadThumb           clipErrorCode = "bad_thumb"
	clipErrRateLimited        clipErrorCode = "rate_limited"
	clipErrRecordingFailed    clipErrorCode = "recording_failed"
	clipErrBusy               clipErrorCode = "busy"
	clipErrStorageUnavailable clipErrorCode = "storage_unavailable"
	clipErrStore              clipErrorCode = "store"
	clipErrStorageFull        clipErrorCode = "storage_full"
	clipErrUserStorageFull    clipErrorCode = "user_storage_full"
	clipErrLocked             clipErrorCode = "locked"
	clipErrWatchOnly          clipErrorCode = "watch_only"
	clipErrCannotPost         clipErrorCode = "cannot_post"
	clipErrShareCap           clipErrorCode = "share_cap"

	// 3.2.1's downloads (clips_download.go): the admin's switch is off, or
	// the clip's streamer has turned downloads of their clips off.
	clipErrDownloadsOff         clipErrorCode = "downloads_off"
	clipErrStreamerDownloadsOff clipErrorCode = "streamer_downloads_off"
)

// clipErrorText is the server's text for a code, with name, the server's name
// for the plugin (brandName), in the one that says it.
func clipErrorText(code clipErrorCode, name string) string {
	if code == clipErrLocked {
		return accessLockedText(name)
	}

	text, known := clipErrorTexts[code]
	if !known {
		// A code without a text is a mistake here, not something to show
		// anybody as an empty error.
		text = clipErrorTexts[clipErrStore]
	}

	return text
}

// clipErrorTexts is the server's fixed text for each code but locked, which
// names the plugin (clipErrorText). The status goes with the call rather
// than here, because draft_gone is a 404 for a draft nobody can see and a
// 410 for one that has expired.
var clipErrorTexts = map[clipErrorCode]string{
	clipErrClipsOff:           "Clipping has been switched off.",
	clipErrBlocked:            "An admin has stopped you making clips.",
	clipErrForbidden:          "That is not allowed.",
	clipErrNotAllowed:         "Only the person who made it, the streamer or an admin can do that.",
	clipErrOwnClip:            "You can't upvote your own clip.",
	clipErrNotWatching:        "Tune in to this stream to clip it.",
	clipErrStreamerOff:        "The streamer has switched clipping off.",
	clipErrNotLive:            "That stream is not live right now.",
	clipErrNotClippable:       "This stream can't be clipped.",
	clipErrWarmingUp:          "Clipping starts a few seconds after a stream does. Try again in a moment.",
	clipErrDraftCap:           "You already have 3 drafts. Publish or discard one first.",
	clipErrClipCap:            "There is no room for another clip. Delete an old one first.",
	clipErrDuplicate:          "You have already published most of this moment.",
	clipErrPublishing:         "This clip is already being published.",
	clipErrDraftGone:          "This draft has expired.",
	clipErrDraftWiped:         "The streamer wiped this part of their stream.",
	clipErrTailGone:           "The end of that stream is no longer held.",
	clipErrClipGone:           "This clip has gone.",
	clipErrTooShort:           "A clip has to be at least 5 seconds long.",
	clipErrBadRange:           "That request did not make sense.",
	clipErrBadRequest:         "That request did not make sense.",
	clipErrBadThumb:           "That picture is not usable.",
	clipErrRateLimited:        "Too many at once. Wait a moment and try again.",
	clipErrRecordingFailed:    "That stream could not be clipped just now.",
	clipErrBusy:               "The server is busy cutting other clips. Try again in a moment.",
	clipErrStorageUnavailable: "Clips are not set up on this server.",
	clipErrStore:              "Clips could not be saved just now. Try again.",
	clipErrStorageFull:        "The server is out of room for clips.",
	clipErrUserStorageFull:    "You are using your share of clip space. Delete one of your clips first.",
	clipErrWatchOnly:          accessWatchOnlyText,
	clipErrCannotPost:         "You can't post in that channel.",
	clipErrShareCap:           "This clip has already been shared in 10 places.",

	clipErrDownloadsOff:         "Downloads are off.",
	clipErrStreamerDownloadsOff: "The streamer has turned off downloads of their clips.",
}

type clipErrorResponse struct {
	Error string        `json:"error"`
	Code  clipErrorCode `json:"code"`

	// Only with a refusal that is about time: how long until asking again
	// can work (writeClipTooMany).
	RetryAfterMs int64 `json:"retryAfterMs,omitempty"`
}

// clipError is a refusal the panel has words for: a status and a code, and
// nothing else, so whatever went wrong behind it never reaches a browser.
type clipError struct {
	status int
	code   clipErrorCode
}

func (e *clipError) Error() string {
	return string(e.code)
}

func refuseClip(status int, code clipErrorCode) error {
	return &clipError{status: status, code: code}
}

// clipErrorFor is how an error from the clips store is answered. A refusal is
// answered as itself. Anything else — the store not answering, too many
// writers at once, a document that could not be read — is the store's
// trouble rather than the caller's, and the caller is told to try again.
func clipErrorFor(err error) (int, clipErrorCode) {
	var refusal *clipError
	if errors.As(err, &refusal) {
		return refusal.status, refusal.code
	}

	return http.StatusServiceUnavailable, clipErrStore
}

var (
	// errClipsHalted is every write, and every read with nothing cached, once
	// the document has been found unreadable. See haltClips.
	errClipsHalted = errors.New("the clips document could not be read")

	// errClipsLookUp stops a write before it is made, because the document
	// names people this node has not looked up yet. Never leaves clipsCAS.
	errClipsLookUp = errors.New("there are people to look up first")
)

// clipsUnreadableError carries the bytes that would not decode out of a
// compare-and-set, so they can be set aside once the write has given up.
type clipsUnreadableError struct {
	raw []byte
	err error
}

func (e *clipsUnreadableError) Error() string {
	return e.err.Error()
}

// clipsStore is this node's side of Clips: its copy of the document, who it
// knows to count for Best Clipper, and the limiters. Made on first use, like
// the stills', so a Plugin built in a test has one and nothing has to be set
// up for it; making it reads nothing and calls nothing.
type clipsStore struct {
	lock sync.Mutex

	// The document as this node last knew it, and when that was confirmed
	// against the store. A copy is only ever replaced by one with a higher
	// rev, so a slow read can never put back what a write here has moved on
	// from. Shared with readers, who must not change it: every write decodes
	// its own from the store's bytes.
	doc    *clipsDoc
	loaded bool
	readAt time.Time

	// The stop switch for a document that will not decode. Once thrown,
	// nothing is written to it, by this node, until the plugin restarts: the
	// bytes are kept as they are and a copy is quarantined for an admin. See
	// haltClips.
	halt recordingHalt

	// now is the clock for handlers. Everything below them takes the moment
	// as an argument.
	now func() time.Time

	captureLimiter *rateLimiter
	publishLimiter *rateLimiter
	voteLimiter    *rateLimiter
	writeLimiter   *rateLimiter
	shareLimiter   *rateLimiter

	// downloadLimiter meters download tickets (3.2.1, clips_download.go):
	// one per Download pressed, spent only by a request that is going to get
	// one.
	downloadLimiter *rateLimiter

	// attemptLimiter meters every attempt at a share, a draft or a publish,
	// refused or not, before any of them reads the whole document afresh or
	// asks Mattermost anything. Their own tokens are spent only once the
	// request is known to be good, so that a refusal costs nobody a real
	// share, draft or publish; without this, those refusals cost the server
	// a megabyte's read and a handful of calls each, as often as anybody
	// liked to ask. Generous, and shared by the three routes.
	attemptLimiter *rateLimiter

	// Who counts for Best Clipper, and who is linked, as last looked up; see
	// clips_access.go. Under peopleLock, which is never held across a call to
	// Mattermost.
	peopleLock sync.Mutex
	people     map[string]clipFacts

	// linkEpoch moves on every link change, so that an answer read before a
	// change is never kept after it. Under peopleLock.
	linkEpoch uint64

	// Whether the store is running, which is from startClips to stopClips:
	// link changes only rearm Best Clipper in between. Under runLock, which
	// also covers adding to background, so a stop that is waiting for the
	// background work is never handed more of it.
	runLock    sync.Mutex
	running    bool
	rearmed    bool
	background sync.WaitGroup

	// The sweep's schedule, and the switch that stops a sweep part way, from
	// startClips to stopClips; and the one republish that may be waiting to
	// go. Under runLock. schedule and afterFunc are how the sweep and the
	// republish are timed, which a test replaces so that nothing runs until
	// it says.
	job            clipsJob
	sweepCancel    context.CancelFunc
	republishTimer clipsTimer
	republishArmed bool
	schedule       func(api cluster.JobPluginAPI, run func()) (clipsJob, error)
	afterFunc      func(wait time.Duration, run func()) clipsTimer

	// Where clip files are kept on this node, and how much room they take,
	// and how the room left on the volume is found out, which a test
	// replaces so that its answer does not depend on the machine running it.
	// See clips_files.go.
	disk      clipsDisk
	freeSpace func(path string) (free, total uint64, ok bool)

	// Shared clips' posts: the passes the hooks check, the marks waiting to
	// land, and the shares of the last minute. See clips_share.go.
	shares clipShares

	// Every draft on this node, which is the only place a draft is: a
	// restart loses them, and the README says so. See clips_drafts.go.
	drafts clipDrafts

	// Who has been watching which stream in the panel, for the watch gate
	// and nothing else. See clips_access.go.
	viewers clipViewers

	// Where drafts are cut from: the recordings of live streams, and the
	// held ends of streams that stopped. The recorder manager, given to the
	// store when the plugin makes it; see clipRecordings.
	recordings clipRecordings

	// From startClips to stopClips: the context every export and trim runs
	// under, which stopping cancels, and the minute's housekeeping. Under
	// runLock. every is how the housekeeping is timed, which a test
	// replaces so that it runs only when the test says.
	runCtx       context.Context
	runCancel    context.CancelFunc
	housekeeping clipsJob
	every        func(interval time.Duration, run func()) clipsJob
}

func newClipsStore() *clipsStore {
	return &clipsStore{
		now:            time.Now,
		captureLimiter: newRateLimiter(5, 10*time.Minute),
		publishLimiter: newRateLimiter(10, time.Hour),
		voteLimiter:    newRateLimiter(20, time.Minute),
		writeLimiter:   newRateLimiter(30, time.Minute),
		shareLimiter:   newRateLimiter(10, 10*time.Minute),
		attemptLimiter: newRateLimiter(clipAttemptsPerMinute, time.Minute),

		downloadLimiter: newRateLimiter(clipDownloadsPerWindow, clipDownloadWindow),

		people:     map[string]clipFacts{},
		schedule:   scheduleClipsSweep,
		afterFunc:  afterClipsDelay,
		freeSpace:  freeBytes,
		recordings: noClipRecordings{},
		every:      everyClipsInterval,
	}
}

func (p *Plugin) clips() *clipsStore {
	p.clipsOnce.Do(func() {
		p.clipsState = newClipsStore()

		// Drafts are cut from what the recorder manager holds. Making it
		// asks nothing of the store, so it can be made from here.
		p.clipsState.recordings = p.recorders()
	})

	return p.clipsState
}

// markRunning is startClips's: from here on, a link change rearms Best
// Clipper in the background.
func (s *clipsStore) markRunning() {
	s.runLock.Lock()
	defer s.runLock.Unlock()

	s.running = true
}

// stopRunning is stopClips's: no more background work is started, and what
// is already running is waited for.
func (s *clipsStore) stopRunning() {
	s.runLock.Lock()
	s.running = false
	s.runLock.Unlock()

	s.background.Wait()
}

// cached is the document as this node last knew it, without asking the store:
// nil when it has never been read. For the snapshot, which is built on every
// poll and must never wait on the store or make a call of its own.
func (s *clipsStore) cached() *clipsDoc {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.doc
}

// offer is a document just read from, or written to, the store. It replaces
// this node's copy only if it is newer, but either way the copy has now been
// checked against the store.
func (s *clipsStore) offer(doc *clipsDoc, now time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if now.After(s.readAt) {
		s.readAt = now
	}
	if s.loaded && doc.Rev <= s.doc.Rev {
		return
	}

	s.doc = doc
	s.loaded = true
}

// decodeClipsDoc reads the stored bytes. Nothing stored is an empty document,
// which is how Clips starts. A document from a newer version of the plugin is
// as unreadable as a broken one: writing it back through this version's types
// would drop whatever that version added.
func decodeClipsDoc(raw []byte) (*clipsDoc, error) {
	if len(raw) == 0 {
		return &clipsDoc{V: clipsDocVersion}, nil
	}

	var doc clipsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.V > clipsDocVersion {
		return nil, fmt.Errorf("it was written by a newer version of "+productName+" (format %d)", doc.V)
	}

	return &doc, nil
}

// haltClips stops every write to a document that will not decode, keeps a
// copy of it, and says so once.
//
// The airtime rule rather than the schedule's: setAside would delete the
// document so the feature could carry on, which for a plan to stream on
// Friday is right, but this one holds every clip's votes and the Best
// Clipper, which exist nowhere else. So nothing is written over it, reads go
// on serving the last good copy this node had, and an admin fixes or deletes
// the key and restarts the plugin.
func (p *Plugin) haltClips(raw []byte, cause error) {
	if !p.clips().halt.stop(errClipsHalted.Error()) {
		return
	}

	p.quarantine(kvClips, raw)
	p.API.LogError("Clips have stopped so that the stored clips are not overwritten", "reason", errClipsHalted.Error(), "error", cause.Error())
}

// readClipsFresh reads the document from the store now, for a check that
// must not act on a copy up to a minute old: who is blocked and which streams
// are switched off, before a draft, a publish, an opt-out or a wipe, and the
// sweep. It fails closed: no answer is no answer, never an empty document.
func (p *Plugin) readClipsFresh(now time.Time) (*clipsDoc, error) {
	s := p.clips()
	if halted, _ := s.halt.state(); halted {
		return nil, errClipsHalted
	}

	raw, appErr := p.API.KVGet(kvClips)
	if appErr != nil {
		p.API.LogWarn("Could not read the clips", "error", appErr.Error())

		return nil, errStore
	}

	doc, err := decodeClipsDoc(raw)
	if err != nil {
		p.haltClips(raw, err)

		return nil, errClipsHalted
	}

	s.offer(doc, now)

	return doc, nil
}

// clipsDocument is the document for anything that only reads it: this node's
// copy, read again when it is more than a minute old.
//
// One reader does the re-read while the others carry on with the copy, and a
// re-read that fails keeps the copy: a minute-old list is better than none.
// Once halted, the last good copy is all there is.
func (p *Plugin) clipsDocument(now time.Time) (*clipsDoc, error) {
	s := p.clips()

	if halted, _ := s.halt.state(); halted {
		if doc := s.cached(); doc != nil {
			return doc, nil
		}

		return nil, errClipsHalted
	}

	s.lock.Lock()
	doc := s.doc
	due := !s.loaded || now.Sub(s.readAt) >= clipsCacheMaxAge
	if s.loaded && due {
		s.readAt = now
	}
	s.lock.Unlock()

	if !due {
		return doc, nil
	}

	if _, err := p.readClipsFresh(now); err != nil {
		if doc != nil {
			return doc, nil
		}

		return nil, err
	}

	return s.cached(), nil
}

// clipsWrite is what one attempt at a write knows besides the document: the
// moment, in milliseconds; how long clips are kept as of then; and who in the
// document counts, as the eligibility cache had it when the attempt began.
type clipsWrite struct {
	now    int64
	kept   retentionRecord
	counts clipVerdicts

	// unseen is set by a mutation whose change no panel shows — the posts a
	// clip was shared in — so that writing it pushes nothing to anybody.
	unseen bool
}

// clipsMutation changes the document and says whether it did. It may run more
// than once for one write, so it must start from nothing each time, and one
// that says it changed nothing must not have. An error refuses the write.
type clipsMutation func(doc *clipsDoc, write *clipsWrite) (bool, error)

// holderMove is a change of Best Clipper, for the log.
type holderMove struct {
	from    string
	to      string
	upvotes int
}

// clipsCAS is the only way the clips document is written.
//
// Every write works out the Best Clipper again before it is made, and the
// holder goes into the same bytes as the votes that decided it, so nothing
// can ever read a vote counted but its award not yet moved, or a clip settled
// but its votes not banked. A write that changes nothing, the holder
// included, writes nothing.
//
// Who counts comes from the eligibility cache, filled before the write and
// never during it: the compare-and-set may run its apply several times, and a
// lookup inside it would be repeated with it. people are anybody the write is
// about to add, so they are looked up first too. If the document as stored
// names somebody this call has not looked up — another node's write, landed
// in between — the write is abandoned before it is made, they are looked up,
// and it starts again, once.
//
// It answers with the document as written, or as read when nothing changed,
// which callers must not change. Refusals come back as a *clipError; see
// clipErrorFor for the rest.
func (p *Plugin) clipsCAS(now time.Time, mutate clipsMutation, people ...string) (*clipsDoc, error) {
	s := p.clips()
	if halted, _ := s.halt.state(); halted {
		return nil, errClipsHalted
	}

	nowMS := now.UnixMilli()
	keepDays := p.getConfiguration().clipRetentionDays()
	asked := map[string]bool{}
	wanted := append(clipPeopleOf(s.cached()), people...)

	for round := 0; ; round++ {
		p.lookUpClipPeople(wanted, now, true)
		for _, userID := range wanted {
			asked[userID] = true
		}

		var result *clipsDoc
		var wrote, unseen bool
		var moved *holderMove
		var missing []string

		err := p.casUpdate(kvClips, func(raw []byte) ([]byte, error) {
			result, wrote, unseen, moved, missing = nil, false, false, nil, nil

			doc, err := decodeClipsDoc(raw)
			if err != nil {
				return nil, &clipsUnreadableError{raw: raw, err: err}
			}

			write := &clipsWrite{
				now:    nowMS,
				kept:   effectiveRetention(doc.Retention, keepDays, nowMS),
				counts: s.verdictsFor(clipPeopleOf(doc), doc.Blocked),
			}

			changed, err := mutate(doc, write)
			if err != nil {
				return nil, err
			}
			unseen = write.unseen

			everyone := clipPeopleOf(doc)
			if round == 0 {
				for _, userID := range everyone {
					if !asked[userID] && s.lookUpWanted(userID, now) {
						missing = append(missing, userID)
					}
				}
				if len(missing) > 0 {
					return nil, errClipsLookUp
				}
			}

			moved = recomputeHolder(doc, s.verdictsFor(everyone, doc.Blocked), nowMS)
			if !changed && moved == nil {
				result = doc

				return nil, nil
			}

			doc.V = clipsDocVersion
			doc.Rev++
			if doc.Clips == nil {
				doc.Clips = []clipEntry{}
			}

			next, err := json.Marshal(doc)
			if err != nil {
				return nil, err
			}

			// Only a write that grows the document can be too big for it, so a
			// document that somehow already is can still be shrunk.
			if len(next) > maxClipsDocBytes && len(next) > len(raw) {
				return nil, refuseClip(http.StatusConflict, clipErrClipCap)
			}

			result, wrote = doc, true

			return next, nil
		})

		if errors.Is(err, errClipsLookUp) {
			wanted = missing

			continue
		}

		var unreadable *clipsUnreadableError
		if errors.As(err, &unreadable) {
			p.haltClips(unreadable.raw, unreadable.err)

			return nil, errClipsHalted
		}
		if err != nil {
			return nil, err
		}

		s.offer(result, now)

		if wrote && moved != nil {
			p.API.LogInfo("Best Clipper changed", "from", moved.from, "to", moved.to, "upvotes", moved.upvotes)
		}

		// Every write ends here, so every write reaches the panels, from
		// this node's copy and never by asking Broadcast Box: see
		// republishClips. All but one that changed nothing a panel shows,
		// unless Best Clipper moved with it.
		if wrote && (!unseen || moved != nil) {
			p.republishClips()
		}

		return result, nil
	}
}

// recomputeBestClipper writes the holder again if it should change and
// nothing else does: on activation, after a sweep, after a block or a score
// reset, and after somebody's link changes.
func (p *Plugin) recomputeBestClipper(now time.Time) error {
	_, err := p.clipsCAS(now, func(*clipsDoc, *clipsWrite) (bool, error) {
		return false, nil
	})

	return err
}

// clipPeopleOf is everybody the document names who could hold Best Clipper or
// move it: every clipper and voter, everybody with banked upvotes, and the
// holder. In a stable order, so that lookups and logs are the same each time.
func clipPeopleOf(doc *clipsDoc) []string {
	if doc == nil {
		return nil
	}

	seen := map[string]bool{}
	add := func(userID string) {
		if userID != "" {
			seen[userID] = true
		}
	}

	for i := range doc.Clips {
		add(doc.Clips[i].ClipperID)
		for voter := range doc.Clips[i].Voters {
			add(voter)
		}
	}
	for userID := range doc.Banked {
		add(userID)
	}
	if doc.Holder != nil {
		add(doc.Holder.UserID)
	}

	people := make([]string, 0, len(seen))
	for userID := range seen {
		people = append(people, userID)
	}
	sort.Strings(people)

	return people
}

// clip is the entry with this id, or nil.
func (doc *clipsDoc) clip(clipID string) *clipEntry {
	for i := range doc.Clips {
		if doc.Clips[i].ID == clipID {
			return &doc.Clips[i]
		}
	}

	return nil
}

// addClip puts a newly published clip at the end. One whose id is already
// here is left exactly as it is, so a publish retried after it landed — a
// double click, a second tab, a response lost on the way back — changes
// nothing.
//
// The duplicate and the caps are checked here, in the write, as well as by
// the publish before it cuts anything: two publishes racing past those early
// checks must not both land.
func (doc *clipsDoc) addClip(entry clipEntry) (bool, error) {
	if doc.clip(entry.ID) != nil {
		return false, nil
	}

	if doc.duplicateOf(&entry) != nil {
		return false, refuseClip(http.StatusConflict, clipErrDuplicate)
	}

	mine := 0
	for i := range doc.Clips {
		if doc.Clips[i].ClipperID == entry.ClipperID {
			mine++
		}
	}
	if len(doc.Clips) >= maxClips || mine >= maxClipsPerUser {
		return false, refuseClip(http.StatusConflict, clipErrClipCap)
	}

	doc.Clips = append(doc.Clips, entry)

	return true, nil
}

// duplicateOf is a clip already published by the same person, of the same
// stream, that covers more than half of this one (or of itself, when it is
// the shorter): publishing one moment over and over would farm upvotes for
// it. Expired clips not yet swept still count; they are still in the
// document, and by then the moment is long gone from every recording.
func (doc *clipsDoc) duplicateOf(entry *clipEntry) *clipEntry {
	for i := range doc.Clips {
		other := &doc.Clips[i]
		if other.ID == entry.ID || other.ClipperID != entry.ClipperID || other.Handle != entry.Handle {
			continue
		}
		if overlapShare(entry.StartAt, entry.DurationMs, other.StartAt, other.DurationMs) > duplicateOverlap {
			return other
		}
	}

	return nil
}

// overlapShare is how much two spans have in common, as a share of the
// shorter: 0 when they do not meet, 1 when one lies inside the other.
func overlapShare(start1, length1, start2, length2 int64) float64 {
	shorter := min(length1, length2)
	if shorter <= 0 {
		return 0
	}

	common := min(start1+length1, start2+length2) - max(start1, start2)
	if common <= 0 {
		return 0
	}

	return float64(common) / float64(shorter)
}

// vote sets one person's vote on a clip to what they asked for. It is the
// state they want, never a toggle, so a retried request lands where the first
// would have; asking for what is already so changes nothing.
func (doc *clipsDoc) vote(clipID, voterID string, up bool, kept retentionRecord, now int64) (bool, error) {
	entry := doc.clip(clipID)
	if entry == nil || now >= clipExpiresAt(entry.CreatedAt, kept) {
		return false, refuseClip(http.StatusGone, clipErrClipGone)
	}
	if entry.ClipperID == voterID {
		return false, refuseClip(http.StatusForbidden, clipErrOwnClip)
	}

	_, present := entry.Voters[voterID]
	if up == present {
		return false, nil
	}

	if up {
		if entry.Voters == nil {
			entry.Voters = map[string]int64{}
		}
		entry.Voters[voterID] = now
	} else {
		delete(entry.Voters, voterID)
		if len(entry.Voters) == 0 {
			entry.Voters = nil
		}
	}

	return true, nil
}

// block stops somebody making clips, and from holding Best Clipper or having
// their votes counted while it lasts. Blocking somebody already blocked keeps
// the first block, and who made it.
func (doc *clipsDoc) block(userID string, mark clipMark) bool {
	if _, blocked := doc.Blocked[userID]; blocked {
		return false
	}

	if doc.Blocked == nil {
		doc.Blocked = map[string]clipMark{}
	}
	doc.Blocked[userID] = mark

	return true
}

func (doc *clipsDoc) unblock(userID string) bool {
	if _, blocked := doc.Blocked[userID]; !blocked {
		return false
	}

	delete(doc.Blocked, userID)
	if len(doc.Blocked) == 0 {
		doc.Blocked = nil
	}

	return true
}

// resetScore starts somebody's Best Clipper score again from nothing: what
// they had banked goes, and votes already cast on their clips stop counting
// toward it. The upvotes people see on those clips are left alone.
func (doc *clipsDoc) resetScore(userID string, mark clipMark) bool {
	if doc.Banked == nil {
		doc.Banked = map[string]bankedScore{}
	}
	doc.Banked[userID] = bankedScore{}

	if doc.Resets == nil {
		doc.Resets = map[string]clipMark{}
	}
	doc.Resets[userID] = mark

	return true
}

// countedVotes is how many of a clip's upvotes count toward its clipper's Best
// Clipper score, and when the latest of them was cast: votes from people who
// count, cast after any reset of the clipper's score.
func countedVotes(entry *clipEntry, resets map[string]clipMark, counts func(string) bool) (int, int64) {
	reset, wasReset := resets[entry.ClipperID]

	count, latest := 0, int64(0)
	for voter, at := range entry.Voters {
		if voter == entry.ClipperID || !counts(voter) {
			continue
		}
		if wasReset && at <= reset.At {
			continue
		}

		count++
		latest = max(latest, at)
	}

	return count, latest
}

// displayedUpvotes is the count people see on a clip: everybody who upvoted it
// and counts now. A score reset does not touch it.
func displayedUpvotes(entry *clipEntry, counts func(string) bool) int {
	count := 0
	for voter := range entry.Voters {
		if voter != entry.ClipperID && counts(voter) {
			count++
		}
	}

	return count
}

// clipperScore is one person's standing for Best Clipper: their counted
// upvotes, on clips still here and banked from clips that have gone; how many
// clips those are; and when the latest of those upvotes was cast, which says
// who got to a score first.
type clipperScore struct {
	up        int
	clips     int
	reachedAt int64
}

// clipperScores is everybody's standing. Clips past their time that the sweep
// has not reached yet count as they are, which comes to the same as the
// banking the sweep will do.
func clipperScores(doc *clipsDoc, counts func(string) bool) map[string]clipperScore {
	scores := map[string]clipperScore{}

	for userID, banked := range doc.Banked {
		score := scores[userID]
		score.up += banked.Up
		score.clips += banked.Clips
		score.reachedAt = max(score.reachedAt, banked.LastUpAt)
		scores[userID] = score
	}

	for i := range doc.Clips {
		entry := &doc.Clips[i]
		up, latest := countedVotes(entry, doc.Resets, counts)

		score := scores[entry.ClipperID]
		score.up += up
		score.clips++
		score.reachedAt = max(score.reachedAt, latest)
		scores[entry.ClipperID] = score
	}

	return scores
}

// pickBestClipper is who holds Best Clipper: one person, the most counted
// upvotes of all time, at least one of them. A holder who is still level with
// the best keeps it, so a tie never takes it away from somebody; with no
// holder in the running, whoever got there first wins, and the smaller user
// id after that, so every node picks the same person. Nobody who does not
// count can hold it, and nobody holds it at all with no upvotes counted.
func pickBestClipper(scores map[string]clipperScore, holder string, eligible func(string) bool) string {
	best := ""
	var top clipperScore

	for userID, score := range scores {
		if score.up < minBestClipperUpvotes || !eligible(userID) {
			continue
		}

		better := best == "" ||
			score.up > top.up ||
			(score.up == top.up && score.reachedAt < top.reachedAt) ||
			(score.up == top.up && score.reachedAt == top.reachedAt && userID < best)
		if better {
			best, top = userID, score
		}
	}

	if best == "" {
		return ""
	}

	if current, held := scores[holder]; held && holder != "" && eligible(holder) && current.up == top.up {
		return holder
	}

	return best
}

// recomputeHolder moves Best Clipper if the document says it should, and
// says how. Not while anybody in it has never been looked up: counting them
// out would hand a tied title to somebody else for good on the strength of
// one failed lookup — the holder's, say, on a node with a cold cache — so
// until everybody is known, the holder stays who it was.
func recomputeHolder(doc *clipsDoc, counts clipVerdicts, now int64) *holderMove {
	for _, userID := range clipPeopleOf(doc) {
		if !counts.known(userID) {
			return nil
		}
	}

	current := ""
	if doc.Holder != nil {
		current = doc.Holder.UserID
	}

	scores := clipperScores(doc, counts.eligible)
	winner := pickBestClipper(scores, current, counts.eligible)
	if winner == current {
		return nil
	}

	if winner == "" {
		doc.Holder = nil
	} else {
		doc.Holder = &clipHolder{UserID: winner, Since: now}
	}

	return &holderMove{from: current, to: winner, upvotes: scores[winner].up}
}

// settledClip is a clip a settle removed: what the caller needs to delete its
// files, account for their space and tell the posts it was shared in.
type settledClip struct {
	ID        string
	ClipperID string
	Reason    string
	Banked    int
	Bytes     int64
	Shares    []string
}

// clipsSettlement is what one settle did: the clips it removed, and the ones
// it had to leave for now because somebody who voted for them could not be
// looked up, which a banking settle needs to know (see settleIn).
type clipsSettlement struct {
	Settled  []settledClip
	Deferred []string
}

// settleIn removes the clips asked for, each for its reason, banking the
// upvotes of those that ran their course or that their streamer took down.
// Ids not here are skipped, so settling twice is settling once.
//
// A clip already past its time is settled as expired whatever was asked:
// a clipper's delete racing the sweep must not void votes that expiry would
// have kept.
//
// A clip that banks, and that somebody voted for who has never been looked
// up, is left where it is. Banking freezes the count for good, and counting
// such a vote out would lose it for good; the sweep comes back to it, and a
// streamer deleting it is told to try again.
func settleIn(doc *clipsDoc, reasons map[string]string, kept retentionRecord, now int64, counts clipVerdicts) ([]settledClip, []string) {
	var settled []settledClip
	var deferred []string

	remaining := doc.Clips[:0]
	for _, entry := range doc.Clips {
		reason, asked := reasons[entry.ID]
		if !asked {
			remaining = append(remaining, entry)

			continue
		}

		if now >= clipExpiresAt(entry.CreatedAt, kept) {
			reason = clipGoneExpired
		}

		gone := settledClip{
			ID:        entry.ID,
			ClipperID: entry.ClipperID,
			Reason:    reason,
			Bytes:     entry.Bytes,
			Shares:    append([]string(nil), entry.Shares...),
		}

		if reason == clipGoneExpired || reason == clipGoneStreamer {
			if hasUnknownVoter(&entry, doc.Resets, counts) {
				deferred = append(deferred, entry.ID)
				remaining = append(remaining, entry)

				continue
			}

			up, latest := countedVotes(&entry, doc.Resets, counts.eligible)

			if doc.Banked == nil {
				doc.Banked = map[string]bankedScore{}
			}
			banked := doc.Banked[entry.ClipperID]
			banked.Up += up
			banked.Clips++
			banked.LastUpAt = max(banked.LastUpAt, latest)
			doc.Banked[entry.ClipperID] = banked

			gone.Banked = up
		}

		settled = append(settled, gone)
	}
	doc.Clips = remaining

	return settled, deferred
}

// hasUnknownVoter is whether a clip has a vote that would count, but for a
// voter nobody has been able to look up yet. Votes a score reset already
// ruled out do not matter either way.
func hasUnknownVoter(entry *clipEntry, resets map[string]clipMark, counts clipVerdicts) bool {
	reset, wasReset := resets[entry.ClipperID]

	for voter, at := range entry.Voters {
		if voter == entry.ClipperID || (wasReset && at <= reset.At) {
			continue
		}
		if !counts.known(voter) {
			return true
		}
	}

	return false
}

// settleClips removes clips for the reasons given, in one write. The files and
// the shared posts are the caller's, from what comes back: only after the
// write has landed, and only for what it removed.
func (p *Plugin) settleClips(reasons map[string]string, now time.Time) (clipsSettlement, error) {
	var outcome clipsSettlement

	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		outcome = clipsSettlement{}
		outcome.Settled, outcome.Deferred = settleIn(doc, reasons, write.kept, write.now, write.counts)

		return len(outcome.Settled) > 0, nil
	})
	if err != nil {
		return clipsSettlement{}, err
	}

	return outcome, nil
}

// expireClips is the sweep's write: it stores how long clips are kept as of
// now, if that has moved, and settles every clip past it as expired, all in
// one write.
func (p *Plugin) expireClips(now time.Time) (clipsSettlement, error) {
	var outcome clipsSettlement

	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		outcome = clipsSettlement{}

		changed := write.kept != doc.Retention
		doc.Retention = write.kept

		reasons := map[string]string{}
		for i := range doc.Clips {
			if write.now >= clipExpiresAt(doc.Clips[i].CreatedAt, write.kept) {
				reasons[doc.Clips[i].ID] = clipGoneExpired
			}
		}
		outcome.Settled, outcome.Deferred = settleIn(doc, reasons, write.kept, write.now, write.counts)

		return changed || len(outcome.Settled) > 0, nil
	})
	if err != nil {
		return clipsSettlement{}, err
	}

	return outcome, nil
}

// voteClip sets somebody's vote on a clip, and answers with the count as they
// now see it and whether their vote is in it. Bots are refused before
// anything is written; so is somebody whose account cannot be read, since
// there is then no telling whether they are one.
func (p *Plugin) voteClip(clipID, voterID string, up bool, now time.Time) (int, bool, error) {
	s := p.clips()

	p.lookUpClipPeople([]string{voterID}, now, false)
	facts := s.factsOf(voterID)
	if !facts.userKnown {
		return 0, false, errStore
	}
	if facts.bot || facts.gone {
		return 0, false, refuseClip(http.StatusForbidden, clipErrForbidden)
	}

	doc, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		return doc.vote(clipID, voterID, up, write.kept, write.now)
	}, voterID)
	if err != nil {
		return 0, false, err
	}

	entry := doc.clip(clipID)
	if entry == nil {
		return 0, false, refuseClip(http.StatusGone, clipErrClipGone)
	}

	_, voted := entry.Voters[voterID]
	counts := s.verdictsFor(clipPeopleOf(doc), doc.Blocked)

	return displayedUpvotes(entry, counts.eligible), voted, nil
}

// effectiveRetention is how long clips are kept as of now: the record the
// sweep would store, and what every reader goes by until it has.
//
// Lowering the setting starts a day's grace, during which clips it would
// remove are kept as long as the old setting kept them, so a 3 typed for a 30
// can be put right before anything goes; lowering it again in that day
// starts the day again. Raising it applies at once, and raising it during a
// grace keeps the grace running for anything the raise does not already
// cover.
func effectiveRetention(rec retentionRecord, cfgDays int, now int64) retentionRecord {
	if rec.Days == 0 {
		return retentionRecord{Days: cfgDays}
	}
	if rec.Days == cfgDays {
		return rec
	}

	inGrace := rec.ChangedAt > 0 && now < rec.ChangedAt+retentionGraceMs
	prev := rec.Days
	if inGrace && rec.PrevDays > prev {
		prev = rec.PrevDays
	}

	changedAt := now
	if cfgDays > rec.Days {
		if inGrace {
			changedAt = rec.ChangedAt
		} else {
			changedAt, prev = 0, 0
		}
	}

	return retentionRecord{Days: cfgDays, PrevDays: prev, ChangedAt: changedAt}
}

// clipExpiresAt is when a clip published at createdAt goes. A clip published
// before the setting was lowered keeps the old setting's time, but never past
// the end of the grace.
func clipExpiresAt(createdAt int64, rec retentionRecord) int64 {
	expires := createdAt + int64(rec.Days)*dayMs
	if rec.ChangedAt > 0 && rec.PrevDays > rec.Days && createdAt < rec.ChangedAt {
		expires = max(expires, min(createdAt+int64(rec.PrevDays)*dayMs, rec.ChangedAt+retentionGraceMs))
	}

	return expires
}

// clipViewer is who a clipView is built for. Tag is the stream tag of the
// stream they have linked, which is how a streamer is recognised; Admin is
// asked of Mattermost once per request; Console is the System Console's view,
// which may delete anything and sees how often each clip was reported.
// WatchOnly is somebody a system admin let in, who may look and do nothing
// else, admin or not, and whatever they did while they were linked.
// Downloads is the admin's switch as it stood when the request came in: with
// the document's streamers' switches, whether a clip may be downloaded
// (clipReading.viewOf).
type clipViewer struct {
	UserID    string
	Tag       string
	Admin     bool
	Bot       bool
	Console   bool
	WatchOnly bool
	Downloads bool
}

// clipNamer says who somebody is, for a view.
type clipNamer func(userID string) clipPerson

// clipViewOf is one clip as a reader sees it, linked or let in to watch.
// counts is who counts now, for the upvotes shown.
func clipViewOf(entry *clipEntry, viewer clipViewer, kept retentionRecord, counts func(string) bool, names clipNamer) clipView {
	mine := entry.ClipperID == viewer.UserID
	ofMyStream := entry.StreamTag != "" && entry.StreamTag == viewer.Tag
	_, voted := entry.Voters[viewer.UserID]
	_, reported := entry.Reports[viewer.UserID]

	view := clipView{
		ID:         entry.ID,
		Title:      entry.Title,
		CreatedAt:  entry.CreatedAt,
		ExpiresAt:  clipExpiresAt(entry.CreatedAt, kept),
		StartAt:    entry.StartAt,
		DurationMs: entry.DurationMs,
		LeadMs:     entry.LeadMs,
		Width:      entry.Width,
		Height:     entry.Height,
		Codecs:     entry.Codecs,
		Bytes:      entry.Bytes,
		Thumb:      entry.Thumb,
		Clipper:    names(entry.ClipperID),
		Streamer:   clipStreamerOf(entry, names),
		StreamNote: entry.StreamNote,
		Tag:        entry.Tag.shown(),
		Upvotes:    displayedUpvotes(entry, counts),
		Voted:      voted,
		Mine:       mine,
		OfMyStream: ofMyStream,
		CanVote:    !mine && !viewer.Bot,
		CanDelete:  viewer.Admin || mine || ofMyStream,
		CanReport:  !mine && !viewer.Bot,
		CanShare:   !viewer.Bot,
		Reported:   reported,
	}

	// Looking only. A vote from when they were linked still shows as theirs,
	// and cannot be taken back from here.
	if viewer.WatchOnly {
		view.CanVote, view.CanReport, view.CanDelete, view.CanShare = false, false, false, false
	}

	if viewer.Console {
		reports := len(entry.Reports)
		view.Reports = &reports
		view.CanDelete = true
	}

	return view
}

// clipCardOf is a shared clip's card: enough to draw it, and the same for
// every reader of the channel who may look at clips.
func clipCardOf(entry *clipEntry, names clipNamer) clipCardView {
	return clipCardView{
		ID:         entry.ID,
		Title:      entry.Title,
		CreatedAt:  entry.CreatedAt,
		DurationMs: entry.DurationMs,
		Thumb:      entry.Thumb,
		Clipper:    names(entry.ClipperID),
		Streamer:   clipStreamerOf(entry, names),
		Tag:        entry.Tag.shown(),
	}
}

// clipStreamerOf is whose stream a clip was cut from: the person it was
// attributed to when it was clipped, by today's name, or the stream's own
// name when it was nobody's or they cannot be read now.
func clipStreamerOf(entry *clipEntry, names clipNamer) clipPerson {
	name := entry.StreamName
	if name == "" {
		name = unnamedClipStream
	}

	if entry.StreamerID == "" {
		return clipPerson{Name: name}
	}

	person := names(entry.StreamerID)
	if person.Username == "" {
		return clipPerson{UserID: entry.StreamerID, Name: name}
	}

	return person
}

// bestClipperViewOf is the award as the snapshot carries it, or nil when
// nobody holds it, or while it is not shown — Clips switched off hides it,
// as the boards go while the leaderboard is off, and the holder is kept for
// when it comes back.
//
// The clip it points at is the holder's most upvoted one still here, the
// newest of those on a tie, and only by id: a linked reader can look it up,
// and one who is not gets nothing from an id.
func bestClipperViewOf(doc *clipsDoc, shown bool, kept retentionRecord, now int64, counts func(string) bool, names clipNamer) *bestClipperView {
	if !shown || doc == nil || doc.Holder == nil || doc.Holder.UserID == "" {
		return nil
	}

	holder := doc.Holder.UserID
	score := clipperScores(doc, counts)[holder]
	person := names(holder)

	view := &bestClipperView{
		UserID:      holder,
		Username:    person.Username,
		DisplayName: person.DisplayName,
		Upvotes:     score.up,
		Clips:       score.clips,
		Since:       doc.Holder.Since,
	}

	var best *clipEntry
	bestUp := 0
	for i := range doc.Clips {
		entry := &doc.Clips[i]
		if entry.ClipperID != holder || now >= clipExpiresAt(entry.CreatedAt, kept) {
			continue
		}

		up := displayedUpvotes(entry, counts)
		if up < 1 {
			continue
		}
		if best == nil || up > bestUp || (up == bestUp && entry.CreatedAt >= best.CreatedAt) {
			best, bestUp = entry, up
		}
	}
	if best != nil {
		view.BestClip = &bestClipRef{ID: best.ID}
	}

	return view
}

// clipsVersionDomain is put in front of what the version is taken over, so
// that it can never equal any other HMAC taken under the same secret.
const clipsVersionDomain = "villanet-clips-version\x00"

// clipsViewOf is what every snapshot says about Clips: the settings, how many
// clips there are to watch and when the newest was published, and a version
// that moves whenever what the list shows does — a clip, a title, an upvote
// count, a picture, when it goes — so an open list knows to fetch itself
// again. Never a
// clip itself. enabled is whether a clip can be made now, which the caller
// knows and this does not.
//
// The version is an HMAC under secret, the installation's handle secret, which
// every node of a cluster shares, so every node and every client agree on it.
// It must be keyed. The snapshot goes to everybody, linked or not, beside the
// Best Clipper's clip id and upvotes, and a picture is 8 hex or nothing, so a
// plain hash of the same lines would let somebody who may not see clips check
// guesses at a title offline. Keyed, it is only a marker that something moved.
// Without a secret there is no version at all, rather than an unkeyed one.
func clipsViewOf(doc *clipsDoc, cfg *configuration, enabled bool, now int64, counts func(string) bool, secret []byte) *clipsView {
	view := &clipsView{
		Enabled:       enabled,
		LengthSeconds: cfg.clipLengthSeconds(),
		RetentionDays: cfg.clipRetentionDays(),
	}
	if doc == nil {
		return view
	}

	kept := effectiveRetention(doc.Retention, cfg.clipRetentionDays(), now)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(clipsVersionDomain))

	// From 3.2.1 a clip's Download follows the admin's switch and its
	// streamer's (clips_download.go), so the version moves with either and an
	// open viewer asks about its clip again. Only while downloads are on: a
	// server with them off versions its clips exactly as 3.2.0 did.
	downloads := cfg.AllowClipDownloads
	if downloads {
		mac.Write([]byte("downloads\n"))
	}

	// When a clip goes is on its card (clipView.ExpiresAt), so it moves the
	// version too: an admin who changes how long clips are kept has every
	// open card asked about again. Once the sweep has stored the change it
	// does not tick, and a clip that outlives a grace has the same expiry
	// once the grace is over. Until then the grace is counted from now, which
	// moves on every poll, so the version takes the expiry without it: the
	// change moves the version once, and the sweep storing it once more.
	steady := kept
	if steady != doc.Retention {
		steady.ChangedAt = 0
	}

	for i := range doc.Clips {
		entry := &doc.Clips[i]
		if now >= clipExpiresAt(entry.CreatedAt, kept) {
			continue
		}

		view.Count++
		view.Newest = max(view.Newest, entry.CreatedAt)
		fmt.Fprintf(mac, "%s|%s|%d|%s|%d\n", entry.ID, entry.Title, displayedUpvotes(entry, counts), entry.Thumb, clipExpiresAt(entry.CreatedAt, steady))
		if downloads && clipDownloadRefusal(true, true, entry, doc) != "" {
			fmt.Fprintf(mac, "%s|no downloads\n", entry.ID)
		}
	}

	if view.Count > 0 && len(secret) > 0 {
		view.Version = hex.EncodeToString(mac.Sum(nil))[:16]
	}

	return view
}

// addClips puts what there is of Clips on a snapshot: the counts and settings
// every panel shows, the Best Clipper, and the recordings — each live
// stream's clip state and the held ends of streams (addRecordings). The last
// thing done to a snapshot before it is published, on every path, so that it
// reads this node's copy, and the recorder manager, as they are then.
//
// From memory only: no read of the store, no lookup of anybody, no log line.
// It runs on every poll, a store that has never been started included, and a
// snapshot must never wait on the store or fail because of it. A copy more
// than a minute old is not read again from here; reading is for the routes.
//
// It sets only fields of current itself, never anything current shares with
// the snapshot it was copied from, which readers hold without a lock.
func (p *Plugin) addClips(current *snapshot, cfg *configuration, now time.Time) {
	s := p.clips()
	nowMS := now.UnixMilli()
	doc := s.cached()

	halted, _ := s.halt.state()
	root, problem := s.storage()
	enabled := cfg.EnableClips && root != "" && problem == "" && !halted

	var counts func(string) bool
	var best *bestClipperView
	if doc != nil {
		counts = s.verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible
		kept := effectiveRetention(doc.Retention, cfg.clipRetentionDays(), nowMS)
		best = bestClipperViewOf(doc, cfg.EnableClips, kept, nowMS, counts, p.clipNames(now, false))
	}

	current.Clips = clipsViewOf(doc, cfg, enabled, nowMS, counts, p.handleSecret)
	current.BestClipper = best

	p.addRecordings(current, enabled, cfg, now)
}

// republishClips puts a clip write in front of every panel without waiting
// for the next poll, and without making one: Broadcast Box is not asked
// anything, the snapshot as last published is copied, given this node's
// clips, and published again.
//
// Coalesced, at most once every republishEvery: the first write arms a timer
// for the next boundary, and every write until it fires rides on it. Each
// would otherwise be a push to every connected client, and a run of votes a
// run of pushes. Nothing is armed before startClips or after stopClips.
//
// If a poll has published something newer in the meantime, publish discards
// the copy, and that poll carried the change itself.
func (p *Plugin) republishClips() {
	s := p.clips()

	s.runLock.Lock()
	defer s.runLock.Unlock()

	if !s.running || s.republishArmed {
		return
	}
	s.republishArmed = true

	now := s.now()
	wait := republishEvery - now.Sub(now.Truncate(republishEvery))
	// On the timer's own goroutine, so run safely (recovery.go). It lets the
	// republish go before anything else, and its counter by a defer.
	s.republishTimer = s.afterFunc(wait, func() { p.safely("republishing the clips", p.republishClipsNow) })
}

// republishClipsNow is the armed republish going off.
func (p *Plugin) republishClipsNow() {
	s := p.clips()

	s.runLock.Lock()
	s.republishArmed, s.republishTimer = false, nil
	if !s.running {
		s.runLock.Unlock()

		return
	}
	s.background.Add(1)
	s.runLock.Unlock()

	defer s.background.Done()

	// The roster too, as publishRosterNow puts on the clips: a roster change
	// can be copying the same snapshot at the same moment, and whichever is
	// published second must not take the other's change back.
	current := *p.getSnapshot()
	cfg := p.getConfiguration()
	p.addFromMemory(&current, cfg, s.now())
	p.publish(&current)
}
