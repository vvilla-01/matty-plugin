package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
	"layeh.com/gumble/gumble"

	"github.com/vvilla-01/matty-plugin/server/release"
)

const (
	// manifestID must match the id in plugin.json.
	manifestID = "ceo.villa.matty"

	// manifestVersion must match the version in plugin.json; build/check-version.py
	// fails the build if the two drift apart. It is recorded in the airtime
	// backup so an admin can see which build took the copy.
	manifestVersion = "4.4.0"

	// contractRevision is the number of the client contract this server
	// speaks (contract/README.md): the routes, events and documents a client
	// reads. It goes up when a client written for the last one would misread
	// this one. /access and /me say it, so that a client can tell a plugin older or newer
	// than itself from one it understands, and say so, instead of failing
	// strangely. 1 was everything up to 3.4.0, which did not say; 2 is 3.5.0:
	// the people index, the tags, GET /api/v1/access; 3 is 3.6.0: one "me"
	// event where there were "access", "twitch_link" and "account_link"; 4 is
	// 4.0.0: the plugin id ceo.villa.matty, and the server's name and artwork
	// flags in /access and "me".
	//
	// contractSince is the first version that spoke it, which is the version
	// a client out of step asks its reader to install. The two are written to
	// contract/contract.json (contract_test.go), which travels to the app
	// with the fixtures.
	contractRevision = 4
	contractSince    = "4.0.0"

	defaultLiveEmoji = "red_circle"
	defaultLiveText  = "Live now"

	// productName is the fixed product word: what the System Console lists
	// the plugin as (plugin.json's name), and what the server log and the
	// User-Agent say. What people on a server see it called is the Name
	// setting (brandName).
	productName = "Matty"

	// maxNameRunes bounds the Name setting.
	maxNameRunes = 32

	// What the plugin calls itself in Mumble, and therefore what it hides from
	// its own roster.
	defaultMumbleUsername = "Matty"

	// What the bot is called until an admin renames it. The username has to be
	// a valid Mattermost username; the display name is what shows on a post.
	// EnsureBot adopts an existing bot by USERNAME when the plugin has no
	// stored bot id. Renaming the bot from the settings page is safe, because
	// by then the plugin holds its id and patches that account.
	defaultBotUsername    = "matty-bot"
	defaultBotDisplayName = "Matty"
	defaultBotDescription = "Relays chat between Mumble and Mattermost, and posts birthdays."

	// Mattermost's own limits: model.BotDisplayNameMaxRunes (64) and
	// model.BotDescriptionMaxRunes (1024). Clamped here rather than discovered
	// as a refused patch, which would lose the username change with it.
	maxBotDisplayName   = 64
	maxBotDescription   = 1024
	defaultPollSeconds  = 10
	defaultOfflineGrace = 2
	minPollSeconds      = 3
	maxPollSeconds      = 300

	maxStatusBytes  = 4 << 20  // 4 MiB
	maxSDPBytes     = 1 << 20  // 1 MiB
	maxProfileBytes = 64 << 10 // 64 KiB

	// Broadcast Box shows the MOTD under the stream name; keep it a caption.
	maxMOTDLength = 200

	// A hostile or broken origin should not be able to hand every connected
	// client an unbounded payload.
	maxStreams = 500

	maxStreamKeyRunes = 100

	// podiumSize is how many places earn an award badge. The boards themselves
	// are no longer cut to it: every streamer's hours are listed.
	podiumSize = 3

	// maxLeaderboardRows bounds what goes into the snapshot, which is
	// serialised and pushed to every connected client whenever it changes. It
	// is far above any plausible roster here; it exists so a thousand-row table
	// could never become a thousand-row WebSocket message.
	maxLeaderboardRows = 50

	maxTrackedStreamers = 1000

	// One poll should never credit more than a long day of airtime.
	maxCreditPerPoll = int64(30 * 60 * 60)

	// How long a session mark is kept after its stream stops appearing. Long
	// enough to cover an OBS reconnect, a pause, or a restart of the origin;
	// a mark is a few dozen bytes, so there is no reason to be mean about it.
	sessionRetentionMS = int64(30 * 60 * 1000)

	// A mark's last-seen timestamp is only rewritten once it is this stale.
	// The retention window is half an hour, so a minute of slack costs nothing
	// and saves a KV write on every single poll.
	lastSeenRefreshMS = int64(60 * 1000)

	// How many status reads may fail in a row before the panel is emptied.
	// Below this the last known list is kept, with the error shown alongside.
	pollFailuresBeforeEmpty = 3

	// How long to wait before trying again to create the bot, when there is no
	// bot. The retry runs from the per-post hook, so an unpaced one is a write
	// attempt per message on the whole server.
	botRetryInterval = 30 * time.Second

	maxRateBuckets = 5000
	rateBucketTTL  = 30 * time.Minute

	httpTimeout = 15 * time.Second

	// wsEventStreams reaches the webapp as custom_ceo.villa.matty_streams.
	wsEventStreams = "streams"

	// peekHeader marks a playback session opened only to take a preview
	// picture: it carries the peek lease the panel was given (stills.go).
	peekHeader = "X-Matty-Peek"

	kvLinkByUserPrefix = "link_user_"
	kvLinkByKeyPrefix  = "link_key_"
	kvLinkTokenPrefix  = "link_token_"
	kvHandleSecret     = "handle_secret"
	kvBrandPrefix      = "brand_"
	kvStatsTotals      = "stats_totals"
	kvStatsSessions    = "stats_sessions"
	kvStatsMonthPrefix = "stats_month_"
	kvStatsSchema      = "stats_schema"
	kvStatsBackup      = "stats_backup_totals"

	// The Top Snoopers board's hours, by Mattermost user id: see watchtime.go.
	// Not under stats_, so nothing that looks after airtime mistakes them for
	// it, and the airtime export stays exactly the hours streamed.
	kvWatchTotals      = "watch_totals"
	kvWatchMonthPrefix = "watch_month_"
	kvQuarantinePrefix = "quarantine_"
	kvPrevStatusPrefix = "prevstatus_"

	// statsSchemaVersion is the format the airtime documents are written in.
	// Version 0 is the original, unversioned format; it is byte-identical to
	// version 1, which only added this marker. Bump it ONLY alongside a
	// conversion step in migrateStats, never for a cosmetic change.
	statsSchemaVersion = 1

	linkSourceManual   = "manual"
	linkSourceUsername = "username"
)

