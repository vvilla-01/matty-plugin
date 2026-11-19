package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Games from IGDB, Twitch's game database, for the games the Steam store does
// not have: Hytale and Vintage Story, which were never on it, and the games
// Steam no longer sells, which its search no longer lists.
//
// Asked only while MediaLookup is on AND the admin has set up a Twitch
// application (mediaFlags().IGDB), and always signed with that application's
// app access token from twitch_app.go. Steam stays the first source for games;
// IGDB fills in what Steam leaves (mergeGames).
//
// Every request is a POST of an Apicalypse query. The query is fixed text,
// plus an id the server parsed itself or the person's words reduced to a set
// of characters that cannot end a string or start a clause (igdbSearchText),
// because IGDB documents no grammar for its strings at all.

// Where IGDB is. Variables only so tests can point them at a fake.
var (
	igdbAPIBase   = "https://api.igdb.com/v4"
	igdbImageBase = "https://images.igdb.com/igdb/image/upload"

	// igdbGrace is how long a game search goes on waiting for IGDB once Steam
	// has answered with games. Past it the person gets Steam's games, marked
	// as missing IGDB's; IGDB's answer still lands in memory and the
	// remembered titles for the form to ask again. A variable so tests can
	// shorten it.
	igdbGrace = 1500 * time.Millisecond
)

const (
	// Raw rows asked for. At most eight are kept after filtering, and a name
	// with many add-ons still fills them with games.
	igdbSearchLimit = 16

	// After IGDB times out, says slow down or fails, it is left alone this
	// long, so a struggling IGDB does not hold up every game search.
	igdbFailurePause = 30 * time.Second

	igdbSearchFields = "name,first_release_date,cover.image_id,game_type.type,parent_game,version_parent," +
		"external_games.external_game_source,external_games.uid,external_games.url,websites.type,websites.url"
	igdbGameFields = "name,slug,url,summary,storyline,first_release_date,cover.image_id,artworks.image_id," +
		"screenshots.image_id,genres.name,themes,game_type.type,game_status.status,involved_companies.developer," +
		"involved_companies.company.name"

	// IGDB's documented ids: the erotic theme, which a work chat leaves out,
	// and Steam in the external game sources.
	igdbEroticTheme = 42
	igdbSteamSource = 1
)

var (
	igdbImageID        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	igdbSlugPattern    = regexp.MustCompile(`^[a-z0-9-]{1,120}$`)
	igdbLinkPattern    = regexp.MustCompile(`^https://www\.igdb\.com/games/[a-z0-9-]{1,120}$`)
	steamAppURLPattern = regexp.MustCompile(`^https?://store\.steampowered\.com/app/([0-9]{1,10})(?:[/?#]|$)`)
	steamAppIDPattern  = regexp.MustCompile(`^[0-9]{1,10}$`)
	igdbGenreShort     = regexp.MustCompile(`^(.+) \(([A-Z]{2,6})\)$`)
	igdbEnumSeparator  = regexp.MustCompile(`[^a-z0-9]+`)

	// What counts as a game somebody could play, by IGDB's documented game
	// types: by name when the text comes back, by id when only that does. DLC,
	// bundles, mods, episodes, seasons, forks, packs and updates are not.
	igdbPlayableTypes   = map[string]bool{"main_game": true, "standalone_expansion": true, "remake": true, "remaster": true, "expanded_game": true, "port": true}
	igdbPlayableTypeIDs = map[int64]bool{0: true, 4: true, 8: true, 9: true, 10: true, 11: true}

	// A game in these states is still settling, and is remembered for less
	// long (media_cache.go).
	igdbChangingStatuses   = map[string]bool{"alpha": true, "beta": true, "early_access": true, "rumored": true}
	igdbChangingStatusIDs  = map[int64]bool{2: true, 3: true, 4: true, 7: true}
	errIGDBUnexpectedShape = errors.New("IGDB: the answer was not the expected shape")
)

