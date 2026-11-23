package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// Updates for the plugin itself, the setting EnableUpdates (Look for
// updates): off by default, and then nothing here asks anything outside, no
// job runs, and /me and /access say nothing about updates.
//
// Looking is one GET of GitHub's newest release of Matty Plugin, once a day
// on one node (a cluster job that exists only while the setting is on), once
// when the setting goes on (on one node; the job's first run and this look
// share one claim, so it is one look), and when an admin presses Check now
// (at most once a minute for the whole server). Of the answer only two
// things are used: the tag, v and a version, and whether the bundle for this
// server's platform is among its files. Nothing else of it reaches a person
// or a log.
//
// When a newer version is known, system admins are shown it in the panel
// (/me updateAlert) and in Matty (/access update), the card in the System
// Console says how it stands, and the bot sends each of them one direct
// message per version.
//
// The plugin installs nothing. A system admin installs a newer version with
// their own session: their client (the card, or Matty) asks POST
// /api/v1/admin/update/bundle, which downloads the bundle from GitHub to a
// new private folder, unpacks it strictly (release.Unpack), checks it as the
// plugin checks itself but with no file it does not list
// (release.CheckDir), and answers with a fresh archive packed from that
// checked folder, never the downloaded bytes. The client uploads that
// archive to Mattermost's own plugin upload. The plugin API offers no way
// for a plugin to replace itself: Mattermost stops the plugin inside such a
// call and waits for the call to end, which then never happens.

const (
	// updateRepo is where Matty Plugin is released.
	updateRepo = "vvilla-01/matty-plugin"

	// updateProduct is what the messages and the card call it, whatever
	// this server calls the plugin: the name its releases have.
	updateProduct = "Matty Plugin"

	// updateLookJob names the daily look's cluster job.
	updateLookJob   = "plugin_updates"
	updateLookEvery = 24 * time.Hour

	updateLookTimeout  = 15 * time.Second
	updateLookMaxBytes = 256 << 10

	updateFetchTimeout  = 2 * time.Minute
	updateFetchMaxBytes = release.BundleDownloadBytes
	updateMaxRedirects  = 5

	// updateClaimWindow is how often Check now may look, and a bundle be
	// prepared, for the whole server, and within what time the nodes told
	// of the same settings change are one change.
	updateClaimWindow = time.Minute

	// updateTickEvery is how often a node reads the last look another node
	// may have kept, while the setting is on.
	updateTickEvery = time.Minute

	// updateLeftoverAge is how old a private folder of an earlier bundle,
	// left by a process that ended in the middle, is before a sweep removes
	// it. A sweep runs at the start and once more updateSweepAgain later,
	// so a folder the process before this one left goes within about ten
	// minutes of the start.
	updateLeftoverAge = 10 * time.Minute
	updateSweepAgain  = 11 * time.Minute
	updateFolderGlob  = "matty-update-*"

	updateMaxAdmins   = 20
	updateAdminsPage  = 100
	updateAdminsPages = 50

	kvUpdateSeen       = "update_seen"
	kvUpdateToldPrefix = "update_told_"
	kvUpdateCheckClaim = "update_check_claim"
	// kvUpdateLookClaim is taken by the daily job's look and by the look
	// when the setting goes on, so that the two, which come at the same
	// moment when the job first runs, are one look.
	kvUpdateLookClaim   = "update_on_claim"
	kvUpdateBundleClaim = "update_bundle_claim"

	updateAdminOnlyText = "Only a system admin can see how updates stand."
	updateOffText       = "Updates are off. Switch on Look for updates above and save first."
	updateTooSoonText   = "Somebody looked less than a minute ago. Try again in a minute."
	updateNoneText      = "No newer version is known. Press Check now first."
	updateBundleSoon    = "A bundle was asked for less than a minute ago. Try again in a minute."
	updateBundleBusy    = "A bundle is being prepared just now. Try again in a minute."
	updateStoreText     = "This server could not read how updates stand just now. Try again in a moment."
	updateUploadsText   = "Mattermost does not take plugin uploads here: in System Console > Plugins > Plugin Management, Enable Plugins and Enable Plugin Uploads must be on and Require Plugin Signature off."
	updateStoppedText   = "The plugin stopped while it got the bundle. Try again once it is back."

	// codeStopping is the bundle's refusal when the plugin stopped while it
	// was being prepared: no archive and no other refusal.
	codeStopping = "stopping"
)

// Where a release build looks, and the rule for where a download may be
// redirected. Only the stand-in build (standin.go) changes them; nothing
// else can, by a setting or by the environment, and TestAReleaseAsksOnlyGitHub
// holds that.
var (
	updateAPIBase  = "https://api.github.com"
	updateSiteBase = "https://github.com"

	// updateRedirectOK is where a download may be sent on to: GitHub, or
	// where GitHub keeps release files, over HTTPS.
	updateRedirectOK = githubRedirectOK

	// updatePlainOK lets the bases above be plain http, which only the
	// stand-in's address on this machine is.
	updatePlainOK = false

	// updateNeedsSigned is whether only a signed release hands out a
	// bundle to install. The stand-in build is never one.
	updateNeedsSigned = true

	// updatePinsFrom, when set, is where the certificates a bundle is
	// checked against come from in place of the ones built in: the
	// stand-in's, for the stand-in build.
	updatePinsFrom func(ctx context.Context, client *http.Client) (release.Pins, error)

	// updateRunning is the version that is running, as releases are
	// numbered. The stand-in build can be told another when it is made.
	updateRunning = manifestVersion
)

// runningVersion is the version that is running, as /access, /me and the
// card say it: manifestVersion in every release build. Log lines and the
// release check say manifestVersion.
func runningVersion() string { return updateRunning }

