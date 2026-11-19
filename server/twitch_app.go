package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The admin's Twitch developer application, and the one app access token this
// server holds for it.
//
// One component, shared by everything in VillaNET that talks to Twitch: looking
// games up in IGDB, and showing people who connected their Twitch account while
// they are live there (twitch_live.go). It deliberately knows nothing about
// either. It reads no feature's switch, MediaLookup and EnableTwitchLive
// included: each caller decides whether its own feature is on before asking, so
// switching one feature off never changes what this does for another.
//
// It talks to id.twitch.tv and api.twitch.tv, and only when a caller asks.
// Nothing here runs on activation, on a settings change or on a timer.
//
// The token comes from Twitch's client-credentials flow and is held in memory,
// nowhere else. Not in KV, which outlives an uninstall and lands in every
// database backup: a restart, or another node in a cluster, costs one fetch
// instead. Twitch allows an application 25 active tokens and cancels the oldest
// when more are issued, so a node whose token was cancelled is answered 401,
// asks for a new one, and carries on. Nor is it revoked on deactivate, which
// would add an outside call to every plugin stop for very little.
//
// Neither the secret, the token nor anything Twitch answered ever appears in a
// log line, an error or an HTTP response. Errors are the three fixed values
// below, which callers wrap with their own name ("IGDB: ").

// Where Twitch issues tokens. A variable only so tests can point it at a
// fake: nothing a test runs ever leaves the machine.
var twitchIDBase = "https://id.twitch.tv"

const (
	// One fetch, on a context of its own, so the caller that happened to start
	// it going away does not fail everybody else waiting for the same token.
	twitchTokenBudget = 8 * time.Second

	// Renewed this close to its expiry. Twitch prefers tokens to be renewed
	// when one is refused rather than early, which is done too; an hour before
	// a roughly sixty-day expiry costs nothing.
	twitchRenewBefore = time.Hour

	// Twitch asks apps that keep an OAuth session to validate it hourly.
	// Whether that covers an app token is not documented, so it is done, but
	// only when the token is about to be used.
	twitchValidateEvery = time.Hour

	// A validation that could not be finished is tried again this soon. A
	// hiccup at id.twitch.tv must not stop the lookups using the token.
	twitchRevalidateAfter = 5 * time.Minute

	// After Twitch refuses the credentials, the same ones are not tried again
	// for this long. Otherwise every keystroke in the planning form would be
	// another token request with a secret Twitch has just said is wrong.
	twitchRefusedPause = 5 * time.Minute

	// After any other failure: Twitch slow or down.
	twitchFailurePause = 15 * time.Second

	// This component's own warning throttle. The same length as the media
	// lookups' but not shared with it, so nothing here depends on that feature.
	twitchWarnEvery = 15 * time.Minute

	// expires_in is clamped to this, and a missing or nonsensical one counts as
	// a day.
	twitchMaxLifetime     = 90 * 24 * time.Hour
	twitchDefaultLifetime = 24 * time.Hour

	maxTwitchAnswerBytes = 64 << 10
	maxTwitchTokenBytes  = 512
)

var (
	errTwitchNotSetUp    = errors.New("no Twitch application is set up")
	errTwitchRefused     = errors.New("Twitch refused the application's Client ID or Client Secret")
	errTwitchUnavailable = errors.New("Twitch did not issue a token just now")
)

// twitchCredentials are the two halves of the admin's Twitch application.
type twitchCredentials struct {
	clientID string
	secret   string
}

// twitchCredentials is the Twitch application the admin set up, or false
// when either half is missing. Not tied to any feature's switch: each
// caller decides whether its own feature is on.
func (c *configuration) twitchCredentials() (twitchCredentials, bool) {
	if c.TwitchClientID == "" || c.TwitchClientSecret == "" {
		return twitchCredentials{}, false
	}

	return twitchCredentials{clientID: c.TwitchClientID, secret: c.TwitchClientSecret}, true
}

