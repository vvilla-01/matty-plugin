package release

import (
	"bytes"
	"crypto"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The keys every test signs with: made here, once, and never written down.
type testKeys struct {
	rsa   *rsa.PrivateKey
	mldsa *mldsa.PrivateKey
	pins  Pins

	rsaCert, mldsaCert []byte // DER
}

var (
	keysOnce sync.Once
	keys     testKeys
)

func theKeys(t *testing.T) testKeys {
	t.Helper()

	keysOnce.Do(func() {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			panic(err)
		}
		mldsaKey, err := mldsa.GenerateKey(mldsa.MLDSA87())
		if err != nil {
			panic(err)
		}
		keys = testKeys{rsa: rsaKey, mldsa: mldsaKey}
		keys.rsaCert = selfSigned(rsaKey.Public(), rsaKey)
		keys.mldsaCert = selfSigned(mldsaKey.PublicKey(), mldsaKey)
		keys.pins, err = NewPins(keys.rsaCert, keys.mldsaCert)
		if err != nil {
			panic(err)
		}
	})

	return keys
}

func selfSigned(public any, signer crypto.Signer) []byte {
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, signer)
	if err != nil {
		panic(err)
	}

	return der
}

func pemOf(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// A bundle as make dist-linux lays one out, with a large executable.
func newBundle(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	files := map[string][]byte{
		"plugin.json":                    []byte(`{"id":"ceo.villa.matty","version":"4.0.0","server":{"executables":{"linux-amd64":"server/dist/plugin-linux-amd64"}},"webapp":{"bundle_path":"webapp/dist/main.js"},"icon_path":"assets/icon.svg"}`),
		"assets/icon.svg":                []byte("<svg/>"),
		"server/dist/plugin-linux-amd64": bytes.Repeat([]byte("executable "), 300000),
		"webapp/dist/main.js":            []byte("console.log('panel')"),
	}
	for name, content := range files {
		write(t, dir, name, content)
	}

	return dir
}

func write(t *testing.T, dir, name string, content []byte) {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, content, 0o644))
}

// manifestOf is the manifest of every regular file in dir outside the release
// folder, as build/sign-release.sh writes one.
func manifestOf(t *testing.T, dir, version, platform string) []byte {
	t.Helper()

	var paths []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() || rel == Dir || strings.HasPrefix(rel, Dir+"/") {
			return nil
		}
		paths = append(paths, rel)
		return nil
	}))
	sort.Strings(paths)

	var b strings.Builder
	fmt.Fprintf(&b, "%s\nproduct %s\nkind %s\nversion %s\nplatform %s\nfiles %d\n", Header, Product, Kind, version, platform, len(paths))
	for _, p := range paths {
		content, err := os.ReadFile(filepath.Join(dir, p))
		require.NoError(t, err)
		sum := sha256.Sum256(content)
		fmt.Fprintf(&b, "%s %d %s\n", hex.EncodeToString(sum[:]), len(content), p)
	}

	return []byte(b.String())
}

func rsaSign(t *testing.T, key *rsa.PrivateKey, message []byte) []byte {
	t.Helper()

	digest := sha512.Sum512(message)
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA512, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	require.NoError(t, err)

	return sig
}

func mldsaSign(t *testing.T, key *mldsa.PrivateKey, message []byte, context string) []byte {
	t.Helper()

	sig, err := key.Sign(nil, message, &mldsa.Options{Context: context})
	require.NoError(t, err)

	return sig
}

// sign puts a manifest and both signatures over it into the release folder.
func sign(t *testing.T, dir string, manifest []byte) {
	t.Helper()

	k := theKeys(t)
	write(t, dir, ManifestPath, manifest)
	write(t, dir, RSASignaturePath, rsaSign(t, k.rsa, manifest))
	write(t, dir, MLDSASignaturePath, mldsaSign(t, k.mldsa, manifest, ""))
}

func signedBundle(t *testing.T) string {
	t.Helper()

	dir := newBundle(t)
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64"))

	return dir
}

var here = Expect{Version: "4.0.0", Platform: "linux-amd64"}

func checkOf(t *testing.T, dir string) Finding {
	t.Helper()
	return CheckDir(dir, theKeys(t).pins, here)
}

func requireAltered(t *testing.T, f Finding, reasonPart string) {
	t.Helper()

	require.Equal(t, Altered, f.State, f.Reason)
	require.Contains(t, f.Reason, reasonPart)
}

