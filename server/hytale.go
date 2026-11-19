package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Connect Hytale (design/3.4.0.md §4): Hytale's third-party sign-in is OpenID
// Connect, and it gives a person's identity only. With the scope
// hytale:profile the person picks one of their game profiles, and Hytale says
// its username and uuid; nothing about whether they are playing, nor where.
// So what VillaNET shows is the username, for friends to find each other
// in-game, and nothing else.
//
// The authorization code flow, with:
//
//   - PKCE S256, which Hytale requires of every client, confidential ones
//     included. The verifier is kept in the state on the server and never
//     leaves it, so a code that leaks (a proxy's access log records the whole
//     callback address) is useless to anybody else.
//   - HTTP Basic client authentication, with the Client ID and secret each
//     URL-escaped before they are joined, as the OAuth standard says and
//     Hytale's issuer expects. Hytale rejects a secret in the body, so the
//     body holds nothing secret at all, and a bare SetBasicAuth, which
//     escapes nothing, would break on a secret holding a colon or a percent.
//   - An ID token checked without its signature: it comes straight from the
//     token endpoint over TLS in this same request, which OpenID Connect
//     allows in place of the signature, as Twitch's does. Its issuer,
//     audience, expiry and nonce are checked, the nonce binding the code to
//     this state a second time.
//   - Userinfo, whose sub must be the ID token's, or nothing it says may be
//     used. Then sub is dropped: it is an identifier for this application
//     alone, and nothing needs it.
//
// The token is revoked as soon as the username is read, whatever happens, and
// VillaNET never asks for a refresh token (no offline scope): keeping one would
// break the promise that it keeps no Hytale token. The cost is that a renamed
// profile shows its old name until its owner connects again.

// Where Hytale's issuer lives. A variable only so tests can point it at a
// fake. The second discovery document at oauth.accounts.hytale.com belongs to
// Hytale's own game-server client and is never used.
var hytaleBase = "https://connect.accounts.hytale.com"

const (
	// The issuer an ID token must name: hytaleBase as Hytale publishes it,
	// a constant of its own so that a fake host never changes what is checked.
	hytaleIssuer = "https://connect.accounts.hytale.com"

	// Exactly the two scopes the application is granted. Never offline,
	// offline_access or account:*.
	hytaleScope = "openid hytale:profile"

	// The revoke runs on a budget of its own, so a slow page still revokes.
	hytaleRevokeBudget = 8 * time.Second

	hytaleIDTokenLeeway = 5 * time.Minute

	maxHytaleCodeBytes    = 512
	maxHytaleAnswerBytes  = 64 << 10
	maxHytaleTokenBytes   = 4096
	maxHytaleIDTokenBytes = 8 << 10
	maxHytaleSubBytes     = 255

	// What an admin's /me and card are told about the last sign-in: the
	// application's credentials refused, or the profile scope not granted.
	hytaleProblemCredentials = "credentials"
	hytaleProblemScope       = "scope"
)

var (
	hytaleUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// Looser than the 3 to 16 letters, digits and underscores people report,
	// so a rule change at Hytale does not lock anybody out.
	hytaleUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
)

// hytaleOn: the switch is on and the application's two halves are set.
// Everything that contacts Hytale, shows Hytale names or offers Connect
// Hytale asks this first; taking things away does not.
func (c *configuration) hytaleOn() bool {
	return c.EnableHytale && c.HytaleClientID != "" && c.HytaleClientSecret != ""
}

// hytaleCredentials are the application's two halves, when both can travel
// in a header.
func (c *configuration) hytaleCredentials() (clientID, secret string, ok bool) {
	if !accountCredentialShaped(c.HytaleClientID) || !accountCredentialShaped(c.HytaleClientSecret) {
		return "", "", false
	}

	return c.HytaleClientID, c.HytaleClientSecret, true
}

// hytaleCallbackURL is the Redirect URI, exactly as the admin registers it
// with Hytale, which matches it byte for byte. It is sent the same in the
// authorize request and the exchange.
func hytaleCallbackURL(siteURL string) (address, problem string) {
	return accountCallbackURL(siteURL, hytaleCallbackPath, "Hytale",
		"Hytale needs an https:// Redirect URI, and the Site URL is ")
}