// fingerprint is sha256(clientID + "\x00" + secret). Kept instead of the
// secret wherever credentials have to be compared later, so no copy of the
// secret outlives the configuration it came from.
func (c twitchCredentials) fingerprint() [32]byte {
	return sha256.Sum256([]byte(c.clientID + "\x00" + c.secret))
}

// twitchApp holds the one app access token this server has for the admin's
// Twitch application, in memory only.
type twitchApp struct {
	plugin *Plugin
	now    func() time.Time // time.Now; tests replace it

	// after waits, as time.After does: only Helix's one retry of a 503 waits
	// on anything, and tests replace it so that nothing sleeps.
	after func(time.Duration) <-chan time.Time

	lock      sync.Mutex
	token     string
	issuedFor [32]byte // the fingerprint of the credentials it was issued for
	issuedBy  uint64   // the fetch that produced it; a fetch started later wins the slot
	expiresAt time.Time
	checkedAt time.Time // issued, or last confirmed by /oauth2/validate

	// fetches counts fetches started, so each knows its place. A count rather
	// than a start time, because two fetches can start within one tick of a
	// coarse clock, and "which started later" must still have one answer.
	fetches uint64

	refusedFor   [32]byte // credentials Twitch refused, and until when that is believed
	refusedUntil time.Time
	failedFor    [32]byte // credentials whose last fetch failed for some other reason
	retryAt      time.Time

	// Keyed "token/" and "validate/" plus the first eight bytes of the
	// fingerprint, in hex: one of each per set of credentials at a time, so a
	// caller with a newly pasted secret never joins a fetch made with the old.
	flights flightGroup

	warnLock sync.Mutex
	warnedAt map[string]time.Time // fixed keys only: see warnOnce's callers
}

// twitchApp is made on first use, so a server that never sets Twitch up holds
// none of it.
func (p *Plugin) twitchApp() *twitchApp {
	p.twitchOnce.Do(func() {
		p.twitchState = &twitchApp{plugin: p, now: time.Now, after: time.After, warnedAt: map[string]time.Time{}}
	})

	return p.twitchState
}

// twitchHeld is what the held token means for one set of credentials now.
type twitchHeld int

const (
	twitchNeedsFetch twitchHeld = iota
	twitchUsable
	twitchDueCheck
	twitchRefusedNow
	twitchFailedNow
)

// look decides what a caller with these credentials gets. Called with lock
// held.
//
// While a renewal has just failed, the token being renewed is still handed
// out until it expires: it works for up to an hour yet, and an id.twitch.tv
// that is down must not take every feature using Twitch down with it. The
// renewal is tried again once the pause is over.
func (a *twitchApp) look(fp [32]byte, now time.Time) (string, twitchHeld) {
	switch {
	case fp == a.refusedFor && now.Before(a.refusedUntil):
		if token, ok := a.unexpired(fp, now); ok {
			return token, twitchUsable
		}

		return "", twitchRefusedNow
	case fp == a.failedFor && now.Before(a.retryAt):
		if token, ok := a.unexpired(fp, now); ok {
			return token, twitchUsable
		}

		return "", twitchFailedNow
	case a.token != "" && a.issuedFor == fp && now.Before(a.expiresAt.Add(-twitchRenewBefore)):
		if now.Sub(a.checkedAt) < twitchValidateEvery {
			return a.token, twitchUsable
		}

		return a.token, twitchDueCheck
	default:
		return "", twitchNeedsFetch
	}
}

// unexpired is the token held for these credentials while it has not expired,
// whether or not it is due for renewal. Called with lock held.
func (a *twitchApp) unexpired(fp [32]byte, now time.Time) (string, bool) {
	if a.token != "" && a.issuedFor == fp && now.Before(a.expiresAt) {
		return a.token, true
	}

	return "", false
}

