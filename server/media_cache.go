package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Remembered titles: what the providers said, kept in the plugin's KV store so
// the same search or the same title is not looked up again, after a restart or
// for days. Asked for by the admin who runs the server, so it would not keep
// searching online for the same games, movies and shows.
//
// Two layers. Memory (the memoryCache instances in mediaStore) is asked first,
// as it always was. Behind it are sixteen fixed KV documents, mediacache_00 to
// mediacache_0f, read only when memory misses and written only with something
// a provider has just answered. Neither layer is touched by the poll.
//
// Kept: search results with the art each one's search described, a title's
// details (the text a plan carries and where its pictures are), and "no such
// title" for a day. Not kept here: the pictures, which are kept on the
// server's disk when Mattermost's files are (media_disk.go) and otherwise in
// memory only, and any credential or token, which is never kept anywhere.
//
// Sixteen fixed documents rather than one key per entry, because
// kvKeysWithPrefix walks the WHOLE KV namespace on every poll on every node,
// and every key added lengthens it. And a shard is never deleted, not even by
// the admin's Forget or when it cannot be read: it is emptied instead. That
// walk pages by OFFSET over keys in order, so a key deleted ahead of the page
// being read moves every later key back one place, and the key on the boundary
// is silently missed — and mediacache_ sorts before keys the badge and airtime
// code walk for.
//
// Nothing typed is ever stored in the clear. A search is filed under a hash of
// the normalised query, because KV keys appear in Mattermost's own logs on a
// failure. The hash is not a secret — a list of game names reverses it, and a
// remembered search's results say what it was for — it only keeps the words
// out of the log lines.
//
// Read as though a stranger wrote it: everything that comes back is checked
// again before it is used, and a KV problem never fails a search, a plan or a
// picture. The provider is simply asked.

const (
	kvMediaCachePrefix   = "mediacache_" // + two hex digits, 00..0f
	mediaCacheShards     = 16
	mediaCacheVersion    = 1
	maxMediaShardBytes   = 128 << 10 // per shard as stored; 2 MiB in all
	maxMediaShardEntries = 256       // per shard; 4,096 in all
	maxMediaKeptBytes    = 16 << 10  // one entry larger than this is not kept

	// How long each kind of answer is believed. IGDB's are shorter on
	// purpose: its FAQ welcomes caching, but the Twitch Developer Agreement
	// speaks of keeping data fresh and honouring deletions.
	mediaKeepSearch      = 7 * 24 * time.Hour
	mediaKeepIGDBSearch  = 3 * 24 * time.Hour
	mediaKeepEmptySearch = 24 * time.Hour
	mediaKeepTitle       = 30 * 24 * time.Hour
	mediaKeepIGDBTitle   = 7 * 24 * time.Hour
	mediaKeepRunningShow = 7 * 24 * time.Hour
	mediaKeepChanging    = 3 * 24 * time.Hour
	mediaKeepMissing     = 24 * time.Hour

	// Past this, an entry is never served, not even when the provider is down,
	// and it is dropped the next time its shard is written. IGDB's entries
	// never get this far: their own lives above are hard limits (see
	// mediaHardLimited).
	mediaKeepStaleLimit = 90 * 24 * time.Hour

	// An entry served from KV is marked used at most this often, which keeps
	// the titles in daily use out of eviction without a write per read.
	mediaTouchEvery = 3 * 24 * time.Hour

	// Art addresses this old are asked about again once they stop working.
	mediaRefreshArtAfter = 24 * time.Hour

	mediaCacheAttempts = 2

	// Searches shorter than this are typing on the way to something. They are
	// asked again rarely and would only churn the shards, so they stay in
	// memory.
	mediaKeepMinRunes = 3

	// A provider saying it no longer has a title it had is believed the second
	// time, at least this long after the first: Steam answers "no such app"
	// for reasons that are not deletions.
	mediaGoneConfirm = time.Hour

	mediaKeptSearch  = "search"
	mediaKeptTitle   = "title"
	mediaKeptMissing = "missing"

	// emptyMediaShard is what an emptied shard holds. Never an empty value:
	// over RPC an empty slice arrives as nil, which deletes the key.
	emptyMediaShard = `{"v":1,"entries":{}}`
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
	Gone    int64               `json:"gone,omitempty"`    // title: when the provider first said it had no such title
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
	SteamApp string   `json:"steam,omitempty"` // IGDB results only: the Steam app IGDB links the game to
}

