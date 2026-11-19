package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mattermost does not reject a theme value that is not a colour — it silently
// replaces it with WHITE. Finding that out as a white sidebar, after forcing it
// onto everybody, is a poor way to learn it.
func TestAThemeSaysWhatMattermostWouldWhiten(t *testing.T) {
	_, whitened, err := parseTheme(`{
		"type": "custom",
		"codeTheme": "monokai",
		"sidebarBg": "#000000",
		"linkColor": "#f00",
		"buttonBg": "red",
		"mentionBg": "rgb(200,16,46)"
	}`)

	require.NoError(t, err)
	require.Equal(t, []string{"buttonBg", "mentionBg"}, whitened)
}

// A stock Mattermost theme, copied as Settings › Display › Theme copies one,
// survives a save unchanged.
func TestAStockThemeIsClean(t *testing.T) {
	source, err := readFixture("testdata/theme-denim.json")
	require.NoError(t, err)

	encoded, whitened, err := parseTheme(source)
	require.NoError(t, err)
	require.Empty(t, whitened, "a stock theme survives a save unchanged")

	var theme map[string]string
	require.NoError(t, json.Unmarshal([]byte(encoded), &theme))
	require.Equal(t, "Denim", theme["type"])
	require.Equal(t, "#1e325c", theme["sidebarBg"])
}

// Nothing is applied on a fresh server: the Theme setting ships empty.
func TestTheDefaultThemeIsNothingToApply(t *testing.T) {
	require.Equal(t, "", sectionDefaults["Theme"])

	encoded, whitened, err := parseTheme(sectionDefaults["Theme"].(string))
	require.NoError(t, err)
	require.Empty(t, encoded)
	require.Empty(t, whitened)
}

func TestABadThemeIsRefusedRatherThanApplied(t *testing.T) {
	for name, source := range map[string]string{
		"not json":     `{"sidebarBg": }`,
		"not flat":     `{"sidebarBg": {"nested": "#000"}}`,
		"an array":     `["#000000"]`,
		"empty object": `{}`,
	} {
		_, _, err := parseTheme(source)
		require.Error(t, err, name)
	}

	// Nothing configured is not an error, it is nothing to do.
	encoded, _, err := parseTheme("   ")
	require.NoError(t, err)
	require.Empty(t, encoded)
}

// A per-team theme wins over the unnamed one in the webapp, so leaving it would
// mean "applied" people seeing no change at all.
func TestApplyingClearsAPerTeamTheme(t *testing.T) {
	api := &plugintest.API{}

	var set []model.Preference
	var deleted []model.Preference

	api.On("UpdatePreferencesForUser", "uid-victor", mock.Anything).Return(
		func(_ string, prefs []model.Preference) *model.AppError {
			set = append(set, prefs...)

			return nil
		},
	)
	api.On("GetPreferencesForUser", "uid-victor").Return([]model.Preference{
		{UserId: "uid-victor", Category: model.PreferenceCategoryTheme, Name: "", Value: "{}"},
		{UserId: "uid-victor", Category: model.PreferenceCategoryTheme, Name: "team-one", Value: "{}"},
		{UserId: "uid-victor", Category: "display_settings", Name: "use_military_time", Value: "true"},
	}, nil)
	api.On("DeletePreferencesForUser", "uid-victor", mock.Anything).Return(
		func(_ string, prefs []model.Preference) *model.AppError {
			deleted = append(deleted, prefs...)

			return nil
		},
	)
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	p := &Plugin{}
	p.SetAPI(api)

	require.NoError(t, p.applyThemeTo("uid-victor", `{"sidebarBg":"#000000"}`))

	require.Len(t, set, 1)
	require.Equal(t, model.PreferenceCategoryTheme, set[0].Category)
	require.Empty(t, set[0].Name, "the unnamed theme is the one everything falls back to")

	require.Len(t, deleted, 1, "only the per-team theme")
	require.Equal(t, "team-one", deleted[0].Name)
}

// Switched off, or told to leave people alone, the hooks do nothing.
func TestTheLoginHookOnlyActsWhenAsked(t *testing.T) {
	api := &plugintest.API{}
	p := &Plugin{}
	p.SetAPI(api)

	off := &configuration{EnableTheme: false, Theme: `{"sidebarBg":"#000000"}`, ThemeApply: themeApplyOnLogin}
	p.setConfiguration(off)
	p.UserHasLoggedIn(nil, &model.User{Id: "uid-victor"})

	leaveAlone := &configuration{EnableTheme: true, Theme: `{"sidebarBg":"#000000"}`, ThemeApply: "new"}
	p.setConfiguration(leaveAlone)
	p.UserHasLoggedIn(nil, &model.User{Id: "uid-victor"})

	// A bot has no screen to theme.
	forcing := &configuration{EnableTheme: true, Theme: `{"sidebarBg":"#000000"}`, ThemeApply: themeApplyOnLogin}
	p.setConfiguration(forcing)
	p.UserHasLoggedIn(nil, &model.User{Id: "uid-bot", IsBot: true})

	api.AssertNotCalled(t, "UpdatePreferencesForUser", mock.Anything, mock.Anything)
}

func readFixture(path string) (string, error) {
	data, err := os.ReadFile(path)

	return string(data), err
}
