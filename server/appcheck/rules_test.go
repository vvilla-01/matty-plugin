package appcheck

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every rule, each against a chain made here that breaks it and nothing
// else: the chain is made under a test root the policy pins, so that each
// rule is the only thing between it and a verdict of verified.

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

const (
	mattyDigest   = "e55250bea6690039b7849e33b049088ecbb998d9f20aaa8c7eb2dc5a4363273c"
	otherDigest   = "0011223344556677889900112233445566778899001122334455667788990011"
	pixel9BootKey = "9e6a8f3e0d761a780179f93acd5721ba1ab7c8c537c7761073c0a754b0e932de"
)

var (
	keysOnce sync.Once
	testKeys []*ecdsa.PrivateKey
)

// key is one of a few keys made once for every test.
func key(t *testing.T, i int) *ecdsa.PrivateKey {
	t.Helper()

	keysOnce.Do(func() {
		for range 6 {
			k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				panic(err)
			}
			testKeys = append(testKeys, k)
		}
	})

	return testKeys[i]
}

// The attestation as a test writes it.

func explicit(tag int, inner []byte) []byte {
	der, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, IsCompound: true, Bytes: inner})
	if err != nil {
		panic(err)
	}

	return der
}

func marshal(v any) []byte {
	der, err := asn1.Marshal(v)
	if err != nil {
		panic(err)
	}

	return der
}

func sequence(items ...[]byte) []byte {
	return marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: concat(items)})
}

func set(items ...[]byte) []byte {
	return marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: concat(items)})
}

func concat(items [][]byte) []byte {
	var out []byte
	for _, item := range items {
		out = append(out, item...)
	}

	return out
}

func fieldInt(tag int, v int64) []byte { return explicit(tag, marshal(v)) }

func fieldNull(tag int) []byte {
	return explicit(tag, marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagNull}))
}

func fieldRootOfTrust(bootKey []byte, locked bool, state BootState, withHash bool) []byte {
	items := [][]byte{marshal(bootKey), marshal(locked), marshal(asn1.Enumerated(state))}
	if withHash {
		items = append(items, marshal(make([]byte, 32)))
	}

	return explicit(tagRootOfTrust, sequence(items...))
}

type pkg struct {
	name    string
	version int64
}

func fieldApplicationID(packages []pkg, digests ...string) []byte {
	var records [][]byte
	for _, p := range packages {
		records = append(records, sequence(marshal([]byte(p.name)), marshal(p.version)))
	}
	var sums [][]byte
	for _, d := range digests {
		raw, err := hex.DecodeString(d)
		if err != nil {
			panic(err)
		}
		sums = append(sums, marshal(raw))
	}

	return explicit(tagApplicationID, marshal(sequence(set(records...), set(sums...))))
}

// attestation is a KeyDescription to write.
type attestation struct {
	version          int64
	attestationLevel Level
	keyMintLevel     Level
	challenge        []byte
	software         [][]byte
	hardware         [][]byte
}

func (a attestation) der() []byte {
	return sequence(
		marshal(a.version), marshal(asn1.Enumerated(a.attestationLevel)),
		marshal(a.version), marshal(asn1.Enumerated(a.keyMintLevel)),
		marshal(a.challenge), marshal([]byte{}),
		sequence(a.software...), sequence(a.hardware...),
	)
}

