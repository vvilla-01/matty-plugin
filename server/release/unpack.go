package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// UnpackLimits is what a bundle may hold for Unpack.
type UnpackLimits struct {
	// Top is the one folder a bundle holds at its top, which is the
	// plugin's: Product for every bundle of Matty Plugin.
	Top string

	// Entries bounds the files and folders, and Bytes what the files hold
	// in all, as their headers say and as is written.
	Entries int
	Bytes   int64
}

// BundleLimits are what the plugin takes a downloaded bundle with: a release
// holds a few dozen entries and a few tens of megabytes.
var BundleLimits = UnpackLimits{Top: Product, Entries: 4000, Bytes: 256 << 20}

// BundleDownloadBytes is the most the plugin downloads of a bundle.
const BundleDownloadBytes = 64 << 20

// The names a bundle may hold: the manifest's own rule for a path (parts of
// [A-Za-z0-9._-] joined by "/", no "." or ".." part), each part at most
// 255 bytes, the whole at most 512, and every name of one bundle at most
// 1 MiB together. A release holds a few dozen short names.
const (
	maxNamePart   = 255
	maxNamePath   = 512
	maxNameBudget = 1 << 20
)

// Refusal is why Unpack refused a bundle: one of a fixed set of sentences,
// with no word of the bundle in it, so it can be shown and logged as it is.
type Refusal string

func (r Refusal) Error() string { return string(r) }

const (
	refusedGzip    Refusal = "the bundle is not a gzip file"
	refusedTar     Refusal = "the bundle does not read as a tar"
	refusedName    Refusal = "the bundle holds a name that is not allowed"
	refusedNames   Refusal = "the bundle's names are longer than 1 MiB in all"
	refusedTwice   Refusal = "the bundle holds a path twice"
	refusedTop     Refusal = "the bundle holds more than one folder at its top"
	refusedTopFile Refusal = "the bundle's top is a file, not a folder"
	refusedKind    Refusal = "the bundle holds something that is not a regular file or a folder"
	refusedEmpty   Refusal = "the bundle is empty"
	refusedNoTop   Refusal = "the bundle has no folder of the plugin's at its top"
	refusedWrite   Refusal = "a file of the bundle could not be written"
)

func refusedEntries(limit int) Refusal {
	return Refusal("the bundle holds more than " + thousands(limit) + " files and folders")
}

// thousands writes n as a person reads it, with a comma between each three
// digits: 4,000.
func thousands(n int) string {
	digits := fmt.Sprint(n)
	for i := len(digits) - 3; i > 0 && digits[i-1] != '-'; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}

	return digits
}

func refusedBytes(limit int64) Refusal {
	return Refusal(fmt.Sprintf("the bundle unpacks to more than %d MB", limit>>20))
}

// Unpack writes a bundle (a .tar.gz) under dest, an empty folder, through a
// root nothing in it can lead out of, and is the plugin's folder, dest/<top>.
// It refuses, before or while writing, anything but regular files and
// folders (a link of either kind, a device), a name outside the rule above
// (which leaves out an absolute path and a ".."), a path given twice,
// anything outside the one top folder limits.Top or a file in its place,
// and more entries or bytes than the limits. Each refusal is a Refusal. It
// looks at ctx at every entry and while a file is written, and stops with
// ctx's error once it ends. Files are written 0600, or 0700 when the bundle
// marks them executable, and folders 0700. On a refusal what was written is
// left for the caller to remove with dest.
func Unpack(ctx context.Context, bundle io.Reader, dest string, limits UnpackLimits) (string, error) {
	if limits.Top == "" || strings.ContainsAny(limits.Top, `/\`) {
		return "", errors.New("no top folder to unpack to")
	}

	compressed, err := gzip.NewReader(bundle)
	if err != nil {
		return "", refusedGzip
	}
	defer compressed.Close()

	root, err := os.OpenRoot(dest)
	if err != nil {
		return "", err
	}
	defer root.Close()

	archive := tar.NewReader(compressed)
	given := map[string]bool{}
	entries, names := 0, 0
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", refusedTar
		}

		entries++
		if entries > limits.Entries {
			return "", refusedEntries(limits.Entries)
		}

		// Counted before anything else is done with them, and never kept
		// unless they pass.
		names += len(header.Name) + len(header.Linkname)
		if names > maxNameBudget {
			return "", refusedNames
		}
		clean, err := bundlePath(header.Name, limits.Top)
		if err != nil {
			return "", err
		}
		if given[clean] {
			return "", refusedTwice
		}
		given[clean] = true

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(clean, 0o700); err != nil {
				return "", refusedWrite
			}
		case tar.TypeReg:
			if clean == limits.Top {
				return "", refusedTopFile
			}
			if header.Size < 0 || header.Size > limits.Bytes-total {
				return "", refusedBytes(limits.Bytes)
			}
			total += header.Size
			if err := writeUnpacked(ctx, root, archive, clean, header); err != nil {
				return "", err
			}
		default:
			return "", refusedKind
		}
	}

	if len(given) == 0 {
		return "", refusedEmpty
	}
	if info, err := root.Lstat(limits.Top); err != nil || !info.IsDir() {
		return "", refusedNoTop
	}

	return limits.Top, nil
}

// bundlePath is a member's name as a path under top, or why it is refused.
// A leading "./", as tar writes when given ".", is no part of it, and nor
// is a folder's trailing "/".
func bundlePath(name, top string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
	if len(trimmed) > maxNamePath || !pathRE.MatchString(trimmed) {
		return "", refusedName
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "." || part == ".." || len(part) > maxNamePart {
			return "", refusedName
		}
	}
	if parts[0] != top {
		return "", refusedTop
	}

	return path.Clean(trimmed), nil
}

// writeUnpacked writes one regular file, exactly its header's size, never
// over anything already there.
func writeUnpacked(ctx context.Context, root *os.Root, archive io.Reader, clean string, header *tar.Header) error {
	if err := root.MkdirAll(path.Dir(clean), 0o700); err != nil {
		return refusedWrite
	}

	mode := os.FileMode(0o600)
	if header.Mode&0o111 != 0 {
		mode = 0o700
	}
	target, err := root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return refusedWrite
	}

	n, err := io.Copy(target, io.LimitReader(contextReader{ctx: ctx, r: archive}, header.Size))
	closeErr := target.Close()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil || closeErr != nil || n != header.Size {
		return refusedWrite
	}

	return nil
}

// contextReader stops reading once ctx ends.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}

	return c.r.Read(p)
}
