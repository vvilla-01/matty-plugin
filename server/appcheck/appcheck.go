// Package appcheck reads an Android key attestation: a certificate chain a
// phone's Keystore makes for a new key, in which the phone's secure hardware
// and Android say which app asked for the key, which certificate signed that
// app, and how the phone started. It is how the plugin learns, on Android's
// own word and not the app's, whether the Matty asking is the official build.
//
// Verify is a pure function: the chain, the challenge it must carry, a
// policy and the time in, a verdict and the facts it read out. It contacts
// nothing. Everything it checks against is passed in, or built in (data/,
// whose README says where each file came from and when).
//
// What a verdict of verified means, and what it cannot: it holds only while
// the phone is intact (its secure hardware not broken into, its factory
// attestation key not leaked and not yet revoked) and while the signing key
// of the certificate it accepts is safe. It proves which RSA certificate
// signed the app: Android attests the oldest certificate of an app's signing
// history, which for Matty is the RSA one, and never the post-quantum
// ML-DSA certificate that is its current signer. And a program that never
// asks Android for an attestation simply has none: this recognises the
// official app; it does not tell an impostor that hides from a browser.
package appcheck

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// Level is a SecurityLevel of the attestation: where the key and the
// attestation were made.
type Level int

const (
	Software           Level = 0
	TrustedEnvironment Level = 1
	StrongBox          Level = 2
)

// String is the level as the plugin says it: "software", "tee",
// "strongbox", or "unknown".
func (l Level) String() string {
	switch l {
	case Software:
		return "software"
	case TrustedEnvironment:
		return "tee"
	case StrongBox:
		return "strongbox"
	default:
		return "unknown"
	}
}

// BootState is the root of trust's VerifiedBootState.
type BootState int

const (
	BootVerified   BootState = 0
	BootSelfSigned BootState = 1
	BootUnverified BootState = 2
	BootFailed     BootState = 3

	// BootUnknown is no root of trust read at all.
	BootUnknown BootState = -1
)