var testChallenge = DeriveChallenge("https://chat.example.com", "uid-alice", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")

// goodAttestation is what the official Matty on a locked phone with a
// verified system sends, as KeyMint 4 writes it.
func goodAttestation() attestation {
	return attestation{
		version:          400,
		attestationLevel: TrustedEnvironment,
		keyMintLevel:     TrustedEnvironment,
		challenge:        testChallenge,
		software: [][]byte{
			fieldInt(701, 1795000847217),
			fieldApplicationID([]pkg{{"ceo.villa.matty", 28}}, mattyDigest),
		},
		hardware: [][]byte{
			fieldInt(1, 2),
			fieldInt(702, originGenerated),
			fieldRootOfTrust(make([]byte, 32), true, BootVerified, true),
			fieldInt(705, 170000),
			fieldInt(tagOSPatchLevel, 202611),
		},
	}
}

// chainShape is what a test changes about the chain around the attestation.
type chainShape struct {
	// factory makes the certificate under the root name a serial number,
	// as a factory-provisioned chain's does.
	factory bool

	// attestationKeyNotCA leaves the CA flag off the certificate just above
	// the first; intermediateNotCA, off the one above that.
	attestationKeyNotCA bool
	intermediateNotCA   bool

	// Validity of the two certificates between the first and the root.
	notBefore, notAfter time.Time

	// extensionOn are the certificates, by index, that carry the
	// attestation besides the first, and noLeafExtension takes the first's.
	extensionOn     []int
	noLeafExtension bool
	twoOnLeaf       bool

	// leafCA makes the first certificate a CA, which no phone's is.
	leafCA bool
}

const testRootIndex = 3

// chain is four certificates: the first with the attestation, the phone's
// attestation key's, an intermediate, and a self-signed test root.
func buildChain(t *testing.T, ext []byte, shape chainShape) [][]byte {
	t.Helper()

	if shape.notBefore.IsZero() {
		shape.notBefore = testNow.AddDate(0, 0, -10)
	}
	if shape.notAfter.IsZero() {
		shape.notAfter = testNow.AddDate(0, 0, 20)
	}

	attestationExt := func(i int) []pkix.Extension {
		var out []pkix.Extension
		if i == 0 && !shape.noLeafExtension {
			out = append(out, pkix.Extension{Id: oidKeyAttestation, Value: ext})
			if shape.twoOnLeaf {
				out = append(out, pkix.Extension{Id: oidKeyAttestation, Value: ext})
			}
		}
		for _, j := range shape.extensionOn {
			if j == i {
				out = append(out, pkix.Extension{Id: oidKeyAttestation, Value: ext})
			}
		}

		return out
	}

	intermediateName := pkix.Name{CommonName: "Droid CA2", Organization: []string{"Example LLC"}}
	if shape.factory {
		intermediateName = pkix.Name{SerialNumber: "87f4514475ba0a2b"}
	}

	templates := []*x509.Certificate{
		{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Android Keystore Key"},
			NotBefore: time.Unix(0, 0), NotAfter: time.Date(2048, 1, 1, 0, 0, 0, 0, time.UTC),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtraExtensions: attestationExt(0),
			BasicConstraintsValid: shape.leafCA, IsCA: shape.leafCA,
		},
		{
			SerialNumber: big.NewInt(0x5ee8e1017e57), Subject: pkix.Name{CommonName: "c0ffee", Organization: []string{"TEE"}},
			NotBefore: shape.notBefore, NotAfter: shape.notAfter,
			BasicConstraintsValid: true, IsCA: !shape.attestationKeyNotCA, KeyUsage: x509.KeyUsageCertSign, ExtraExtensions: attestationExt(1),
		},
		{
			SerialNumber: big.NewInt(0x2a), Subject: intermediateName,
			NotBefore: shape.notBefore, NotAfter: shape.notAfter,
			BasicConstraintsValid: true, IsCA: !shape.intermediateNotCA, KeyUsage: x509.KeyUsageCertSign, ExtraExtensions: attestationExt(2),
		},
		{
			SerialNumber: big.NewInt(0x77), Subject: pkix.Name{CommonName: "Test Attestation Root"},
			NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC),
			BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign, ExtraExtensions: attestationExt(3),
		},
	}

	chain := make([][]byte, len(templates))
	for i := len(templates) - 1; i >= 0; i-- {
		parent, signer := templates[i], key(t, i)
		if i < len(templates)-1 {
			parent, signer = templates[i+1], key(t, i+1)
		}
		der, err := x509.CreateCertificate(rand.Reader, templates[i], parent, &key(t, i).PublicKey, signer)
		require.NoError(t, err)
		chain[i] = der

		// Parsed back, so that the next one is issued by exactly this. The
		// first issues nothing, and may be one Go will not parse.
		if i == 0 {
			break
		}
		parsed, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		templates[i] = parsed
		templates[i].ExtraExtensions = attestationExt(i)
	}

	return chain
}

func testRootKey(t *testing.T) string {
	t.Helper()

	spki, err := x509.MarshalPKIXPublicKey(&key(t, testRootIndex).PublicKey)
	require.NoError(t, err)
	sum := sha256.Sum256(spki)

	return hex.EncodeToString(sum[:])
}

