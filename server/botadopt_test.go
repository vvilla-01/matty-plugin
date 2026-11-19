package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The situation this exists for, exactly as it was found on the server:
//
//   - @villanet-tv, made by THIS plugin when it was first installed under its
//     new id, because it could not find the old bot by its old name.
//   - @villanet, the original — renamed on the previous plugin, carrying every
//     relayed message and birthday post — owned by com.villa.broadcastbox,
//     which is no longer installed.
//   - "villanet" typed into the username box, and refused, because the account
//     the admin actually wants is the one holding it.
type adoptScene struct {
	api       *plugintest.API
	plugin    *Plugin
	store     map[string][]byte
	activated map[string]bool
}

const (
	sceneNewBot = "uid-villanet-tv"
	sceneOldBot = "uid-villanet"
	sceneOwner  = "com.villa.broadcastbox"
)

type sceneOptions struct {
	oldIsBot        bool
	oldDisabled     bool
	ownerIsPerson   bool
	ownerInstalled  bool
	ensureFails     bool
	ensureReturnsID string
}

func adoptSceneWith(t *testing.T, options sceneOptions) *adoptScene {
	t.Helper()

	scene := &adoptScene{
		api:       &plugintest.API{},
		store:     map[string][]byte{ensureBotKey: []byte(sceneNewBot)},
		activated: map[string]bool{},
	}
	api := scene.api

	var deleteAt int64
	if options.oldDisabled {
		deleteAt = 1700000000000
	}

	api.On("GetUserByUsername", "villanet").Return(
		&model.User{Id: sceneOldBot, Username: "villanet", IsBot: options.oldIsBot}, nil)
	api.On("GetBot", sceneOldBot, true).Return(
		&model.Bot{UserId: sceneOldBot, Username: "villanet", OwnerId: sceneOwner, DeleteAt: deleteAt}, nil)

	if options.ownerIsPerson {
		api.On("GetUser", sceneOwner).Return(&model.User{Id: sceneOwner, Username: "villa"}, nil)
	} else {
		api.On("GetUser", sceneOwner).Return(nil, &model.AppError{Message: "no such user", StatusCode: http.StatusNotFound})
	}

	if options.ownerInstalled {
		api.On("GetPluginStatus", sceneOwner).Return(&model.PluginStatus{PluginId: sceneOwner}, nil)
	} else {
		api.On("GetPluginStatus", sceneOwner).Return(nil,
			&model.AppError{Message: "app.plugin.not_installed.app_error", StatusCode: http.StatusNotFound})
	}

	api.On("KVSet", ensureBotKey, mock.Anything).Return(
		func(key string, value []byte) *model.AppError {
			scene.store[key] = append([]byte(nil), value...)

			return nil
		})
	api.On("KVDelete", ensureBotKey).Return(
		func(key string) *model.AppError {
			delete(scene.store, key)

			return nil
		})

	returns := options.ensureReturnsID
	if returns == "" {
		returns = sceneOldBot
	}
	if options.ensureFails {
		api.On("EnsureBotUser", mock.Anything).Return("", errors.New("PatchBot: something went wrong"))
	} else {
		api.On("EnsureBotUser", mock.Anything).Return(
			func(bot *model.Bot) string {
				// What Mattermost does with a remembered id: patch that account.
				if string(scene.store[ensureBotKey]) == sceneOldBot && bot.Username == "villanet" {
					return returns
				}

				return ""
			},
			func(bot *model.Bot) error {
				if string(scene.store[ensureBotKey]) == sceneOldBot && bot.Username == "villanet" {
					return nil
				}

				return errors.New("username villanet is taken")
			},
		)
	}

	api.On("UpdateBotActive", mock.Anything, mock.Anything).Return(
		func(id string, active bool) *model.Bot {
			scene.activated[id] = active

			return &model.Bot{UserId: id}
		},
		func(_ string, _ bool) *model.AppError { return nil },
	)

	// Joining the configured channels runs on its own goroutine afterwards;
	// with none configured it asks for nothing.
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	scene.plugin = &Plugin{}
	scene.plugin.SetAPI(api)
	scene.plugin.botUserID = sceneNewBot
	scene.plugin.setConfiguration(&configuration{
		BotUsername:    "villanet",
		BotDisplayName: "VillaNET",
		BotDescription: "The all-seeing eye.",
	})

	return scene
}

