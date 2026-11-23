package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// tarOf packs headers as they are, each regular file with content of its
// header's size.
func tarOf(t *testing.T, headers ...*tar.Header) []byte {
	t.Helper()

	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)
	for _, header := range headers {
		if header.Mode == 0 {
			header.Mode = 0o644
		}
		require.NoError(t, archive.WriteHeader(header))
		if header.Typeflag == tar.TypeReg {
			_, err := archive.Write(bytes.Repeat([]byte("x"), int(header.Size)))
			require.NoError(t, err)
		}
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return buf.Bytes()
}

func dirEntry(name string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}
}

func fileEntry(name string, size int64) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Size: size}
}

// A bundle as releases are packed: the one folder, plain files and folders,
// an executable kept executable, and nothing readable by anybody else.
func TestUnpackWritesABundleUnderItsOneFolder(t *testing.T) {
	dest := t.TempDir()
	executable := fileEntry("ceo.villa.matty/server/dist/plugin-linux-amd64", 10)
	executable.Mode = 0o755
	bundle := tarOf(t,
		dirEntry("ceo.villa.matty/"),
		fileEntry("ceo.villa.matty/plugin.json", 3),
		dirEntry("ceo.villa.matty/server/"),
		executable,
		fileEntry("./ceo.villa.matty/assets/icon.svg", 4),
	)

	top, err := Unpack(context.Background(), bytes.NewReader(bundle), dest, BundleLimits)
	require.NoError(t, err)
	require.Equal(t, Product, top)

	info, err := os.Stat(filepath.Join(dest, top, "server/dist/plugin-linux-amd64"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(filepath.Join(dest, top, "plugin.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	content, err := os.ReadFile(filepath.Join(dest, top, "assets/icon.svg"))
	require.NoError(t, err)
	require.Equal(t, "xxxx", string(content))
}

// Every refusal, each before anything is written outside the folder.
func TestUnpackRefusesWhatIsNotPlainFilesUnderTheOneFolder(t *testing.T) {
	small := UnpackLimits{Top: Product, Entries: 5, Bytes: 100}

	cases := map[string]struct {
		bundle []byte
		limits UnpackLimits
		reason string
	}{
		"a symbolic link":   {tarOf(t, dirEntry("ceo.villa.matty/"), &tar.Header{Name: "ceo.villa.matty/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/hostname"}), BundleLimits, "is not a regular file or a folder"},
		"a hard link":       {tarOf(t, fileEntry("ceo.villa.matty/a", 1), &tar.Header{Name: "ceo.villa.matty/b", Typeflag: tar.TypeLink, Linkname: "ceo.villa.matty/a"}), BundleLimits, "is not a regular file or a folder"},
		"a device":          {tarOf(t, &tar.Header{Name: "ceo.villa.matty/null", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}), BundleLimits, "is not a regular file or a folder"},
		"a fifo":            {tarOf(t, &tar.Header{Name: "ceo.villa.matty/fifo", Typeflag: tar.TypeFifo}), BundleLimits, "is not a regular file or a folder"},
		"an absolute path":  {tarOf(t, fileEntry("/ceo.villa.matty/x", 1)), BundleLimits, "a name that is not allowed"},
		"a path out":        {tarOf(t, fileEntry("ceo.villa.matty/../escape", 1)), BundleLimits, "a name that is not allowed"},
		"a dot dot inside":  {tarOf(t, fileEntry("ceo.villa.matty/a/../../x", 1)), BundleLimits, "a name that is not allowed"},
		"a backslash":       {tarOf(t, fileEntry(`ceo.villa.matty\..\x`, 1)), BundleLimits, "a name that is not allowed"},
		"a path twice":      {tarOf(t, fileEntry("ceo.villa.matty/plugin.json", 1), fileEntry("ceo.villa.matty/plugin.json", 2)), BundleLimits, "holds a path twice"},
		"a folder twice":    {tarOf(t, dirEntry("ceo.villa.matty/"), dirEntry("./ceo.villa.matty")), BundleLimits, "holds a path twice"},
		"a second top":      {tarOf(t, fileEntry("ceo.villa.matty/a", 1), fileEntry("other/x", 1)), BundleLimits, "more than one folder at its top"},
		"another top":       {tarOf(t, fileEntry("ceo.villa.matty.evil/x", 1)), BundleLimits, "more than one folder at its top"},
		"a file at the top": {tarOf(t, fileEntry("ceo.villa.matty", 1)), BundleLimits, "top is a file"},
		"too many entries":  {tarOf(t, fileEntry("ceo.villa.matty/1", 1), fileEntry("ceo.villa.matty/2", 1), fileEntry("ceo.villa.matty/3", 1), fileEntry("ceo.villa.matty/4", 1), fileEntry("ceo.villa.matty/5", 1), fileEntry("ceo.villa.matty/6", 1)), small, "more than 5 files and folders"},
		"too many bytes":    {tarOf(t, fileEntry("ceo.villa.matty/1", 60), fileEntry("ceo.villa.matty/2", 60)), small, "unpacks to more than"},
		"not gzip":          {[]byte("plain text, not a bundle"), BundleLimits, "not a gzip file"},
		"empty":             {tarOf(t), BundleLimits, "empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "unpacked")
			require.NoError(t, os.Mkdir(dest, 0o700))

			_, err := Unpack(context.Background(), bytes.NewReader(c.bundle), dest, c.limits)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.reason)

			// Nothing was written beside the folder it was given.
			entries, err := os.ReadDir(parent)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}

	// Not a tar inside the gzip.
	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	_, _ = compressed.Write(bytes.Repeat([]byte{0xff}, 1024))
	require.NoError(t, compressed.Close())
	_, err := Unpack(context.Background(), bytes.NewReader(buf.Bytes()), t.TempDir(), BundleLimits)
	require.ErrorContains(t, err, "does not read as a tar")
}

// A name is held to the manifest's own rule for a path, and the refusal
// never repeats it: whatever a bundle's names say reaches nobody.
func TestUnpackHoldsNamesToTheManifestsRuleAndNeverRepeatsThem(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	words := "\n\n**Security notice:** install it from www.evil.example\n\n"
	cases := map[string][]*tar.Header{
		"words in a name":              {dirEntry("ceo.villa.matty/"), fileEntry("ceo.villa.matty/"+words, 1)},
		"words as a second top":        {fileEntry("ceo.villa.matty/a", 1), fileEntry(words+"/x", 1)},
		"a space":                      {fileEntry("ceo.villa.matty/a b", 1)},
		"a part longer than 255 bytes": {dirEntry("ceo.villa.matty/" + long(256) + "/")},
		"a path longer than 512 bytes": {dirEntry("ceo.villa.matty/" + strings.Repeat(long(100)+"/", 5) + long(20) + "/")},
		"an empty part":                {fileEntry("ceo.villa.matty//x", 1)},
		"a dot part":                   {fileEntry("ceo.villa.matty/./x", 1)},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			for _, header := range headers {
				header.Format = tar.FormatPAX
			}
			_, err := Unpack(context.Background(), bytes.NewReader(tarOf(t, headers...)), t.TempDir(), BundleLimits)
			require.Error(t, err)
			var refusal Refusal
			require.ErrorAs(t, err, &refusal)
			require.NotContains(t, err.Error(), "evil")
			require.NotContains(t, err.Error(), "aaaa")
			require.LessOrEqual(t, len(err.Error()), 80)
		})
	}

	// Every real name passes, a part of 255 bytes and a path of 512 among
	// them.
	part := "ceo.villa.matty/" + long(255)
	path := "ceo.villa.matty/" + strings.Repeat(long(99)+"/", 4) + long(512-len("ceo.villa.matty/")-400)
	require.Len(t, path, 512)
	_, err := Unpack(context.Background(), bytes.NewReader(tarOf(t, fileEntry(part, 1), fileEntry(path, 1), fileEntry("ceo.villa.matty/server/dist/plugin-linux_amd64.exe", 1))), t.TempDir(), BundleLimits)
	require.NoError(t, err)
}

// The names of one bundle count against 1 MiB together, before any is kept:
// a bundle of many long names is refused long before its entries run out.
func TestUnpackRefusesNamesLongerThanAMebibyteInAll(t *testing.T) {
	var headers []*tar.Header
	for i := range 3000 {
		unique := string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))
		headers = append(headers, dirEntry("ceo.villa.matty/"+strings.Repeat("a", 200)+"/"+strings.Repeat("b", 200)+"/"+strings.Repeat("c", 20)+unique+"/"))
	}
	_, err := Unpack(context.Background(), bytes.NewReader(tarOf(t, headers...)), t.TempDir(), BundleLimits)
	require.ErrorIs(t, err, refusedNames)
}