// testPolicy is a release's policy, with the test root in place of
// Google's.
func testPolicy(t *testing.T) Policy {
	t.Helper()

	bootKeys, err := GrapheneOSBootKeys()
	require.NoError(t, err)

	return Policy{
		Roots:    map[string]bool{testRootKey(t): true},
		Packages: map[string]bool{"ceo.villa.matty": true},
		Digests:  map[string]bool{mattyDigest: true},
		BootKeys: bootKeys,
		Revoked:  map[string]bool{},
	}
}

func verifyWith(t *testing.T, a attestation, shape chainShape, policy Policy) Result {
	t.Helper()

	return Verify(buildChain(t, a.der(), shape), testChallenge, policy, testNow)
}

func TestTheOfficialAppOnAnIntactPhoneIsVerified(t *testing.T) {
	result := verifyWith(t, goodAttestation(), chainShape{}, testPolicy(t))

	require.True(t, result.Verified, result.Reason)
	require.Empty(t, result.Reason)
	require.Equal(t, Facts{
		AttestationVersion: 400, KeyMintVersion: 400, Level: TrustedEnvironment,
		Package: "ceo.villa.matty", VersionCode: 28, Digest: mattyDigest,
		BootState: BootVerified, DeviceLocked: true, OSPatchLevel: 202611,
	}, result.Facts)
}

// The chain's rules.
func TestEveryChainRuleRefusesWhatBreaksIt(t *testing.T) {
	good := goodAttestation()

	for name, tc := range map[string]struct {
		shape  chainShape
		policy func(*Policy)
		reason string
	}{
		"an intermediate that is not a CA": {
			shape:  chainShape{intermediateNotCA: true},
			reason: "Certificate 3 of the app's chain signs another but is not a certificate authority.",
		},
		"a remotely provisioned attestation key that is not a CA": {
			shape:  chainShape{attestationKeyNotCA: true},
			reason: "Certificate 2 of the app's chain signs another but is not a certificate authority.",
		},
		"a factory chain's intermediate that is not a CA": {
			shape:  chainShape{factory: true, intermediateNotCA: true},
			reason: "Certificate 3 of the app's chain signs another but is not a certificate authority.",
		},
		"a root that is not pinned": {
			policy: func(p *Policy) { p.Roots = map[string]bool{GoogleRSARootKey: true} },
			reason: "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.",
		},
		"the attestation in the attestation key's certificate too": {
			shape:  chainShape{extensionOn: []int{1}},
			reason: "Certificate 2 of the app's chain carries an attestation, which only the first may: the chain was extended by somebody else.",
		},
		"the attestation in the root": {
			shape:  chainShape{extensionOn: []int{3}},
			reason: "Certificate 4 of the app's chain carries an attestation, which only the first may: the chain was extended by somebody else.",
		},
		"no attestation": {
			shape:  chainShape{noLeafExtension: true},
			reason: "The app's certificate carries no attestation.",
		},
		"two attestations": {
			// Go's parser refuses a certificate with an extension twice.
			shape:  chainShape{twoOnLeaf: true},
			reason: "Certificate 1 of the app's chain could not be read.",
		},
		"an expired remotely provisioned chain": {
			shape:  chainShape{notBefore: testNow.AddDate(0, -2, 0), notAfter: testNow.AddDate(0, 0, -1)},
			reason: "Certificate 2 of the app's chain expired on 2026-11-17.",
		},
		"a chain not valid yet": {
			shape:  chainShape{notBefore: testNow.AddDate(0, 0, 1), notAfter: testNow.AddDate(0, 1, 0)},
			reason: "Certificate 2 of the app's chain is not valid until 2026-11-19.",
		},
		"a factory chain not valid yet": {
			shape:  chainShape{factory: true, notBefore: testNow.AddDate(0, 0, 1), notAfter: testNow.AddDate(0, 1, 0)},
			reason: "Certificate 2 of the app's chain is not valid until 2026-11-19.",
		},
		"a revoked intermediate, in hex": {
			policy: func(p *Policy) { p.Revoked = map[string]bool{"2a": true} },
			reason: "Google has revoked certificate 3 of this phone's attestation chain (serial 2a), so its secure hardware can no longer be trusted.",
		},
		"a revoked attestation key, in decimal": {
			policy: func(p *Policy) { p.Revoked = map[string]bool{big.NewInt(0x5ee8e1017e57).String(): true} },
			reason: "Google has revoked certificate 2 of this phone's attestation chain (serial 5ee8e1017e57), so its secure hardware can no longer be trusted.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := testPolicy(t)
			if tc.policy != nil {
				tc.policy(&policy)
			}
			result := verifyWith(t, good, tc.shape, policy)
			require.False(t, result.Verified)
			require.Equal(t, tc.reason, result.Reason)
		})
	}

	// What the rules leave alone: a factory chain's expired certificates,
	// and the CA flag missing from a factory chain's attestation key.
	for name, shape := range map[string]chainShape{
		"an expired factory chain":                     {factory: true, notBefore: testNow.AddDate(-10, 0, 0), notAfter: testNow.AddDate(0, -6, 0)},
		"a factory attestation key that is not a CA":   {factory: true, attestationKeyNotCA: true},
		"a remotely provisioned chain, as it is today": {},
	} {
		result := verifyWith(t, good, shape, testPolicy(t))
		require.True(t, result.Verified, "%s: %s", name, result.Reason)
		require.Equal(t, shape.factory, result.Facts.Factory, name)
	}
}

