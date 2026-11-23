//go:build !villanet_standin

package main

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A release asks Steam, and only Steam, whatever is beside it: the stand-in a
// developer runs (build/fakebb) is reached only by a build made with the
// villanet_standin tag, which make dist never passes. If this fails, something
// that should be behind that tag is in every build.
func TestAReleaseAsksOnlySteam(t *testing.T) {
	require.Equal(t, "https://steamcommunity.com", steamOpenIDBase)
	require.Equal(t, "https://api.steampowered.com", steamAPIBase)

	// And it starts anywhere, asking Mattermost nothing to decide that: this
	// plugin has no API at all, and a question would panic.
	require.Empty(t, (&Plugin{}).standInRefusal())
}

// A release looks for its updates at GitHub, and only there: with HTTPS, a
// download sent on only to GitHub's hosts, its own build of the certificates,
// and only as a signed release; whatever the environment says, and with no
// setting that could say otherwise. The stand-in's releases (build/fakebb)
// are asked only by a build made with the villanet_standin tag.
func TestAReleaseAsksOnlyGitHub(t *testing.T) {
	for _, name := range []string{
		"MATTY_UPDATE_API", "MATTY_UPDATE_SITE", "MATTY_UPDATE_PLAIN", "MATTY_UPDATE_PINS", "MATTY_UPDATE_REDIRECT",
		"MATTY_STANDIN", "VILLANET_STANDIN", "GITHUB_API_URL", "GITHUB_SERVER_URL", "GH_HOST",
	} {
		t.Setenv(name, "http://127.0.0.1:7788")
	}

	require.Equal(t, "https://api.github.com", updateAPIBase)
	require.Equal(t, "https://github.com", updateSiteBase)
	require.False(t, updatePlainOK)
	require.True(t, updateNeedsSigned)
	require.Nil(t, updatePinsFrom)
	require.Equal(t, manifestVersion, updateRunning)
	require.Equal(t, manifestVersion, runningVersion())

	r := newUpdateRuntime()
	require.Equal(t, "https://api.github.com", r.apiBase)
	require.Equal(t, "https://github.com", r.siteBase)
	require.False(t, r.plainOK)
	require.True(t, r.needsSigned)
	require.Nil(t, r.pins)
	require.Equal(t, manifestVersion, r.running)
	require.Equal(t, "https://github.com/vvilla-01/matty-plugin/releases/tag/v9.9.9", r.page("9.9.9"))
	for address, allowed := range map[string]bool{
		"https://github.com/x":                                true,
		"https://GITHUB.COM/x":                                true,
		"https://objects.githubusercontent.com/x":             true,
		"https://objects.GitHubUserContent.com/x":             true,
		"https://release-assets.githubusercontent.com:443/x":  true,
		"http://github.com/x":                                 false,
		"http://127.0.0.1:7788/x":                             false,
		"https://localhost/x":                                 false,
		"https://127.0.0.1/x":                                 false,
		"https://[::1]/x":                                     false,
		"https://github.com./x":                               false,
		"https://objects.githubusercontent.com./x":            false,
		"https://github.com.example.org/x":                    false,
		"https://githubusercontent.com/x":                     false,
		"https://evilgithubusercontent.com/x":                 false,
		"https://objects.githubusercontent.co/x":              false,
		"https://github.co/x":                                 false,
		"https://user@github.com/x":                           false,
		"https://github.com:8443/x":                           false,
		"https://objects.githubusercontent.com.example.org/x": false,
	} {
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		require.Equal(t, allowed, r.redirectOK(parsed), address)
	}

	// Nothing reads the environment or a setting for any of it: only
	// standin.go, which a release does not have, sets these, and the
	// runtime takes them from nowhere else. No file of the release writes
	// one of the runtime's own fields either, but its constructor: an
	// assignment whose left side ends in one of them, alone or among
	// others, is refused in any file that reaches the runtime (names it or
	// calls updates()), and the five names no other type has are refused
	// in every file. (running and pins are also other types' fields.)
	setters := regexp.MustCompile(`(?m)^[^/\n]*\b(updateAPIBase|updateSiteBase|updatePlainOK|updateRedirectOK|updateNeedsSigned|updatePinsFrom|updateRunning)\b[\w \t,]*=[^=].*$`)
	fields := regexp.MustCompile(`(?m)^\s*(apiBase|siteBase|plainOK|redirectOK|needsSigned|pins|running):\s*(.+),$`)
	assigned := regexp.MustCompile(`(?m)^\s*((?:[\w.()\[\]*&]+\s*,\s*)*[\w.()\[\]*&]+)\s*(?::=|=|\+=|-=)[^=]`)
	ownField := regexp.MustCompile(`\.(apiBase|siteBase|plainOK|redirectOK|needsSigned|pins|running)$`)
	onlyOwn := regexp.MustCompile(`\.(apiBase|siteBase|plainOK|redirectOK|needsSigned)$`)
	reaches := regexp.MustCompile(`updateRuntime|\.updates\(\)`)
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	released, err := filepath.Glob("*/*.go")
	require.NoError(t, err)
	deeper, err := filepath.Glob("*/*/*.go")
	require.NoError(t, err)
	released = append(released, deeper...)
	require.Contains(t, released, filepath.Join("release", "release.go"))
	for _, file := range append(sources, released...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		source := string(raw)
		if file == "update.go" || strings.HasPrefix(file, "release/") {
			for _, reads := range []string{"os.Getenv", "os.LookupEnv", "os.Environ", "syscall.Getenv"} {
				require.NotContains(t, source, reads, file)
			}
		}
		if file == "standin.go" {
			continue
		}
		rule := onlyOwn
		if reaches.MatchString(source) {
			rule = ownField
		}
		for _, match := range assigned.FindAllStringSubmatch(source, -1) {
			for _, part := range strings.Split(match[1], ",") {
				require.NotRegexp(t, rule, strings.TrimSpace(part), "%s writes the update runtime's %q", file, strings.TrimSpace(match[0]))
			}
		}
		if file != "update.go" {
			require.Empty(t, fields.FindAllString(source, -1), "%s builds an update runtime of its own", file)
		}
		for _, match := range setters.FindAllString(source, -1) {
			require.Regexp(t, `^\s*update\w+\s*=\s*("https://|githubRedirectOK|false|true|manifestVersion)|^\s*updatePinsFrom func`, match, "%s sets %q", file, match)
		}
		if file == "update.go" {
			want := map[string]string{"apiBase": "updateAPIBase", "siteBase": "updateSiteBase", "plainOK": "updatePlainOK", "redirectOK": "updateRedirectOK", "needsSigned": "updateNeedsSigned", "pins": "updatePinsFrom", "running": "runningVersion()"}
			found := fields.FindAllStringSubmatch(source, -1)
			require.Len(t, found, len(want))
			for _, field := range found {
				require.Equal(t, want[field[1]], field[2], field[1])
			}
		}
	}
}

