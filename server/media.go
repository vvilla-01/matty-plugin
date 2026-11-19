package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Movies, shows and games, looked up by name while somebody plans a stream.
//
// This is one of the two parts of the plugin that talk to anybody the admin
// did not point it at; the other is Twitch live (twitch_live.go). Everything
// else reaches Broadcast Box and Mumble — machines the admin configured — and
// nothing more, which was a promise worth keeping. Real titles and real art on
// the schedule cannot be had without asking somebody who has them, so the
// promise is now narrower, and exact:
//
//   - Three providers only. TMDB for movies and TV, which needs a token the
//     admin pastes; the Steam store for games, which needs none; and IGDB,
//     Twitch's game database, for the games Steam does not have, which needs
//     the admin's Twitch application (twitch_app.go) and is asked only when
//     one is set up. Steam stays the first source for games; IGDB fills in.
//   - Only while MediaLookup is on. Switched off, not one request leaves for
//     any of them, and nothing remembered is read or added.
//   - Only the SERVER asks. Every picture is fetched by the plugin and relayed,
//     so opening the panel never hands a viewer's address to a third party
//     for lookups; Twitch live is the exception, see twitch_oauth.go and
//     twitchEmbed.tsx.
//   - Only on demand: somebody searching while planning, somebody saving a plan
//     with a title picked, or a panel wanting a picture not already held.
//
// What comes back is held in memory, bounded, so a schedule on everybody's
// screen costs the providers one fetch per picture rather than one per viewer.
// The words are also remembered durably, in the plugin's KV store: searches,
// a title's details and where its pictures are (media_cache.go). So the same
// search or title is not looked up again for days, and a restart does not send
// every plan on screen back to its provider. The pictures themselves are kept
// on the server's own disk, bounded, when Mattermost keeps its files there
// (media_disk.go), so none is fetched twice either.

const (
	mediaKindMovie = "movie"
	mediaKindTV    = "tv"
	mediaKindGame  = "game"

	mediaSourceTMDB  = "TMDB"
	mediaSourceSteam = "Steam"
	mediaSourceIGDB  = "IGDB"

	// Who a ref names a title at. Its kind says what the title is; its
	// provider says who to ask, which for a game is one of two.
	mediaProviderTMDB  = "tmdb"
	mediaProviderSteam = "steam"
	mediaProviderIGDB  = "igdb"

	mediaSizeThumb    = "thumb"
	mediaSizeCover    = "cover"
	mediaSizeBackdrop = "backdrop"

	maxMediaResults  = 8
	minMediaQuery    = 2
	maxMediaQuery    = 80
	maxMediaTitle    = 120
	maxMediaOverview = 320
	maxMediaFacts    = 100
	maxMediaGenres   = 2

	// The providers' JSON is tens of kilobytes; a poster is a few hundred. Both
	// caps are far above that and far below anything that could hurt.
	maxMediaJSONBytes  = 2 << 20
	maxMediaImageBytes = 2 << 20
	maxMediaRedirects  = 3

	// Every single request is bounded by the client's timeout. The budgets
	// bound a whole operation, which can be more than one request: a picture
	// may need the title's details first, then a fallback.
	mediaHTTPTimeout   = 6 * time.Second
	mediaSearchBudget  = 8 * time.Second
	mediaPlanBudget    = 8 * time.Second
	mediaPictureBudget = 12 * time.Second

	mediaSearchTTL     = 30 * time.Minute
	mediaSearchEntries = 256
	mediaDetailsTTL    = 6 * time.Hour
	mediaDetailsItems  = 256
	mediaHintTTL       = time.Hour
	mediaHintEntries   = 1024
	mediaPictureTTL    = 12 * time.Hour
	mediaPictureItems  = 512
	mediaPictureBytes  = 24 << 20

	// How long "this title has no such picture" is believed. Short, because it
	// is sometimes only "not yet": TMDB gains posters for new titles all the
	// time.
	mediaMissTTL = 10 * time.Minute

	// A problem the admin has to fix is logged once in this long rather than
	// once per search, which on a busy evening would bury everything else.
	mediaWarnEvery = 15 * time.Minute

	// Searching is metered like any other endpoint that reaches out. Pictures
	// are metered only when they are NOT already held: a schedule of three
	// plans on a panel reopened ten times is thirty requests that cost nobody
	// anything, and should not count.
	mediaSearchesPerMinute = 40
	mediaFetchesPerMinute  = 120

	// And the whole server's calls to each provider's API, whoever is asking.
	// Steam's store API answers an address that asks more than about two
	// hundred times in five minutes with 403 for a while, and every game plan
	// on the server then saves without its art; staying well under that is
	// the plugin's job, not the person typing. TMDB allows far more.
	steamCallsPerMinute = 30
	tmdbCallsPerMinute  = 120

	// IGDB allows 4 requests a second and 8 open at once, and its terms forbid
	// going over either. The server keeps to half its per-minute rate, as for
	// Steam, to exactly its per-second rate, and to half its open requests.
	igdbCallsPerMinute = 120
	igdbCallsPerSecond = 4
	igdbConcurrent     = 4

	mediaUserAgent = "MattyPlugin/" + manifestVersion + " (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)"
)

// Where the providers are. Variables rather than constants only so that tests
// can point them at a fake: nothing a test runs ever leaves the machine.
var (
	tmdbAPIBase      = "https://api.themoviedb.org/3"
	tmdbImageBase    = "https://image.tmdb.org/t/p"
	steamStoreBase   = "https://store.steampowered.com"
	steamLibraryBase = "https://shared.akamai.steamstatic.com/store_item_assets/steam/apps"

	// mediaImageHosts are the only hosts a picture is ever fetched from, and
	// the only hosts a picture's redirect is ever followed to. Steam's art
	// addresses carry content hashes and are taken from its answers rather
	// than built, so this list is what stands between "an address Steam gave
	// us" and "any address at all".
	mediaImageHosts = map[string]bool{
		"image.tmdb.org":                    true,
		"shared.akamai.steamstatic.com":     true,
		"cdn.akamai.steamstatic.com":        true,
		"store.akamai.steamstatic.com":      true,
		"shared.cloudflare.steamstatic.com": true,
		"cdn.cloudflare.steamstatic.com":    true,
		"shared.fastly.steamstatic.com":     true,
		"cdn.fastly.steamstatic.com":        true,
		"images.igdb.com":                   true,
	}

	// mediaHTTPClient is the one client every lookup goes through, with its
	// own short timeout. Not the plugin's other client, whose timeout suits a
	// Broadcast Box on the same network and whose connection limits are sized
	// for it.
	mediaHTTPClient = newMediaClient(&http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxConnsPerHost:     8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	})
)