// igdbObject is true for JSON that is an object. IGDB returns a reference as a
// bare id unless it was expanded, and as an object when it was, so anything
// read from a reference has to cope with both.
func igdbObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)

	return len(raw) > 0 && raw[0] == '{'
}

// igdbRef is an IGDB reference whether it arrives as 12 or as {"id":12,...}.
type igdbRef struct{ ID int64 }

func (r *igdbRef) UnmarshalJSON(raw []byte) error {
	if igdbObject(raw) {
		var object struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		r.ID = object.ID

		return nil
	}

	return json.Unmarshal(raw, &r.ID)
}

// igdbEnum is a game type or game status, whether it arrives expanded with its
// text or as a bare id. IGDB's docs give the ids but not the text, so both are
// read and either decides.
type igdbEnum struct {
	ID   int64
	Text string
}

func (e *igdbEnum) UnmarshalJSON(raw []byte) error {
	if igdbObject(raw) {
		var object struct {
			ID     int64  `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		e.ID, e.Text = object.ID, object.Type
		if e.Text == "" {
			e.Text = object.Status
		}

		return nil
	}

	return json.Unmarshal(raw, &e.ID)
}

// name is the text as IGDB's enum names are written: "Main Game" is
// main_game, "Early Access" early_access.
func (e *igdbEnum) name() string {
	return strings.Trim(igdbEnumSeparator.ReplaceAllString(strings.ToLower(e.Text), "_"), "_")
}

// igdbImage is a picture IGDB describes. A bare id says nothing about where
// the picture is, so it reads as none.
type igdbImage struct{ ImageID string }

func (i *igdbImage) UnmarshalJSON(raw []byte) error {
	if !igdbObject(raw) {
		return nil
	}

	var object struct {
		ImageID string `json:"image_id"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	i.ImageID = object.ImageID

	return nil
}

// igdbNamed is a genre or a company, of which only the name is wanted.
type igdbNamed struct{ Name string }

func (n *igdbNamed) UnmarshalJSON(raw []byte) error {
	if !igdbObject(raw) {
		return nil
	}

	var object struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	n.Name = object.Name

	return nil
}

// igdbLink is a website or an external game record: where else the game is.
type igdbLink struct {
	Type   igdbRef `json:"type"`                 // websites
	Source igdbRef `json:"external_game_source"` // external_games
	UID    string  `json:"uid"`
	URL    string  `json:"url"`
}

func (l *igdbLink) UnmarshalJSON(raw []byte) error {
	if !igdbObject(raw) {
		return nil
	}

	type plain igdbLink

	return json.Unmarshal(raw, (*plain)(l))
}

type igdbInvolved struct {
	Developer bool       `json:"developer"`
	Company   *igdbNamed `json:"company"`
}

func (c *igdbInvolved) UnmarshalJSON(raw []byte) error {
	if !igdbObject(raw) {
		return nil
	}

	type plain igdbInvolved

	return json.Unmarshal(raw, (*plain)(c))
}

// igdbGame is one row of IGDB's games, with only the fields the plugin asks
// for. The field names are IGDB's current ones (game_type, game_status,
// external_game_source, a website's type): the old enum fields are gone.
type igdbGame struct {
	ID                int64          `json:"id"`
	Name              string         `json:"name"`
	Slug              string         `json:"slug"`
	URL               string         `json:"url"`
	Summary           string         `json:"summary"`
	Storyline         string         `json:"storyline"`
	FirstReleaseDate  int64          `json:"first_release_date"`
	Cover             *igdbImage     `json:"cover"`
	Artworks          []igdbImage    `json:"artworks"`
	Screenshots       []igdbImage    `json:"screenshots"`
	Genres            []igdbNamed    `json:"genres"`
	Themes            []igdbRef      `json:"themes"`
	GameType          *igdbEnum      `json:"game_type"`
	GameStatus        *igdbEnum      `json:"game_status"`
	VersionParent     igdbRef        `json:"version_parent"`
	ParentGame        igdbRef        `json:"parent_game"`
	ExternalGames     []igdbLink     `json:"external_games"`
	Websites          []igdbLink     `json:"websites"`
	InvolvedCompanies []igdbInvolved `json:"involved_companies"`
}

// igdbPost asks IGDB one Apicalypse query. Every call is a POST with the query
// as the body, signed with the Twitch application's app access token.
//
// IGDB's limits are its terms, not a suggestion: 4 requests a second, 8 open
// at once. So the server's whole budget is checked on the store's clock as
// each request goes out (igdbSend), and a request over it is refused rather
// than sent.
func (p *Plugin) igdbPost(ctx context.Context, cfg *configuration, endpoint, body string, into any) error {
	creds, set := cfg.twitchCredentials()
	if !set {
		return fmt.Errorf("IGDB: %w", errTwitchNotSetUp)
	}

	store := p.mediaStore()
	fp := creds.fingerprint()

	for attempt := 0; ; attempt++ {
		// Held back: nothing is asked of id.twitch.tv or IGDB at all.
		if err := store.igdbHeld(fp); err != nil {
			return fmt.Errorf("IGDB: %w", err)
		}

		token, err := p.twitchApp().appToken(ctx, creds)
		if err != nil {
			return fmt.Errorf("IGDB: %w", err)
		}

		err = p.igdbSend(ctx, creds, fp, token, endpoint, body, into)

		var answered *mediaStatusError
		status := 0
		if errors.As(err, &answered) {
			status = answered.status
		}

		switch {
		case err == nil:
			store.igdbAnswered()
			return nil

		case status == http.StatusUnauthorized && attempt == 0 && !store.igdbRefused(token):
			// The token was cancelled or has lapsed: a new one, and once more.
			// The retry is a real request and takes its own place in the
			// budget; the four-a-second window leaves room for it.
			p.twitchApp().invalidate(token)
			continue

		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			// A token fetched moments ago and still refused, or a 403, means
			// the token is not what is wrong. Minting another would only spend
			// one of the application's 25 active tokens, and going over cancels
			// the oldest, which are other nodes' and other features'. So the
			// token is kept, and IGDB is left alone for a while.
			store.holdIGDB(fp, twitchRefusedPause, errTwitchRefused, token)
			p.warnMediaOnce("igdb-token",
				"IGDB refused Matty's Twitch access token (401/403), so games not on Steam cannot be looked up. "+
					"Check the Client ID and Client Secret in System Console > Plugins > Matty > Twitch.")

			return fmt.Errorf("IGDB: %w", errTwitchRefused)

		case status == http.StatusBadRequest:
			p.warnMediaOnce("igdb-query",
				"IGDB refused a query Matty built (400 Bad Request). If this keeps happening, IGDB may have changed "+
					"its fields and games not on Steam cannot be looked up until the plugin is updated.")

		case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError ||
			status == http.StatusNotFound || status == http.StatusGone:
			// Slow down, failing, or (a 404 or 410, see igdbSend) not where
			// it should be: left alone for a while either way.
			store.holdIGDB(fp, igdbFailurePause, errIGDBResting, "")

		case errors.Is(err, context.Canceled):
			// Whoever asked went away. IGDB did nothing wrong.

		case errors.Is(err, errMediaUnreachable) || errors.Is(err, context.DeadlineExceeded):
			// Unreachable, or too slow for the client's timeout or the search's
			// budget.
			store.holdIGDB(fp, igdbFailurePause, errIGDBResting, "")
		}

		return fmt.Errorf("IGDB: %w", err)
	}
}

// igdbSend makes one request to IGDB, within its limit of open requests and
// its rates.
func (p *Plugin) igdbSend(ctx context.Context, creds twitchCredentials, fp [32]byte, token, endpoint, body string, into any) error {
	store := p.mediaStore()

	select {
	case store.igdbSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-store.igdbSlots }()

	// The place in the budget is taken here, with a slot in hand and the
	// request about to go, so the window counts requests when they are sent.
	// Counted when first asked for, requests that then waited for a token or
	// a slot went out together, more than four inside one second.
	if err := store.igdbMayAsk(fp); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, igdbAPIBase+"/"+endpoint, strings.NewReader(body))
	if err != nil {
		return errors.New("the request could not be built")
	}

	// Set directly rather than through Set, which would write it Client-Id.
	// Header names are not case-sensitive, but this is how IGDB writes it.
	request.Header["Client-ID"] = []string{creds.clientID}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "text/plain")

	// IGDB answers "no such game" with 200 and an empty list, never with a
	// status. A 404 or a 410 from it is a wrong address or a failure on its
	// side, and must not be remembered as a game that does not exist.
	return getMediaJSONAs(request, into, func(status int) error {
		return &mediaStatusError{status: status}
	})
}

