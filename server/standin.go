//go:build villanet_standin

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// A build for developing on one machine (make standin-linux), and never a
// release: only a build made with the villanet_standin tag has this file.
//
// The same holds for the plugin's own updates (update.go): GitHub's address
// is fixed, and this build asks the stand-in in its place.
//
// Where Steam lives is not a setting, on purpose (steam.go): the sign-in's
// address is fixed, so nobody can point the server at something that is not
// Steam. That is also what keeps a release from ever asking a fake. So the
// one thing a stand-in for Steam needs of the plugin is here: both of Steam's
// hosts are the stand-in beside it (build/fakebb), which vouches for whichever
// account anybody picks. Nothing else changes. Every check of the sign-in
// still runs, against the stand-in's own signatures, and Mumble needs nothing
// at all, since where Mumble lives is a setting.
//
// Because the stand-in takes anybody's word, a build that asks it must never
// serve real people. Three things say which build this is: the bundle's file
// name and the version Mattermost shows for it (make standin-linux puts
// "standin" in both), and standInRefusal, which keeps the plugin from starting
// anywhere but on a Mattermost whose Site URL is this machine.

// standInSteam is where the stand-in listens. Not a setting and not read from
// the environment: this build can ask this machine, and nothing else.
const standInSteam = "http://127.0.0.1:7788"

// standInVersion is the version this build is to the plugin's updates, when
// build/make-standin-update.sh makes one to install, with
// -ldflags "-X main.standInVersion=<version>": so that once installed the
// card, /access and /me say that version (runningVersion). Empty: the
// plugin's own.
var standInVersion = ""

func init() {
	steamOpenIDBase = standInSteam
	steamAPIBase = standInSteam

	// The plugin's updates ask the stand-in beside it, which serves releases
	// as GitHub does, from a folder of bundles signed with throwaway keys,
	// and the two certificates they are checked against. This build is
	// never a signed release, so it need not be one to hand out a bundle.
	updateAPIBase, updateSiteBase = standInSteam, standInSteam
	updatePlainOK = true
	updateRedirectOK = standInRedirectOK
	updateNeedsSigned = false
	updatePinsFrom = standInPinsAt(standInSteam)
	if plainVersion(standInVersion) == standInVersion && standInVersion != "" {
		updateRunning = standInVersion
	}
}

// standInRedirectOK lets a download be sent on only to the stand-in itself.
func standInRedirectOK(address *url.URL) bool {
	return address != nil && address.Scheme == "http" && address.Host == strings.TrimPrefix(standInSteam, "http://") && address.User == nil
}

// standInPinsAt are the certificates the stand-in at base signs its bundles
// with, as it serves them.
func standInPinsAt(base string) func(ctx context.Context, client *http.Client) (release.Pins, error) {
	return func(ctx context.Context, client *http.Client) (release.Pins, error) {
		return standInPins(ctx, client, base)
	}
}

func standInPins(ctx context.Context, client *http.Client, base string) (release.Pins, error) {
	read := func(name string) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/fake/update/"+name, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, errors.New("the stand-in has no " + name)
		}

		return io.ReadAll(io.LimitReader(response.Body, 64<<10))
	}

	rsaCert, err := read("rsa.pem")
	if err != nil {
		return release.Pins{}, err
	}
	mldsaCert, err := read("mldsa87.pem")
	if err != nil {
		return release.Pins{}, err
	}

	return release.NewPins(rsaCert, mldsaCert)
}

// standInRefusal is why this build will not start here, or "" where it may:
// only where Mattermost's Site URL is this machine. A development server's
// is; a server people use never is.
func (p *Plugin) standInRefusal() string {
	return standInSiteRefusal(p.siteURL())
}

func standInSiteRefusal(siteURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(siteURL))
	if err == nil && isLocalAccountHost(parsed.Hostname()) {
		return ""
	}

	// The Makefile's NO_STANDIN guard greps a release build for "development
	// build of Matty Plugin": keep this a literal, and change both together.
	return "This is a development build of Matty Plugin: it takes a stand-in's word for whose Steam account is whose, " +
		"so it only starts where the Site URL is this machine (http://localhost:8065, for one). Install a release bundle here instead."
}

// mattyDebugPackage is the package name of Matty's debug build, which only
// this build's app check accepts.
const mattyDebugPackage = "ceo.villa.matty.debug"

// withDevelopmentAppCheck is the app check as this build makes it: it also
// takes what an emulator and a debug build produce (a software security
// level, the emulator's own test root, any boot state, the debug build's
// package), and says "development" beside every such verdict. The signing
// certificate is still held to the list, so a debug build's has to be added
// to More signing certificates. A release has none of it (standin_off.go,
// and appcheck's developmentBuild).
func withDevelopmentAppCheck(policy appcheck.Policy) appcheck.Policy {
	policy.Development = true
	policy.Packages[mattyDebugPackage] = true

	return policy
}