// mediaSearchKey files a search under a hash of its words, never the words.
func mediaSearchKey(provider, normal string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + normal))

	return "s:" + provider + ":" + hex.EncodeToString(sum[:])[:32]
}

// mediaTitleKey is a title's entry. A title and its "no such title" marker
// share it, so one replaces the other.
func mediaTitleKey(ref mediaRef) string {
	return "t:" + ref.String()
}

func mediaShardKey(entryKey string) string {
	sum := sha256.Sum256([]byte(entryKey))

	return fmt.Sprintf("%s%02x", kvMediaCachePrefix, sum[0]&0x0f)
}

func mediaShardKeys() []string {
	keys := make([]string, 0, mediaCacheShards)
	for i := 0; i < mediaCacheShards; i++ {
		keys = append(keys, fmt.Sprintf("%s%02x", kvMediaCachePrefix, i))
	}

	return keys
}

type mediaShardState int

const (
	mediaShardAbsent mediaShardState = iota
	mediaShardReadable
	mediaShardNewer
	mediaShardUnreadable
	mediaShardFailed
)

// readMediaShard reads one shard as it is stored.
func (p *Plugin) readMediaShard(shard string) ([]byte, *mediaShard, mediaShardState) {
	raw, appErr := p.API.KVGet(shard)
	if appErr != nil {
		p.warnMediaOnce("media-cache",
			"The remembered titles could not be read or written just now; titles are looked up instead",
			"error", appErr.Error())

		return nil, nil, mediaShardFailed
	}
	if len(raw) == 0 {
		return nil, nil, mediaShardAbsent
	}

	var doc mediaShard
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version < 1 {
		return raw, nil, mediaShardUnreadable
	}
	// A newer build's document is a miss, and is never written over: during
	// an upgrade a mixed cluster must not undo each other's work.
	if doc.Version > mediaCacheVersion {
		return raw, nil, mediaShardNewer
	}
	if doc.Entries == nil {
		doc.Entries = map[string]*mediaKept{}
	}

	return raw, &doc, mediaShardReadable
}

// resetMediaShard empties a shard that could not be read. Emptied rather than
// deleted, so the number of keys stays the same (see the top of this file).
func (p *Plugin) resetMediaShard(shard string, raw []byte) {
	if _, appErr := p.API.KVCompareAndSet(shard, raw, []byte(emptyMediaShard)); appErr != nil {
		p.warnMediaOnce("media-cache",
			"The remembered titles could not be read or written just now; titles are looked up instead",
			"error", appErr.Error())

		return
	}

	p.warnMediaOnce("media-cache",
		"A remembered-titles document could not be read and was emptied; titles in it will be looked up again")
}

// recallMedia reads one entry, fresh or stale, or false.
func (p *Plugin) recallMedia(entryKey string) (*mediaKept, bool) {
	shard := mediaShardKey(entryKey)

	raw, doc, state := p.readMediaShard(shard)
	switch state {
	case mediaShardReadable:
		kept, found := doc.Entries[entryKey]

		return kept, found && kept != nil
	case mediaShardUnreadable:
		p.resetMediaShard(shard, raw)
	}

	return nil, false
}

// keepMedia writes one entry into its shard. Best effort: a failure is
// warned about once in a while and never fails the lookup that caused it.
// Called only with something a provider has just answered, so it can never
// put back an older copy of anything.
func (p *Plugin) keepMedia(entryKey string, kept *mediaKept) {
	store := p.mediaStore()
	kept.Used = store.now().UnixMilli()

	if encoded, err := json.Marshal(kept); err != nil || len(encoded) > maxMediaKeptBytes {
		return
	}

	shard := mediaShardKey(entryKey)
	for attempt := 0; attempt < mediaCacheAttempts; attempt++ {
		raw, doc, state := p.readMediaShard(shard)
		switch state {
		case mediaShardFailed, mediaShardNewer:
			return
		case mediaShardUnreadable:
			p.resetMediaShard(shard, raw)
			return
		case mediaShardAbsent:
			// Nil, not empty: KVCompareAndSet reads a nil old value as "insert
			// only if absent".
			raw = nil
			doc = &mediaShard{Version: mediaCacheVersion, Entries: map[string]*mediaKept{}}
		}

		doc.Entries[entryKey] = kept

		next, ok := pruneMediaShard(doc, store.now())
		if !ok {
			return
		}

		written, appErr := p.API.KVCompareAndSet(shard, raw, next)
		if appErr != nil {
			p.warnMediaOnce("media-cache",
				"The remembered titles could not be read or written just now; titles are looked up instead",
				"error", appErr.Error())

			return
		}
		if written {
			return
		}
	}
}

