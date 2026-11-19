//go:build !villanet_standin

package main

import "github.com/vvilla-01/matty-plugin/server/appcheck"

// Every build that is released: standin.go, which points Steam's two hosts at
// the stand-in on a developer's machine, is not in it. Nothing here can be
// told to ask anything but Steam, by a setting or by the environment, and
// TestAReleaseAsksOnlySteam holds that.

// standInRefusal is why a build will not start: never, for a release, which
// asks Mattermost nothing to find that out.
func (p *Plugin) standInRefusal() string { return "" }

// withDevelopmentAppCheck is the app check as a release makes it: as the
// settings say, and nothing more. The development build's twin also takes an
// emulator's attestation.
func withDevelopmentAppCheck(policy appcheck.Policy) appcheck.Policy { return policy }