// appToken is a token for these credentials, fetching one only when none is
// held, the held one was issued for other credentials, or it is near expiry.
func (a *twitchApp) appToken(ctx context.Context, creds twitchCredentials) (string, error) {
	if creds.clientID == "" || creds.secret == "" {
		return "", errTwitchNotSetUp
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	fp := creds.fingerprint()

	a.lock.Lock()
	token, held := a.look(fp, a.now())
	a.lock.Unlock()

	switch held {
	case twitchRefusedNow:
		return "", errTwitchRefused
	case twitchFailedNow:
		return "", errTwitchUnavailable
	case twitchUsable:
		return token, nil
	case twitchDueCheck:
		if kept, good := a.validate(creds, fp, token); good {
			return kept, nil
		}
	}

	fetched, err := a.fetch(creds, fp)
	if err != nil {
		// A renewal that failed leaves the token it was renewing, which still
		// works until it expires (look, above).
		a.lock.Lock()
		spare, ok := a.unexpired(fp, a.now())
		a.lock.Unlock()

		if ok {
			return spare, nil
		}
	}

	return fetched, err
}

// invalidate forgets token if it is still the one held. Called by whoever got
// a 401 with it; a newer token fetched in the meantime is kept.
func (a *twitchApp) invalidate(token string) {
	if token == "" {
		return
	}

	a.lock.Lock()
	defer a.lock.Unlock()

	if a.token == token {
		a.token = ""
		a.expiresAt = time.Time{}
		a.checkedAt = time.Time{}
	}
}

func flightName(kind string, fp [32]byte) string {
	return kind + "/" + hex.EncodeToString(fp[:8])
}

// twitchChecked is one validation's verdict on one token.
type twitchChecked struct {
	token string
	good  bool
}

// validate asks Twitch whether the held token is still good, one request for
// everybody asking at once. False means a new token is needed.
func (a *twitchApp) validate(creds twitchCredentials, fp [32]byte, token string) (string, bool) {
	value, _ := a.flights.do(flightName("validate", fp), func() (any, error) {
		return twitchChecked{token: token, good: a.validateNow(creds, token)}, nil
	})

	checked, _ := value.(twitchChecked)
	if !checked.good || checked.token == "" {
		return "", false
	}

	return checked.token, true
}

func (a *twitchApp) validateNow(creds twitchCredentials, token string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), twitchTokenBudget)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, twitchIDBase+"/oauth2/validate", nil)
	if err != nil {
		a.validateLater(token)
		return true
	}
	request.Header.Set("Authorization", "OAuth "+token)
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		a.validateLater(token)
		return true
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		a.invalidate(token)
		return false
	default:
		a.validateLater(token)
		return true
	}

	var answer struct {
		ClientID  string  `json:"client_id"`
		ExpiresIn float64 `json:"expires_in"`
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxTwitchAnswerBytes+1))
	if err != nil || len(body) > maxTwitchAnswerBytes || json.Unmarshal(body, &answer) != nil {
		a.validateLater(token)
		return true
	}

	// A token for another application is not this application's token, and
	// whatever it can do is not what the admin set up.
	if answer.ClientID != creds.clientID {
		a.invalidate(token)
		return false
	}

	a.lock.Lock()
	if a.token == token {
		now := a.now()
		a.checkedAt = now
		if answer.ExpiresIn > 0 {
			a.expiresAt = now.Add(twitchLifetime(answer.ExpiresIn))
		}
	}
	a.lock.Unlock()

	return true
}

// validateLater keeps the token in use and asks about it again shortly.
func (a *twitchApp) validateLater(token string) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.token == token {
		a.checkedAt = a.now().Add(-twitchValidateEvery + twitchRevalidateAfter)
	}
}

// twitchIssued is a fetch's answer: the token and the credentials it is for.
type twitchIssued struct {
	token string
	fp    [32]byte
}

