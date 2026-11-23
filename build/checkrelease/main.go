// Command checkrelease checks a signed release of Matty Plugin with the same
// code the plugin checks itself with (server/release): both signatures over
// the manifest, then every file it lists, and that what Mattermost loads is
// among them.
//
//	checkrelease [-rsa-cert FILE -mldsa-cert FILE] [-version V] [-platform P] [-exact] BUNDLE
//
// BUNDLE is a bundle (.tar.gz) or an unpacked plugin folder. Without
// certificates it checks against the two built into the plugin, which are
// Matty's own. -version is the version the release must be (by default the
// one its plugin.json says); -platform the one platform whose executable
// must be listed (by default every platform the manifest names); -exact
// refuses any file the manifest does not list, as the plugin does with a
// bundle it hands to a system admin to install, and holds a bundle to the
// plugin's own limits for one it downloads: at most 64 MB, unpacked within
// release.BundleLimits.
//
// It prints what it found and exits 0 for a signed release, 1 for anything
// else, and 2 when it was not asked properly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// limitsFor is what a bundle is unpacked within: with -exact the plugin's
// own (release.BundleLimits), which a bundle a system admin installs must
// pass; otherwise those, with room for a bundle of every platform's
// program.
func limitsFor(exact bool) release.UnpackLimits {
	if exact {
		return release.BundleLimits
	}

	return release.UnpackLimits{Top: release.Product, Entries: release.BundleLimits.Entries, Bytes: 1 << 30}
}

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
	exact := flags.Bool("exact", false, "refuse any file the manifest does not list")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || (*rsaCert == "") != (*mldsaCert == "") {
		fmt.Fprintln(errOut, "usage: checkrelease [-rsa-cert FILE -mldsa-cert FILE] [-version V] [-platform P] [-exact] BUNDLE")
		return 2
	}

	pins, which, err := pinsFor(*rsaCert, *mldsaCert)
	if err != nil {
		fmt.Fprintln(errOut, "checkrelease:", err)
		return 2
	}

	dir, cleanup, err := pluginFolder(flags.Arg(0), *exact)
	if err != nil {
		fmt.Fprintln(out, "altered:", err)
		return 1
	}
	defer cleanup()

	want := release.Expect{Version: *version, Platform: *platform, Exact: *exact}
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
// removes. With exact a bundle larger than the plugin downloads is refused
// before it is read.
func pluginFolder(arg string, exact bool) (dir string, cleanup func(), err error) {
	info, err := os.Stat(arg)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return arg, func() {}, nil
	}
	if exact && info.Size() > release.BundleDownloadBytes {
		return "", nil, fmt.Errorf("the bundle is larger than %d MB, which the plugin does not download", release.BundleDownloadBytes>>20)
	}

	tmp, err := os.MkdirTemp("", "checkrelease-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	top, err := unpack(arg, tmp, limitsFor(exact))
	if err != nil {
		cleanup()
		return "", nil, err
	}

	return filepath.Join(tmp, top), cleanup, nil
}

// unpack writes a bundle under dest with the unpacker the plugin takes a
// downloaded release with (release.Unpack): regular files and folders under
// the one folder ceo.villa.matty, and nothing else.
func unpack(bundle, dest string, limits release.UnpackLimits) (string, error) {
	file, err := os.Open(bundle)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return release.Unpack(context.Background(), file, dest, limits)
}
