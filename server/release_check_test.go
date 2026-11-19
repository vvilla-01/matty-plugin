package main

import (
	"crypto"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// Signing keys made here, once, for the plugin's own check of its folder.
var (
	releaseKeysOnce sync.Once
	releaseRSA      *rsa.PrivateKey
	releaseMLDSA    *mldsa.PrivateKey
	releaseTestPins release.Pins
)

func releaseTestKeys(t *testing.T) {
	t.Helper()

	releaseKeysOnce.Do(func() {
		var err error
		releaseRSA, err = rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			panic(err)
		}
		releaseMLDSA, err = mldsa.GenerateKey(mldsa.MLDSA87())
		if err != nil {
			panic(err)
		}
		certificate := func(public any, signer crypto.Signer) []byte {
			template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test signer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
			der, err := x509.CreateCertificate(rand.Reader, template, template, public, signer)
			if err != nil {
				panic(err)
			}
			return der
		}
		releaseTestPins, err = release.NewPins(certificate(releaseRSA.Public(), releaseRSA), certificate(releaseMLDSA.PublicKey(), releaseMLDSA))
		if err != nil {
			panic(err)
		}
	})
}

// signedPluginFolder is the plugin's folder as Mattermost unpacks a signed
// release of this version for this platform.
func signedPluginFolder(t *testing.T) string {
	t.Helper()
	releaseTestKeys(t)

	dir := t.TempDir()
	executable := "server/dist/plugin-" + thisPlatform()
	files := map[string]string{
		"plugin.json":         fmt.Sprintf(`{"id":%q,"version":%q,"server":{"executables":{%q:%q}},"webapp":{"bundle_path":"webapp/dist/main.js"}}`, manifestID, manifestVersion, thisPlatform(), executable),
		"assets/icon.svg":     "<svg/>",
		executable:            "the server",
		"webapp/dist/main.js": "the panel",
	}
	paths := make([]string, 0, len(files))
	for name, content := range files {
		writeReleaseFile(t, dir, name, []byte(content))
		paths = append(paths, name)
	}
	sort.Strings(paths)

	var manifest strings.Builder
	fmt.Fprintf(&manifest, "%s\nproduct %s\nkind %s\nversion %s\nplatform %s\nfiles %d\n", release.Header, release.Product, release.Kind, manifestVersion, thisPlatform(), len(paths))
	for _, name := range paths {
		sum := sha256.Sum256([]byte(files[name]))
		fmt.Fprintf(&manifest, "%s %d %s\n", hex.EncodeToString(sum[:]), len(files[name]), name)
	}
	m := []byte(manifest.String())

	digest := sha512.Sum512(m)
	rsaSig, err := rsa.SignPSS(rand.Reader, releaseRSA, crypto.SHA512, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	require.NoError(t, err)
	mldsaSig, err := releaseMLDSA.Sign(nil, m, nil)
	require.NoError(t, err)

	writeReleaseFile(t, dir, release.ManifestPath, m)
	writeReleaseFile(t, dir, release.RSASignaturePath, rsaSig)
	writeReleaseFile(t, dir, release.MLDSASignaturePath, mldsaSig)

	return dir
}

func writeReleaseFile(t *testing.T, dir, name string, content []byte) {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, content, 0o644))
}

// releasePlugin is a plugin whose folder is dir, checking against the test's
// keys, and the log lines it wrote.
func releasePlugin(t *testing.T, dir string, bundleErr error) (*Plugin, *[]string) {
	t.Helper()
	releaseTestKeys(t)

	api := &plugintest.API{}
	api.On("GetBundlePath").Return(dir, bundleErr)
	var lock sync.Mutex
	logs := []string{}
	for _, level := range []string{"LogInfo", "LogWarn"} {
		for pairs := 0; pairs <= 6; pairs++ {
			args := make([]any, 1+pairs*2)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(level, args...).Run(func(args mock.Arguments) {
				lock.Lock()
				defer lock.Unlock()
				line := level + ": " + args.String(0)
				for i := 1; i+1 < len(args); i += 2 {
					line += fmt.Sprintf(" %v=%v", args[i], args[i+1])
				}
				logs = append(logs, line)
			}).Return()
		}
	}

	p := &Plugin{}
	p.SetAPI(api)
	p.releasePins = func() (release.Pins, error) { return releaseTestPins, nil }

	return p, &logs
}

// The usual start reads the plugin's own folder once, says what it found in
// the log once, and keeps it.
func TestThePluginChecksItsOwnFolderOnceAtTheUsualStart(t *testing.T) {
	require.Equal(t, "the release check", (&Plugin{}).usualStarters()[0].name)

	dir := signedPluginFolder(t)
	p, logs := releasePlugin(t, dir, nil)
	p.checkOwnRelease()
	require.Equal(t, release.Signed, p.ownRelease().State)
	require.Len(t, *logs, 1)
	require.Contains(t, (*logs)[0], "LogInfo: Matty is a signed release")

	// Kept, not worked out again: a file changed afterwards is not seen
	// until the next start.
	writeReleaseFile(t, dir, "webapp/dist/main.js", []byte("the panel, changed"))
	require.Equal(t, release.Signed, p.ownRelease().State)
	p.API.(*plugintest.API).AssertNumberOfCalls(t, "GetBundlePath", 1)

	// The next start finds it, names the file and goes on.
	p, logs = releasePlugin(t, dir, nil)
	p.checkOwnRelease()
	found := p.ownRelease()
	require.Equal(t, release.Altered, found.State)
	require.Equal(t, "webapp/dist/main.js", found.File)
	require.Equal(t, []string{"LogWarn: Matty's files do not match a signed release version=" + manifestVersion + " reason=webapp/dist/main.js has changed file=webapp/dist/main.js"}, *logs)

	// No release folder: a development build.
	p, logs = releasePlugin(t, t.TempDir(), nil)
	p.checkOwnRelease()
	require.Equal(t, release.Unsigned, p.ownRelease().State)
	require.Contains(t, (*logs)[0], "LogInfo: Matty is not a signed release")

	// A folder Mattermost will not name is not taken as signed.
	p, _ = releasePlugin(t, "", errors.New("no bundle path"))
	p.checkOwnRelease()
	require.Equal(t, release.Altered, p.ownRelease().State)
	require.Contains(t, p.ownRelease().Reason, "no bundle path")

	// Nor is a release signed by anybody else: the certificates built in.
	p, _ = releasePlugin(t, signedPluginFolder(t), nil)
	p.releasePins = nil
	p.checkOwnRelease()
	require.Equal(t, release.Altered, p.ownRelease().State)
	require.Contains(t, p.ownRelease().Reason, "the RSA signature does not verify; the ML-DSA signature does not verify")
}

