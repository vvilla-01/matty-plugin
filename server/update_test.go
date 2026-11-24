package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// Updates for the plugin itself (update.go), against a GitHub of the test's
// own on this machine, over HTTPS, with bundles signed by keys made here.
// The running version is a test's own, never the plugin's: these tests mean
// the same whatever this plugin is numbered.

const (
	testRunning  = "4.9.0"
	testNewer    = "4.10.0"
	testPlatform = "linux-amd64"
	testProgram  = "server/dist/plugin-" + testPlatform
)

// ---------------------------------------------------------------- bundles

type updateKeys struct {
	rsa   *rsa.PrivateKey
	mldsa *mldsa.PrivateKey
	pins  release.Pins
}

var (
	otherKeysOnce sync.Once
	otherKeys     updateKeys
)

// theUpdateKeys are the keys the test's releases are signed with, which the
// plugin under test trusts.
func theUpdateKeys(t *testing.T) updateKeys {
	t.Helper()
	releaseTestKeys(t)

	return updateKeys{rsa: releaseRSA, mldsa: releaseMLDSA, pins: releaseTestPins}
}

// someoneElsesKeys sign a release nobody here trusts.
func someoneElsesKeys(t *testing.T) updateKeys {
	t.Helper()

	otherKeysOnce.Do(func() {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			panic(err)
		}
		mldsaKey, err := mldsa.GenerateKey(mldsa.MLDSA87())
		if err != nil {
			panic(err)
		}
		otherKeys = updateKeys{rsa: rsaKey, mldsa: mldsaKey}
	})

	return otherKeys
}

// bundleSpec is a release bundle to make: its version and id as plugin.json
// says, the version it is signed as, the keys, the modes its tar gives some
// files, and what is added once it is signed.
type bundleSpec struct {
	version  string
	signedAs string
	id       string
	keys     updateKeys
	modes    map[string]int64
	unlisted map[string][]byte
	extra    []*tar.Header
}

func newerBundle(t *testing.T) bundleSpec {
	return bundleSpec{version: testNewer, signedAs: testNewer, id: manifestID, keys: theUpdateKeys(t)}
}

// bundleFiles are a release's files, as make dist-linux lays them out.
func bundleFiles(spec bundleSpec) map[string][]byte {
	return map[string][]byte{
		"plugin.json":         []byte(fmt.Sprintf(`{"id":%q,"version":%q,"server":{"executables":{%q:%q}},"webapp":{"bundle_path":"webapp/dist/main.js"}}`, spec.id, spec.version, testPlatform, testProgram)),
		"assets/icon.svg":     []byte("<svg/>"),
		testProgram:           bytes.Repeat([]byte("the server "), 2000),
		"webapp/dist/main.js": []byte("the panel"),
	}
}

func makeBundle(t *testing.T, spec bundleSpec) []byte {
	t.Helper()

	files := bundleFiles(spec)
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)

	var manifest strings.Builder
	fmt.Fprintf(&manifest, "%s\nproduct %s\nkind %s\nversion %s\nplatform %s\nfiles %d\n", release.Header, release.Product, release.Kind, spec.signedAs, testPlatform, len(paths))
	for _, name := range paths {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&manifest, "%s %d %s\n", hex.EncodeToString(sum[:]), len(files[name]), name)
	}
	m := []byte(manifest.String())
	digest := sha512.Sum512(m)
	rsaSig, err := rsa.SignPSS(rand.Reader, spec.keys.rsa, crypto.SHA512, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	require.NoError(t, err)
	mldsaSig, err := spec.keys.mldsa.Sign(nil, m, nil)
	require.NoError(t, err)

	files[release.ManifestPath] = m
	files[release.RSASignaturePath] = rsaSig
	files[release.MLDSASignaturePath] = mldsaSig
	for name, content := range spec.unlisted {
		files[name] = content
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/", Typeflag: tar.TypeDir, Mode: 0o755}))
	for _, name := range names {
		mode := int64(0o644)
		if strings.HasPrefix(name, "server/dist/") {
			mode = 0o755
		}
		if given, ok := spec.modes[name]; ok {
			mode = given
		}
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: "ceo.villa.matty/" + name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(files[name]))}))
		_, err := archive.Write(files[name])
		require.NoError(t, err)
	}
	for _, header := range spec.extra {
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

// ---------------------------------------------------------------- GitHub

// fakeGitHub answers as GitHub does for the newest release and its files.
type fakeGitHub struct {
	server *httptest.Server

	mu       sync.Mutex
	latest   http.HandlerFunc
	routes   map[string]http.HandlerFunc
	bundles  map[string][]byte
	requests []*http.Request
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()

	g := &fakeGitHub{routes: map[string]http.HandlerFunc{}, bundles: map[string][]byte{}}
	g.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.requests = append(g.requests, r.Clone(context.Background()))
		route, routed := g.routes[r.URL.Path]
		latest := g.latest
		bundle, isBundle := g.bundles[r.URL.Path]
		g.mu.Unlock()

		switch {
		case routed:
			route(w, r)
		case r.URL.Path == "/repos/vvilla-01/matty-plugin/releases/latest" && latest != nil:
			latest(w, r)
		case isBundle:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.server.Close)

	return g
}

// releases answers the newest release as a tag with these files.
func (g *fakeGitHub) releases(tag string, draft, prerelease bool, assets ...string) {
	files := make([]map[string]any, 0, len(assets))
	for _, name := range assets {
		files = append(files, map[string]any{"name": name, "browser_download_url": "https://elsewhere.example/" + name, "size": 1})
	}
	body, _ := json.Marshal(map[string]any{
		"tag_name": tag, "draft": draft, "prerelease": prerelease, "assets": files,
		"name": "Release notes nobody reads", "body": "**Markdown** that must never be shown", "html_url": "https://elsewhere.example/page",
	})

	g.mu.Lock()
	g.latest = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
	g.mu.Unlock()
}

func (g *fakeGitHub) answer(handler http.HandlerFunc) {
	g.mu.Lock()
	g.latest = handler
	g.mu.Unlock()
}

func (g *fakeGitHub) route(path string, handler http.HandlerFunc) {
	g.mu.Lock()
	g.routes[path] = handler
	g.mu.Unlock()
}

func downloadPath(version, asset string) string {
	return "/vvilla-01/matty-plugin/releases/download/v" + version + "/" + asset
}

// serveBundle puts bundle where GitHub keeps version's file for the
// platform.
func (g *fakeGitHub) serveBundle(version string, bundle []byte) {
	g.mu.Lock()
	g.bundles[downloadPath(version, updateAssetName(version, testPlatform))] = bundle
	g.mu.Unlock()
}

func (g *fakeGitHub) asked() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]*http.Request(nil), g.requests...)
}

// ---------------------------------------------------------------- the scene

type updateScene struct {
	t      *testing.T
	store  map[string][]byte
	faults *kvFaults
	github *fakeGitHub
	others []*httptest.Server

	lock          sync.Mutex
	now           time.Time
	users         []*model.User
	roleHonoured  bool
	uploads       bool
	needSignature bool
	configReads   int
	voice         int
	posts         map[string][]string
	nodes         []*Plugin
	scheduled     int
	closed        int
	warns         map[*Plugin][]string
}

func newUpdateScene(t *testing.T) *updateScene {
	t.Helper()

	s := &updateScene{
		t:            t,
		store:        map[string][]byte{},
		faults:       newKVFaults(),
		github:       newFakeGitHub(t),
		now:          time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
		roleHonoured: true,
		uploads:      true,
		posts:        map[string][]string{},
		warns:        map[*Plugin][]string{},
		users: []*model.User{
			{Id: "uid-alice", Username: "alice", Roles: "system_user system_admin"},
			{Id: "uid-bob", Username: "bob", Roles: "system_admin system_user"},
			{Id: "uid-carol", Username: "carol", Roles: "system_user"},
			{Id: "uid-dave", Username: "dave", Roles: "system_user system_admin", DeleteAt: 1},
			{Id: "uid-erin-bot", Username: "erin-bot", Roles: "system_user system_admin", IsBot: true},
		},
	}

	return s
}

func (s *updateScene) clock() time.Time {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.now
}

func (s *updateScene) advance(by time.Duration) {
	s.lock.Lock()
	s.now = s.now.Add(by)
	s.lock.Unlock()
}

func (s *updateScene) set(f func()) {
	s.lock.Lock()
	f()
	s.lock.Unlock()
}

// transport trusts the fake GitHub and every other server the test made.
func (s *updateScene) transport() http.RoundTripper {
	pool := x509.NewCertPool()
	pool.AddCert(s.github.server.Certificate())
	for _, other := range s.others {
		pool.AddCert(other.Certificate())
	}

	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
}

// otherHost is another HTTPS server on this machine, at its own address.
func (s *updateScene) otherHost(handler http.HandlerFunc) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	server.StartTLS()
	s.t.Cleanup(server.Close)
	s.others = append(s.others, server)
	for _, p := range s.nodes {
		p.updates().transport = s.transport()
	}

	return server
}

// testJob is the daily job as a test sees it: made and closed.
type testJob struct{ s *updateScene }

