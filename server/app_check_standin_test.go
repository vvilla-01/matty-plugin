//go:build villanet_standin

package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The development build takes what the emulator and a debug build make, says
// "development" beside it, and still holds the signing certificate to the
// list.
func TestStandInAppCheckTakesTheEmulatorAndSaysSo(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	// Any self-signed root: the emulator's is its own.
	a.p.appCheckRoots = nil

	emulator := officialPhone
	emulator.Package, emulator.Digest, emulator.Level, emulator.Locked, emulator.BootState = "ceo.villa.matty.debug", debugCertificate, 0, false, 2

	refused := a.prove(t, sceneLinked, davesPhone, emulator, appSite)
	require.Equal(t, http.StatusOK, refused.Code)
	require.Equal(t, "This copy of Matty was signed with a certificate this server does not accept (5A:6B:7C:8D:9E:AF:00:11…).", decoded(t, refused)["reason"])

	cfg := a.p.getConfiguration().Clone()
	cfg.AppCheckCertificates = debugCertificate
	a.p.setConfiguration(cfg)

	passed := a.prove(t, sceneLinked, davesPhone, emulator, appSite)
	require.Equal(t, map[string]any{"state": "verified", "development": true, "versionCode": float64(28), "expires": float64(a.now.AddDate(0, 0, 30).UnixMilli())}, decoded(t, passed))
	require.Equal(t, map[string]any{"check": "require", "state": "verified", "development": true, "versionCode": float64(28)},
		a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil).Code)

	// Any boot state, an owner's key nobody listed included, and the verdict
	// goes on counting when it is read again.
	ownersSystem := emulator
	ownersSystem.Locked, ownersSystem.BootState, ownersSystem.BootKey = true, 1, "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLinked, davesLaptop, ownersSystem, appSite))["state"])
	require.Equal(t, "verified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop)["state"])
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil).Code)

	// A real phone under Google's root is no development verdict.
	a.p.appCheckRoots = map[string]bool{a.root.Key(): true}
	require.Equal(t, map[string]any{"state": "verified", "versionCode": float64(28), "expires": float64(a.now.AddDate(0, 0, 30).UnixMilli())},
		decoded(t, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)))
}