// igdbHeld is the hold-back's reason while IGDB is being left alone for these
// credentials, or nil.
func (s *mediaStore) igdbHeld(fp [32]byte) error {
	now := s.now()

	s.igdbLock.Lock()
	defer s.igdbLock.Unlock()

	if now.Before(s.igdbHeldUntil) && fp == s.igdbHeldFor {
		return s.igdbHeldErr
	}

	return nil
}

// igdbMayAsk is whether a request to IGDB may go out now: not held back (which
// may have begun while it waited), and within both of IGDB's rates. A request
// that may go is counted.
func (s *mediaStore) igdbMayAsk(fp [32]byte) error {
	now := s.now()

	s.igdbLock.Lock()
	defer s.igdbLock.Unlock()

	if now.Before(s.igdbHeldUntil) && fp == s.igdbHeldFor {
		return s.igdbHeldErr
	}

	// The window is looked at before the bucket is spent, so a request the
	// window refuses does not also cost a place in the minute.
	if now.Sub(s.igdbStarts[s.igdbNext]) < time.Second || !s.igdbLimiter.allowAt("igdb", now) {
		return errMediaBusy
	}

	s.igdbStarts[s.igdbNext] = now
	s.igdbNext = (s.igdbNext + 1) % igdbCallsPerSecond

	return nil
}