// hytaleSetup is "" when people can connect Hytale, otherwise the sentence an
// admin is shown.
func hytaleSetup(p *Plugin, cfg *configuration) string {
	if _, problem := hytaleCallbackURL(p.siteURL()); problem != "" {
		return problem
	}
	if cfg.HytaleClientID == "" || cfg.HytaleClientSecret == "" {
		return "Fill in the Client ID and Client Secret above first."
	}
	if _, _, ok := cfg.hytaleCredentials(); !ok {
		return "The Hytale Client ID or Client Secret has characters they cannot have."
	}

	return ""
}

// hytaleQueryEscape is url.QueryEscape with a space as %20, so the scope reads
// openid%20hytale%3Aprofile. A + in a value is escaped first, as %2B, so
// turning the escaped + back into %20 never touches one.
func hytaleQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// hytalePKCE is a code verifier, base64url of 32 random bytes (43
// characters), and its S256 challenge.
func hytalePKCE() (verifier, challenge string, err error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", err
	}

	verifier = base64.RawURLEncoding.EncodeToString(buffer)

	return verifier, hytaleChallenge(verifier), nil
}

func hytaleChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// hytaleStart is where Continue on Hytale goes (§4.3): Hytale's authorize
// endpoint, with the parameters in a fixed order so the address is always the
// same string for the same values. No prompt (Hytale asks every time) and no
// response_mode: the default for a code is the query, a GET, which both the
// browser's session cookie and Matty's window need. The server asks Hytale
// nothing here.
func hytaleStart(p *Plugin, cfg *configuration, state string, st *accountState) (string, error) {
	redirectURI, problem := hytaleCallbackURL(p.siteURL())
	if problem != "" {
		return "", errors.New("no Redirect URI for Hytale")
	}

	clientID, _, ok := cfg.hytaleCredentials()
	if !ok {
		return "", errors.New("no Hytale application")
	}

	nonce, err := randomHex(32)
	if err != nil {
		return "", err
	}
	verifier, challenge, err := hytalePKCE()
	if err != nil {
		return "", err
	}

	st.Return, st.Nonce, st.Verifier = redirectURI, nonce, verifier

	var query strings.Builder
	for i, pair := range [][2]string{
		{"response_type", "code"},
		{"client_id", clientID},
		{"redirect_uri", redirectURI},
		{"scope", hytaleScope},
		{"state", state},
		{"nonce", nonce},
		{"code_challenge", challenge},
		{"code_challenge_method", "S256"},
	} {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(hytaleQueryEscape(pair[0]))
		query.WriteByte('=')
		query.WriteString(hytaleQueryEscape(pair[1]))
	}

	return hytaleBase + "/oauth2/auth?" + query.String(), nil
}

// hytaleDeclined is Y1, the "came back without an account" check. Whoever
// completes a callback chooses error, so it reaches the log only when it is
// one of the plain code words OAuth uses; error_description is never read.
func hytaleDeclined(query url.Values) *accountRefusal {
	errs, present := query["error"]
	if !present {
		return nil
	}

	word := ""
	if len(errs) > 0 {
		word = errs[0]
	}

	switch word {
	case "access_denied":
		return accountRefused(accountPageCancelled, "")
	case "invalid_scope":
		refusal := accountRefused(accountPageScope, "invalid_scope")
		refusal.problem, refusal.signIn = accountScopeProblem, hytaleProblemScope

		return refusal
	}

	logged := "other"
	if len(errs) == 1 && accountErrorWord.MatchString(word) {
		logged = word
	}

	return accountRefused(accountPageProviderError, "hytale_error", "error", logged)
}