// updateKeptMedia changes one entry in place, and only if the shard still
// holds it with this At: touch (Used) and the NotFound mark (Gone). It never
// inserts, never writes back a copy read earlier, and never prunes. So an
// entry an admin has just forgotten stays forgotten, and one another node has
// just refreshed stays as that node wrote it.
func (p *Plugin) updateKeptMedia(entryKey string, at int64, change func(*mediaKept)) {
	shard := mediaShardKey(entryKey)

	for attempt := 0; attempt < mediaCacheAttempts; attempt++ {
		raw, doc, state := p.readMediaShard(shard)
		switch state {
		case mediaShardReadable:
		case mediaShardUnreadable:
			p.resetMediaShard(shard, raw)
			return
		default:
			return
		}

		kept, found := doc.Entries[entryKey]
		if !found || kept == nil || kept.At != at {
			return
		}
		change(kept)

		next, err := json.Marshal(doc)
		if err != nil {
			return
		}

		written, appErr := p.API.KVCompareAndSet(shard, raw, next)
		if appErr != nil {
			p.warnMediaOnce("media-cache",
				"The remembered titles could not be read or written just now; titles are looked up instead",
				"error", appErr.Error())

			return
		}
		if written {
			return
		}
	}
}

// pruneMediaShard keeps a shard within its bounds and encodes it. Entries past
// the stale limit go first, and IGDB's past their own time; then, while the shard is too full, one at a time:
// searches before "no such title" markers before titles, and within a kind the
// least recently used, then the oldest, then by key, so the result does not
// depend on map order. Typing makes many searches, and a title's details are
// what is most worth keeping, so a search can never push a planned title out
// while there is a search left to drop.
func pruneMediaShard(doc *mediaShard, now time.Time) ([]byte, bool) {
	oldest := now.Add(-mediaKeepStaleLimit).UnixMilli()
	for key, kept := range doc.Entries {
		if kept == nil || kept.At < oldest || (mediaHardLimited(key) && now.UnixMilli() >= kept.Until) {
			delete(doc.Entries, key)
		}
	}

	order := mediaEvictionOrder(doc)
	for len(doc.Entries) > maxMediaShardEntries {
		delete(doc.Entries, order[0])
		order = order[1:]
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	if len(encoded) <= maxMediaShardBytes {
		return encoded, true
	}

	// Too large. Each entry's encoded size is measured once, rather than the
	// shard being encoded again for every entry dropped. The sum is a little
	// over the true size, never under; the final encoding is checked all the
	// same.
	sizes := make(map[string]int, len(order))
	total := len(emptyMediaShard)
	for _, key := range order {
		entry, err := json.Marshal(doc.Entries[key])
		if err != nil {
			return nil, false
		}
		name, _ := json.Marshal(key)

		sizes[key] = len(name) + 1 + len(entry) + 1
		total += sizes[key]
	}

	for len(order) > 0 && total > maxMediaShardBytes {
		total -= sizes[order[0]]
		delete(doc.Entries, order[0])
		order = order[1:]
	}

	for {
		encoded, err := json.Marshal(doc)
		if err != nil {
			return nil, false
		}
		if len(encoded) <= maxMediaShardBytes || len(order) == 0 {
			return encoded, true
		}

		delete(doc.Entries, order[0])
		order = order[1:]
	}
}

// mediaEvictionOrder is the shard's keys, the first to go first.
func mediaEvictionOrder(doc *mediaShard) []string {
	rank := func(kind string) int {
		switch kind {
		case mediaKeptTitle:
			return 2
		case mediaKeptMissing:
			return 1
		default:
			return 0
		}
	}

	order := make([]string, 0, len(doc.Entries))
	for key := range doc.Entries {
		order = append(order, key)
	}

	sort.Slice(order, func(i, j int) bool {
		a, b := doc.Entries[order[i]], doc.Entries[order[j]]
		if rank(a.Type) != rank(b.Type) {
			return rank(a.Type) < rank(b.Type)
		}
		if a.Used != b.Used {
			return a.Used < b.Used
		}
		if a.At != b.At {
			return a.At < b.At
		}

		return order[i] < order[j]
	})

	return order
}

// touchMedia marks an entry served from KV as used, if it has not been for a
// while, through updateKeptMedia so it can bring nothing back.
func (p *Plugin) touchMedia(entryKey string, kept *mediaKept, now time.Time) {
	if now.UnixMilli()-kept.Used < mediaTouchEvery.Milliseconds() {
		return
	}

	stamp := now.UnixMilli()
	p.updateKeptMedia(entryKey, kept.At, func(entry *mediaKept) {
		entry.Used = stamp
	})
}

// memoryLife is how long something read from KV is held in memory: memory's
// usual time, but never past what KV believes it for, and not less than a
// minute, so a nearly stale entry is not read from KV on every request.
func memoryLife(usual time.Duration, until int64, now time.Time) time.Duration {
	left := time.UnixMilli(until).Sub(now)

	return min(usual, max(time.Minute, left))
}

// mediaHardLimited is whether an entry is never believed past its own time,
// not even while its provider is down: IGDB's. Victor's answer was that IGDB
// data is kept 7 days for a title and 3 for a search (design/media-lookups.md,
// "Answered by Victor"), and letting an old entry stand in for an IGDB that
// does not answer would keep it for as long as the 90-day stale limit. The
// pictures on the disk already follow the same rule (pictureMayFallBack).
// Everybody else's entries may stand in until the stale limit.
func mediaHardLimited(entryKey string) bool {
	return mediaKeptProvider(entryKey) == mediaProviderIGDB
}

// mayStandIn is whether a remembered entry may be served in place of an answer
// its provider could not give: an IGDB entry only while it is still believed,
// anybody else's until the stale limit.
func mayStandIn(entryKey string, kept *mediaKept, now time.Time) bool {
	if mediaHardLimited(entryKey) {
		return now.UnixMilli() < kept.Until
	}

	return now.UnixMilli()-kept.At < mediaKeepStaleLimit.Milliseconds()
}

// standInLife is how long a stand-in is held in memory: briefly, so the
// provider is asked again soon, and for IGDB never past the entry's own time.
// A life that has already run out is one memory does not keep at all.
func standInLife(entryKey string, kept *mediaKept, now time.Time) time.Duration {
	if !mediaHardLimited(entryKey) {
		return mediaMissTTL
	}

	return min(mediaMissTTL, time.UnixMilli(kept.Until).Sub(now))
}

// providerSearch is one provider's answer for one normalised query: from
// memory, from the remembered titles, or from the provider, in that order.
//
// Two people typing the same thing share one fetch. The fetch runs on a
// context of its own rather than the first caller's, so the person who
// started it typing on — the browser drops the old request — does not fail
// everybody else waiting for it, and an IGDB answer that arrives after the
// game search stopped waiting still fills memory and KV for the next
// keystroke. The caller waits for the fetch or for its own ctx, whichever
// ends first.
func (p *Plugin) providerSearch(ctx context.Context, cfg *configuration, provider, query, normal string) ([]mediaCachedResult, error) {
	store := p.mediaStore()
	if store.searchFault != nil {
		store.searchFault(provider)
	}

	// A query that is all punctuation has no normal form to share, and is not
	// worth remembering past memory.
	memoryKey := provider + "\x00" + normal
	flight := "search/" + provider + "/" + normal
	durable := normal != ""
	if !durable {
		memoryKey = "\x01" + provider + "\x00" + strings.ToLower(query)
		flight = "search/" + memoryKey
	}

	if cached, hit := store.searches.get(memoryKey); hit {
		p.hintArt(cached)
		return cached, nil
	}

	found, err := store.flights.doDetached(ctx, p.API, "a title search's shared fetch", flight, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), mediaSearchBudget)
		defer cancel()

		if cached, hit := store.searches.get(memoryKey); hit {
			p.hintArt(cached)
			return cached, nil
		}

		entryKey := mediaSearchKey(provider, normal)

		var fallback *mediaKept
		if durable {
			if kept, found := p.recallMedia(entryKey); found && keptSearchChecked(kept, provider) {
				now := store.now()
				switch {
				case now.UnixMilli() < kept.Until:
					store.searches.put(memoryKey, kept.Results, memoryLife(mediaSearchTTL, kept.Until, now))
					p.hintArt(kept.Results)
					p.touchMedia(entryKey, kept, now)

					return kept.Results, nil
				case mayStandIn(entryKey, kept, now):
					fallback = kept
				}
			}
		}

		results, err := p.askForSearch(ctx, cfg, provider, query)
		if err != nil {
			// Stale, but better than nothing while the provider is down. Held
			// in memory only briefly, so the provider is asked again soon.
			if fallback != nil {
				store.searches.put(memoryKey, fallback.Results, standInLife(entryKey, fallback, store.now()))
				p.hintArt(fallback.Results)

				return fallback.Results, nil
			}

			return nil, err
		}

		store.searches.put(memoryKey, results, mediaSearchTTL)
		p.hintArt(results)

		if durable && utf8.RuneCountInString(normal) >= mediaKeepMinRunes {
			now := store.now()
			p.keepMedia(entryKey, &mediaKept{
				Type:    mediaKeptSearch,
				At:      now.UnixMilli(),
				Until:   now.Add(searchLife(provider, results)).UnixMilli(),
				Results: results,
			})
		}

		return results, nil
	})
	if err != nil {
		return nil, err
	}

	results, _ := found.([]mediaCachedResult)

	return results, nil
}

