// Command checkrelease checks a signed release of Matty Plugin with the same
// code the plugin checks itself with (server/release): both signatures over
// the manifest, then every file it lists, and that what Mattermost loads is
// among them.
//
//	checkrelease [-rsa-cert FILE -mldsa-cert FILE] [-version V] [-platform P] BUNDLE
//
// BUNDLE is a bundle (.tar.gz) or an unpacked plugin folder. Without
// certificates it checks against the two built into the plugin, which are
// Matty's own. -version is the version the release must be (by default the
// one its plugin.json says); -platform the one platform whose executable
// must be listed (by default every platform the manifest names).
//
// It prints what it found and exits 0 for a signed release, 1 for anything
// else, and 2 when it was not asked properly.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// maxUnpacked bounds what a bundle may unpack to.
const maxUnpacked = 1 << 30

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("checkrelease", flag.ContinueOnError)
	flags.SetOutput(errOut)
	rsaCert := flags.String("rsa-cert", "", "the RSA 4096 certificate to check against (PEM or DER)")
	mldsaCert := flags.String("mldsa-cert", "", "the ML-DSA-87 certificate to check against (PEM or DER)")
	version := flags.String("version", "", "the version the release must be (default: its plugin.json's)")
	platform := flags.String("platform", "", "the platform whose executable must be listed (default: every one named)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || (*rsaCert == "") != (*mldsaCert == "") {
		fmt.Fprintln(errOut, "usage: checkrelease [-rsa-cert FILE -mldsa-cert FILE] [-version V] [-platform P] BUNDLE")
		return 2
	}

	pins, which, err := pinsFor(*rsaCert, *mldsaCert)
	if err != nil {
		fmt.Fprintln(errOut, "checkrelease:", err)
		return 2
	}

	dir, cleanup, err := pluginFolder(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(out, "altered:", err)
		return 1
	}
	defer cleanup()

	want := release.Expect{Version: *version, Platform: *platform}
	if want.Version == "" {
		want.Version = declaredVersion(dir)
	}

	f := release.CheckDir(dir, pins, want)
	report(out, f, pins, which)
	if f.State != release.Signed {
		return 1
	}

	return 0
}

func pinsFor(rsaPath, mldsaPath string) (release.Pins, string, error) {
	if rsaPath == "" {
		pins, err := release.OfficialPins()
		return pins, "the certificates built into the plugin", err
	}

	rsaCert, err := os.ReadFile(rsaPath)
	if err != nil {
		return release.Pins{}, "", err
	}
	mldsaCert, err := os.ReadFile(mldsaPath)
	if err != nil {
		return release.Pins{}, "", err
	}
	pins, err := release.NewPins(rsaCert, mldsaCert)

	return pins, "the certificates given", err
}

// declaredVersion is what plugin.json says, read only to know what to hold
// the release to: the check itself holds plugin.json to the manifest.
func declaredVersion(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(raw, &manifest)

	return manifest.Version
}

func report(out io.Writer, f release.Finding, pins release.Pins, which string) {
	switch f.State {
	case release.Signed:
		fmt.Fprintf(out, "signed: %s %s %s, %d files\n", release.Product, f.Version, strings.Join(f.Platforms, ","), f.Files)
	case release.Unsigned:
		fmt.Fprintln(out, "unsigned: there is no release folder")
	default:
		fmt.Fprintln(out, "altered:", f.Reason)
	}

	if f.ManifestSHA256 != "" {
		fmt.Fprintln(out, "manifest SHA-256:", release.Colons(f.ManifestSHA256))
	}
	fmt.Fprintf(out, "checked against %s:\n  RSA    %s\n  ML-DSA %s\n", which, release.Colons(pins.RSASHA256), release.Colons(pins.MLDSASHA256))
	if f.UnlistedCount > 0 {
		fmt.Fprintf(out, "files nobody signed: %d (%s)\n", f.UnlistedCount, strings.Join(f.Unlisted, ", "))
	}
}

// pluginFolder is the plugin's folder: the argument itself when it is a
// folder, or a bundle unpacked into a folder of its own, which cleanup
// removes.
func pluginFolder(arg string) (dir string, cleanup func(), err error) {
	info, err := os.Stat(arg)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return arg, func() {}, nil
	}

	tmp, err := os.MkdirTemp("", "checkrelease-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	top, err := unpack(arg, tmp)
	if err != nil {
		cleanup()
		return "", nil, err
	}

	return filepath.Join(tmp, top), cleanup, nil
}

// unpack writes a bundle's regular files and folders under dest and is the
// one folder at its top, which is the plugin's. Links, devices, paths that
// climb out and more than one top folder are refused.
func unpack(bundle, dest string) (string, error) {
	file, err := os.Open(bundle)
	if err != nil {
		return "", err
	}
	defer file.Close()

	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("%s is not a gzip file: %w", bundle, err)
	}
	defer compressed.Close()

	root, err := os.OpenRoot(dest)
	if err != nil {
		return "", err
	}
	defer root.Close()

	archive := tar.NewReader(compressed)
	top := ""
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("the bundle does not read as a tar: %w", err)
		}

		name := strings.TrimPrefix(header.Name, "./")
		clean := path.Clean(name)
		if name == "" || clean == "." || path.IsAbs(name) || hasDotDot(name) {
			return "", fmt.Errorf("the bundle holds %q, which is outside its folder", header.Name)
		}
		first, _, _ := strings.Cut(clean, "/")
		if top == "" {
			top = first
		} else if first != top {
			return "", fmt.Errorf("the bundle holds more than one folder at its top: %s and %s", top, first)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(clean, 0o700); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if clean == first {
				return "", fmt.Errorf("the bundle's top is a file, %s, not a folder", clean)
			}
			total += header.Size
			if header.Size < 0 || total > maxUnpacked {
				return "", errors.New("the bundle unpacks to more than 1 GiB")
			}
			if err := root.MkdirAll(path.Dir(clean), 0o700); err != nil {
				return "", err
			}
			target, err := root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return "", fmt.Errorf("%s: %w", clean, err)
			}
			_, err = io.Copy(target, io.LimitReader(archive, header.Size))
			closeErr := target.Close()
			if err != nil || closeErr != nil {
				return "", fmt.Errorf("%s could not be unpacked", clean)
			}
		default:
			return "", fmt.Errorf("%s is not a regular file or a folder", clean)
		}
	}

	if top == "" {
		return "", errors.New("the bundle is empty")
	}

	return top, nil
}

func hasDotDot(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}

	return false
}
