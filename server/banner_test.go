package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The two choices Mattermost's Classification Markings offer, and nothing
// else: anything unrecognised shows the banner top and bottom, as the panel
// always did, rather than quietly dropping a classification marking.
func TestBannerPlacementIsTopOrBoth(t *testing.T) {
	for saved, want := range map[string]string{
		"top":    "top",
		" top ":  "top",
		"both":   "both",
		"":       "both",
		"TOP":    "both",
		"bottom": "both",
	} {
		cfg := &configuration{BannerPlacement: saved}
		require.Equal(t, want, cfg.bannerPlacement(), "saved %q", saved)
	}
}