// askForSearch asks one provider, and nothing else.
func (p *Plugin) askForSearch(ctx context.Context, cfg *configuration, provider, query string) ([]mediaCachedResult, error) {
	switch provider {
	case mediaProviderTMDB:
		return p.searchTMDB(ctx, cfg.TMDBToken, query)
	case mediaProviderSteam:
		return p.searchSteam(ctx, query)
	case mediaProviderIGDB:
		return p.searchIGDB(ctx, cfg, query)
	default:
		return nil, errMediaNotFound
	}
}

// hintArt remembers, in memory, where each result's pictures are, so the
// planning form's thumbnails need nothing but the pictures. Put again on every
// hit, so a list served from memory or KV keeps its thumbnails too.
func (p *Plugin) hintArt(results []mediaCachedResult) {
	hints := p.mediaStore().hints
	for _, result := range results {
		hints.put(result.Ref, result.Art, mediaHintTTL)
	}
}

func searchLife(provider string, results []mediaCachedResult) time.Duration {
	switch {
	case len(results) == 0:
		return mediaKeepEmptySearch
	case provider == mediaProviderIGDB:
		return mediaKeepIGDBSearch
	default:
		return mediaKeepSearch
	}
}

// keptSearchChecked is whether a remembered search can be served, checked as if
// a stranger wrote it: every result a ref of this provider, with a title. One
// bad result makes the whole entry a miss. Good ones are tidied in place.
func keptSearchChecked(kept *mediaKept, provider string) bool {
	if kept.Type != mediaKeptSearch || len(kept.Results) > maxMediaResults {
		return false
	}

	for i := range kept.Results {
		result := &kept.Results[i]

		ref, ok := parseMediaRef(result.Ref)
		title := cutRunes(strings.TrimSpace(result.Title), maxMediaTitle)
		if !ok || ref.provider != provider || result.Kind != ref.kind || title == "" {
			return false
		}
		if result.SteamApp != "" && !steamAppIDPattern.MatchString(result.SteamApp) {
			return false
		}

		result.Ref, result.Title = ref.String(), title
		result.Label = cutRunes(strings.TrimSpace(result.Label), 20)
		if !yearPattern.MatchString(result.Year) || len(result.Year) != 4 {
			result.Year = ""
		}
	}

	return true
}