// holdIGDB leaves IGDB alone for a while, for these credentials. New
// credentials end it at once.
func (s *mediaStore) holdIGDB(fp [32]byte, pause time.Duration, why error, refusedToken string) {
	now := s.now()

	s.igdbLock.Lock()
	defer s.igdbLock.Unlock()

	s.igdbHeldUntil = now.Add(pause)
	s.igdbHeldFor = fp
	s.igdbHeldErr = why
	if refusedToken != "" {
		s.igdbRefusedToken = refusedToken
	}
}

// igdbRefused is whether IGDB has already refused this very token after it
// was fetched new, in which case a new one would not help.
func (s *mediaStore) igdbRefused(token string) bool {
	s.igdbLock.Lock()
	defer s.igdbLock.Unlock()

	return token != "" && token == s.igdbRefusedToken
}

// igdbAnswered is IGDB accepting a request: whatever token it refused before,
// that episode is over. Kept, the mark would make a later 401 with the same
// token, which by then is Twitch having cancelled it, hold IGDB back instead
// of earning a new one.
func (s *mediaStore) igdbAnswered() {
	s.igdbLock.Lock()
	defer s.igdbLock.Unlock()

	s.igdbRefusedToken = ""
}

// igdbSearchText is what somebody typed, made safe to put between the quotes
// of an IGDB search. IGDB documents nothing about its strings — its own docs
// write a search with curly quotes — so rather than guess what would end one,
// only a known-safe set of characters is kept: letters, digits, spaces and
// - : . , & ! ? +. Apostrophes are removed without a space, as mediaQueryKey
// does, so "Baldur's Gate" is sent as Baldurs Gate; IGDB searches by
// similarity, so nothing is lost. Everything else becomes a space.
func igdbSearchText(query string) string {
	var text strings.Builder

	for _, r := range query {
		switch {
		case r == '\'' || r == '’':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune("-:.,&!?+", r):
			text.WriteRune(r)
		default:
			text.WriteRune(' ')
		}
	}

	return strings.Join(strings.Fields(text.String()), " ")
}