var (
	errMediaNotFound = errors.New("the provider has no such title or picture")

	// errMediaBusy is the plugin holding itself back: this server has asked a
	// provider as often as it should this minute.
	errMediaBusy     = errors.New("asked too often just now; try again in a minute")
	errMediaRefused  = errors.New("not one of the hosts the plugin fetches pictures from")
	errMediaTooLarge = errors.New("the picture is larger than the plugin relays")
	errMediaNotImage = errors.New("the answer was not a picture")

	// errMediaForbidden is an image host answering 403. For a picture that is
	// usually a CDN with no such object, so it is errMediaNotFound and the
	// next address is tried; but a firewall or a proxy in the way answers the
	// same, so it is never taken as proof that a picture is gone.
	errMediaForbidden = fmt.Errorf("%w: the image host answered 403", errMediaNotFound)

	// errMediaPictureUnsure is no picture, and no answer that says there is
	// none: something that was not a picture, a host refused, a 403, or
	// addresses from details that could not be brought up to date. Shown as
	// no picture for a little while, as it always was, but a picture the
	// disk holds is neither dropped for it nor kept from standing in.
	errMediaPictureUnsure = errors.New("no picture, and no way to be sure there is none")

	// errMediaUnreachable is a provider that could not be reached at all, as
	// opposed to one that answered badly.
	errMediaUnreachable = errors.New("could not reach it")

	// errMediaPanicked is work for a search or a lookup that panicked: logged
	// with its stack (recovery.go), and to whoever waited for it a failure
	// like any other, so a search says it could not search just now.
	errMediaPanicked = errors.New("went wrong on the server; the server log says how")

	// errIGDBResting is IGDB being left alone for a few seconds after it
	// answered slowly or badly, so a struggling IGDB does not hold up every
	// keystroke of every game search.
	errIGDBResting = errors.New("IGDB did not answer properly just now, so it is left alone for a few seconds")

	mediaRefPattern   = regexp.MustCompile(`^(?:tmdb:(movie|tv)|(steam|igdb)):([0-9]{1,10})$`)
	tmdbAPIKeyPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	tmdbArtPath       = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,64}\.(?:jpe?g|png|webp)$`)
	imdbIDPattern     = regexp.MustCompile(`^tt[0-9]{5,12}$`)
	yearPattern       = regexp.MustCompile(`\b[12][0-9]{3}\b`)
	markupPattern     = regexp.MustCompile(`<[^>]*>`)
)

// mediaStatusError is a provider answering with a status that is neither
// success nor "no such thing".
type mediaStatusError struct {
	status int
}

func (e *mediaStatusError) Error() string {
	return fmt.Sprintf("the provider answered %d", e.status)
}

// mediaRef names one title at one provider: tmdb:movie:949, tmdb:tv:1396,
// steam:1245620 or igdb:113360. It is the only thing the browser ever sends to
// name a title, and it is checked against one strict pattern, so nothing a
// person types becomes part of an address the server fetches.
type mediaRef struct {
	provider string // mediaProviderTMDB, mediaProviderSteam or mediaProviderIGDB
	kind     string // movie, tv or game
	id       string
}

func parseMediaRef(value string) (mediaRef, bool) {
	match := mediaRefPattern.FindStringSubmatch(value)
	if match == nil {
		return mediaRef{}, false
	}

	// Through a number and back, so "tmdb:movie:0949" and "tmdb:movie:949" are
	// one title with one cache entry rather than two.
	id, err := strconv.ParseUint(match[3], 10, 64)
	if err != nil {
		return mediaRef{}, false
	}

	ref := mediaRef{provider: mediaProviderTMDB, kind: match[1], id: strconv.FormatUint(id, 10)}
	if match[2] != "" {
		ref.provider, ref.kind = match[2], mediaKindGame
	}

	return ref, true
}

// newMediaRef makes a ref from TMDB's or Steam's own id, refusing one the
// pattern would refuse, so that everything handed to the browser can come
// back. A game here is Steam's; IGDB's are made by newIGDBRef.
func newMediaRef(kind string, id int64) (mediaRef, bool) {
	if id <= 0 || id > 9999999999 {
		return mediaRef{}, false
	}

	provider := mediaProviderTMDB
	if kind == mediaKindGame {
		provider = mediaProviderSteam
	}

	return mediaRef{provider: provider, kind: kind, id: strconv.FormatInt(id, 10)}, true
}

// newIGDBRef makes a ref from IGDB's id for a game, within the same bounds.
func newIGDBRef(id int64) (mediaRef, bool) {
	if id <= 0 || id > 9999999999 {
		return mediaRef{}, false
	}

	return mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: strconv.FormatInt(id, 10)}, true
}

func (r mediaRef) String() string {
	if r.provider == mediaProviderTMDB {
		return "tmdb:" + r.kind + ":" + r.id
	}

	return r.provider + ":" + r.id
}

// source is the provider as a plan names it: "Data from IGDB".
func (r mediaRef) source() string {
	switch r.provider {
	case mediaProviderSteam:
		return mediaSourceSteam
	case mediaProviderIGDB:
		return mediaSourceIGDB
	default:
		return mediaSourceTMDB
	}
}

// category is the plan category this kind of title belongs under. A TV show
// is planned under the film icon: it is watched together in the same way.
func (r mediaRef) category() string {
	if r.kind == mediaKindGame {
		return categoryGame
	}

	return categoryMovie
}

// mediaFlags is what /me says can be looked up right now.
type mediaFlags struct {
	Movies bool `json:"movies"`
	Games  bool `json:"games"`
	IGDB   bool `json:"igdb"` // games also come from IGDB
}

func (c *configuration) mediaFlags() mediaFlags {
	_, twitch := c.twitchCredentials()

	return mediaFlags{
		Movies: c.MediaLookup && c.TMDBToken != "",
		Games:  c.MediaLookup,
		IGDB:   c.MediaLookup && twitch,
	}
}

// allowsRef is allows by kind, except that an IGDB title also needs IGDB.
// Used everywhere a REF is gated: saving a plan or What's on?, and the
// pictures. A search is gated by kind, and asks IGDB only when this is on.
func (f mediaFlags) allowsRef(ref mediaRef) bool {
	if ref.provider == mediaProviderIGDB && !f.IGDB {
		return false
	}

	return f.allows(ref.kind)
}

func (f mediaFlags) allows(kind string) bool {
	if kind == mediaKindGame {
		return f.Games
	}

	return f.Movies
}

// unavailable is the sentence the planning form shows in place of results, or
// "" when this kind can be searched. Written for the person planning, who can
// do nothing about either and should not be sent looking for a setting.
func (f mediaFlags) unavailable(kind string) string {
	switch {
	case f.allows(kind):
		return ""
	case !f.Games:
		return "Looking titles up is switched off on this server."
	default:
		return "Movies and shows cannot be looked up on this server yet."
	}
}

// mediaResult is one line in the planning form's list of matches.
type mediaResult struct {
	Ref   string `json:"ref"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Year  string `json:"year"`
	Label string `json:"label"`
}

// scheduleMedia is what a plan carries about its title. Compact on purpose:
// it is stored in the one schedule document and rides every snapshot pushed to
// every panel, so it holds a line or two of text and a link, never pictures —
// those are fetched by ref, once, when a panel shows them.
type scheduleMedia struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Year     string `json:"year"`
	Overview string `json:"overview"`
	Facts    string `json:"facts"`
	Link     string `json:"link"`
	Source   string `json:"source"`
}

// mediaLinkPrefixes are the only places a plan's link may lead. The panel puts
// it in an href, and a document edited by hand should not be able to make that
// a javascript: URL.
var mediaLinkPrefixes = []string{
	"https://www.imdb.com/title/",
	"https://www.themoviedb.org/movie/",
	"https://www.themoviedb.org/tv/",
	"https://store.steampowered.com/app/",
	"https://www.igdb.com/games/",
}