// A node of a release build started with every variable a leak could read,
// switched on and changed, still has the runtime a release has: GitHub over
// HTTPS, GitHub's redirect rule, the certificates built in, only a signed
// release handing out a bundle, and the plugin's own version. Its looks go
// to a transport that answers nothing, so nothing leaves this machine.
func TestAReleaseNodeStartedWithTheVariablesSetIsTheSame(t *testing.T) {
	for _, name := range []string{
		"MATTY_UPDATE_API", "MATTY_UPDATE_SITE", "MATTY_UPDATE_PLAIN", "MATTY_UPDATE_PINS", "MATTY_UPDATE_REDIRECT",
		"MATTY_UPDATE_SIGNED", "MATTY_UPDATE_RUNNING", "MATTY_STANDIN", "VILLANET_STANDIN", "GITHUB_API_URL", "GITHUB_SERVER_URL", "GH_HOST",
	} {
		t.Setenv(name, "http://127.0.0.1:7788")
	}

	s := newUpdateScene(t)
	asked := make(chan string, 16)
	p := s.nodeWith(false, func(r *updateRuntime) {
		fresh := newUpdateRuntime()
		r.apiBase, r.siteBase, r.plainOK, r.redirectOK = fresh.apiBase, fresh.siteBase, fresh.plainOK, fresh.redirectOK
		r.needsSigned, r.pins, r.running = fresh.needsSigned, fresh.pins, fresh.running
		r.transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			asked <- request.URL.Scheme + "://" + request.URL.Host
			return nil, errors.New("nothing leaves this machine in a test")
		})
	})
	off, on := &configuration{}, &configuration{EnableUpdates: true}
	p.setConfiguration(on)
	p.updatesChanged(off, on)
	p.updatesChanged(on, on)
	look(p)

	r := p.updates()
	require.Equal(t, "https://api.github.com", r.apiBase)
	require.Equal(t, "https://github.com", r.siteBase)
	require.False(t, r.plainOK)
	require.True(t, r.needsSigned)
	require.Nil(t, r.pins)
	require.Equal(t, manifestVersion, r.running)
	require.Equal(t, manifestVersion, runningVersion())
	for address, allowed := range map[string]bool{"https://github.com/x": true, "http://127.0.0.1:7788/x": false, "https://127.0.0.1:7788/x": false} {
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		require.Equal(t, allowed, r.redirectOK(parsed), address)
	}
	require.Eventually(t, func() bool { return len(asked) > 0 }, 5*time.Second, 5*time.Millisecond)
	for len(asked) > 0 {
		require.Equal(t, "https://api.github.com", <-asked)
	}
}

// A release build checks a bundle against the two certificates built into
// it, and never asks for the stand-in's: a bundle signed with any other
// keys is refused, though somebody serves their certificates beside it.
func TestAReleaseChecksABundleOnlyAgainstItsOwnCertificates(t *testing.T) {
	s := newUpdateScene(t)
	s.github.route("/fake/update/rsa.pem", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("a certificate")) })
	s.github.route("/fake/update/mldsa87.pem", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("a certificate")) })
	p := s.nodeWith(true, func(r *updateRuntime) { r.pins = updatePinsFrom })
	s.ready(p)

	refused := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusBadGateway, refused.Code, refused.Body.String())
	require.Contains(t, refused.Body.String(), "do not verify against Matty's two certificates")
	for _, request := range s.github.asked() {
		require.NotContains(t, request.URL.Path, "/fake/")
	}
}
