package main

import (
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A Broadcast Box profile is stored as a file named "<streamKey>_<token>".
// The stream key is the public display name that appears in /api/status; the
// token is a UUID that only the streamer holds, and it is what OBS sends. A
// user proves they own a stream by producing the token, never by typing the
// stream key, which anyone can read off the panel.
//
// Three KV entries per link:
//
//	link_user_<userID>        -> stream key
//	link_key_<lowercased key> -> userID
//	link_token_<userID>       -> publish token (never sent to the browser)

// validStreamKey bounds what an origin-supplied name may become as a KV key.
// Broadcast Box only creates profiles matching [\p{L}\p{N}_-]+, so anything
// outside that is a bug or an attempt to write somewhere unexpected.
func validStreamKey(streamKey string) bool {
	if streamKey == "" || utf8.RuneCountInString(streamKey) > maxStreamKeyRunes {
		return false
	}

	for _, r := range streamKey {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}

	return true
}

func cleanStreamKey(streamKey string) string {
	trimmed := strings.TrimSpace(streamKey)
	if len(trimmed) >= 7 && strings.EqualFold(trimmed[:7], "bearer ") {
		trimmed = strings.TrimSpace(trimmed[7:])
	}

	return trimmed
}

func normalizeKey(streamKey string) string {
	return strings.ToLower(cleanStreamKey(streamKey))
}

// publishToken is how Broadcast Box expects a bearer to arrive: base64 encoded.
// ResolveBearerToken base64-decodes when it can, which is what the Broadcast Box
// web client does for WHIP and profile calls.
func publishToken(token string) string {
	return base64.StdEncoding.EncodeToString([]byte(token))
}

func (p *Plugin) getLinkedKey(userID string) string {
	key, _ := p.linkedKey(userID)

	return key
}

// linkedKey distinguishes "no link" from "could not read", which matters when
// unlinking: treating a read failure as absence leaves the reverse mapping in
// place and the user keeps being attributed and badged.
func (p *Plugin) linkedKey(userID string) (string, error) {
	data, appErr := p.API.KVGet(kvLinkByUserPrefix + userID)
	if appErr != nil {
		return "", appErr
	}

	return string(data), nil
}

func (p *Plugin) getLinkedToken(userID string) string {
	data, appErr := p.API.KVGet(kvLinkTokenPrefix + userID)
	if appErr != nil || len(data) == 0 {
		return ""
	}

	return string(data)
}

// getLinkedUser is who the reverse entry names, with a read that fails taken
// as nobody. Only for attribution (whose stream a card shows), which decides
// nobody's access; anything that does uses linkedUser or linkOf.
func (p *Plugin) getLinkedUser(streamKey string) string {
	owner, _ := p.linkedUser(streamKey)

	return owner
}

// linkedUser is who the reverse entry for a stream key names, telling "no
// owner" from "could not read": a read that failed and was taken as nobody
// would skip a takeover and leave the previous owner linked.
func (p *Plugin) linkedUser(streamKey string) (string, error) {
	data, appErr := p.API.KVGet(kvLinkByKeyPrefix + normalizeKey(streamKey))
	if appErr != nil {
		return "", errors.New(appErr.Error())
	}

	return string(data), nil
}

// linkOf is somebody's stream key while they are linked, and "" while they
// are not. Linked means both entries agree: link_user_ names a key whose
// link_key_ names them back. setLink writes the reverse entry first and the
// forward one last, and two links of one key at once are not compare-and-set,
// so whoever wrote the reverse entry last is the only one linked, and a
// forward entry left behind by a write that failed half way, or by a race,
// reads as no link at all. A read that fails is an error, never a no.
func (p *Plugin) linkOf(userID string) (string, error) {
	key, err := p.linkedKey(userID)
	if err != nil || key == "" {
		return "", err
	}

	owner, err := p.linkedUser(key)
	if err != nil {
		return "", err
	}
	if owner != userID {
		return "", nil
	}

	return key, nil
}

// hasLink is whether somebody is linked by linkOf's rule, with a read that
// fails taken as no. It is for what only counts or shows linked people (the
// Top Snoopers board, and fetching a picture nobody here has shown), where
// leaving somebody out for a moment is the cheaper mistake. Deciding access
// goes through accessOf, which tells a failed read from a no. A forward entry
// left behind by a race or a failed cleanup must not count here either: the
// gate treats that person as locked or let in, and let-in watching is never
// counted (lock-screen Q5).
func (p *Plugin) hasLink(userID string) bool {
	key, err := p.linkOf(userID)

	return err == nil && key != ""
}

// setLink records a verified link. Every branch returns a plain nil rather than
// a *model.AppError: handing a typed nil pointer back through an error
// interface makes it compare non-nil at the call site.
//
// Everything it needs to know is read before anything is written, and a read
// that fails stops it there with nothing changed. Taking a failed read as "no
// owner" used to skip the takeover and leave the previous owner linked to the
// key, with its streamer switch and wipe; taking one as "no previous key"
// could leave a reverse entry naming this person beside their new one.
//
// Whoever it takes the key from is told at once, as the person linking is
// told by handleLink: for them it is an unlink.
func (p *Plugin) setLink(userID, streamKey, token string) error {
	streamKey = cleanStreamKey(streamKey)
	if streamKey == "" {
		return p.clearLink(userID)
	}

	normalized := normalizeKey(streamKey)

	previous, err := p.linkedKey(userID)
	if err != nil {
		return err
	}

	// Their previous key's reverse entry goes only if it still names them:
	// somebody else may have proved that key since, and their link must
	// survive this one.
	dropPrevious := false
	if previous != "" && normalizeKey(previous) != normalized {
		previousNames, err := p.linkedUser(previous)
		if err != nil {
			return err
		}
		dropPrevious = previousNames == userID
	}

	owner, err := p.linkedUser(normalized)
	if err != nil {
		return err
	}

	// The owner's forward entry goes only if it is this key. One that names
	// another key is a link of theirs to that one, which proving this key
	// says nothing about.
	takeOver := false
	if owner != "" && owner != userID {
		ownersKey, err := p.linkedKey(owner)
		if err != nil {
			return err
		}
		takeOver = normalizeKey(ownersKey) == normalized
	}

	if dropPrevious {
		if appErr := p.API.KVDelete(kvLinkByKeyPrefix + normalizeKey(previous)); appErr != nil {
			return appErr
		}
	}

	// A verified token beats any earlier claim on the same key.
	if takeOver {
		if appErr := p.API.KVDelete(kvLinkTokenPrefix + owner); appErr != nil {
			return appErr
		}
		if appErr := p.API.KVDelete(kvLinkByUserPrefix + owner); appErr != nil {
			return appErr
		}

		// For them this is an unlink, and until now nobody told them: their
		// votes stop counting, their streamer rights go with the key, and the
		// panel locks for them unless an admin has let them in.
		p.clipsLinkChanged(owner)
		p.accessChanged(owner)
	}

	// Order matters: write the reverse mapping and the token before the forward
	// mapping, so a failure part way through never leaves a forward link
	// pointing at a record that does not exist.
	if appErr := p.API.KVSet(kvLinkByKeyPrefix+normalized, []byte(userID)); appErr != nil {
		return appErr
	}
	if appErr := p.API.KVSet(kvLinkTokenPrefix+userID, []byte(token)); appErr != nil {
		_ = p.API.KVDelete(kvLinkByKeyPrefix + normalized)
		return appErr
	}
	if appErr := p.API.KVSet(kvLinkByUserPrefix+userID, []byte(streamKey)); appErr != nil {
		_ = p.API.KVDelete(kvLinkTokenPrefix + userID)
		_ = p.API.KVDelete(kvLinkByKeyPrefix + normalized)
		return appErr
	}

	return nil
}

// holdsLinkEntries is whether anything a link leaves in the store is there
// for this person: the entry naming their stream, or their kept token. Either
// by itself (left by a race or a failed cleanup) is still something an unlink
// has to clear, so this is not linkOf: it is "would clearLink change anything".
func (p *Plugin) holdsLinkEntries(userID string) (bool, error) {
	key, err := p.linkedKey(userID)
	if err != nil {
		return false, err
	}
	if key != "" {
		return true, nil
	}

	token, appErr := p.API.KVGet(kvLinkTokenPrefix + userID)
	if appErr != nil {
		return false, appErr
	}

	return len(token) > 0, nil
}

func (p *Plugin) clearLink(userID string) error {
	previous, err := p.linkedKey(userID)
	if err != nil {
		return err
	}

	// The reverse entry goes only while it names them: once somebody else has
	// proved the key it is theirs, and this unlink must not take it from them.
	if previous != "" {
		owner, err := p.linkedUser(previous)
		if err != nil {
			return err
		}
		if owner == userID {
			if appErr := p.API.KVDelete(kvLinkByKeyPrefix + normalizeKey(previous)); appErr != nil {
				return appErr
			}
		}
	}

	if appErr := p.API.KVDelete(kvLinkTokenPrefix + userID); appErr != nil {
		return appErr
	}
	if appErr := p.API.KVDelete(kvLinkByUserPrefix + userID); appErr != nil {
		return appErr
	}

	return nil
}

// kvKeysWithPrefix pages through the whole KV namespace collecting matches.
//
// Deduplicated, because the paging is by OFFSET over a namespace that ordinary
// users can now write to. A key inserted between two pages shifts every later
// key along by one, and the key that straddled the boundary comes back twice.
// reconcileBadges does offlineStreak[userID]++ once per returned key, so a
// duplicate there is a badge cleared a poll early; the airtime export would
// list a month twice. Sorted so callers get a stable order whatever the store
// returns.
//
// The same paging can also MISS a key, with no error: one deleted ahead of the
// page being read moves every later key back one place, and the key on the
// boundary is never returned. The callers here ask again on the next poll or
// the next click. Anything that would remember the answer for longer must not
// take it from here, which is why the monthly medals read their months by name
// (see readEndedPodiums).
func (p *Plugin) kvKeysWithPrefix(prefix string) []string {
	seen := map[string]bool{}

	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, 200)
		if appErr != nil {
			p.API.LogWarn("Could not list plugin KV keys", "error", appErr.Error())
			break
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if strings.HasPrefix(key, prefix) {
				seen[key] = true
			}
		}
		if len(keys) < 200 {
			break
		}
	}

	matched := make([]string, 0, len(seen))
	for key := range seen {
		matched = append(matched, key)
	}
	sort.Strings(matched)

	return matched
}