// hytaleFinish proves the account Hytale sent back (§4.4, Y1-Y7).
func hytaleFinish(ctx context.Context, p *Plugin, cfg *configuration, _ string, st accountState, query url.Values, now time.Time) (accountProof, *accountRefusal) {
	if refusal := hytaleDeclined(query); refusal != nil {
		return accountProof{}, refusal
	}

	codes := query["code"]
	if len(codes) != 1 || codes[0] == "" || len(codes[0]) > maxHytaleCodeBytes {
		return accountProof{}, accountRefused(accountPageExpired, "code")
	}

	clientID, secret, ok := cfg.hytaleCredentials()
	if !ok || st.Return == "" || st.Verifier == "" || st.Nonce == "" {
		refusal := accountRefused(accountPageSetup, "")
		refusal.problem = hytaleSetup(p, cfg)

		return accountProof{}, refusal
	}

	token, idToken, refusal := p.hytaleExchange(ctx, clientID, secret, codes[0], st.Return, st.Verifier)
	if refusal != nil {
		return accountProof{}, refusal
	}

	// Whatever happens next, the token is revoked, on a budget of its own,
	// and it is never kept, logged or shown.
	defer p.hytaleRevoke(clientID, secret, token)

	sub, err := checkHytaleIDToken(idToken, clientID, st.Nonce, now)
	if err != nil {
		reason := "shape"
		var refused *hytaleIDTokenError
		if errors.As(err, &refused) {
			reason = refused.code
		}

		return accountProof{}, accountRefused(accountPageProviderError, "id_token", "check", reason)
	}

	info, refusal := p.hytaleUserinfo(ctx, token)
	if refusal != nil {
		return accountProof{}, refusal
	}

	// Userinfo's values may be used only when it is about the same person
	// as the ID token.
	if subtle.ConstantTimeCompare([]byte(info.Sub), []byte(sub)) != 1 {
		return accountProof{}, accountRefused(accountPageProviderError, "sub")
	}
	if info.Profile == nil {
		return accountProof{}, accountRefused(accountPageNoProfile, "no_profile")
	}
	if !hytaleUUIDPattern.MatchString(info.Profile.UUID) || !hytaleUsernamePattern.MatchString(info.Profile.Username) {
		return accountProof{}, accountRefused(accountPageProviderError, "profile")
	}

	// sub goes here: it names the person for this application alone, and
	// nothing needs it.
	return accountProof{ID: strings.ToLower(info.Profile.UUID), Name: info.Profile.Username}, nil
}

// hytaleBasic is the Authorization header for the application: Basic, with
// the id and the secret each URL-escaped before they are joined.
func hytaleBasic(clientID, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(clientID)+":"+url.QueryEscape(secret)))
}

// hytaleForm is a form body in a fixed order.
func hytaleForm(pairs ...[2]string) string {
	var body strings.Builder
	for i, pair := range pairs {
		if i > 0 {
			body.WriteByte('&')
		}
		body.WriteString(url.QueryEscape(pair[0]))
		body.WriteByte('=')
		body.WriteString(url.QueryEscape(pair[1]))
	}

	return body.String()
}

// hytaleExchange is Y3: the code for a token and an ID token. The body holds
// nothing secret, no client_id and no client_secret: the application signs in
// with the Basic header. A refresh token, should Hytale ever send one, is not
// a field of the answer below, so it is dropped where the answer is read.
func (p *Plugin) hytaleExchange(ctx context.Context, clientID, secret, code, redirectURI, verifier string) (token, idToken string, refusal *accountRefusal) {
	body := hytaleForm(
		[2]string{"grant_type", "authorization_code"},
		[2]string{"code", code},
		[2]string{"redirect_uri", redirectURI},
		[2]string{"code_verifier", verifier},
	)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hytaleBase+"/oauth2/token", strings.NewReader(body))
	if err != nil {
		return "", "", accountRefused(accountPageProviderError, "unreachable")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", hytaleBasic(clientID, secret))

	response, err := sendMedia(request)
	if err != nil {
		return "", "", accountRefused(accountPageProviderError, "unreachable")
	}
	defer response.Body.Close()

	answer, err := io.ReadAll(io.LimitReader(response.Body, maxHytaleAnswerBytes+1))
	if err != nil || len(answer) > maxHytaleAnswerBytes {
		return "", "", accountRefused(accountPageProviderError, "answer")
	}

	if response.StatusCode != http.StatusOK {
		var refused struct {
			Error string `json:"error"`
		}
		if response.StatusCode == http.StatusUnauthorized && json.Unmarshal(answer, &refused) == nil && refused.Error == "invalid_client" {
			refusal := accountRefused(accountPageProviderError, "invalid_client", "status", response.StatusCode)
			refusal.signIn = hytaleProblemCredentials

			return "", "", refusal
		}

		return "", "", accountRefused(accountPageProviderError, "status", "status", response.StatusCode)
	}

	var tokens struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}
	if json.Unmarshal(answer, &tokens) != nil || !hytaleTokenShaped(tokens.AccessToken) ||
		tokens.IDToken == "" || len(tokens.IDToken) > maxHytaleIDTokenBytes || !strings.EqualFold(tokens.TokenType, "bearer") {
		return "", "", accountRefused(accountPageProviderError, "answer")
	}

	return tokens.AccessToken, tokens.IDToken, nil
}