// String is the state as the plugin says it.
func (b BootState) String() string {
	switch b {
	case BootVerified:
		return "verified"
	case BootSelfSigned:
		return "selfSigned"
	case BootUnverified:
		return "unverified"
	case BootFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Policy is what Verify accepts.
type Policy struct {
	// Roots are the SHA-256 digests, lower-case hex, of the
	// SubjectPublicKeyInfo of each root the chain may end in: GoogleRoots
	// for real phones. Pinned by key rather than by certificate, so that a
	// root Google issues again over the same key is the same root.
	Roots map[string]bool

	// Packages are the package names the app may have.
	Packages map[string]bool

	// Digests are the SHA-256 digests, lower-case hex, of the signing
	// certificates the app may be signed with.
	Digests map[string]bool

	// BootKeys are the verified-boot key digests, lower-case hex, accepted
	// for a phone whose system is signed by a key its owner put in
	// (SelfSigned): GrapheneOSBootKeys, and any an admin adds.
	BootKeys map[string]bool

	// Revoked are the serial numbers of attestation certificates Google
	// has revoked (RevokedSerials).
	Revoked map[string]bool

	// Development asks for what an emulator and a debug build produce to be
	// accepted as well: a software security level, any self-signed root,
	// any boot state, and a root of trust or an origin that only Android
	// vouches for. It means something only in the development build (the
	// villanet_standin tag); a release build ignores it entirely, and a
	// test holds that.
	Development bool
}

// development is whether this verification takes the development build's
// allowances: only in that build, and only when the policy asks.
func (p Policy) development() bool {
	return developmentBuild && p.Development
}

// Facts are what Verify read, as far as it got. Nothing in them tells one
// phone from another of the same model and system.
type Facts struct {
	AttestationVersion int64
	KeyMintVersion     int64

	// Level is the lower of the attestation's two security levels.
	Level Level

	Package     string
	VersionCode int64

	// Digest is the signing certificate's SHA-256, lower-case hex.
	Digest string

	BootState    BootState
	DeviceLocked bool

	// BootKey is the verified-boot key's digest, lower-case hex, when the
	// phone's system is signed by its owner's key (BootSelfSigned): the key
	// it was accepted for, so that a verdict can be judged again once that
	// key is no longer accepted.
	BootKey string

	// OSPatchLevel is YYYYMM, from the hardware-enforced list, or 0.
	OSPatchLevel int64

	// Factory is whether the chain is a factory-provisioned one (its
	// expired certificates are tolerated) rather than a remotely
	// provisioned one.
	Factory bool

	// Development is whether the verdict took the development build's
	// allowances (a software level, a root that is not Google's, a boot
	// state a real phone would be refused for).
	Development bool
}

// Result is a verdict: verified, or not and why, in a sentence a person can
// act on.
type Result struct {
	Verified bool
	Reason   string
	Facts    Facts
}

// MaxChain is the most certificates a chain may have. Real ones have three
// to five.
const MaxChain = 10

// knownVersions are the attestation versions this package reads: KeyMaster
// 3.0 to 4.1 and KeyMint 1 to 5, everything an Android 17 phone may report.
// A later KeyMint (a multiple of 100 above them) is read the same way, since
// every version so far has only added fields.
var knownVersions = map[int64]bool{2: true, 3: true, 4: true, 100: true, 200: true, 300: true, 400: true, 500: true}

func knownVersion(v int64) bool {
	return knownVersions[v] || (v > 500 && v%100 == 0)
}

var (
	oidKeyAttestation = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}
	oidSerialNumber   = asn1.ObjectIdentifier{2, 5, 4, 5}
)

func refuse(facts Facts, format string, args ...any) Result {
	return Result{Reason: fmt.Sprintf(format, args...), Facts: facts}
}

// Verify decides whether chain, leaf first and root last as Android's
// Keystore returns it, is a hardware attestation of a key made for
// challenge by an app the policy accepts, on a phone the policy accepts,
// at now. It stops at the first rule broken.
func Verify(chain [][]byte, challenge []byte, policy Policy, now time.Time) Result {
	facts := Facts{BootState: BootUnknown}
	dev := policy.development()

	// The chain.
	switch {
	case len(chain) == 0:
		return refuse(facts, "The app sent no certificate chain.")
	case len(chain) == 1:
		return refuse(facts, "The app sent a single certificate where a phone sends a chain of them.")
	case len(chain) > MaxChain:
		return refuse(facts, "The app sent a chain of %d certificates, more than any phone makes.", len(chain))
	}

	certs := make([]*x509.Certificate, len(chain))
	for i, der := range chain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return refuse(facts, "Certificate %d of the app's chain could not be read.", i+1)
		}
		certs[i] = cert
	}

	// The root first, by its key, before any signature is checked; and then
	// the signatures from the root down, so that no key the chain carries is
	// used before the one above it has vouched for it. A chain made under
	// somebody else's root, or with a key of their choosing anywhere in it,
	// then costs this server no more than a signature or two checked with
	// Google's own keys.
	last := len(certs) - 1
	root := certs[last]
	if !bytes.Equal(root.RawSubject, root.RawIssuer) {
		return refuse(facts, "The app's chain does not end in a root certificate that signed itself.")
	}

	rootKey := sha256.Sum256(root.RawSubjectPublicKeyInfo)
	if !policy.Roots[hex.EncodeToString(rootKey[:])] {
		if !dev {
			return refuse(facts, "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.")
		}
		facts.Development = true
	}

	if root.CheckSignature(root.SignatureAlgorithm, root.RawTBSCertificate, root.Signature) != nil {
		return refuse(facts, "The app's chain does not end in a root certificate that signed itself.")
	}

	// Every certificate that signs another is a certificate authority, but
	// for one: on a factory-provisioned chain (root, factory intermediate,
	// the phone's attestation key, the attested key), the attestation key's
	// certificate, which some phones were given without the flag (Google's
	// test chains from a Pixel 2 and a Sony Xperia 10 III have it so). Its
	// key lives in the secure hardware and signs nothing but attestations.
	// It is told by its place from the root, so a certificate somebody puts
	// in front of the chain does not move the exception onto the app's own.
	factory := factoryProvisioned(certs)
	for i := last - 1; i >= 0; i-- {
		parent := certs[i+1]
		attestationKey := factory && i+1 == last-2
		if (!parent.BasicConstraintsValid || !parent.IsCA) && !attestationKey {
			return refuse(facts, "Certificate %d of the app's chain signs another but is not a certificate authority.", i+2)
		}
		if parent.CheckSignature(certs[i].SignatureAlgorithm, certs[i].RawTBSCertificate, certs[i].Signature) != nil {
			return refuse(facts, "Certificate %d of the app's chain is not signed by the one after it.", i+1)
		}
	}

	// Only the first certificate may carry the attestation: one further up
	// was not written by the phone's secure hardware, and is how a chain
	// extended by somebody else looks.
	var extension []byte
	for i, cert := range certs {
		for _, ext := range cert.Extensions {
			if !ext.Id.Equal(oidKeyAttestation) {
				continue
			}
			if i > 0 {
				return refuse(facts, "Certificate %d of the app's chain carries an attestation, which only the first may: the chain was extended by somebody else.", i+1)
			}
			if extension != nil {
				return refuse(facts, "The app's certificate carries two attestations.")
			}
			extension = ext.Value
		}
	}
	if extension == nil {
		return refuse(facts, "The app's certificate carries no attestation.")
	}

	// Validity, as Google's own verifier treats it: never the first
	// certificate's (the phone sets it, and its clock may be wrong), nor the
	// root's (trusted for its key, as the anchor); the ones between always,
	// except that a factory-provisioned chain's may have expired (Google:
	// such keys "are still trustworthy unless they appear in the
	// certificate revocation list").
	facts.Factory = factory
	for i := 1; i < last; i++ {
		switch {
		case now.Before(certs[i].NotBefore):
			return refuse(facts, "Certificate %d of the app's chain is not valid until %s.", i+1, certs[i].NotBefore.UTC().Format(time.DateOnly))
		case now.After(certs[i].NotAfter) && !facts.Factory:
			return refuse(facts, "Certificate %d of the app's chain expired on %s.", i+1, certs[i].NotAfter.UTC().Format(time.DateOnly))
		}
	}

	for i, cert := range certs {
		if revoked(policy.Revoked, cert) {
			return refuse(facts, "Google has revoked certificate %d of this phone's attestation chain (serial %s), so its secure hardware can no longer be trusted.", i+1, cert.SerialNumber.Text(16))
		}
	}

	// The attestation.
	kd, err := parseKeyDescription(extension)
	if err != nil {
		return refuse(facts, "The app's attestation could not be read: %v.", err)
	}

	facts.AttestationVersion = kd.attestationVersion
	facts.KeyMintVersion = kd.keyMintVersion
	facts.Level = min(kd.attestationLevel, kd.keyMintLevel)
	facts.OSPatchLevel = kd.hardware.osPatchLevel

	if !knownVersion(kd.attestationVersion) {
		return refuse(facts, "The app's attestation is in a format this server does not know (version %d).", kd.attestationVersion)
	}

	for _, level := range []Level{kd.attestationLevel, kd.keyMintLevel} {
		switch {
		case level == TrustedEnvironment || level == StrongBox:
		case level == Software && dev:
			facts.Development = true
		case level == Software:
			return refuse(facts, "This phone made its attestation in software, not in its secure hardware.")
		default:
			return refuse(facts, "The app's attestation names a security level this server does not know (%d).", level)
		}
	}

	if len(challenge) == 0 || subtle.ConstantTimeCompare(kd.challenge, challenge) != 1 {
		return refuse(facts, "The app's attestation was made for another challenge, so it is not an answer to this one.")
	}

	origin := kd.hardware.origin
	if origin == nil && dev {
		origin = kd.software.origin
	}
	switch {
	case origin == nil:
		return refuse(facts, "The phone's secure hardware did not say where the attested key was made.")
	case *origin != originGenerated:
		return refuse(facts, "The attested key was put into the phone, not made inside it.")
	}

	// The root of trust is the secure hardware's word on how the phone
	// started, and only its word counts: a copy in the list Android fills
	// is Android's, which is what it vouches for.
	rot := kd.hardware.rootOfTrust
	if rot == nil && dev {
		rot = kd.software.rootOfTrust
		if rot != nil {
			facts.Development = true
		}
	}
	if rot != nil {
		facts.BootState = rot.bootState
		facts.DeviceLocked = rot.deviceLocked
		if rot.bootState == BootSelfSigned {
			facts.BootKey = hex.EncodeToString(rot.bootKey)
		}
	}
	if reason := bootRefusal(rot, policy.BootKeys); reason != "" {
		if !dev {
			return refuse(facts, "%s", reason)
		}
		facts.Development = true
	}

	// Which app asked: Android's word, in the list it fills.
	if kd.software.applicationID == nil {
		return refuse(facts, "The app's attestation does not say which app asked.")
	}
	id, err := parseApplicationID(kd.software.applicationID)
	if err != nil {
		return refuse(facts, "The app's attestation does not say readably which app asked: %v.", err)
	}
	if len(id.packages) != 1 {
		return refuse(facts, "The attestation names %d apps sharing one identity, so it cannot say which of them asked.", len(id.packages))
	}
	facts.Package = id.packages[0].name
	facts.VersionCode = id.packages[0].version
	if !policy.Packages[facts.Package] {
		return refuse(facts, "The app that asked is %s, not Matty.", printable(facts.Package))
	}
	if len(id.digests) != 1 {
		return refuse(facts, "The attestation names %d signing certificates for the app; Matty has one.", len(id.digests))
	}
	facts.Digest = hex.EncodeToString(id.digests[0])
	if !policy.Digests[facts.Digest] {
		return refuse(facts, "This copy of Matty was signed with a certificate this server does not accept (%s).", ShortDigest(facts.Digest))
	}

	if kd.software.allApplications || kd.hardware.allApplications {
		return refuse(facts, "The attested key may be used by every app on the phone, so it says nothing about which app asked.")
	}

	return Result{Verified: true, Facts: facts}
}