func TestTheOrphanedOriginalBotIsOfferedForAdoption(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})

	candidate, err := scene.plugin.adoptionCandidate("villanet")
	require.NoError(t, err)
	require.NotNil(t, candidate)
	require.Equal(t, sceneOldBot, candidate.UserID)
	require.Equal(t, sceneOwner, candidate.Owner, "the admin is told what made it")
}

// The whole point: after adoption the plugin posts as the ORIGINAL account, so
// its history stays attached to the bot that is still in use.
func TestTakingOverTheOriginalKeepsItsHistoryAndRetiresTheNewOne(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})

	require.NoError(t, scene.plugin.adoptBot(sceneOldBot))

	require.Equal(t, sceneOldBot, scene.plugin.currentBotID(), "the plugin now posts as @villanet")
	require.Equal(t, sceneOldBot, string(scene.store[ensureBotKey]),
		"and EnsureBotUser will find it again after a restart")
	require.Equal(t, "", scene.plugin.botProblemNow(), "the red error goes away")

	// Retired, not deleted: deleting a user erases every post it made.
	active, touched := scene.activated[sceneNewBot]
	require.True(t, touched, "@villanet-tv is taken out of use")
	require.False(t, active)

	_, touchedOld := scene.activated[sceneOldBot]
	require.False(t, touchedOld, "an already-active bot is not toggled for no reason")

	scene.api.AssertNotCalled(t, "DeleteUser", mock.Anything)
	scene.api.AssertNotCalled(t, "PermanentDeleteUser", mock.Anything)
}

func TestADisabledOriginalIsSwitchedBackOn(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true, oldDisabled: true})

	require.NoError(t, scene.plugin.adoptBot(sceneOldBot))
	require.True(t, scene.activated[sceneOldBot], "a disabled bot cannot post")
}

// If Mattermost will not hand the account over, the plugin must end up exactly
// where it started — still posting as the bot it had, and remembering it.
func TestAFailedTakeoverPutsEverythingBack(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true, oldDisabled: true, ensureFails: true})

	err := scene.plugin.adoptBot(sceneOldBot)
	require.Error(t, err)

	require.Equal(t, sceneNewBot, scene.plugin.currentBotID())
	require.Equal(t, sceneNewBot, string(scene.store[ensureBotKey]), "the remembered bot is restored")
	require.False(t, scene.activated[sceneOldBot], "the original is switched back off, as it was found")

	_, touchedNew := scene.activated[sceneNewBot]
	require.False(t, touchedNew, "the bot still in use is not disabled")
}

func TestAPersonIsNeverTakenOver(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: false})

	candidate, err := scene.plugin.adoptionCandidate("villanet")
	require.Nil(t, candidate)
	require.ErrorContains(t, err, "person's account")

	require.Error(t, scene.plugin.adoptBot(sceneOldBot))
	require.Equal(t, sceneNewBot, string(scene.store[ensureBotKey]))
}

// A bot made from Integrations belongs to whoever made it.
func TestAPersonsBotIsNeverTakenOver(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true, ownerIsPerson: true})

	candidate, err := scene.plugin.adoptionCandidate("villanet")
	require.Nil(t, candidate)
	require.ErrorContains(t, err, "belongs to @villa")
}

// A disabled plugin that is enabled again goes straight back to its own bot.
// Taking it over while that plugin still exists would start a tug of war.
func TestABotOfAPluginStillInstalledIsLeftAlone(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true, ownerInstalled: true})

	candidate, err := scene.plugin.adoptionCandidate("villanet")
	require.Nil(t, candidate)
	require.ErrorContains(t, err, "still installed")
}

