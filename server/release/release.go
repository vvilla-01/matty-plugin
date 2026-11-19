// Package release checks a signed release of Matty Plugin.
//
// A signed release carries, inside the bundle, release/manifest.txt: a
// header, then one line per file of the bundle with its SHA-256 and size.
// Beside it are the two signatures over the manifest's exact bytes, made with
// the two keys Matty itself is signed with: release/manifest.rsa.sig
// (RSASSA-PSS, RSA 4096, SHA-512, MGF1 with SHA-512, a 64-byte salt; the raw
// 512 bytes) and release/manifest.mldsa87.sig (pure ML-DSA-87, FIPS 204, with
// the empty context; the raw 4627 bytes). The manifest's first line names the
// format and is the domain separation for both signatures.
//
// A checker trusts only the certificates it is given (Pins), never one that
// travels with a release. Both signatures must verify, over the exact bytes,
// before anything in the manifest is read; either one failing or missing is a
// failure. build/sign-release.sh makes a signed release; build/checkrelease
// and the plugin itself check one with this package.
package release

import (
	"bytes"
	"crypto"
	"crypto/mldsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	_ "embed" // the pinned certificates
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// Header is the manifest's first line: the format and its version.
	Header = "matty-release-manifest 1"

	// Product and Kind are the only ones this package accepts.
	Product = "ceo.villa.matty"
	Kind    = "plugin"

	// Where a release keeps its manifest and signatures, inside the bundle.
	Dir                = "release"
	ManifestPath       = Dir + "/manifest.txt"
	RSASignaturePath   = Dir + "/manifest.rsa.sig"
	MLDSASignaturePath = Dir + "/manifest.mldsa87.sig"

	// publicDir is the plugin's folder whose every file Mattermost serves
	// to anybody, signed in or not.
	publicDir = "public"

	// The signatures' exact sizes.
	RSASignatureSize   = 512
	MLDSASignatureSize = mldsa.MLDSA87SignatureSize

	// MaxManifestBytes bounds what is read as a manifest; a real one is a
	// few hundred bytes.
	MaxManifestBytes = 1 << 20

	// maxEntries bounds the files a manifest may list, and maxWalked the
	// entries a check walks through in a folder.
	maxEntries = 10000
	maxWalked  = 100000

	// maxPluginJSON bounds plugin.json as it is read to find what
	// Mattermost loads.
	maxPluginJSON = 1 << 20

	// MaxUnlistedShown is how many unlisted files a Finding names; it counts
	// all of them.
	MaxUnlistedShown = 10
)

// The SHA-256 of the two certificates official releases are signed with,
// which are Matty's own: the same certificates sign the app, and these are
// the digests its releases publish.
const (
	OfficialRSASHA256   = "e55250bea6690039b7849e33b049088ecbb998d9f20aaa8c7eb2dc5a4363273c"
	OfficialMLDSASHA256 = "7578cb63b43ecc6fb127645154f522fb15d13a2608e459bcf42a7cd14186a949"
)

var (
	//go:embed pins/matty-rsa.pem
	officialRSAPEM []byte

	//go:embed pins/matty-mldsa87.pem
	officialMLDSAPEM []byte
)

var (
	versionRE  = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
	platformRE = regexp.MustCompile(`^[a-z0-9]{1,16}-[a-z0-9]{1,16}$`)
	countRE    = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)
	entryRE    = regexp.MustCompile(`^([0-9a-f]{64}) (0|[1-9][0-9]{0,15}) (.+)$`)
	pathRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

// Pins are the two certificates a checker trusts.
type Pins struct {
	rsa   *rsa.PublicKey
	mldsa *mldsa.PublicKey

	// The SHA-256 of each certificate's DER, lower-case hex.
	RSASHA256   string
	MLDSASHA256 string
}