// apiStream is one live stream as handed to the webapp.
type apiStream struct {
	// StreamKey is deliberately not serialized: the browser only ever sees
	// Handle, and the plugin maps it back when relaying WHEP.
	StreamKey   string `json:"-"`
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	MOTD        string `json:"motd,omitempty"`
	Paused      bool   `json:"paused"`
	AlwaysOn    bool   `json:"alwaysOn"`
	Viewers     int    `json:"viewers"`
	Bitrate     int64  `json:"bitrate"`
	StreamStart string `json:"streamStart,omitempty"`

	VideoTracks int `json:"videoTracks"`
	AudioTracks int `json:"audioTracks"`

	// Set when the stream key could be attributed to a Mattermost account.
	UserID      string `json:"userId,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	LinkSource  string `json:"linkSource,omitempty"`

	// Place on the leaderboards, 1-3, or zero when unplaced. Not sent: what a
	// card draws for them is decided here and sent as Awards.
	AllTimeRank int `json:"-"`
	MonthRank   int `json:"-"`

	// The badges beside the streamer's name on the card, in the order they
	// are drawn: the person's own choice with this card's places, or 3.1.0's
	// card set. Filled in by publish: see awards_decide.go onCard.
	Awards []besideAward `json:"awards,omitempty"`

	// Set on a 24/7 channel that has a picture: which version of it, so the
	// panel can load it by handle and pick up a new one on the next poll.
	PictureVersion string `json:"pictureVersion,omitempty"`

	// Set on a 24/7 channel an admin has given a card picture: the wide one
	// shown on its card where a person's stream shows a preview still.
	ThumbnailVersion string `json:"thumbnailVersion,omitempty"`

	// The latest preview still of a person's live stream, and when it was
	// taken, in RFC 3339. Absent until somebody's browser has captured one,
	// and whenever Live previews is off. See stills.go.
	StillVersion string `json:"stillVersion,omitempty"`
	StillAt      string `json:"stillAt,omitempty"`

	// Who is watching it in the panel, earliest first, while Show who is
	// watching is on; absent when it is off. Viewers can be higher: somebody
	// watching some other way has no face to show. See watchers.go.
	Watchers []apiWatcher `json:"watchers,omitempty"`

	// How many more people are watching than Watchers names, for the count
	// beside the faces: one for each session nobody can be named for, and one
	// for each person known to be watching who is not in Watchers, past its
	// cap or with no name to show. Not Viewers less the faces, because Viewers
	// counts sessions, and somebody named with two tabs open would make a
	// "+1" who is nobody. Only ever beside Watchers, and absent when it is.
	Others int `json:"others,omitempty"`

	// What is on it: the movie, show or game the streamer said with the
	// footer's What's on?, or else the one linked to their plan that is on
	// now. Decided once, here, by nowPlayingFor, so that the card, the line
	// under the picture and the profile all say the same thing. Never on a
	// 24/7 channel; absent when nothing is known.
	NowPlaying *apiNowPlaying `json:"nowPlaying,omitempty"`

	// Where the stream is: "" for Broadcast Box, "twitch" for Twitch (see
	// twitch_live.go). Absent on every Broadcast Box stream, so older
	// panels, the lab and every existing test see exactly what they saw
	// before.
	Source string `json:"source,omitempty"`

	// For a Twitch stream: whose channel, for the player and the Watch on
	// Twitch link (a public name, checked ^[a-z0-9_]{1,25}$), and the
	// category Twitch lists it under.
	TwitchLogin    string `json:"twitchLogin,omitempty"`
	TwitchCategory string `json:"twitchCategory,omitempty"`

	// The Twitch user id. Never sent: the handle stands for it.
	TwitchID string `json:"-"`

	// Which relayed preview of a Twitch stream is current (twitch_preview.go).
	PreviewVersion string `json:"previewVersion,omitempty"`

	// On a Broadcast Box card: this person is ALSO live on Twitch, at this
	// login. Their Twitch card is left out of Live now.
	AlsoTwitch string `json:"alsoTwitch,omitempty"`

	// On a Twitch entry: the handle of the same person's Broadcast Box card
	// that stands in for it in Live now. The entry stays in the list, so a
	// Twitch tile somebody is watching keeps playing.
	CoveredBy string `json:"coveredBy,omitempty"`

	// Clips, while they can be made and this is not a 24/7 channel (spec
	// §2.4.6, §2.12): how the server's recording of the stream is doing —
	// starting, ready, failed, unsupported, full, queued or off — since when
	// it has held an unbroken stretch of it while ready (ms), and until when
	// the end of an earlier stretch is still held (ms), after a drop. Put on
	// by addClips from the recorder manager, never by asking anything.
	Clip     string `json:"clip,omitempty"`
	ClipFrom int64  `json:"clipFrom,omitempty"`
	ClipTail int64  `json:"clipTail,omitempty"`
}

// apiWatcher is one person watching a stream through the panel, named the way
// a streamer is named on their card.
type apiWatcher struct {
	UserID string `json:"userId"`
	Who    string `json:"who"`
}

// snapshot is the most recent view of the world, served to the webapp and
// pushed over the WebSocket whenever it changes.
type snapshot struct {
	Streams []apiStream `json:"streams"`

	// The boards, the medals, the fans, the achievements and everybody's
	// choice of awards are gathered on the snapshot as they always were, and
	// none of them is sent with it any more (json:"-"): publish lays them out
	// as GET /api/v1/boards and decides from them what goes beside each name
	// (boards.go, awards_decide.go). What the snapshot says of them is Boards.
	AllTime    []leaderEntry `json:"-"`
	Month      []leaderEntry `json:"-"`
	MonthLabel string        `json:"-"`

	// How many streamers the boards actually have, so the panel can say when
	// the row cap has hidden some rather than looking like it lost them.
	AllTimeTotal int `json:"-"`
	MonthTotal   int `json:"-"`

	// Who finished on the podium of a month that has ended, and which months,
	// for the tally in their profile and in the trophy menu. By person — user
	// id and name, as the boards carry them, never a stream key — and only for
	// people with a linked stream; absent whenever the boards are. See
	// monthmedals.go.
	MonthMedals []monthMedals `json:"-"`

	// The Top Snoopers boards: how long people have spent watching other
	// people's streams in the panel, this UTC month and all time, by person.
	// Only time watched by somebody with a linked stream, while it was
	// linked, is ever recorded; see watchtime.go. The rows are leaderEntry,
	// as on the airtime boards, so they name people and carry no stream key.
	//
	// WatchBoards says the boards are on and could be read, so the panel can
	// tell "nobody has watched yet" from "not shown". All of these are absent
	// while the setting is off.
	WatchBoards       bool          `json:"-"`
	WatchMonth        []leaderEntry `json:"-"`
	WatchAllTime      []leaderEntry `json:"-"`
	WatchMonthTotal   int           `json:"-"`
	WatchAllTimeTotal int           `json:"-"`

	// When to read the boards again: a word that moves when anything in GET
	// /api/v1/boards does other than a board's seconds. Filled in by publish.
	Boards *boardsRef `json:"boards,omitempty"`

	// When to read GET /api/v1/banners again: a word that moves whenever
	// anybody's banner is set, replaced or taken away. Always there, and
	// nothing else about banners is. Filled in by publish.
	Banners bannersRef `json:"banners"`

	// The schedule rides the snapshot rather than an event of its own. One push
	// channel, already coalesced and already fingerprinted so an unchanged
	// payload sends nothing — which is what stops somebody adding and removing
	// an entry in a loop fanning that out to every client.
	//
	// Nothing here may be specific to a viewer: this object is built once and
	// served identically to everybody, and nothing in it is formatted. Each
	// browser turns the stored instant into words in its own zone.
	Schedule []scheduleItem `json:"schedule,omitempty"`

	// Who is in voice. Absent entirely when the feature is off, so it costs
	// nothing on the servers that do not use it.
	Voice *voiceView `json:"voice,omitempty"`

	// The same people the cards, the voice section and the badges are about,
	// by user id, with the rules applied: see people.go. Filled in by
	// publish, so every snapshot that goes out has it as of that moment.
	// Always there, {} when nobody has anything to say: a client must be able
	// to tell "nobody" from "a server that sends no index".
	People map[string]personView `json:"people"`

	FetchedAt  int64  `json:"fetchedAt"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`

	// Whether the Live Now cards show preview stills at all, which is also
	// whether the panel should take any. On the snapshot rather than /me
	// because it is the same for everybody and has to reach panels that are
	// already open when an admin changes it. Absent when off.
	LivePreviews bool `json:"livePreviews,omitempty"`

	// Whether Twitch streams can appear at all, and whether they may play in
	// the panel. Here rather than on /me because it is the same for everybody
	// and has to reach open panels when an admin changes it. Absent when off.
	TwitchOn    bool `json:"twitchOn,omitempty"`
	TwitchEmbed bool `json:"twitchEmbed,omitempty"`

	// What there is of Clips: whether a clip can be made now, the lengths an
	// admin chose, and how many there are. The same for everybody, linked or
	// not, and never a clip itself. Always present once the plugin is up.
	Clips *clipsView `json:"clips,omitempty"`

	// Who holds Best Clipper, while Clips is on and somebody does: an award,
	// public like the crowns and medals, so a person and a score and only the
	// id of their best clip, which a reader who is not linked can do nothing
	// with. See addClips. With the boards, not the snapshot.
	BestClipper *bestClipperView `json:"-"`

	// The held ends of streams that have stopped, newest first, which the
	// people who were watching may still clip for a while.
	ClipTails []clipTailView `json:"clipTails,omitempty"`

	// 3.2.0 (design/3.2.0.md §2.3). Every one is absent while its feature is
	// off, so a server with everything new switched off and nobody having
	// chosen anything publishes exactly 3.1.0's snapshot. Each list is in a
	// total order, so an unchanged world publishes nothing.
	//
	// Who chose which awards to show by their name, sorted by user id: see
	// awards.go. Never sent: the choice is applied here, and what it comes
	// to is what each person's entry in People says.
	AwardChoices []awardChoice `json:"-"`

	// Each linked streamer's biggest fan, sorted by streamer id, and whether
	// biggest fans are on (the setting and Top Snoopers): see fans.go. With
	// the boards.
	Fans   []fanView `json:"-"`
	FansOn bool      `json:"-"`

	// The achievement families that are on, in catalogue order, and who
	// holds which step of each, sorted by user id: see achievements.go. With
	// the boards.
	AchievementKinds []achievementKind   `json:"-"`
	Achievements     []achievementHolder `json:"-"`

	// Present only while the soundboard is on and Mumble connected: a
	// fingerprint of the board that changes when it does and says nothing
	// about it. See sounds.go.
	Soundboard *soundboardView `json:"soundboard,omitempty"`
}

type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration     *configuration

	httpClient *http.Client

	// artworkSeen is which artwork this server has, as last read (brand.go).
	artworkSeen atomic.Pointer[brandArtworkSeen]

	// releaseFound is what the usual start found in the plugin's own folder
	// (release_check.go); releasePins, when a test sets it, the certificates
	// to check against instead of the ones built in.
	releaseFound atomic.Pointer[release.Finding]
	releasePins  func() (release.Pins, error)

	// The app check's meters, the verdicts lately read and its clock, made
	// on first use (app_check_store.go).
	appCheckOnce   sync.Once
	appCheckMemory *appCheckState

	// appCheckRoots, when a test sets it, are the roots the app check
	// accepts in place of Google's.
	appCheckRoots map[string]bool

	// What of the app check is in force, as the last settings load found it
	// (app_check_store.go: noteAppCheckInForce).
	appCheckKept appCheckInForce

	// handleSecret keys the HMAC that turns stream keys into opaque handles.
	handleSecret []byte

	// activated guards against OnConfigurationChange running its side effects
	// before the plugin is up, which Mattermost does on startup.
	activatedLock sync.Mutex
	activated     bool

	// Linking is metered hardest: a successful link proves a publish token is
	// valid, so an unmetered endpoint is a guessing oracle.
	linkLimiter     *rateLimiter
	profileLimiter  *rateLimiter
	playbackLimiter *rateLimiter

	// Schedule entries are the only thing ordinary users write, so it is the
	// first endpoint a bored person with a browser console can aim at.
	communityLimiter *rateLimiter

	// Saying what is on, metered like saving the stream title beside it:
	// each save can be a lookup at TMDB, Steam or IGDB, and every one reaches
	// every panel with the next poll.
	nowPlayingLimiter *rateLimiter

	// Looking up movies, shows and games: caches and meters, made on first
	// use. See mediaStore.
	mediaOnce  sync.Once
	mediaState *mediaStore

	// Where those pictures are kept on the disk, if anywhere: worked out at
	// activation, used only while lookups are on. See mediaDisk.
	mediaDiskOnce  sync.Once
	mediaDiskState *mediaDisk

	// The admin's Twitch application and the one app access token held for
	// it, made on first use. See twitchApp.
	twitchOnce  sync.Once
	twitchState *twitchApp

	// Steam and Hytale connections' memory and loop, made on first use. See
	// accountsStore.
	accountsOnce  sync.Once
	accountsState *accountsStore

	// refreshCh coalesces user-triggered refreshes: at most one poll is ever
	// queued, however many requests ask for one.
	refreshCh chan struct{}

	// rosterCh is a change to who is in voice, for the poller to put in
	// front of every panel without polling: see publishRosterSoon.
	rosterCh chan struct{}

	pollerLock   sync.Mutex
	pollerCancel context.CancelFunc
	pollerDone   chan struct{}

	// When the poller last ticked, in milliseconds, or zero before it ever
	// has. Outlives the poller, which every settings save restarts, so the
	// next one can tell whether its first poll is a tick. See runPoller.
	pollerTickedAt atomic.Int64

	// The Mumble connection runs on its own lifecycle rather than the poller's:
	// it is a connection that is held, not a thing that is done every fifteen
	// seconds, and it must survive a poll interval change untouched.
	voiceRunLock sync.Mutex
	voiceCancel  context.CancelFunc
	voiceDone    chan struct{}

	voiceLock  sync.RWMutex
	voice      voiceState
	voiceEpoch uint64

	// Serialises every WRITE of the roster — building one and storing it, and
	// the connection's own "dropped" and "stopped" states — so an older roster
	// can never land on top of a newer one. Separate from voiceLock because
	// building a roster makes an API call per person, and the readers of
	// voiceLock (the snapshot, /me) must not wait on those.
	voicePublishLock sync.Mutex

	// The live connection, for the text bridge to write back through. Held
	// beside the roster because they share a lifetime exactly.
	voiceClient *gumble.Client

	// The Mumble sessions this node holds for people talking from Matty, by
	// session number, which the roster asks who each one is (voice_phones.go).
	voicePhones voicePhoneRegistry

	// The voice sessions of people talking from Matty, with the budget of
	// connections to the voice server (voice_sessions.go), made on first use.
	voiceTalkOnce  sync.Once
	voiceTalkState *voiceSessions

	// Talking from Matty's UDP port (voice_rtc.go), made on first use, and
	// the worker that keeps it up (voice_api.go): one restart at a time, the
	// settings the running one started with, and, for tests, how the
	// addresses for phones are looked up (nil: the system's resolver).
	voiceRTCOnce     sync.Once
	voiceRTCState    *voiceListener
	voiceTalkRunLock sync.Mutex
	voiceTalkCancel  context.CancelFunc
	voiceTalkDone    chan struct{}
	voiceTalkRanWith *configuration
	voiceTalkLookup  voiceLookup
	voiceTalkPasses  atomic.Uint64

	// The bridge's bot account, and the channel it posts into, resolved from
	// the team/channel an admin typed.
	bridgeLock sync.RWMutex
	botUserID  string
	botProblem string
	botTriedAt time.Time

	// Channels the bot is known to be IN, and the team each one belongs to.
	// A channel id is cached the first time it resolves; without this, so was
	// a failed attempt to join it.
	channelJoined map[string]bool
	teamIDs       map[string]string
	channelIDs    map[string]string
	channelMisses map[string]time.Time

	// A circuit breaker on the Mattermost-to-Mumble leg, in case two bridges
	// ever end up pointed at each other.
	relayWindowStart time.Time
	relayCount       int
	relayStopped     bool

	// Today's date, once this node has dealt with it, so the poller's several
	// thousand daily "is it anybody's birthday?" questions cost nothing.
	birthdayLock    sync.RWMutex
	birthdayDone    string
	birthdayProblem string

	// Who is wearing a cake today, resolved once a day rather than once a poll.
	// Keyed by the date AND by the list itself, so editing the list takes
	// effect without waiting for tomorrow.
	celebrantLock sync.RWMutex
	celebrants    map[string]bool
	celebrantDay  string
	celebrantList string

	snapshotLock  sync.RWMutex
	snapshot      *snapshot
	lastPublished string

	// The count each airtime board was last logged as cut at, by board, so
	// that a cut is said once and not on every poll (leaderboard).
	boardCutsLogged sync.Map

	// The number of the snapshot last stored, counting from 1: what lets the
	// accounts store tell, once the lock is let go, which of two snapshots
	// stored at nearly the same moment is the newer (noteOnAir). Guarded by
	// snapshotLock.
	snapshotSeq uint64

	// The published snapshot's tag: its fingerprint in hex, which changes
	// exactly when a push goes out. GET /api/v1/streams answers a reader who
	// sends it back with 304. Guarded by snapshotLock.
	snapshotTag string

	// GET /api/v1/boards as the last publish laid it out, ready to send, and
	// its tag: see boards.go. Guarded by snapshotLock, and always of the
	// snapshot stored beside them.
	boardsJSON []byte
	boardsTag  string

	// Who is wearing one of the plugin's badges, and which kind, as of the
	// last reconcileBadges: see people.go.
	badgeLock sync.RWMutex
	badges    map[string]string

	// channelHandles remembers the EXACT key behind each always-on stream's
	// handle, so a 24/7 channel that drops off the air keeps its picture on
	// the tile waiting for it. A handle is an HMAC of the key exactly as
	// Broadcast Box relays it, and the always-on setting is case-folded, so it
	// cannot be rebuilt from the setting alone.
	channelHandlesLock sync.Mutex
	channelHandles     map[string]string

	// Which version of each 24/7 channel's two pictures is current, so the
	// poller does not read, decode and hash every picture from the KV store on
	// every poll just to learn that nothing has changed. See
	// channelImageVersion.
	channelVersionsLock sync.Mutex
	channelVersions     map[string]channelVersion
	channelVersionsGen  uint64

	// The preview stills, their leases and the peek sessions: made on first
	// use. See stills.
	stillsOnce  sync.Once
	stillsState *stillStore

	// Profile banners: where they are kept, the list as last read and the
	// meter on uploads. See banners.go.
	bannersOnce  sync.Once
	bannersState *bannerStore

	// Twitch live: who is live on Twitch, the link document as last read, the
	// loop that asks Twitch and the worker that purges what people withdrew.
	// Made on first use, and outliving the poller. See twitchLive.
	twitchLiveOnce  sync.Once
	twitchLiveState *twitchStore

	// Whose Twitch entry is covered by their Broadcast Box card in Live now,
	// with a grace each way so a simulcaster's card does not swap handles on
	// every OBS reconnect. Reset when the poller restarts. See mergeStreams.
	twitchCoverLock sync.Mutex
	twitchCovers    map[string]twitchCover

	// Clips: this node's copy of the clips document, who counts for Best
	// Clipper, and the limiters, made on first use. See clips.
	clipsOnce  sync.Once
	clipsState *clipsStore

	// The recorder manager: which streams are recorded for clipping, and the
	// held ends of those that stopped. Made on first use, and outliving the
	// poller, which every settings save restarts. See clips_manager.go.
	recordersOnce  sync.Once
	recordersState *clipRecorders

	// Who may use the panel, as this node last heard (see access.go), and its
	// copy of the let-in list (see letin.go). Both made on first use.
	accessOnce  sync.Once
	accessState *accessStore
	letInOnce   sync.Once
	letInState  *letInCopy

	// Which Broadcast Box sessions the panel opened, and for whom, so a card
	// can show who is watching: made on first use. See watchers.
	watchersOnce  sync.Once
	watchersState *watcherStore

	// offlineStreak counts consecutive ticks in which a badged user was not
	// seen live, so a brief OBS reconnect does not flap their status.
	offlineLock   sync.Mutex
	offlineStreak map[string]int

	// What is on, per streamer: how many ticks each one's stream has been off
	// the air, counted the way offlineStreak is but kept apart from it, since
	// that one only runs while the live badge is on. And the entries as last
	// read, for a poll whose read of them fails. See nowplaying.go.
	nowPlayingLock sync.Mutex
	nowPlayingOff  map[string]offAirCount
	nowPlayingLast map[string]nowPlayingEntry

	// statsHalted stops airtime recording when the stored history could not be
	// read. Carrying on would mean writing a fresh, empty table over years of
	// accumulated hours, so nothing is written at all until an admin has had a
	// chance to look.
	statsLock   sync.Mutex
	statsHalted bool
	statsCause  string
	statsReason string

	// statsResyncAt is when a settings change last made backdated airtime
	// unwanted. Every live stream is brought up to date against it ONCE,
	// tracked per session rather than as a single flag: a flag is consumed by
	// whichever poll happens to run next, even one that saw none of the
	// streams it was meant to apply to.
	statsResyncAt int64

	// lastPollAt is when this process last read a status listing, in
	// milliseconds. It is the evidence for how far back a newly seen session
	// may reach: in-memory on purpose, because a fresh process genuinely has
	// not been watching, which is what makes recovery after a restart right.
	lastPollAt int64

	// statsWriteLock serialises the read-modify-write of the airtime tables.
	// The poller is the usual writer; an admin restoring a backup or importing
	// a file is the other, and without this one of them silently wins. The
	// Top Snoopers tables are written under it too.
	statsWriteLock sync.Mutex

	// The Top Snoopers board's own stop switch, kept apart from airtime's so
	// that a damaged watching table can never stop the hours streamed being
	// recorded. And when its last credit pass ran, in milliseconds, under
	// statsWriteLock. See watchtime.go.
	watchHalt   recordingHalt
	watchPollAt int64

	// The Twitch ledger's own stop switch, apart from both of those: a
	// damaged Twitch table never stops Broadcast Box hours being recorded.
	// And when Twitch live was last switched on, for twitchResyncEpoch, under
	// statsLock. See twitch_air.go.
	twitchAirHalt  recordingHalt
	twitchResyncAt int64

	// watchShown is what the Top Snoopers boards were last built from, so
	// that with faces off their figures move once an hour rather than on
	// every poll. See watchTablesShown.
	watchShown watchTables

	// endedPodiums is the top three of every month that has ended, by stream
	// key, for the medal tally in people's profiles. Kept in memory because
	// finding them means reading every month since 2020. See monthmedals.go.
	endedPodiums podiumCache

	// pollFails counts consecutive failed status reads on ticks, so a brief
	// outage keeps the last known list on screen instead of emptying every
	// panel.
	pollFailLock sync.Mutex
	pollFails    int

	// 3.2.0's meters, made on first use. See limiters.
	limitersOnce  sync.Once
	limitersState *featureLimiters

	// This node's copy of who chose which awards to show and who chose not
	// to be named as a fan. See awards.go.
	awardsOnce  sync.Once
	awardsState *awardDocs

	// The biggest-fan and 24/7 channel ledgers as the last credit pass
	// saved them, and when either was last started from zero. See fans.go.
	ledgersOnce  sync.Once
	ledgersState *fanLedgers

	// The figures achievements are worked out from, as the last refresh
	// had them, swapped whole. See achievements.go.
	achievementsLock  sync.Mutex
	achievementFacts  achievementFigures
	achievementsAlert string

	// The soundboard: this node's copy of the board, the cache of sounds'
	// packets, the gate every press goes through, and what the last check
	// of VillaNET's own Mumble session found. See sounds.go.
	soundsOnce  sync.Once
	soundsState *soundStore

	// Push notifications: this node's key, client, lanes and meters, made
	// on first use. See push.go and push_send.go.
	pushOnce  sync.Once
	pushState *pushRuntime

	// Updates for the plugin itself: the last look, the daily job and a
	// bundle being prepared, made on first use. See update.go.
	updateOnce  sync.Once
	updateState *updateRuntime

	// The player on the Mumble connection held now, under voiceLock beside
	// voiceClient, with which it shares a lifetime. See sounds_play.go.
	soundPlayer *soundPlayer
}

// featureLimiters are 3.2.0's meters, per person unless said otherwise. Made
// on first use rather than in OnActivate, so that a route a test reaches
// without activating the plugin is metered like any other.
type featureLimiters struct {
	// Choosing awards, going back to Automatic and opting in or out of
	// biggest fans, together.
	award *rateLimiter

	// Sharing a plan into a channel: a post as the sharer each time.
	planShare *rateLimiter

	// The soundboard: uploads, renames and deletes, presses, and fetching
	// your own sound's audio for Listen here. The server-wide eight a minute
	// lives in the gate, which takes it under its own lock (sounds_gate.go).
	soundUpload *rateLimiter
	soundEdit   *rateLimiter
	soundPress  *rateLimiter
	soundAudio  *rateLimiter

	// Talking from Matty: joins, and moves and mute changes together.
	voiceJoin   *rateLimiter
	voiceChange *rateLimiter
}

func (p *Plugin) limiters() *featureLimiters {
	p.limitersOnce.Do(func() {
		p.limitersState = &featureLimiters{
			award:       newRateLimiter(10, time.Minute),
			planShare:   newRateLimiter(10, 10*time.Minute),
			soundUpload: newRateLimiter(20, time.Hour),
			soundEdit:   newRateLimiter(30, time.Minute),
			soundPress:  newRateLimiter(30, time.Minute),
			soundAudio:  newRateLimiter(60, time.Minute),
			voiceJoin:   newRateLimiter(6, time.Minute),
			voiceChange: newRateLimiter(60, time.Minute),
		}
	})

	return p.limitersState
}

// OnActivate makes what lives in memory, then runs the usual start.
func (p *Plugin) OnActivate() error {
	// Only ever a sentence in a build made for a developer's machine
	// (standin.go), which must not start on a server people use.
	if problem := p.standInRefusal(); problem != "" {
		return errors.New(problem)
	}

	p.prepareMemory()
	p.startUsual()

	return nil
}

// prepareMemory makes what the plugin holds in memory: the client it asks
// others with, its meters and its channels. It reads and writes nothing.
func (p *Plugin) prepareMemory() {
	p.httpClient = &http.Client{
		Timeout: httpTimeout,

		// Never follow a redirect: every request here targets a specific API on
		// a specific host, and following one could send the Authorization
		// header — a publish token — somewhere the admin did not configure.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},

		// Bound concurrent sockets to the origin so a burst of playback
		// requests cannot exhaust its accept queue.
		Transport: &http.Transport{
			MaxConnsPerHost:     32,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     60 * time.Second,
		},
	}
	p.offlineStreak = map[string]int{}
	p.refreshCh = make(chan struct{}, 1)
	p.rosterCh = make(chan struct{}, 1)
	p.linkLimiter = newRateLimiter(5, 15*time.Minute)
	p.profileLimiter = newRateLimiter(10, time.Minute)
	// Generous, because a grid costs one request per tile and reopening the
	// panel renegotiates all of them at once. A viewer watching four streams
	// who closes and reopens the sidebar twice is at eight requests before
	// anything has gone wrong.
	p.playbackLimiter = newRateLimiter(60, time.Minute)

	p.communityLimiter = newRateLimiter(30, time.Minute)
	p.nowPlayingLimiter = newRateLimiter(10, time.Minute)
}

// starter is one thing the usual start starts.
type starter struct {
	name string
	run  func()
}

// usualStarters is everything activation starts that reads or writes the
// store, the disk, Mattermost or anything outside, in order. It is the one
// list: startUsual runs it and nothing else runs these at activation.
func (p *Plugin) usualStarters() []starter {
	return []starter{
		// First, and only reading: whether the plugin's own files are a
		// signed release, which /access and /me say from the first request.
		{"the release check", p.checkOwnRelease},

		{"the handle secret", p.loadHandleSecret},

		// The push key, which /access hands to Matty, before anything
		// could ask for it; never in a backup, never shown.
		{"the push key", p.loadPushKey},
		{"the bot", p.ensureBot},
		{"the bot's channels", p.joinConfiguredChannels},
		{"retired documents", p.removeRetiredDocuments},

		// Before anything is written: work out which format the stored
		// airtime is in, then take the safety copy. An upgrade is exactly the
		// moment this is for, and the copy is only ever replaced by a larger
		// one.
		{"the airtime format", p.migrateStats},
		{"the airtime backup", func() { p.backupStats(manifestVersion) }},

		// Who chose which awards to show, before the first snapshot carries
		// their choices; and, once, the list of biggest-fan opt-outs 3.2.0
		// left behind, which 3.2.1 deletes (design/3.2.1.md §2).
		{"the award choices", func() { p.refreshAwardDocs(true) }},
		{"the biggest-fan opt-outs", p.removeFanOptouts},

		// The soundboard's board, and the sweep of sounds nothing names any
		// more, while it is on (sounds.go).
		{"the soundboard", func() { p.startSounds(time.Now()) }},

		// Before the plugin counts as activated, so that nothing a settings
		// change does to clips can land half way through starting them; and
		// before the first snapshot, so that it already says what there is.
		{"clips", p.startClips},

		// Where the title lookups keep their pictures, in the same directory
		// as Clips but not depending on them. Nothing is made, and nothing
		// outside is asked, until lookups are on and a picture is wanted.
		{"the title pictures", func() { p.startMediaDisk(time.Now()) }},

		// Where the profile banners are kept, in the same directory. Nothing
		// is made until somebody sets one.
		{"the banners", p.startBanners},

		{"the first snapshot", p.publishFirstSnapshot},
		{"activated", p.markActivated},

		{"the poller", p.restartPoller},
		{"voice", p.restartVoice},

		// Talking from Matty, after the roster connection whose channels a
		// join is checked against: the UDP port, while it is switched on.
		{"voice talk", p.restartVoiceTalk},

		// After the poller, whose first poll reads who has connected Twitch
		// for itself. Then the purge of anything somebody withdrew, whatever
		// the switch says: a person's right to have their Twitch data deleted
		// does not wait for an admin to switch Twitch on.
		{"Twitch live", p.restartTwitchLive},
		{"the Twitch purges", p.startTwitchPurges},

		// Steam statuses and Hytale names (accounts_live.go), while either is
		// on: with both off nothing is read, asked or started.
		{"Steam and Hytale", func() { p.restartAccountsLive(nil) }},

		// Push notifications' sending, whom to offer them for, and the
		// daily sweep of registrations, whatever the switch says: with it
		// off, nothing is offered or sent.
		{"push notifications", p.startPush},

		// Updates for the plugin itself: whether Mattermost takes uploads,
		// leftovers swept, and, while the setting is on, the daily look.
		{"plugin updates", p.startUpdates},
	}
}

// startUsual is the usual start, from OnActivate.
func (p *Plugin) startUsual() {
	for _, s := range p.usualStarters() {
		s.run()
	}

	p.API.LogInfo(productName+" plugin activated", "version", manifestVersion)
}

// publishFirstSnapshot is what the panel is shown before the first poll.
func (p *Plugin) publishFirstSnapshot() {
	initial := &snapshot{
		Streams: []apiStream{},
		// Before the first poll has published anything: still an index, of nobody.
		People:       map[string]personView{},
		Configured:   p.getConfiguration().validate() == nil,
		LivePreviews: p.getConfiguration().LivePreviews,
		TwitchOn:     p.getConfiguration().twitchLiveOn(),
		TwitchEmbed:  p.getConfiguration().twitchEmbedOn(),
		Banners:      p.banners().word(),
	}
	p.addClips(initial, p.getConfiguration(), time.Now())
	p.addAwardChoices(initial)
	p.addAchievements(initial, p.getConfiguration())
	p.setSnapshot(initial)
}

// markActivated lets OnConfigurationChange act on a change from here on.
func (p *Plugin) markActivated() {
	p.activatedLock.Lock()
	p.activated = true
	p.activatedLock.Unlock()
}

// joinConfiguredChannels resolves the channels the plugin posts to, which as a
// side effect puts its bot in them.
//
// Done at activation rather than left to the first post, so that an admin who
// has just upgraded can see the bot in the member list. The birthday channel is
// otherwise only resolved on the morning of somebody's birthday, which is a
// poor time to discover that the bot is not in it and its @all will therefore
// notify nobody — see joinBot.
func (p *Plugin) joinConfiguredChannels() {
	cfg := p.getConfiguration()

	for _, setting := range []string{cfg.BridgeChannel, cfg.BirthdayChannel} {
		if strings.TrimSpace(setting) == "" {
			continue
		}

		p.channelIDFor(setting)
	}
}

// OnDeactivate stops the usual start's work.
func (p *Plugin) OnDeactivate() error {
	p.stopUsual()

	p.API.LogInfo(productName+" plugin deactivated", "version", manifestVersion)

	return nil
}

// stopUsual stops what startUsual started.
func (p *Plugin) stopUsual() {
	p.activatedLock.Lock()
	p.activated = false
	p.activatedLock.Unlock()

	p.stopPoller()

	// After the poller, which would otherwise publish clips into a store
	// being stopped, and before anything else is taken down.
	p.stopClips()

	// The Twitch loop and the purge worker, each waited for, before the
	// badges they may have put on come off.
	p.stopTwitchLive()
	p.stopTwitchPurges()

	// The Steam and Hytale loop, waited for, and an accounts event still
	// waiting to go out.
	p.stopAccountsLive()

	// The pictures' hourly tidy, and a moment for pictures being written to
	// land rather than be cut off half way.
	p.stopMediaDisk()

	// Every phone told voice is restarting here (it joins again by itself)
	// and let go, and the voice port closed, before the roster connection
	// goes.
	p.stopVoiceTalk()
	p.stopVoice()

	// Push notifications' senders, waited for; what was queued is lost.
	p.stopPush()

	// The daily look ended, waited for five seconds at most while under
	// way, and a bundle being prepared cancelled.
	p.stopUpdates()

	// Leave nobody wearing a live badge we can no longer clear.
	p.clearAllBadges()
}

func (p *Plugin) OnConfigurationChange() error {
	var cfg configuration

	if err := p.API.LoadPluginConfiguration(&cfg); err != nil {
		return err
	}

	// Before normalize, so a default is tidied exactly as a saved value would
	// be, and before setConfiguration, so everything that reads the settings
	// back — the settings backup included — sees what the console shows. A
	// failure here leaves the settings as loaded, which is how every version
	// before this one ran, so it is logged rather than refused.
	stored, storedOK := p.savedPluginSettings()
	if !storedOK {
		p.API.LogWarn("Could not tell which settings have been saved, so none has been given its default")
	} else if err := applySectionDefaults(&cfg, stored); err != nil {
		p.API.LogWarn("The defaults for settings that have never been saved could not be applied", "error", err.Error())
	}
	// Before normalize leaves them out: which lines of the list of push
	// servers are not one.
	pushHostsTrouble := pushHostsProblem(cfg.PushAllowedHosts)
	cfg.normalize()

	// Two settings CHANGES would otherwise hand a live stream a lump of
	// backdated airtime, and both are fixed the same way: every session is
	// brought up to date once, earning nothing for the period before the
	// change.
	//
	//   - Switching the leaderboard back on, which would credit the whole
	//     period it spent switched off.
	//   - Taking a key out of "24/7 stream keys". Always-on streams carry
	//     no session mark, so the first poll after the change would see a
	//     session that apparently started hours ago and credit all of it.
	//
	// The nil check is load-bearing, not defensive. Mattermost calls this once
	// on every plugin start, BEFORE OnActivate, when there is no previous
	// configuration at all — and a zero-value configuration has the leaderboard
	// off, so comparing against it reads every start as "the leaderboard was
	// just switched on". That would discard the airtime accrued while the
	// plugin was down, on every single upgrade, which is precisely the time
	// this plugin is supposed to be protecting it.
	previous := p.previousConfiguration()
	if previous != nil {
		if (!previous.EnableLeaderboard && cfg.EnableLeaderboard) || demotedFromAlwaysOn(previous, &cfg) {
			p.armStatsResync(nowMillis())
		}

		// The Top Snoopers board has the same problem, solved the way it
		// solves it on a poll: forget the last pass, so the first one after
		// this only looks. Here as well, because no poll may reach Broadcast
		// Box between the board going off and coming back on; and before the
		// new setting is in place, so no pass can read it with the old mark
		// still set.
		if previous.TopWatchers != cfg.TopWatchers {
			p.forgetWatchPass()
		}

		// Twitch sessions running when Twitch live is switched on earn
		// nothing for the time before it, as with the leaderboard.
		if !previous.twitchLiveOn() && cfg.twitchLiveOn() {
			p.armTwitchResync(nowMillis())
		}
	}

	p.setConfiguration(&cfg)

	// Once a change of the address, and once a start, rather than once a
	// request: an admin reading the log needs to be told, not reminded.
	if previous == nil || previous.BroadcastBoxURL != cfg.BroadcastBoxURL {
		if host, plain := plainHTTPElsewhere(cfg.BroadcastBoxURL); plain {
			p.API.LogWarn(plainBroadcastBoxWarning, "host", host)
		}
	}

	// The push client sends under the rules as saved from now on, and
	// whether Mattermost can push is worked out afresh. A bad line of the
	// list is logged by number, once until what is wrong changes.
	p.pushConfigured(&cfg)
	if pushHostsTrouble != "" && pushHostsTrouble != p.pushes().hostsProblemNow() {
		p.API.LogWarn(pushHostsTrouble)
	}
	p.pushes().setHostsProblem(pushHostsTrouble)

	// Before the poller restarts below, so its first poll reads the 24/7
	// pictures afresh. See forgetAllChannelImageVersions.
	p.forgetAllChannelImageVersions()

	// Kept whether or not the plugin is activated, so that a load before it
	// is up is not told again once it is up.
	appCheckChanged := p.noteAppCheckInForce(&cfg)

	p.activatedLock.Lock()
	activated := p.activated
	p.activatedLock.Unlock()

	if activated {
		// Every open client draws the name, and every Matty asks /access
		// again when the app check's setting or what it accepts has changed,
		// and proves itself if its verdict no longer counts, or when the
		// oldest Matty this server answers has: one event for any of them.
		minimumChanged := previous != nil && previous.appMinimum() != cfg.appMinimum()
		if (previous != nil && previous.Name != cfg.Name) || appCheckChanged || minimumChanged {
			p.tellEverybodyMe()
		}

		// A minimum that came into force, or rose, ends the voice of every
		// Matty here that does not meet it, with the sentence its requests
		// are now refused with. On its own goroutine, with the minimum in
		// force when it runs: this hook holds up Save.
		if minimumChanged && cfg.appMinimum() != "" {
			p.goSafely("ending the voice of a Matty below the minimum", func() {
				p.voiceTalk().endOutdated(p.getConfiguration().appMinimum())
			})
		}

		// Before the poller restarts, so that no draft is cut and nothing is
		// recorded after Clips went off, without waiting for a poll that
		// Broadcast Box may never answer; and not on the first call, which
		// comes before OnActivate with nothing to compare against (A§2.1,
		// spec §2.14). What is recorded belongs to the Broadcast Box it came
		// from, so a new one starts from nothing, drafts kept; a new clip
		// length is taken by running recorders at their next segment.
		if previous != nil {
			now := p.clips().now()
			switch {
			case previous.EnableClips && !cfg.EnableClips:
				p.clipsTurnedOff(now)
			case previous.BroadcastBoxURL != cfg.BroadcastBoxURL:
				p.recorders().stopAll(false, whyURLChanged, now)
			}
			if previous.clipLengthSeconds() != cfg.clipLengthSeconds() {
				p.recorders().setWindow(cfg.ringWindow())
			}
		}

		// Before the poller, so the poll its restart makes already sees the
		// Twitch list emptied when the switch has just gone off. Only when
		// the loop would do something different: on the first call there is
		// nothing to compare with, which counts as different.
		if !sameTwitchLive(previous, &cfg) {
			p.restartTwitchLive()
		}

		// Steam and Hytale, likewise: only when a switch, the key or the
		// application changed. Switching one off lets go of what is held
		// about it at once; a new Steam key is asked with at once.
		if !sameAccountsLive(previous, &cfg) {
			p.restartAccountsLive(previous)
		}

		p.restartPoller()

		// Only when it would dial somewhere else, or for a different reason.
		// A held connection is not something to drop over a banner colour.
		if !sameMumbleConnection(previous, &cfg) {
			p.restartVoice()
		} else if !sameVoiceRoster(previous, &cfg) {
			// Same connection, different roster rules: rebuilt now, on the
			// connection already held. On its own goroutine, because matching
			// names is a lookup per person and this hook holds up Save.
			p.goSafely("rebuilding the voice roster", p.republishVoice)
		}

		// Talking from Matty: switched on or off, another port or other
		// addresses, another budget, or another voice server under it,
		// whose sessions end. On its own goroutine: it may wait for phones
		// to be told, and this hook holds up Save.
		if !sameVoiceTalk(previous, &cfg) || !sameMumbleConnection(previous, &cfg) {
			p.goSafely("restarting talking from Matty", p.restartVoiceTalk)
		}

		// Updates for the plugin: whether Mattermost takes plugin uploads is
		// read again, the daily look follows the setting, and switched on,
		// one node looks at once.
		p.updatesChanged(previous, &cfg)

		// Renaming the bot is a write to Mattermost, so it only happens when
		// the name has actually changed. On its own goroutine: this hook holds
		// up the Save button, and a rename is a database write that can fail
		// slowly.
		if !sameBotIdentity(previous, &cfg) {
			p.goSafely("renaming the bot", p.ensureBot)
		}

		// A channel setting that has just been pointed somewhere else resolves
		// under a new cache key, and resolving is what puts the bot in it. Free
		// when nothing changed: the second call is a cache hit that does not
		// reach Mattermost at all.
		p.goSafely("joining the bot to its channels", p.joinConfiguredChannels)
	}

	return nil
}

func (p *Plugin) getSnapshot() *snapshot {
	p.snapshotLock.RLock()
	defer p.snapshotLock.RUnlock()

	if p.snapshot == nil {
		return &snapshot{Streams: []apiStream{}, People: map[string]personView{}}
	}

	return p.snapshot
}

func (p *Plugin) setSnapshot(s *snapshot) {
	p.snapshotLock.Lock()
	defer p.snapshotLock.Unlock()

	p.snapshot = s
}