// A chain somebody extended: a certificate with an attestation of their own
// making, signed with the key the app was really given, in front of the
// real chain. The real attestation is then the second certificate's.
func TestAChainExtendedWithTheAppsOwnKeyIsRefused(t *testing.T) {
	real := buildChain(t, goodAttestation().der(), chainShape{factory: true, attestationKeyNotCA: true})
	realLeaf, err := x509.ParseCertificate(real[0])
	require.NoError(t, err)

	forged := goodAttestation()
	forged.challenge = DeriveChallenge("https://chat.example.com", "uid-alice", "another challenge")
	fake, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Date(2048, 1, 1, 0, 0, 0, 0, time.UTC),
		ExtraExtensions: []pkix.Extension{{Id: oidKeyAttestation, Value: forged.der()}},
	}, realLeaf, &key(t, 5).PublicKey, key(t, 0))
	require.NoError(t, err)

	extended := append([][]byte{fake}, real...)
	result := Verify(extended, forged.challenge, testPolicy(t), testNow)
	require.False(t, result.Verified)
	require.Equal(t, "Certificate 2 of the app's chain signs another but is not a certificate authority.", result.Reason)

	// Even were the app's certificate a CA, its own attestation, now second,
	// gives the chain away.
	real = buildChain(t, goodAttestation().der(), chainShape{factory: true, attestationKeyNotCA: true, leafCA: true})
	realLeaf, err = x509.ParseCertificate(real[0])
	require.NoError(t, err)
	fake, err = x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "Android Keystore Key"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Date(2048, 1, 1, 0, 0, 0, 0, time.UTC),
		ExtraExtensions: []pkix.Extension{{Id: oidKeyAttestation, Value: forged.der()}},
	}, realLeaf, &key(t, 5).PublicKey, key(t, 0))
	require.NoError(t, err)

	result = Verify(append([][]byte{fake}, real...), forged.challenge, testPolicy(t), testNow)
	require.False(t, result.Verified)
	require.Equal(t, "Certificate 2 of the app's chain carries an attestation, which only the first may: the chain was extended by somebody else.", result.Reason)
}

// No signature is checked with a key the chain carries before the root is
// known to be pinned, and before the key above it has vouched for it: the
// root's key is held to the pins first, and the links are checked from the
// root down, so a chain somebody made costs no signature work with keys of
// their choosing.
func TestTheChainIsCheckedFromItsPinnedRootDown(t *testing.T) {
	// The last byte of a certificate is the last of its signature.
	broken := func(chain [][]byte, which ...int) [][]byte {
		out := make([][]byte, len(chain))
		for i, der := range chain {
			out[i] = append([]byte{}, der...)
		}
		for _, i := range which {
			out[i][len(out[i])-1] ^= 1
		}

		return out
	}
	chain := buildChain(t, goodAttestation().der(), chainShape{})

	// A link that does not hold, under a root nobody pinned: the root is
	// what is refused, before the link is looked at.
	notPinned := testPolicy(t)
	notPinned.Roots = map[string]bool{GoogleRSARootKey: true}
	require.Equal(t, "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.",
		Verify(broken(chain, 0), testChallenge, notPinned, testNow).Reason)

	// Two links that do not hold: the one nearer the root is found.
	require.Equal(t, "Certificate 2 of the app's chain is not signed by the one after it.",
		Verify(broken(chain, 0, 1), testChallenge, testPolicy(t), testNow).Reason)
	require.Equal(t, "Certificate 1 of the app's chain is not signed by the one after it.",
		Verify(broken(chain, 0), testChallenge, testPolicy(t), testNow).Reason)

	// The root's own signature is checked once its key is known to be pinned.
	require.Equal(t, "The app's chain does not end in a root certificate that signed itself.",
		Verify(broken(chain, testRootIndex), testChallenge, testPolicy(t), testNow).Reason)
}