// NewPins reads the two certificates a release is checked against, each PEM
// (one CERTIFICATE block and nothing else) or DER. The first must hold an RSA
// 4096 key and the second an ML-DSA-87 key. Their own signatures are not
// looked at: trust in them is the caller's, by choosing them.
func NewPins(rsaCert, mldsaCert []byte) (Pins, error) {
	rsaDER, err := certificateDER(rsaCert)
	if err != nil {
		return Pins{}, fmt.Errorf("the RSA certificate: %w", err)
	}
	mldsaDER, err := certificateDER(mldsaCert)
	if err != nil {
		return Pins{}, fmt.Errorf("the ML-DSA certificate: %w", err)
	}

	rc, err := x509.ParseCertificate(rsaDER)
	if err != nil {
		return Pins{}, fmt.Errorf("the RSA certificate: %w", err)
	}
	mc, err := x509.ParseCertificate(mldsaDER)
	if err != nil {
		return Pins{}, fmt.Errorf("the ML-DSA certificate: %w", err)
	}

	rk, ok := rc.PublicKey.(*rsa.PublicKey)
	if !ok || rk.N.BitLen() != 4096 {
		return Pins{}, errors.New("the RSA certificate does not hold an RSA 4096 key")
	}
	mk, ok := mc.PublicKey.(*mldsa.PublicKey)
	if !ok || mk.Parameters() != mldsa.MLDSA87() {
		return Pins{}, errors.New("the ML-DSA certificate does not hold an ML-DSA-87 key")
	}

	return Pins{rsa: rk, mldsa: mk, RSASHA256: sha256Hex(rsaDER), MLDSASHA256: sha256Hex(mldsaDER)}, nil
}

// OfficialPins are the certificates built in, after their digests have been
// held to the published ones.
func OfficialPins() (Pins, error) {
	pins, err := NewPins(officialRSAPEM, officialMLDSAPEM)
	if err != nil {
		return Pins{}, err
	}
	if pins.RSASHA256 != OfficialRSASHA256 || pins.MLDSASHA256 != OfficialMLDSASHA256 {
		return Pins{}, errors.New("the built-in certificates are not the published ones")
	}

	return pins, nil
}

// certificateDER is a certificate's DER, from PEM or DER.
func certificateDER(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN")) {
		return raw, nil
	}

	block, rest := pem.Decode(trimmed)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
		return nil, errors.New("not one PEM certificate")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("more than one PEM block")
	}

	return block.Bytes, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Colons is a lower-case hex digest as releases publish it: upper-case
// pairs joined by colons.
func Colons(digest string) string {
	upper := strings.ToUpper(digest)
	pairs := make([]string, 0, len(upper)/2)
	for i := 0; i+1 < len(upper); i += 2 {
		pairs = append(pairs, upper[i:i+2])
	}

	return strings.Join(pairs, ":")
}

// VerifySignatures checks both signatures over the manifest's exact bytes.
// Both are always checked; the error names each one that failed.
func VerifySignatures(pins Pins, manifest, rsaSig, mldsaSig []byte) error {
	if pins.rsa == nil || pins.mldsa == nil {
		return errors.New("no certificates to check against")
	}
	if len(manifest) == 0 || len(manifest) > MaxManifestBytes {
		return errors.New("the manifest is empty or too large")
	}

	var failed []string
	if len(rsaSig) != RSASignatureSize {
		failed = append(failed, "the RSA signature is missing or not 512 bytes")
	} else {
		digest := sha512.Sum512(manifest)
		options := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA512}
		if rsa.VerifyPSS(pins.rsa, crypto.SHA512, digest[:], rsaSig, options) != nil {
			failed = append(failed, "the RSA signature does not verify")
		}
	}

	if len(mldsaSig) != MLDSASignatureSize {
		failed = append(failed, "the ML-DSA signature is missing or not 4627 bytes")
	} else if mldsa.Verify(pins.mldsa, manifest, mldsaSig, nil) != nil {
		failed = append(failed, "the ML-DSA signature does not verify")
	}

	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}

	return nil
}