// fetch gets a new token, one request for everybody with these credentials
// asking at once. The flight is bounded by its own budget, so the wait is too.
func (a *twitchApp) fetch(creds twitchCredentials, fp [32]byte) (string, error) {
	// Twice at most: a waiter whose credentials differ from the flight's,
	// which keyed flights should make impossible, fetches once more for its
	// own rather than take a token issued for somebody else's.
	for attempt := 0; attempt < 2; attempt++ {
		value, err := a.flights.do(flightName("token", fp), func() (any, error) {
			return a.fetchNow(creds, fp)
		})
		if err != nil {
			return "", err
		}

		issued, _ := value.(twitchIssued)
		if issued.fp == fp && issued.token != "" {
			return issued.token, nil
		}
	}

	return "", errTwitchUnavailable
}

func (a *twitchApp) fetchNow(creds twitchCredentials, fp [32]byte) (any, error) {
	a.lock.Lock()
	// Another flight may have just finished for these credentials, between
	// this caller looking and this flight starting.
	switch token, held := a.look(fp, a.now()); held {
	case twitchUsable, twitchDueCheck:
		a.lock.Unlock()
		return twitchIssued{token: token, fp: fp}, nil
	case twitchRefusedNow:
		a.lock.Unlock()
		return nil, errTwitchRefused
	case twitchFailedNow:
		a.lock.Unlock()
		return nil, errTwitchUnavailable
	}
	a.fetches++
	place := a.fetches
	a.lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), twitchTokenBudget)
	defer cancel()

	// In the body, never the query string, so the secret is never part of an
	// address that some proxy or error message could repeat.
	form := url.Values{}
	form.Set("client_id", creds.clientID)
	form.Set("client_secret", creds.secret)
	form.Set("grant_type", "client_credentials")

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, twitchIDBase+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, a.failed(fp, 0, "the request could not be built")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		return nil, a.failed(fp, 0, "Twitch could not be reached")
	}
	defer response.Body.Close()

	switch status := response.StatusCode; status {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		// A made-up Client ID is answered 400 "invalid client"; a wrong
		// secret is reported as 403. Either way the admin has to act.
		return nil, a.refused(fp, status)
	default:
		return nil, a.failed(fp, status, "")
	}

	var answer struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		TokenType   string  `json:"token_type"`
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxTwitchAnswerBytes+1))
	if err != nil || len(body) > maxTwitchAnswerBytes || json.Unmarshal(body, &answer) != nil ||
		!twitchTokenShaped(answer.AccessToken) || !strings.EqualFold(answer.TokenType, "bearer") {
		return nil, a.failed(fp, 0, "the answer was not the expected shape")
	}

	a.lock.Lock()
	now := a.now()
	// A token from a fetch that started later is kept: the admin changed the
	// credentials while this one was out, and the old credentials' late answer
	// must not displace the new ones'. This caller's own waiters still get it.
	if a.token == "" || place > a.issuedBy {
		a.token = answer.AccessToken
		a.issuedFor = fp
		a.issuedBy = place
		a.expiresAt = now.Add(twitchLifetime(answer.ExpiresIn))
		a.checkedAt = now
	}
	if a.refusedFor == fp {
		a.refusedFor, a.refusedUntil = [32]byte{}, time.Time{}
	}
	if a.failedFor == fp {
		a.failedFor, a.retryAt = [32]byte{}, time.Time{}
	}
	a.lock.Unlock()

	return twitchIssued{token: answer.AccessToken, fp: fp}, nil
}

// refused remembers that Twitch refused these credentials, and tells the admin.
func (a *twitchApp) refused(fp [32]byte, status int) error {
	a.lock.Lock()
	a.refusedFor = fp
	a.refusedUntil = a.now().Add(twitchRefusedPause)
	a.lock.Unlock()

	a.warnOnce("twitch-credentials",
		"Twitch refused the Twitch application's Client ID or Client Secret, so Matty cannot use Twitch "+
			"(IGDB game lookups stop until this is fixed). In the Twitch developer console (dev.twitch.tv/console/apps) "+
			"open the application, check the Client ID, press New Secret, and paste both into "+
			"System Console > Plugins > Matty > Twitch.",
		"status", status)

	return errTwitchRefused
}