func (j testJob) Close() error {
	j.s.set(func() { j.s.closed++ })
	return nil
}

// node is one more Mattermost node over the same store, with Look for
// updates on or off, started. Mattermost has no InstallPlugin here: a call
// to it fails the test.
func (s *updateScene) node(on bool) *Plugin {
	return s.nodeWith(on, nil)
}

// nodeWith is node, with change made to its updates before they start.
func (s *updateScene) nodeWith(on bool, change func(r *updateRuntime)) *Plugin {
	t := s.t
	t.Helper()

	p, _, _ := kvBackedPluginWithFaultsOn(s.store, s.faults)
	api := p.API.(*plugintest.API)

	// Every warning, kept: ahead of the ones that only allow them.
	var captured []*mock.Call
	for pairs := 0; pairs <= 8; pairs++ {
		args := make([]any, 1+pairs*2)
		for i := range args {
			args[i] = mock.Anything
		}
		captured = append(captured, api.On("LogWarn", args...).Run(func(arguments mock.Arguments) {
			words := make([]string, 0, len(arguments))
			for _, argument := range arguments {
				words = append(words, fmt.Sprint(argument))
			}
			s.set(func() { s.warns[p] = append(s.warns[p], strings.Join(words, " ")) })
		}).Return())
	}
	api.ExpectedCalls = append(captured, api.ExpectedCalls[:len(api.ExpectedCalls)-len(captured)]...)

	api.On("GetConfig").Return(func() *model.Config {
		s.lock.Lock()
		defer s.lock.Unlock()

		s.configReads++
		config := &model.Config{}
		config.PluginSettings.Enable = model.NewPointer(true)
		config.PluginSettings.EnableUploads = model.NewPointer(s.uploads)
		config.PluginSettings.RequirePluginSignature = model.NewPointer(s.needSignature)

		return config
	}).Maybe()
	clipsAdmins(api, "uid-alice", "uid-bob")
	api.On("GetUsers", mock.Anything).Return(func(options *model.UserGetOptions) ([]*model.User, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		var matching []*model.User
		for _, user := range s.users {
			if s.roleHonoured && (!user.IsInRole(options.Role) || (options.Active && user.DeleteAt != 0)) {
				continue
			}
			copied := *user
			matching = append(matching, &copied)
		}
		from, to := min(options.Page*options.PerPage, len(matching)), min((options.Page+1)*options.PerPage, len(matching))

		return matching[from:to], nil
	}).Maybe()
	api.On("GetDirectChannel", "uid-bot", mock.Anything).Return(func(_, other string) (*model.Channel, *model.AppError) {
		return &model.Channel{Id: "dm-" + other}, nil
	}).Maybe()
	api.On("CreatePost", mock.Anything).Return(func(post *model.Post) (*model.Post, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		require.Equal(t, "uid-bot", post.UserId)
		who := strings.TrimPrefix(post.ChannelId, "dm-")
		s.posts[who] = append(s.posts[who], post.Message)

		return post, nil
	}).Maybe()

	p.bridgeLock.Lock()
	p.botUserID = "uid-bot"
	p.bridgeLock.Unlock()
	p.releaseFound.Store(&release.Finding{State: release.Signed})

	cfg := &configuration{EnableUpdates: on}
	cfg.normalize()
	p.setConfiguration(cfg)

	r := p.updates()
	r.now = s.clock
	r.apiBase, r.siteBase = s.github.server.URL, s.github.server.URL
	r.transport = s.transport()
	github, _ := url.Parse(s.github.server.URL)
	r.redirectOK = func(address *url.URL) bool {
		if address.Scheme != "https" {
			return false
		}
		if address.Host == github.Host {
			return true
		}
		for _, other := range s.others {
			if strings.TrimPrefix(other.URL, "https://") == address.Host {
				return true
			}
		}
		return false
	}
	r.platform = testPlatform
	r.running = testRunning
	r.needsSigned = true
	r.lookTimeout = 2 * time.Second
	r.fetchTimeout = 5 * time.Second
	r.tickEvery = time.Hour
	r.tempDir = t.TempDir()
	r.pins = func(context.Context, *http.Client) (release.Pins, error) { return theUpdateKeys(t).pins, nil }
	r.schedule = func(cluster.JobPluginAPI, func()) (clipsJob, error) {
		s.set(func() { s.scheduled++ })
		return testJob{s}, nil
	}
	r.voiceCount = func() int {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.voice
	}

	if change != nil {
		change(r)
	}

	p.startUpdates()
	t.Cleanup(p.stopUpdates)
	s.nodes = append(s.nodes, p)

	return p
}

func (s *updateScene) postsTo(userID string) []string {
	s.lock.Lock()
	defer s.lock.Unlock()

	return append([]string(nil), s.posts[userID]...)
}

func (s *updateScene) reads() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.configReads
}

func (s *updateScene) kv(key string) []byte {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	return append([]byte(nil), s.store[key]...)
}

func (s *updateScene) setKV(key string, value []byte) {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	s.store[key] = value
}

func (s *updateScene) failReads(key string, fail bool) {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	s.faults.readFails[key] = fail
}