// Entry is one file a manifest lists.
type Entry struct {
	SHA256 string // lower-case hex
	Size   int64
	Path   string // relative to the plugin's folder, with "/"
}

// Manifest is a parsed manifest.
type Manifest struct {
	Version   string
	Platforms []string // sorted
	Files     []Entry  // sorted by path

	byPath map[string]Entry
}

// Lookup is the entry for a path.
func (m *Manifest) Lookup(p string) (Entry, bool) {
	e, ok := m.byPath[p]
	return e, ok
}

// ParseManifest reads a manifest strictly: printable ASCII and LF only,
// ending in LF, the header lines exactly in order, the product and kind this
// package knows, a plain version, the platforms sorted, the count equal to
// the entries, and the entries sorted by path, each path once.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes || raw[len(raw)-1] != '\n' {
		return nil, errors.New("not a manifest: empty, too large, or not ending in a line feed")
	}
	for _, b := range raw {
		if b != '\n' && (b < 0x20 || b > 0x7e) {
			return nil, errors.New("not a manifest: only printable ASCII and line feeds are allowed")
		}
	}

	lines := strings.Split(string(raw[:len(raw)-1]), "\n")
	if len(lines) < 7 {
		return nil, errors.New("the manifest is too short")
	}
	if lines[0] != Header {
		return nil, fmt.Errorf("the manifest's first line is %q, not %q", lines[0], Header)
	}

	field := func(i int, name string) (string, error) {
		value, ok := strings.CutPrefix(lines[i], name+" ")
		if !ok {
			return "", fmt.Errorf("line %d of the manifest is not %q", i+1, name)
		}
		return value, nil
	}

	product, err := field(1, "product")
	if err != nil {
		return nil, err
	}
	if product != Product {
		return nil, fmt.Errorf("the manifest is for %q, not %s", product, Product)
	}
	kind, err := field(2, "kind")
	if err != nil {
		return nil, err
	}
	if kind != Kind {
		return nil, fmt.Errorf("the manifest is of a %q, not a %s", kind, Kind)
	}
	version, err := field(3, "version")
	if err != nil {
		return nil, err
	}
	if !versionRE.MatchString(version) {
		return nil, fmt.Errorf("the manifest's version %q is not a plain version", version)
	}
	platformLine, err := field(4, "platform")
	if err != nil {
		return nil, err
	}
	platforms := strings.Split(platformLine, ",")
	for i, platform := range platforms {
		if !platformRE.MatchString(platform) || i > 0 && platform <= platforms[i-1] {
			return nil, fmt.Errorf("the manifest's platforms %q are not sorted names such as linux-amd64", platformLine)
		}
	}
	count, err := field(5, "files")
	if err != nil {
		return nil, err
	}
	if !countRE.MatchString(count) {
		return nil, fmt.Errorf("the manifest's file count %q is not a number", count)
	}
	n, _ := strconv.Atoi(count)
	if n < 1 || n > maxEntries || n != len(lines)-6 {
		return nil, fmt.Errorf("the manifest says %d files and lists %d", n, len(lines)-6)
	}

	m := &Manifest{Version: version, Platforms: platforms, byPath: make(map[string]Entry, n)}
	for i, line := range lines[6:] {
		match := entryRE.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("line %d of the manifest is not a file", 7+i)
		}
		if err := checkPath(match[3]); err != nil {
			return nil, fmt.Errorf("line %d of the manifest: %w", 7+i, err)
		}
		if i > 0 && match[3] <= m.Files[i-1].Path {
			return nil, fmt.Errorf("line %d of the manifest, %s, is out of order or listed twice", 7+i, match[3])
		}
		size, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d of the manifest: the size is too large", 7+i)
		}

		entry := Entry{SHA256: match[1], Size: size, Path: match[3]}
		m.Files = append(m.Files, entry)
		m.byPath[entry.Path] = entry
	}

	return m, nil
}

