package appcheck

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Real chains: what phones and the development emulator actually made. They
// are the only stand-in for a phone there is, so each is read to the end and
// held to what is known of it.
//
// ../testdata/appcheck/keyattestation is Google's own test data for its
// reference verifier (github.com/android/keyattestation, Apache-2.0: see the
// README and LICENSE there), from Pixels and one Sony, factory and remotely
// provisioned, under both of Google's roots, TEE and StrongBox, locked and
// unlocked. ../testdata/appcheck/emulator is what the development emulator
// made for a probe app signed three ways.

const vectorDir = "../testdata/appcheck"

// readChain is a PEM file's certificates, in order.
func readChain(t *testing.T, name string) [][]byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(vectorDir, name))
	require.NoError(t, err)

	var chain [][]byte
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		require.Equal(t, "CERTIFICATE", block.Type, name)
		chain = append(chain, block.Bytes)
	}
	require.NotEmpty(t, chain, name)

	return chain
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()

	b, err := hex.DecodeString(s)
	require.NoError(t, err)

	return b
}

// googlePolicy is a release's policy for an app of this package and digest.
func googlePolicy(t *testing.T, pkg, digest string) Policy {
	t.Helper()

	roots, err := GoogleRoots()
	require.NoError(t, err)
	revoked, err := RevokedSerials()
	require.NoError(t, err)
	bootKeys, err := GrapheneOSBootKeys()
	require.NoError(t, err)

	return Policy{
		Roots:    roots,
		Packages: map[string]bool{pkg: true},
		Digests:  map[string]bool{digest: true},
		BootKeys: bootKeys,
		Revoked:  revoked,
	}
}

func day(s string) time.Time {
	at, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}

	return at.Add(12 * time.Hour)
}

// The challenge each vector was made for, as its JSON beside it says.
const (
	challengeWord      = "6368616c6c656e6765" // "challenge"
	googleDigest       = "103938ee4537e59e8ee792f654504fb8346fc6b346d0bbc4415fc339fcfc8ec1"
	googleApp          = "com.google.android.attestation"
	googleCollector    = "com.google.wireless.android.security.attestationverifier.collector"
	ctsApp             = "android.keystore.cts"
	ctsDigest          = "6cecc50e34ae31bfb5678986d6d6d3736c571ded2f2459527793e1f054eb0c9b"
	playStore          = "com.android.vending"
	playStoreDigest    = "f0fd6c5b410f25cb25c3b53346c8972fae30f8ee7411df910480ad6b2d60db83"
	challengeCaimanTEE = "64363838643736332d363131382d346361362d393462322d653663643965643765346534"
	challengeCaimanSB  = "37636361633165612d343834352d343832652d383538642d663666613961613863323935"
	challengeFrankel   = "31343661616631362d373831342d346534342d616364622d653564363532613762363032"
	challengeTeguSB    = "39303537386531642d663562662d346363662d613237662d613466346438396565323166"
	challengeTeguConf  = "62313633316461622d326536632d343635662d386137662d323461363135326135313861"
	challengeWalleye   = "39313238393565302d336531392d343332372d613066622d393165326633663436636462"
	challengeSony      = "3eafe4d5dd0090de5a42b432b42481af5ce29963656b2584c59a492de16d00c9"
)