// failed remembers a fetch that went wrong for some other reason. The reason
// given is always one of this file's fixed strings, never the answer itself.
func (a *twitchApp) failed(fp [32]byte, status int, reason string) error {
	a.lock.Lock()
	a.failedFor = fp
	a.retryAt = a.now().Add(twitchFailurePause)
	a.lock.Unlock()

	const message = "Twitch did not issue an access token just now; Matty will try again shortly."
	if status != 0 {
		a.warnOnce("twitch-token", message, "status", status)
	} else {
		a.warnOnce("twitch-token", message, "error", reason)
	}

	return errTwitchUnavailable
}

// warnOnce logs something the admin has to know, once in a while rather than
// once per request. Keys are fixed strings, so the map cannot grow.
func (a *twitchApp) warnOnce(key, message string, pairs ...any) {
	now := a.now()

	a.warnLock.Lock()
	last, warned := a.warnedAt[key]
	if warned && now.Sub(last) < twitchWarnEvery && !now.Before(last) {
		a.warnLock.Unlock()
		return
	}
	a.warnedAt[key] = now
	a.warnLock.Unlock()

	a.plugin.API.LogWarn(message, pairs...)
}

// twitchLifetime is how long a token Twitch said lasts seconds is believed.
func twitchLifetime(seconds float64) time.Duration {
	switch {
	case math.IsNaN(seconds) || seconds <= 0:
		return twitchDefaultLifetime
	case seconds >= twitchMaxLifetime.Seconds():
		return twitchMaxLifetime
	default:
		return time.Duration(seconds * float64(time.Second))
	}
}

// twitchTokenShaped is true for what an access token looks like: printable
// ASCII without spaces, of a sensible length. Anything else is not stored,
// because it goes into an Authorization header on every request after.
func twitchTokenShaped(token string) bool {
	if token == "" || len(token) > maxTwitchTokenBytes {
		return false
	}

	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] > '~' {
			return false
		}
	}

	return true
}

// What Twitch live adds to the shared component (design/twitch-live.md §3).
// Here rather than in twitch_live.go so that the client secret is still read
// in this file only. Twitch live never fetches an app token itself: Helix is
// asked with the one appToken holds.

// Where Helix lives. A variable only so tests can point it at a fake.
var twitchAPIBase = "https://api.twitch.tv"

const (
	// maxHelixAnswerBytes caps any Helix answer: 100 streams is about 100 KiB.
	maxHelixAnswerBytes = 1 << 20

	// maxIDTokenBytes caps the ID token before anything decodes it.
	maxIDTokenBytes = 8 << 10

	// twitchIssuer is the issuer Twitch's discovery document names. A claim
	// value, not a host, so tests do not swap it.
	twitchIssuer = "https://id.twitch.tv/oauth2"

	// How far an ID token's expiry may be behind this server's clock, which
	// need not agree with Twitch's to the second.
	twitchIDTokenLeeway = 5 * time.Minute

	// A Helix 503 is asked once more after this. Twitch's guide allows one
	// retry of a 503; anything more would be hammering a service that is down.
	helixRetryAfter = time.Second
)

var (
	// errTwitchSlowDown is Helix answering 429. The moment it said to wait
	// for travels with it as a *twitchSlowDown.
	errTwitchSlowDown = errors.New("Twitch asked to slow down")

	// errTwitchBadAnswer is an answer that was not the shape Twitch documents.
	errTwitchBadAnswer = errors.New("Twitch's answer was not the expected shape")

	twitchIDPattern    = regexp.MustCompile(`^[0-9]{1,20}$`)
	twitchLoginPattern = regexp.MustCompile(`^[a-z0-9_]{1,25}$`)
)

