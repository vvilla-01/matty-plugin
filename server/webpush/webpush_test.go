package webpush

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// RFC 8291 section 5 and appendix A, with the RFC's line breaks and spaces
// taken out.
const (
	rfcPlaintext  = "When I grow up, I want to be a watermelon"
	rfcAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcUAPrivate  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublic   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcASPrivate  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcASPublic   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcSalt       = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcECDH       = "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"
	rfcIKM        = "S4lYMb_L0FxCeq0WhDx813KgSYqU26kOyzWUdsXYyrg"
	rfcCEK        = "oIhVW04MRdy2XN9CiKLxTg"
	rfcNonce      = "4h_95klXJ5E_qnoN"
	rfcHeader     = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcBody       = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(s)
	require.NoError(t, err)

	return raw
}

func p256Private(t *testing.T, s string) *ecdh.PrivateKey {
	t.Helper()

	key, err := ecdh.P256().NewPrivateKey(b64(t, s))
	require.NoError(t, err)

	return key
}

func TestTheRFCExampleIsWrittenByteForByte(t *testing.T) {
	as := p256Private(t, rfcASPrivate)
	require.Equal(t, b64(t, rfcASPublic), as.PublicKey().Bytes())
	require.Equal(t, b64(t, rfcUAPublic), p256Private(t, rfcUAPrivate).PublicKey().Bytes())

	body, err := encrypt([]byte(rfcPlaintext), b64(t, rfcUAPublic), b64(t, rfcAuthSecret), as, b64(t, rfcSalt), 0)
	require.NoError(t, err)
	require.Equal(t, rfcBody, base64.RawURLEncoding.EncodeToString(body))
	require.Equal(t, rfcHeader, base64.RawURLEncoding.EncodeToString(body[:HeaderSize]))
}

func TestTheRFCExampleIntermediateValues(t *testing.T) {
	as := p256Private(t, rfcASPrivate)
	ua, err := ecdh.P256().NewPublicKey(b64(t, rfcUAPublic))
	require.NoError(t, err)

	secret, err := as.ECDH(ua)
	require.NoError(t, err)
	require.Equal(t, rfcECDH, base64.RawURLEncoding.EncodeToString(secret))

	ikm, cek, nonce, err := deriveKeys(secret, b64(t, rfcAuthSecret), b64(t, rfcUAPublic), b64(t, rfcASPublic), b64(t, rfcSalt))
	require.NoError(t, err)
	require.Equal(t, rfcIKM, base64.RawURLEncoding.EncodeToString(ikm))
	require.Equal(t, rfcCEK, base64.RawURLEncoding.EncodeToString(cek))
	require.Equal(t, rfcNonce, base64.RawURLEncoding.EncodeToString(nonce))
}

func TestTheRFCExampleDecrypts(t *testing.T) {
	plain, err := Decrypt(b64(t, rfcBody), p256Private(t, rfcUAPrivate), b64(t, rfcAuthSecret))
	require.NoError(t, err)
	require.Equal(t, rfcPlaintext, string(plain))
}

func TestEncryptPadsToStepsOf1024AndDecrypts(t *testing.T) {
	ua := p256Private(t, rfcUAPrivate)
	auth := b64(t, rfcAuthSecret)

	for _, size := range []int{0, 1, 41, 1022, 1023, 1024, 2047, 2048, 2999, MaxPlaintext} {
		plain := bytes.Repeat([]byte{'a'}, size)

		body, err := Encrypt(plain, ua.PublicKey().Bytes(), auth)
		require.NoError(t, err)

		padded := (size + 1 + PadStep - 1) / PadStep * PadStep
		require.Equal(t, HeaderSize+padded+tagSize, len(body), "size %d", size)
		require.LessOrEqual(t, len(body), MaxBody)
		require.Equal(t, uint32(RecordSize), binary.BigEndian.Uint32(body[16:20]))
		require.Equal(t, byte(65), body[20])

		got, err := Decrypt(body, ua, auth)
		require.NoError(t, err, "size %d", size)
		require.Equal(t, plain, got)
	}
}

func TestEncryptUsesAFreshKeyAndSaltEachTime(t *testing.T) {
	ua := p256Private(t, rfcUAPrivate)

	one, err := Encrypt([]byte("hello"), ua.PublicKey().Bytes(), b64(t, rfcAuthSecret))
	require.NoError(t, err)
	two, err := Encrypt([]byte("hello"), ua.PublicKey().Bytes(), b64(t, rfcAuthSecret))
	require.NoError(t, err)

	require.NotEqual(t, one[:16], two[:16], "the salt")
	require.NotEqual(t, one[21:HeaderSize], two[21:HeaderSize], "the one-off key")
}

