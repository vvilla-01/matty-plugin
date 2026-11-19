package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
)

// What the app check keeps (app_check.go), all of it in the key-value store
// with an expiry, and nothing that tells one phone from another of the same
// model and system:
//
//   - a challenge: its hash, the session it was given to and when, for five
//     minutes, in one of a few slots per session, and spent by
//     compare-and-delete, the way the connect tickets are (accounts.go);
//   - a result, per session: verified or not and why, when, the app's
//     version code, the security level, the boot state, and the app's own
//     claims, labelled as its word; a digest of the rules it was judged by;
//     with a verdict of verified, what it was given for (the package, the
//     signing certificate's digest, the owner's boot key when there is one,
//     the revocation list's date); for 30 days or until the session ends,
//     whichever is sooner;
//   - an index per person of their sessions' results, and one of the people
//     who have any, so that the admins' list reads no more than it shows.
const (
	kvAppCheckChallengePrefix = "appcheck_challenge_"
	kvAppCheckResultPrefix    = "appcheck_result_"
	kvAppCheckPersonPrefix    = "appcheck_person_"
	kvAppCheckPeople          = "appcheck_people"

	// A few challenges may be outstanding per session: an app that asks
	// again before answering overwrites the oldest.
	appCheckChallengeSlots   = 3
	appCheckChallengeSeconds = 5 * 60

	appCheckResultDays        = 30
	appCheckSessionsPerPerson = 20
	appCheckPeopleKept        = 5000

	// A verdict is remembered on each node for the gate: a pass for a
	// minute, anything else for a few seconds, so that a Matty that has
	// just proved itself on another node waits no longer than that.
	appCheckPassRemembered  = time.Minute
	appCheckOtherRemembered = 5 * time.Second
	appCheckRememberedMax   = 10000
)

var errAppCheckStale = errors.New("no such challenge")

// appCheckChallenge is a challenge as kept: never the challenge itself.
type appCheckChallenge struct {
	Hash    string `json:"hash"`
	Session string `json:"session"`
	At      int64  `json:"at"`
}

// appCheckClaims is what the app says about itself: Android's record of its
// own signing certificates as it reads them. Its word only, kept beside the
// verdict and labelled so wherever it is shown; nothing is decided by it.
type appCheckClaims struct {
	Package       string   `json:"package,omitempty"`
	VersionCode   int64    `json:"versionCode,omitempty"`
	CurrentSigner string   `json:"currentSigner,omitempty"`
	History       []string `json:"history,omitempty"`
	Hybrid        bool     `json:"hybrid,omitempty"`
}

