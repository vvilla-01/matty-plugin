package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// A bundle signed with keys made here, and the certificates to check it
// against, written to files as a person would pass them.
type signedBundle struct {
	tarball, rsaCert, mldsaCert string
	members                     map[string][]byte
}

func newSignedBundle(t *testing.T) signedBundle {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 4096)
	require.NoError(t, err)
	mldsaKey, err := mldsa.GenerateKey(mldsa.MLDSA87())
	require.NoError(t, err)

	files := map[string][]byte{
		"plugin.json":                    []byte(`{"id":"ceo.villa.matty","version":"4.0.0","server":{"executables":{"linux-amd64":"server/dist/plugin-linux-amd64"}},"webapp":{"bundle_path":"webapp/dist/main.js"}}`),
		"assets/icon.svg":                []byte("<svg/>"),
		"server/dist/plugin-linux-amd64": []byte("executable"),
		"webapp/dist/main.js":            []byte("panel"),
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var manifest strings.Builder
	fmt.Fprintf(&manifest, "%s\nproduct %s\nkind %s\nversion 4.0.0\nplatform linux-amd64\nfiles %d\n", release.Header, release.Product, release.Kind, len(paths))
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(&manifest, "%s %d %s\n", hex.EncodeToString(sum[:]), len(files[p]), p)
	}
	m := []byte(manifest.String())

	digest := sha512.Sum512(m)
	rsaSig, err := rsa.SignPSS(rand.Reader, rsaKey, crypto.SHA512, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	require.NoError(t, err)
	mldsaSig, err := mldsaKey.Sign(nil, m, nil)
	require.NoError(t, err)

	members := map[string][]byte{}
	for p, content := range files {
		members["ceo.villa.matty/"+p] = content
	}
	members["ceo.villa.matty/"+release.ManifestPath] = m
	members["ceo.villa.matty/"+release.RSASignaturePath] = rsaSig
	members["ceo.villa.matty/"+release.MLDSASignaturePath] = mldsaSig

	dir := t.TempDir()
	b := signedBundle{
		tarball:   filepath.Join(dir, "ceo.villa.matty-4.0.0-linux-amd64.tar.gz"),
		rsaCert:   filepath.Join(dir, "rsa.der"),
		mldsaCert: filepath.Join(dir, "mldsa.der"),
		members:   members,
	}
	require.NoError(t, os.WriteFile(b.rsaCert, certificate(t, rsaKey.Public(), rsaKey), 0o644))
	require.NoError(t, os.WriteFile(b.mldsaCert, certificate(t, mldsaKey.PublicKey(), mldsaKey), 0o644))
	require.NoError(t, os.WriteFile(b.tarball, tarGz(t, members, nil), 0o644))

	return b
}

func certificate(t *testing.T, public any, signer crypto.Signer) []byte {
	t.Helper()

	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test signer"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, signer)
	require.NoError(t, err)

	return der
}

// tarGz packs members (sorted) and then any extra headers as they are.
func tarGz(t *testing.T, members map[string][]byte, extra []*tar.Header) []byte {
	t.Helper()

	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)

	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/", Typeflag: tar.TypeDir, Mode: 0o755}))
	for _, name := range names {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(members[name]))}))
		_, err := archive.Write(members[name])
		require.NoError(t, err)
	}
	for _, header := range extra {
		require.NoError(t, archive.WriteHeader(header))
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return buf.Bytes()
}

func runIt(args ...string) (int, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)

	return code, out.String() + errOut.String()
}

func TestTheCheckerAcceptsABundleSignedWithTheCertificatesGivenAndNoOther(t *testing.T) {
	b := newSignedBundle(t)

	code, out := runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, b.tarball)
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "signed: ceo.villa.matty 4.0.0 linux-amd64, 4 files")
	require.Contains(t, out, "manifest SHA-256: ")

	// The certificates built in are Matty's own, which did not sign it.
	code, out = runIt(b.tarball)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "altered: the RSA signature does not verify; the ML-DSA signature does not verify")
	require.Contains(t, out, "E5:52:50:BE")

	code, out = runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, "-version", "4.0.1", b.tarball)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "signed as version 4.0.0")
}

// A signed bundle with a file added that Mattermost reads in place of what
// was signed, or serves: a plugin.yml or plugin.yaml, a file beside the web
// panel, or a file in the public folder.
func TestTheCheckerRefusesABundleWithFilesMattermostReadsInsteadOfTheSignedOnes(t *testing.T) {
	b := newSignedBundle(t)
	dir := t.TempDir()

	for added, reason := range map[string]string{
		"plugin.yml":         "altered: Mattermost reads plugin.yml before plugin.json",
		"plugin.yaml":        "altered: Mattermost reads plugin.yaml before plugin.json",
		"webapp/dist/x.html": "altered: webapp/dist/x.html is not in the release, and Mattermost serves every file in webapp/dist with the web panel",
		"public/x.html":      "altered: public/x.html is not in the release, and Mattermost serves every file in public to anybody",
	} {
		members := map[string][]byte{"ceo.villa.matty/" + added: []byte("id: ceo.villa.matty\nversion: 4.0.0\n")}
		for name, content := range b.members {
			members[name] = content
		}
		bundle := filepath.Join(dir, strings.ReplaceAll(added, "/", "-")+".tar.gz")
		require.NoError(t, os.WriteFile(bundle, tarGz(t, members, nil), 0o644))

		code, out := runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, bundle)
		require.Equal(t, 1, code, out)
		require.Contains(t, out, reason)
	}
}