// The certificates built in are the two Matty's releases publish, RSA 4096
// and ML-DSA-87, digest for digest.
func TestTheBuiltInCertificatesAreThePublishedOnes(t *testing.T) {
	pins, err := OfficialPins()
	require.NoError(t, err)

	require.Equal(t, "E5:52:50:BE:A6:69:00:39:B7:84:9E:33:B0:49:08:8E:CB:B9:98:D9:F2:0A:AA:8C:7E:B2:DC:5A:43:63:27:3C", Colons(pins.RSASHA256))
	require.Equal(t, "75:78:CB:63:B4:3E:CC:6F:B1:27:64:51:54:F5:22:FB:15:D1:3A:26:08:E4:59:BC:F4:2A:7C:D1:41:86:A9:49", Colons(pins.MLDSASHA256))

	// The same digests taken straight from the embedded files.
	for pemFile, want := range map[string]string{"pins/matty-rsa.pem": OfficialRSASHA256, "pins/matty-mldsa87.pem": OfficialMLDSASHA256} {
		raw, err := os.ReadFile(pemFile)
		require.NoError(t, err)
		block, rest := pem.Decode(raw)
		require.NotNil(t, block)
		require.Empty(t, bytes.TrimSpace(rest))
		sum := sha256.Sum256(block.Bytes)
		require.Equal(t, want, hex.EncodeToString(sum[:]), pemFile)
	}

	require.Equal(t, 4096, pins.rsa.N.BitLen())
	require.Equal(t, mldsa.MLDSA87(), pins.mldsa.Parameters())
}

func TestPinsAreHeldToTheirKeyTypes(t *testing.T) {
	k := theKeys(t)

	_, err := NewPins(k.mldsaCert, k.rsaCert)
	require.Error(t, err, "swapped")

	small, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	_, err = NewPins(selfSigned(small.Public(), small), k.mldsaCert)
	require.ErrorContains(t, err, "RSA 4096")

	mid, err := mldsa.GenerateKey(mldsa.MLDSA65())
	require.NoError(t, err)
	_, err = NewPins(k.rsaCert, selfSigned(mid.PublicKey(), mid))
	require.ErrorContains(t, err, "ML-DSA-87")

	_, err = NewPins(append(pemOf(k.rsaCert), pemOf(k.rsaCert)...), k.mldsaCert)
	require.ErrorContains(t, err, "more than one")

	pins, err := NewPins(pemOf(k.rsaCert), pemOf(k.mldsaCert))
	require.NoError(t, err)
	require.Equal(t, k.pins.RSASHA256, pins.RSASHA256)
}

func TestASignedReleaseIsSigned(t *testing.T) {
	dir := signedBundle(t)

	f := checkOf(t, dir)
	require.Equal(t, Signed, f.State, f.Reason)
	require.Equal(t, "4.0.0", f.Version)
	require.Equal(t, []string{"linux-amd64"}, f.Platforms)
	require.Equal(t, 4, f.Files)
	require.Zero(t, f.UnlistedCount)

	manifest, err := os.ReadFile(filepath.Join(dir, ManifestPath))
	require.NoError(t, err)
	require.Equal(t, manifest, f.Manifest)
	sum := sha256.Sum256(manifest)
	require.Equal(t, hex.EncodeToString(sum[:]), f.ManifestSHA256)
	require.Len(t, f.RSASignature, RSASignatureSize)
	require.Len(t, f.MLDSASignature, MLDSASignatureSize)

	// A checker that asks for no platform in particular checks them all.
	require.Equal(t, Signed, CheckDir(dir, theKeys(t).pins, Expect{Version: "4.0.0"}).State)
}

func TestNoReleaseFolderIsUnsigned(t *testing.T) {
	f := checkOf(t, newBundle(t))
	require.Equal(t, Unsigned, f.State)
	require.Empty(t, f.Reason)
	require.Nil(t, f.Manifest)

	dir := newBundle(t)
	write(t, dir, Dir, []byte("not a folder"))
	requireAltered(t, checkOf(t, dir), "not a folder")
}