func TestEncryptRefuses(t *testing.T) {
	ua := p256Private(t, rfcUAPrivate).PublicKey().Bytes()
	auth := b64(t, rfcAuthSecret)

	_, err := Encrypt(make([]byte, MaxPlaintext+1), ua, auth)
	require.ErrorIs(t, err, ErrPlaintextTooLong)

	_, err = Encrypt([]byte("x"), ua[:33], auth)
	require.ErrorIs(t, err, ErrSubscriberKey)

	offCurve := bytes.Clone(ua)
	offCurve[64] ^= 1
	_, err = Encrypt([]byte("x"), offCurve, auth)
	require.ErrorIs(t, err, ErrSubscriberKey)

	_, err = Encrypt([]byte("x"), ua, auth[:15])
	require.ErrorIs(t, err, ErrAuthSecret)
}

func TestDecryptRefusesWhatAPhoneRefuses(t *testing.T) {
	ua := p256Private(t, rfcUAPrivate)
	auth := b64(t, rfcAuthSecret)
	body := b64(t, rfcBody)

	tampered := bytes.Clone(body)
	tampered[len(tampered)-1] ^= 1
	_, err := Decrypt(tampered, ua, auth)
	require.ErrorIs(t, err, ErrUndecryptable)

	wrongAuth := bytes.Clone(auth)
	wrongAuth[0] ^= 1
	_, err = Decrypt(body, ua, wrongAuth)
	require.ErrorIs(t, err, ErrUndecryptable)

	badIDLen := bytes.Clone(body)
	badIDLen[20] = 64
	_, err = Decrypt(badIDLen, ua, auth)
	require.ErrorIs(t, err, ErrUndecryptable)

	_, err = Decrypt(body[:HeaderSize+16], ua, auth)
	require.ErrorIs(t, err, ErrUndecryptable)

	_, err = Decrypt(make([]byte, MaxBody+1), ua, auth)
	require.ErrorIs(t, err, ErrUndecryptable)

	// A last record must end in 0x02: the RFC's example sealed again with
	// 0x01 (a record that is not the last) is refused.
	header := body[:HeaderSize]
	_, cek, nonce, err := deriveKeys(b64(t, rfcECDH), auth, b64(t, rfcUAPublic), b64(t, rfcASPublic), b64(t, rfcSalt))
	require.NoError(t, err)
	gcm, err := newGCM(cek)
	require.NoError(t, err)
	notLast := gcm.Seal(bytes.Clone(header), nonce, append([]byte(rfcPlaintext), 0x01), nil)
	_, err = Decrypt(notLast, ua, auth)
	require.ErrorIs(t, err, ErrUndecryptable)

	sealedAgain := gcm.Seal(bytes.Clone(header), nonce, append([]byte(rfcPlaintext), 0x02, 0, 0), nil)
	plain, err := Decrypt(sealedAgain, ua, auth)
	require.NoError(t, err)
	require.Equal(t, rfcPlaintext, string(plain))
}

func TestDecodeSubscriberKey(t *testing.T) {
	raw, err := DecodeSubscriberKey(rfcUAPublic)
	require.NoError(t, err)
	require.Equal(t, b64(t, rfcUAPublic), raw)

	for _, bad := range []string{
		"",
		rfcUAPublic + "=",
		rfcUAPublic[:40] + "\n" + rfcUAPublic[40:],
		rfcUAPublic[:40] + " " + rfcUAPublic[40:],
		strings.ReplaceAll(strings.ReplaceAll(rfcUAPublic, "-", "+"), "_", "/"),
		rfcUAPublic[:86],
		"A" + rfcUAPublic[1:], // 0x00, not an uncompressed point
		base64.RawURLEncoding.EncodeToString(make([]byte, 65)), // not on the curve
		rfcAuthSecret,
	} {
		_, err := DecodeSubscriberKey(bad)
		require.ErrorIs(t, err, ErrSubscriberKey, "%q", bad)
	}
}

func TestDecodeAuthSecret(t *testing.T) {
	raw, err := DecodeAuthSecret(rfcAuthSecret)
	require.NoError(t, err)
	require.Len(t, raw, 16)

	for _, bad := range []string{"", rfcAuthSecret + "==", rfcAuthSecret[:21], rfcAuthSecret + "AA", rfcAuthSecret[:21] + "h"} {
		_, err := DecodeAuthSecret(bad)
		require.ErrorIs(t, err, ErrAuthSecret, "%q", bad)
	}
}