func TestRealPhonesChainsReadAsGooglesDataSays(t *testing.T) {
	for _, tc := range []struct {
		file      string
		at        string
		challenge string
		pkg       string
		digest    string

		reason   string // "" for verified
		version  int64
		level    Level
		boot     BootState
		locked   bool
		factory  bool
		patch    int64
		versionC int64
	}{
		// Locked phones with their systems verified, which a release
		// accepts: remotely provisioned under the RSA root (TEE, StrongBox),
		// under the ECDSA root (Android 17, KeyMint 5; and a StrongBox one),
		// and a factory-provisioned Sony whose certificates expired in May
		// 2026 and are still good, as Google says they are.
		{file: "caiman-sdk36_TEE_EC_RKP.pem", at: "2025-10-01", challenge: challengeCaimanTEE, pkg: googleApp, digest: googleDigest,
			version: 400, level: TrustedEnvironment, boot: BootVerified, locked: true, patch: 202511},
		{file: "caiman-sdk36_SB_EC_RKP.pem", at: "2025-10-01", challenge: challengeCaimanSB, pkg: googleApp, digest: googleDigest,
			version: 300, level: StrongBox, boot: BootVerified, locked: true, patch: 202511},
		{file: "frankel-sdk37_TEE_EC_2026.pem", at: "2026-11-18", challenge: challengeFrankel, pkg: googleApp, digest: googleDigest,
			version: 500, level: TrustedEnvironment, boot: BootVerified, locked: true, patch: 202611},
		{file: "tegu-sdk36_SB_EC_2026_ROOT.pem", at: "2026-03-01", challenge: challengeTeguSB, pkg: googleApp, digest: googleDigest,
			version: 300, level: StrongBox, boot: BootVerified, locked: true, patch: 202602},
		{file: "sony-xperia10-iii-sdk33_TEE_EC.pem", at: "2026-11-18", challenge: challengeSony, pkg: playStore, digest: playStoreDigest,
			version: 3, level: TrustedEnvironment, boot: BootVerified, locked: true, factory: true, patch: 202307, versionC: 85162330},

		// Unlocked phones: every attestation version an Android 17 phone
		// may send among them (2, 3, 300, 500), and an ML-DSA key.
		{file: "akita-sdk34_TEE_EC_NONE.pem", at: "2024-10-01", challenge: challengeWord, pkg: googleCollector, digest: googleDigest,
			reason: "This phone's bootloader is unlocked.", version: 300, level: TrustedEnvironment, boot: BootUnverified, patch: 202408},
		{file: "akita-sdk34_SB_RSA_NONE.pem", at: "2024-10-01", challenge: challengeWord, pkg: googleCollector, digest: googleDigest,
			reason: "This phone's bootloader is unlocked.", version: 300, level: StrongBox, boot: BootUnverified, patch: 202408},
		{file: "blueline-sdk28_TEE_EC_NONE.pem", at: "2026-11-18", challenge: challengeWord, pkg: googleCollector, digest: googleDigest,
			reason: "This phone's bootloader is unlocked.", version: 3, level: TrustedEnvironment, boot: BootUnverified, factory: true, patch: 201908},
		{file: "walleye-sdk27_TEE_EC_NONE.pem", at: "2026-11-18", challenge: challengeWalleye, pkg: googleApp, digest: googleDigest,
			reason: "This phone's bootloader is unlocked.", version: 2, level: TrustedEnvironment, boot: BootUnverified, factory: true, patch: 201807},
		{file: "tegu-sdk37_TEE_TRUSTED_CONF.pem", at: "2026-07-01", challenge: challengeTeguConf, pkg: googleApp, digest: googleDigest,
			reason: "This phone's bootloader is unlocked.", version: 500, level: TrustedEnvironment, boot: BootUnverified, patch: 202608},
		{file: "tokay-sdk37_TEE_MLDSA_RKP.pem", at: "2026-05-01", challenge: challengeWord, pkg: ctsApp, digest: ctsDigest,
			reason: "This phone's bootloader is unlocked.", version: 500, level: TrustedEnvironment, boot: BootUnverified, patch: 202606},
		{file: "tokay-sdk37_TEE_MLDSA_FACTORY.pem", at: "2026-11-18", challenge: challengeWord, pkg: ctsApp, digest: ctsDigest,
			reason: "This phone's bootloader is unlocked.", version: 500, level: TrustedEnvironment, boot: BootUnverified, factory: true, patch: 202606},

		// Android's software attestation, whose key is in Android's source.
		{file: "marlin-sdk29_TEE_EC_NONE.pem", at: "2026-11-18", challenge: challengeWord, pkg: googleCollector, digest: googleDigest,
			reason: "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.", boot: BootUnknown},

		// A remotely provisioned chain is held to its dates.
		{file: "caiman-sdk36_TEE_EC_RKP.pem", at: "2026-11-18", challenge: challengeCaimanTEE, pkg: googleApp, digest: googleDigest,
			reason: "Certificate 2 of the app's chain expired on 2025-10-03.", boot: BootUnknown},
	} {
		t.Run(tc.file+"@"+tc.at, func(t *testing.T) {
			result := Verify(readChain(t, "keyattestation/"+tc.file), mustHex(t, tc.challenge), googlePolicy(t, tc.pkg, tc.digest), day(tc.at))

			require.Equal(t, tc.reason, result.Reason)
			require.Equal(t, tc.reason == "", result.Verified)
			require.Equal(t, tc.version, result.Facts.AttestationVersion)
			require.Equal(t, tc.boot, result.Facts.BootState)
			require.Equal(t, tc.locked, result.Facts.DeviceLocked)
			require.False(t, result.Facts.Development)
			if tc.version != 0 {
				require.Equal(t, tc.level, result.Facts.Level)
				require.Equal(t, tc.factory, result.Facts.Factory)
				require.Equal(t, tc.patch, result.Facts.OSPatchLevel)
			}
			if tc.reason == "" {
				require.Equal(t, tc.pkg, result.Facts.Package)
				require.Equal(t, tc.versionC, result.Facts.VersionCode)
				require.Equal(t, tc.digest, result.Facts.Digest)
			}
		})
	}
}

