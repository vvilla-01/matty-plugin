package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// The server's name for the plugin (the Name setting, brandName) is what
// people read; the product word, productName, is what the log, the System
// Console and every outside service are told.

const acmeName = "Acme TV"

// nameServer gives p the Name setting acmeName.
func nameServer(p *Plugin) {
	cfg := p.getConfiguration().Clone()
	cfg.Name = acmeName
	p.setConfiguration(cfg)
}

func TestTheNameIsMattyUntilOneIsSet(t *testing.T) {
	p := &Plugin{}
	require.Equal(t, "Matty", p.brandName(), "before the first configuration")

	p.setConfiguration(&configuration{})
	require.Equal(t, "Matty", p.brandName(), "blank")

	nameServer(p)
	require.Equal(t, acmeName, p.brandName())
}

func TestAServersNameIsWhatPeopleRead(t *testing.T) {
	t.Run("the locked refusal, the store's, and the log", func(t *testing.T) {
		s := newAccessScene(t)
		nameServer(s.p)

		passed, recorder := s.gate(http.MethodGet, "/api/v1/me/profile", sceneLocked, nil)
		require.False(t, passed)
		requireRefused(t, recorder, http.StatusForbidden, accessCodeLocked, "locked")
		require.Equal(t, "Acme TV is locked. Unlock it with your bearer token.", codedError(t, recorder).Error)
		require.Equal(t, accessLockedText(acmeName), codedError(t, recorder).Error)

		s.faults.failRead(kvLinkByUserPrefix + sceneLinked)
		passed, recorder = s.gate(http.MethodGet, "/api/v1/me/profile", sceneLinked, nil)
		require.False(t, passed)
		require.Equal(t, "Acme TV could not check your access just now. Try again in a moment.", codedError(t, recorder).Error)
		require.NotEmpty(t, logged(s.api, "LogWarn", "Could not read somebody's access to Matty"),
			"the log names the software, not the server's name for it")
		s.faults.healRead(kvLinkByUserPrefix + sceneLinked)

		me := meOf(t, s.p, sceneLocked)
		require.Equal(t, acmeName, me["name"])
	})

	t.Run("the token refusal", func(t *testing.T) {
		s := newAccessScene(t)
		nameServer(s.p)

		_, refusal := s.p.MessageWillBePosted(nil, &model.Post{UserId: sceneLocked, ChannelId: "channel", Message: dansToken})
		require.Equal(t, "That looks like your bearer token. Paste it into the bar in the Acme TV panel instead.", refusal)
	})

	t.Run("a shared clip and a shared plan", func(t *testing.T) {
		w, srv, planID := planShareWorld(t)
		nameServer(w.p)

		decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
		require.True(t, strings.HasPrefix(srv.sent[0].Message, "🎬 Acme TV clip · "), srv.sent[0].Message)
		require.Contains(t, srv.sent[0].Message, "\nWatch it: https://chat.example.com/plug/ceo.villa.matty/clip/"+clipA)

		decodePlanShare(t, w.sharePlan(t, "uid-bob", planID, townSquare), http.StatusCreated)
		require.Equal(t, "📅 Acme TV plan\nSee it: https://chat.example.com/plug/ceo.villa.matty/plan/"+planID, srv.sent[1].Message)
	})

	t.Run("a page's title and words", func(t *testing.T) {
		s := newAccountsScene(t)
		nameServer(s.p)

		response := s.connectWith(steamProvider, acDave, web(acDave), strings.Repeat("0", 64), nil)
		requirePage(t, response, http.StatusBadRequest, "Start from Acme TV")
		body := response.Body.String()
		require.Contains(t, body, "<title>Acme TV · Start from Acme TV</title>")
		require.Contains(t, body, "Connect Steam in the Acme TV panel")
		require.NotContains(t, body, "Matty")
		require.NotContains(t, strings.ToLower(body), "#c8102e", "the border is the page's own grey")
	})

	t.Run("a Twitch page", func(t *testing.T) {
		s := newTwitchScene(t)
		nameServer(s.p)

		response := s.connectWith(twDave, web(twDave), strings.Repeat("0", 64), nil)
		requirePage(t, response, http.StatusBadRequest, "Start from Acme TV")
		require.Contains(t, response.Body.String(), "<title>Acme TV · Start from Acme TV</title>")
		require.Contains(t, response.Body.String(), "Connect Twitch in the Acme TV panel")
		require.NotContains(t, strings.ToLower(response.Body.String()), "#c8102e")
	})

	t.Run("outside services are told the software", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/search/multi", map[string]any{"page": 1, "results": []map[string]any{}})

		cfg := movieConfig()
		cfg.Name = acmeName
		p, _ := mediaPlugin(t, cfg)
		require.Equal(t, acmeName, p.brandName())
		mustSearch(t, p, "movie", "heat")

		asked := fake.requests()
		require.Len(t, asked, 1)
		require.Equal(t, "MattyPlugin/"+manifestVersion+" (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)", asked[0].agent)
		require.NotContains(t, asked[0].agent, "Acme")
	})
}