// checked is the stored media as it may be shown, or nil. Checked on the way
// OUT as well as on the way in, exactly as a stored category is: the webapp
// trusts this object, and the document it came from could have been written by
// a later build or by hand.
func (m *scheduleMedia) checked() *scheduleMedia {
	if m == nil {
		return nil
	}

	ref, ok := parseMediaRef(m.Ref)
	if !ok || ref.kind != m.Kind {
		return nil
	}

	if m.Source != ref.source() {
		return nil
	}

	linked := false
	for _, prefix := range mediaLinkPrefixes {
		if strings.HasPrefix(m.Link, prefix) {
			linked = true
			break
		}
	}
	if !linked {
		return nil
	}

	shown := *m
	shown.Ref = ref.String()
	shown.Title = cutRunes(strings.TrimSpace(shown.Title), maxMediaTitle)
	shown.Overview = cutRunes(shown.Overview, maxMediaOverview)
	shown.Facts = cutRunes(shown.Facts, maxMediaFacts)
	if !yearPattern.MatchString(shown.Year) || len(shown.Year) != 4 {
		shown.Year = ""
	}
	if shown.Title == "" {
		return nil
	}

	return &shown
}

// mediaArt is where a title's pictures are, as its provider described them:
// TMDB's paths, which are joined to a size; Steam's full addresses, which
// carry content hashes and cannot be built; and IGDB's image ids, which are
// joined to a size like TMDB's paths. For IGDB, Poster is the portrait cover
// and Backdrop the wide artwork or screenshot. Tagged because it is
// remembered (media_cache.go).
type mediaArt struct {
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
	Capsule  string `json:"capsule,omitempty"`
	Header   string `json:"header,omitempty"`
}

type mediaDetails struct {
	info scheduleMedia
	art  mediaArt

	// fetchedAt is when the provider said this, by the store's clock: art
	// addresses a day old are worth asking about again once they stop
	// working. changing is a title still settling (not out yet, in early
	// access, a show still running), which is remembered for less long.
	fetchedAt time.Time
	changing  bool

	// until is when the title stops being believed: fetchedAt and its life
	// for an answer just given, and what the remembered titles said for one
	// read back from them, which do not keep whether it was changing. Its
	// pictures are believed no longer (media_disk.go), and a title past it
	// is a stand-in for a provider that could not be asked.
	until time.Time
}

// mediaPicture is a picture held in memory. No data means "there is no such
// picture", remembered for a little while so that a title with no backdrop
// does not ask again on every panel that shows it.
type mediaPicture struct {
	contentType string
	data        []byte
}

// mediaStore is everything the lookups keep between requests.
type mediaStore struct {
	// searchFault, when a test sets it before searching, is called as each
	// provider's search begins, so that a test can make one panic.
	searchFault func(provider string)

	// Keyed provider + "\x00" + the normalised query, holding the art each
	// result's search described along with it.
	searches *memoryCache[[]mediaCachedResult]
	details  *memoryCache[*mediaDetails]
	hints    *memoryCache[mediaArt]
	pictures *memoryCache[*mediaPicture]

	searchLimiter *rateLimiter
	fetchLimiter  *rateLimiter

	// Per provider, not per person: every call this server makes to each
	// provider's API, whoever it is for.
	tmdbLimiter  *rateLimiter
	steamLimiter *rateLimiter

	// IGDB's budget is two limits, both of which IGDB states. The bucket keeps
	// to 120 a minute, asked with allowAt("igdb", store.now()). igdbStarts are
	// the start times of the last igdbCallsPerSecond requests, a ring: a
	// request may start only when the oldest of them is at least a second old,
	// which is exactly IGDB's "4 requests per second" over ANY one-second
	// window. A token bucket would let 4 go at once and then refill, which is
	// about 7 inside one second.
	igdbLimiter *rateLimiter
	igdbLock    sync.Mutex
	igdbStarts  [igdbCallsPerSecond]time.Time
	igdbNext    int

	// The hold-back: while now is before igdbHeldUntil and the credentials'
	// fingerprint is igdbHeldFor, igdbPost answers igdbHeldErr without asking
	// id.twitch.tv or IGDB anything. igdbRefusedToken is a freshly fetched
	// token IGDB still refused, which is never invalidated (memory only, never
	// logged).
	igdbHeldUntil    time.Time
	igdbHeldFor      [32]byte
	igdbHeldErr      error
	igdbRefusedToken string

	// IGDB's open requests. MaxConnsPerHost does not bound HTTP/2 streams, so
	// this is what keeps the server under IGDB's limit of 8.
	igdbSlots chan struct{}

	// One outbound fetch per title, picture or search at a time. A new plan
	// appears on every open panel at once, and without this each of them asked
	// TMDB or Steam for the same art in the same second.
	flights flightGroup

	// now is the clock behind everything remembered durably and behind IGDB's
	// budget and hold-back: time.Now, which tests replace. The in-memory
	// caches keep real time.
	now func() time.Time

	warnLock sync.Mutex
	warnedAt map[string]time.Time
}

// mediaStore is made on first use rather than in OnActivate, so a server that
// never switches the feature on never holds any of it.
func (p *Plugin) mediaStore() *mediaStore {
	p.mediaOnce.Do(func() {
		p.mediaState = &mediaStore{
			searches: newMemoryCache[[]mediaCachedResult](mediaSearchEntries, 0, nil),
			details:  newMemoryCache[*mediaDetails](mediaDetailsItems, 0, nil),
			hints:    newMemoryCache[mediaArt](mediaHintEntries, 0, nil),
			pictures: newMemoryCache(mediaPictureItems, mediaPictureBytes, func(picture *mediaPicture) int {
				// The fixed part stands for the entry itself, so that a
				// thousand remembered misses are not counted as free.
				return 256 + len(picture.contentType) + len(picture.data)
			}),
			searchLimiter: newRateLimiter(mediaSearchesPerMinute, time.Minute),
			fetchLimiter:  newRateLimiter(mediaFetchesPerMinute, time.Minute),

			tmdbLimiter:  newRateLimiter(tmdbCallsPerMinute, time.Minute),
			steamLimiter: newRateLimiter(steamCallsPerMinute, time.Minute),
			igdbLimiter:  newRateLimiter(igdbCallsPerMinute, time.Minute),
			igdbSlots:    make(chan struct{}, igdbConcurrent),
			now:          time.Now,
			warnedAt:     map[string]time.Time{},
		}
	})

	return p.mediaState
}

// warnMediaOnce logs something an admin has to act on, once in a while rather
// than once per request. Keys are fixed strings, so the map cannot grow.
func (p *Plugin) warnMediaOnce(key, message string, pairs ...any) {
	store := p.mediaStore()
	now := time.Now()

	store.warnLock.Lock()
	last, warned := store.warnedAt[key]
	if warned && now.Sub(last) < mediaWarnEvery {
		store.warnLock.Unlock()
		return
	}
	store.warnedAt[key] = now
	store.warnLock.Unlock()

	p.API.LogWarn(message, pairs...)
}

func newMediaClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:       mediaHTTPTimeout,
		Transport:     transport,
		CheckRedirect: followMediaRedirect,
	}
}