// The attestation's rules.
func TestEveryAttestationRuleRefusesWhatBreaksIt(t *testing.T) {
	type change func(a *attestation, p *Policy)

	withoutHardware := func(tag int) change {
		return func(a *attestation, _ *Policy) {
			var kept [][]byte
			for _, field := range a.hardware {
				var raw asn1.RawValue
				_, err := asn1.Unmarshal(field, &raw)
				if err != nil {
					panic(err)
				}
				if raw.Tag != tag {
					kept = append(kept, field)
				}
			}
			a.hardware = kept
		}
	}
	replaceHardware := func(tag int, field []byte) change {
		return func(a *attestation, p *Policy) {
			withoutHardware(tag)(a, p)
			a.hardware = append(a.hardware, field)
		}
	}
	appID := func(field []byte) change {
		return func(a *attestation, _ *Policy) {
			a.software = [][]byte{fieldInt(701, 1), field}
		}
	}

	for name, tc := range map[string]struct {
		change change
		reason string
	}{
		"version 1": {
			change: func(a *attestation, _ *Policy) { a.version = 1 },
			reason: "The app's attestation is in a format this server does not know (version 1).",
		},
		"version 41": {
			change: func(a *attestation, _ *Policy) { a.version = 41 },
			reason: "The app's attestation is in a format this server does not know (version 41).",
		},
		"made in software": {
			change: func(a *attestation, _ *Policy) { a.attestationLevel = Software },
			reason: "This phone made its attestation in software, not in its secure hardware.",
		},
		"KeyMint in software": {
			change: func(a *attestation, _ *Policy) { a.keyMintLevel = Software },
			reason: "This phone made its attestation in software, not in its secure hardware.",
		},
		"a security level nobody knows": {
			change: func(a *attestation, _ *Policy) { a.keyMintLevel = 3 },
			reason: "The app's attestation names a security level this server does not know (3).",
		},
		"another challenge": {
			change: func(a *attestation, _ *Policy) {
				a.challenge = DeriveChallenge("https://chat.example.com", "uid-bob", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
			},
			reason: "The app's attestation was made for another challenge, so it is not an answer to this one.",
		},
		"no origin": {
			change: withoutHardware(tagOrigin),
			reason: "The phone's secure hardware did not say where the attested key was made.",
		},
		"an imported key": {
			change: replaceHardware(tagOrigin, fieldInt(tagOrigin, 2)),
			reason: "The attested key was put into the phone, not made inside it.",
		},
		"the origin only in Android's list": {
			change: func(a *attestation, p *Policy) {
				withoutHardware(tagOrigin)(a, p)
				a.software = append(a.software, fieldInt(tagOrigin, originGenerated))
			},
			reason: "The phone's secure hardware did not say where the attested key was made.",
		},
		"no root of trust": {
			change: withoutHardware(tagRootOfTrust),
			reason: "This phone's secure hardware did not say how the phone started.",
		},
		"the root of trust only in Android's list": {
			change: func(a *attestation, p *Policy) {
				withoutHardware(tagRootOfTrust)(a, p)
				a.software = append(a.software, fieldRootOfTrust(make([]byte, 32), true, BootVerified, true))
			},
			reason: "This phone's secure hardware did not say how the phone started.",
		},
		"an unlocked bootloader": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(make([]byte, 32), false, BootUnverified, true)),
			reason: "This phone's bootloader is unlocked.",
		},
		"an unlocked bootloader that says verified": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(make([]byte, 32), false, BootVerified, true)),
			reason: "This phone's bootloader is unlocked.",
		},
		"an unverified system": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(make([]byte, 32), true, BootUnverified, true)),
			reason: "This phone's system is not verified.",
		},
		"a failed boot": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(make([]byte, 32), true, BootFailed, true)),
			reason: "This phone's verified boot failed.",
		},
		"a boot state nobody knows": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(make([]byte, 32), true, 7, true)),
			reason: "This phone reported a boot state this server does not know (7).",
		},
		"a system signed by a key nobody accepts": {
			change: replaceHardware(tagRootOfTrust, fieldRootOfTrust(mustHex(t, otherDigest), true, BootSelfSigned, true)),
			reason: "This phone runs a system signed with its owner's own key (00:11:22:33:44:55:66:77…), which this server does not accept.",
		},
		"no application id": {
			change: func(a *attestation, _ *Policy) { a.software = [][]byte{fieldInt(701, 1)} },
			reason: "The app's attestation does not say which app asked.",
		},
		"the application id only in the hardware list": {
			change: func(a *attestation, _ *Policy) {
				a.hardware = append(a.hardware, fieldApplicationID([]pkg{{"ceo.villa.matty", 28}}, mattyDigest))
				a.software = [][]byte{fieldInt(701, 1)}
			},
			reason: "The app's attestation does not say which app asked.",
		},
		"two packages sharing a uid": {
			change: appID(fieldApplicationID([]pkg{{"ceo.villa.matty", 28}, {"com.example.other", 1}}, mattyDigest)),
			reason: "The attestation names 2 apps sharing one identity, so it cannot say which of them asked.",
		},
		"no package": {
			change: appID(fieldApplicationID(nil, mattyDigest)),
			reason: "The attestation names 0 apps sharing one identity, so it cannot say which of them asked.",
		},
		"another package": {
			change: appID(fieldApplicationID([]pkg{{"com.example.matty", 28}}, mattyDigest)),
			reason: "The app that asked is com.example.matty, not Matty.",
		},
		"the debug build's package": {
			change: appID(fieldApplicationID([]pkg{{"ceo.villa.matty.debug", 28}}, mattyDigest)),
			reason: "The app that asked is ceo.villa.matty.debug, not Matty.",
		},
		"a package name with control characters": {
			change: appID(fieldApplicationID([]pkg{{"evil\napp", 1}}, mattyDigest)),
			reason: "The app that asked is evil?app, not Matty.",
		},
		"two signing certificates": {
			change: appID(fieldApplicationID([]pkg{{"ceo.villa.matty", 28}}, mattyDigest, otherDigest)),
			reason: "The attestation names 2 signing certificates for the app; Matty has one.",
		},
		"another signing certificate": {
			change: appID(fieldApplicationID([]pkg{{"ceo.villa.matty", 28}}, otherDigest)),
			reason: "This copy of Matty was signed with a certificate this server does not accept (00:11:22:33:44:55:66:77…).",
		},
		"all applications, as Android writes it": {
			change: func(a *attestation, _ *Policy) { a.software = append(a.software, fieldNull(tagAllApplications)) },
			reason: "The attested key may be used by every app on the phone, so it says nothing about which app asked.",
		},
		"all applications in the hardware list": {
			change: func(a *attestation, _ *Policy) { a.hardware = append(a.hardware, fieldNull(tagAllApplications)) },
			reason: "The attested key may be used by every app on the phone, so it says nothing about which app asked.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := goodAttestation()
			policy := testPolicy(t)
			tc.change(&a, &policy)

			result := verifyWith(t, a, chainShape{}, policy)
			require.False(t, result.Verified)
			require.Equal(t, tc.reason, result.Reason)
		})
	}
}