// Both signatures, always: one good and the other bad or missing is altered.
func TestBothSignaturesMustVerify(t *testing.T) {
	k := theKeys(t)

	cases := map[string]struct {
		mend   func(t *testing.T, dir string, manifest []byte)
		reason string
	}{
		"the RSA signature zeroed": {func(t *testing.T, dir string, _ []byte) {
			write(t, dir, RSASignaturePath, make([]byte, RSASignatureSize))
		}, "the RSA signature does not verify"},
		"the ML-DSA signature with one bit changed": {func(t *testing.T, dir string, _ []byte) {
			sig, err := os.ReadFile(filepath.Join(dir, MLDSASignaturePath))
			require.NoError(t, err)
			sig[100] ^= 1
			write(t, dir, MLDSASignaturePath, sig)
		}, "the ML-DSA signature does not verify"},
		"the RSA signature missing": {func(t *testing.T, dir string, _ []byte) {
			require.NoError(t, os.Remove(filepath.Join(dir, RSASignaturePath)))
		}, RSASignaturePath + " is missing"},
		"the ML-DSA signature missing": {func(t *testing.T, dir string, _ []byte) {
			require.NoError(t, os.Remove(filepath.Join(dir, MLDSASignaturePath)))
		}, MLDSASignaturePath + " is missing"},
		"the manifest missing": {func(t *testing.T, dir string, _ []byte) {
			require.NoError(t, os.Remove(filepath.Join(dir, ManifestPath)))
		}, ManifestPath + " is missing"},
		"the RSA signature cut short": {func(t *testing.T, dir string, _ []byte) {
			sig, err := os.ReadFile(filepath.Join(dir, RSASignaturePath))
			require.NoError(t, err)
			write(t, dir, RSASignaturePath, sig[:511])
		}, "not 512 bytes"},
		"the ML-DSA signature too long": {func(t *testing.T, dir string, _ []byte) {
			sig, err := os.ReadFile(filepath.Join(dir, MLDSASignaturePath))
			require.NoError(t, err)
			write(t, dir, MLDSASignaturePath, append(sig, 0))
		}, "too large"},
		// The context must be empty: it is all the app's platform verifies.
		"the ML-DSA signature made with a context": {func(t *testing.T, dir string, manifest []byte) {
			write(t, dir, MLDSASignaturePath, mldsaSign(t, k.mldsa, manifest, "ceo.villa.matty/release-manifest/1"))
		}, "the ML-DSA signature does not verify"},
		// RSA-PSS with another salt length is another signature.
		"the RSA signature with a 32-byte salt": {func(t *testing.T, dir string, manifest []byte) {
			digest := sha512.Sum512(manifest)
			sig, err := rsa.SignPSS(rand.Reader, k.rsa, crypto.SHA512, digest[:], &rsa.PSSOptions{SaltLength: 32})
			require.NoError(t, err)
			write(t, dir, RSASignaturePath, sig)
		}, "the RSA signature does not verify"},
		"a signature file that is a link": {func(t *testing.T, dir string, _ []byte) {
			path := filepath.Join(dir, RSASignaturePath)
			require.NoError(t, os.Rename(path, path+".real"))
			require.NoError(t, os.Symlink("manifest.rsa.sig.real", path))
		}, "not a regular file"},
		"both bad": {func(t *testing.T, dir string, _ []byte) {
			write(t, dir, RSASignaturePath, make([]byte, RSASignatureSize))
			write(t, dir, MLDSASignaturePath, make([]byte, MLDSASignatureSize))
		}, "the RSA signature does not verify; the ML-DSA signature does not verify"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newBundle(t)
			manifest := manifestOf(t, dir, "4.0.0", "linux-amd64")
			sign(t, dir, manifest)
			c.mend(t, dir, manifest)

			requireAltered(t, checkOf(t, dir), c.reason)
		})
	}
}

// Signed with keys that are not the pinned ones: here, the test's own
// release held to the certificates built in.
func TestAReleaseSignedWithOtherKeysIsAltered(t *testing.T) {
	official, err := OfficialPins()
	require.NoError(t, err)

	f := CheckDir(signedBundle(t), official, here)
	requireAltered(t, f, "the RSA signature does not verify; the ML-DSA signature does not verify")
}