// followMediaRedirect keeps a redirect on the kind of host it started on. A
// picture may move between the image hosts, as CDNs do; an API call may move
// only within its own host. Anything else is refused rather than followed,
// which is what makes the image host list mean something: a provider's answer
// pointing at an allowed host that then redirects elsewhere gets nowhere.
func followMediaRedirect(next *http.Request, via []*http.Request) error {
	if len(via) >= maxMediaRedirects {
		return errors.New("too many redirects")
	}

	first := via[0].URL
	if mediaImageURLAllowed(first) {
		if !mediaImageURLAllowed(next.URL) {
			return errMediaRefused
		}

		return nil
	}

	if next.URL.Scheme != "https" || !strings.EqualFold(next.URL.Host, first.Host) {
		return errMediaRefused
	}

	return nil
}

func mediaImageURLAllowed(address *url.URL) bool {
	return address != nil &&
		address.Scheme == "https" &&
		address.User == nil &&
		mediaImageHosts[strings.ToLower(address.Host)]
}

// sendMedia makes one outbound request.
func sendMedia(request *http.Request) (*http.Response, error) {
	request.Header.Set("User-Agent", mediaUserAgent)

	response, err := mediaHTTPClient.Do(request)
	if err != nil {
		// A *url.Error spells out the whole address it was given, and a TMDB
		// address can carry the admin's API key in its query string. Only the
		// cause travels on — into a log line or anywhere else.
		var addressed *url.Error
		if errors.As(err, &addressed) {
			err = addressed.Err
		}

		return nil, fmt.Errorf("%w: %w", errMediaUnreachable, err)
	}

	return response, nil
}

// apiStatus is what a status from a provider's API means. Only a 404 or a 410
// is "no such title". A 403 or a 429 is the provider saying slow down — it is
// how Steam's store API refuses an address that has asked too often — and must
// be "could not find out" instead, or a title that exists would be remembered
// as one that does not for as long as misses are kept.
func apiStatus(status int) error {
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return errMediaNotFound
	default:
		return &mediaStatusError{status: status}
	}
}

// mediaStatus is what a status means for a PICTURE, where a CDN answering 403
// for an object it does not have is ordinary, and the next address is tried.
func mediaStatus(status int) error {
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return errMediaNotFound
	case http.StatusForbidden:
		return errMediaForbidden
	default:
		return &mediaStatusError{status: status}
	}
}

func getMediaJSON(request *http.Request, into any) error {
	return getMediaJSONAs(request, into, apiStatus)
}

// getMediaJSONAs is getMediaJSON for a provider whose statuses mean something
// else: meaning says what an answer other than 200 is.
func getMediaJSONAs(request *http.Request, into any, meaning func(status int) error) error {
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return meaning(response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxMediaJSONBytes+1))
	if err != nil {
		return errors.New("the answer could not be read")
	}
	if len(body) > maxMediaJSONBytes {
		return errors.New("the answer was too large")
	}

	if err := json.Unmarshal(body, into); err != nil {
		return errors.New("the answer was not the expected shape")
	}

	return nil
}