// hytaleTokenShaped is what an access token looks like: printable ASCII
// without spaces, at most 4096 bytes. Anything else never goes into an
// Authorization header.
func hytaleTokenShaped(token string) bool {
	if token == "" || len(token) > maxHytaleTokenBytes {
		return false
	}

	for i := 0; i < len(token); i++ {
		if token[i] <= ' ' || token[i] > '~' {
			return false
		}
	}

	return true
}

// hytaleRevoke is Y4. Best effort: a failure is warned once and otherwise
// ignored, because VillaNET kept no copy of the token in any case; revoking
// only makes sure nobody else can use one that escaped.
func (p *Plugin) hytaleRevoke(clientID, secret, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), hytaleRevokeBudget)
	defer cancel()

	const message = "Hytale did not confirm revoking a connection's one-time sign-in; Matty kept no copy of it."

	body := hytaleForm([2]string{"token", token}, [2]string{"token_type_hint", "access_token"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hytaleBase+"/oauth2/revoke", strings.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", hytaleBasic(clientID, secret))

	response, err := sendMedia(request)
	if err != nil {
		p.accounts().warnOnce("hytale-revoke", message, "status", 0)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxHytaleAnswerBytes))

	if response.StatusCode != http.StatusOK {
		p.accounts().warnOnce("hytale-revoke", message, "status", response.StatusCode)
	}
}

// hytaleIDClaims is what VillaNET reads from an ID token: nothing else.
type hytaleIDClaims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"` // a string, or an array of one
	Exp   int64           `json:"exp"`
	Nonce string          `json:"nonce"`
	Sub   *string         `json:"sub"`
}

// hytaleIDTokenError is why an ID token was refused: one code word, and
// nothing from the token itself, so it can go into a log line.
type hytaleIDTokenError struct {
	code string
}

func (e *hytaleIDTokenError) Error() string { return "the ID token was refused: " + e.code }

// checkHytaleIDToken is Y5: the ID token's payload belongs to this connection.
// It returns sub. No signature check (see the top of this file), and no shape
// is assumed for sub: Hytale documents none, and a guessed pattern would fail
// every connect closed. It only has to be printable, of a sane length, and
// what userinfo says next.
func checkHytaleIDToken(idToken, clientID, nonce string, now time.Time) (string, error) {
	refuse := func(code string) (string, error) {
		return "", &hytaleIDTokenError{code: code}
	}

	if len(idToken) > maxHytaleIDTokenBytes {
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

	var claims hytaleIDClaims
	if json.Unmarshal(payload, &claims) != nil {
		return refuse("shape")
	}

	if claims.Iss != hytaleIssuer {
		return refuse("iss")
	}
	if !audienceIs(claims.Aud, clientID) {
		return refuse("aud")
	}
	if claims.Exp <= 0 || !time.Unix(claims.Exp, 0).After(now.Add(-hytaleIDTokenLeeway)) {
		return refuse("exp")
	}
	if nonce == "" || claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return refuse("nonce")
	}
	if claims.Sub == nil || !hytaleSubShaped(*claims.Sub) {
		return refuse("sub")
	}

	return *claims.Sub, nil
}

// hytaleSubShaped is 1 to 255 bytes of printable ASCII.
func hytaleSubShaped(sub string) bool {
	if sub == "" || len(sub) > maxHytaleSubBytes {
		return false
	}

	for i := 0; i < len(sub); i++ {
		if sub[i] < ' ' || sub[i] > '~' {
			return false
		}
	}

	return true
}

// hytaleInfo is what userinfo says: whom the token is for, and the profile
// they picked, if any.
type hytaleInfo struct {
	Sub     string `json:"sub"`
	Profile *struct {
		UUID     string `json:"uuid"`
		Username string `json:"username"`
	} `json:"profile"`
}

// hytaleUserinfo is Y6's request. A signed answer (application/jwt) is
// refused: only plain JSON is read.
func (p *Plugin) hytaleUserinfo(ctx context.Context, token string) (hytaleInfo, *accountRefusal) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hytaleBase+"/userinfo", nil)
	if err != nil {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "unreachable")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")

	response, err := sendMedia(request)
	if err != nil {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "unreachable")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "userinfo", "status", response.StatusCode)
	}

	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "userinfo")
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxHytaleAnswerBytes+1))
	if err != nil || len(body) > maxHytaleAnswerBytes {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "userinfo")
	}

	var info hytaleInfo
	if json.Unmarshal(body, &info) != nil {
		return hytaleInfo{}, accountRefused(accountPageProviderError, "userinfo")
	}

	return info, nil
}