// keptDetails is a remembered title as details, or nil when it does not pass
// the same checks a stored plan's title does on its way out.
func keptDetails(kept *mediaKept, ref mediaRef) *mediaDetails {
	if kept.Type != mediaKeptTitle || kept.Info == nil {
		return nil
	}

	info := kept.Info.checked()
	if info == nil || info.Ref != ref.String() {
		return nil
	}

	details := &mediaDetails{info: *info, fetchedAt: time.UnixMilli(kept.At), until: time.UnixMilli(kept.Until)}
	if kept.Art != nil {
		details.art = *kept.Art
	}

	return details
}

// titleLife is how long a title's details are believed.
func titleLife(ref mediaRef, details *mediaDetails) time.Duration {
	switch {
	case ref.kind == mediaKindTV && details.changing:
		return mediaKeepRunningShow
	case details.changing:
		return mediaKeepChanging
	case ref.provider == mediaProviderIGDB:
		return mediaKeepIGDBTitle
	default:
		return mediaKeepTitle
	}
}

// rememberedDetails is a title's details from the remembered titles, or from
// its provider when they are not remembered or have gone stale. It runs inside
// mediaDetailsFor's flight, once memory has missed.
func (p *Plugin) rememberedDetails(ctx context.Context, cfg *configuration, ref mediaRef) (*mediaDetails, error) {
	store := p.mediaStore()
	entryKey := mediaTitleKey(ref)
	now := store.now()

	var remembered *mediaKept
	if kept, found := p.recallMedia(entryKey); found {
		switch kept.Type {
		case mediaKeptMissing:
			if now.UnixMilli() < kept.Until {
				store.details.put(ref.String(), nil, memoryLife(mediaMissTTL, kept.Until, now))
				return nil, errMediaNotFound
			}
		case mediaKeptTitle:
			if details := keptDetails(kept, ref); details != nil {
				if now.UnixMilli() < kept.Until {
					store.details.put(ref.String(), details, memoryLife(mediaDetailsTTL, kept.Until, now))
					p.touchMedia(entryKey, kept, now)

					return details, nil
				}
				if mayStandIn(entryKey, kept, now) {
					remembered = kept
				}
			}
		}
	}

	return p.detailsFromProvider(ctx, cfg, ref, remembered)
}