// igdbSearchBody is the whole search query. version_parent = null is IGDB's
// documented way to leave editions out, themes != (42) leaves out erotic
// games, and the game_type group keeps the kinds of entry that are games
// somebody plays, letting untyped rows through to be judged by igdbPlayable.
func igdbSearchBody(text string) string {
	return fmt.Sprintf("search \"%s\";\n"+
		"fields %s;\n"+
		"where version_parent = null & themes != (%d) & (game_type = (0,4,8,9,10,11) | game_type = null);\n"+
		"limit %d;",
		text, igdbSearchFields, igdbEroticTheme, igdbSearchLimit)
}

// searchIGDB is IGDB's half of a game search: at most eight games, in IGDB's
// order of relevance.
func (p *Plugin) searchIGDB(ctx context.Context, cfg *configuration, query string) ([]mediaCachedResult, error) {
	text := igdbSearchText(query)
	if utf8.RuneCountInString(text) < minMediaQuery {
		return []mediaCachedResult{}, nil
	}

	var games []igdbGame
	if err := p.igdbPost(ctx, cfg, "games", igdbSearchBody(text), &games); err != nil {
		return nil, err
	}

	return igdbResults(games), nil
}

// igdbResults turns IGDB's rows into the planning form's list.
func igdbResults(games []igdbGame) []mediaCachedResult {
	results := make([]mediaCachedResult, 0, maxMediaResults)
	same := map[string]bool{}

	for i := range games {
		if len(results) == maxMediaResults {
			break
		}

		game := &games[i]
		if !igdbPlayable(game) || igdbErotic(game) {
			continue
		}

		ref, ok := newIGDBRef(game.ID)
		title := cleanText(plainText(game.Name), maxMediaTitle)
		if !ok || title == "" {
			continue
		}

		// The same game ported to five machines is five rows with one name
		// and one year, and one line is enough.
		year := igdbYear(game.FirstReleaseDate)
		key := mediaQueryKey(title) + "\x00" + year
		if same[key] {
			continue
		}
		same[key] = true

		result := mediaCachedResult{
			mediaResult: mediaResult{Ref: ref.String(), Kind: mediaKindGame, Title: title, Year: year, Label: "Game"},
			SteamApp:    igdbSteamApp(game),
		}
		if game.Cover != nil && igdbImageID.MatchString(game.Cover.ImageID) {
			result.Art.Poster = game.Cover.ImageID
		}

		results = append(results, result)
	}

	return results
}

// igdbPlayable is whether a row is a game somebody plays rather than an
// add-on, an edition or a bundle. The query's where clause does most of this
// already; these checks guard against IGDB changing how it answers.
func igdbPlayable(game *igdbGame) bool {
	switch {
	case game.GameType != nil:
		if name := game.GameType.name(); name != "" {
			if !igdbPlayableTypes[name] {
				return false
			}
		} else if !igdbPlayableTypeIDs[game.GameType.ID] {
			return false
		}
	case game.ParentGame.ID != 0:
		// Only for a row with no type at all. A remake, remaster or port has
		// a parent game too — the reverse lists are built from it — and once
		// the type has said it is one of those, it is kept.
		return false
	}

	return game.VersionParent.ID == 0
}

func igdbErotic(game *igdbGame) bool {
	for _, theme := range game.Themes {
		if theme.ID == igdbEroticTheme {
			return true
		}
	}

	return false
}