func TestFilesThatAreNotAsSignedAreAltered(t *testing.T) {
	cases := map[string]struct {
		mend   func(t *testing.T, dir string)
		file   string
		reason string
	}{
		"a byte of the executable changed": {func(t *testing.T, dir string) {
			path := filepath.Join(dir, "server/dist/plugin-linux-amd64")
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			content[len(content)/2] ^= 1
			require.NoError(t, os.WriteFile(path, content, 0o755))
		}, "server/dist/plugin-linux-amd64", "has changed"},
		"the webapp grown": {func(t *testing.T, dir string) {
			write(t, dir, "webapp/dist/main.js", []byte("console.log('panel');alert(1)"))
		}, "webapp/dist/main.js", "has changed"},
		"plugin.json changed": {func(t *testing.T, dir string) {
			write(t, dir, "plugin.json", []byte(`{"id":"ceo.villa.matty","version":"4.0.0"}`))
		}, "plugin.json", "plugin.json has changed"},
		"the icon gone": {func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "assets/icon.svg")))
		}, "assets/icon.svg", "is missing"},
		"the icon a link to a copy of itself": {func(t *testing.T, dir string) {
			path := filepath.Join(dir, "assets/icon.svg")
			require.NoError(t, os.Rename(path, filepath.Join(dir, "icon-copy.svg")))
			require.NoError(t, os.Symlink("../icon-copy.svg", path))
		}, "assets/icon.svg", "not a regular file"},
		"a link nobody lists": {func(t *testing.T, dir string) {
			require.NoError(t, os.Symlink("/etc/hostname", filepath.Join(dir, "assets/elsewhere")))
		}, "assets/elsewhere", "not a regular file"},
		"a folder that is a link": {func(t *testing.T, dir string) {
			require.NoError(t, os.Rename(filepath.Join(dir, "assets"), filepath.Join(dir, "assets-real")))
			require.NoError(t, os.Symlink("assets-real", filepath.Join(dir, "assets")))
		}, "assets", "not a regular file"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := signedBundle(t)
			c.mend(t, dir)

			f := checkOf(t, dir)
			requireAltered(t, f, c.reason)
			require.Equal(t, c.file, f.File)
		})
	}
}

// Validly signed manifests that say the wrong thing, or say it wrongly.
func TestManifestsThatAreNotThisReleaseAreAltered(t *testing.T) {
	cases := map[string]struct {
		edit   func(m string) string
		reason string
	}{
		"another version":  {func(m string) string { return strings.Replace(m, "version 4.0.0", "version 4.0.1", 1) }, "signed as version 4.0.1"},
		"another platform": {func(m string) string { return strings.Replace(m, "platform linux-amd64", "platform linux-arm64", 1) }, "signed for linux-arm64"},
		"another product":  {func(m string) string { return strings.Replace(m, "product "+Product, "product ceo.villa.other", 1) }, `for "ceo.villa.other"`},
		"another kind":     {func(m string) string { return strings.Replace(m, "kind plugin", "kind app", 1) }, `of a "app"`},
		"another header":   {func(m string) string { return strings.Replace(m, Header, "matty-release-manifest 2", 1) }, "first line"},
		"a count that is one short": {func(m string) string {
			return strings.Replace(m, "files 4", "files 3", 1)
		}, "says 3 files and lists 4"},
		"a count with a leading zero": {func(m string) string { return strings.Replace(m, "files 4", "files 04", 1) }, "not a number"},
		"a carriage return":           {func(m string) string { return strings.Replace(m, "\n", "\r\n", 1) }, "printable ASCII"},
		"no final line feed":          {func(m string) string { return strings.TrimSuffix(m, "\n") }, "not ending in a line feed"},
		"a blank line": {func(m string) string {
			return strings.Replace(m, "files 4\n", "files 4\n\n", 1)
		}, "says 4 files and lists 5"},
		"unsorted entries": {func(m string) string {
			lines := strings.Split(m, "\n")
			lines[6], lines[7] = lines[7], lines[6]
			return strings.Join(lines, "\n")
		}, "out of order or listed twice"},
		"an entry listed twice": {func(m string) string {
			lines := strings.Split(m, "\n")
			lines[7] = lines[6]
			return strings.Join(lines, "\n")
		}, "out of order or listed twice"},
		"a path that climbs out": {func(m string) string {
			return strings.Replace(m, " assets/icon.svg", " ../assets/icon.svg", 1)
		}, "has a . or .. in it"},
		"a path with a dot part": {func(m string) string {
			return strings.Replace(m, " assets/icon.svg", " assets/./icon.svg", 1)
		}, "has a . or .. in it"},
		"an absolute path": {func(m string) string {
			return strings.Replace(m, " assets/icon.svg", " /assets/icon.svg", 1)
		}, "not a plain relative path"},
		"a path in the release folder": {func(m string) string {
			return strings.Replace(m, " assets/icon.svg", " release/icon.svg", 1)
		}, "in the release folder"},
		"upper-case hex": {func(m string) string {
			lines := strings.Split(m, "\n")
			lines[6] = strings.ToUpper(lines[6][:64]) + lines[6][64:]
			return strings.Join(lines, "\n")
		}, "is not a file"},
		"a size that disagrees": {func(m string) string {
			return strings.Replace(m, " 6 assets/icon.svg", " 7 assets/icon.svg", 1)
		}, "assets/icon.svg has changed"},
		"a size with a leading zero": {func(m string) string {
			return strings.Replace(m, " 6 assets/icon.svg", " 06 assets/icon.svg", 1)
		}, "is not a file"},
		"plugin.json not listed": {func(m string) string { return dropLine(m, "plugin.json") }, "does not list plugin.json"},
		"the executable not listed": {func(m string) string {
			return dropLine(m, "server/dist/plugin-linux-amd64")
		}, "does not list server/dist/plugin-linux-amd64, which Mattermost loads"},
		"the webapp bundle not listed": {func(m string) string {
			return dropLine(m, "webapp/dist/main.js")
		}, "does not list webapp/dist/main.js, which Mattermost loads"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newBundle(t)
			sign(t, dir, []byte(c.edit(string(manifestOf(t, dir, "4.0.0", "linux-amd64")))))

			requireAltered(t, checkOf(t, dir), c.reason)
		})
	}
}