func (s *updateScene) card(p *Plugin) updateAdminView {
	s.t.Helper()

	recorder := httptest.NewRecorder()
	p.handleAdminUpdate(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update", nil), "uid-alice")
	require.Equal(s.t, http.StatusOK, recorder.Code, recorder.Body.String())
	var view updateAdminView
	require.NoError(s.t, json.Unmarshal(recorder.Body.Bytes(), &view))

	return view
}

func (s *updateScene) checkNow(p *Plugin, userID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	p.handleAdminUpdateCheck(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/update/check", nil), userID)

	return recorder
}

// bundle is POST /api/v1/admin/update/bundle by userID, answered.
func (s *updateScene) bundle(p *Plugin, userID string) *httptest.ResponseRecorder {
	return s.bundleIn(context.Background(), p, userID)
}

func (s *updateScene) bundleIn(ctx context.Context, p *Plugin, userID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/admin/update/bundle", nil)
	p.handleAdminUpdateBundle(recorder, request, userID)

	return recorder
}

// leftovers are what is in p's private folder for bundles: nothing, once a
// request is answered.
func leftovers(t *testing.T, p *Plugin) []string {
	t.Helper()

	entries, err := os.ReadDir(p.updates().tempDir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// ready makes a newer release known, with its bundle at GitHub, to every
// node.
func (s *updateScene) ready(p *Plugin) []byte {
	s.t.Helper()

	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	bundle := makeBundle(s.t, newerBundle(s.t))
	s.github.serveBundle(testNewer, bundle)
	look(p)
	require.Equal(s.t, testNewer, p.updates().newerSeen())

	return bundle
}

// look is one look by p, as the daily job makes it, waited for.
func look(p *Plugin) {
	p.lookForUpdate(context.Background())
}

// warnings are the LogWarn lines p wrote, each with its fields.
func (s *updateScene) warnings(p *Plugin) []string {
	s.lock.Lock()
	defer s.lock.Unlock()

	return append([]string(nil), s.warns[p]...)
}

// ---------------------------------------------------------------- versions

func TestPluginVersionsAreComparedPartByPart(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"4.10.0", "4.9.0", true},
		{"4.9.1", "4.9.0", true},
		{"5.0.0", "4.99.99", true},
		{"4.9.0", "4.9.0", false},
		{"4.8.9", "4.9.0", false},
		{"4.10.0", "4.9.0+standin", true},
		{"4.9.0", "4.9.0+standin", false},
		{"4.9.0", "4.9.0-debug", false},
		{"v4.10.0", "4.9.0", false},
		{"4.10", "4.9.0", false},
		{"", "4.9.0", false},
		{"4.10.0", "", false},
		{"04.10.0", "4.9.0", false},
	} {
		require.Equal(t, c.newer, newerVersion(c.a, c.b), "%s against %s", c.a, c.b)
	}

	require.Equal(t, "4.9.0", plainVersion("4.9.0+standin"))
	require.Empty(t, plainVersion("four"))
}

// ---------------------------------------------------------------- looking

// The look uses the tag and whether this platform's bundle is there, and
// nothing else of the answer; asks with the plugin's own User-Agent and no
// cookie; and treats a 404 as no release yet.
func TestTheLookReadsOnlyTheTagAndThePlatformsBundle(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	asset := updateAssetName(testNewer, testPlatform)

	cases := []struct {
		name    string
		set     func()
		latest  string
		asset   bool
		newer   bool
		trouble string
	}{
		{name: "newer", set: func() { s.github.releases("v"+testNewer, false, false, asset, "other.txt") }, latest: testNewer, asset: true, newer: true},
		{name: "the same", set: func() { s.github.releases("v"+testRunning, false, false) }, latest: testRunning},
		{name: "older", set: func() { s.github.releases("v4.8.0", false, false) }, latest: "4.8.0"},
		{name: "no release yet", set: func() {
			s.github.answer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		}},
		{name: "a draft", set: func() { s.github.releases("v"+testNewer, true, false, asset) }},
		{name: "a pre-release", set: func() { s.github.releases("v"+testNewer, false, true, asset) }},
		{name: "no bundle for this platform", set: func() {
			s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, "linux-arm64"), "ceo.villa.matty-"+testNewer+".tar.gz")
		}, latest: testNewer, newer: true},
		{name: "a tag that is no version", set: func() { s.github.releases("latest", false, false) }, trouble: "The newest release's tag is not a version such as v4.5.0."},
		{name: "too big", set: func() {
			s.github.answer(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"tag_name":"v4.10.0","name":"` + strings.Repeat("x", updateLookMaxBytes) + `"}`))
			})
		}, trouble: "GitHub's answer was larger than 256 KB."},
		{name: "garbage", set: func() {
			s.github.answer(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>rate limited</html>")) })
		}, trouble: "GitHub's answer could not be read."},
		{name: "a refusal", set: func() {
			s.github.answer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
		}, trouble: "GitHub answered with status 403."},
		{name: "a slow answer", set: func() {
			p.updates().lookTimeout = 100 * time.Millisecond
			s.github.answer(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
			})
		}, trouble: "GitHub did not answer within 15 seconds."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p.updates().setSeen(updateSeen{})
			p.updates().lookTimeout = 2 * time.Second
			c.set()
			look(p)

			seen := p.updates().seenNow()
			require.Equal(t, c.latest, seen.Latest)
			require.Equal(t, c.asset, seen.Asset)
			require.Equal(t, c.trouble, seen.Trouble)
			require.Equal(t, s.clock().UnixMilli(), seen.LookedAt)
			require.Equal(t, c.newer, p.updateWordNow(p.getConfiguration()) != nil)

			// Every node reads the same from the store.
			var kept updateSeen
			require.NoError(t, json.Unmarshal(s.kv(kvUpdateSeen), &kept))
			require.Equal(t, seen, kept)
		})
	}

	requests := s.github.asked()
	require.NotEmpty(t, requests)
	for _, request := range requests {
		require.Equal(t, "/repos/vvilla-01/matty-plugin/releases/latest", request.URL.Path)
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, mediaUserAgent, request.Header.Get("User-Agent"))
		require.Empty(t, request.Header.Get("Cookie"))
		require.Empty(t, request.Header.Get("Authorization"))
		require.Empty(t, request.URL.RawQuery)
	}

	// A look that went wrong says nothing new about the release: the newest
	// one known stays known, with the trouble beside it.
	s.github.releases("v"+testNewer, false, false, asset)
	look(p)
	s.github.answer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	look(p)
	seen := p.updates().seenNow()
	require.Equal(t, testNewer, seen.Latest)
	require.Equal(t, "GitHub answered with status 502.", seen.Trouble)
	require.Equal(t, &updateWord{Version: testNewer, URL: s.github.server.URL + "/vvilla-01/matty-plugin/releases/tag/v" + testNewer}, p.updateWordNow(p.getConfiguration()))

	// Only HTTPS: a plain address is never asked.
	before := len(s.github.asked())
	p.updates().apiBase = strings.Replace(s.github.server.URL, "https://", "http://", 1)
	look(p)
	require.Equal(t, "GitHub could not be reached.", p.updates().seenNow().Trouble)
	require.Len(t, s.github.asked(), before)
}

// A look that was stopped, by the plugin stopping or the setting going off,
// is no look: nothing is kept, no trouble is shown, nothing is logged.
// Check now's look is the plugin's own, so a browser that leaves does not
// cut it short.
func TestALookThatWasStoppedKeepsNothing(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testNewer, false, false)
	p := s.node(true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.lookForUpdate(ctx)
	require.Equal(t, updateSeen{}, p.updates().seenNow())
	require.Empty(t, s.kv(kvUpdateSeen))
	require.Empty(t, s.warnings(p))

	// Stopped in the middle of the answer.
	middle, stop := context.WithCancel(context.Background())
	defer stop()
	s.github.answer(func(w http.ResponseWriter, r *http.Request) {
		stop()
		<-r.Context().Done()
	})
	p.lookForUpdate(middle)
	require.Empty(t, s.kv(kvUpdateSeen))

	// Check now from a browser that has already gone.
	s.github.releases("v"+testNewer, false, false)
	gone, leave := context.WithCancel(context.Background())
	leave()
	recorder := httptest.NewRecorder()
	p.handleAdminUpdateCheck(recorder, httptest.NewRequestWithContext(gone, http.MethodPost, "/api/v1/admin/update/check", nil), "uid-alice")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, testNewer, p.updates().seenNow().Latest)
	require.Empty(t, p.updates().seenNow().Trouble)
}

// A request that fails is logged by its kind alone: never the error's own
// words, which carry what the other end sent, such as a certificate's names.
func TestAFailedLookIsLoggedByItsKindOnly(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testNewer, false, false)
	p := s.node(true)

	// The fake GitHub's certificate names example.com and this machine's
	// addresses, and not localhost.
	_, port, err := net.SplitHostPort(strings.TrimPrefix(s.github.server.URL, "https://"))
	require.NoError(t, err)
	p.updates().apiBase = "https://localhost:" + port
	look(p)
	require.Equal(t, "GitHub could not be reached.", p.updates().seenNow().Trouble)

	logged := s.warnings(p)
	require.Len(t, logged, 1)
	require.Contains(t, logged[0], "kind TLS")
	require.NotContains(t, logged[0], "example.com")
	require.NotContains(t, logged[0], "x509")

	// A timeout is logged by its kind like the others; a status GitHub
	// answered with is said on the card and not logged.
	p.updates().apiBase = s.github.server.URL
	p.updates().lookTimeout = 100 * time.Millisecond
	s.github.answer(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	look(p)
	require.Equal(t, "GitHub did not answer within 15 seconds.", p.updates().seenNow().Trouble)
	logged = s.warnings(p)
	require.Len(t, logged, 2)
	require.Contains(t, logged[1], "kind timeout")

	// The status and the headers come at once and the body stalls past the
	// look's limit: a timeout too, logged by its kind.
	s.github.answer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tag_name":`))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	p.updates().setSeen(updateSeen{})
	look(p)
	require.Equal(t, "GitHub did not answer within 15 seconds.", p.updates().seenNow().Trouble)
	logged = s.warnings(p)
	require.Len(t, logged, 3)
	require.Contains(t, logged[2], "kind timeout")

	p.updates().lookTimeout = 2 * time.Second
	s.github.answer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	look(p)
	require.Equal(t, "GitHub answered with status 403.", p.updates().seenNow().Trouble)
	require.Len(t, s.warnings(p), 3)

	for err, kind := range map[error]string{
		context.DeadlineExceeded:                                         "timeout",
		&net.DNSError{Err: "no such host", Name: "words.example"}:        "name lookup",
		x509.HostnameError{Host: "words.example"}:                        "TLS",
		&net.OpError{Op: "dial", Err: errors.New("connection refused")}:  "connection",
		fmt.Errorf("wrapped: %w", &net.OpError{Op: "read", Err: io.EOF}): "connection",
		errors.New("something else"):                                     "other",
	} {
		require.Equal(t, kind, requestFailureKind(err), "%v", err)
	}
}

// Off asks nothing of anybody: not the daily job, not Check now, not the
// bundle, not /me; and neither the job nor the minute's refresh exists.
func TestUpdatesOffAskNothing(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	p := s.node(false)

	look(p)
	recorder := s.checkNow(p, "uid-alice")
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), updateOffText)
	require.Empty(t, s.github.asked())
	require.False(t, p.updates().updatesOnNow())
	require.Zero(t, s.scheduled)

	// What an earlier look found is not shown while it is off, and no bundle
	// is handed out.
	p.updates().setSeen(updateSeen{Latest: testNewer, Asset: true, Platform: testPlatform, LookedAt: 1})
	require.Nil(t, p.updateWordNow(p.getConfiguration()))
	require.Nil(t, p.accessUpdateWordNow(p.getConfiguration()))
	require.False(t, s.card(p).Enabled)
	require.Equal(t, testPlatform, s.card(p).Platform, "the platform is always on the card")
	refusal := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusConflict, refusal.Code)
	require.Equal(t, codeConflict, codedErrorOrEmpty(refusal))
	require.Contains(t, refusal.Body.String(), updateOffText)

	// Nobody but a system admin sees the card, presses Check now or gets a
	// bundle.
	refused := s.checkNow(p, "uid-carol")
	require.Equal(t, http.StatusForbidden, refused.Code)
	recorder = httptest.NewRecorder()
	p.handleAdminUpdate(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update", nil), "uid-carol")
	require.Equal(t, http.StatusForbidden, recorder.Code)
	refused = s.bundle(p, "uid-carol")
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Contains(t, refused.Body.String(), updateAdminOnlyText)
	require.Empty(t, s.github.asked())
}