// twitchSlowDown is errTwitchSlowDown with the reset Helix sent, which is zero
// when it sent none.
type twitchSlowDown struct {
	reset time.Time
}

func (e *twitchSlowDown) Error() string { return errTwitchSlowDown.Error() }

func (e *twitchSlowDown) Is(target error) bool { return target == errTwitchSlowDown }

// twitchStatusError is an answer whose status VillaNET has no use for. It
// carries the status and nothing Twitch said.
type twitchStatusError struct {
	status int
}

func (e *twitchStatusError) Error() string { return fmt.Sprintf("Twitch answered %d", e.status) }

// twitchRate is what Helix said about the bucket on its last answer.
// remaining and limit are -1 when Helix did not say.
type twitchRate struct {
	limit, remaining int
	reset            time.Time // zero when not sent
}

func twitchRateOf(header http.Header) twitchRate {
	rate := twitchRate{limit: -1, remaining: -1}

	if limit, err := strconv.Atoi(header.Get("Ratelimit-Limit")); err == nil && limit >= 0 {
		rate.limit = limit
	}
	if remaining, err := strconv.Atoi(header.Get("Ratelimit-Remaining")); err == nil && remaining >= 0 {
		rate.remaining = remaining
	}
	if reset, err := strconv.ParseInt(header.Get("Ratelimit-Reset"), 10, 64); err == nil && reset > 0 {
		rate.reset = time.Unix(reset, 0).UTC()
	}

	return rate
}

// twitchUser is who a Twitch account is, as cleanTwitchUser let it through.
type twitchUser struct{ ID, Login, DisplayName string }

// cleanTwitchUser is the one check on who Twitch says somebody is, from
// whoever says it: whoseToken, Get Streams and the daily Get Users. The id is
// what everything is keyed on and the login goes into the player's address
// and every Watch on Twitch link, so both are held to their documented shape;
// the display name is only ever shown, so it is cleaned like anything a person
// typed, and is the login when nothing is left of it.
func cleanTwitchUser(id, login, name string) (twitchUser, bool) {
	login = strings.ToLower(login)
	if !twitchIDPattern.MatchString(id) || !twitchLoginPattern.MatchString(login) {
		return twitchUser{}, false
	}

	shown := cleanText(name, 64)
	if shown == "" {
		shown = login
	}

	return twitchUser{ID: id, Login: login, DisplayName: shown}, true
}

// helixGet asks Helix with the app token. On a 401 it invalidates that token
// and asks once more with a fresh one: Twitch cancels the oldest of an
// application's tokens when it issues too many, and a cancelled token is
// answered 401. A 503 is asked once more after a second. The body is capped
// and returned for the caller to decode; a status other than 200 is an error
// that carries the status and nothing else.
func (a *twitchApp) helixGet(ctx context.Context, creds twitchCredentials, path string, query url.Values) ([]byte, twitchRate, error) {
	token, err := a.appToken(ctx, creds)
	if err != nil {
		return nil, twitchRate{limit: -1, remaining: -1}, err
	}

	refreshed, retried := false, false
	for {
		body, rate, status, err := a.helixOnce(ctx, creds.clientID, token, path, query)
		if err != nil {
			return nil, rate, err
		}

		switch {
		case status == http.StatusOK:
			return body, rate, nil

		case status == http.StatusUnauthorized && !refreshed:
			a.invalidate(token)
			refreshed = true

			if token, err = a.appToken(ctx, creds); err != nil {
				return nil, rate, err
			}

			continue

		case status == http.StatusServiceUnavailable && !retried:
			retried = true

			select {
			case <-ctx.Done():
				return nil, rate, ctx.Err()
			case <-a.after(helixRetryAfter):
			}

			continue
		}

		a.warnOnce("twitch-helix", "Twitch's API did not answer Matty as expected; it will ask again later.", "status", status)

		switch {
		case status == http.StatusUnauthorized:
			return nil, rate, errTwitchRefused
		case status == http.StatusTooManyRequests:
			return nil, rate, &twitchSlowDown{reset: rate.reset}
		case status >= http.StatusInternalServerError:
			return nil, rate, errTwitchUnavailable
		default:
			return nil, rate, &twitchStatusError{status: status}
		}
	}
}