// A stop ends the unpacking at the next entry, or in the middle of a file.
func TestUnpackStopsWhenItsContextEnds(t *testing.T) {
	// A file that does not compress, so that it is still being read when
	// the stop comes.
	noise := make([]byte, 1<<20)
	_, err := rand.Read(noise)
	require.NoError(t, err)
	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)
	require.NoError(t, archive.WriteHeader(dirEntry("ceo.villa.matty/")))
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/a", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(noise))}))
	_, err = archive.Write(noise)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	bundle := buf.Bytes()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Unpack(ctx, bytes.NewReader(bundle), t.TempDir(), BundleLimits)
	require.ErrorIs(t, err, context.Canceled)

	ctx, cancel = context.WithCancel(context.Background())
	stopping := &cancelAfter{r: bytes.NewReader(bundle), after: 64 << 10, cancel: cancel}
	_, err = Unpack(ctx, stopping, t.TempDir(), BundleLimits)
	require.ErrorIs(t, err, context.Canceled)
}

// cancelAfter reads r and cancels once after bytes have been read.
type cancelAfter struct {
	r      *bytes.Reader
	after  int
	read   int
	cancel context.CancelFunc
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	if c.read >= c.after {
		c.cancel()
	}

	return n, err
}

// A check made to be exact, as one of a bundle about to be installed is,
// refuses any file the manifest does not list, wherever it is.
func TestAnExactCheckRefusesAFileTheManifestDoesNotList(t *testing.T) {
	dir := signedBundle(t)
	exact := Expect{Version: here.Version, Platform: here.Platform, Exact: true}

	f := CheckDir(dir, theKeys(t).pins, exact)
	require.Equal(t, Signed, f.State, f.Reason)

	write(t, dir, "notes.txt", []byte("left by somebody"))
	require.Equal(t, Signed, checkOf(t, dir).State, "counted, not failed, by the plugin's check of itself")
	requireAltered(t, CheckDir(dir, theKeys(t).pins, exact), "notes.txt is not in the release")

	dir = signedBundle(t)
	write(t, dir, Dir+"/README", []byte("beside the signatures"))
	requireAltered(t, CheckDir(dir, theKeys(t).pins, exact), "release/README is not in the release")
}

// A count in a refusal is written as a person reads it.
func TestARefusalWritesItsCountWithCommas(t *testing.T) {
	require.Equal(t, Refusal("the bundle holds more than 4,000 files and folders"), refusedEntries(BundleLimits.Entries))
	for n, words := range map[int]string{0: "0", 5: "5", 999: "999", 1000: "1,000", 4000: "4,000", 1234567: "1,234,567"} {
		require.Equal(t, words, thousands(n))
	}
}
