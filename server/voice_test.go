package main

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
)

// The roster is a line of names at the top of the panel, so what is NOT in it
// matters as much as what is: the chat bridge and the plugin's own connection
// are furniture, and a row that permanently listed them would never do the one
// thing it is for, which is to disappear when voice is empty.

func voiceConfig() *configuration {
	cfg := &configuration{
		EnableVoice:         true,
		MumbleServer:        "192.168.1.20",
		MumbleUsername:      "VillaNET",
		VoiceHiddenNames:    "Mattermost",
		VoiceMatchUsernames: true,
	}
	cfg.normalize()

	return cfg
}

func TestTheBridgeAndThePluginAreNotPeople(t *testing.T) {
	cfg := voiceConfig()
	hidden := cfg.hiddenVoiceNames()

	require.True(t, hidden["mattermost"], "the chat bridge sits in Mumble under this name")
	require.False(t, hidden["victor"])

	// And the plugin's own connection is hidden by the name it dials under,
	// which an admin can rename without having to remember to hide it too.
	require.Equal(t, "VillaNET", cfg.MumbleUsername)
}

func TestHiddenNamesAreCaseAndSpaceInsensitive(t *testing.T) {
	cfg := &configuration{VoiceHiddenNames: " Mattermost , music-bot\nRadio "}
	cfg.normalize()

	hidden := cfg.hiddenVoiceNames()
	require.True(t, hidden["mattermost"])
	require.True(t, hidden["music-bot"])
	require.True(t, hidden["radio"])
	require.Len(t, hidden, 3, "blank entries are not names")
}

// A bare host is what an admin will type.
func TestTheMumblePortDefaults(t *testing.T) {
	require.Equal(t, "192.168.1.20:64738", withDefaultPort("192.168.1.20", "64738"))
	require.Equal(t, "192.168.1.20:9999", withDefaultPort("192.168.1.20:9999", "64738"))
	require.Equal(t, "mumble.example.com:64738", withDefaultPort("  mumble.example.com  ", "64738"))
	require.Equal(t, "[::1]:64738", withDefaultPort("::1", "64738"))
}

// The match exists so the panel can say "you". It is never used for access.
func TestMumbleNamesMatchMattermostAccounts(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", "victor").Return(&model.User{Id: "uid-victor", Username: "victor"}, nil)
	api.On("GetUserByUsername", "dave").Return(&model.User{Id: "uid-dave", Username: "dave"}, nil)
	api.On("GetUserByUsername", "ghost").Return(&model.User{Id: "uid-ghost", DeleteAt: 12345}, nil)
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)

	cfg := voiceConfig()
	cfg.VoiceNameMap = "Dave_ = dave"
	overrides := cfg.voiceNameMap()

	require.Equal(t, "uid-victor", p.voiceUserID("Victor", overrides, cfg), "usernames match case-insensitively")
	require.Equal(t, "uid-dave", p.voiceUserID("Dave_", overrides, cfg), "and the override map covers the rest")
	require.Empty(t, p.voiceUserID("bagelboy", overrides, cfg), "somebody with no account is still listed, just unmatched")
	require.Empty(t, p.voiceUserID("ghost", overrides, cfg), "a deactivated account is not a match")

	// With matching switched off, only the explicit map is honoured.
	off := voiceConfig()
	off.VoiceMatchUsernames = false
	require.Empty(t, p.voiceUserID("Victor", overrides, off))
	require.Equal(t, "uid-dave", p.voiceUserID("Dave_", overrides, off))
}