var (
	updateTagRE     = regexp.MustCompile(`^v((?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8}))$`)
	updateVersionRE = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})$`)

	errUpdateRedirect = errors.New("redirected to a host that is not GitHub's")
)

// What keeps a newer version from being installed from here, in this
// order, as the card names them (webapp/src/updateAdmin.ts).
const (
	updateMissingSigned  = "unsigned"
	updateMissingAsset   = "asset"
	updateMissingUploads = "uploads"
)

// githubRedirectOK is the release build's rule: HTTPS, on the usual port, to
// github.com or a host under githubusercontent.com, with no user name.
func githubRedirectOK(address *url.URL) bool {
	if address == nil || address.Scheme != "https" || address.User != nil {
		return false
	}
	if port := address.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(address.Hostname())

	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// ---------------------------------------------------------------- versions

// plainVersion is a version as releases are numbered, without a "+…" or a
// "-…" ending, or "" when it is not one.
func plainVersion(version string) string {
	version, _, _ = strings.Cut(version, "+")
	version, _, _ = strings.Cut(version, "-")
	if !updateVersionRE.MatchString(version) {
		return ""
	}

	return version
}

// newerVersion is whether a is newer than b, part by part; false when either
// is not a version.
func newerVersion(a, b string) bool {
	a, b = plainVersion(a), plainVersion(b)
	if a == "" || b == "" {
		return false
	}

	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x > y
		}
	}

	return false
}

// pageUnder is a release's page, built here: never an address from an
// answer.
func pageUnder(base, version string) string {
	return base + "/" + updateRepo + "/releases/tag/v" + version
}

// page is a release's page where this node looks.
func (r *updateRuntime) page(version string) string {
	return pageUnder(r.siteBase, version)
}

// updateAssetName is the bundle for a platform, as releases name it.
func updateAssetName(version, platform string) string {
	return manifestID + "-" + version + "-" + platform + ".tar.gz"
}

// ---------------------------------------------------------------- what is kept

// updateSeen is the last look, shared by every node (kvUpdateSeen).
type updateSeen struct {
	// Latest is the newest release's version, "" when there is none.
	Latest string `json:"latest,omitempty"`

	// Asset is whether it has the bundle for Platform.
	Asset    bool   `json:"asset,omitempty"`
	Platform string `json:"platform,omitempty"`

	// When it looked (ms), and what went wrong, if anything, in plain words:
	// the look's, or the last bundle's that could not be prepared. It goes at
	// the next look that works or the next bundle that is handed out.
	LookedAt  int64  `json:"lookedAt,omitempty"`
	Trouble   string `json:"trouble,omitempty"`
	TroubleAt int64  `json:"troubleAt,omitempty"`
}

// updateWord is what /me (updateAlert) tells a system admin.
type updateWord struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// accessUpdateWord is what /access (update) tells a system admin: the same,
// and whether a bundle to install can be had from here.
type accessUpdateWord struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Install bool   `json:"install"`
}

// ---------------------------------------------------------------- the runtime

type updateRuntime struct {
	mu sync.Mutex

	// What tests replace.
	now          func() time.Time
	apiBase      string
	siteBase     string
	plainOK      bool
	redirectOK   func(*url.URL) bool
	platform     string
	running      string
	needsSigned  bool
	lookTimeout  time.Duration
	fetchTimeout time.Duration
	tickEvery    time.Duration
	tempDir      string
	transport    http.RoundTripper
	pins         func(ctx context.Context, client *http.Client) (release.Pins, error)
	schedule     func(api cluster.JobPluginAPI, run func()) (clipsJob, error)
	voiceCount   func() int
	sweepTimer   func() <-chan time.Time

	// The life of this node's part.
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// While the setting is on: the daily job and the minute's refresh,
	// which onCancel ends. onLock keeps one change of them at a time.
	onLock   sync.Mutex
	on       bool
	onCancel context.CancelFunc
	job      clipsJob

	// uploads is whether Mattermost takes plugin uploads, as its
	// configuration said at the start or at its last change.
	uploads bool

	// preparing is a bundle being prepared on this node.
	preparing bool

	// The last look as this node knows it.
	seen updateSeen

	// One look at a time on this node.
	lookLock sync.Mutex
}

func newUpdateRuntime() *updateRuntime {
	return &updateRuntime{
		now:          time.Now,
		apiBase:      updateAPIBase,
		siteBase:     updateSiteBase,
		plainOK:      updatePlainOK,
		redirectOK:   updateRedirectOK,
		platform:     thisPlatform(),
		running:      runningVersion(),
		needsSigned:  updateNeedsSigned,
		lookTimeout:  updateLookTimeout,
		fetchTimeout: updateFetchTimeout,
		tickEvery:    updateTickEvery,
		tempDir:      os.TempDir(),
		transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
		pins: updatePinsFrom,
		schedule: func(api cluster.JobPluginAPI, run func()) (clipsJob, error) {
			job, err := cluster.Schedule(api, updateLookJob, cluster.MakeWaitForInterval(updateLookEvery), run)
			if err != nil {
				return nil, err
			}

			return job, nil
		},
		voiceCount: func() int { return 0 },
		sweepTimer: func() <-chan time.Time { return time.After(updateSweepAgain) },
	}
}

func (p *Plugin) updates() *updateRuntime {
	p.updateOnce.Do(func() {
		r := newUpdateRuntime()
		r.voiceCount = func() int { return p.voiceTalk().count() }
		p.updateState = r
	})

	return p.updateState
}

// lookClient follows no redirect; fetchClient follows at most five, each to
// where redirectOK allows. Neither has a cookie jar.
func (r *updateRuntime) lookClient() *http.Client {
	return &http.Client{
		Timeout:   r.lookTimeout,
		Transport: r.transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (r *updateRuntime) fetchClient() *http.Client {
	return &http.Client{
		Timeout:   r.fetchTimeout,
		Transport: r.transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > updateMaxRedirects {
				return errUpdateRedirect
			}
			if !r.redirectOK(next.URL) {
				return errUpdateRedirect
			}

			return nil
		},
	}
}

// get is one request to an address built here, which must be HTTPS (or the
// stand-in's plain address).
func (r *updateRuntime) get(ctx context.Context, client *http.Client, address, accept string) (*http.Response, error) {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "https" && !(r.plainOK && parsed.Scheme == "http")) {
		return nil, errors.New("not an HTTPS address")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", mediaUserAgent)
	request.Header.Set("Accept", accept)

	response, err := client.Do(request)
	if err != nil {
		var addressed *url.Error
		if errors.As(err, &addressed) {
			err = addressed.Err
		}

		return nil, err
	}

	return response, nil
}

// requestFailureKind is what kind of failure a request met, for the log:
// never the error's own words, which can carry what the other end sent (a
// certificate's names, say).
func requestFailureKind(err error) string {
	var (
		dns        *net.DNSError
		verify     *tls.CertificateVerificationError
		hostname   x509.HostnameError
		authority  x509.UnknownAuthorityError
		invalid    x509.CertificateInvalidError
		record     tls.RecordHeaderError
		alert      tls.AlertError
		connection *net.OpError
	)

	switch {
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return "timeout"
	case errors.As(err, &dns):
		return "name lookup"
	case errors.As(err, &verify) || errors.As(err, &hostname) || errors.As(err, &authority) || errors.As(err, &invalid) || errors.As(err, &record) || errors.As(err, &alert):
		return "TLS"
	case errors.As(err, &connection):
		return "connection"
	default:
		return "other"
	}
}

func (r *updateRuntime) seenNow() updateSeen {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.seen
}

func (r *updateRuntime) setSeen(seen updateSeen) {
	r.mu.Lock()
	r.seen = seen
	r.mu.Unlock()
}

func (r *updateRuntime) uploadsNow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.uploads
}

// spawn runs f on a goroutine stopUpdates waits for, unless updates have
// stopped on this node.
func (r *updateRuntime) spawn(f func(ctx context.Context)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return false
	}

	ctx := r.ctx
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		f(ctx)
	}()

	return true
}

// ---------------------------------------------------------------- the life

// startUpdates is the usual start's: whether Mattermost takes plugin
// uploads, any private folder an earlier process left (swept now and once
// more eleven minutes later, which the stop ends), and, while the setting is
// on, the last look as kept, the daily look and the minute's refresh. With
// the setting off nothing is asked of anything outside and the store is not
// read.
func (p *Plugin) startUpdates() {
	p.stopUpdates()

	r := p.updates()
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.started, r.ctx, r.cancel = true, ctx, cancel
	r.preparing = false
	r.mu.Unlock()

	p.rememberUploads()
	again := r.sweepTimer
	r.spawn(func(ctx context.Context) {
		sweep := func() {
			p.safely("removing what an earlier bundle left", func() { sweepUpdateFolders(r.tempDir, r.now()) })
		}
		sweep()

		// A folder younger than ten minutes at the start can still be one
		// the process before this one left; eleven minutes on, it is old
		// enough, and a bundle being prepared then is not.
		select {
		case <-ctx.Done():
			return
		case <-again():
		}
		sweep()
	})

	p.setUpdatesOn(p.getConfiguration().EnableUpdates)
}

// stopUpdates is OnDeactivate's: it stops the job, the refresh and the
// second sweep, waits five seconds at most for a look under way, and cancels
// a bundle being prepared (which runs on its request, and is not waited
// for).
func (p *Plugin) stopUpdates() {
	r := p.updates()

	r.onLock.Lock()
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		r.onLock.Unlock()
		return
	}
	r.started = false
	r.cancel()
	job := r.job
	r.on, r.job, r.onCancel = false, nil, nil
	r.mu.Unlock()
	r.onLock.Unlock()

	if job != nil {
		_ = job.Close()
	}

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		p.API.LogWarn("Stopped waiting for a plugin update's work to stop")
	}
}

// setUpdatesOn starts the daily look's job and the minute's refresh when on,
// and stops them when off. Nothing of either exists while the setting is
// off.
func (p *Plugin) setUpdatesOn(on bool) {
	r := p.updates()
	r.onLock.Lock()
	defer r.onLock.Unlock()

	r.mu.Lock()
	if !r.started || r.on == on {
		r.mu.Unlock()
		return
	}
	r.on = on
	if !on {
		job, stop := r.job, r.onCancel
		r.job, r.onCancel = nil, nil
		r.mu.Unlock()

		if stop != nil {
			stop()
		}
		if job != nil {
			_ = job.Close()
		}
		return
	}
	onCtx, onCancel := context.WithCancel(r.ctx)
	r.onCancel = onCancel
	r.mu.Unlock()

	r.spawn(func(context.Context) { p.updateTicks(onCtx) })

	// The cluster job holds its lock while its callback runs, so the look
	// runs on a goroutine of its own, which turning the setting off or a
	// stop ends, and the callback returns at once. A job that never ran
	// runs at once, as the setting goes on: it and updatesChanged's look
	// take one claim, so only one of them looks.
	job, err := r.schedule(p.API, func() {
		r.spawn(func(context.Context) {
			p.safely("the daily look for a plugin update", func() { p.lookClaimed(onCtx) })
		})
	})
	if err != nil {
		p.API.LogWarn("Could not schedule the daily look for a plugin update", "error", err.Error())
	}
	r.mu.Lock()
	if r.on && r.onCancel != nil {
		r.job = job
		job = nil
	}
	r.mu.Unlock()
	if job != nil {
		_ = job.Close()
	}
}

// updatesOnNow is whether the daily job and the refresh run on this node.
func (r *updateRuntime) updatesOnNow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.on
}

// updateTicks is the minute's work on every node while the setting is on:
// the last look as another node may have kept it, read at once when the
// setting goes on (or the plugin starts with it on) and then every minute.
func (p *Plugin) updateTicks(ctx context.Context) {
	r := p.updates()
	p.safely("reading how plugin updates stand", func() {
		r.lookLock.Lock()
		defer r.lookLock.Unlock()
		p.refreshUpdateSeen()
	})
	for {
		timer := time.NewTimer(r.tickEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		p.safely("the minute's look at plugin updates", p.refreshUpdateSeen)
	}
}

// updatesChanged is OnConfigurationChange's, once the plugin is up, for any
// change of Mattermost's configuration or the plugin's: whether Mattermost
// takes plugin uploads is read again, the job and the refresh follow the
// setting, and when the setting goes on, one node looks at once (the same
// look as the job's first run, by their shared claim).
func (p *Plugin) updatesChanged(previous, cfg *configuration) {
	p.rememberUploads()
	if previous == nil {
		return
	}
	p.setUpdatesOn(cfg.EnableUpdates)
	if previous.EnableUpdates || !cfg.EnableUpdates {
		return
	}

	p.updates().spawn(func(ctx context.Context) {
		p.safely("looking for a plugin update", func() { p.lookClaimed(ctx) })
	})
}

// lookClaimed is the daily look and the look when the setting goes on: a
// look, by the node that claims it, at most once a minute for the whole
// server. Only a claim another node holds skips it: when the store does not
// answer, the node says so in the log and looks anyway.
func (p *Plugin) lookClaimed(ctx context.Context) {
	claimed, _, err := p.claimUpdateWindow(kvUpdateLookClaim)
	if err != nil {
		p.API.LogWarn("Could not claim the look for a plugin update", "kind", "store")
		claimed = true
	}
	if claimed {
		p.lookForUpdate(ctx)
	}
}

// rememberUploads reads whether Mattermost takes plugin uploads, so that no
// request asks Mattermost for its whole configuration.
func (p *Plugin) rememberUploads() {
	allowed := uploadsAllowed(p.API.GetConfig())

	r := p.updates()
	r.mu.Lock()
	r.uploads = allowed
	r.mu.Unlock()
}

// uploadsAllowed is whether Mattermost's own plugin upload takes a bundle
// that Mattermost did not sign: plugins and their uploads on, and no
// signature required.
func uploadsAllowed(config *model.Config) bool {
	if config == nil {
		return false
	}
	settings := config.PluginSettings
	on := func(value *bool) bool { return value != nil && *value }

	return on(settings.Enable) && on(settings.EnableUploads) && !on(settings.RequirePluginSignature)
}

// sweepUpdateFolders removes the private folders of bundles that a process
// which ended in the middle left in dir, once they are older than ten
// minutes: a bundle being prepared now is never that old (its download
// gives up after two).
func sweepUpdateFolders(dir string, now time.Time) {
	folders, err := filepath.Glob(filepath.Join(dir, updateFolderGlob))
	if err != nil {
		return
	}
	for _, folder := range folders {
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() || now.Sub(info.ModTime()) < updateLeftoverAge {
			continue
		}
		_ = os.RemoveAll(folder)
	}
}

// ---------------------------------------------------------------- the store

func (p *Plugin) readUpdateDoc(key string, into any) ([]byte, bool) {
	raw, appErr := p.API.KVGet(key)
	if appErr != nil || len(raw) == 0 {
		return nil, false
	}
	if json.Unmarshal(raw, into) != nil {
		return raw, false
	}

	return raw, true
}

func (p *Plugin) writeUpdateDoc(key string, doc any) bool {
	raw, err := json.Marshal(doc)
	if err != nil {
		return false
	}
	if appErr := p.API.KVSet(key, raw); appErr != nil {
		p.API.LogWarn("Could not keep how plugin updates stand", "key", key, "error", appErr.Error())
		return false
	}

	return true
}

// refreshUpdateSeen takes the last look as the store has it, which another
// node may have made.
func (p *Plugin) refreshUpdateSeen() {
	var seen updateSeen
	if _, ok := p.readUpdateDoc(kvUpdateSeen, &seen); ok {
		p.updates().setSeen(seen)
	}
}

// claimUpdateWindow is whether this node is the one to act now: false when
// any node claimed key within the last minute, with how long is left of
// that minute (a whole minute when another node claimed it just now), and
// an error when the store did not answer.
func (p *Plugin) claimUpdateWindow(key string) (bool, time.Duration, error) {
	now := p.updates().now()

	old, appErr := p.API.KVGet(key)
	if appErr != nil {
		return false, 0, appErr
	}
	if len(old) > 0 {
		if at, err := strconv.ParseInt(string(old), 10, 64); err == nil && now.Sub(time.UnixMilli(at)) < updateClaimWindow && now.UnixMilli() >= at {
			return false, updateClaimWindow - now.Sub(time.UnixMilli(at)), nil
		}
	} else {
		old = nil
	}

	claimed, appErr := p.API.KVCompareAndSet(key, old, []byte(strconv.FormatInt(now.UnixMilli(), 10)))
	if appErr != nil {
		return false, 0, appErr
	}

	return claimed, updateClaimWindow, nil
}

// noteUpdateTrouble puts why a bundle could not be prepared on the card, as
// the last trouble, for every node.
func (p *Plugin) noteUpdateTrouble(words string) {
	r := p.updates()
	r.lookLock.Lock()
	defer r.lookLock.Unlock()

	p.refreshUpdateSeen()
	seen := r.seenNow()
	seen.Trouble, seen.TroubleAt = words, r.now().UnixMilli()
	p.writeUpdateDoc(kvUpdateSeen, seen)
	r.setSeen(seen)
}

// clearUpdateTrouble takes the last trouble off the card, for every node,
// once a bundle was handed out whole. What the last look found stays.
func (p *Plugin) clearUpdateTrouble() {
	r := p.updates()
	r.lookLock.Lock()
	defer r.lookLock.Unlock()

	p.refreshUpdateSeen()
	seen := r.seenNow()
	if seen.Trouble == "" && seen.TroubleAt == 0 {
		return
	}
	seen.Trouble, seen.TroubleAt = "", 0
	p.writeUpdateDoc(kvUpdateSeen, seen)
	r.setSeen(seen)
}

// ---------------------------------------------------------------- looking

// lookForUpdate asks GitHub for the newest release, keeps what it found for
// every node, and tells the admins of a newer one.
func (p *Plugin) lookForUpdate(ctx context.Context) {
	if !p.getConfiguration().EnableUpdates {
		return
	}

	r := p.updates()
	r.lookLock.Lock()
	seen, done := p.lookOnce(ctx)
	if done {
		p.writeUpdateDoc(kvUpdateSeen, seen)
		r.setSeen(seen)
	}
	r.lookLock.Unlock()

	if done {
		p.afterUpdateLook(seen)
	}
}

// lookOnce is one look: the newest release's version and whether it has
// this platform's bundle, or what went wrong. A 404 is no release yet, which
// is no trouble. A look that was stopped (not timed out) is no look at all,
// and done is false.
func (p *Plugin) lookOnce(parent context.Context) (seen updateSeen, done bool) {
	r := p.updates()
	now := r.now()
	previous := r.seenNow()
	seen = updateSeen{Platform: r.platform, LookedAt: now.UnixMilli()}
	trouble := func(words string) (updateSeen, bool) {
		// What was last known stays known: a look that failed says
		// nothing about the release.
		previous.LookedAt, previous.Trouble, previous.TroubleAt = now.UnixMilli(), words, now.UnixMilli()
		if previous.Platform == "" {
			previous.Platform = r.platform
		}
		return previous, true
	}

	ctx, cancel := context.WithTimeout(parent, r.lookTimeout)
	defer cancel()
	stopped := func() bool { return errors.Is(ctx.Err(), context.Canceled) }

	response, err := r.get(ctx, r.lookClient(), r.apiBase+"/repos/"+updateRepo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		if stopped() {
			return updateSeen{}, false
		}
		kind := requestFailureKind(err)
		p.API.LogWarn("Could not reach GitHub to look for a plugin update", "kind", kind)
		if kind == "timeout" {
			return trouble("GitHub did not answer within 15 seconds.")
		}
		return trouble("GitHub could not be reached.")
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusNotFound:
		return seen, true
	case response.StatusCode != http.StatusOK:
		return trouble(fmt.Sprintf("GitHub answered with status %d.", response.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, updateLookMaxBytes+1))
	if err != nil {
		if stopped() {
			return updateSeen{}, false
		}
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			p.API.LogWarn("Could not reach GitHub to look for a plugin update", "kind", "timeout")
			return trouble("GitHub did not answer within 15 seconds.")
		}
		return trouble("GitHub's answer could not be read.")
	}
	if len(body) > updateLookMaxBytes {
		return trouble("GitHub's answer was larger than 256 KB.")
	}

	var answer struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return trouble("GitHub's answer could not be read.")
	}
	if answer.Draft || answer.Prerelease {
		return seen, true
	}
	match := updateTagRE.FindStringSubmatch(answer.TagName)
	if match == nil {
		return trouble("The newest release's tag is not a version such as v4.5.0.")
	}

	seen.Latest = match[1]
	asset := updateAssetName(seen.Latest, r.platform)
	for _, file := range answer.Assets {
		if file.Name == asset {
			seen.Asset = true
		}
	}

	return seen, true
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// newerSeen is the newer version the last look found, or "".
func (r *updateRuntime) newerSeen() string {
	seen := r.seenNow()
	if newerVersion(seen.Latest, r.running) {
		return seen.Latest
	}

	return ""
}

// updateWordNow is the newer version for /me, from memory, or nil: none
// known, or the setting off.
func (p *Plugin) updateWordNow(cfg *configuration) *updateWord {
	if !cfg.EnableUpdates {
		return nil
	}
	version := p.updates().newerSeen()
	if version == "" {
		return nil
	}

	return &updateWord{Version: version, URL: p.updates().page(version)}
}

// accessUpdateWordNow is /access's, from memory: the same, and whether this
// server hands out a bundle of it to install.
func (p *Plugin) accessUpdateWordNow(cfg *configuration) *accessUpdateWord {
	word := p.updateWordNow(cfg)
	if word == nil {
		return nil
	}

	return &accessUpdateWord{Version: word.Version, URL: word.URL, Install: len(p.updateMissing(word.Version)) == 0}
}

// updateMissing is what keeps version from being installed from here, in
// the card's order, or nothing. It reads only memory.
func (p *Plugin) updateMissing(version string) []string {
	r := p.updates()
	var missing []string

	if r.needsSigned && p.ownRelease().State != release.Signed {
		missing = append(missing, updateMissingSigned)
	}
	if seen := r.seenNow(); seen.Latest != version || !seen.Asset || seen.Platform != r.platform {
		missing = append(missing, updateMissingAsset)
	}
	if !r.uploadsNow() {
		missing = append(missing, updateMissingUploads)
	}

	return missing
}

// updateMissingText is the refusal for what is missing, as a person reads
// it.
func (p *Plugin) updateMissingText(missing, version string) string {
	switch missing {
	case updateMissingSigned:
		return "This copy of " + updateProduct + " is not a signed release, so it does not install releases. Install " + version + " by hand from its page."
	case updateMissingAsset:
		return updateProduct + " " + version + " has no bundle for this server's platform (" + p.updates().platform + ")."
	default:
		return updateUploadsText
	}
}

// ---------------------------------------------------------------- telling

// afterUpdateLook tells the admins of a newer version, once.
func (p *Plugin) afterUpdateLook(seen updateSeen) {
	r := p.updates()
	if !newerVersion(seen.Latest, r.running) || !p.getConfiguration().EnableUpdates {
		return
	}

	p.tellUpdateOnce(seen.Latest, updateOutMessage(seen.Latest, r.page(seen.Latest)))
}

func updateOutMessage(version, page string) string {
	return fmt.Sprintf("%s %s is out. To install it, a system admin opens System Console > Plugins > Matty > Updates. What is new: %s", updateProduct, version, page)
}

// tellUpdateOnce sends the admins' one message about version, once for the
// whole server: the node that claims it sends it. With no bot yet nothing is
// claimed, and when not one admin was reached the claim is given back, so a
// later look sends it.
func (p *Plugin) tellUpdateOnce(version, message string) {
	if p.getBotUserID() == "" {
		return
	}

	key := kvUpdateToldPrefix + version
	stamp := []byte(strconv.FormatInt(p.updates().now().UnixMilli(), 10))
	claimed, appErr := p.API.KVSetWithOptions(key, stamp, model.PluginKVSetOptions{Atomic: true, OldValue: nil})
	if appErr != nil || !claimed {
		return
	}

	if p.tellAdmins(message) == 0 {
		_, _ = p.API.KVCompareAndDelete(key, stamp)
	}
}

// tellAdmins sends message from the bot to each system admin, at most
// twenty, active people and never a bot, each in their direct messages with
// the bot, and is how many it reached.
func (p *Plugin) tellAdmins(message string) int {
	botID := p.getBotUserID()
	if botID == "" {
		p.API.LogWarn("Could not tell the system admins about a plugin update: there is no bot")
		return 0
	}

	reached := 0
	for _, admin := range p.updateAdmins() {
		channel, appErr := p.API.GetDirectChannel(botID, admin)
		if appErr != nil || channel == nil {
			p.API.LogWarn("Could not open a direct message to a system admin", "user_id", admin)
			continue
		}
		if _, appErr := p.API.CreatePost(&model.Post{UserId: botID, ChannelId: channel.Id, Message: message}); appErr != nil {
			p.API.LogWarn("Could not tell a system admin about a plugin update", "user_id", admin, "error", appErr.Error())
			continue
		}
		reached++
	}

	return reached
}

// updateAdmins are the first twenty system admins Mattermost lists: asked
// for by role, and each held to it again, so the answer is right whether or
// not Mattermost honours the role in the question.
func (p *Plugin) updateAdmins() []string {
	var admins []string
	seen := map[string]bool{}
	for page := 0; page < updateAdminsPages && len(admins) < updateMaxAdmins; page++ {
		users, appErr := p.API.GetUsers(&model.UserGetOptions{Role: model.SystemAdminRoleId, Active: true, Page: page, PerPage: updateAdminsPage})
		if appErr != nil {
			p.API.LogWarn("Could not list the system admins", "error", appErr.Error())
			break
		}
		for _, user := range users {
			if user == nil || user.IsBot || user.DeleteAt != 0 || !user.IsSystemAdmin() || seen[user.Id] {
				continue
			}
			seen[user.Id] = true
			admins = append(admins, user.Id)
			if len(admins) == updateMaxAdmins {
				break
			}
		}
		if len(users) < updateAdminsPage {
			break
		}
	}

	return admins
}

// ---------------------------------------------------------------- the bundle

// preparedUpdate is the archive of version for an admin's client to hand
// Mattermost, made from the folder the check passed, or why there is none,
// in one of a fixed set of sentences that hold no word of the download.
// The private folder is removed before it returns. When ctx ends first,
// both are empty.
func (p *Plugin) preparedUpdate(ctx context.Context, version string) ([]byte, string) {
	r := p.updates()

	folder, err := os.MkdirTemp(r.tempDir, updateFolderGlob)
	if err != nil {
		return nil, "No private folder could be made to unpack it in."
	}
	defer os.RemoveAll(folder)

	stopped := func(archive []byte, why string) ([]byte, string) {
		if ctx.Err() != nil {
			return nil, ""
		}
		return archive, why
	}

	asset := updateAssetName(version, r.platform)
	downloaded := filepath.Join(folder, "bundle.tar.gz")
	if why := r.download(ctx, r.siteBase+"/"+updateRepo+"/releases/download/v"+version+"/"+asset, downloaded); why != "" {
		return stopped(nil, why)
	}

	unpacked := filepath.Join(folder, "unpacked")
	if err := os.Mkdir(unpacked, 0o700); err != nil {
		return nil, "No private folder could be made to unpack it in."
	}
	bundle, err := os.Open(downloaded)
	if err != nil {
		return nil, "The downloaded bundle could not be read."
	}
	top, err := release.Unpack(ctx, bundle, unpacked, release.BundleLimits)
	_ = bundle.Close()
	if err != nil {
		var refusal release.Refusal
		if errors.As(err, &refusal) {
			return stopped(nil, refusalSentence(refusal))
		}
		return stopped(nil, "The bundle could not be unpacked.")
	}
	_ = os.Remove(downloaded)
	dir := filepath.Join(unpacked, top)

	pins, err := r.pinsNow(ctx, p)
	if err != nil {
		return stopped(nil, "The certificates to check it against could not be read.")
	}
	found := release.CheckDir(dir, pins, release.Expect{Version: version, Platform: r.platform, Exact: true})
	if found.State != release.Signed {
		return stopped(nil, bundleCheckWords(found, version, r.platform))
	}
	manifest, err := release.ParseManifest(found.Manifest)
	if err != nil {
		return nil, "The checked manifest does not read."
	}
	programs, why := checkedPluginJSON(dir, manifest, version)
	if why != "" {
		return stopped(nil, why)
	}

	archive, err := repackChecked(ctx, dir, top, manifest, found, programs)
	if err != nil {
		return stopped(nil, "The checked folder could not be packed again.")
	}

	return stopped(archive, "")
}

// refusalSentence is an unpack refusal as a sentence: "The bundle holds
// more than 4,000 files and folders."
func refusalSentence(refusal release.Refusal) string {
	words := string(refusal)
	if words == "" {
		return "The bundle could not be unpacked."
	}

	return strings.ToUpper(words[:1]) + words[1:] + "."
}

// bundleCheckWords is why a downloaded bundle is not a signed release of
// version for platform, in one of a fixed set of sentences: the check's own
// reasons name files of the download, which a person is never shown.
func bundleCheckWords(found release.Finding, version, platform string) string {
	switch {
	case found.State == release.Unsigned:
		return "The bundle has no release folder, so it is not a signed release of " + version + "."
	case found.Version == "":
		return "The bundle's signatures are missing or do not verify against Matty's two certificates."
	case found.Version != version:
		return "The bundle is signed as another version than " + version + "."
	case !slices.Contains(found.Platforms, platform):
		return "The bundle is signed for another platform than " + platform + "."
	default:
		return "The bundle's files are not exactly the ones its release signed."
	}
}

func (r *updateRuntime) pinsNow(ctx context.Context, p *Plugin) (release.Pins, error) {
	if r.pins != nil {
		return r.pins(ctx, r.lookClient())
	}

	return p.releasePinsNow()
}

// download writes the bundle at address to file: at most 64 MB, within two
// minutes, through at most five redirects that redirectOK allows.
func (r *updateRuntime) download(ctx context.Context, address, file string) string {
	ctx, cancel := context.WithTimeout(ctx, r.fetchTimeout)
	defer cancel()

	response, err := r.get(ctx, r.fetchClient(), address, "application/octet-stream")
	if err != nil {
		switch {
		case errors.Is(err, errUpdateRedirect):
			return "The bundle's address was redirected to a host that is not GitHub's, or more than five times."
		case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
			return "The bundle did not download within two minutes."
		default:
			return "GitHub could not be reached for the bundle."
		}
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Sprintf("GitHub answered the bundle's download with status %d.", response.StatusCode)
	}
	if response.ContentLength > updateFetchMaxBytes {
		return "The bundle is larger than 64 MB."
	}

	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "The bundle could not be written to the private folder."
	}
	n, err := io.Copy(out, io.LimitReader(response.Body, updateFetchMaxBytes+1))
	closeErr := out.Close()
	switch {
	case n > updateFetchMaxBytes:
		return "The bundle is larger than 64 MB."
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || isTimeout(err)):
		return "The bundle did not download within two minutes."
	case err != nil || closeErr != nil:
		return "The bundle could not be downloaded whole."
	}

	return ""
}

// checkedPluginJSON holds the checked folder's plugin.json, read again and
// held to its entry in the manifest, to this plugin and the version looked
// for, and is the programs it names (server.executables' values and
// server.executable, as paths in the folder): the only files packed as
// programs.
func checkedPluginJSON(dir string, manifest *release.Manifest, version string) (map[string]bool, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, "The bundle's plugin.json could not be read."
	}
	entry, listed := manifest.Lookup("plugin.json")
	sum := sha256.Sum256(raw)
	if !listed || int64(len(raw)) != entry.Size || hex.EncodeToString(sum[:]) != entry.SHA256 {
		return nil, "The bundle's plugin.json changed after it was checked."
	}

	var plugin struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Server  *struct {
			Executables map[string]string `json:"executables"`
			Executable  string            `json:"executable"`
		} `json:"server"`
	}
	if json.Unmarshal(raw, &plugin) != nil {
		return nil, "The bundle's plugin.json does not read."
	}
	if plugin.ID != manifestID || plugin.Version != version {
		return nil, fmt.Sprintf("The bundle's plugin.json is not %s %s.", manifestID, version)
	}

	programs := map[string]bool{}
	clean := func(p string) string { return strings.TrimPrefix(path.Clean(strings.TrimPrefix(p, "./")), "./") }
	if plugin.Server != nil {
		for _, program := range plugin.Server.Executables {
			if program != "" {
				programs[clean(program)] = true
			}
		}
		if plugin.Server.Executable != "" {
			programs[clean(plugin.Server.Executable)] = true
		}
	}

	return programs, ""
}

// repackChecked is a .tar.gz of exactly what the check passed: every file
// the manifest lists, each hashed again as it is packed and held to its
// entry, the manifest and its two signatures as the check read them, and
// the folders that hold them. Modes are the archive's own, never the
// download's: 0755 for the programs plugin.json names and for folders,
// 0644 for every other file. A file in the folder that the manifest does not
// list stops it, and so does ctx ending.
func repackChecked(ctx context.Context, dir, top string, manifest *release.Manifest, found release.Finding, programs map[string]bool) ([]byte, error) {
	var buf bytes.Buffer
	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)
	stamp := time.Unix(0, 0)

	own := map[string][]byte{
		release.ManifestPath:       found.Manifest,
		release.RSASignaturePath:   found.RSASignature,
		release.MLDSASignaturePath: found.MLDSASignature,
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// The folders above a file, each once, before it.
	packedDirs := map[string]bool{}
	folder := func(name string) error {
		if packedDirs[name] {
			return nil
		}
		packedDirs[name] = true
		header := top + "/"
		if name != "." {
			header += name + "/"
		}

		return archive.WriteHeader(&tar.Header{Name: header, Typeflag: tar.TypeDir, Mode: 0o755, ModTime: stamp})
	}
	foldersOf := func(name string) error {
		if err := folder("."); err != nil {
			return err
		}
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			if err := folder(strings.Join(parts[:i], "/")); err != nil {
				return err
			}
		}
		return nil
	}

	packed := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("not a regular file")
		}

		if content, isOwn := own[name]; isOwn {
			packed++
			if err := foldersOf(name); err != nil {
				return err
			}
			if err := archive.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content)), ModTime: stamp}); err != nil {
				return err
			}
			_, err := archive.Write(content)
			return err
		}

		listed, ok := manifest.Lookup(name)
		if !ok {
			return errors.New("a file is not in the release")
		}
		packed++

		mode := int64(0o644)
		if programs[name] {
			mode = 0o755
		}
		if err := foldersOf(name); err != nil {
			return err
		}
		if err := archive.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: mode, Size: listed.Size, ModTime: stamp}); err != nil {
			return err
		}

		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(file, listed.Size))
		if err != nil {
			return err
		}
		more, _ := file.Read(make([]byte, 1))
		if n != listed.Size || more > 0 || hex.EncodeToString(hash.Sum(nil)) != listed.SHA256 {
			return errors.New("a file changed after it was checked")
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	if packed != len(manifest.Files)+len(own) {
		return nil, errors.New("not every file the release lists was packed")
	}

	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- the card

// updateAdminView is GET /api/v1/admin/update, the System Console's
// Updates card: whether the setting is on, the running version, this
// server's platform (such as linux-amd64), the newest seen and its page,
// when the last look was, the last trouble in plain words, and, while a
// newer one is known, whether a bundle of it can be had from here (install)
// and how many phones are in voice through the plugin on this node
// (inVoice), which an install drops.
type updateAdminView struct {
	Enabled   bool               `json:"enabled"`
	Running   string             `json:"running"`
	Platform  string             `json:"platform"`
	Latest    string             `json:"latest,omitempty"`
	Newer     bool               `json:"newer"`
	URL       string             `json:"url,omitempty"`
	LookedAt  int64              `json:"lookedAt,omitempty"`
	Trouble   string             `json:"trouble,omitempty"`
	TroubleAt int64              `json:"troubleAt,omitempty"`
	Install   *updateInstallView `json:"install,omitempty"`
	InVoice   int                `json:"inVoice,omitempty"`
}

// updateInstallView is whether a bundle can be had from here, and when not,
// every reason that holds, in order.
type updateInstallView struct {
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing,omitempty"`
}