// refreshMediaDetails asks a title's provider again, past memory and the
// remembered titles, because the art addresses it gave have stopped working.
// What it finds replaces both.
func (p *Plugin) refreshMediaDetails(ctx context.Context, cfg *configuration, ref mediaRef) (*mediaDetails, error) {
	found, err := p.mediaStore().flights.do("details-fresh/"+ref.String(), func() (any, error) {
		now := p.mediaStore().now()

		var remembered *mediaKept
		entryKey := mediaTitleKey(ref)
		kept, recalled := p.recallMedia(entryKey)
		if recalled && keptDetails(kept, ref) != nil && mayStandIn(entryKey, kept, now) {
			remembered = kept
		}

		return p.detailsFromProvider(ctx, cfg, ref, remembered)
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

// detailsFromProvider asks the provider and files what it says. remembered is
// the title as KV last had it, if it had one young enough to fall back on.
func (p *Plugin) detailsFromProvider(ctx context.Context, cfg *configuration, ref mediaRef, remembered *mediaKept) (*mediaDetails, error) {
	store := p.mediaStore()
	entryKey := mediaTitleKey(ref)

	details, err := p.askForDetails(ctx, cfg, ref)
	switch {
	case err == nil:
		details.until = details.fetchedAt.Add(titleLife(ref, details))
		store.details.put(ref.String(), details, mediaDetailsTTL)

		info, art := details.info, details.art
		p.keepMedia(entryKey, &mediaKept{
			Type:  mediaKeptTitle,
			At:    details.fetchedAt.UnixMilli(),
			Until: details.until.UnixMilli(),
			Info:  &info,
			Art:   &art,
		})

		// New art replaces the old at once, every size of it: the pictures
		// held were of the art the title had, and would otherwise be served
		// for as long as that title was believed.
		if remembered != nil && remembered.Art != nil && *remembered.Art != art {
			p.forgetPicturesOf(ref)
		}

		return details, nil

	case errors.Is(err, errMediaNotFound):
		return p.titleNotFound(ref, remembered)

	case remembered != nil:
		// The provider is down or slow: what it said before, briefly.
		stale := keptDetails(remembered, ref)
		store.details.put(ref.String(), stale, standInLife(entryKey, remembered, store.now()))

		return stale, nil

	default:
		return nil, err
	}
}

// titleNotFound is the provider saying it has no such title. Over a title it
// had, that is believed only the second time, at least an hour after the
// first: one "no such app" from Steam is often not a deletion, and a title
// fine for a month should not be thrown away on one answer. Deletions are
// still honoured, within about an hour, and an answer in between replaces the
// entry and with it the mark.
func (p *Plugin) titleNotFound(ref mediaRef, remembered *mediaKept) (*mediaDetails, error) {
	store := p.mediaStore()
	entryKey := mediaTitleKey(ref)
	now := store.now()

	if remembered != nil {
		stale := keptDetails(remembered, ref)

		switch {
		case remembered.Gone == 0:
			stamp := now.UnixMilli()
			p.updateKeptMedia(entryKey, remembered.At, func(kept *mediaKept) {
				if kept.Gone == 0 {
					kept.Gone = stamp
				}
			})
			store.details.put(ref.String(), stale, standInLife(entryKey, remembered, now))

			return stale, nil

		case now.UnixMilli()-remembered.Gone < mediaGoneConfirm.Milliseconds():
			store.details.put(ref.String(), stale, standInLife(entryKey, remembered, now))

			return stale, nil
		}
	}

	p.keepMedia(entryKey, &mediaKept{
		Type:  mediaKeptMissing,
		At:    now.UnixMilli(),
		Until: now.Add(mediaKeepMissing).UnixMilli(),
	})
	store.details.put(ref.String(), nil, mediaMissTTL)

	// A title that is gone takes its pictures on the disk with it, which is
	// also how a deletion at IGDB is honoured for its art.
	p.dropPicturesOf(ref)

	return nil, errMediaNotFound
}

// mediaCacheSummary is the admin card's one line.
type mediaCacheSummary struct {
	Titles     int            `json:"titles"`
	Searches   int            `json:"searches"`
	Missing    int            `json:"missing"`
	Bytes      int            `json:"bytes"`
	LimitBytes int            `json:"limitBytes"`
	ByProvider map[string]int `json:"byProvider"`
	Oldest     int64          `json:"oldest"`

	// The pictures kept on the server's disk (media_disk.go), counted from
	// the directory, which every node of a cluster shares. PicturesKept is
	// false while they are held in memory only, and PictureProblem then says
	// why: Mattermost's files are not on this server's own disk, or the
	// directory would not take a write.
	PicturesKept      bool   `json:"picturesKept"`
	PictureProblem    string `json:"pictureProblem,omitempty"`
	Pictures          int    `json:"pictures"`
	PictureBytes      int64  `json:"pictureBytes"`
	PictureLimitBytes int64  `json:"pictureLimitBytes"`
	PictureLimit      int    `json:"pictureLimit"`
}

type mediaCacheForgotten struct {
	OK                bool `json:"ok"`
	Forgotten         int  `json:"forgotten"`
	PicturesForgotten int  `json:"picturesForgotten"`
}

// handleMediaCache answers /api/v1/admin/media/cache: GET counts what is
// remembered, DELETE forgets all of it. For system admins, and whether or not
// lookups are on: it makes no outside request either way.
func (p *Plugin) handleMediaCache(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can see or forget remembered titles.")
		return
	}

	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodGet:
		p.describeMediaCache(w)
	case http.MethodDelete:
		p.forgetMediaCache(w, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) describeMediaCache(w http.ResponseWriter) {
	summary := mediaCacheSummary{
		LimitBytes: mediaCacheShards * maxMediaShardBytes,
		ByProvider: map[string]int{mediaProviderTMDB: 0, mediaProviderSteam: 0, mediaProviderIGDB: 0},
	}

	for _, shard := range mediaShardKeys() {
		raw, appErr := p.API.KVGet(shard)
		if appErr != nil {
			p.API.LogWarn("The remembered titles could not be read", "error", appErr.Error())
			writeError(w, http.StatusInternalServerError, "The remembered titles could not be read just now. Try again.")

			return
		}
		summary.Bytes += len(raw)

		var doc mediaShard
		if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil || doc.Version != mediaCacheVersion {
			continue
		}

		for key, kept := range doc.Entries {
			if kept == nil {
				continue
			}

			switch kept.Type {
			case mediaKeptSearch:
				summary.Searches++
			case mediaKeptTitle:
				summary.Titles++
			case mediaKeptMissing:
				summary.Missing++
			default:
				continue
			}

			if provider := mediaKeptProvider(key); provider != "" {
				summary.ByProvider[provider]++
			}
			if kept.At > 0 && (summary.Oldest == 0 || kept.At < summary.Oldest) {
				summary.Oldest = kept.At
			}
		}
	}

	pictures := p.describePictures()
	summary.PicturesKept, summary.PictureProblem = pictures.kept, pictures.problem
	summary.Pictures, summary.PictureBytes = pictures.pictures, pictures.bytes
	summary.PictureLimitBytes, summary.PictureLimit = pictures.limitBytes, pictures.limit

	writeJSON(w, http.StatusOK, summary)
}

// mediaKeptProvider is whose answer an entry is, from its key: a search names
// its provider, and a title its ref.
func mediaKeptProvider(key string) string {
	switch {
	case strings.HasPrefix(key, "s:"):
		provider, _, _ := strings.Cut(strings.TrimPrefix(key, "s:"), ":")
		switch provider {
		case mediaProviderTMDB, mediaProviderSteam, mediaProviderIGDB:
			return provider
		}
	case strings.HasPrefix(key, "t:"):
		if ref, ok := parseMediaRef(strings.TrimPrefix(key, "t:")); ok {
			return ref.provider
		}
	}

	return ""
}

// forgetMediaCache empties every shard there is. Never KVDelete, which would
// shift the paged key walk, and never KVDeleteAll, which would take airtime
// with it. A shard that does not exist is left absent, so forgetting creates
// nothing. Any version, readable or not, is emptied: the admin asked.
func (p *Plugin) forgetMediaCache(w http.ResponseWriter, userID string) {
	forgotten := 0
	failed := false

	for _, shard := range mediaShardKeys() {
		emptied := false

		for attempt := 0; attempt < mediaCacheAttempts && !emptied; attempt++ {
			raw, appErr := p.API.KVGet(shard)
			if appErr != nil {
				break
			}
			if len(raw) == 0 || string(raw) == emptyMediaShard {
				emptied = true
				break
			}

			var doc mediaShard
			entries := 0
			if json.Unmarshal(raw, &doc) == nil {
				entries = len(doc.Entries)
			}

			written, appErr := p.API.KVCompareAndSet(shard, raw, []byte(emptyMediaShard))
			if appErr != nil {
				break
			}
			if written {
				forgotten += entries
				emptied = true
			}
		}

		if !emptied {
			failed = true
		}
	}

	// The pictures on the disk go too: one directory, which every node
	// shares, so they go for the whole cluster at once.
	pictures, err := p.forgetPictures()
	if err != nil {
		failed = true
		p.API.LogWarn("Some pictures kept on the disk could not be forgotten", "error", err.Error())
	}

	// This node's memory goes too, whatever happened in KV. Other nodes keep
	// theirs until it expires, which the admin card says.
	store := p.mediaStore()
	store.searches.clear()
	store.details.clear()
	store.hints.clear()
	store.pictures.clear()

	if failed {
		p.API.LogWarn("Some remembered titles could not be forgotten", "user_id", userID)
		writeError(w, http.StatusInternalServerError, "Some remembered titles could not be forgotten just now. Try again.")

		return
	}

	p.API.LogInfo("A system admin forgot the remembered titles", "user_id", userID, "forgotten", forgotten, "pictures", pictures)
	writeJSON(w, http.StatusOK, mediaCacheForgotten{OK: true, Forgotten: forgotten, PicturesForgotten: pictures})
}