// The daily job and the minute's refresh exist only while the setting is
// on: started when it goes on, stopped when it goes off.
func TestTheDailyLookExistsOnlyWhileTheSettingIsOn(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testRunning, false, false)
	p := s.nodeWith(false, func(r *updateRuntime) { r.tickEvery = 5 * time.Millisecond })
	off, on := &configuration{}, &configuration{EnableUpdates: true}

	require.False(t, p.updates().updatesOnNow())
	require.Zero(t, s.scheduled)

	p.setConfiguration(on)
	p.updatesChanged(off, on)
	require.True(t, p.updates().updatesOnNow())
	require.Equal(t, 1, s.scheduled)
	p.updatesChanged(on, on)
	require.Equal(t, 1, s.scheduled, "a save that changes nothing makes no second job")

	// The refresh reads what another node kept, once the look that turning
	// it on made has kept its own.
	require.Eventually(t, func() bool { return len(s.kv(kvUpdateSeen)) > 0 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	raw, _ := json.Marshal(updateSeen{Latest: "4.11.0", Asset: true, Platform: testPlatform, LookedAt: 7})
	s.setKV(kvUpdateSeen, raw)
	require.Eventually(t, func() bool { return p.updates().seenNow().Latest == "4.11.0" }, 5*time.Second, 5*time.Millisecond)

	p.setConfiguration(off)
	p.updatesChanged(on, off)
	require.False(t, p.updates().updatesOnNow())
	require.Equal(t, 1, s.closed)
	raw, _ = json.Marshal(updateSeen{Latest: "4.12.0", LookedAt: 8})
	s.setKV(kvUpdateSeen, raw)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, "4.11.0", p.updates().seenNow().Latest, "no refresh while off")

	// A node that starts with it on has them; its stop ends them.
	q := s.node(true)
	require.True(t, q.updates().updatesOnNow())
	require.Equal(t, 2, s.scheduled)
	q.stopUpdates()
	require.False(t, q.updates().updatesOnNow())
	require.Equal(t, 2, s.closed)
}

// A refresh under way when the setting goes off ends before the switch
// returns: once it has returned, nothing of the minute's work reads the
// store or changes this node's copy.
func TestTheMinutesRefreshEndsBeforeTheSettingGoesOff(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testRunning, false, false)
	p := s.nodeWith(false, func(r *updateRuntime) { r.tickEvery = 5 * time.Millisecond })
	off, on := &configuration{}, &configuration{EnableUpdates: true}
	p.setConfiguration(on)
	p.updatesChanged(off, on)
	require.Eventually(t, func() bool { return len(s.kv(kvUpdateSeen)) > 0 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	// The next refresh is held in its read, which then finds a newer word.
	newer, _ := json.Marshal(updateSeen{Latest: "4.12.0", LookedAt: 8})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.faults.setOnRead(func(key string) {
		if key != kvUpdateSeen {
			return
		}
		once.Do(func() {
			close(entered)
			<-release
			s.store[kvUpdateSeen] = newer
		})
	})
	<-entered

	atOff := make(chan string, 1)
	go func() {
		p.setConfiguration(off)
		p.updatesChanged(on, off)
		atOff <- p.updates().seenNow().Latest
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	seen := <-atOff

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, seen, p.updates().seenNow().Latest, "a refresh changed the copy after the setting went off")
	reads := 0
	s.faults.setOnRead(func(key string) {
		if key == kvUpdateSeen {
			reads++
		}
	})
	time.Sleep(50 * time.Millisecond)
	s.faults.setOnRead(nil)
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()
	require.Zero(t, reads, "the store read while the setting is off")
}

// Check now looks at once, once a minute for the whole server, whichever
// node is asked; turning the setting on looks once, on one node.
func TestCheckNowAndTurningOnLookOnceForTheWholeServer(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testRunning, false, false)
	one, two := s.node(true), s.node(true)

	first := s.checkNow(one, "uid-alice")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var view updateAdminView
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &view))
	require.Equal(t, testRunning, view.Latest)
	require.False(t, view.Newer)
	require.Nil(t, view.Install)

	again := s.checkNow(two, "uid-bob")
	require.Equal(t, http.StatusTooManyRequests, again.Code)
	require.Contains(t, again.Body.String(), updateTooSoonText)
	require.Equal(t, codeRateLimited, codedErrorOrEmpty(again))
	require.Len(t, s.github.asked(), 1)

	s.advance(61 * time.Second)
	require.Equal(t, http.StatusOK, s.checkNow(two, "uid-bob").Code)
	require.Len(t, s.github.asked(), 2)

	// The setting goes from off to on on every node at once: one looks.
	off := &configuration{}
	on := &configuration{EnableUpdates: true}
	var wg sync.WaitGroup
	for _, p := range []*Plugin{one, two} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.updatesChanged(off, on)
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return len(s.github.asked()) == 3 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Len(t, s.github.asked(), 3)

	// A save that keeps it on, or the first load, is no "turned on".
	one.updatesChanged(on, on)
	one.updatesChanged(nil, on)
	time.Sleep(50 * time.Millisecond)
	require.Len(t, s.github.asked(), 3)
}

// Check now pressed again within the minute says the time really left of
// it in retryAfterMs, whichever node is asked, and never less than a second.
func TestCheckNowTooSoonSaysTheTimeLeft(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testRunning, false, false)
	one, two := s.node(true), s.node(true)
	waits := func(recorder *httptest.ResponseRecorder) int64 {
		t.Helper()
		require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
		require.Equal(t, codeRateLimited, codedErrorOrEmpty(recorder))
		var body errorResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.Equal(t, updateTooSoonText, body.Error)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		return body.RetryAfterMs
	}

	require.Equal(t, http.StatusOK, s.checkNow(one, "uid-alice").Code)
	require.Equal(t, int64(60000), waits(s.checkNow(one, "uid-alice")))
	s.advance(5 * time.Second)
	require.Equal(t, int64(55000), waits(s.checkNow(two, "uid-bob")))
	s.advance(54*time.Second + 700*time.Millisecond)
	require.Equal(t, int64(1000), waits(s.checkNow(one, "uid-bob")))
	require.Len(t, s.github.asked(), 1)

	s.advance(time.Second)
	require.Equal(t, http.StatusOK, s.checkNow(two, "uid-bob").Code)
	require.Len(t, s.github.asked(), 2)
}

// The daily look and the look when the setting goes on take the look's
// claim. When the store does not answer, the node says so in one warning
// and looks anyway; only a claim another node holds skips the look.
func TestALookWhoseClaimTheStoreDidNotAnswerStillLooks(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	p := s.node(true)

	s.failReads(kvUpdateLookClaim, true)
	p.lookClaimed(context.Background())
	require.Len(t, s.github.asked(), 1)
	require.Equal(t, testNewer, p.updates().seenNow().Latest)
	logged := s.warnings(p)
	require.Len(t, logged, 1)
	require.Equal(t, "Could not claim the look for a plugin update kind store", logged[0])

	// Another node holds the claim: no look, and nothing to say.
	s.failReads(kvUpdateLookClaim, false)
	s.advance(time.Hour)
	s.setKV(kvUpdateLookClaim, []byte(fmt.Sprint(s.clock().UnixMilli())))
	p.lookClaimed(context.Background())
	require.Len(t, s.github.asked(), 1)
	require.Len(t, s.warnings(p), 1)

	// Free again: this node claims it and looks.
	s.advance(2 * time.Minute)
	p.lookClaimed(context.Background())
	require.Len(t, s.github.asked(), 2)
	require.Len(t, s.warnings(p), 1)
}

// A store that does not answer during a claim is said as that, 503, and
// never as somebody having asked a minute ago.
func TestAStoreErrorDuringAClaimIsNotTooSoon(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	s.ready(p)

	s.failReads(kvUpdateCheckClaim, true)
	recorder := s.checkNow(p, "uid-alice")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, accessCodeStore, codedErrorOrEmpty(recorder))
	require.Contains(t, recorder.Body.String(), updateStoreText)

	s.failReads(kvUpdateBundleClaim, true)
	recorder = s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, accessCodeStore, codedErrorOrEmpty(recorder))

	// And the store back, the bundle is handed out.
	s.failReads(kvUpdateBundleClaim, false)
	require.Equal(t, http.StatusOK, s.bundle(p, "uid-alice").Code)
}

// ---------------------------------------------------------------- telling

// Each system admin gets one message for a version, from the bot, with the
// page, however many nodes act on it at once; nobody else does, and never a
// bot or somebody deactivated.
func TestEachAdminIsToldOnceAVersionWithTwoNodesRacing(t *testing.T) {
	for _, honoured := range []bool{true, false} {
		t.Run(fmt.Sprintf("the role filter honoured: %v", honoured), func(t *testing.T) {
			s := newUpdateScene(t)
			s.roleHonoured = honoured
			s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
			nodes := []*Plugin{s.node(true), s.node(true)}

			seen, done := nodes[0].lookOnce(context.Background())
			require.True(t, done)
			for _, p := range nodes {
				p.updates().setSeen(seen)
			}
			var wg sync.WaitGroup
			for range 3 {
				for _, p := range nodes {
					wg.Add(1)
					go func() {
						defer wg.Done()
						p.afterUpdateLook(seen)
					}()
				}
			}
			wg.Wait()

			page := s.github.server.URL + "/vvilla-01/matty-plugin/releases/tag/v" + testNewer
			want := []string{"Matty Plugin " + testNewer + " is out. To install it, a system admin opens System Console > Plugins > Matty > Updates. What is new: " + page}
			require.Equal(t, want, s.postsTo("uid-alice"))
			require.Equal(t, want, s.postsTo("uid-bob"))
			for _, nobody := range []string{"uid-carol", "uid-dave", "uid-erin-bot"} {
				require.Empty(t, s.postsTo(nobody), nobody)
			}

			// The next day's look tells nobody again.
			s.advance(24 * time.Hour)
			look(nodes[1])
			require.Len(t, s.postsTo("uid-alice"), 1)
		})
	}
}