// helixOnce is one request to Helix with bearer, which is the app token or,
// for whoseToken, a person's own. A status other than 200 comes back with no
// body and no error, for the caller to decide about.
func (a *twitchApp) helixOnce(ctx context.Context, clientID, bearer, path string, query url.Values) ([]byte, twitchRate, int, error) {
	unknown := twitchRate{limit: -1, remaining: -1}

	address := twitchAPIBase + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, unknown, 0, errTwitchBadAnswer
	}
	request.Header.Set("Client-Id", clientID)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, unknown, 0, ctx.Err()
		}
		a.warnOnce("twitch-helix", "Twitch's API could not be reached; Matty will ask again later.")

		return nil, unknown, 0, errTwitchUnavailable
	}
	defer response.Body.Close()

	rate := twitchRateOf(response.Header)
	if response.StatusCode != http.StatusOK {
		return nil, rate, response.StatusCode, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxHelixAnswerBytes+1))
	if err != nil || len(body) > maxHelixAnswerBytes {
		a.warnOnce("twitch-helix", "Twitch's API answered with more than Matty reads; it will ask again later.")

		return nil, rate, 0, errTwitchBadAnswer
	}

	return body, rate, http.StatusOK, nil
}

// exchangeCode trades an authorization code for a user access token and the
// ID token that came with it. The secret goes in the form body only, as the
// app token's does. The refresh token in Twitch's answer is never decoded:
// it is not a field of the answer below, so it is dropped where the answer is
// read and never leaves this function.
func (a *twitchApp) exchangeCode(ctx context.Context, creds twitchCredentials, code, redirectURI string) (accessToken, idToken string, err error) {
	form := url.Values{}
	form.Set("client_id", creds.clientID)
	form.Set("client_secret", creds.secret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", redirectURI)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, twitchIDBase+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", errTwitchBadAnswer
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		a.warnOnce("twitch-exchange", "Twitch could not be reached to finish connecting somebody's Twitch account.")
		return "", "", errTwitchUnavailable
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		a.warnOnce("twitch-exchange", "Twitch did not finish connecting somebody's Twitch account.", "status", response.StatusCode)
		return "", "", &twitchStatusError{status: response.StatusCode}
	}

	var answer struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxTwitchAnswerBytes+1))
	if err != nil || len(body) > maxTwitchAnswerBytes || json.Unmarshal(body, &answer) != nil ||
		!twitchTokenShaped(answer.AccessToken) || answer.IDToken == "" || !strings.EqualFold(answer.TokenType, "bearer") {
		a.warnOnce("twitch-exchange", "Twitch's answer to connecting somebody's Twitch account was not the expected shape.")
		return "", "", errTwitchBadAnswer
	}

	return answer.AccessToken, answer.IDToken, nil
}

// twitchIDClaims is what VillaNET reads from an ID token: nothing else.
type twitchIDClaims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"` // a string, or an array of one
	Exp   int64           `json:"exp"`
	Nonce string          `json:"nonce"`
	Sub   string          `json:"sub"`
}

// twitchIDTokenError is why an ID token was refused: one code word, and
// nothing from the token itself, so it can go into a log line.
type twitchIDTokenError struct {
	code string
}

func (e *twitchIDTokenError) Error() string { return "the ID token was refused: " + e.code }