// tmdbGet asks TMDB something, authenticated the way the token asks for.
//
// TMDB has two kinds of credential and admins will paste either. The one its
// settings page calls the "API Read Access Token" is a long JWT sent as a
// Bearer header; the older "API Key" is 32 hex characters and only works as a
// query parameter. Telling them apart by shape means neither needs a second
// setting, or a paragraph of help text about which one to use.
func (p *Plugin) tmdbGet(ctx context.Context, token, path string, params url.Values, into any) error {
	if !p.mediaStore().tmdbLimiter.allow("tmdb") {
		return fmt.Errorf("TMDB: %w", errMediaBusy)
	}

	apiKey := tmdbAPIKeyPattern.MatchString(token)
	if apiKey {
		params.Set("api_key", token)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tmdbAPIBase+path+"?"+params.Encode(), nil)
	if err != nil {
		// Not wrapped: a URL parse error quotes the URL, key and all.
		return errors.New("TMDB: the request could not be built")
	}
	if !apiKey {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	err = getMediaJSON(request, into)

	var refused *mediaStatusError
	if errors.As(err, &refused) && refused.status == http.StatusUnauthorized {
		// Said plainly, and only here. The person searching is told the
		// search failed, which is all they can act on; the admin is told what
		// to change.
		p.warnMediaOnce("tmdb-token",
			"TMDB refused the TMDB API read access token (401 Unauthorized), so movies and shows cannot be looked up. "+
				"Sign in at themoviedb.org, open Settings > API, copy the API Read Access Token, and paste it into "+
				"System Console > Plugins > Matty > Movies and games.")
	}

	if err != nil {
		return fmt.Errorf("TMDB: %w", err)
	}

	return nil
}

func (p *Plugin) steamGet(ctx context.Context, path string, params url.Values, into any) error {
	if !p.mediaStore().steamLimiter.allow("steam") {
		return fmt.Errorf("Steam: %w", errMediaBusy)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, steamStoreBase+path+"?"+params.Encode(), nil)
	if err != nil {
		return errors.New("Steam: the request could not be built")
	}

	if err := getMediaJSON(request, into); err != nil {
		return fmt.Errorf("Steam: %w", err)
	}

	return nil
}

// searchMedia answers the planning form: at most eight titles matching what
// has been typed so far, from memory or the remembered titles when somebody
// has typed it before. Games come from Steam first, with IGDB filling in.
//
// The second value names a source that was asked but is missing from the
// answer (searchGames), or is "".
func (p *Plugin) searchMedia(ctx context.Context, cfg *configuration, kind, query string) ([]mediaResult, string, error) {
	normal := mediaQueryKey(query)

	var found []mediaCachedResult
	var missing string
	var err error
	if kind == mediaKindGame {
		found, missing, err = p.searchGames(ctx, cfg, query, normal)
	} else {
		found, err = p.providerSearch(ctx, cfg, mediaProviderTMDB, query, normal)
	}
	if err != nil {
		return nil, "", err
	}

	return shownResults(found), missing, nil
}

// shownResults is what the browser gets: the results without the art and
// Steam links they are remembered with, and never null.
func shownResults(found []mediaCachedResult) []mediaResult {
	results := make([]mediaResult, 0, len(found))
	for _, result := range found {
		results = append(results, result.mediaResult)
	}

	return results
}

type tmdbSearch struct {
	Results []struct {
		Adult        bool   `json:"adult"`
		MediaType    string `json:"media_type"`
		ID           int64  `json:"id"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		PosterPath   string `json:"poster_path"`
		BackdropPath string `json:"backdrop_path"`
	} `json:"results"`
}

func (p *Plugin) searchTMDB(ctx context.Context, token, query string) ([]mediaCachedResult, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("include_adult", "false")
	params.Set("language", "en-US")
	params.Set("page", "1")

	var found tmdbSearch
	if err := p.tmdbGet(ctx, token, "/search/multi", params, &found); err != nil {
		return nil, err
	}

	results := make([]mediaCachedResult, 0, maxMediaResults)
	for _, entry := range found.Results {
		if len(results) == maxMediaResults {
			break
		}

		// A multi-search also finds people, and anything TMDB adds to it later.
		// Only the two kinds of thing somebody could put on the television.
		var title, date, label string
		// Asked for without adult titles already; checked again anyway.
		if entry.Adult {
			continue
		}

		switch entry.MediaType {
		case mediaKindMovie:
			title, date, label = entry.Title, entry.ReleaseDate, "Movie"
		case mediaKindTV:
			title, date, label = entry.Name, entry.FirstAirDate, "TV series"
		default:
			continue
		}

		ref, ok := newMediaRef(entry.MediaType, entry.ID)
		title = cleanText(plainText(title), maxMediaTitle)
		if !ok || title == "" {
			continue
		}

		// The art goes with it, so the list's own thumbnails need nothing more
		// from TMDB than the pictures themselves.
		results = append(results, mediaCachedResult{
			mediaResult: mediaResult{Ref: ref.String(), Kind: ref.kind, Title: title, Year: yearOf(date), Label: label},
			Art:         mediaArt{Poster: entry.PosterPath, Backdrop: entry.BackdropPath},
		})
	}

	return results, nil
}

type steamSearch struct {
	Items []struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		ID        int64  `json:"id"`
		TinyImage string `json:"tiny_image"`
	} `json:"items"`
}

func (p *Plugin) searchSteam(ctx context.Context, query string) ([]mediaCachedResult, error) {
	params := url.Values{}
	params.Set("term", query)
	params.Set("l", "english")
	params.Set("cc", "US")

	var found steamSearch
	if err := p.steamGet(ctx, "/api/storesearch/", params, &found); err != nil {
		return nil, err
	}

	results := make([]mediaCachedResult, 0, maxMediaResults)
	for _, item := range found.Items {
		if len(results) == maxMediaResults {
			break
		}

		// Apps only: the store search also lists bundles and packages, which
		// are ways to buy things rather than things to play.
		if item.Type != "app" {
			continue
		}

		ref, ok := newMediaRef(mediaKindGame, item.ID)
		title := cleanText(plainText(item.Name), maxMediaTitle)
		if !ok || title == "" {
			continue
		}

		// No year. The search does not carry one, and asking for the details
		// of eight games on every keystroke to find out is not worth a year.
		results = append(results, mediaCachedResult{
			mediaResult: mediaResult{Ref: ref.String(), Kind: ref.kind, Title: title, Year: "", Label: "Game"},
			Art:         mediaArt{Capsule: item.TinyImage},
		})
	}

	return results, nil
}

// mediaDetailsFor is everything about one title: the text a plan carries and
// where its pictures are. Cached, because one title is asked about by the
// person who plans it and then by every panel that shows its art.
//
// Memory first, then the remembered titles, then the provider. A nil held in
// memory is a title the provider does not have, remembered for a little while,
// and both checks below turn it into exactly that: returned as it is, it would
// be (nil, nil), which no caller expects.
func (p *Plugin) mediaDetailsFor(ctx context.Context, cfg *configuration, ref mediaRef) (*mediaDetails, error) {
	store := p.mediaStore()

	if cached, hit := store.details.get(ref.String()); hit {
		if cached == nil {
			return nil, errMediaNotFound
		}

		return cached, nil
	}

	// Whoever asks first fetches; anybody asking for the same title while
	// that is under way waits for its answer instead of fetching again.
	found, err := store.flights.do("details/"+ref.String(), func() (any, error) {
		if cached, hit := store.details.get(ref.String()); hit {
			if cached == nil {
				return nil, errMediaNotFound
			}

			return cached, nil
		}

		return p.rememberedDetails(ctx, cfg, ref)
	})
	if err != nil {
		return nil, err
	}

	details, _ := found.(*mediaDetails)
	if details == nil {
		return nil, errMediaNotFound
	}

	return details, nil
}

// askForDetails asks a title's provider for its details, and nothing else:
// no memory, nothing remembered.
func (p *Plugin) askForDetails(ctx context.Context, cfg *configuration, ref mediaRef) (*mediaDetails, error) {
	var details *mediaDetails
	var err error

	switch {
	case ref.provider == mediaProviderTMDB && ref.kind == mediaKindMovie:
		details, err = p.tmdbMovie(ctx, cfg.TMDBToken, ref)
	case ref.provider == mediaProviderTMDB && ref.kind == mediaKindTV:
		details, err = p.tmdbShow(ctx, cfg.TMDBToken, ref)
	case ref.provider == mediaProviderSteam:
		details, err = p.steamGame(ctx, ref)
	case ref.provider == mediaProviderIGDB:
		details, err = p.igdbGame(ctx, cfg, ref)
	default:
		// Never Steam by default: a ref nobody knows how to ask about is
		// a title nobody has.
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, err
	}

	details.fetchedAt = p.mediaStore().now()

	return details, nil
}

type tmdbGenre struct {
	Name string `json:"name"`
}

type tmdbMovie struct {
	Adult        bool        `json:"adult"`
	Title        string      `json:"title"`
	Status       string      `json:"status"`
	ReleaseDate  string      `json:"release_date"`
	Overview     string      `json:"overview"`
	Tagline      string      `json:"tagline"`
	Runtime      int         `json:"runtime"`
	Genres       []tmdbGenre `json:"genres"`
	IMDBID       string      `json:"imdb_id"`
	PosterPath   string      `json:"poster_path"`
	BackdropPath string      `json:"backdrop_path"`
}

type tmdbShow struct {
	Adult           bool        `json:"adult"`
	Name            string      `json:"name"`
	Status          string      `json:"status"`
	FirstAirDate    string      `json:"first_air_date"`
	Overview        string      `json:"overview"`
	Tagline         string      `json:"tagline"`
	NumberOfSeasons int         `json:"number_of_seasons"`
	Genres          []tmdbGenre `json:"genres"`
	PosterPath      string      `json:"poster_path"`
	BackdropPath    string      `json:"backdrop_path"`
	ExternalIDs     struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

func (p *Plugin) tmdbMovie(ctx context.Context, token string, ref mediaRef) (*mediaDetails, error) {
	params := url.Values{}
	params.Set("language", "en-US")

	var movie tmdbMovie
	if err := p.tmdbGet(ctx, token, "/movie/"+ref.id, params, &movie); err != nil {
		return nil, err
	}

	// The search leaves adult titles out, and so does everything else: a ref
	// typed in by hand is answered as though there were no such title, so no
	// plan carries one and no panel is sent its poster.
	if movie.Adult {
		return nil, errMediaNotFound
	}

	title := cleanText(plainText(movie.Title), maxMediaTitle)
	if title == "" {
		return nil, errMediaNotFound
	}

	facts := append(genreNames(movie.Genres), runtimeText(movie.Runtime))

	return &mediaDetails{
		info: scheduleMedia{
			Ref:      ref.String(),
			Kind:     mediaKindMovie,
			Title:    title,
			Year:     yearOf(movie.ReleaseDate),
			Overview: overviewOf(movie.Overview, movie.Tagline),
			Facts:    factLine(facts...),
			Link:     tmdbLink(ref, movie.IMDBID),
			Source:   mediaSourceTMDB,
		},
		art: mediaArt{Poster: movie.PosterPath, Backdrop: movie.BackdropPath},
		// Not out yet, or TMDB not sure: its details are likely to change.
		changing: movie.Status != "Released" || p.notYetOut(movie.ReleaseDate),
	}, nil
}

func (p *Plugin) tmdbShow(ctx context.Context, token string, ref mediaRef) (*mediaDetails, error) {
	params := url.Values{}
	params.Set("append_to_response", "external_ids")
	params.Set("language", "en-US")

	var show tmdbShow
	if err := p.tmdbGet(ctx, token, "/tv/"+ref.id, params, &show); err != nil {
		return nil, err
	}

	if show.Adult {
		return nil, errMediaNotFound
	}

	title := cleanText(plainText(show.Name), maxMediaTitle)
	if title == "" {
		return nil, errMediaNotFound
	}

	facts := append(genreNames(show.Genres), seasonsText(show.NumberOfSeasons))

	return &mediaDetails{
		info: scheduleMedia{
			Ref:      ref.String(),
			Kind:     mediaKindTV,
			Title:    title,
			Year:     yearOf(show.FirstAirDate),
			Overview: overviewOf(show.Overview, show.Tagline),
			Facts:    factLine(facts...),
			Link:     tmdbLink(ref, show.ExternalIDs.IMDBID),
			Source:   mediaSourceTMDB,
		},
		art: mediaArt{Poster: show.PosterPath, Backdrop: show.BackdropPath},
		// A show still being made gains seasons, and its facts line says how
		// many.
		changing: tmdbShowRunning[show.Status],
	}, nil
}

// tmdbShowRunning are TMDB's statuses for a show that is not finished.
var tmdbShowRunning = map[string]bool{"Returning Series": true, "In Production": true, "Planned": true, "Pilot": true}

// notYetOut is true for a release date that is missing, unreadable or still
// to come, by the store's clock.
func (p *Plugin) notYetOut(date string) bool {
	day, err := time.Parse("2006-01-02", strings.TrimSpace(date))

	return err != nil || day.After(p.mediaStore().now())
}

type steamEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type steamApp struct {
	Name             string `json:"name"`
	ShortDescription string `json:"short_description"`
	HeaderImage      string `json:"header_image"`
	CapsuleImage     string `json:"capsule_image"`
	Genres           []struct {
		Description string `json:"description"`
	} `json:"genres"`
	Developers  []string `json:"developers"`
	ReleaseDate struct {
		ComingSoon bool   `json:"coming_soon"`
		Date       string `json:"date"`
	} `json:"release_date"`
}

func (p *Plugin) steamGame(ctx context.Context, ref mediaRef) (*mediaDetails, error) {
	params := url.Values{}
	params.Set("appids", ref.id)
	params.Set("l", "english")
	params.Set("cc", "US")

	var answer map[string]steamEnvelope
	if err := p.steamGet(ctx, "/api/appdetails", params, &answer); err != nil {
		return nil, err
	}

	// The answer is an object keyed by an app id, and NOT necessarily the one
	// asked for: Steam answers for the current edition of a game, so asking
	// for Elden Ring's 1245620 came back keyed 2855530. One id was asked for,
	// so the single entry is the answer whatever it is keyed by. The asked-for
	// key wins only in the case that has never been seen, of there being more.
	entry, found := answer[ref.id]
	if !found && len(answer) == 1 {
		for _, only := range answer {
			entry, found = only, true
		}
	}
	if !found || !entry.Success || len(entry.Data) == 0 {
		return nil, errMediaNotFound
	}

	var app steamApp
	if err := json.Unmarshal(entry.Data, &app); err != nil {
		return nil, errors.New("Steam: the answer was not the expected shape")
	}

	title := cleanText(plainText(app.Name), maxMediaTitle)
	if title == "" {
		return nil, errMediaNotFound
	}

	facts := make([]string, 0, maxMediaGenres+1)
	for _, genre := range app.Genres {
		if len(facts) == maxMediaGenres {
			break
		}
		facts = append(facts, genre.Description)
	}
	if len(app.Developers) > 0 {
		facts = append(facts, app.Developers[0])
	}

	return &mediaDetails{
		info: scheduleMedia{
			Ref:      ref.String(),
			Kind:     mediaKindGame,
			Title:    title,
			Year:     yearOf(app.ReleaseDate.Date),
			Overview: overviewOf(app.ShortDescription, ""),
			Facts:    factLine(facts...),
			Link:     "https://store.steampowered.com/app/" + ref.id + "/",
			Source:   mediaSourceSteam,
		},
		art:      mediaArt{Capsule: app.CapsuleImage, Header: app.HeaderImage},
		changing: app.ReleaseDate.ComingSoon,
	}, nil
}

// planMedia is what a plan being saved will carry about its title, or nil.
//
// Nil is never an error. The title is the least important thing about a plan:
// a provider that is down, slow or has never heard of it costs the plan its
// poster, not its place on the schedule — and the wait is bounded, because
// the person pressing Save is watching a spinner.
func (p *Plugin) planMedia(ctx context.Context, ref mediaRef) *scheduleMedia {
	cfg := p.getConfiguration()
	if !cfg.mediaFlags().allowsRef(ref) {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, mediaPlanBudget)
	defer cancel()

	details, err := p.mediaDetailsFor(ctx, cfg, ref)
	if err != nil {
		p.API.LogWarn("A planned stream was saved without its title's details, which could not be looked up",
			"ref", ref.String(), "error", err.Error())

		return nil
	}

	info := details.info

	return &info
}

// pictureAddresses lists where one size of a title's art may be found, best
// first. Only TMDB's and IGDB's documented patterns and Steam's portrait box
// art are built; every other address is one the provider gave.
func pictureAddresses(ref mediaRef, art mediaArt, size string) []string {
	var addresses []string

	switch ref.provider {
	case mediaProviderSteam:
		switch size {
		case mediaSizeThumb:
			addresses = []string{art.Capsule}
		case mediaSizeCover:
			addresses = []string{steamLibraryURL(ref), art.Header}
		case mediaSizeBackdrop:
			addresses = []string{art.Header}
		}
	case mediaProviderIGDB:
		// IGDB's cover is box art, portrait like a poster. Its wide pictures
		// are artworks and screenshots; a game with neither still has its
		// cover, larger, which is better than nothing behind a card.
		switch size {
		case mediaSizeThumb:
			addresses = []string{igdbImageURL("cover_small", art.Poster)}
		case mediaSizeCover:
			addresses = []string{igdbImageURL("cover_big", art.Poster)}
		case mediaSizeBackdrop:
			addresses = []string{igdbImageURL("screenshot_big", art.Backdrop), igdbImageURL("720p", art.Poster)}
		}
	default:
		switch size {
		case mediaSizeThumb:
			addresses = []string{tmdbImageURL("w92", art.Poster)}
		case mediaSizeCover:
			addresses = []string{tmdbImageURL("w342", art.Poster)}
		case mediaSizeBackdrop:
			// A title with no backdrop still has a poster, and a poster
			// faded behind a card is better than nothing there at all.
			addresses = []string{tmdbImageURL("w780", art.Backdrop), tmdbImageURL("w780", art.Poster)}
		}
	}

	kept := addresses[:0]
	for _, address := range addresses {
		if address != "" {
			kept = append(kept, address)
		}
	}

	return kept
}

// steamLibraryURL is the portrait box art. Verified to live at this fixed
// address for games that have it — the one Steam picture that can be built
// rather than taken from an answer — and the only one the right shape for a
// cover.
func steamLibraryURL(ref mediaRef) string {
	return steamLibraryBase + "/" + ref.id + "/library_600x900.jpg"
}

// tmdbImageURL joins a TMDB path to a size, or is "" for a path that is not
// the shape TMDB's are. The path comes from TMDB's answer; checking it anyway
// means nothing but a file name is ever appended to the image host.
func tmdbImageURL(size, path string) string {
	if !tmdbArtPath.MatchString(path) {
		return ""
	}

	return tmdbImageBase + "/" + size + path
}

// loadMediaPicture finds one size of a title's art and fetches it.
//
// Nil with no error is "there is no such picture": the title is gone, or
// every address for this size answered 404 or 410 and the details that gave
// them are ones the provider stands by now (or list no address for this size
// at all). Nil with errMediaPictureUnsure is no picture and nothing that says
// there is none. Any other error is "could not find out". The difference is
// what the disk does with a picture it already holds (fetchPicture): only the
// first drops it.
func (p *Plugin) loadMediaPicture(ctx context.Context, cfg *configuration, ref mediaRef, size string) (*mediaPicture, error) {
	started := p.mediaStore().now()
	tried := map[string]bool{}
	var failed error

	// gone stays true while every address tried has answered that there is
	// no such picture there — the sign of art that has been replaced.
	gone := true

	// missing is narrower: every address tried answered 404 or 410, which is
	// the only answer believed as "this picture is not there". A 403 can be a
	// firewall, and something that is not a picture can be a captive portal.
	missing := true

	attempt := func(addresses []string) *mediaPicture {
		for _, address := range addresses {
			if tried[address] {
				continue
			}
			tried[address] = true

			picture, err := p.fetchMediaPicture(ctx, address)
			if err == nil {
				return picture
			}

			// A missing, refused, oversized or non-picture answer means try
			// the next address. Anything else is the provider not answering,
			// which is worth reporting if nothing else works either.
			if !errors.Is(err, errMediaNotFound) && !errors.Is(err, errMediaRefused) &&
				!errors.Is(err, errMediaTooLarge) && !errors.Is(err, errMediaNotImage) {
				failed = err
			}
			if !errors.Is(err, errMediaNotFound) && !errors.Is(err, errMediaRefused) && !errors.Is(err, errMediaNotImage) {
				gone = false
			}
			if !errors.Is(err, errMediaNotFound) || errors.Is(err, errMediaForbidden) {
				missing = false
			}
		}

		return nil
	}

	// First, whatever can be had without asking for the title's details: the
	// art the search that listed it described, and Steam's box art, whose
	// address is fixed. The planning form's thumbnails are all served this
	// way, one request each.
	var first []string
	if ref.provider == mediaProviderSteam && size == mediaSizeCover {
		first = append(first, steamLibraryURL(ref))
	}
	if hint, hinted := p.mediaStore().hints.get(ref.String()); hinted {
		// Except an IGDB backdrop. A search describes only a game's cover, so
		// from the hint alone the backdrop would be the cover's portrait
		// stand-in, fetched and kept for hours before the details, which may
		// hold wide art, were asked for at all. For that size they decide.
		if ref.provider != mediaProviderIGDB || size != mediaSizeBackdrop || hint.Backdrop != "" {
			first = append(first, pictureAddresses(ref, hint, size)...)
		}
	}
	if picture := attempt(first); picture != nil {
		return picture, nil
	}

	details, err := p.mediaDetailsFor(ctx, cfg, ref)
	switch {
	case errors.Is(err, errMediaNotFound):
		// No such title, so no such picture.
	case err != nil:
		return nil, err
	default:
		addresses := pictureAddresses(ref, details.art, size)
		if picture := attempt(addresses); picture != nil {
			return picture, nil
		}

		// Details past their date are a stand-in for a provider that could
		// not be asked: the addresses they give may simply be old ones.
		current := started.Before(details.until)

		// Art addresses go out of date: IGDB deletes replaced pictures after
		// a month, and Steam's change their hash when the art changes. So when
		// every address the details gave is gone, and the details are more
		// than a day old, they are asked for once more. A title whose details
		// list no address for this size is not: there was nothing to replace,
		// and asking would cost a lookup per size per day for nothing.
		stale := p.mediaStore().now().Sub(details.fetchedAt) > mediaRefreshArtAfter
		if len(addresses) > 0 && gone && stale {
			fresh, err := p.refreshMediaDetails(ctx, cfg, ref)
			switch {
			case err == nil:
				if picture := attempt(pictureAddresses(ref, fresh.art, size)); picture != nil {
					return picture, nil
				}

				// A provider that could not be asked, or said once that it
				// has no such title, hands back what was remembered: only
				// an answer it has just given is one it stands by.
				current = !fresh.fetchedAt.Before(started)
			case errors.Is(err, errMediaNotFound):
				// Gone, and believed: no such title, so no such picture.
				return nil, failed
			default:
				current = false
			}
		}

		if failed == nil && (!current || !missing) {
			return nil, errMediaPictureUnsure
		}
	}

	return nil, failed
}

// fetchMediaPicture fetches one picture, if it is on an allowed host, within
// the size cap, and really a picture.
func (p *Plugin) fetchMediaPicture(ctx context.Context, address string) (*mediaPicture, error) {
	parsed, err := url.Parse(address)
	if err != nil || !mediaImageURLAllowed(parsed) {
		host := ""
		if parsed != nil {
			host = parsed.Host
		}
		p.warnMediaOnce("picture-host",
			"A provider described a picture on a host the plugin does not fetch from, so it was not fetched",
			"host", host)

		return nil, errMediaRefused
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errMediaRefused
	}
	request.Header.Set("Accept", "image/webp,image/jpeg,image/png,image/gif")

	response, err := sendMedia(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, mediaStatus(response.StatusCode)
	}
	if response.ContentLength > maxMediaImageBytes {
		return nil, errMediaTooLarge
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxMediaImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read it: %w", err)
	}
	if len(data) > maxMediaImageBytes {
		return nil, errMediaTooLarge
	}

	// From the bytes, as for an upload, and never an SVG: every picture these
	// providers serve is a photograph, so an SVG — which is a document, and
	// can carry a script — is something that should not have been there.
	contentType, isImage := imageType(data)
	if !isImage || contentType == "image/svg+xml" {
		return nil, errMediaNotImage
	}

	return &mediaPicture{contentType: contentType, data: data}, nil
}

// yearOf finds a four-digit year in a date as TMDB and Steam write one:
// TMDB's "1995-12-15" and Steam's "Feb 24, 2022". Steam's "Coming soon" has
// none, and "" is the honest answer. IGDB's dates are Unix times, which
// igdbYear reads.
func yearOf(date string) string {
	return yearPattern.FindString(date)
}

// mediaQueryKey is how a query or a title is compared: case, punctuation and
// spacing do not make a different search. Apostrophes vanish rather than
// split a word, so "Assassin's" and "Assassins" are one. No folding of
// accents: that would need a new dependency, and "é" typed is rarely "e"
// meant.
func mediaQueryKey(text string) string {
	var key strings.Builder

	for _, r := range strings.ToLower(text) {
		switch {
		case r == '\'' || r == '’':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			key.WriteRune(r)
		default:
			key.WriteRune(' ')
		}
	}

	return strings.Join(strings.Fields(key.String()), " ")
}

// plainText is a provider's text as plain words. Steam's descriptions carry
// markup and entities ("Tom Clancy&#39;s"); the panel shows text, never HTML,
// so both come out, and runs of whitespace become one space.
func plainText(value string) string {
	value = markupPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)

	return strings.Join(strings.Fields(value), " ")
}

// overviewOf is a title's description, trimmed at a word so it reads as a
// sentence that goes on rather than one cut off mid-word. The ellipsis counts
// towards the limit, so the whole thing fits the profile card as designed.
func overviewOf(overview, fallback string) string {
	text := cleanText(plainText(overview), 4*maxMediaOverview)
	if text == "" {
		text = cleanText(plainText(fallback), 4*maxMediaOverview)
	}

	if utf8.RuneCountInString(text) <= maxMediaOverview {
		return text
	}

	runes := []rune(text)[:maxMediaOverview-1]
	cut := string(runes)

	// Back to the last space, unless that throws away most of it: one very
	// long word should be cut, not deleted.
	if space := strings.LastIndexByte(cut, ' '); space > len(cut)/2 {
		cut = cut[:space]
	}

	return strings.TrimRight(cut, " ,;:-–—") + "…"
}

// factLine is the one line of facts under a title: genres, then how long or
// how many seasons, or who made it. Whole facts only — one that would not fit
// is left off rather than cut, because "Action · RPG · FromSoftw…" says less
// than "Action · RPG".
func factLine(parts ...string) string {
	line := ""

	for _, part := range parts {
		// Bounded, but loosely: a single fact too long for the line has to
		// arrive too long, so it is cut below with an ellipsis rather than
		// silently here without one.
		part = cleanText(plainText(part), 4*maxMediaFacts)
		if part == "" {
			continue
		}

		next := part
		if line != "" {
			next = line + " · " + part
		}

		if utf8.RuneCountInString(next) > maxMediaFacts {
			if line == "" {
				line = clampRunes(part, maxMediaFacts-1)
			}

			break
		}

		line = next
	}

	return line
}

func genreNames(genres []tmdbGenre) []string {
	names := make([]string, 0, maxMediaGenres+1)
	for _, genre := range genres {
		if len(names) == maxMediaGenres {
			break
		}
		names = append(names, genre.Name)
	}

	return names
}

func runtimeText(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
	}
}

func seasonsText(seasons int) string {
	switch {
	case seasons <= 0:
		return ""
	case seasons == 1:
		return "1 season"
	default:
		return fmt.Sprintf("%d seasons", seasons)
	}
}

// tmdbLink is where a movie or show's title leads: its IMDb page when TMDB
// knows it, because that is the page people expect, and otherwise TMDB's own.
func tmdbLink(ref mediaRef, imdbID string) string {
	if imdbIDPattern.MatchString(imdbID) {
		return "https://www.imdb.com/title/" + imdbID + "/"
	}

	return "https://www.themoviedb.org/" + ref.kind + "/" + ref.id
}

// memoryCache holds what the providers said, for a while, within a bound.
//
// Bounded twice over — by entries, and for pictures by bytes — because what
// goes in is decided by what people type and which titles they pick, and a
// cache whose size its users decide is a memory leak with extra steps. When it
// is full, anything expired goes first, then whatever was used least recently.
type memoryCache[V any] struct {
	lock     sync.Mutex
	entries  map[string]*memoryEntry[V]
	maxItems int
	maxBytes int
	bytes    int
	sizeOf   func(V) int
}

type memoryEntry[V any] struct {
	value   V
	size    int
	expires time.Time
	used    time.Time
}

func newMemoryCache[V any](maxItems, maxBytes int, sizeOf func(V) int) *memoryCache[V] {
	return &memoryCache[V]{
		entries:  map[string]*memoryEntry[V]{},
		maxItems: maxItems,
		maxBytes: maxBytes,
		sizeOf:   sizeOf,
	}
}

func (c *memoryCache[V]) get(key string) (V, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	now := time.Now()

	entry, found := c.entries[key]
	if !found || now.After(entry.expires) {
		if found {
			c.drop(key, entry)
		}

		var nothing V

		return nothing, false
	}

	entry.used = now

	return entry.value, true
}

func (c *memoryCache[V]) put(key string, value V, ttl time.Duration) {
	size := 0
	if c.sizeOf != nil {
		size = c.sizeOf(value)
	}

	// Something bigger than the whole cache is not kept at all, rather than
	// emptying the cache to make room for it.
	if c.maxBytes > 0 && size > c.maxBytes {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	now := time.Now()

	if old, found := c.entries[key]; found {
		c.drop(key, old)
	}

	full := func() bool {
		return len(c.entries) >= c.maxItems || (c.maxBytes > 0 && c.bytes+size > c.maxBytes)
	}

	if full() {
		for key, entry := range c.entries {
			if now.After(entry.expires) {
				c.drop(key, entry)
			}
		}
	}

	for full() && len(c.entries) > 0 {
		var oldestKey string
		var oldest *memoryEntry[V]
		for key, entry := range c.entries {
			if oldest == nil || entry.used.Before(oldest.used) {
				oldestKey, oldest = key, entry
			}
		}
		c.drop(oldestKey, oldest)
	}

	c.entries[key] = &memoryEntry[V]{value: value, size: size, expires: now.Add(ttl), used: now}
	c.bytes += size
}

func (c *memoryCache[V]) drop(key string, entry *memoryEntry[V]) {
	delete(c.entries, key)
	c.bytes -= entry.size
}

// forget lets go of one entry, for something known to have changed rather
// than merely grown old.
func (c *memoryCache[V]) forget(key string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if entry, found := c.entries[key]; found {
		c.drop(key, entry)
	}
}

// clear forgets everything at once, for a cache whose contents are no longer
// wanted at all rather than merely old.
func (c *memoryCache[V]) clear() {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.entries = map[string]*memoryEntry[V]{}
	c.bytes = 0
}

// flightGroup lets one caller do a piece of work while any others asking for
// the same key at the same time wait for its result, rather than each doing
// it again. The same idea as golang.org/x/sync/singleflight, in the dozen
// lines this needs, rather than a new dependency.
type flightGroup struct {
	lock  sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	done  chan struct{}
	value any
	err   error
}

// doDetached is do, except that the work runs on a goroutine of its own and
// the caller waits for it or for ctx, whichever ends first. The work goes on
// when the caller stops waiting, so what it finds still lands wherever the
// work keeps it, for the next caller. Work that panics is logged under where
// and answers errMediaPanicked to everybody waiting for it, who are let go by
// the defer as after any other answer.
func (g *flightGroup) doDetached(ctx context.Context, log panicLog, where, key string, work func() (any, error)) (any, error) {
	g.lock.Lock()
	if g.calls == nil {
		g.calls = map[string]*flightCall{}
	}
	call, running := g.calls[key]
	if !running {
		call = &flightCall{done: make(chan struct{})}
		g.calls[key] = call

		go func() {
			defer func() {
				g.lock.Lock()
				delete(g.calls, key)
				g.lock.Unlock()
				close(call.done)
			}()

			if safely(log, where, func() { call.value, call.err = work() }) {
				call.value, call.err = nil, errMediaPanicked
			}
		}()
	}
	g.lock.Unlock()

	select {
	case <-call.done:
		return call.value, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *flightGroup) do(key string, work func() (any, error)) (any, error) {
	g.lock.Lock()
	if g.calls == nil {
		g.calls = map[string]*flightCall{}
	}
	if call, running := g.calls[key]; running {
		g.lock.Unlock()
		<-call.done

		return call.value, call.err
	}

	call := &flightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.lock.Unlock()

	// Released however the work ends, so nobody waits for ever.
	defer func() {
		g.lock.Lock()
		delete(g.calls, key)
		g.lock.Unlock()
		close(call.done)
	}()

	call.value, call.err = work()

	return call.value, call.err
}