func (p *Plugin) updateAdminNow() updateAdminView {
	r := p.updates()
	cfg := p.getConfiguration()
	p.refreshUpdateSeen()
	seen := r.seenNow()

	view := updateAdminView{
		Enabled:   cfg.EnableUpdates,
		Running:   r.running,
		Platform:  r.platform,
		Latest:    seen.Latest,
		LookedAt:  seen.LookedAt,
		Trouble:   seen.Trouble,
		TroubleAt: seen.TroubleAt,
	}
	if seen.Latest == "" {
		return view
	}
	view.Newer = newerVersion(seen.Latest, r.running)
	view.URL = r.page(seen.Latest)
	if !view.Newer {
		return view
	}

	missing := p.updateMissing(seen.Latest)
	view.Install = &updateInstallView{Ready: len(missing) == 0, Missing: missing}
	view.InVoice = r.voiceCount()

	return view
}

// handleAdminUpdate answers GET /api/v1/admin/update, for system admins.
func (p *Plugin) handleAdminUpdate(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, updateAdminOnlyText)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	writeJSON(w, http.StatusOK, p.updateAdminNow())
}

// handleAdminUpdateCheck answers POST /api/v1/admin/update/check, the card's
// Check now: a look now, at most once a minute for the whole server, and
// never while the setting is off. The look runs on the plugin's own context
// with its own 15 seconds, so a browser that leaves does not cut it short.
// It answers with the card as it then stands; the message to the admins
// goes on after.
func (p *Plugin) handleAdminUpdateCheck(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, updateAdminOnlyText)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !p.getConfiguration().EnableUpdates {
		writeCodedError(w, http.StatusConflict, updateOffText, codeConflict)
		return
	}
	claimed, left, err := p.claimUpdateWindow(kvUpdateCheckClaim)
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, updateStoreText, accessCodeStore)
		return
	}
	if !claimed {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: updateTooSoonText, Code: codeRateLimited, RetryAfterMs: max(left.Milliseconds(), 1000)})
		return
	}

	updates := p.updates()
	updates.mu.Lock()
	ctx := updates.ctx
	updates.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}

	updates.lookLock.Lock()
	seen, done := p.lookOnce(ctx)
	if done {
		p.writeUpdateDoc(kvUpdateSeen, seen)
		updates.setSeen(seen)
	}
	updates.lookLock.Unlock()

	if done {
		updates.spawn(func(context.Context) {
			p.safely("telling of a plugin update", func() { p.afterUpdateLook(seen) })
		})
	}

	writeJSON(w, http.StatusOK, p.updateAdminNow())
}