// An empty list only ever means "nobody is in voice", and that is only true
// when the plugin is connected and in a position to know.
func TestAnUnreachableServerIsNotAnEmptyRoom(t *testing.T) {
	p := &Plugin{}
	cfg := voiceConfig()

	p.setVoice(voiceState{problem: "could not connect: dial tcp: refused"})
	view := p.voiceOf(cfg)
	require.NotNil(t, view)
	require.False(t, view.Connected, "the panel is told nothing rather than told it is empty")
	require.Empty(t, view.People)

	p.setVoice(voiceState{connected: true})
	view = p.voiceOf(cfg)
	require.True(t, view.Connected, "connected with nobody in there is a different thing")
	require.Empty(t, view.People)

	// Switched off, the field leaves the payload entirely.
	off := voiceConfig()
	off.EnableVoice = false
	require.Nil(t, p.voiceOf(off))
}

// Mumble is chatty about things the panel does not show. Republishing on every
// one of them would poll Broadcast Box and push to every client per fidget.
func TestOnlyAVisibleChangeIsWorthPublishing(t *testing.T) {
	victor := voicePerson{Name: "Victor", UserID: "uid-victor"}
	dave := voicePerson{Name: "Dave"}

	require.True(t, sameRoster(nil, nil))
	require.True(t, sameRoster([]voicePerson{victor, dave}, []voicePerson{victor, dave}))
	require.False(t, sameRoster([]voicePerson{victor}, []voicePerson{victor, dave}))
	require.False(t, sameRoster([]voicePerson{victor, dave}, []voicePerson{dave, victor}), "order is part of it")

	muted := victor
	muted.Muted = true
	require.False(t, sameRoster([]voicePerson{victor}, []voicePerson{muted}), "muted is shown, so it counts")
}

// The roster rides every publish path, including the ones where Broadcast Box
// could not be reached: standing in voice has nothing to do with the television
// server being up.
func TestVoiceSurvivesAnOriginOutage(t *testing.T) {
	newMediaFake(t)
	g := newGoldenScene(t, voiceConfig())
	g.p.setVoice(voiceState{connected: true, people: []voicePerson{{Name: "Victor"}}})

	for path, url := range map[string]string{
		"answering":          g.origin.URL,
		"unreachable":        "http://127.0.0.1:1",
		"never named at all": "",
	} {
		cfg := g.p.getConfiguration().Clone()
		cfg.BroadcastBoxURL = url
		g.p.setConfiguration(cfg)

		g.p.pollOnce(t.Context(), true)

		voice := g.p.getSnapshot().Voice
		require.NotNil(t, voice, path)
		require.True(t, voice.Connected, path)
		require.Len(t, voice.People, 1, path)
		require.Equal(t, "Victor", voice.People[0].Name, path)
	}
}

// The connection is held, not polled, so it is only rebuilt when it would go
// somewhere else. Everything else an admin saves used to drop it.
func TestOnlyMumbleSettingsRebuildTheConnection(t *testing.T) {
	base := voiceConfig()

	same := voiceConfig()
	same.BannerText = "Something else entirely"
	same.BirthdayHour = 17
	same.VoiceHiddenNames = "Mattermost, someone-else"
	require.True(t, sameMumbleConnection(base, same), "none of that changes where it dials")

	for name, changed := range map[string]func(*configuration){
		"a different server":   func(c *configuration) { c.MumbleServer = "10.0.0.9" },
		"a different name":     func(c *configuration) { c.MumbleUsername = "Something Else" },
		"a different password": func(c *configuration) { c.MumblePassword = "hunter2" },
		"switched off":         func(c *configuration) { c.EnableVoice = false; c.EnableChatBridge = false },
		"no server at all":     func(c *configuration) { c.MumbleServer = "" },
	} {
		next := voiceConfig()
		changed(next)
		require.False(t, sameMumbleConnection(base, next), name)
	}

	// The first configuration of all has no previous, and must connect.
	require.False(t, sameMumbleConnection(nil, base))
}