// igdbYear is the year of an IGDB date, which is a Unix time.
func igdbYear(unix int64) string {
	if unix <= 0 {
		return ""
	}

	year := time.Unix(unix, 0).UTC().Year()
	if year < 1000 || year > 2999 {
		return ""
	}

	return strconv.Itoa(year)
}

// igdbSteamApp is the Steam app IGDB links a game to, or "". Recorded, not
// acted on: whether the game is left to Steam depends on whether Steam listed
// it for the same search (mergeGames), because Steam's search does not list
// the games it no longer sells.
func igdbSteamApp(game *igdbGame) string {
	for _, links := range [][]igdbLink{game.Websites, game.ExternalGames} {
		for _, link := range links {
			if match := steamAppURLPattern.FindStringSubmatch(link.URL); match != nil {
				if app := steamAppID(match[1]); app != "" {
					return app
				}
			}
		}
	}

	for _, link := range game.ExternalGames {
		if link.Source.ID == igdbSteamSource && steamAppIDPattern.MatchString(link.UID) {
			if app := steamAppID(link.UID); app != "" {
				return app
			}
		}
	}

	return ""
}

// steamAppID is a Steam app id written the way a steam: ref writes it, or ""
// for one no ref could carry.
func steamAppID(digits string) string {
	id, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || id == 0 || id > 9999999999 {
		return ""
	}

	return strconv.FormatUint(id, 10)
}

// gamesAnswer is one provider's half of a game search.
type gamesAnswer struct {
	results []mediaCachedResult
	err     error
}

// searchGames asks Steam and, when it is set up, IGDB at the same time, and
// merges what they say. Steam's games do not wait for a slow IGDB beyond a
// short grace; one source failing still answers with the other; only both
// failing is a failure.
//
// The second value names a source that was asked but is missing from the
// answer: it did not answer in time, or failed. The planning form then says
// the list is the other source's alone and asks again, rather than say a game
// is on neither, which for Hytale would be exactly wrong.
func (p *Plugin) searchGames(ctx context.Context, cfg *configuration, query, normal string) ([]mediaCachedResult, string, error) {
	// Each half that panics is logged and answers errMediaPanicked, a failure
	// like any other: the other half still answers, and the search is never
	// left waiting for a half that will not send (recovery.go).
	steamDone := make(chan gamesAnswer, 1)
	go func() {
		answer := gamesAnswer{err: errMediaPanicked}
		defer func() { steamDone <- answer }()

		p.safely("the Steam half of a game search", func() {
			answer.results, answer.err = p.providerSearch(ctx, cfg, mediaProviderSteam, query, normal)
		})
	}()

	// IGDB is not asked about text with nothing left to search for once it is
	// made safe. It then counts as off: an empty list, and nothing remembered.
	var igdbDone chan gamesAnswer
	if cfg.mediaFlags().IGDB && utf8.RuneCountInString(igdbSearchText(query)) >= minMediaQuery {
		igdbDone = make(chan gamesAnswer, 1)
		go func() {
			answer := gamesAnswer{err: errMediaPanicked}
			defer func() { igdbDone <- answer }()

			p.safely("the IGDB half of a game search", func() {
				answer.results, answer.err = p.providerSearch(ctx, cfg, mediaProviderIGDB, query, normal)
			})
		}()
	}

	steam := <-steamDone

	var igdb gamesAnswer
	answered := false
	if igdbDone != nil {
		if steam.err == nil && len(steam.results) > 0 {
			grace := time.NewTimer(igdbGrace)
			select {
			case igdb = <-igdbDone:
				answered = true
			case <-grace.C:
			case <-ctx.Done():
			}
			grace.Stop()
		} else {
			// Steam failed, or has nothing to show, which is exactly the case
			// for the games IGDB is here for: IGDB is all there is, waited for
			// until the search's budget ends.
			select {
			case igdb = <-igdbDone:
				answered = true
			case <-ctx.Done():
			}
		}
	}

	// IGDB missing from the answer: asked, and not heard from in time or
	// failed. Not set up at all is IGDB off, not missing.
	igdbMissing := ""
	if igdbDone != nil && (!answered || (igdb.err != nil && !errors.Is(igdb.err, errTwitchNotSetUp))) {
		igdbMissing = mediaSourceIGDB
	}

	switch {
	case steam.err == nil && (!answered || igdb.err == nil):
		return mergeGames(steam.results, igdb.results, normal), igdbMissing, nil
	case steam.err == nil:
		p.warnPartialSearch(mediaSourceIGDB, igdb.err)
		return mergeGames(steam.results, nil, normal), igdbMissing, nil
	case answered && igdb.err == nil:
		p.warnPartialSearch(mediaSourceSteam, steam.err)
		return mergeGames(nil, igdb.results, normal), mediaSourceSteam, nil
	default:
		return nil, "", steam.err
	}
}