// What the attestation's rules accept besides the usual: every version an
// Android 17 phone may send, StrongBox, a later KeyMint, the owner's own
// system when its key is accepted, and another certificate when an admin
// lists it.
func TestTheAttestationRulesAcceptWhatTheyShould(t *testing.T) {
	for _, version := range []int64{2, 3, 4, 100, 200, 300, 400, 500, 600} {
		a := goodAttestation()
		a.version = version
		if version == 2 {
			// Attestation version 2 has no verifiedBootHash.
			a.hardware[2] = fieldRootOfTrust(make([]byte, 32), true, BootVerified, false)
		}
		result := verifyWith(t, a, chainShape{}, testPolicy(t))
		require.True(t, result.Verified, "version %d: %s", version, result.Reason)
		require.Equal(t, version, result.Facts.AttestationVersion)
	}

	strongBox := goodAttestation()
	strongBox.attestationLevel, strongBox.keyMintLevel = StrongBox, StrongBox
	result := verifyWith(t, strongBox, chainShape{}, testPolicy(t))
	require.True(t, result.Verified, result.Reason)
	require.Equal(t, StrongBox, result.Facts.Level)

	mixed := goodAttestation()
	mixed.attestationLevel = StrongBox
	require.Equal(t, TrustedEnvironment, verifyWith(t, mixed, chainShape{}, testPolicy(t)).Facts.Level, "the lower of the two")

	graphene := goodAttestation()
	graphene.hardware[2] = fieldRootOfTrust(mustHex(t, pixel9BootKey), true, BootSelfSigned, true)
	result = verifyWith(t, graphene, chainShape{}, testPolicy(t))
	require.True(t, result.Verified, result.Reason)
	require.Equal(t, BootSelfSigned, result.Facts.BootState)
	require.Equal(t, pixel9BootKey, result.Facts.BootKey, "the key it was accepted for")

	owners := goodAttestation()
	owners.hardware[2] = fieldRootOfTrust(mustHex(t, otherDigest), true, BootSelfSigned, true)
	policy := testPolicy(t)
	policy.BootKeys[otherDigest] = true
	require.True(t, verifyWith(t, owners, chainShape{}, policy).Verified)

	ownBuild := goodAttestation()
	ownBuild.software[1] = fieldApplicationID([]pkg{{"ceo.villa.matty", 28}}, otherDigest)
	policy = testPolicy(t)
	policy.Digests[otherDigest] = true
	require.True(t, verifyWith(t, ownBuild, chainShape{}, policy).Verified)

	// Fields this check does not decide by are skipped, whatever they hold.
	extra := goodAttestation()
	extra.hardware = append(extra.hardware, explicit(724, marshal("anything at all")), explicit(11, marshal(true)))
	require.True(t, verifyWith(t, extra, chainShape{}, testPolicy(t)).Verified)
}

