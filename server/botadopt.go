package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Taking over a bot an earlier plugin left behind.
//
// This exists because of a migration. The plugin's id changed from
// com.villa.broadcastbox to ceo.villa.villanet, and Mattermost treats a new id
// as a new plugin: EnsureBotUser has no memory of the old bot and finds one by
// USERNAME. The old bot had already been renamed from "villanet-tv" to
// "villanet", so the lookup found nothing, a brand-new @villanet-tv was created,
// and the original @villanet — every relayed message and birthday greeting it
// ever posted — was left owned by a plugin that no longer exists. Typing
// "villanet" into the username box then failed, because the name was taken by
// exactly the account the admin wanted.
//
// Deleting the old one is the wrong fix. PermanentDeleteUser removes every post
// the account ever made, in every channel. So this points the plugin at it
// instead, and keeps the history where it is.

const (
	// ensureBotKey is where EnsureBotUser remembers which account is this
	// plugin's bot: server/channels/app/bot.go, internalKeyPrefix + "botid",
	// read and written under the plugin's own id — the same store KVGet reads,
	// with no guard on the prefix. Writing it is how the plugin is told "this
	// one", deterministically, instead of hoping a lookup by name lands on the
	// right account.
	ensureBotKey = "mmi_botid"
)

// adoptableBot is an existing bot the plugin may take over.
type adoptableBot struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`

	// Whatever created it: a plugin id, usually one that is not installed any
	// more. Shown to the admin, because "this belonged to X" is the whole
	// reason they would or would not want it.
	Owner string `json:"owner"`

	disabled bool
}

// adoptionCandidate reports the bot holding a username when the plugin may take
// it over — nil and no error when there is nothing to take over, and an error
// saying why when there is something but the plugin must leave it alone.
//
// The rules, and why each is there:
//
//   - It must be a BOT. Taking over a person's account is not a thing a
//     settings page should be able to do.
//   - It must not belong to a person. A bot made from Integrations is owned by
//     whoever made it, and is theirs.
//   - The plugin that made it must be GONE, not merely disabled. A disabled
//     plugin that is switched back on goes straight back to its own bot, and
//     two plugins patching one account alternately is a fight nobody wins.
func (p *Plugin) adoptionCandidate(username string) (*adoptableBot, error) {
	username = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if username == "" {
		return nil, nil
	}

	user, appErr := p.API.GetUserByUsername(username)
	if appErr != nil || user == nil {
		// Nobody has the name, so there is nothing in the way to take over.
		return nil, nil
	}

	if user.Id == p.currentBotID() {
		return nil, nil
	}

	if !user.IsBot {
		return nil, fmt.Errorf("@%s is a person's account, not a bot, so the plugin will not take it over", username)
	}

	bot, appErr := p.API.GetBot(user.Id, true)
	if appErr != nil || bot == nil {
		return nil, fmt.Errorf("@%s could not be looked up as a bot", username)
	}

	candidate := &adoptableBot{
		UserID:   user.Id,
		Username: user.Username,
		Owner:    bot.OwnerId,
		disabled: bot.DeleteAt != 0,
	}

	// Made by this plugin at some earlier point: already ours in every sense
	// but the remembered id.
	if bot.OwnerId == manifestID {
		return candidate, nil
	}

	if owner, ownerErr := p.API.GetUser(bot.OwnerId); ownerErr == nil && owner != nil {
		return nil, fmt.Errorf("@%s belongs to @%s rather than to a plugin, so the plugin will not take it over", username, owner.Username)
	}

	// Not installed is a 404 from GetPluginStatus. Anything else — installed
	// and disabled, or an answer that could not be had — leaves it alone.
	if _, statusErr := p.API.GetPluginStatus(bot.OwnerId); statusErr == nil {
		return nil, fmt.Errorf("@%s belongs to the plugin %s, which is still installed. Remove that plugin first, or it will take its bot back the next time it is enabled", username, bot.OwnerId)
	} else if statusErr.StatusCode != http.StatusNotFound {
		return nil, fmt.Errorf("could not confirm that %s, which made @%s, has been removed", bot.OwnerId, username)
	}

	return candidate, nil
}

// adoptBot makes an existing bot this plugin's bot, and retires the one it was
// using. It is only ever the bot holding the CONFIGURED username, looked up
// again here rather than trusted from the request: the page that asked may be
// minutes old.
func (p *Plugin) adoptBot(userID string) error {
	cfg := p.getConfiguration()

	candidate, err := p.adoptionCandidate(cfg.BotUsername)
	if err != nil {
		return err
	}
	if candidate == nil || candidate.UserID != userID {
		return errors.New("that is no longer the bot holding this name. Reload the page and look again")
	}

	previous := p.currentBotID()

	restore := func() {
		if previous != "" {
			_ = p.API.KVSet(ensureBotKey, []byte(previous))
		} else {
			_ = p.API.KVDelete(ensureBotKey)
		}
	}

	if appErr := p.API.KVSet(ensureBotKey, []byte(candidate.UserID)); appErr != nil {
		return fmt.Errorf("could not point the plugin at @%s: %s", candidate.Username, appErr.Error())
	}

	// A disabled bot cannot post. Switched on only if it was off, and back off
	// if what follows fails, so a failed attempt leaves things as it found them.
	if candidate.disabled {
		if _, appErr := p.API.UpdateBotActive(candidate.UserID, true); appErr != nil {
			restore()

			return fmt.Errorf("@%s is disabled and could not be switched back on: %s", candidate.Username, appErr.Error())
		}
	}

	displayName := cfg.BotDisplayName
	if displayName == "" {
		displayName = defaultBotDisplayName
	}

	// Through EnsureBotUser like every other bot change, so the configured
	// display name and description land on the account now in use. The
	// username is unchanged — it is the name that was in the way — so this is
	// a patch that cannot collide.
	botID, ensureErr := p.ensureBotNamed(cfg.BotUsername, displayName, cfg.BotDescription)
	if ensureErr != nil || botID != candidate.UserID {
		restore()
		if candidate.disabled {
			_, _ = p.API.UpdateBotActive(candidate.UserID, false)
		}

		if ensureErr != nil {
			return fmt.Errorf("could not take over @%s: %s", candidate.Username, ensureErr.Error())
		}

		return fmt.Errorf("Mattermost did not hand over @%s", candidate.Username)
	}

	p.rememberBot(botID)
	p.setBotProblem("")

	// Retired, NOT deleted. PermanentDeleteUser removes every post an account
	// has made, and whatever the retired bot posted since the migration is
	// still somebody's conversation. Disabling takes it out of use and out of
	// the way; deleting it is left to an admin who has decided those posts can
	// go.
	if previous != "" && previous != botID {
		if _, appErr := p.API.UpdateBotActive(previous, false); appErr != nil {
			p.API.LogWarn("Took over a bot but could not disable the one it replaced",
				"replaced", previous, "error", appErr.Error())
		}
	}

	// Channel membership was remembered for the old account. The new one has to
	// be in those channels in its own right, or its @all reaches nobody.
	p.bridgeLock.Lock()
	p.channelJoined = map[string]bool{}
	p.bridgeLock.Unlock()

	p.goSafely("joining the bot to its channels", p.joinConfiguredChannels)

	p.API.LogInfo("Took over an existing bot", "username", candidate.Username,
		"previous_owner", candidate.Owner, "replaced", previous)

	return nil
}

// currentBotID reads the bot id without trying to create one. getBotUserID
// retries EnsureBotUser when there is no bot, which is the wrong side effect
// for code that only wants to know which account is in use.
func (p *Plugin) currentBotID() string {
	p.bridgeLock.RLock()
	defer p.bridgeLock.RUnlock()

	return p.botUserID
}

// storedBotUsername is the name the plugin's remembered bot has right now.
// Used as the fallback when a rename is refused: the account already holds that
// name, so asking for it again cannot collide with anything.
func (p *Plugin) storedBotUsername() string {
	stored, appErr := p.API.KVGet(ensureBotKey)
	if appErr != nil || len(stored) == 0 {
		return ""
	}

	user, appErr := p.API.GetUser(string(stored))
	if appErr != nil || user == nil {
		return ""
	}

	return user.Username
}

type adoptRequest struct {
	UserID string `json:"userId"`
}

func (p *Plugin) handleBotAdopt(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can change which account the plugin posts as.")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	var request adoptRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil || request.UserID == "" {
		writeError(w, http.StatusBadRequest, "Say which bot to take over.")
		return
	}

	if err := p.adoptBot(request.UserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	p.API.LogWarn("A system admin moved the plugin onto an existing bot", "user_id", userID, "bot_id", request.UserID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
