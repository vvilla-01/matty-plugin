package main

import (
	"net/http"
	"strings"
)

// The oldest Matty this server answers: the Minimum Matty version setting
// (MinimumAppVersion), under App check, which is blank for none, "latest"
// for the newest release of Matty this server knows (app_latest.go), or a
// version N.N.N. Matty says its version first in its
// User-Agent ("Matty/0.1.32 (Android 17; Mattermost client)", or
// "Matty/0.1.32-debug" for a debug build). A request that says it is Matty
// with a version older than the minimum, or with no version that can be
// read, is refused on every route but the ones the app check leaves open,
// so that the app can still read /access, see the minimum there and say
// why. Whatever does not say it is Matty, the web panel included, is never
// refused by it.
//
// A voice session keeps what its join said (voiceJoinAsk), and when the
// minimum in force changes, the session of a Matty that does not meet it
// ends with the same sentence (voiceSessions.endOutdated); a join still
// connecting then reads the minimum once its session is its own, and ends
// it the same way (voiceSessions.join).
//
// The version is the app's own word: a changed copy can say any version, or
// not say it is Matty at all, and then neither this nor the app check
// refuses it. The app check, set to refuse, turns away a copy that says it
// is Matty and is not the official build, and holds a session that passed
// to the version its app said as it proved itself (appProvenText), so that
// with it the minimum goes by what the phone proved.

// codeAppOutdated is the refusal of a Matty older than the minimum.
const codeAppOutdated = "app_outdated"

// Where the minimum comes from, as the admin card's minimumFrom says it.
const (
	appMinimumFromSetting = "setting"
	appMinimumFromLatest  = "latest"
)

// appMinimum is the setting as a version, or "" when it holds none: blank,
// latest, or anything that is not N.N.N.
func (c *configuration) appMinimum() string {
	minimum := strings.TrimSpace(c.MinimumAppVersion)
	if !updateVersionRE.MatchString(minimum) {
		return ""
	}

	return minimum
}

// appMinimumLatest is whether the setting is the latest release: "latest"
// in any case, with spaces around it.
func (c *configuration) appMinimumLatest() bool {
	return strings.EqualFold(strings.TrimSpace(c.MinimumAppVersion), appMinimumFromLatest)
}

// appMinimumFrom is which of the setting's three values it holds: "setting"
// for a version, "latest", or "" for none, blank or anything else.
func (c *configuration) appMinimumFrom() string {
	switch {
	case c.appMinimumLatest():
		return appMinimumFromLatest
	case c.appMinimum() != "":
		return appMinimumFromSetting
	default:
		return ""
	}
}

// appMinimumFor is the minimum in force under cfg, or "": the setting's
// version, or, for the latest release, the version this node knows, and
// none while it knows none. Every reader of the minimum asks this: the
// gate, /access and /me, the admin card, a voice join and endOutdated.
func (p *Plugin) appMinimumFor(cfg *configuration) string {
	if cfg.appMinimumLatest() {
		return p.appLatestNow().Version
	}

	return cfg.appMinimum()
}

// appMinimumInForce is the minimum in force under the settings as they are
// now, or "".
func (p *Plugin) appMinimumInForce() string {
	return p.appMinimumFor(p.getConfiguration())
}

// appMinimumProblem says that the setting holds something that is neither
// a version nor the latest release and so counts as none, for the app
// check's card, or "".
func (c *configuration) appMinimumProblem() string {
	shown := strings.TrimSpace(c.MinimumAppVersion)
	if shown == "" || c.appMinimumFrom() != "" {
		return ""
	}

	return "“" + cutRunes(printableText(shown), 40) + "” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force."
}

// mattyVersionOf is the version a Matty's User-Agent says, without a "-…"
// or "+…" ending, or "" when it says none that can be read.
func mattyVersionOf(userAgent string) string {
	said, ok := strings.CutPrefix(userAgent, mattyUserAgent)
	if !ok {
		return ""
	}
	said, _, _ = strings.Cut(said, " ")

	return plainVersion(said)
}

// mattyOf is whether a User-Agent says it is Matty, and the version it
// says, "" when none can be read.
func mattyOf(userAgent string) (bool, string) {
	return strings.HasPrefix(userAgent, mattyUserAgent), mattyVersionOf(userAgent)
}

// passAppVersionGate refuses a Matty older than the minimum, or whose
// version cannot be read, while a minimum is in force. True when the request may
// go on; otherwise it has answered. It runs before the app check's gate, on
// the same routes: a Matty that is too old is told so rather than asked to
// prove itself.
func (p *Plugin) passAppVersionGate(w http.ResponseWriter, r *http.Request) bool {
	if appCheckExemptRequest(r) || !strings.HasPrefix(r.UserAgent(), mattyUserAgent) {
		return true
	}

	minimum := p.appMinimumInForce()
	if minimum == "" {
		return true
	}

	message := appOutdatedText(minimum, mattyVersionOf(r.UserAgent()))
	if message == "" {
		return true
	}

	w.Header().Set("Cache-Control", "no-store")
	writeCodedError(w, http.StatusForbidden, message, codeAppOutdated)

	return false
}

// appOutdatedText is what a Matty of a version ("" when none can be read)
// is told under a minimum it does not meet, or "" when it meets it or there
// is no minimum.
func appOutdatedText(minimum, version string) string {
	if minimum == "" || (version != "" && !newerVersion(minimum, version)) {
		return ""
	}

	message := "This server needs Matty " + minimum + " or newer."
	if version != "" {
		message += " You have " + version + "."
	}

	return message
}