// checkPath holds a listed path to the format: relative, plain characters,
// no "." or ".." part, and nothing of the release folder.
func checkPath(p string) error {
	if !pathRE.MatchString(p) {
		return fmt.Errorf("%q is not a plain relative path", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("%q has a . or .. in it", p)
		}
	}
	if p == Dir || strings.HasPrefix(p, Dir+"/") {
		return fmt.Errorf("%q is in the release folder", p)
	}

	return nil
}

// State is what a check found.
type State string

const (
	// Signed: both signatures verify, the manifest is for this version and
	// platform, and every file it lists is on disk as listed, the ones
	// Mattermost loads among them.
	Signed State = "signed"

	// Unsigned: there is no release folder. Every development build, and
	// any build somebody makes of their own, is unsigned.
	Unsigned State = "unsigned"

	// Altered: there is a release folder, and something about it, or about
	// the files, is not as signed.
	Altered State = "altered"
)

// Expect is what a check holds a release to.
type Expect struct {
	// Version is the version the release must be.
	Version string

	// Platform is the platform whose executable must be listed, such as
	// linux-amd64. Empty: every platform the manifest names.
	Platform string
}

// Finding is what a check of a plugin's folder found.
type Finding struct {
	State State

	// Why, for Altered, as a sentence a person can read; and File, the file
	// to blame when there is one.
	Reason string
	File   string

	// The release folder's three files as found (each nil when absent or
	// unreadable), and the manifest's SHA-256.
	Manifest       []byte
	RSASignature   []byte
	MLDSASignature []byte
	ManifestSHA256 string

	// Version and Platforms are the manifest's, once it has verified and
	// parsed; Files is how many it lists.
	Version   string
	Platforms []string
	Files     int

	// Regular files in the folder that the manifest does not list: how many,
	// and the first MaxUnlistedShown of them by path. Not a failure where
	// Mattermost does not read them: it runs the executable plugin.json
	// names, and plugin.json is listed. Three kinds are a failure, since
	// Mattermost does read them: a plugin.yml or plugin.yaml beside
	// plugin.json, which it reads in its place, any file in the web
	// panel's bundle's folder, all of which it copies and serves, and any
	// file in the public folder, all of which it serves to anybody.
	UnlistedCount int
	Unlisted      []string
}

func altered(f Finding, file, reason string, args ...any) Finding {
	f.State = Altered
	f.File = file
	f.Reason = fmt.Sprintf(reason, args...)

	return f
}

// CheckDir checks the plugin folder dir, through a root that nothing in it
// can lead out of. A large file is hashed as a stream.
func CheckDir(dir string, pins Pins, want Expect) Finding {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return altered(Finding{}, "", "the plugin's folder cannot be opened: %v", err)
	}
	defer root.Close()

	return check(root, pins, want)
}

