//go:build villanet_standin

package main

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/build/fakebb/standin"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// The stand-in build looks for its updates on this machine, at the stand-in
// beside it, and nowhere else: plain http there, a download sent on only to
// it, the certificates it serves, and no need to be a signed release itself.
func TestStandInBuildAsksThisMachineForUpdates(t *testing.T) {
	require.Equal(t, standInSteam, updateAPIBase)
	require.Equal(t, standInSteam, updateSiteBase)
	require.True(t, updatePlainOK)
	require.False(t, updateNeedsSigned)
	require.NotNil(t, updatePinsFrom)

	for address, allowed := range map[string]bool{
		standInSteam + "/vvilla-01/matty-plugin/releases/download/v1.2.3/x": true,
		"https://github.com/x":                    false,
		"https://objects.githubusercontent.com/x": false,
		"http://127.0.0.1:7789/x":                 false,
		"http://localhost:7788/x":                 false,
	} {
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		require.Equal(t, allowed, standInRedirectOK(parsed), address)
	}
}

// What build/make-standin-update.sh leaves, served by the stand-in fakebb
// runs, goes through the plugin's own code: looked for, downloaded,
// unpacked, checked against the certificates the stand-in serves, and
// handed to a system admin as checked. With no folder there is no release.
func TestStandInHandsOutABundleSignedWithTheStandInsCertificates(t *testing.T) {
	dir := t.TempDir()
	keys := theUpdateKeys(t)
	certificate := func(public any, signer crypto.Signer) []byte {
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Stand-in signer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
		der, err := x509.CreateCertificate(rand.Reader, template, template, public, signer)
		require.NoError(t, err)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rsa.pem"), certificate(keys.rsa.Public(), keys.rsa), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mldsa87.pem"), certificate(keys.mldsa.PublicKey(), keys.mldsa), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, updateAssetName(testNewer, testPlatform)), makeBundle(t, newerBundle(t)), 0o644))

	mux := http.NewServeMux()
	standin.NewReleases(dir).Routes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := server.Listener.Addr().String()

	s := newUpdateScene(t)
	standInNode := func() *Plugin {
		p := s.nodeWith(true, func(r *updateRuntime) {
			r.apiBase, r.siteBase, r.plainOK = server.URL, server.URL, true
			r.transport = http.DefaultTransport.(*http.Transport).Clone()
			r.redirectOK = func(address *url.URL) bool { return address.Scheme == "http" && address.Host == host }
			r.needsSigned = false
			r.pins = standInPinsAt(server.URL)
		})
		p.releaseFound.Store(&release.Finding{State: release.Unsigned})
		return p
	}
	p := standInNode()

	look(p)
	require.Equal(t, testNewer, p.updates().seenNow().Latest)
	require.True(t, p.updates().seenNow().Asset)
	require.Equal(t, &updateInstallView{Ready: true}, s.card(p).Install, "not a signed release, and it need not be")
	answer := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusOK, answer.Code, answer.Body.String())
	checkArchive(t, answer.Body.Bytes())
	require.Equal(t, server.URL+"/vvilla-01/matty-plugin/releases/tag/v"+testNewer, s.card(p).URL)

	page, err := http.Get(s.card(p).URL)
	require.NoError(t, err)
	_ = page.Body.Close()
	require.Equal(t, http.StatusOK, page.StatusCode)

	// No folder: no release, as GitHub answers before the first one.
	empty := http.NewServeMux()
	standin.NewReleases("").Routes(empty)
	nothing := httptest.NewServer(empty)
	t.Cleanup(nothing.Close)
	p.updates().apiBase = nothing.URL
	look(p)
	require.Empty(t, p.updates().seenNow().Latest)
	require.Empty(t, p.updates().seenNow().Trouble)

	// A bundle signed by other keys than the ones the stand-in serves is
	// refused like any other.
	spec := newerBundle(t)
	spec.version, spec.signedAs, spec.keys = "4.11.0", "4.11.0", someoneElsesKeys(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, updateAssetName("4.11.0", testPlatform)), makeBundle(t, spec), 0o644))
	p = standInNode()
	look(p)
	s.advance(2 * time.Minute)
	refused := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusBadGateway, refused.Code)
	require.Contains(t, s.card(p).Trouble, "do not verify")
}

// The stand-in build says the version it was made to be wherever the
// running version is said: the card, /access and /me alike.
func TestStandInSaysItsOwnVersionEverywhere(t *testing.T) {
	require.Equal(t, updateRunning, runningVersion())
	require.Equal(t, runningVersion(), newUpdateRuntime().running)
}