// warnPartialSearch tells the admin a search was answered without one of its
// sources: their business only if it keeps happening, so once in a while per
// source. Nothing is said for what is reported elsewhere already (the Twitch
// component's and igdbPost's own warnings) or expected.
func (p *Plugin) warnPartialSearch(source string, err error) {
	for _, quiet := range []error{errTwitchNotSetUp, errTwitchRefused, errTwitchUnavailable, errIGDBResting, errMediaBusy, context.Canceled} {
		if errors.Is(err, quiet) {
			return
		}
	}

	p.warnMediaOnce("search-partial-"+strings.ToLower(source),
		"A title search was answered without one of its sources", "provider", source, "error", err.Error())
}

// mergeGames is one list from Steam's games and IGDB's: Steam first, IGDB
// filling in, at most maxMediaResults.
//
//  1. What Steam listed is left to Steam. An IGDB game is dropped when it
//     links to a Steam app in THIS answer, or has the same name as a game in
//     it (Steam's results carry no year, so the name alone). Every other IGDB
//     game is kept, including ones linked to a Steam app Steam did not list:
//     those are delisted or region-locked, and Steam's search never lists
//     them whatever is typed.
//  2. A game typed in full comes first wherever it came from: Steam's exact
//     matches, then IGDB's.
//  3. Of the rest, Steam keeps five of eight places when both are full, and
//     IGDB fills whatever Steam leaves.
func mergeGames(steam, igdb []mediaCachedResult, normalQuery string) []mediaCachedResult {
	steamApps := map[string]bool{}
	steamNames := map[string]bool{}
	for _, result := range steam {
		if ref, ok := parseMediaRef(result.Ref); ok {
			steamApps[ref.id] = true
		}
		steamNames[mediaQueryKey(result.Title)] = true
	}

	var exact, steamRest, igdbRest []mediaCachedResult
	for _, result := range steam {
		if mediaQueryKey(result.Title) == normalQuery {
			exact = append(exact, result)
		} else {
			steamRest = append(steamRest, result)
		}
	}
	for _, result := range igdb {
		name := mediaQueryKey(result.Title)
		if (result.SteamApp != "" && steamApps[result.SteamApp]) || steamNames[name] {
			continue
		}
		if name == normalQuery {
			exact = append(exact, result)
		} else {
			igdbRest = append(igdbRest, result)
		}
	}

	if len(exact) > maxMediaResults {
		exact = exact[:maxMediaResults]
	}

	room := maxMediaResults - len(exact)
	fromIGDB := min(len(igdbRest), max(room-len(steamRest), min(3, room)))
	fromSteam := min(len(steamRest), room-fromIGDB)

	merged := make([]mediaCachedResult, 0, maxMediaResults)
	merged = append(merged, exact...)
	merged = append(merged, steamRest[:fromSteam]...)
	merged = append(merged, igdbRest[:fromIGDB]...)

	return merged
}

// igdbGameBody asks for everything a plan carries about one game.
func igdbGameBody(id string) string {
	return fmt.Sprintf("fields %s;\nwhere id = %s;\nlimit 1;", igdbGameFields, id)
}

