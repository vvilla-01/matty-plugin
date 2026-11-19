// Package appchecktest makes key attestations for tests: certificate chains
// shaped like a phone's, under a root made here, carrying whatever a test
// asks. Only tests import it; the plugin never does.
package appchecktest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"math/big"
	"time"
)

// Phone is what an attestation says. The zero value is not useful: start
// from Intact.
type Phone struct {
	Version     int64
	Level       int // 0 software, 1 TEE, 2 StrongBox
	Package     string
	VersionCode int64
	Digest      string // hex SHA-256 of the signing certificate
	Locked      bool
	BootState   int    // 0 Verified, 1 SelfSigned, 2 Unverified, 3 Failed
	BootKey     string // hex verified-boot key digest; 32 zero bytes when empty
}

// Intact is the official Matty on a locked phone with a verified system.
func Intact(digest string) Phone {
	return Phone{Version: 400, Level: 1, Package: "ceo.villa.matty", VersionCode: 28, Digest: digest, Locked: true}
}

// Root is a test root and the keys under it.
type Root struct {
	keys []*ecdsa.PrivateKey
	cert *x509.Certificate
}

// NewRoot makes a root, and the keys a chain under it uses.
func NewRoot() *Root {
	r := &Root{}
	for range 3 {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		r.keys = append(r.keys, key)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x77), Subject: pkix.Name{CommonName: "Test Attestation Root"},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign,
	}
	r.cert = issue(template, template, &r.keys[2].PublicKey, r.keys[2])

	return r
}

// Key is the root's SubjectPublicKeyInfo SHA-256, as a policy pins it.
func (r *Root) Key() string {
	sum := sha256.Sum256(r.cert.RawSubjectPublicKeyInfo)

	return hex.EncodeToString(sum[:])
}

// Chain is the attestation of a key made for challenge on phone, leaf
// first: the attested key's certificate, an intermediate, and the root.
func (r *Root) Chain(phone Phone, challenge []byte) [][]byte {
	intermediate := issue(&x509.Certificate{
		SerialNumber: big.NewInt(0x2a), Subject: pkix.Name{CommonName: "c0ffee", Organization: []string{"TEE"}},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2039, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign,
	}, r.cert, &r.keys[1].PublicKey, r.keys[2])

	leaf := issue(&x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Date(2048, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage: x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{
			Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17},
			Value: phone.attestation(challenge),
		}},
	}, intermediate, &r.keys[0].PublicKey, r.keys[1])

	return [][]byte{leaf.Raw, intermediate.Raw, r.cert.Raw}
}

func issue(template, parent *x509.Certificate, public *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	der, err := x509.CreateCertificate(rand.Reader, template, parent, public, signer)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}

	return cert
}

func (phone Phone) attestation(challenge []byte) []byte {
	digest, err := hex.DecodeString(phone.Digest)
	if err != nil {
		panic(err)
	}

	applicationID := sequence(
		set(sequence(marshal([]byte(phone.Package)), marshal(phone.VersionCode))),
		set(marshal(digest)),
	)
	bootKey := make([]byte, 32)
	if phone.BootKey != "" {
		if bootKey, err = hex.DecodeString(phone.BootKey); err != nil {
			panic(err)
		}
	}
	rootOfTrust := sequence(marshal(bootKey), marshal(phone.Locked), marshal(asn1.Enumerated(phone.BootState)), marshal(make([]byte, 32)))

	return sequence(
		marshal(phone.Version), marshal(asn1.Enumerated(phone.Level)),
		marshal(phone.Version), marshal(asn1.Enumerated(phone.Level)),
		marshal(challenge), marshal([]byte{}),
		sequence(explicit(709, marshal(applicationID))),
		sequence(explicit(702, marshal(int64(0))), explicit(704, rootOfTrust)),
	)
}

func marshal(v any) []byte {
	der, err := asn1.Marshal(v)
	if err != nil {
		panic(err)
	}

	return der
}

func explicit(tag int, inner []byte) []byte {
	return marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, IsCompound: true, Bytes: inner})
}

func sequence(items ...[]byte) []byte {
	return marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: join(items)})
}

func set(items ...[]byte) []byte {
	return marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: join(items)})
}

func join(items [][]byte) []byte {
	var out []byte
	for _, item := range items {
		out = append(out, item...)
	}

	return out
}