// handleAdminUpdateBundle answers POST /api/v1/admin/update/bundle: the
// newer version the last look found, downloaded, unpacked, checked and
// packed again (preparedUpdate), for a system admin's own client to upload
// to Mattermost. Refused, in this order: while the setting is off, when no
// newer version is known, when it cannot be installed from here (the first
// reason that holds), within a minute of the last bundle anywhere on the
// server or while one is being prepared on this node, when the download or
// the check fails, whose words become the card's trouble, and when the
// plugin stopped in the middle (503 stopping: never an empty 200). A bundle
// handed out whole takes the trouble off the card.
//
// Under Mattermost a plugin's request has no context that ends, so a
// browser that leaves does not stop the work: it ends by itself (the
// download gives up after two minutes) and leaves no folder, and meanwhile
// another Install on this node is told one is being prepared. The plugin
// stopping ends it at once. Nothing is kept: the private folder is gone
// before the answer is written.
func (p *Plugin) handleAdminUpdateBundle(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, updateAdminOnlyText)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !p.getConfiguration().EnableUpdates {
		writeCodedError(w, http.StatusConflict, updateOffText, codeConflict)
		return
	}
	updates := p.updates()
	version := updates.newerSeen()
	if version == "" {
		writeCodedError(w, http.StatusConflict, updateNoneText, codeConflict)
		return
	}
	if missing := p.updateMissing(version); len(missing) > 0 {
		writeCodedError(w, http.StatusConflict, p.updateMissingText(missing[0], version), codeConflict)
		return
	}

	tooSoon := func(words string, left time.Duration) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: words, Code: codeRateLimited, RetryAfterMs: max(left.Milliseconds(), 1000)})
	}
	updates.mu.Lock()
	busy, stop := updates.preparing, updates.ctx
	updates.preparing = true
	updates.mu.Unlock()
	if busy {
		tooSoon(updateBundleBusy, updateClaimWindow)
		return
	}
	if stop == nil {
		stop = context.Background()
	}
	defer func() {
		updates.mu.Lock()
		updates.preparing = false
		updates.mu.Unlock()
	}()

	// The claim is kept whatever comes of it: a bundle that failed was
	// still asked of GitHub.
	claimed, left, err := p.claimUpdateWindow(kvUpdateBundleClaim)
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, updateStoreText, accessCodeStore)
		return
	}
	if !claimed {
		tooSoon(updateBundleSoon, left)
		return
	}

	// The plugin's stop ends the work, and so would the request's context
	// ending, which under Mattermost it never does.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(stop, cancel)()

	archive, why := p.preparedUpdate(ctx, version)
	switch {
	case ctx.Err() != nil:
		writeCodedError(w, http.StatusServiceUnavailable, updateStoppedText, codeStopping)
		return
	case why != "":
		p.API.LogWarn("Could not prepare a plugin update's bundle", "version", version, "why", why)
		p.noteUpdateTrouble(why)
		writeCodedError(w, http.StatusBadGateway, why, codeUnreachable)
		return
	case len(archive) == 0:
		writeCodedError(w, http.StatusServiceUnavailable, updateStoppedText, codeStopping)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+updateAssetName(version, updates.platform)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	w.Header().Set("X-Matty-Update-Version", version)
	w.WriteHeader(http.StatusOK)
	if n, err := w.Write(archive); err == nil && n == len(archive) {
		p.clearUpdateTrouble()
	}
}