// dropLine takes one file out of a manifest and keeps the count right.
func dropLine(m, path string) string {
	lines := strings.Split(strings.TrimSuffix(m, "\n"), "\n")
	var kept []string
	for _, line := range lines {
		if !strings.HasSuffix(line, " "+path) {
			kept = append(kept, line)
		}
	}
	kept[5] = fmt.Sprintf("files %d", len(kept)-6)

	return strings.Join(kept, "\n") + "\n"
}

// An edit to the manifest after it was signed: its signatures no longer
// cover it.
func TestAManifestEditedAfterSigningIsAltered(t *testing.T) {
	dir := signedBundle(t)
	path := filepath.Join(dir, ManifestPath)
	manifest, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bytes.Replace(manifest, []byte("version 4.0.0"), []byte("version 4.0.1"), 1), 0o644))

	f := checkOf(t, dir)
	requireAltered(t, f, "the RSA signature does not verify; the ML-DSA signature does not verify")
	require.Empty(t, f.Version, "nothing in an unverified manifest is believed")
}

func TestPluginJSONMustBeTheReleasesVersion(t *testing.T) {
	dir := newBundle(t)
	write(t, dir, "plugin.json", []byte(`{"id":"ceo.villa.matty","version":"4.0.1","server":{"executables":{"linux-amd64":"server/dist/plugin-linux-amd64"}},"webapp":{"bundle_path":"webapp/dist/main.js"}}`))
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64"))

	requireAltered(t, checkOf(t, dir), "plugin.json says version 4.0.1")
}

// Files on disk that nobody signed are counted and named, and are no
// failure where Mattermost does not read them: it runs what plugin.json
// names, and plugin.json is signed.
func TestFilesNobodyListedAreCountedNotFailed(t *testing.T) {
	dir := signedBundle(t)
	write(t, dir, "notes.txt", []byte("left by somebody"))
	write(t, dir, "server/dist/plugin-linux-arm64", []byte("another"))
	write(t, dir, Dir+"/README", []byte("beside the signatures"))
	for i := range 12 {
		write(t, dir, fmt.Sprintf("extra/%02d", i), []byte("x"))
	}

	f := checkOf(t, dir)
	require.Equal(t, Signed, f.State, f.Reason)
	require.Equal(t, 15, f.UnlistedCount)
	require.Len(t, f.Unlisted, MaxUnlistedShown)
	require.Equal(t, "extra/00", f.Unlisted[0])
}