func releaseOf(t *testing.T, s *accessScene, userID string) releaseResponse {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/release", userID, "", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var answer releaseResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer
}

func releaseAnswerOf(t *testing.T, s *accessScene, path, userID string) map[string]any {
	t.Helper()

	response := s.serve(http.MethodGet, path, userID, "", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var answer map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer
}

// GET /api/v1/release in each of the three states, to anybody signed in, the
// locked included; and the word on /access and /me that says whether to ask.
func TestTheReleaseRouteInEachState(t *testing.T) {
	s := newAccessScene(t)

	// Unsigned, and the same before any check has run.
	require.Equal(t, releaseResponse{State: "unsigned", Version: manifestVersion}, releaseOf(t, s, sceneLocked))
	unsignedWord := map[string]any{"state": "unsigned", "version": manifestVersion}
	require.Equal(t, unsignedWord, releaseAnswerOf(t, s, "/api/v1/access", sceneLocked)["release"])
	require.Equal(t, unsignedWord, releaseAnswerOf(t, s, "/api/v1/me", sceneLocked)["release"])
	require.NotContains(t, releaseAnswerOf(t, s, "/api/v1/me", sceneLinkedAdmin), "releaseAlert")

	// Signed: the manifest as text and both signatures, byte for byte.
	dir := signedPluginFolder(t)
	found := release.CheckDir(dir, releaseTestPins, release.Expect{Version: manifestVersion, Platform: thisPlatform()})
	require.Equal(t, release.Signed, found.State, found.Reason)
	s.p.releaseFound.Store(&found)

	signed := releaseOf(t, s, sceneLocked)
	require.Equal(t, "signed", signed.State)
	require.Empty(t, signed.Reason)
	manifest, err := os.ReadFile(filepath.Join(dir, release.ManifestPath))
	require.NoError(t, err)
	require.Equal(t, string(manifest), signed.Manifest)
	rsaSig, err := base64.StdEncoding.DecodeString(signed.RSA)
	require.NoError(t, err)
	mldsaSig, err := base64.StdEncoding.DecodeString(signed.MLDSA87)
	require.NoError(t, err)
	require.NoError(t, release.VerifySignatures(releaseTestPins, []byte(signed.Manifest), rsaSig, mldsaSig), "what is sent verifies as it is")
	require.Equal(t, map[string]any{"state": "signed", "version": manifestVersion}, releaseAnswerOf(t, s, "/api/v1/access", sceneLetIn)["release"])
	require.NotContains(t, releaseAnswerOf(t, s, "/api/v1/me", sceneLinkedAdmin), "releaseAlert")

	// Signed, with files nobody signed: admins are told which.
	withExtra := found
	withExtra.UnlistedCount, withExtra.Unlisted = 1, []string{"notes.txt"}
	s.p.releaseFound.Store(&withExtra)
	require.Equal(t, map[string]any{"state": "signed", "unlisted": float64(1), "unlistedFiles": []any{"notes.txt"}},
		releaseAnswerOf(t, s, "/api/v1/me", sceneLinkedAdmin)["releaseAlert"])
	require.NotContains(t, releaseAnswerOf(t, s, "/api/v1/me", sceneLinked), "releaseAlert", "admins only")

	// Altered: the reason, and the files as found, for a client to see the
	// same failure for itself.
	writeReleaseFile(t, dir, "assets/icon.svg", []byte("<svg>changed</svg>"))
	altered := release.CheckDir(dir, releaseTestPins, release.Expect{Version: manifestVersion, Platform: thisPlatform()})
	s.p.releaseFound.Store(&altered)

	answer := releaseOf(t, s, sceneLocked)
	require.Equal(t, "altered", answer.State)
	require.Equal(t, "assets/icon.svg has changed", answer.Reason)
	require.Equal(t, string(manifest), answer.Manifest)
	require.NotEmpty(t, answer.RSA)
	require.NotEmpty(t, answer.MLDSA87)
	require.Equal(t, map[string]any{"state": "altered", "version": manifestVersion}, releaseAnswerOf(t, s, "/api/v1/me", sceneLocked)["release"])

	alert, ok := releaseAnswerOf(t, s, "/api/v1/me", sceneLinkedAdmin)["releaseAlert"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "altered", alert["state"])
	require.Equal(t, "assets/icon.svg", alert["file"])
	require.Contains(t, alert["problem"], "assets/icon.svg has changed. It goes on running.")

	// Anything but GET is refused, and nobody signed out is answered.
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPost, "/api/v1/release", sceneLinked, "", map[string]string{"X-Requested-With": "XMLHttpRequest"}).Code)
	require.Equal(t, http.StatusUnauthorized, s.serve(http.MethodGet, "/api/v1/release", "", "", nil).Code)
}