// A real phone's verified chain, refused for each thing about it that is not
// what this server accepts.
func TestARealPhoneIsRefusedForWhatItIsNot(t *testing.T) {
	chain := readChain(t, "keyattestation/frankel-sdk37_TEE_EC_2026.pem")
	challenge := mustHex(t, challengeFrankel)
	at := day("2026-11-18")

	require.True(t, Verify(chain, challenge, googlePolicy(t, googleApp, googleDigest), at).Verified)

	other := googlePolicy(t, "ceo.villa.matty", googleDigest)
	require.Equal(t, "The app that asked is com.google.android.attestation, not Matty.", Verify(chain, challenge, other, at).Reason)

	other = googlePolicy(t, googleApp, "e55250bea6690039b7849e33b049088ecbb998d9f20aaa8c7eb2dc5a4363273c")
	require.Equal(t, "This copy of Matty was signed with a certificate this server does not accept (10:39:38:EE:45:37:E5:9E…).", Verify(chain, challenge, other, at).Reason)

	require.Equal(t, "The app's attestation was made for another challenge, so it is not an answer to this one.",
		Verify(chain, mustHex(t, challengeCaimanTEE), googlePolicy(t, googleApp, googleDigest), at).Reason)

	// Revoked: its intermediate's serial, as Google's list writes serials.
	intermediate, err := x509.ParseCertificate(chain[1])
	require.NoError(t, err)
	for _, form := range []string{intermediate.SerialNumber.Text(16), intermediate.SerialNumber.String()} {
		policy := googlePolicy(t, googleApp, googleDigest)
		policy.Revoked = map[string]bool{form: true}
		result := Verify(chain, challenge, policy, at)
		require.False(t, result.Verified, form)
		require.Contains(t, result.Reason, "Google has revoked certificate 2 of this phone's attestation chain", form)
	}

	// Under a root that is not pinned.
	policy := googlePolicy(t, googleApp, googleDigest)
	delete(policy.Roots, GoogleECDSARootKey)
	require.Contains(t, Verify(chain, challenge, policy, at).Reason, "Google's hardware attestation roots")

	// The chain without its root, and with its order changed.
	require.Equal(t, "The app's chain does not end in a root certificate that signed itself.",
		Verify(chain[:len(chain)-1], challenge, googlePolicy(t, googleApp, googleDigest), at).Reason)
	swapped := [][]byte{chain[1], chain[0]}
	swapped = append(swapped, chain[2:]...)
	require.False(t, Verify(swapped, challenge, googlePolicy(t, googleApp, googleDigest), at).Verified)
}