// The headphones put the roster on people's posts, so a change to how it is
// built has to be noticed on save — while a change that only touches the
// connection is the reconnect's business, not this.
func TestRosterRulesAreComparedSeparatelyFromTheConnection(t *testing.T) {
	base := &configuration{EnableVoice: true, VoiceMatchUsernames: true, VoiceNameMap: "bob = robert", VoiceHiddenNames: "Mattermost"}

	same := *base
	require.True(t, sameVoiceRoster(base, &same))

	for name, change := range map[string]func(c *configuration){
		"enabled":  func(c *configuration) { c.EnableVoice = false },
		"hidden":   func(c *configuration) { c.VoiceHiddenNames = "Mattermost, Music" },
		"name map": func(c *configuration) { c.VoiceNameMap = "bob = bobby" },
		"matching": func(c *configuration) { c.VoiceMatchUsernames = false },
	} {
		changed := *base
		change(&changed)
		require.False(t, sameVoiceRoster(base, &changed), name)
	}

	require.False(t, sameVoiceRoster(nil, base))
}

// A roster change is one field of the snapshot, and it used to cost a whole
// poll: Broadcast Box asked, every store read a poll makes, and while anybody
// was live the airtime tables written again. A self-mute is a change the panel
// shows, so somebody using mute as push-to-talk was a poll a press. Now the
// poller puts the new roster on the snapshot it last published, asking nothing
// of anybody, and a run of changes is one push.
func TestARosterChangeIsPublishedWithoutAPoll(t *testing.T) {
	newMediaFake(t)
	g := newGoldenScene(t, voiceConfig())
	g.p.refreshCh = make(chan struct{}, 1)
	g.p.rosterCh = make(chan struct{}, 1)

	var asked, written atomic.Int64
	transport := g.p.httpClient.Transport
	g.p.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		asked.Add(1)

		return transport.RoundTrip(r)
	})}
	g.faults.setOnWrite(func(string) { written.Add(1) })

	client := &gumble.Client{Users: gumble.Users{
		1: {Name: "Victor"},
		2: {Name: "Dave"},
		3: {Name: "VillaNET"},
	}}
	epoch := g.p.beginVoiceEpoch()
	g.p.publishVoice(client, g.p.getConfiguration(), epoch)

	// Before anything has been published there is nothing to put it on, and
	// a snapshot made from nothing would tell every panel nothing is live.
	g.p.publishRosterNow()
	require.Nil(t, g.p.snapshot)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go g.p.runPoller(ctx, done, time.Hour)
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)

	// The first poll carries the roster like everything else.
	require.Eventually(t, func() bool {
		voice := g.p.getSnapshot().Voice
		return voice != nil && len(voice.People) == 2
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, int64(1), asked.Load())
	reads, writes := len(g.readKeys()), written.Load()

	// Victor mutes and unmutes, ten times over, and ends muted.
	for i := range 11 {
		client.Users[1].SelfMuted = i%2 == 0
		g.p.publishVoice(client, g.p.getConfiguration(), epoch)
	}

	require.Eventually(t, func() bool {
		people := g.p.getSnapshot().Voice.People
		return len(people) == 2 && people[1].Name == "Victor" && people[1].Muted
	}, 5*time.Second, 10*time.Millisecond, "every panel is told")

	stop()

	require.Equal(t, int64(1), asked.Load(), "Broadcast Box was not asked again")
	require.Len(t, g.readKeys(), reads, "nor was the store read")
	require.Equal(t, writes, written.Load(), "nor written")

	pushes := 0
	for _, call := range g.api.Calls {
		if call.Method == "PublishWebSocketEvent" && call.Arguments.Get(0) == wsEventStreams {
			pushes++
		}
	}
	// One for the first poll, and one for the eleven changes, or two if they
	// happened to straddle a boundary.
	require.LessOrEqual(t, pushes, 3)
}

// Somebody Mumble will not let speak in their channel is as silent as
// somebody muted, so their headphones turn slate too, and the soundboard's
// grey tiles match what the server will refuse (design/3.2.0.md §7.15).
func TestRosterMutedIncludesSuppressed(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)

	client := &gumble.Client{Users: gumble.Users{
		1: {Name: "Victor", Suppressed: true},
		2: {Name: "Dave"},
	}}

	people := p.rosterOf(client, voiceConfig())
	require.Len(t, people, 2)
	require.Equal(t, "Dave", people[0].Name)
	require.False(t, people[0].Muted)
	require.Equal(t, "Victor", people[1].Name)
	require.True(t, people[1].Muted, "not allowed to speak is shown as muted")
}