// bootRefusal is why the root of trust is not one a phone is accepted with,
// or "": the bootloader locked, and the system verified by the
// manufacturer's key, or by its owner's key when that key is accepted.
func bootRefusal(rot *rootOfTrust, bootKeys map[string]bool) string {
	switch {
	case rot == nil:
		return "This phone's secure hardware did not say how the phone started."
	case !rot.deviceLocked:
		return "This phone's bootloader is unlocked."
	}

	switch rot.bootState {
	case BootVerified:
		return ""
	case BootSelfSigned:
		key := hex.EncodeToString(rot.bootKey)
		if bootKeys[key] {
			return ""
		}

		return fmt.Sprintf("This phone runs a system signed with its owner's own key (%s), which this server does not accept.", ShortDigest(key))
	case BootUnverified:
		return "This phone's system is not verified."
	case BootFailed:
		return "This phone's verified boot failed."
	default:
		return fmt.Sprintf("This phone reported a boot state this server does not know (%d).", rot.bootState)
	}
}

// factoryProvisioned is Google's test: the certificate under the root of a
// factory-provisioned chain names a serial number. A remotely provisioned
// one's is Google's own CA.
func factoryProvisioned(certs []*x509.Certificate) bool {
	if len(certs) < 3 {
		return false
	}

	for _, name := range certs[len(certs)-2].Subject.Names {
		if name.Type.Equal(oidSerialNumber) {
			return true
		}
	}

	return false
}