// When not one admin was reached, the claim is given back, so the next look
// tells them.
func TestNobodyReachedIsToldAtTheNextLook(t *testing.T) {
	s := newUpdateScene(t)
	admins := s.users
	s.users = nil
	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	p := s.node(true)

	look(p)
	require.Empty(t, s.kv(kvUpdateToldPrefix+testNewer))

	s.set(func() { s.users = admins })
	look(p)
	require.Len(t, s.postsTo("uid-alice"), 1)
	require.NotEmpty(t, s.kv(kvUpdateToldPrefix+testNewer))
}

// At most twenty admins are told.
func TestAtMostTwentyAdminsAreTold(t *testing.T) {
	s := newUpdateScene(t)
	s.users = nil
	for i := range 25 {
		s.users = append(s.users, &model.User{Id: fmt.Sprintf("uid-admin-%02d", i), Roles: "system_admin"})
	}
	p := s.node(true)

	require.Equal(t, updateMaxAdmins, p.tellAdmins("A message."))
	told := 0
	for i := range 25 {
		told += len(s.postsTo(fmt.Sprintf("uid-admin-%02d", i)))
	}
	require.Equal(t, updateMaxAdmins, told)
}

// /me tells a system admin as the panel's alert, and /access tells Matty,
// with whether a bundle can be had from here, for system admins alone,
// while the setting is on. Neither asks Mattermost for its configuration.
func TestTheAlertAndAccessTellSystemAdminsAlone(t *testing.T) {
	s := newAccessScene(t)
	cfg := *s.p.getConfiguration()
	cfg.EnableUpdates = true
	s.p.setConfiguration(&cfg)
	s.p.releaseFound.Store(&release.Finding{State: release.Signed})
	r := s.p.updates()
	r.running = testRunning
	r.platform = testPlatform
	r.uploads = true
	r.setSeen(updateSeen{Latest: testNewer, Asset: true, Platform: testPlatform, LookedAt: 1})
	page := "https://github.com/vvilla-01/matty-plugin/releases/tag/v" + testNewer

	var access accessResponse
	answer := s.serve(http.MethodGet, "/api/v1/access", sceneLinkedAdmin, "", nil)
	require.Equal(t, http.StatusOK, answer.Code)
	require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &access))
	require.Equal(t, &accessUpdateWord{Version: testNewer, URL: page, Install: true}, access.Update)
	require.Equal(t, runningVersion(), access.Version)
	contractFixture(t, "access-update.json", answer.Body.Bytes())

	var me meResponse
	require.NoError(t, json.Unmarshal(s.serve(http.MethodGet, "/api/v1/me", sceneLockedAdmin, "", nil).Body.Bytes(), &me))
	require.Equal(t, &updateWord{Version: testNewer, URL: page}, me.UpdateAlert)
	require.Equal(t, runningVersion(), me.Version)

	// install is false, and still written, when a bundle cannot be had.
	r.mu.Lock()
	r.uploads = false
	r.mu.Unlock()
	answer = s.serve(http.MethodGet, "/api/v1/access", sceneLinkedAdmin, "", nil)
	require.Contains(t, answer.Body.String(), `"install":false`)

	for _, nobody := range []string{sceneLinked, sceneLetIn, sceneLocked} {
		require.NotContains(t, s.serve(http.MethodGet, "/api/v1/access", nobody, "", nil).Body.String(), `"update"`, nobody)
		require.NotContains(t, s.serve(http.MethodGet, "/api/v1/me", nobody, "", nil).Body.String(), `"updateAlert"`, nobody)
	}

	// Off: nobody is told, and /access does not even ask who is an admin.
	cfg.EnableUpdates = false
	s.p.setConfiguration(&cfg)
	asked := len(s.api.Calls)
	require.NotContains(t, s.serve(http.MethodGet, "/api/v1/access", sceneLinkedAdmin, "", nil).Body.String(), `"update"`)
	for _, call := range s.api.Calls[asked:] {
		require.NotEqual(t, "HasPermissionTo", call.Method)
	}
	require.NotContains(t, s.serve(http.MethodGet, "/api/v1/me", sceneLockedAdmin, "", nil).Body.String(), `"updateAlert"`)
}

// /access, /me and the card say the one running version, whatever it is:
// set to 9.9.0, as a stand-in made to be 9.9.0 is, all three say 9.9.0.
func TestTheRunningVersionIsSaidEverywhere(t *testing.T) {
	was := updateRunning
	updateRunning = "9.9.0"
	t.Cleanup(func() { updateRunning = was })

	s := newAccessScene(t)
	cfg := *s.p.getConfiguration()
	cfg.EnableUpdates = true
	s.p.setConfiguration(&cfg)
	require.Equal(t, "9.9.0", s.p.updates().running)

	var access accessResponse
	require.NoError(t, json.Unmarshal(s.serve(http.MethodGet, "/api/v1/access", sceneLinkedAdmin, "", nil).Body.Bytes(), &access))
	require.Equal(t, "9.9.0", access.Version)

	var me meResponse
	require.NoError(t, json.Unmarshal(s.serve(http.MethodGet, "/api/v1/me", sceneLockedAdmin, "", nil).Body.Bytes(), &me))
	require.Equal(t, "9.9.0", me.Version)

	recorder := httptest.NewRecorder()
	s.p.handleAdminUpdate(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update", nil), sceneLinkedAdmin)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var view updateAdminView
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &view))
	require.Equal(t, "9.9.0", view.Running)
}

// ---------------------------------------------------------------- the bundle

// What the archive's own tar headers say: each name's mode, in order.
type packedEntry struct {
	name string
	mode int64
	dir  bool
}

func packedEntries(t *testing.T, archive []byte) []packedEntry {
	t.Helper()

	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	reader := tar.NewReader(compressed)
	var entries []packedEntry
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, packedEntry{name: header.Name, mode: header.Mode, dir: header.Typeflag == tar.TypeDir})
	}

	return entries
}

// checkArchive checks what a client is handed as the plugin did, exactly:
// the files of the release, as signed, and nothing else; and reads the
// archive's own tar headers, which are what Mattermost unpacks with: one
// folder entry for the top, 0755 for the program plugin.json names and for
// folders, 0644 for every other file, and a folder only where it holds a
// file.
func checkArchive(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()

	dest := t.TempDir()
	top, err := release.Unpack(context.Background(), bytes.NewReader(archive), dest, release.BundleLimits)
	require.NoError(t, err)
	found := release.CheckDir(filepath.Join(dest, top), theUpdateKeys(t).pins, release.Expect{Version: testNewer, Platform: testPlatform, Exact: true})
	require.Equal(t, release.Signed, found.State, found.Reason)

	files := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(filepath.Join(dest, top), func(path string, entry os.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.Type().IsRegular() {
			rel, _ := filepath.Rel(filepath.Join(dest, top), path)
			files[filepath.ToSlash(rel)], err = os.ReadFile(path)
			require.NoError(t, err)
		}
		return nil
	}))

	tops := 0
	folders := map[string]bool{}
	for _, entry := range packedEntries(t, archive) {
		require.True(t, strings.HasPrefix(entry.name, "ceo.villa.matty/"), entry.name)
		name := strings.TrimPrefix(entry.name, "ceo.villa.matty/")
		switch {
		case entry.dir && name == "":
			tops++
			require.Equal(t, int64(0o755), entry.mode)
		case entry.dir:
			folders[strings.TrimSuffix(name, "/")] = true
			require.Equal(t, int64(0o755), entry.mode, entry.name)
		case name == testProgram:
			require.Equal(t, int64(0o755), entry.mode, entry.name)
		default:
			require.Equal(t, int64(0o644), entry.mode, entry.name)
		}
	}
	require.Equal(t, 1, tops, "one folder entry for the top")
	for folder := range folders {
		holds := false
		for name := range files {
			holds = holds || strings.HasPrefix(name, folder+"/")
		}
		require.True(t, holds, "%s holds no file", folder)
	}

	return files
}

