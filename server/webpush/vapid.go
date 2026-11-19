package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
)

const (
	// JWTLifetime is how far ahead a VAPID token's exp is set: well inside
	// the 24 hours RFC 8292 allows, so that a push server whose clock runs
	// ahead still takes it.
	JWTLifetime = 12 * time.Hour

	// JWTKept is how long a Signer uses one token for one origin before it
	// signs a new one.
	JWTKept = time.Hour

	// PublicKeyLength is the length of a VAPID public key as it is written:
	// 65 bytes in base64url without padding.
	PublicKeyLength = 87

	// maxContactBytes bounds the contact a token names.
	maxContactBytes = 256

	// maxKeptOrigins bounds a Signer's tokens; past it they are all
	// dropped and signed again as they are needed.
	maxKeptOrigins = 1024
)

// ErrVAPIDKey is stored key material that is not a P-256 private key.
var ErrVAPIDKey = errors.New("the stored push key is not a P-256 private key")

// ErrOrigin is an endpoint whose origin cannot be read.
var ErrOrigin = errors.New("the push address has no origin")

// jwtHeader is the token's header, RFC 8292 section 2: always this.
const jwtHeader = `{"typ":"JWT","alg":"ES256"}`

// Key is the server's VAPID key: an ECDSA P-256 key pair that signs the
// tokens push servers check, and nothing else. It prints as its public half
// only.
type Key struct {
	private *ecdsa.PrivateKey
	public  string
}

// GenerateKey is a new VAPID key.
func GenerateKey() (*Key, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	return newKey(private)
}

// ParseKey is the key whose private scalar Bytes wrote (32 bytes).
func ParseKey(raw []byte) (*Key, error) {
	private, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, ErrVAPIDKey
	}

	return newKey(private)
}

func newKey(private *ecdsa.PrivateKey) (*Key, error) {
	public, err := private.PublicKey.Bytes()
	if err != nil {
		return nil, ErrVAPIDKey
	}

	return &Key{private: private, public: base64.RawURLEncoding.EncodeToString(public)}, nil
}

// Bytes is the private scalar, 32 bytes, the form the key is kept in. It is
// a secret: it is never logged, shown or put in a backup.
func (k *Key) Bytes() []byte {
	raw, err := k.private.Bytes()
	if err != nil {
		// A key made by GenerateKey or ParseKey is always on P-256.
		panic("webpush: a VAPID key that is not on P-256")
	}

	return raw
}

// Public is the public half as push servers and phones are given it: the
// 65-byte uncompressed point in base64url without padding, 87 characters.
func (k *Key) Public() string {
	return k.public
}

// String is the public half only, so that a key printed by mistake shows
// nothing secret.
func (k *Key) String() string {
	return "VAPID key " + k.public
}

// GoString is String, for %#v.
func (k *Key) GoString() string {
	return k.String()
}

// Sign is a VAPID token (a JWS, ES256, RFC 8292 section 2) for the push
// server at aud (an origin), good until exp, naming the contact sub when
// ContactOK accepts it. The signature is R||S, 64 bytes, as JWS has it, not
// DER.
func (k *Key) Sign(aud string, exp time.Time, sub string) (string, error) {
	claims := struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub,omitempty"`
	}{Aud: aud, Exp: exp.Unix()}
	if ContactOK(sub) {
		claims.Sub = sub
	}

	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	input := base64.RawURLEncoding.EncodeToString([]byte(jwtHeader)) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(input))

	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", err
	}

	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])

	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// Authorization is the value of the Authorization header for a token and a
// public key: "vapid t=<token>, k=<key>" (RFC 8292 section 3).
func Authorization(token, publicKey string) string {
	return "vapid t=" + token + ", k=" + publicKey
}

// ContactOK says whether a contact may go in a token's sub: a mailto: with
// an address in it, or an https: URL with a host and no user name, in
// printable ASCII, at most 256 bytes.
func ContactOK(contact string) bool {
	if contact == "" || len(contact) > maxContactBytes {
		return false
	}

	for i := range len(contact) {
		if contact[i] <= ' ' || contact[i] >= 0x7f {
			return false
		}
	}

	if rest, ok := strings.CutPrefix(contact, "mailto:"); ok {
		at := strings.IndexByte(rest, '@')

		return at > 0 && at < len(rest)-1
	}

	if !strings.HasPrefix(contact, "https:") {
		return false
	}

	u, err := url.Parse(contact)

	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// Origin is a push endpoint's origin, which a token's aud must be: the
// scheme, and the host in the canonical form the push address is checked
// and sent in (pushsend's Endpoint.Host: lower case, no trailing dot, a
// name that is not ASCII in its xn-- form, the port only when it is not the
// scheme's own).
func Origin(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ErrOrigin
	}

	host := pushsend.HostOf(endpoint)
	if host == "" {
		return "", ErrOrigin
	}

	return u.Scheme + "://" + host, nil
}

// Signer hands out Authorization headers for one key, keeping one token per
// origin and contact for JWTKept, each good for JWTLifetime from when it
// was signed. It is safe for use by several goroutines.
type Signer struct {
	key *Key
	now func() time.Time

	mu   sync.Mutex
	kept map[string]keptToken
}

type keptToken struct {
	contact string
	header  string
	signed  time.Time
}

// NewSigner is a Signer for key, telling the time by now (time.Now when nil).
func NewSigner(key *Key, now func() time.Time) *Signer {
	if now == nil {
		now = time.Now
	}

	return &Signer{key: key, now: now, kept: map[string]keptToken{}}
}

// Key is the key the Signer signs with.
func (s *Signer) Key() *Key {
	return s.key
}

// Authorization is the Authorization header for a push to endpoint, naming
// contact when ContactOK accepts it.
func (s *Signer) Authorization(endpoint, contact string) (string, error) {
	origin, err := Origin(endpoint)
	if err != nil {
		return "", err
	}

	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if kept, ok := s.kept[origin]; ok && kept.contact == contact && !now.Before(kept.signed) && now.Sub(kept.signed) < JWTKept {
		return kept.header, nil
	}

	token, err := s.key.Sign(origin, now.Add(JWTLifetime), contact)
	if err != nil {
		return "", err
	}

	if len(s.kept) >= maxKeptOrigins {
		clear(s.kept)
	}

	header := Authorization(token, s.key.Public())
	s.kept[origin] = keptToken{contact: contact, header: header, signed: now}

	return header, nil
}
