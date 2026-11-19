// Package webpush writes the message a push server carries to a phone: one
// record of Web Push's message encryption (RFC 8291, the aes128gcm content
// coding of RFC 8188), which only the phone that registered can read, and
// the VAPID header (RFC 8292) by which this server says who is sending.
//
// It uses the standard library, and pushsend's canonical form of a push
// server's host for a token's audience. It contacts nothing: sending is the
// pushsend package's.
//
// Two keys are involved and they are never the same one. The phone's key
// (its P-256 public key, "p256dh", and its 16-byte auth secret) is what a
// message is encrypted to, together with a fresh key pair and salt of this
// server's for every message. The VAPID key (Key) only signs; it never
// encrypts a message.
package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

const (
	// RecordSize is the record size every message names: the most a
	// phone's decrypter accepts, as in RFC 8291's own example.
	RecordSize = 4096

	// MaxPlaintext is the most a message may say, in bytes. Padded and
	// encrypted it stays under MaxBody, which is what a push server must
	// accept and what some keep without a word when given more.
	MaxPlaintext = 3000

	// PadStep is what the plaintext, with its delimiter, is padded up to a
	// multiple of, so that a message's size says little about its text.
	PadStep = 1024

	// MaxBody is the most a push server must carry (RFC 8030 section 7.2).
	MaxBody = 4096

	// HeaderSize is the size of the record's header: salt, record size,
	// key length and this server's one-off public key.
	HeaderSize = saltSize + 4 + 1 + publicKeySize

	// AuthSecretSize is the size of a phone's auth secret.
	AuthSecretSize = 16

	saltSize      = 16
	publicKeySize = 65
	tagSize       = 16
	delimiter     = 0x02
)

var (
	// ErrPlaintextTooLong is a message longer than MaxPlaintext.
	ErrPlaintextTooLong = errors.New("the push message is longer than 3000 bytes")

	// ErrSubscriberKey is a phone's public key that is not an uncompressed
	// P-256 point written in base64url without padding.
	ErrSubscriberKey = errors.New("the phone's push key is not a P-256 public key in base64url without padding")

	// ErrAuthSecret is an auth secret that is not 16 bytes written in
	// base64url without padding.
	ErrAuthSecret = errors.New("the phone's push secret is not 16 bytes in base64url without padding")

	// ErrUndecryptable is a body that is not one record this package's
	// Decrypt can open with the key and secret it was given.
	ErrUndecryptable = errors.New("the push message cannot be decrypted")
)

// Encrypt is plaintext encrypted to the phone whose public key (65 bytes,
// an uncompressed P-256 point) and auth secret (16 bytes) are given: one
// aes128gcm record of record size RecordSize, under a fresh key pair and
// salt, with the plaintext padded by zeros after its delimiter to the next
// multiple of PadStep. The result is at most HeaderSize + 3072 + 16 bytes.
func Encrypt(plaintext, uaPublic, authSecret []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrPlaintextTooLong
	}

	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	return encrypt(plaintext, uaPublic, authSecret, ephemeral, salt, PadStep)
}

// encrypt is Encrypt with its randomness given, and padding to multiples of
// padStep (none when padStep is 0), so that RFC 8291's example can be
// written again byte for byte.
func encrypt(plaintext, uaPublic, authSecret []byte, ephemeral *ecdh.PrivateKey, salt []byte, padStep int) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrPlaintextTooLong
	}

	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, ErrSubscriberKey
	}

	if len(authSecret) != AuthSecretSize {
		return nil, ErrAuthSecret
	}

	if len(salt) != saltSize {
		return nil, errors.New("the salt is not 16 bytes")
	}

	secret, err := ephemeral.ECDH(ua)
	if err != nil {
		return nil, ErrSubscriberKey
	}

	asPublic := ephemeral.PublicKey().Bytes()

	_, cek, nonce, err := deriveKeys(secret, authSecret, uaPublic, asPublic, salt)
	if err != nil {
		return nil, err
	}

	padded := len(plaintext) + 1
	if padStep > 0 {
		padded = (padded + padStep - 1) / padStep * padStep
	}

	record := make([]byte, padded)
	copy(record, plaintext)
	record[len(plaintext)] = delimiter

	gcm, err := newGCM(cek)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, HeaderSize+padded+tagSize)
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, RecordSize)
	body = append(body, publicKeySize)
	body = append(body, asPublic...)
	body = gcm.Seal(body, nonce, record, nil)

	return body, nil
}