// Google's two chains that are wrong on purpose.
func TestGooglesInvalidChainsAreRefused(t *testing.T) {
	policy := googlePolicy(t, googleCollector, googleDigest)

	// deviceLocked is not a DER BOOLEAN.
	malformed := Verify(readChain(t, "keyattestation/invalid_malformed_rot_device_locked.pem"), []byte("challenge"), policy, day("2026-11-18"))
	require.False(t, malformed.Verified)
	require.Contains(t, malformed.Reason, "The app's attestation could not be read: its hardware-enforced list does not read: rootOfTrust does not read: deviceLocked")

	// Fields out of order, put there by editing a real certificate, whose
	// signature then no longer holds. (Order alone is not refused: fields
	// are read by their tags.)
	unordered := Verify(readChain(t, "keyattestation/invalid_tags_not_in_ascending_order.pem"), []byte("challenge"), policy, day("2026-11-18"))
	require.Equal(t, "Certificate 1 of the app's chain is not signed by the one after it.", unordered.Reason)
}

// The development emulator's attestations, read exactly as the two
// independent decoders read them (the probe's Go decoder and a hand-written
// DER walker): version 400, software at both levels, the challenge A0..BF,
// everything in the software-enforced list and nothing in the hardware one,
// an unlocked, unverified boot, and the one package with the one digest:
// the RSA certificate's when the app is signed hybrid or classic alike, the
// debug key's when its history starts with it.
func TestTheEmulatorsAttestationReadsAsTheDecodersReadIt(t *testing.T) {
	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(0xa0 + i)
	}

	for file, digest := range map[string]string{
		"emulator/classic.pem":       "ff2b7e6348b7dc1e97882e96fe6f8f4ccf6613f25255d364dd210e129cf0072c",
		"emulator/hybrid-clean.pem":  "ff2b7e6348b7dc1e97882e96fe6f8f4ccf6613f25255d364dd210e129cf0072c",
		"emulator/hybrid-rotate.pem": "70896594e5822d147b543ed6dd7bc5d6315d8e2fc857bc37b7d853d7705e6eb6",
	} {
		chain := readChain(t, file)
		require.Len(t, chain, 3, file)

		leaf, err := x509.ParseCertificate(chain[0])
		require.NoError(t, err)

		var extension []byte
		for _, ext := range leaf.Extensions {
			if ext.Id.Equal(oidKeyAttestation) {
				extension = ext.Value
			}
		}
		require.Len(t, extension, 343, file)

		kd, err := parseKeyDescription(extension)
		require.NoError(t, err, file)
		require.EqualValues(t, 400, kd.attestationVersion)
		require.EqualValues(t, 400, kd.keyMintVersion)
		require.Equal(t, Software, kd.attestationLevel)
		require.Equal(t, Software, kd.keyMintLevel)
		require.Equal(t, challenge, kd.challenge)

		require.Equal(t, authorizationList{}, kd.hardware, "%s: nothing is hardware-enforced on the emulator", file)
		require.NotNil(t, kd.software.origin)
		require.EqualValues(t, originGenerated, *kd.software.origin)
		require.NotNil(t, kd.software.rootOfTrust)
		require.Equal(t, make([]byte, 32), kd.software.rootOfTrust.bootKey)
		require.False(t, kd.software.rootOfTrust.deviceLocked)
		require.Equal(t, BootUnverified, kd.software.rootOfTrust.bootState)
		require.EqualValues(t, 202605, kd.software.osPatchLevel)

		id, err := parseApplicationID(kd.software.applicationID)
		require.NoError(t, err)
		require.Equal(t, []packageRecord{{name: "dev.probe.attest", version: 7}}, id.packages)
		require.Equal(t, [][]byte{mustHex(t, digest)}, id.digests, file)

		// The root is the emulator's own short-lived test root, which is
		// not Google's.
		root, err := x509.ParseCertificate(chain[2])
		require.NoError(t, err)
		key := sha256.Sum256(root.RawSubjectPublicKeyInfo)
		require.Equal(t, "7a8cea957d0c4970fdc9b3d5cacb9ef68780600c76d37881b7df6f6436c0055f", hex.EncodeToString(key[:]))
	}
}