// igdbGame is one IGDB game's details. Whether it is the kind of entry a
// search would list is not asked again: a ref the search handed out must keep
// working even if IGDB later files it differently.
func (p *Plugin) igdbGame(ctx context.Context, cfg *configuration, ref mediaRef) (*mediaDetails, error) {
	var games []igdbGame
	if err := p.igdbPost(ctx, cfg, "games", igdbGameBody(ref.id), &games); err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, errMediaNotFound
	}

	game := &games[0]
	if strconv.FormatInt(game.ID, 10) != ref.id {
		return nil, errIGDBUnexpectedShape
	}

	// Left out of searches, and so of everything else: a ref typed in by hand
	// is answered as though there were no such game.
	if igdbErotic(game) {
		return nil, errMediaNotFound
	}

	title := cleanText(plainText(game.Name), maxMediaTitle)
	if title == "" {
		return nil, errMediaNotFound
	}

	link := ""
	switch {
	case igdbLinkPattern.MatchString(game.URL):
		link = game.URL
	case igdbSlugPattern.MatchString(game.Slug):
		link = "https://www.igdb.com/games/" + game.Slug
	default:
		return nil, errIGDBUnexpectedShape
	}

	facts := make([]string, 0, maxMediaGenres+1)
	for _, genre := range game.Genres {
		if len(facts) == maxMediaGenres {
			break
		}
		if name := igdbGenreName(genre.Name); name != "" {
			facts = append(facts, name)
		}
	}
	for _, involved := range game.InvolvedCompanies {
		if involved.Developer && involved.Company != nil && strings.TrimSpace(involved.Company.Name) != "" {
			facts = append(facts, involved.Company.Name)
			break
		}
	}

	art := mediaArt{}
	if game.Cover != nil && igdbImageID.MatchString(game.Cover.ImageID) {
		art.Poster = game.Cover.ImageID
	}
	for _, wide := range append(append([]igdbImage{}, game.Artworks...), game.Screenshots...) {
		if igdbImageID.MatchString(wide.ImageID) {
			art.Backdrop = wide.ImageID
			break
		}
	}

	return &mediaDetails{
		info: scheduleMedia{
			Ref:      ref.String(),
			Kind:     mediaKindGame,
			Title:    title,
			Year:     igdbYear(game.FirstReleaseDate),
			Overview: overviewOf(game.Summary, game.Storyline),
			Facts:    factLine(facts...),
			Link:     link,
			Source:   mediaSourceIGDB,
		},
		art:      art,
		changing: igdbChanging(game, p.mediaStore().now()),
	}, nil
}

// igdbGenreName is a genre as the facts line shows it. IGDB writes some with
// their abbreviation, "Role-playing (RPG)", and the abbreviation is what
// people say; every other name is kept as IGDB wrote it.
func igdbGenreName(name string) string {
	name = strings.TrimSpace(name)
	if match := igdbGenreShort.FindStringSubmatch(name); match != nil {
		return match[2]
	}

	return name
}

// igdbChanging is a game still settling: no release date, one still to come,
// or alpha, beta, early access or only rumoured.
func igdbChanging(game *igdbGame, now time.Time) bool {
	if game.FirstReleaseDate <= 0 || time.Unix(game.FirstReleaseDate, 0).After(now) {
		return true
	}

	if game.GameStatus != nil {
		if name := game.GameStatus.name(); name != "" {
			return igdbChangingStatuses[name]
		}

		return igdbChangingStatusIDs[game.GameStatus.ID]
	}

	return false
}

// igdbImageURL is "" unless id is the shape IGDB's are, so nothing but an id
// is ever appended to the image host.
func igdbImageURL(size, id string) string {
	if !igdbImageID.MatchString(id) {
		return ""
	}

	return igdbImageBase + "/t_" + size + "/" + id + ".jpg"
}