// deriveKeys is RFC 8291 section 3.4 and RFC 8188 section 2.2: the input
// keying material from the shared secret and the phone's auth secret, then
// the content key and nonce from that and the salt.
func deriveKeys(ecdhSecret, authSecret, uaPublic, asPublic, salt []byte) (ikm, cek, nonce []byte, err error) {
	keyInfo := make([]byte, 0, 14+2*publicKeySize)
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)

	ikm, err = hkdf.Key(sha256.New, ecdhSecret, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, nil, nil, err
	}

	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, nil, nil, err
	}

	cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, nil, nil, err
	}

	nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, nil, nil, err
	}

	return ikm, cek, nonce, nil
}

func newGCM(cek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCM(block)
}

// Decrypt is the plaintext of a body Encrypt made, opened with the phone's
// private key and auth secret, as a phone opens it: one record of at most
// MaxBody bytes, its key 65 bytes long, its last non-zero byte the final
// record's delimiter. Tests read a message with it; the server itself
// never decrypts.
func Decrypt(body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) > MaxBody || len(body) < HeaderSize+1+tagSize || len(authSecret) != AuthSecretSize {
		return nil, ErrUndecryptable
	}

	salt := body[:saltSize]
	rs := binary.BigEndian.Uint32(body[saltSize : saltSize+4])
	idlen := body[saltSize+4]
	if idlen != publicKeySize || rs <= tagSize+1 || uint64(len(body)-HeaderSize) > uint64(rs) {
		return nil, ErrUndecryptable
	}

	asPublic := body[saltSize+5 : HeaderSize]
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		return nil, ErrUndecryptable
	}

	secret, err := uaPrivate.ECDH(as)
	if err != nil {
		return nil, ErrUndecryptable
	}

	_, cek, nonce, err := deriveKeys(secret, authSecret, uaPrivate.PublicKey().Bytes(), asPublic, salt)
	if err != nil {
		return nil, ErrUndecryptable
	}

	gcm, err := newGCM(cek)
	if err != nil {
		return nil, ErrUndecryptable
	}

	record, err := gcm.Open(nil, nonce, body[HeaderSize:], nil)
	if err != nil {
		return nil, ErrUndecryptable
	}

	end := len(bytes.TrimRight(record, "\x00"))
	if end == 0 || record[end-1] != delimiter {
		return nil, ErrUndecryptable
	}

	return record[:end-1], nil
}

// DecodeSubscriberKey is a phone's public key ("p256dh") read from
// base64url without padding: 65 bytes that crypto/ecdh's P-256 accepts as
// an uncompressed point.
func DecodeSubscriberKey(text string) ([]byte, error) {
	raw, ok := decodeBase64URL(text)
	if !ok || len(raw) != publicKeySize {
		return nil, ErrSubscriberKey
	}

	if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
		return nil, ErrSubscriberKey
	}

	return raw, nil
}

// DecodeAuthSecret is a phone's auth secret read from base64url without
// padding: exactly 16 bytes.
func DecodeAuthSecret(text string) ([]byte, error) {
	raw, ok := decodeBase64URL(text)
	if !ok || len(raw) != AuthSecretSize {
		return nil, ErrAuthSecret
	}

	return raw, nil
}

// decodeBase64URL reads base64url without padding, and only it: no '=', no
// line breaks or spaces (which Go's decoder would skip), no stray bits.
func decodeBase64URL(text string) ([]byte, bool) {
	if text == "" || len(text) > 256 {
		return nil, false
	}

	for i := range len(text) {
		c := text[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return nil, false
		}
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		return nil, false
	}

	return raw, true
}
