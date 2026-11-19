package main

import (
	"encoding/base64"
	"net/http"
	"runtime"

	"github.com/vvilla-01/matty-plugin/server/release"
)

// The plugin's word on its own files: whether the folder Mattermost runs it
// from holds a release signed with Matty's two keys (server/release), and
// whether every file that release lists is as signed. It is evidence, not a
// lock: the plugin runs in every state, and a binary somebody altered can
// simply skip the check. What it is for is the honest case, a server running
// what it was given, and the app, which checks the signatures itself
// (GET /api/v1/release) against the same two certificates.
//
// It is worked out once, at the usual start, before any route is served, and
// kept: nothing here is read again per request.

// releaseWord is what /access and /me say about it: "signed", "unsigned"
// (no release folder: every development build, and any build of somebody's
// own) or "altered", and the version this plugin is.
type releaseWord struct {
	State   string `json:"state"`
	Version string `json:"version"`
}

// releaseResponse is GET /api/v1/release: releaseWord, and, when there is a
// release folder, its three files as found, for a client to verify for
// itself. Reason says why, when altered.
type releaseResponse struct {
	State    string `json:"state"`
	Version  string `json:"version"`
	Reason   string `json:"reason,omitempty"`
	Manifest string `json:"manifest,omitempty"`
	RSA      string `json:"rsa,omitempty"`
	MLDSA87  string `json:"mldsa87,omitempty"`
}

// releaseAlert is what /me tells system admins: present when the files are
// not as signed, and when a signed release's folder holds files nobody
// signed that Mattermost does not read (which are no failure: a file it
// does read, a plugin.yml or plugin.yaml, one beside the web panel's
// bundle, or one in the public folder, makes the release altered).
type releaseAlert struct {
	State         string   `json:"state"`
	Problem       string   `json:"problem,omitempty"`
	File          string   `json:"file,omitempty"`
	Unlisted      int      `json:"unlisted,omitempty"`
	UnlistedFiles []string `json:"unlistedFiles,omitempty"`
}

// thisPlatform is the platform whose executable is running, as plugin.json
// names it.
func thisPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// checkOwnRelease is the usual start's: it reads the plugin's own folder,
// keeps what it found, and says so in the log once.
func (p *Plugin) checkOwnRelease() {
	finding := p.findOwnRelease()
	p.releaseFound.Store(&finding)

	switch finding.State {
	case release.Signed:
		p.API.LogInfo(productName+" is a signed release", "version", manifestVersion,
			"manifest_sha256", release.Colons(finding.ManifestSHA256), "unsigned_files", finding.UnlistedCount)
	case release.Unsigned:
		p.API.LogInfo(productName+" is not a signed release: there is no release folder, as in any development build", "version", manifestVersion)
	default:
		p.API.LogWarn(productName+"'s files do not match a signed release", "version", manifestVersion,
			"reason", finding.Reason, "file", finding.File)
	}
}

func (p *Plugin) findOwnRelease() release.Finding {
	pins, err := p.releasePinsNow()
	if err != nil {
		return release.Finding{State: release.Altered, Reason: "the certificates to check against do not read: " + err.Error()}
	}

	dir, err := p.API.GetBundlePath()
	if err != nil {
		return release.Finding{State: release.Altered, Reason: "the plugin's folder is not known: " + err.Error()}
	}

	return release.CheckDir(dir, pins, release.Expect{Version: manifestVersion, Platform: thisPlatform()})
}

// releasePinsNow are the certificates built in, unless a test gave others.
func (p *Plugin) releasePinsNow() (release.Pins, error) {
	if p.releasePins != nil {
		return p.releasePins()
	}

	return release.OfficialPins()
}

// ownRelease is what checkOwnRelease found. Before it has run, which only a
// test that never activates sees, it is unsigned.
func (p *Plugin) ownRelease() release.Finding {
	if found := p.releaseFound.Load(); found != nil {
		return *found
	}

	return release.Finding{State: release.Unsigned}
}

func (p *Plugin) releaseWordNow() releaseWord {
	return releaseWord{State: string(p.ownRelease().State), Version: manifestVersion}
}

// handleRelease answers GET /api/v1/release.
func (p *Plugin) handleRelease(w http.ResponseWriter) {
	found := p.ownRelease()

	response := releaseResponse{State: string(found.State), Version: manifestVersion}
	if found.State == release.Altered {
		response.Reason = found.Reason
	}
	response.Manifest = string(found.Manifest)
	if found.RSASignature != nil {
		response.RSA = base64.StdEncoding.EncodeToString(found.RSASignature)
	}
	if found.MLDSASignature != nil {
		response.MLDSA87 = base64.StdEncoding.EncodeToString(found.MLDSASignature)
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

// releaseAlertNow is /me's admin-only releaseAlert, or nil when there is
// nothing to say.
func (p *Plugin) releaseAlertNow() *releaseAlert {
	found := p.ownRelease()

	switch {
	case found.State == release.Altered:
		return &releaseAlert{
			State:   string(found.State),
			Problem: "This plugin's files do not match the signed release they came with: " + found.Reason + ". It goes on running. Install the release again from its published bundle.",
			File:    found.File,
		}
	case found.State == release.Signed && found.UnlistedCount > 0:
		return &releaseAlert{State: string(found.State), Unlisted: found.UnlistedCount, UnlistedFiles: found.Unlisted}
	default:
		return nil
	}
}