func check(root *os.Root, pins Pins, want Expect) Finding {
	var f Finding

	info, err := root.Lstat(Dir)
	if errors.Is(err, fs.ErrNotExist) {
		f.State = Unsigned
		return f
	}
	if err != nil {
		return altered(f, Dir, "the release folder cannot be read: %v", err)
	}
	if !info.IsDir() {
		return altered(f, Dir, "release is not a folder")
	}

	var problems []string
	read := func(name string, limit int64) []byte {
		b, err := readRegular(root, name, limit)
		if err != nil {
			problems = append(problems, err.Error())
			return nil
		}
		return b
	}
	f.Manifest = read(ManifestPath, MaxManifestBytes)
	f.RSASignature = read(RSASignaturePath, RSASignatureSize)
	f.MLDSASignature = read(MLDSASignaturePath, MLDSASignatureSize)
	if f.Manifest != nil {
		f.ManifestSHA256 = sha256Hex(f.Manifest)
	}
	if len(problems) > 0 {
		return altered(f, "", "%s", strings.Join(problems, "; "))
	}

	if err := VerifySignatures(pins, f.Manifest, f.RSASignature, f.MLDSASignature); err != nil {
		return altered(f, ManifestPath, "%v", err)
	}

	m, err := ParseManifest(f.Manifest)
	if err != nil {
		return altered(f, ManifestPath, "%v", err)
	}
	f.Version, f.Platforms, f.Files = m.Version, m.Platforms, len(m.Files)

	if m.Version != want.Version {
		return altered(f, ManifestPath, "the release is signed as version %s, and this is %s", m.Version, want.Version)
	}
	platforms := m.Platforms
	if want.Platform != "" {
		if !contains(m.Platforms, want.Platform) {
			return altered(f, ManifestPath, "the release is signed for %s, and this is %s", strings.Join(m.Platforms, ", "), want.Platform)
		}
		platforms = []string{want.Platform}
	}

	// Mattermost reads plugin.yml, and then plugin.yaml, before plugin.json,
	// which it reads only when neither is there (model.FindManifest): either
	// one, listed or not, decides what it runs and serves in place of the
	// plugin.json that was signed. No release has one.
	for _, name := range []string{"plugin.yml", "plugin.yaml"} {
		if _, err := root.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			return altered(f, name, "Mattermost reads %s before plugin.json", name)
		}
	}

	// What Mattermost loads must be among what was signed: plugin.json,
	// read once, held to its entry, and then read for the rest.
	webappDir, file, reason := checkLoaded(root, m, platforms)
	if reason != "" {
		return altered(f, file, "%s", reason)
	}

	present, file, reason := walk(root)
	if reason != "" {
		return altered(f, file, "%s", reason)
	}

	for _, entry := range m.Files {
		if !present[entry.Path] {
			return altered(f, entry.Path, "%s is missing", entry.Path)
		}
		if reason := checkFile(root, entry); reason != "" {
			return altered(f, entry.Path, "%s", reason)
		}
		delete(present, entry.Path)
	}

	for _, own := range []string{ManifestPath, RSASignaturePath, MLDSASignaturePath} {
		delete(present, own)
	}
	unlisted := make([]string, 0, len(present))
	for p := range present {
		unlisted = append(unlisted, p)
	}
	sort.Strings(unlisted)

	// Mattermost copies the whole of the web panel's bundle's folder to
	// where it serves the panel from, on the server's own address: a file
	// nobody signed there is served as the panel's. It also serves every
	// file under the plugin's public folder, to anybody, at
	// /plugins/<id>/public/<file> (Environment.PublicFilesPath). A file
	// named public, and the name in any case, for a server whose disk
	// ignores case, count as that folder too: no release has either.
	for _, p := range unlisted {
		if webappDir != "" && (webappDir == "." || strings.HasPrefix(p, webappDir+"/")) {
			return altered(f, p, "%s is not in the release, and Mattermost serves every file in %s with the web panel", p, webappDir)
		}
		if top, _, _ := strings.Cut(p, "/"); strings.EqualFold(top, publicDir) {
			return altered(f, p, "%s is not in the release, and Mattermost serves every file in %s to anybody", p, publicDir)
		}
	}

	f.UnlistedCount = len(unlisted)
	f.Unlisted = unlisted[:min(len(unlisted), MaxUnlistedShown)]

	f.State = Signed
	return f
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// readRegular reads a small file that must be a regular file of at most
// limit bytes.
func readRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s is missing", name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %v", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}

	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %v", name, err)
	}
	defer file.Close()

	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %v", name, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", name)
	}

	return b, nil
}

// pluginManifest is what of plugin.json says which files Mattermost loads.
type pluginManifest struct {
	Version string `json:"version"`
	Server  *struct {
		Executables map[string]string `json:"executables"`
		Executable  string            `json:"executable"`
	} `json:"server"`
	Webapp *struct {
		BundlePath string `json:"bundle_path"`
	} `json:"webapp"`
}