// revoked is whether a certificate's serial is on Google's list. The list
// names most serials in lower-case hex, as Google says it does and as
// Google's verifier looks them up, and some in decimal: both are looked up.
func revoked(list map[string]bool, cert *x509.Certificate) bool {
	if cert.SerialNumber == nil || len(list) == 0 {
		return false
	}

	return list[cert.SerialNumber.Text(16)] || list[cert.SerialNumber.String()]
}

// printable keeps a package name from an attestation fit to put in a
// sentence: what is not printable ASCII is shown as a question mark, and a
// long one is cut.
func printable(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i >= 100 {
			b.WriteString("…")
			break
		}
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		b.WriteRune(r)
	}

	return b.String()
}

// ShortDigest is a digest as a person compares it: its first eight bytes in
// upper-case hex with colons, and an ellipsis.
func ShortDigest(digest string) string {
	digest = strings.ToUpper(digest)
	if len(digest) > 16 {
		digest = digest[:16]
	}

	var parts []string
	for i := 0; i+2 <= len(digest); i += 2 {
		parts = append(parts, digest[i:i+2])
	}

	return strings.Join(parts, ":") + "…"
}

// ParseDigest reads a SHA-256 digest as people write one: hex, upper or
// lower case, with or without colons or spaces between the bytes. It is the
// digest in lower-case hex, and whether it is one.
func ParseDigest(s string) (string, bool) {
	cleaned := strings.NewReplacer(":", "", " ", "", "\t", "").Replace(strings.TrimSpace(s))
	cleaned = strings.ToLower(cleaned)
	if len(cleaned) != 2*sha256.Size {
		return "", false
	}
	if _, err := hex.DecodeString(cleaned); err != nil {
		return "", false
	}

	return cleaned, true
}