// checkIDToken decodes the ID token's payload and checks it belongs to this
// connection. No signature check: the token came straight from Twitch's token
// endpoint over TLS in this same request, which OpenID Connect allows in place
// of the signature. It returns the Twitch user id (sub).
//
// The nonce is what makes this worth doing. It was put in the state when the
// person pressed Continue on Twitch, so a code issued to somebody else's
// connection, leaked unspent from an address bar, fails here when anybody
// tries to redeem it with a state of their own.
func checkIDToken(idToken, clientID, nonce string, now time.Time) (string, error) {
	refuse := func(code string) (string, error) {
		return "", &twitchIDTokenError{code: code}
	}

	if len(idToken) > maxIDTokenBytes {
		return refuse("size")
	}

	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return refuse("shape")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return refuse("shape")
	}

	var claims twitchIDClaims
	if json.Unmarshal(payload, &claims) != nil {
		return refuse("shape")
	}

	if claims.Iss != twitchIssuer {
		return refuse("iss")
	}
	if !audienceIs(claims.Aud, clientID) {
		return refuse("aud")
	}
	if claims.Exp <= 0 || !time.Unix(claims.Exp, 0).After(now.Add(-twitchIDTokenLeeway)) {
		return refuse("exp")
	}
	if nonce == "" || claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return refuse("nonce")
	}
	if !twitchIDPattern.MatchString(claims.Sub) {
		return refuse("sub")
	}

	return claims.Sub, nil
}

// audienceIs is whether an ID token's aud names this application alone: the
// string itself, or an array holding it and nothing else.
func audienceIs(raw json.RawMessage, clientID string) bool {
	if clientID == "" || len(raw) == 0 {
		return false
	}

	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}

	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return len(many) == 1 && many[0] == clientID
	}

	return false
}

// whoseToken asks Helix whose a user token is: GET /helix/users with that
// token as the bearer and our Client-Id, which Helix also checks the token
// was issued to. Exactly one user must come back, and pass cleanTwitchUser.
func (a *twitchApp) whoseToken(ctx context.Context, creds twitchCredentials, userToken string) (twitchUser, error) {
	body, _, status, err := a.helixOnce(ctx, creds.clientID, userToken, "/helix/users", nil)
	if err != nil {
		return twitchUser{}, err
	}
	if status != http.StatusOK {
		a.warnOnce("twitch-helix", "Twitch would not say whose a new connection is.", "status", status)
		return twitchUser{}, &twitchStatusError{status: status}
	}

	var answer struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &answer) != nil || len(answer.Data) != 1 {
		a.warnOnce("twitch-helix", "Twitch's answer about a new connection was not the expected shape.", "reason", "users")
		return twitchUser{}, errTwitchBadAnswer
	}

	who, ok := cleanTwitchUser(answer.Data[0].ID, answer.Data[0].Login, answer.Data[0].DisplayName)
	if !ok {
		a.warnOnce("twitch-helix", "Twitch named a new connection's account in a shape Matty does not accept.", "reason", "user")
		return twitchUser{}, errTwitchBadAnswer
	}

	return who, nil
}

// revokeUserToken revokes a user token. Best effort: a failure is warned once
// and otherwise ignored, because the token is already forgotten. VillaNET
// keeps no copy of it in any case; revoking only makes sure nobody else can
// use one that escaped.
func (a *twitchApp) revokeUserToken(ctx context.Context, creds twitchCredentials, userToken string) {
	form := url.Values{}
	form.Set("client_id", creds.clientID)
	form.Set("token", userToken)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, twitchIDBase+"/oauth2/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	const message = "Twitch did not confirm revoking a connection's one-time sign-in; Matty kept no copy of it."

	response, err := sendMedia(request)
	if err != nil {
		a.warnOnce("twitch-revoke", message, "status", 0)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxTwitchAnswerBytes))

	if response.StatusCode != http.StatusOK {
		a.warnOnce("twitch-revoke", message, "status", response.StatusCode)
	}
}