// checkLoaded holds what Mattermost loads to the manifest: plugin.json
// itself, the executable for each platform, and the webapp bundle. It reads
// plugin.json once, and what it parses is what it hashed. webappDir is the
// folder of the webapp bundle, cleaned, or "" when plugin.json names none.
func checkLoaded(root *os.Root, m *Manifest, platforms []string) (webappDir, file, reason string) {
	const name = "plugin.json"

	entry, listed := m.Lookup(name)
	if !listed {
		return "", name, "the manifest does not list plugin.json"
	}
	raw, err := readRegular(root, name, maxPluginJSON)
	if err != nil {
		return "", name, err.Error()
	}
	if int64(len(raw)) != entry.Size || sha256Hex(raw) != entry.SHA256 {
		return "", name, "plugin.json has changed"
	}

	var manifest pluginManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", name, "plugin.json does not parse: " + err.Error()
	}
	if manifest.Version != m.Version {
		return "", name, fmt.Sprintf("plugin.json says version %s, and the release is %s", manifest.Version, m.Version)
	}

	var loaded []string
	for _, platform := range platforms {
		executable := ""
		if manifest.Server != nil {
			executable = manifest.Server.Executables[platform]
			if executable == "" {
				executable = manifest.Server.Executable
			}
		}
		if executable == "" {
			return "", name, "plugin.json names no server executable for " + platform
		}
		loaded = append(loaded, executable)
	}
	if manifest.Webapp != nil && manifest.Webapp.BundlePath != "" {
		loaded = append(loaded, manifest.Webapp.BundlePath)
	}

	clean := func(p string) string { return strings.TrimPrefix(path.Clean(strings.TrimPrefix(p, "./")), "./") }
	for _, p := range loaded {
		if _, listed := m.Lookup(clean(p)); !listed {
			return "", clean(p), fmt.Sprintf("the manifest does not list %s, which Mattermost loads", clean(p))
		}
	}
	if manifest.Webapp != nil && manifest.Webapp.BundlePath != "" {
		webappDir = path.Dir(clean(manifest.Webapp.BundlePath))
	}

	return webappDir, "", ""
}

// walk is every regular file in the folder, by its path with "/". Anything
// that is neither a regular file nor a folder (a symbolic link, say) is an
// alteration: nothing a release holds is one.
func walk(root *os.Root) (present map[string]bool, file, reason string) {
	present = map[string]bool{}
	walked := 0

	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			file, reason = p, fmt.Sprintf("%s cannot be read: %v", p, err)
			return fs.SkipAll
		}
		walked++
		if walked > maxWalked {
			file, reason = "", "the plugin's folder holds too many files"
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			file, reason = p, p+" is not a regular file"
			return fs.SkipAll
		}
		present[p] = true

		return nil
	})
	if err != nil && reason == "" {
		file, reason = "", fmt.Sprintf("the plugin's folder cannot be read: %v", err)
	}

	return present, file, reason
}

// checkFile holds one listed file to its size and SHA-256, reading it as a
// stream and never more than one byte past its listed size.
func checkFile(root *os.Root, entry Entry) string {
	info, err := root.Lstat(entry.Path)
	if err != nil {
		return fmt.Sprintf("%s cannot be read: %v", entry.Path, err)
	}
	if !info.Mode().IsRegular() {
		return entry.Path + " is not a regular file"
	}
	if info.Size() != entry.Size {
		return entry.Path + " has changed"
	}

	file, err := root.Open(entry.Path)
	if err != nil {
		return fmt.Sprintf("%s cannot be read: %v", entry.Path, err)
	}
	defer file.Close()

	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, entry.Size+1))
	if err != nil {
		return fmt.Sprintf("%s cannot be read: %v", entry.Path, err)
	}
	if n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return entry.Path + " has changed"
	}

	return ""
}
