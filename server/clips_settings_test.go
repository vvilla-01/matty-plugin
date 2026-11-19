package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The Clip length dropdown exactly as plugin.json offers it.
func manifestClipLengths(t *testing.T) (string, []int) {
	t.Helper()

	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					Key     string `json:"key"`
					Default any    `json:"default"`
					Options []struct {
						Value string `json:"value"`
					} `json:"options"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			if setting.Key != "ClipLengthSeconds" {
				continue
			}

			lengths := []int{}
			for _, option := range setting.Options {
				seconds, err := strconv.Atoi(option.Value)
				require.NoError(t, err, "every option is a number of seconds")
				lengths = append(lengths, seconds)
			}

			fallback, ok := setting.Default.(string)
			require.True(t, ok, "a dropdown's default is one of its values, which are text")

			return fallback, lengths
		}
	}

	require.Fail(t, "plugin.json has no ClipLengthSeconds setting")

	return "", nil
}

// One press keeps one of the lengths the page offers, whatever was stored: a
// number in between takes the nearer, the shorter on a tie; anything out of
// range takes the end it is past; anything that is not a number takes the
// default. And the window held on disk is exactly that length.
func TestClipLengthNormalisation(t *testing.T) {
	for stored, want := range map[string]int{
		"999": 300,
		"0":   30,
		"75":  60,
		"100": 90,
		"abc": 120,
		"":    120,

		// Every option as itself.
		"30": 30, "60": 60, "90": 90, "120": 120, "180": 180, "240": 240, "300": 300,

		// The other ties, each to the shorter.
		"150": 120,
		"210": 180,
		"270": 240,

		"-5":    30,
		" 180 ": 180,
		"1e3":   120,
		"2:00":  120,

		// Too big to be a number at all.
		"99999999999999999999": 120,
	} {
		cfg := &configuration{ClipLengthSeconds: stored}
		require.Equal(t, want, cfg.clipLengthSeconds(), "stored %q", stored)
		require.Equal(t, time.Duration(want)*time.Second, cfg.ringWindow(), "stored %q", stored)
	}

	// The lengths it can come out as are the dropdown's, and the default is
	// the dropdown's default.
	fallback, lengths := manifestClipLengths(t)
	require.Equal(t, clipLengthOptions, lengths)
	require.Equal(t, strconv.Itoa(clipLengthDefault), fallback)
	require.Equal(t, clipMaxSeconds, lengths[len(lengths)-1])

	// Normalising leaves the stored text alone: it is read through
	// clipLengthSeconds wherever it is used, and the settings backup carries
	// what the admin chose.
	cfg := &configuration{ClipLengthSeconds: "75"}
	cfg.normalize()
	require.Equal(t, "75", cfg.ClipLengthSeconds)
}

// How long clips are kept and how much room they may take are held inside
// their bounds, and a setting nobody has saved takes the page's default.
func TestClipSettingsAreClampedAndDefaulted(t *testing.T) {
	for stored, want := range map[int]int{-3: 30, 0: 30, 1: 1, 30: 30, 365: 365, 366: 365, 10000: 365} {
		// The same answer before normalize has run as after: an unset
		// configuration must not keep clips for no days.
		raw := &configuration{ClipRetentionDays: stored}
		require.Equal(t, want, raw.clipRetentionDays(), "keep clips for %d days, unnormalised", stored)

		cfg := &configuration{ClipRetentionDays: stored}
		cfg.normalize()
		require.Equal(t, want, cfg.ClipRetentionDays, "keep clips for %d days", stored)
		require.Equal(t, want, cfg.clipRetentionDays(), "keep clips for %d days", stored)
	}

	for stored, want := range map[int]int{-1: 20, 0: 20, 1: 1, 20: 20, 2000: 2000, 2001: 2000} {
		cfg := &configuration{ClipStorageLimitGB: stored}
		cfg.normalize()
		require.Equal(t, want, cfg.ClipStorageLimitGB, "space for clips %d GB", stored)
	}

	// Never saved, on a Mattermost that applies no section defaults itself
	// (as before 11.8): on, two minutes, thirty days, twenty gigabytes.
	p, _ := olderServerPlugin(t, map[string]any{"broadcastboxurl": "http://bb.lan:8080"})
	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.True(t, cfg.EnableClips)
	require.Equal(t, "120", cfg.ClipLengthSeconds)
	require.Equal(t, 120, cfg.clipLengthSeconds())
	require.Equal(t, 30, cfg.ClipRetentionDays)
	require.Equal(t, 20, cfg.ClipStorageLimitGB)

	// Saved, they are what runs, held inside their bounds like any other
	// value: a stored 0 is the default, not a limit of nothing.
	p, _ = olderServerPlugin(t, map[string]any{
		"enableclips":        false,
		"cliplengthseconds":  "300",
		"clipretentiondays":  7,
		"ClipStorageLimitGB": 0,
	})
	require.NoError(t, p.OnConfigurationChange())

	cfg = p.getConfiguration()
	require.False(t, cfg.EnableClips)
	require.Equal(t, 300, cfg.clipLengthSeconds())
	require.Equal(t, 7, cfg.ClipRetentionDays)
	require.Equal(t, 20, cfg.ClipStorageLimitGB)
}

// Every snapshot says what there is of Clips, whichever way the poll went: it
// is always present once the plugin is up, so the panel never has to tell
// "nothing yet" from "not told". Nothing can be made while there is nowhere to
// keep it.
func TestEverySnapshotSaysWhatThereIsOfClips(t *testing.T) {
	var lock sync.Mutex
	up := true

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		if !up {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer origin.Close()

	p := watchersPlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.ClipLengthSeconds = "75"
	cfg.ClipRetentionDays = 7
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	want := &clipsView{Enabled: false, LengthSeconds: 60, RetentionDays: 7, Count: 0}

	p.pollOnce(t.Context(), true)
	current := p.getSnapshot()
	require.Empty(t, current.Error)
	require.Equal(t, want, current.Clips)

	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"clips":{"enabled":false,"lengthSeconds":60,"retentionDays":7,"count":0}`)

	// Broadcast Box down.
	lock.Lock()
	up = false
	lock.Unlock()

	p.pollOnce(t.Context(), true)
	current = p.getSnapshot()
	require.NotEmpty(t, current.Error)
	require.Equal(t, want, current.Clips)

	// Nothing to reach it by.
	unset := cfg.Clone()
	unset.BroadcastBoxURL = ""
	p.setConfiguration(unset)

	p.pollOnce(t.Context(), true)
	current = p.getSnapshot()
	require.False(t, current.Configured)
	require.Equal(t, want, current.Clips)
}