// Strict about what is decided by: anything malformed there is refused.
func TestAMalformedAttestationIsRefused(t *testing.T) {
	broken := func(t *testing.T, ext []byte) string {
		t.Helper()

		result := Verify(buildChain(t, ext, chainShape{}), testChallenge, testPolicy(t), testNow)
		require.False(t, result.Verified)
		require.True(t, strings.HasPrefix(result.Reason, "The app's attestation could not be read: "), result.Reason)

		return result.Reason
	}

	good := goodAttestation().der()
	broken(t, append(append([]byte{}, good...), 0))
	broken(t, good[:len(good)-1])
	broken(t, marshal(int64(5)))

	twice := goodAttestation()
	twice.hardware = append(twice.hardware, fieldInt(tagOrigin, originGenerated))
	require.Contains(t, broken(t, twice.der()), "field [702] appears twice")

	implicit := goodAttestation()
	implicit.hardware = append(implicit.hardware, marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 800, Bytes: []byte{1}}))
	require.Contains(t, broken(t, implicit.der()), "is not an explicitly tagged field")

	wrongType := goodAttestation()
	wrongType.hardware[1] = explicit(tagOrigin, marshal([]byte{0}))
	require.Contains(t, broken(t, wrongType.der()), "origin does not read")

	badBoolean := goodAttestation()
	rot := fieldRootOfTrust(make([]byte, 32), true, BootVerified, true)
	i := strings.Index(string(rot), "\x01\x01\xff")
	require.Positive(t, i)
	rot[i+2] = 0x01
	badBoolean.hardware[2] = rot
	require.Contains(t, broken(t, badBoolean.der()), "deviceLocked")

	short := goodAttestation()
	short.hardware[2] = explicit(tagRootOfTrust, sequence(marshal(make([]byte, 32)), marshal(true)))
	require.Contains(t, broken(t, short.der()), "rootOfTrust does not read")
}

