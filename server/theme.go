package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Putting everybody on the theme an admin chose.
//
// Mattermost can already restrict which of ITS themes people use — ThemeSettings
// has DefaultTheme and AllowedThemes — but those name built-in themes, so a
// custom one cannot be set that way at all. What a custom theme actually is, is
// a per-user preference in the category "theme", and setting a preference is
// something a plugin can do.
//
// Applying it is therefore three separate questions, and they are kept separate
// because the answers differ:
//
//   - New accounts get it, always, when this is switched on. Nobody has chosen
//     anything yet, so nothing is being overridden.
//   - Everybody else gets it when an admin presses the button. An explicit act,
//     with a count of who it touched.
//   - Keeping people on it is optional and off by default, because it means
//     taking the choice away every time they log in.

// themeApplyOnLogin is the value of the dropdown that re-applies at every login.
const themeApplyOnLogin = "login"

// Mattermost's own rule, from Preference.PreUpdate: anything that is not a hex
// colour is silently replaced with white when the preference is saved — except
// these three, which are not colours.
var (
	themeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)
	themeNotColors    = map[string]bool{"image": true, "type": true, "codeTheme": true}
)

// parseTheme reads the configured theme and says what is wrong with it.
//
// Strict about the same things Mattermost is strict about. A value it does not
// recognise as a colour is not rejected by the server — it is quietly turned
// WHITE — and discovering that as a white sidebar after forcing it onto
// everybody is a poor way to find out.
func parseTheme(source string) (string, []string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil, nil
	}

	var theme map[string]string
	if err := json.Unmarshal([]byte(source), &theme); err != nil {
		return "", nil, fmt.Errorf("the theme is not a flat JSON object of strings: %w", err)
	}

	if len(theme) == 0 {
		return "", nil, fmt.Errorf("the theme has nothing in it")
	}

	var whitened []string
	for name, value := range theme {
		if themeNotColors[name] || themeColorPattern.MatchString(value) {
			continue
		}

		whitened = append(whitened, name)
	}
	sort.Strings(whitened)

	// Stored as Mattermost stores it, so the value the plugin writes and the
	// value the webapp reads back are the same string.
	encoded, err := json.Marshal(theme)
	if err != nil {
		return "", nil, err
	}

	return string(encoded), whitened, nil
}

// applyThemeTo puts the theme on one person.
//
// It also removes any PER-TEAM theme they have, which is the difference between
// setting a default and actually applying one: the webapp reads the preference
// named for the current team first and only falls back to the unnamed one, so a
// per-team override would quietly win and the person would see no change at all.
func (p *Plugin) applyThemeTo(userID, theme string) error {
	if err := p.API.UpdatePreferencesForUser(userID, []model.Preference{{
		UserId:   userID,
		Category: model.PreferenceCategoryTheme,
		Name:     "",
		Value:    theme,
	}}); err != nil {
		return fmt.Errorf("could not set the theme: %s", err.Error())
	}

	existing, err := p.API.GetPreferencesForUser(userID)
	if err != nil {
		// The theme itself is set; the overrides are a refinement.
		return nil
	}

	var overrides []model.Preference
	for _, preference := range existing {
		if preference.Category == model.PreferenceCategoryTheme && preference.Name != "" {
			overrides = append(overrides, preference)
		}
	}

	if len(overrides) > 0 {
		if err := p.API.DeletePreferencesForUser(userID, overrides); err != nil {
			p.API.LogWarn("Could not clear a per-team theme", "user_id", userID, "error", err.Error())
		}
	}

	return nil
}

// applyThemeToEveryone is what the button in the System Console does.
func (p *Plugin) applyThemeToEveryone(theme string) (int, error) {
	if theme == "" {
		return 0, fmt.Errorf("there is no theme to apply")
	}

	const perPage = 200

	applied := 0
	for page := 0; page < 500; page++ {
		users, err := p.API.GetUsers(&model.UserGetOptions{Page: page, PerPage: perPage})
		if err != nil {
			return applied, fmt.Errorf("could not list the users: %s", err.Error())
		}
		if len(users) == 0 {
			break
		}

		for _, user := range users {
			// Deactivated accounts and bots are not people with a screen.
			if user == nil || user.DeleteAt != 0 || user.IsBot {
				continue
			}

			if err := p.applyThemeTo(user.Id, theme); err != nil {
				p.API.LogWarn("Could not set the theme", "user_id", user.Id, "error", err.Error())

				continue
			}
			applied++
		}

		if len(users) < perPage {
			break
		}
	}

	return applied, nil
}

// UserHasBeenCreated gives a new account the theme before they ever see the
// default one.
func (p *Plugin) UserHasBeenCreated(_ *plugin.Context, user *model.User) {
	p.themeOnEvent(user, false)
}

// UserHasLoggedIn re-applies it, but only when an admin has asked for that.
func (p *Plugin) UserHasLoggedIn(_ *plugin.Context, user *model.User) {
	p.themeOnEvent(user, true)
}

func (p *Plugin) themeOnEvent(user *model.User, isLogin bool) {
	cfg := p.getConfiguration()
	if !cfg.EnableTheme || user == nil || user.IsBot {
		return
	}

	if isLogin && cfg.ThemeApply != themeApplyOnLogin {
		return
	}

	theme, _, err := parseTheme(cfg.Theme)
	if err != nil || theme == "" {
		return
	}

	if err := p.applyThemeTo(user.Id, theme); err != nil {
		p.API.LogWarn("Could not set the theme", "user_id", user.Id, "error", err.Error())
	}
}