// Three kinds of file nobody signed are read by Mattermost, and make a
// release altered: a plugin.yml or plugin.yaml, which it reads in place of
// plugin.json (model.FindManifest), signed or not, any file in the web
// panel's bundle's folder, all of which it copies and serves, and any file
// in the public folder, which it serves to anybody.
func TestFilesMattermostReadsInPlaceOfTheSignedOnesAreAltered(t *testing.T) {
	other := []byte("id: ceo.villa.matty\nversion: 4.0.0\nwebapp:\n  bundle_path: webapp/dist/other.js\n")
	for _, name := range []string{"plugin.yml", "plugin.yaml"} {
		dir := signedBundle(t)
		write(t, dir, name, other)
		write(t, dir, "webapp/dist/other.js", []byte("console.log('another panel')"))
		f := checkOf(t, dir)
		requireAltered(t, f, "Mattermost reads "+name+" before plugin.json")
		require.Equal(t, name, f.File)

		// Listed in the manifest and signed with the rest: still not a
		// release, since Mattermost would not read the signed plugin.json.
		dir = newBundle(t)
		write(t, dir, name, other)
		sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64"))
		requireAltered(t, checkOf(t, dir), "Mattermost reads "+name+" before plugin.json")

		// Whatever it is.
		dir = signedBundle(t)
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o755))
		requireAltered(t, checkOf(t, dir), "Mattermost reads "+name+" before plugin.json")
	}

	for _, name := range []string{"webapp/dist/x.html", "webapp/dist/images/y.svg"} {
		dir := signedBundle(t)
		write(t, dir, name, []byte("<script>alert(1)</script>"))
		f := checkOf(t, dir)
		requireAltered(t, f, name+" is not in the release, and Mattermost serves every file in webapp/dist with the web panel")
		require.Equal(t, name, f.File)
	}

	for _, name := range []string{"public/x.html", "public/images/y.svg", "Public/x.html", "public"} {
		dir := signedBundle(t)
		write(t, dir, name, []byte("<script>alert(1)</script>"))
		f := checkOf(t, dir)
		requireAltered(t, f, name+" is not in the release, and Mattermost serves every file in public to anybody")
		require.Equal(t, name, f.File)
	}

	// A public folder the release lists and signs is the release's own.
	dir := newBundle(t)
	write(t, dir, "public/x.html", []byte("<p>signed</p>"))
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64"))
	require.Equal(t, Signed, checkOf(t, dir).State)

	// Beside those folders and not in them, Mattermost serves nothing.
	dir = signedBundle(t)
	write(t, dir, "assets/public/x.html", []byte("not the public folder"))
	write(t, dir, "publicity.txt", []byte("not the public folder"))
	write(t, dir, "webapp/README", []byte("notes"))
	write(t, dir, "webapp/distant.js", []byte("not in webapp/dist"))
	f := checkOf(t, dir)
	require.Equal(t, Signed, f.State, f.Reason)
	require.Equal(t, []string{"assets/public/x.html", "publicity.txt", "webapp/README", "webapp/distant.js"}, f.Unlisted)

	// A bundle at the top of the folder: every file is in its folder.
	dir = newBundle(t)
	write(t, dir, "plugin.json", []byte(`{"id":"ceo.villa.matty","version":"4.0.0","server":{"executables":{"linux-amd64":"server/dist/plugin-linux-amd64"}},"webapp":{"bundle_path":"./main.js"}}`))
	write(t, dir, "main.js", []byte("console.log('panel')"))
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64"))
	require.Equal(t, Signed, checkOf(t, dir).State)
	write(t, dir, "notes.txt", []byte("left by somebody"))
	requireAltered(t, checkOf(t, dir), "notes.txt is not in the release, and Mattermost serves every file in . with the web panel")
}

// A bundle with every platform says them all, and each platform's check
// asks for its own executable.
func TestABundleForSeveralPlatforms(t *testing.T) {
	dir := newBundle(t)
	write(t, dir, "plugin.json", []byte(`{"id":"ceo.villa.matty","version":"4.0.0","server":{"executables":{"linux-amd64":"server/dist/plugin-linux-amd64","linux-arm64":"server/dist/plugin-linux-arm64"}},"webapp":{"bundle_path":"webapp/dist/main.js"}}`))
	write(t, dir, "server/dist/plugin-linux-arm64", []byte("arm"))
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-amd64,linux-arm64"))

	for _, platform := range []string{"linux-amd64", "linux-arm64", ""} {
		require.Equal(t, Signed, CheckDir(dir, theKeys(t).pins, Expect{Version: "4.0.0", Platform: platform}).State, platform)
	}
	requireAltered(t, CheckDir(dir, theKeys(t).pins, Expect{Version: "4.0.0", Platform: "darwin-arm64"}), "signed for linux-amd64, linux-arm64")

	dir = newBundle(t)
	sign(t, dir, manifestOf(t, dir, "4.0.0", "linux-arm64,linux-amd64"))
	requireAltered(t, checkOf(t, dir), "are not sorted")

	// A platform plugin.json has no executable for.
	dir = newBundle(t)
	sign(t, dir, manifestOf(t, dir, "4.0.0", "darwin-arm64,linux-amd64"))
	requireAltered(t, CheckDir(dir, theKeys(t).pins, Expect{Version: "4.0.0"}), "no server executable for darwin-arm64")
}

func TestColons(t *testing.T) {
	require.Equal(t, "AB:0C", Colons("ab0c"))
}