// ChallengeHeader is the first line of what the attestation challenge is the
// digest of: it says what the challenge is for, so no other protocol's
// challenge can be the same bytes.
const ChallengeHeader = "matty-app-check 1"

// DeriveChallenge is the attestation challenge an app puts in its key for
// the server's challenge: SHA-256 of "matty-app-check 1", the origin it
// reached the server at (Origin), the user's id and the challenge as the
// server sent it, base64, each on a line of its own, with no newline after
// the last. Binding the origin and the user means an attestation made for
// one server or one person is no answer for another.
func DeriveChallenge(origin, userID, challenge string) []byte {
	sum := sha256.Sum256([]byte(ChallengeHeader + "\n" + origin + "\n" + userID + "\n" + challenge))

	return sum[:]
}

// Origin is an address as the challenge names it: scheme://host[:port],
// lower case, with the scheme's default port left out and any path, query
// or user dropped, and the host as the app's HTTP client writes it
// (canonicalHost). Only http and https.
func Origin(address string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return "", err
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%q is not an http or https address", address)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("%q has no host", address)
	}
	if host, err = canonicalHost(host); err != nil {
		return "", fmt.Errorf("%q: %w", address, err)
	}

	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("%q has no usable port", address)
		}
		port = strconv.Itoa(number)
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	if port == "" {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}

		return scheme + "://" + host, nil
	}

	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// hostNames turns a host name that is not ASCII into its xn-- form as
// Java's IDN does (IDNA2003), as near as UTS #46 comes to it: with the
// transitional mapping, which writes ß as ss and ς as σ the way IDNA2003
// does.
var hostNames = idna.New(idna.MapForLookup(), idna.Transitional(true), idna.BidiRule())

// canonicalHost is a lower-case host as the app's HTTP client (OkHttp's
// HttpUrl) writes it, so that the two sides derive the challenge from the
// same origin: an IPv6 address in its shortest form (RFC 5952), one that
// maps an IPv4 address as that IPv4 address, and a name that is not ASCII
// in its xn-- form. Anything else is left as it is.
func canonicalHost(host string) (string, error) {
	if strings.Contains(host, ":") {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap().String(), nil
		}

		return host, nil
	}

	for i := range len(host) {
		if host[i] >= utf8.RuneSelf {
			ascii, err := hostNames.ToASCII(host)
			if err != nil {
				return "", fmt.Errorf("the host name %s cannot be written in its xn-- form; write the address with the name in that form", printable(host))
			}

			return strings.ToLower(ascii), nil
		}
	}

	return host, nil
}