func TestNothingIsOfferedWhenTheNameIsFreeOrAlreadyOurs(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})
	scene.api.On("GetUserByUsername", "nobody").Return(nil, &model.AppError{StatusCode: http.StatusNotFound})

	candidate, err := scene.plugin.adoptionCandidate("nobody")
	require.NoError(t, err)
	require.Nil(t, candidate)

	scene.plugin.botUserID = sceneOldBot
	candidate, err = scene.plugin.adoptionCandidate("villanet")
	require.NoError(t, err)
	require.Nil(t, candidate, "the bot already in use is not offered to itself")
}

// The settings page is told what is in the way, so it can offer the button.
func TestTheSettingsPageIsToldWhichBotCanBeTakenOver(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})
	scene.api.On("HasPermissionTo", "uid-admin", model.PermissionManageSystem).Return(true)
	scene.api.On("GetUser", sceneNewBot).Return(&model.User{Id: sceneNewBot, Username: "villanet-tv", FirstName: "VillaNET"}, nil)
	scene.api.On("GetBot", sceneNewBot, false).Return(&model.Bot{UserId: sceneNewBot, Description: "The all-seeing eye."}, nil)

	recorder := httptest.NewRecorder()
	scene.plugin.handleBotAvatar(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/avatar", nil), "uid-admin")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var identity struct {
		Username  string        `json:"username"`
		Adoptable *adoptableBot `json:"adoptable"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &identity))
	require.Equal(t, "villanet-tv", identity.Username)
	require.NotNil(t, identity.Adoptable)
	require.Equal(t, sceneOldBot, identity.Adoptable.UserID)
	require.Equal(t, sceneOwner, identity.Adoptable.Owner)
}

func TestOnlyASystemAdminCanMoveThePluginOntoAnotherBot(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})
	scene.api.On("HasPermissionTo", "uid-user", model.PermissionManageSystem).Return(false)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/bot/adopt", strings.NewReader(`{"userId":"`+sceneOldBot+`"}`))
	scene.plugin.handleBotAdopt(recorder, request, "uid-user")

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, sceneNewBot, string(scene.store[ensureBotKey]))
}

// The request names a bot, but only the one holding the CONFIGURED name can be
// taken over — a stale page, or a crafted request, cannot point the plugin at
// some other account.
func TestOnlyTheBotHoldingTheConfiguredNameCanBeTakenOver(t *testing.T) {
	scene := adoptSceneWith(t, sceneOptions{oldIsBot: true})

	err := scene.plugin.adoptBot("uid-some-other-bot")
	require.ErrorContains(t, err, "no longer the bot holding this name")
	require.Equal(t, sceneNewBot, string(scene.store[ensureBotKey]))
}

// After a takeover, the retired bot keeps its old name. A refused rename used
// to fall back to the DEFAULT name only — which the retired bot is sitting on —
// and would then have found both names taken and left the plugin with no bot.
// It now tries the name the account already has first, which cannot collide.
func TestARefusedRenameFallsBackToTheNameTheBotAlreadyHas(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", ensureBotKey).Return([]byte(sceneOldBot), (*model.AppError)(nil))
	api.On("GetUser", sceneOldBot).Return(&model.User{Id: sceneOldBot, Username: "villanet"}, nil)

	var asked []string
	api.On("EnsureBotUser", mock.Anything).Return(
		func(bot *model.Bot) string {
			asked = append(asked, bot.Username)
			if bot.Username == "villanet" {
				return sceneOldBot
			}

			return ""
		},
		func(bot *model.Bot) error {
			if bot.Username == "villanet" {
				return nil
			}

			return errors.New("username taken")
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
	p.setConfiguration(&configuration{BotUsername: "somebody-elses", BotDisplayName: "VillaNET"})

	p.ensureBot()

	require.Equal(t, []string{"somebody-elses", "villanet"}, asked,
		"the name the bot already has is tried before the default")
	require.Equal(t, sceneOldBot, p.currentBotID())
	require.Contains(t, p.botProblemNow(), `left as "villanet"`)
}