// appCheckResult is one session's verdict as kept.
type appCheckResult struct {
	User        string          `json:"user"`
	Verified    bool            `json:"verified"`
	Development bool            `json:"development,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	At          int64           `json:"at"`
	Expires     int64           `json:"expires"`
	VersionCode int64           `json:"versionCode,omitempty"`
	Level       string          `json:"level,omitempty"`
	BootState   string          `json:"bootState,omitempty"`
	Claims      *appCheckClaims `json:"claims,omitempty"`

	// What a verdict of verified was given for, kept only with one: the
	// package and the signing certificate's digest Android attested, the
	// verified-boot key's digest when the phone runs its owner's own system,
	// and the date of the copy of Google's revocation list it was judged
	// against. resultFor holds a verdict to them on every read, so that one
	// stops counting once its certificate or boot key is taken off the
	// settings' lists, or once a release with another revocation list is
	// installed (stillAccepted).
	Package        string `json:"package,omitempty"`
	Digest         string `json:"digest,omitempty"`
	BootKey        string `json:"bootKey,omitempty"`
	RevocationList string `json:"revocationList,omitempty"`

	// Rules is the digest of the rules the verdict was judged by
	// (appCheckRules), kept with every verdict. A verdict of not verified
	// counts only while the rules are still these; one kept without it is
	// judged again. Never shown: the admins' list leaves it out.
	Rules string `json:"rules,omitempty"`
}

// appCheckIndex is a set of ids, each with when its last result expires.
type appCheckIndex struct {
	Entries map[string]int64 `json:"entries"`
}

// appCheckState is the app check's memory on this node: its meters, the
// verdicts lately read, and its clock. Made on first use, so that a route a
// test reaches without activating the plugin works like any other.
type appCheckState struct {
	challengeLimiter *rateLimiter
	attestLimiter    *rateLimiter

	lock       sync.Mutex
	remembered map[string]appCheckRemembered

	now func() time.Time
}

type appCheckRemembered struct {
	result *appCheckResult
	until  time.Time
}

func (p *Plugin) appCheck() *appCheckState {
	p.appCheckOnce.Do(func() {
		p.appCheckMemory = &appCheckState{
			challengeLimiter: newRateLimiter(10, time.Minute),
			attestLimiter:    newRateLimiter(10, time.Minute),
			remembered:       map[string]appCheckRemembered{},
			now:              time.Now,
		}
	})

	return p.appCheckMemory
}

func appCheckChallengeKey(sessionID string, slot int) string {
	return kvAppCheckChallengePrefix + sessionID + "_" + strconv.Itoa(slot)
}

// keepChallenge keeps a new challenge's hash for the session, in the slot
// that is free, or else in the one holding the oldest.
func (p *Plugin) keepChallenge(sessionID, hash string, now time.Time) error {
	slot, oldest := 0, int64(-1)
	for i := range appCheckChallengeSlots {
		raw, appErr := p.API.KVGet(appCheckChallengeKey(sessionID, i))
		if appErr != nil {
			return errStore
		}

		var kept appCheckChallenge
		if len(raw) == 0 || json.Unmarshal(raw, &kept) != nil || now.UnixMilli()-kept.At > appCheckChallengeSeconds*1000 {
			slot = i
			break
		}
		if oldest < 0 || kept.At < oldest {
			slot, oldest = i, kept.At
		}
	}

	record, err := json.Marshal(appCheckChallenge{Hash: hash, Session: sessionID, At: now.UnixMilli()})
	if err != nil {
		return err
	}

	written, appErr := p.API.KVSetWithOptions(appCheckChallengeKey(sessionID, slot), record, model.PluginKVSetOptions{ExpireInSeconds: appCheckChallengeSeconds})
	if appErr != nil || !written {
		return errStore
	}

	return nil
}

// spendChallenge spends the session's challenge with this hash: once, and
// only within its five minutes. errAppCheckStale when there is none such.
func (p *Plugin) spendChallenge(sessionID, hash string, now time.Time) error {
	for i := range appCheckChallengeSlots {
		key := appCheckChallengeKey(sessionID, i)

		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			return errStore
		}
		if len(raw) == 0 {
			continue
		}

		var kept appCheckChallenge
		if json.Unmarshal(raw, &kept) != nil ||
			subtle.ConstantTimeCompare([]byte(kept.Hash), []byte(hash)) != 1 ||
			subtle.ConstantTimeCompare([]byte(kept.Session), []byte(sessionID)) != 1 {
			continue
		}

		// The read may come from a replica: the delete decides, so that a
		// challenge answered twice at once is spent by one of the two.
		spent, appErr := p.API.KVCompareAndDelete(key, raw)
		if appErr != nil {
			return errStore
		}
		if !spent || now.UnixMilli()-kept.At > appCheckChallengeSeconds*1000 || now.UnixMilli() < kept.At {
			return errAppCheckStale
		}

		return nil
	}

	return errAppCheckStale
}

// keepResult keeps a session's verdict, and the two indexes that find it.
func (p *Plugin) keepResult(sessionID string, result *appCheckResult, now time.Time) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}

	seconds := max((result.Expires-now.UnixMilli())/1000, 1)
	written, appErr := p.API.KVSetWithOptions(kvAppCheckResultPrefix+sessionID, raw, model.PluginKVSetOptions{ExpireInSeconds: seconds})
	if appErr != nil || !written {
		return errStore
	}
	p.rememberResult(sessionID, result, now)

	// The indexes only help the admins' list find results; one that could
	// not be written leaves the verdict standing, and is said in the log.
	if err := p.indexAppCheck(kvAppCheckPersonPrefix+result.User, sessionID, result.Expires, now, appCheckSessionsPerPerson); err != nil {
		p.API.LogWarn("Could not note an app check in its person's index", "error", err.Error())
	}
	if err := p.indexAppCheck(kvAppCheckPeople, result.User, result.Expires, now, appCheckPeopleKept); err != nil {
		p.API.LogWarn("Could not note an app check in the index of people", "error", err.Error())
	}

	return nil
}

// indexAppCheck notes id in an index until expires, leaving out what has
// expired and, past limit, what expires soonest.
func (p *Plugin) indexAppCheck(key, id string, expires int64, now time.Time, limit int) error {
	return p.casUpdate(key, func(raw []byte) ([]byte, error) {
		index := appCheckIndex{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &index)
		}

		entries := map[string]int64{id: expires}
		for other, until := range index.Entries {
			if other != id && until > now.UnixMilli() {
				entries[other] = until
			}
		}

		if len(entries) > limit {
			ids := make([]string, 0, len(entries))
			for other := range entries {
				ids = append(ids, other)
			}
			sort.Slice(ids, func(a, b int) bool { return entries[ids[a]] > entries[ids[b]] })
			for _, dropped := range ids[limit:] {
				delete(entries, dropped)
			}
		}

		return json.Marshal(appCheckIndex{Entries: entries})
	})
}

// readIndex is an index's ids that have not expired, the latest first.
func (p *Plugin) readIndex(key string, now time.Time) ([]string, error) {
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		return nil, errStore
	}

	var index appCheckIndex
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &index)
	}

	ids := make([]string, 0, len(index.Entries))
	for id, until := range index.Entries {
		if until > now.UnixMilli() {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(a, b int) bool {
		if index.Entries[ids[a]] != index.Entries[ids[b]] {
			return index.Entries[ids[a]] > index.Entries[ids[b]]
		}

		return ids[a] < ids[b]
	})

	return ids, nil
}

// readResult is a session's verdict, or nil when it has none that has not
// expired.
func (p *Plugin) readResult(sessionID string, now time.Time) (*appCheckResult, error) {
	raw, appErr := p.API.KVGet(kvAppCheckResultPrefix + sessionID)
	if appErr != nil {
		return nil, errStore
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var result appCheckResult
	if json.Unmarshal(raw, &result) != nil || result.Expires <= now.UnixMilli() {
		return nil, nil
	}

	return &result, nil
}

// resultFor is the verdict for the caller's session, from this node's
// memory when it is fresh enough, or nil. A verdict kept for somebody else
// is none: a session is one person's. A verdict that no longer counts under
// what this build and cfg accept (stillCounts) is none as well, and stale
// says so: the app then proves itself again.
func (p *Plugin) resultFor(sessionID, userID string, cfg *configuration) (result *appCheckResult, stale bool, err error) {
	if sessionID == "" {
		return nil, false, nil
	}

	s := p.appCheck()
	now := s.now()

	s.lock.Lock()
	remembered, known := s.remembered[sessionID]
	s.lock.Unlock()

	result = remembered.result
	if !known || now.After(remembered.until) {
		if result, err = p.readResult(sessionID, now); err != nil {
			return nil, false, err
		}
		p.rememberResult(sessionID, result, now)
	}

	if result == nil || result.User != userID || result.Expires <= now.UnixMilli() {
		return nil, false, nil
	}
	if !p.stillCounts(result, cfg) {
		return nil, true, nil
	}

	return result, false, nil
}

// stillCounts is whether a verdict counts under what this build and cfg
// accept now. A verdict of verified is held to what it was given for
// (stillAccepted), so that adding an unrelated certificate to a list leaves
// it standing. A verdict of not verified counts only under the rules it was
// judged by (appCheckRules): any change to them may be what it failed on.
// When this build's own lists do not read, nothing can be judged again any
// better, and such a verdict stands.
func (p *Plugin) stillCounts(result *appCheckResult, cfg *configuration) bool {
	if result.Verified {
		return p.stillAccepted(result, cfg)
	}

	rules, err := p.appCheckRules(cfg)
	if err != nil {
		return true
	}

	return result.Rules == rules
}

// appCheckRules is a digest of the rules a verdict is judged by: what
// appCheckPolicy accepts (the packages, the signing certificates' digests,
// the verified-boot keys, the date of the copy of Google's revocation list,
// and whether the development build's allowances apply), each in a fixed
// order, and this server's Site URL as an origin (judge checks the app's
// address against it first), or "" when it has none that can be used. The
// setting (off, report, require) is not in it: it decides what is done with
// a verdict, not what the verdict is.
func (p *Plugin) appCheckRules(cfg *configuration) (string, error) {
	policy, err := p.appCheckPolicy(cfg)
	if err != nil {
		return "", err
	}

	site, err := appcheck.Origin(p.siteURL())
	if err != nil {
		site = ""
	}

	return appCheckRulesOf(policy, appcheck.RevocationListDate, site), nil
}

// appCheckInForce is the app check's setting and the digest of its rules
// (appCheckRules) as the last settings load found them.
type appCheckInForce struct {
	lock  sync.Mutex
	kept  bool
	mode  string
	rules string
}

// noteAppCheckInForce keeps the app check's setting and the digest of its
// rules under cfg, and says whether either differs from what was kept
// before. Compared with what was kept and not with the previous plugin
// settings, because the Site URL is in the rules and is Mattermost's
// setting, not the plugin's: Mattermost calls OnConfigurationChange for a
// change of either. The first load only keeps. When the digest cannot be
// made, nothing is kept and nothing has changed.
//
// The digest is made while the lock is held, so that it reads the Site URL
// as it is when it is compared: Mattermost runs this hook after letting go
// of its own configuration lock, so two loads can run at once, and one that
// read the Site URL first and locked last would keep the older one. The
// lock is a leaf: nothing is taken while it is held but Mattermost's
// GetConfig, which takes no lock of this plugin's.
func (p *Plugin) noteAppCheckInForce(cfg *configuration) bool {
	kept := &p.appCheckKept
	kept.lock.Lock()
	defer kept.lock.Unlock()

	rules, err := p.appCheckRules(cfg)
	if err != nil {
		return false
	}
	mode := cfg.appCheckMode()

	changed := kept.kept && (kept.mode != mode || kept.rules != rules)
	kept.kept, kept.mode, kept.rules = true, mode, rules

	return changed
}

// appCheckRulesOf is appCheckRules for a policy, a revocation list's date
// and a Site URL's origin.
func appCheckRulesOf(policy appcheck.Policy, revocationList, site string) string {
	var rules strings.Builder
	rules.WriteString("matty-app-check-rules 1\n")

	for _, set := range []struct {
		name    string
		entries map[string]bool
	}{
		{"package", policy.Packages},
		{"certificate", policy.Digests},
		{"boot-key", policy.BootKeys},
	} {
		entries := make([]string, 0, len(set.entries))
		for entry, accepted := range set.entries {
			if accepted {
				entries = append(entries, entry)
			}
		}
		sort.Strings(entries)
		for _, entry := range entries {
			rules.WriteString(set.name + " " + entry + "\n")
		}
	}
	rules.WriteString("revocation-list " + revocationList + "\n")
	rules.WriteString("development " + strconv.FormatBool(policy.Development) + "\n")
	rules.WriteString("origin " + site + "\n")

	return hashHex(rules.String())
}

// stillAccepted is whether a verdict of verified is still one this build
// and cfg would give: its package and signing certificate still accepted,
// its owner's boot key (if it was given for one) still on a list, judged
// against this build's copy of Google's revocation list, and no development
// verdict outside the development build. A verdict kept without what it was
// given for is not.
func (p *Plugin) stillAccepted(result *appCheckResult, cfg *configuration) bool {
	policy, err := p.appCheckPolicy(cfg)
	if err != nil {
		return false
	}

	development := result.Development && policy.Development
	switch {
	case result.RevocationList != appcheck.RevocationListDate:
		return false
	case result.Development && !policy.Development:
		return false
	case !policy.Packages[result.Package] || !policy.Digests[result.Digest]:
		return false
	case result.BootKey != "" && !policy.BootKeys[result.BootKey] && !development:
		// The development build takes any boot state, and so any key.
		return false
	}

	return true
}

func (p *Plugin) rememberResult(sessionID string, result *appCheckResult, now time.Time) {
	s := p.appCheck()

	until := now.Add(appCheckOtherRemembered)
	if result != nil && result.Verified {
		until = now.Add(appCheckPassRemembered)
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.remembered) >= appCheckRememberedMax {
		for id, old := range s.remembered {
			if now.After(old.until) {
				delete(s.remembered, id)
			}
		}
		if len(s.remembered) >= appCheckRememberedMax {
			s.remembered = map[string]appCheckRemembered{}
		}
	}
	s.remembered[sessionID] = appCheckRemembered{result: result, until: until}
}
