//go:build !villanet_standin

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// A release takes nothing of what only the development build takes: the
// debug build's package, an attestation made in software, a root that is not
// pinned, an unlocked phone. app_check_standin_test.go is the other side.
func TestAReleaseTakesNothingTheDevelopmentBuildTakes(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	cfg := a.p.getConfiguration().Clone()
	cfg.AppCheckCertificates = debugCertificate
	a.p.setConfiguration(cfg)

	emulator := officialPhone
	emulator.Package, emulator.Digest, emulator.Level, emulator.Locked, emulator.BootState = "ceo.villa.matty.debug", debugCertificate, 0, false, 2
	require.Equal(t, "This phone made its attestation in software, not in its secure hardware.", decoded(t, a.prove(t, sceneLinked, davesPhone, emulator, appSite))["reason"])

	emulator.Level = 1
	require.Equal(t, "This phone's bootloader is unlocked.", decoded(t, a.prove(t, sceneLinked, davesPhone, emulator, appSite))["reason"])

	emulator.Locked, emulator.BootState = true, 0
	require.Equal(t, "The app that asked is ceo.villa.matty.debug, not Matty.", decoded(t, a.prove(t, sceneLinked, davesPhone, emulator, appSite))["reason"])

	// Under a root that is not Google's, which is every emulator's.
	a.p.appCheckRoots = nil
	response := a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.", decoded(t, response)["reason"])
}

// A verdict only the development build gives does not count in a release,
// kept in a store the two shared: a development verdict, or one for the
// debug build's package.
func TestAReleaseDoesNotCountADevelopmentVerdict(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	now := a.p.appCheck().now()
	good := appCheckResult{
		User: sceneLinked, Verified: true, At: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli(),
		Package: mattyPackage, Digest: release.OfficialRSASHA256, RevocationList: appcheck.RevocationListDate,
	}
	require.NoError(t, a.p.keepResult(davesPhone, &good, now))
	require.Equal(t, "verified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])

	for name, change := range map[string]func(*appCheckResult){
		"a development verdict": func(r *appCheckResult) { r.Development = true },
		"the debug package":     func(r *appCheckResult) { r.Package = "ceo.villa.matty.debug" },
	} {
		stale := good
		change(&stale)
		require.NoError(t, a.p.keepResult(davesPhone, &stale, now))
		require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"], name)
		require.Equal(t, http.StatusForbidden, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil).Code, name)
	}
}