// A system admin's bundle is a newer release downloaded, unpacked, checked
// and packed again from what was checked: never the downloaded bytes, with
// the answer's headers, and no folder left behind.
func TestTheBundleIsWhatWasChecked(t *testing.T) {
	s := newUpdateScene(t)
	asset := updateAssetName(testNewer, testPlatform)
	s.github.releases("v"+testNewer, false, false, asset)
	bundle := makeBundle(t, newerBundle(t))

	// The bundle is where GitHub redirects its downloads to: another host.
	objects := s.otherHost(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Cookie"))
		require.Equal(t, mediaUserAgent, r.Header.Get("User-Agent"))
		_, _ = w.Write(bundle)
	})
	s.github.route(downloadPath(testNewer, asset), func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, objects.URL+"/assets/"+asset, http.StatusFound)
	})
	p := s.node(true)
	look(p)
	require.Equal(t, &updateInstallView{Ready: true}, s.card(p).Install)

	recorder := s.bundle(p, "uid-bob")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	archive := recorder.Body.Bytes()
	require.Equal(t, "application/gzip", recorder.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="ceo.villa.matty-`+testNewer+`-linux-amd64.tar.gz"`, recorder.Header().Get("Content-Disposition"))
	require.Equal(t, fmt.Sprint(len(archive)), recorder.Header().Get("Content-Length"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, testNewer, recorder.Header().Get("X-Matty-Update-Version"))
	require.NotEqual(t, bundle, archive, "a fresh archive, never the downloaded bytes")

	files := checkArchive(t, archive)
	for name, content := range bundleFiles(newerBundle(t)) {
		require.Equal(t, content, files[name], name)
	}
	require.Len(t, files, len(bundleFiles(newerBundle(t)))+3, "the release's files and its manifest and two signatures")
	require.Empty(t, leftovers(t, p))
	require.Empty(t, s.card(p).Trouble)
}

// The execute bit is no part of what is signed, so the archive's modes are
// its own: the program plugin.json names is packed 0755 whatever the
// download said, and any other file 0644. A folder that holds no listed
// file is not packed.
func TestModesAndFoldersAreTheArchivesOwn(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	spec := newerBundle(t)
	spec.modes = map[string]int64{testProgram: 0o644, "webapp/dist/main.js": 0o755, "plugin.json": 0o4777}
	spec.extra = []*tar.Header{
		{Name: "ceo.villa.matty/public/", Typeflag: tar.TypeDir, Mode: 0o777},
		{Name: "ceo.villa.matty/release/extra/", Typeflag: tar.TypeDir, Mode: 0o755},
	}
	s.github.serveBundle(testNewer, makeBundle(t, spec))
	p := s.node(true)
	look(p)

	recorder := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	checkArchive(t, recorder.Body.Bytes())
	var names []string
	for _, entry := range packedEntries(t, recorder.Body.Bytes()) {
		names = append(names, entry.name)
	}
	require.Equal(t, []string{
		"ceo.villa.matty/",
		"ceo.villa.matty/assets/",
		"ceo.villa.matty/assets/icon.svg",
		"ceo.villa.matty/plugin.json",
		"ceo.villa.matty/release/",
		"ceo.villa.matty/release/manifest.mldsa87.sig",
		"ceo.villa.matty/release/manifest.rsa.sig",
		"ceo.villa.matty/release/manifest.txt",
		"ceo.villa.matty/server/",
		"ceo.villa.matty/server/dist/",
		"ceo.villa.matty/server/dist/plugin-linux-amd64",
		"ceo.villa.matty/webapp/",
		"ceo.villa.matty/webapp/dist/",
		"ceo.villa.matty/webapp/dist/main.js",
	}, names)
}

// Every refusal of the bundle route, in its order: who, how, the setting,
// a newer version, what keeps it from being installed from here (the first
// that holds), a minute since the last, and a bundle that is not right.
func TestTheBundleRouteRefusesInOrder(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	refused := func(recorder *httptest.ResponseRecorder, status int, code, words string) {
		t.Helper()
		require.Equal(t, status, recorder.Code, recorder.Body.String())
		require.Equal(t, code, codedErrorOrEmpty(recorder))
		var body errorResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.Equal(t, words, body.Error)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	}

	refused(s.bundle(p, "uid-carol"), http.StatusForbidden, codeForbidden, updateAdminOnlyText)
	wrong := httptest.NewRecorder()
	p.handleAdminUpdateBundle(wrong, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update/bundle", nil), "uid-alice")
	require.Equal(t, http.StatusMethodNotAllowed, wrong.Code)

	refused(s.bundle(p, "uid-alice"), http.StatusConflict, codeConflict, updateNoneText)

	// Every reason holds: the first is said.
	s.github.releases("v"+testNewer, false, false)
	look(p)
	p.releaseFound.Store(&release.Finding{State: release.Unsigned})
	s.set(func() { s.uploads = false })
	p.rememberUploads()
	require.Equal(t, &updateInstallView{Missing: []string{updateMissingSigned, updateMissingAsset, updateMissingUploads}}, s.card(p).Install)
	refused(s.bundle(p, "uid-alice"), http.StatusConflict, codeConflict, "This copy of Matty Plugin is not a signed release, so it does not install releases. Install "+testNewer+" by hand from its page.")

	p.releaseFound.Store(&release.Finding{State: release.Signed})
	refused(s.bundle(p, "uid-alice"), http.StatusConflict, codeConflict, "Matty Plugin "+testNewer+" has no bundle for this server's platform (linux-amd64).")

	s.ready(p)
	refused(s.bundle(p, "uid-alice"), http.StatusConflict, codeConflict, updateUploadsText)

	// Uploads on, but Mattermost asks for its own signature: still no.
	s.set(func() { s.uploads, s.needSignature = true, true })
	p.rememberUploads()
	refused(s.bundle(p, "uid-alice"), http.StatusConflict, codeConflict, updateUploadsText)
	require.Equal(t, []string{updateMissingUploads}, s.card(p).Install.Missing)

	s.set(func() { s.needSignature = false })
	p.rememberUploads()
	require.Equal(t, http.StatusOK, s.bundle(p, "uid-alice").Code)

	// Within a minute, on this node or another, with the time really left,
	// and never less than a second.
	waits := func(recorder *httptest.ResponseRecorder) int64 {
		t.Helper()
		refused(recorder, http.StatusTooManyRequests, codeRateLimited, "A bundle was asked for less than a minute ago. Try again in a minute.")
		var body errorResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		return body.RetryAfterMs
	}
	require.Equal(t, int64(60000), waits(s.bundle(p, "uid-bob")))
	other := s.node(true)
	look(other)
	s.advance(45 * time.Second)
	require.Equal(t, int64(15000), waits(s.bundle(other, "uid-bob")))
	s.advance(14*time.Second + 700*time.Millisecond)
	require.Equal(t, int64(1000), waits(s.bundle(p, "uid-bob")))

	// A bundle that is not right: 502 with the plain reason, which becomes
	// the card's trouble.
	s.advance(2 * time.Second)
	s.github.serveBundle(testNewer, []byte("<html>not found</html>"))
	refused(s.bundle(p, "uid-alice"), http.StatusBadGateway, codeUnreachable, "The bundle is not a gzip file.")
	card := s.card(other)
	require.Equal(t, "The bundle is not a gzip file.", card.Trouble)
	require.Equal(t, s.clock().UnixMilli(), card.TroubleAt)
	require.Empty(t, leftovers(t, p))
}

// A bundle that is handed out whole clears the card's trouble, for every
// node: the card never shows the last bundle as failed beside one that
// worked. A refused bundle leaves it.
func TestABundleHandedOutClearsTheTrouble(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	bundle := s.ready(p)
	other := s.node(true)
	look(other)

	s.github.serveBundle(testNewer, []byte("<html>not found</html>"))
	refused := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusBadGateway, refused.Code, refused.Body.String())
	card := s.card(other)
	require.Equal(t, "The bundle is not a gzip file.", card.Trouble)
	require.Equal(t, s.clock().UnixMilli(), card.TroubleAt)

	// Refused again within the minute: the trouble stays.
	require.Equal(t, http.StatusTooManyRequests, s.bundle(p, "uid-alice").Code)
	require.Equal(t, "The bundle is not a gzip file.", s.card(p).Trouble)

	s.advance(61 * time.Second)
	s.github.serveBundle(testNewer, bundle)
	handed := s.bundle(p, "uid-alice")
	require.Equal(t, http.StatusOK, handed.Code, handed.Body.String())
	for _, node := range []*Plugin{p, other} {
		card := s.card(node)
		require.Empty(t, card.Trouble)
		require.Zero(t, card.TroubleAt)
		require.Equal(t, testNewer, card.Latest, "what the last look found stays")
		require.NotZero(t, card.LookedAt)
	}
	var kept updateSeen
	require.NoError(t, json.Unmarshal(s.kv(kvUpdateSeen), &kept))
	require.Empty(t, kept.Trouble)
	require.Zero(t, kept.TroubleAt)
}

// One bundle at a time on a node: a second request while one is being
// prepared is refused at once, and told that one is.
func TestOneBundleAtATime(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	bundle := s.ready(p)
	release1, began := make(chan struct{}), make(chan struct{}, 1)
	s.github.route(downloadPath(testNewer, updateAssetName(testNewer, testPlatform)), func(w http.ResponseWriter, r *http.Request) {
		began <- struct{}{}
		<-release1
		_, _ = w.Write(bundle)
	})

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- s.bundle(p, "uid-alice") }()
	<-began

	s.advance(2 * time.Minute)
	second := s.bundle(p, "uid-bob")
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, codeRateLimited, codedErrorOrEmpty(second))
	require.Contains(t, second.Body.String(), "A bundle is being prepared just now. Try again in a minute.")

	close(release1)
	require.Equal(t, http.StatusOK, (<-first).Code)
	require.Empty(t, leftovers(t, p))
}

// The plugin stopping in the middle ends the work: the download stops,
// nothing is kept, no folder is left, the card shows no trouble, and the
// answer is 503 stopping with its sentence, never an empty 200 a client
// would upload. A context that ends does the same. (Under Mattermost a
// request's own context never ends, so a browser that leaves does not stop
// the work: the download's two minutes do.)
func TestThePluginStoppingEndsTheBundlesWork(t *testing.T) {
	for _, how := range []string{"the plugin stops", "a context that ends"} {
		t.Run(how, func(t *testing.T) {
			s := newUpdateScene(t)
			p := s.node(true)
			s.ready(p)
			began, ended := make(chan struct{}, 1), make(chan struct{})
			s.github.route(downloadPath(testNewer, updateAssetName(testNewer, testPlatform)), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "1000000")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte{0x1f, 0x8b})
				w.(http.Flusher).Flush()
				began <- struct{}{}
				<-r.Context().Done()
				close(ended)
			})

			ctx, leave := context.WithCancel(context.Background())
			defer leave()
			answered := make(chan *httptest.ResponseRecorder, 1)
			go func() { answered <- s.bundleIn(ctx, p, "uid-alice") }()
			<-began
			if how == "a context that ends" {
				leave()
			} else {
				p.stopUpdates()
			}

			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("the download went on")
			}
			answer := <-answered
			require.Equal(t, http.StatusServiceUnavailable, answer.Code)
			require.Equal(t, codeStopping, codedErrorOrEmpty(answer))
			var body errorResponse
			require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &body))
			require.Equal(t, "The plugin stopped while it got the bundle. Try again once it is back.", body.Error)
			require.Empty(t, answer.Header().Get("X-Matty-Update-Version"))
			require.Equal(t, "no-store", answer.Header().Get("Cache-Control"))
			require.Empty(t, leftovers(t, p))
			require.Empty(t, s.card(p).Trouble)
			p.updates().mu.Lock()
			require.False(t, p.updates().preparing)
			p.updates().mu.Unlock()
		})
	}
}

// Every way a bundle can be wrong is refused with one of a fixed set of
// sentences, which hold no word of the download, and becomes the card's
// trouble; nothing is handed out and no folder is left.
func TestABundleThatIsNotRightIsRefused(t *testing.T) {
	asset := updateAssetName(testNewer, testPlatform)
	words := "\n\n**Security notice:** the signed bundle is at www.evil.example\n\n"

	cases := map[string]struct {
		bundle func(t *testing.T) bundleSpec
		serve  func(s *updateScene, bundle []byte)
		why    string
	}{
		"signed by other keys": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.keys = someoneElsesKeys(t)
			return spec
		}, why: "The bundle's signatures are missing or do not verify against Matty's two certificates."},
		"signed as another version": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.version, spec.signedAs = "4.11.0", "4.11.0"
			return spec
		}, why: "The bundle is signed as another version than " + testNewer + "."},
		"another plugin's id": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.id = "com.example.other"
			return spec
		}, why: "The bundle's plugin.json is not ceo.villa.matty " + testNewer + "."},
		"a file nobody signed": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.unlisted = map[string][]byte{"server/dist/evil-helper": []byte("not signed")}
			return spec
		}, why: "The bundle's files are not exactly the ones its release signed."},
		"a link": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.extra = []*tar.Header{{Name: "ceo.villa.matty/assets/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}
			return spec
		}, why: "The bundle holds something that is not a regular file or a folder."},
		"words in a name": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.extra = []*tar.Header{{Name: words + "/x", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644, Format: tar.FormatPAX}}
			return spec
		}, why: "The bundle holds a name that is not allowed."},
		"a name of a mebibyte": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			spec.extra = []*tar.Header{{Name: "ceo.villa.matty/" + strings.Repeat("evil/", 100000) + "x", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644, Format: tar.FormatPAX}}
			return spec
		}, why: "The bundle holds a name that is not allowed."},
		"more than 4,000 entries": {bundle: func(t *testing.T) bundleSpec {
			spec := newerBundle(t)
			for i := range release.BundleLimits.Entries {
				spec.extra = append(spec.extra, &tar.Header{Name: fmt.Sprintf("ceo.villa.matty/many/%d/", i), Typeflag: tar.TypeDir, Mode: 0o755})
			}
			return spec
		}, why: "The bundle holds more than 4,000 files and folders."},
		"no bundle at its address": {bundle: newerBundle, serve: func(*updateScene, []byte) {}, why: "GitHub answered the bundle's download with status 404."},
		"a redirect to somewhere else": {bundle: newerBundle, serve: func(s *updateScene, bundle []byte) {
			elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bundle) }))
			s.t.Cleanup(elsewhere.Close)
			s.github.route(downloadPath(testNewer, asset), func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, elsewhere.URL+"/x", http.StatusFound)
			})
		}, why: "The bundle's address was redirected to a host that is not GitHub's, or more than five times."},
		"six redirects": {bundle: newerBundle, serve: func(s *updateScene, bundle []byte) {
			for i := range 6 {
				from := downloadPath(testNewer, asset)
				if i > 0 {
					from = fmt.Sprintf("/hop/%d", i)
				}
				to := fmt.Sprintf("/hop/%d", i+1)
				s.github.route(from, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) })
			}
			s.github.route("/hop/6", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bundle) })
		}, why: "The bundle's address was redirected to a host that is not GitHub's, or more than five times."},
		"larger than 64 MB": {bundle: newerBundle, serve: func(s *updateScene, _ []byte) {
			s.github.route(downloadPath(testNewer, asset), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(updateFetchMaxBytes+1))
				w.WriteHeader(http.StatusOK)
			})
		}, why: "The bundle is larger than 64 MB."},
		"not a bundle": {bundle: newerBundle, serve: func(s *updateScene, _ []byte) {
			s.github.serveBundle(testNewer, []byte("<html>not found</html>"))
		}, why: "The bundle is not a gzip file."},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newUpdateScene(t)
			s.github.releases("v"+testNewer, false, false, asset)
			bundle := makeBundle(t, c.bundle(t))
			if c.serve != nil {
				c.serve(s, bundle)
			} else {
				s.github.serveBundle(testNewer, bundle)
			}

			p := s.node(true)
			look(p)
			recorder := s.bundle(p, "uid-alice")
			require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
			require.Equal(t, codeUnreachable, codedErrorOrEmpty(recorder))
			var body errorResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.Equal(t, c.why, body.Error)
			require.Equal(t, c.why, s.card(p).Trouble)
			require.Empty(t, leftovers(t, p))
			for _, line := range append(s.warnings(p), body.Error, s.card(p).Trouble) {
				require.NotContains(t, line, "evil")
			}
		})
	}
}

// What the plugin hands out is the repacked folder, and the repack is what
// the check saw: a file changed or added after the check stops it.
func TestTheRepackIsWhatTheCheckSaw(t *testing.T) {
	dest := t.TempDir()
	top, err := release.Unpack(context.Background(), bytes.NewReader(makeBundle(t, newerBundle(t))), dest, release.BundleLimits)
	require.NoError(t, err)
	dir := filepath.Join(dest, top)
	found := release.CheckDir(dir, theUpdateKeys(t).pins, release.Expect{Version: testNewer, Platform: testPlatform, Exact: true})
	require.Equal(t, release.Signed, found.State, found.Reason)
	manifest, err := release.ParseManifest(found.Manifest)
	require.NoError(t, err)
	programs, why := checkedPluginJSON(dir, manifest, testNewer)
	require.Empty(t, why)
	require.Equal(t, map[string]bool{testProgram: true}, programs)

	archive, err := repackChecked(context.Background(), dir, top, manifest, found, programs)
	require.NoError(t, err)
	checkArchive(t, archive)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "webapp/dist/main.js"), []byte("the panel, swapped"), 0o600))
	_, err = repackChecked(context.Background(), dir, top, manifest, found, programs)
	require.ErrorContains(t, err, "a file changed after it was checked")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "webapp/dist/main.js"), bundleFiles(newerBundle(t))["webapp/dist/main.js"], 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "added.txt"), []byte("late"), 0o600))
	_, err = repackChecked(context.Background(), dir, top, manifest, found, programs)
	require.ErrorContains(t, err, "a file is not in the release")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, os.Remove(filepath.Join(dir, "added.txt")))
	_, err = repackChecked(ctx, dir, top, manifest, found, programs)
	require.ErrorIs(t, err, context.Canceled)
}

// Mattermost's configuration is read at the start and at every change of
// it, and never for a request: whether it takes plugin uploads is
// remembered.
func TestUploadsAreRememberedNotAskedForEachRequest(t *testing.T) {
	s := newUpdateScene(t)
	p := s.node(true)
	require.Equal(t, 1, s.reads(), "read at the start")
	s.ready(p)

	s.card(p)
	require.Equal(t, http.StatusOK, s.bundle(p, "uid-alice").Code)
	s.bundle(p, "uid-alice")
	p.accessUpdateWordNow(p.getConfiguration())
	require.Equal(t, 1, s.reads())

	s.set(func() { s.uploads = false })
	require.True(t, s.card(p).Install.Ready, "the change is not known until Mattermost says its configuration changed")
	p.updatesChanged(p.getConfiguration(), p.getConfiguration())
	require.Equal(t, 2, s.reads())
	require.Equal(t, []string{updateMissingUploads}, s.card(p).Install.Missing)
}

// Private folders an earlier process left are removed at the start, once
// they are older than ten minutes, and once more eleven minutes after the
// start, when one that was younger is old enough. The stop ends the wait for
// the second sweep.
func TestLeftoverFoldersAreRemovedAtTheStart(t *testing.T) {
	s := newUpdateScene(t)
	dir := t.TempDir()
	old, fresh, other := filepath.Join(dir, "matty-update-1"), filepath.Join(dir, "matty-update-2"), filepath.Join(dir, "something-else")
	for _, folder := range []string{old, fresh, other} {
		require.NoError(t, os.MkdirAll(filepath.Join(folder, "unpacked", "deep"), 0o700))
	}
	require.NoError(t, os.Chtimes(old, s.clock().Add(-11*time.Minute), s.clock().Add(-11*time.Minute)))
	require.NoError(t, os.Chtimes(fresh, s.clock().Add(-time.Minute), s.clock().Add(-time.Minute)))
	require.NoError(t, os.Chtimes(other, s.clock().Add(-time.Hour), s.clock().Add(-time.Hour)))

	again := make(chan time.Time)
	p := s.nodeWith(false, func(r *updateRuntime) {
		r.tempDir = dir
		r.sweepTimer = func() <-chan time.Time { return again }
	})
	gone := func(folder string) func() bool {
		return func() bool {
			_, err := os.Stat(folder)
			return errors.Is(err, os.ErrNotExist)
		}
	}
	require.Eventually(t, gone(old), 5*time.Second, 5*time.Millisecond)
	require.DirExists(t, fresh)
	require.DirExists(t, other)

	s.advance(11 * time.Minute)
	again <- s.clock()
	require.Eventually(t, gone(fresh), 5*time.Second, 5*time.Millisecond)
	require.DirExists(t, other)

	// The default is eleven minutes, and a stop does not wait for it.
	require.Equal(t, 11*time.Minute, updateSweepAgain)
	q := s.nodeWith(false, func(r *updateRuntime) { r.tempDir = dir; r.sweepTimer = newUpdateRuntime().sweepTimer })
	began := time.Now()
	q.stopUpdates()
	require.Less(t, time.Since(began), time.Second)
	require.Empty(t, s.warnings(q))
	p.stopUpdates()
}

// With Look for updates off nothing of the feature reads the store at the
// start: only the sweep runs. Switched on, the last look as kept is read at
// once; a node that starts with it on reads it at its start.
func TestUpdatesOffReadNothingAtTheStart(t *testing.T) {
	s := newUpdateScene(t)
	reads := 0
	s.faults.lock.Lock()
	s.faults.onRead = func(key string) {
		if strings.HasPrefix(key, "update_") {
			reads++
		}
	}
	s.faults.lock.Unlock()
	readsNow := func() int {
		s.faults.lock.Lock()
		defer s.faults.lock.Unlock()
		return reads
	}
	raw, _ := json.Marshal(updateSeen{Latest: testNewer, Asset: true, Platform: testPlatform, LookedAt: 7})
	s.setKV(kvUpdateSeen, raw)

	dir := t.TempDir()
	leftover := filepath.Join(dir, "matty-update-1")
	require.NoError(t, os.Mkdir(leftover, 0o700))
	require.NoError(t, os.Chtimes(leftover, s.clock().Add(-time.Hour), s.clock().Add(-time.Hour)))
	p := s.nodeWith(false, func(r *updateRuntime) { r.tempDir = dir })
	require.Eventually(t, func() bool {
		_, err := os.Stat(leftover)
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Zero(t, readsNow())
	require.Equal(t, updateSeen{}, p.updates().seenNow())

	// Another node looked a moment ago, so this one does not look: what it
	// knows is what it read.
	s.setKV(kvUpdateLookClaim, []byte(fmt.Sprint(s.clock().UnixMilli())))
	off, on := &configuration{}, &configuration{EnableUpdates: true}
	p.setConfiguration(on)
	p.updatesChanged(off, on)
	require.Eventually(t, func() bool { return p.updates().seenNow().Latest == testNewer }, 5*time.Second, 5*time.Millisecond)
	require.Empty(t, s.github.asked())

	q := s.node(true)
	require.Eventually(t, func() bool { return q.updates().seenNow().Latest == testNewer }, 5*time.Second, 5*time.Millisecond)
}

// The daily job runs at once when it never ran, which is when the setting
// goes on, and so does the look the switch makes: the two take one claim,
// and GitHub is asked once.
func TestTheJobsFirstRunAndTurningOnAreOneLook(t *testing.T) {
	s := newUpdateScene(t)
	s.github.releases("v"+testRunning, false, false)
	p := s.nodeWith(false, func(r *updateRuntime) {
		r.schedule = func(_ cluster.JobPluginAPI, run func()) (clipsJob, error) {
			s.set(func() { s.scheduled++ })
			go run()
			return testJob{s}, nil
		}
	})

	off, on := &configuration{}, &configuration{EnableUpdates: true}
	p.setConfiguration(on)
	p.updatesChanged(off, on)
	require.Eventually(t, func() bool { return len(s.github.asked()) == 1 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Len(t, s.github.asked(), 1)
	require.Equal(t, 1, s.scheduled)

	// A day on, the job's run looks again.
	s.advance(24 * time.Hour)
	p.setConfiguration(off)
	p.updatesChanged(on, off)
	p.setConfiguration(on)
	p.updatesChanged(off, on)
	require.Eventually(t, func() bool { return len(s.github.asked()) == 2 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Len(t, s.github.asked(), 2)
}

// No code path of the plugin hands a plugin to Mattermost through the
// plugin API: installing is a system admin's own upload.
func TestNoCodePathInstallsAPlugin(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	more, err := filepath.Glob("*/*.go")
	require.NoError(t, err)
	checked := 0
	for _, file := range append(files, more...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NotContains(t, string(source), "InstallPlugin", file)
		checked++
	}
	require.Greater(t, checked, 50)
	require.Contains(t, files, "update.go")
}

// ---------------------------------------------------------------- the card

// The card as the contract holds it: on, a newer release that can be had
// from here, two phones in voice, and a trouble from the last bundle.
func TestContractAdminUpdate(t *testing.T) {
	s := newUpdateScene(t)
	s.voice = 2
	p := s.nodeWith(true, func(r *updateRuntime) { r.siteBase = "https://github.com" })
	s.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	look(p)
	p.noteUpdateTrouble("GitHub could not be reached for the bundle.")

	recorder := httptest.NewRecorder()
	p.handleAdminUpdate(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update", nil), "uid-bob")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	contractFixture(t, "admin-update.json", recorder.Body.Bytes())

	wrong := httptest.NewRecorder()
	p.handleAdminUpdate(wrong, httptest.NewRequest(http.MethodPost, "/api/v1/admin/update", nil), "uid-bob")
	require.Equal(t, http.StatusMethodNotAllowed, wrong.Code)

	// inVoice is left out when nobody is.
	s.set(func() { s.voice = 0 })
	recorder = httptest.NewRecorder()
	p.handleAdminUpdate(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update", nil), "uid-alice")
	require.NotContains(t, recorder.Body.String(), `"inVoice"`)
}

// ---------------------------------------------------------------- the real one

// The release named by MATTY_LIVE_UPDATE, from GitHub itself, through the
// real code, checked against the certificates built in, with the modes the
// archive gives. Only the maintainer runs it, after publishing a release;
// every other run skips it, and no test but this one ever asks anything
// outside this machine.
func TestLiveUpdateRelease(t *testing.T) {
	version := os.Getenv("MATTY_LIVE_UPDATE")
	if version == "" {
		t.Skip("set MATTY_LIVE_UPDATE=<version> to download and check that published release")
	}
	require.Regexp(t, updateVersionRE, version)

	api := &plugintest.API{}
	for _, level := range []string{"LogWarn", "LogInfo"} {
		for pairs := 0; pairs <= 4; pairs++ {
			args := make([]any, 1+pairs*2)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(level, args...).Return().Maybe()
		}
	}
	p := &Plugin{}
	p.SetAPI(api)
	r := p.updates()
	r.platform = "linux-amd64"

	archive, why := p.preparedUpdate(context.Background(), version)
	require.Empty(t, why)
	dest := t.TempDir()
	top, err := release.Unpack(context.Background(), bytes.NewReader(archive), dest, release.BundleLimits)
	require.NoError(t, err)
	pins, err := release.OfficialPins()
	require.NoError(t, err)
	found := release.CheckDir(filepath.Join(dest, top), pins, release.Expect{Version: version, Platform: r.platform, Exact: true})
	require.Equal(t, release.Signed, found.State, found.Reason)
	programs := 0
	for _, entry := range packedEntries(t, archive) {
		if entry.mode == 0o755 && !entry.dir {
			programs++
			require.Equal(t, "ceo.villa.matty/server/dist/plugin-linux-amd64", entry.name)
		}
	}
	require.Equal(t, 1, programs)
	t.Logf("%s %s: signed, manifest SHA-256 %s", updateProduct, version, release.Colons(found.ManifestSHA256))
}