func TestTheChallengeIsTheDigestOfWhatItIsFor(t *testing.T) {
	// The same figure a client computes, worked out independently with
	// Python's hashlib over the same five lines' text.
	require.Equal(t, "c398158673574461506bf44b65ed7a43ef8322a0804efb3b2ee73c94e475e6ab",
		hex.EncodeToString(DeriveChallenge("https://chat.example.com", "uid-alice", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")))

	for in, want := range map[string]string{
		"https://Chat.Example.com":            "https://chat.example.com",
		"https://chat.example.com:443/":       "https://chat.example.com",
		"HTTPS://chat.example.com/mattermost": "https://chat.example.com",
		"http://localhost:8065":               "http://localhost:8065",
		"http://localhost:80":                 "http://localhost",
		"https://chat.example.com:8443":       "https://chat.example.com:8443",
		"http://[::1]:8065":                   "http://[::1]:8065",
		"https://[2001:DB8::1]":               "https://[2001:db8::1]",
		" https://chat.example.com ":          "https://chat.example.com",

		// As OkHttp 4.12's HttpUrl writes the host, which the app's origin
		// is made from: each figure is what it printed for the same input.
		"https://[2001:db8:0:0::1]":                         "https://[2001:db8::1]",
		"https://[2001:0db8:0000:0000:0001:0000:0000:0001]": "https://[2001:db8::1:0:0:1]",
		"http://[::ffff:127.0.0.1]:8065":                    "http://127.0.0.1:8065",
		"https://[::1.2.3.4]":                               "https://[::102:304]",
		"https://bücher.example":                            "https://xn--bcher-kva.example",
		"https://BÜCHER.Example":                            "https://xn--bcher-kva.example",
		"https://straße.example":                            "https://strasse.example",
		"https://ὀδυσσεύς.example":                          "https://xn--pxac5babi3d8526a.example",
		"https://日本.example:8443":                           "https://xn--wgv71a.example:8443",
		"https://ｃｈａｔ.example":                              "https://chat.example",
		"https://xn--bcher-kva.example":                     "https://xn--bcher-kva.example",
		"https://chat_x.example":                            "https://chat_x.example",
	} {
		got, err := Origin(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "chat.example.com", "ftp://chat.example.com", "https://", "https://chat.example.com:0", "https://chat.example.com:99999"} {
		_, err := Origin(bad)
		require.Error(t, err, bad)
	}
}

func TestDigestsAreReadAsPeopleWriteThem(t *testing.T) {
	for _, in := range []string{
		"E5:52:50:BE:A6:69:00:39:B7:84:9E:33:B0:49:08:8E:CB:B9:98:D9:F2:0A:AA:8C:7E:B2:DC:5A:43:63:27:3C",
		"e55250bea6690039b7849e33b049088ecbb998d9f20aaa8c7eb2dc5a4363273c",
		" E5 52 50 BE A6 69 00 39 B7 84 9E 33 B0 49 08 8E CB B9 98 D9 F2 0A AA 8C 7E B2 DC 5A 43 63 27 3C ",
	} {
		got, ok := ParseDigest(in)
		require.True(t, ok, in)
		require.Equal(t, mattyDigest, got)
	}
	for _, bad := range []string{"", "e55250be", mattyDigest + "00", strings.Replace(mattyDigest, "e", "g", 1)} {
		_, ok := ParseDigest(bad)
		require.False(t, ok, bad)
	}

	require.Equal(t, "E5:52:50:BE:A6:69:00:39…", ShortDigest(mattyDigest))
}

func TestTheBuiltInDataIsWhatItsReadmeSays(t *testing.T) {
	roots, err := GoogleRoots()
	require.NoError(t, err)
	require.Equal(t, map[string]bool{GoogleRSARootKey: true, GoogleECDSARootKey: true}, roots)

	revoked, err := RevokedSerials()
	require.NoError(t, err)
	require.Len(t, revoked, RevocationListCount)

	bootKeys, err := GrapheneOSBootKeys()
	require.NoError(t, err)
	require.Len(t, bootKeys, 21)
	require.True(t, bootKeys[pixel9BootKey])

	// Every issue of Google's RSA root is the same key: the probe's copy of
	// the root, and the 2019 one that remotely provisioned chains carry.
	for _, file := range []string{"keyattestation/caiman-sdk36_TEE_EC_RKP.pem", "keyattestation/frankel-sdk37_TEE_EC_2026.pem"} {
		chain := readChain(t, file)
		root, err := x509.ParseCertificate(chain[len(chain)-1])
		require.NoError(t, err)
		sum := sha256.Sum256(root.RawSubjectPublicKeyInfo)
		require.True(t, roots[hex.EncodeToString(sum[:])], file)
	}
}
