package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testKeyScalar is a fixed VAPID key for the tests: never a real one.
var testKeyScalar = []byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

func testKey(t *testing.T) *Key {
	t.Helper()

	key, err := ParseKey(testKeyScalar)
	require.NoError(t, err)

	return key
}

// checkToken is what a push server does with a token (RFC 8292 section 2
// and RFC 7515): its three parts, its header, its claims, and its ES256
// signature, R||S, checked against the public key from the header's k.
func checkToken(t *testing.T, token, publicKey string) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"typ":"JWT","alg":"ES256"}`, string(header))

	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(rawClaims, &claims))

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	require.Len(t, signature, 64, "R||S, not DER")

	point, err := base64.RawURLEncoding.DecodeString(publicKey)
	require.NoError(t, err)
	public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	require.NoError(t, err)

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	require.True(t, ecdsa.Verify(public, digest[:], r, s), "the signature checks out")

	return claims
}

func TestATokenIsSignedAsPushServersCheckIt(t *testing.T) {
	key := testKey(t)
	exp := time.Unix(1_900_000_000, 0)

	token, err := key.Sign("https://push.example.com", exp, "mailto:admin@example.com")
	require.NoError(t, err)

	claims := checkToken(t, token, key.Public())
	require.Equal(t, map[string]any{"aud": "https://push.example.com", "exp": float64(1_900_000_000), "sub": "mailto:admin@example.com"}, claims)

	// Another key's public half does not verify it.
	other, err := GenerateKey()
	require.NoError(t, err)
	parts := strings.Split(token, ".")
	point, _ := base64.RawURLEncoding.DecodeString(other.Public())
	public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	require.NoError(t, err)
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.False(t, ecdsa.Verify(public, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])))
}

func TestATokenLeavesOutAContactThatIsNotOne(t *testing.T) {
	key := testKey(t)

	for _, sub := range []string{"", "admin@example.com", "http://example.com", "mailto:", "mailto:@x", "https://", "mailto:a b@example.com"} {
		token, err := key.Sign("https://push.example.com", time.Unix(1_900_000_000, 0), sub)
		require.NoError(t, err)

		claims := checkToken(t, token, key.Public())
		_, has := claims["sub"]
		require.False(t, has, "%q", sub)
	}
}

func TestContactOK(t *testing.T) {
	for _, good := range []string{"mailto:admin@example.com", "https://example.com", "https://example.com/contact"} {
		require.True(t, ContactOK(good), good)
	}

	for _, bad := range []string{"", "mailto:", "mailto:admin", "mailto:admin@", "https:", "https://user@example.com", "http://example.com", "ftp://x", "mailto:é@example.com", "https://example.com/\t", "mailto:" + strings.Repeat("a", 250) + "@example.com"} {
		require.False(t, ContactOK(bad), bad)
	}
}

func TestAKeyIsWrittenAndReadBack(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	require.Len(t, key.Public(), PublicKeyLength)
	point, err := base64.RawURLEncoding.DecodeString(key.Public())
	require.NoError(t, err)
	require.Len(t, point, 65)
	require.Equal(t, byte(0x04), point[0])

	raw := key.Bytes()
	require.Len(t, raw, 32)

	again, err := ParseKey(raw)
	require.NoError(t, err)
	require.Equal(t, key.Public(), again.Public())

	_, err = ParseKey(raw[:31])
	require.ErrorIs(t, err, ErrVAPIDKey)
	_, err = ParseKey(make([]byte, 32))
	require.ErrorIs(t, err, ErrVAPIDKey)
}

func TestAKeyPrintsOnlyItsPublicHalf(t *testing.T) {
	key := testKey(t)
	secret := fmt.Sprintf("%x", key.Bytes())

	for _, printed := range []string{fmt.Sprint(key), fmt.Sprintf("%v", key), fmt.Sprintf("%+v", key), fmt.Sprintf("%#v", key), fmt.Sprintf("%s", key)} {
		require.NotContains(t, printed, secret)
		require.Contains(t, printed, key.Public())
	}
}

func TestOrigin(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://ntfy.sh/upAbc?up=1":           "https://ntfy.sh",
		"https://Push.Example.COM:443/x":       "https://push.example.com",
		"https://push.example.com:8443/x":      "https://push.example.com:8443",
		"http://localhost:7788/push/abc":       "http://localhost:7788",
		"http://localhost:80/push/abc":         "http://localhost",
		"https://[2001:DB8::1]:443/push":       "https://[2001:db8::1]",
		"https://[2001:db8::1]:4430/push":      "https://[2001:db8::1]:4430",
		"https://push.example.com:4430/x?y=1#": "https://push.example.com:4430",
		"https://push.example.com./x":          "https://push.example.com",
		"https://push.example.com:0443/x":      "https://push.example.com",
		"https://push.example.com:08443/x":     "https://push.example.com:8443",
		"http://localhost:080/push/abc":        "http://localhost",
		"https://b%C3%BCcher.example/x":        "https://xn--bcher-kva.example",
	} {
		got, err := Origin(endpoint)
		require.NoError(t, err, endpoint)
		require.Equal(t, want, got, endpoint)
	}

	for _, bad := range []string{"", "/push/abc", "ftp://example.com/x", "https:///x", "mailto:a@b", "https://127.1/x", "https://push.example.com:99999/x"} {
		_, err := Origin(bad)
		require.ErrorIs(t, err, ErrOrigin, bad)
	}
}

func TestTheSignerKeepsOneTokenPerOriginForAnHour(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	signer := NewSigner(key, func() time.Time { return now })
	require.Same(t, key, signer.Key())

	first, err := signer.Authorization("https://push.example.com/a?x=1", "mailto:admin@example.com")
	require.NoError(t, err)

	token, publicKey, ok := parseHeader(first)
	require.True(t, ok, first)
	require.Equal(t, key.Public(), publicKey)
	claims := checkToken(t, token, publicKey)
	require.Equal(t, "https://push.example.com", claims["aud"])
	require.Equal(t, float64(now.Add(12*time.Hour).Unix()), claims["exp"])
	require.Equal(t, "mailto:admin@example.com", claims["sub"])

	// The same origin, another path: the same header.
	now = now.Add(59 * time.Minute)
	again, err := signer.Authorization("https://push.example.com/b", "mailto:admin@example.com")
	require.NoError(t, err)
	require.Equal(t, first, again)

	// Another contact: signed afresh.
	other, err := signer.Authorization("https://push.example.com/b", "")
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	token, _, _ = parseHeader(other)
	_, hasSub := checkToken(t, token, key.Public())["sub"]
	require.False(t, hasSub)

	// Another origin: its own aud.
	elsewhere, err := signer.Authorization("http://localhost:7788/push/1", "")
	require.NoError(t, err)
	token, _, _ = parseHeader(elsewhere)
	require.Equal(t, "http://localhost:7788", checkToken(t, token, key.Public())["aud"])

	// The same origin spelt with a trailing dot and a padded port: the
	// canonical aud.
	spelt, err := signer.Authorization("https://Push.Example.com.:0443/c", "")
	require.NoError(t, err)
	token, _, _ = parseHeader(spelt)
	require.Equal(t, "https://push.example.com", checkToken(t, token, key.Public())["aud"])

	// An hour after that token was signed: signed afresh, its exp 12 hours
	// from now.
	now = now.Add(time.Hour)
	later, err := signer.Authorization("https://push.example.com/a", "")
	require.NoError(t, err)
	require.NotEqual(t, other, later)
	token, _, _ = parseHeader(later)
	require.Equal(t, float64(now.Add(12*time.Hour).Unix()), checkToken(t, token, key.Public())["exp"])

	// A clock that went back: signed afresh rather than trusted.
	now = now.Add(-2 * time.Hour)
	back, err := signer.Authorization("https://push.example.com/a", "")
	require.NoError(t, err)
	require.NotEqual(t, later, back)

	_, err = signer.Authorization("not a url", "")
	require.ErrorIs(t, err, ErrOrigin)
}

func TestTheSignerKeepsABoundedNumberOfTokens(t *testing.T) {
	signer := NewSigner(testKey(t), nil)

	for i := range maxKeptOrigins + 10 {
		_, err := signer.Authorization(fmt.Sprintf("https://push%d.example.com/x", i), "")
		require.NoError(t, err)
	}

	signer.mu.Lock()
	defer signer.mu.Unlock()
	require.LessOrEqual(t, len(signer.kept), maxKeptOrigins)
}

// parseHeader reads "vapid t=<token>, k=<key>" as a push server does.
func parseHeader(header string) (token, key string, ok bool) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return "", "", false
	}

	for part := range strings.SplitSeq(rest, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			return "", "", false
		}

		switch name {
		case "t":
			token = value
		case "k":
			key = value
		}
	}

	return token, key, token != "" && key != ""
}

func TestAuthorization(t *testing.T) {
	require.Equal(t, "vapid t=a.b.c, k=KEY", Authorization("a.b.c", "KEY"))
}