// -exact refuses a file nobody signed, which the plain check only counts:
// the check the plugin makes of a bundle before it hands it to an admin.
func TestTheCheckerIsExactWhenAsked(t *testing.T) {
	b := newSignedBundle(t)
	members := map[string][]byte{"ceo.villa.matty/notes.txt": []byte("left by somebody")}
	for name, content := range b.members {
		members[name] = content
	}
	bundle := filepath.Join(t.TempDir(), "extra.tar.gz")
	require.NoError(t, os.WriteFile(bundle, tarGz(t, members, nil), 0o644))

	code, out := runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, bundle)
	require.Equal(t, 0, code, out)
	require.Contains(t, out, "files nobody signed: 1 (notes.txt)")

	code, out = runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, "-exact", bundle)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "altered: notes.txt is not in the release")

	code, out = runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, "-exact", b.tarball)
	require.Equal(t, 0, code, out)
}

func TestTheCheckerRefusesBundlesThatDoNotUnpackCleanly(t *testing.T) {
	b := newSignedBundle(t)
	dir := t.TempDir()

	cases := map[string]struct {
		extra  []*tar.Header
		reason string
	}{
		"a link":           {[]*tar.Header{{Name: "ceo.villa.matty/assets/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/hostname"}}, "is not a regular file or a folder"},
		"a path out":       {[]*tar.Header{{Name: "ceo.villa.matty/../escape", Typeflag: tar.TypeReg, Mode: 0o644}}, "a name that is not allowed"},
		"a second top":     {[]*tar.Header{{Name: "other/x", Typeflag: tar.TypeReg, Mode: 0o644}}, "more than one folder at its top"},
		"a file twice":     {[]*tar.Header{{Name: "ceo.villa.matty/plugin.json", Typeflag: tar.TypeReg, Mode: 0o644}}, "holds a path twice"},
		"an absolute path": {[]*tar.Header{{Name: "/ceo.villa.matty/x", Typeflag: tar.TypeReg, Mode: 0o644}}, "a name that is not allowed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bundle := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".tar.gz")
			require.NoError(t, os.WriteFile(bundle, tarGz(t, b.members, c.extra), 0o644))

			code, out := runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, bundle)
			require.Equal(t, 1, code, out)
			require.Contains(t, out, c.reason)
		})
	}
}

func TestTheCheckerReadsAnUnpackedFolderAndSaysUnsigned(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"version":"4.0.0"}`), 0o644))

	code, out := runIt(dir)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "unsigned: there is no release folder")

	code, _ = runIt("-rsa-cert", "only-one.pem", dir)
	require.Equal(t, 2, code)
}

// With -exact a bundle is held to the plugin's own limits for one it
// downloads: larger than 64 MB is refused before it is read, and it unpacks
// within release.BundleLimits, so a file of more than 256 MB is refused as
// the plugin refuses it.
func TestExactHoldsABundleToThePluginsOwnLimits(t *testing.T) {
	require.Equal(t, release.BundleLimits, limitsFor(true))
	require.Greater(t, limitsFor(false).Bytes, release.BundleLimits.Bytes)

	b := newSignedBundle(t)
	dir := t.TempDir()

	large := filepath.Join(dir, "large.tar.gz")
	file, err := os.Create(large)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(release.BundleDownloadBytes+1))
	require.NoError(t, file.Close())
	code, out := runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, "-exact", large)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "altered: the bundle is larger than 64 MB, which the plugin does not download")

	// A header that says 300 MB, and nothing after it: refused at the
	// header, with nothing written.
	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/big", Typeflag: tar.TypeReg, Mode: 0o644, Size: 300 << 20}))
	require.NoError(t, compressed.Close())
	big := filepath.Join(dir, "big.tar.gz")
	require.NoError(t, os.WriteFile(big, buf.Bytes(), 0o644))
	code, out = runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, "-exact", big)
	require.Equal(t, 1, code, out)
	require.Contains(t, out, "unpacks to more than 256 MB")
	code, out = runIt("-rsa-cert", b.rsaCert, "-mldsa-cert", b.mldsaCert, big)
	require.Equal(t, 1, code, out)
	require.NotContains(t, out, "unpacks to more than")
}