// A name that only looks like somebody's matches nobody, for the headphones
// and the soundboard alike: Go lowers a Turkish dotted İ to i and the Kelvin
// sign to k, which Murmur's own clash test does not, so such a name can sit in
// Mumble beside the real one (§7.3).
func TestLookalikeNamesMatchNobody(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", "victor").Return(&model.User{Id: "uid-victor", Username: "victor"}, nil)
	api.On("GetUserByUsername", "frank").Return(&model.User{Id: "uid-frank", Username: "frank"}, nil)
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)

	cfg := voiceConfig()
	overrides := cfg.voiceNameMap()

	require.Equal(t, "uid-victor", p.voiceUserID("Victor", overrides, cfg), "the real name, in any case, still matches")
	require.Equal(t, "uid-frank", p.voiceUserID("FRANK", overrides, cfg))

	for _, lookalike := range []string{
		"vİctor",                // a Turkish dotted capital I
		"franK",                 // the Kelvin sign for a K
		"ｖictor",                // a fullwidth v
		"victor​",               // a trailing zero-width space
		"vi̇ctor",               // a combining dot above
		"victor ",               // a trailing space Mumble keeps
		"Victor\u0000",          // a NUL
		strings.Repeat("v", 65), // longer than any username
	} {
		require.Empty(t, p.voiceUserID(lookalike, overrides, cfg), "%q shows no headphones", lookalike)
		require.False(t, voiceNameMatches(lookalike, "victor", overrides, cfg), "%q plays no sound as Victor", lookalike)
		require.False(t, voiceNameMatches(lookalike, "frank", overrides, cfg), "%q plays no sound as Frank", lookalike)
	}

	require.True(t, voiceNameMatches("Victor", "victor", overrides, cfg))
	require.False(t, voiceNameMatches("VillaNET", "villanet", overrides, cfg), "VillaNET's own name is nobody")
	require.False(t, voiceNameMatches("Mattermost", "mattermost", overrides, cfg), "nor is a hidden one")
}

// Names that differ are compared folded ASCII-only: the admin's Mumble name
// matches in any case of its ASCII letters, and every other character must be
// exactly the one they typed.
func TestNamesThatDifferFoldOnlyASCII(t *testing.T) {
	cfg := voiceConfig()
	cfg.VoiceNameMap = "Dave_ = dave\nÈve = eve\nKelvin = @kelvin\n# Victor = erin"
	overrides := cfg.voiceNameMap()

	require.True(t, voiceNameMatches("Dave_", "dave", overrides, cfg))
	require.True(t, voiceNameMatches("DAVE_", "dave", overrides, cfg), "ASCII letters in any case")
	require.True(t, voiceNameMatches("Ève", "eve", overrides, cfg), "the accented letter exactly as typed")
	require.False(t, voiceNameMatches("ève", "eve", overrides, cfg), "a non-ASCII letter is never folded")
	require.False(t, voiceNameMatches("Kelvin", "kelvin", overrides, cfg), "the Kelvin sign is not a K")
	require.True(t, voiceNameMatches("kelvin", "kelvin", overrides, cfg), "a leading @ on the username is a username")
	require.False(t, voiceNameMatches("Victor", "erin", overrides, cfg), "a line commented out maps nobody")

	// A name the map does not know falls back to the username rule, unless
	// that is switched off.
	require.True(t, voiceNameMatches("Bob", "bob", overrides, cfg))
	off := voiceConfig()
	off.VoiceMatchUsernames = false
	require.False(t, voiceNameMatches("Bob", "bob", off.voiceNameMap(), off))
	require.True(t, voiceNameMatches("Dave_", "dave", overrides, off))
}
