//go:build !villanet_standin

package main

import (
	"testing"

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
