package main

import (
	"context"
	"encoding/json"
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
		"shown":    func(c *configuration) { c.VoiceChannelsShown = "Gaming" },
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

// The rig's voice server, as a Mumble client is told of it: Test (the root),
// Lobby, Gaming and AFK under it, and Duo under Gaming, which takes two. AFK
// is placed last by its position, the others beside each other by name.
func voiceTreeClient() *gumble.Client {
	root := &gumble.Channel{ID: 0, Name: "Test"}
	lobby := &gumble.Channel{ID: 1, Name: "Lobby", Parent: root}
	gaming := &gumble.Channel{ID: 2, Name: "Gaming", Parent: root}
	afk := &gumble.Channel{ID: 3, Name: "AFK", Parent: root, Position: 10}
	duo := &gumble.Channel{ID: 4, Name: "Duo", Parent: gaming, MaxUsers: 2}

	return &gumble.Client{
		Channels: gumble.Channels{0: root, 1: lobby, 2: gaming, 3: afk, 4: duo},
		Users:    gumble.Users{},
	}
}

func parentOf(id uint32) *uint32 {
	return &id
}

// A client draws the tree from the list as it comes: a channel before the
// ones under it, channels beside each other by position and then by name.
func TestTheChannelTreeComesInTreeOrder(t *testing.T) {
	client := voiceTreeClient()

	require.Equal(t, []voiceChannel{
		{ID: 0, Name: "Test", Depth: 0},
		{ID: 2, Name: "Gaming", Parent: parentOf(0), Depth: 1},
		{ID: 4, Name: "Duo", Parent: parentOf(2), Depth: 2, Max: 2},
		{ID: 1, Name: "Lobby", Parent: parentOf(0), Depth: 1},
		{ID: 3, Name: "AFK", Parent: parentOf(0), Depth: 1},
	}, voiceChannelsOf(client))

	// A channel under one the tree does not hold is left out, with what is
	// under it.
	lost := &gumble.Channel{ID: 8, Name: "Gone"}
	client.Channels[9] = &gumble.Channel{ID: 9, Name: "Orphan", Parent: lost}
	client.Channels[10] = &gumble.Channel{ID: 10, Name: "Under the orphan", Parent: client.Channels[9]}
	require.Len(t, voiceChannelsOf(client), 5)

	// Nothing known yet is nothing, not an empty tree.
	require.Nil(t, voiceChannelsOf(&gumble.Client{}))
}

// The root is 0 and is written as 0; so is a parent that is the root. A
// limit is there only where a channel has one.
func TestChannelIdsAreAlwaysWritten(t *testing.T) {
	encoded, err := json.Marshal(voiceChannelsOf(voiceTreeClient())[:3])
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"id": 0, "name": "Test", "depth": 0},
		{"id": 2, "name": "Gaming", "parent": 0, "depth": 1},
		{"id": 4, "name": "Duo", "parent": 2, "depth": 2, "max": 2}
	]`, string(encoded))
}

// Each person says which channel they are in, and whether they hear anybody.
func TestTheRosterSaysWhereEverybodyIs(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", "victor").Return(&model.User{Id: "uid-victor", Username: "victor"}, nil)
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)

	client := voiceTreeClient()
	client.Users[3] = &gumble.User{Session: 3, Name: "Victor", Channel: client.Channels[1]}
	client.Users[5] = &gumble.User{Session: 5, Name: "Bob", Channel: client.Channels[4], SelfDeafened: true}
	client.Users[6] = &gumble.User{Session: 6, Name: "Carol", Channel: client.Channels[0], Deafened: true}
	client.Users[7] = &gumble.User{Session: 7, Name: "Erin"}
	client.Users[1] = &gumble.User{Session: 1, Name: "VillaNET", Channel: client.Channels[0]}

	require.Equal(t, []voicePerson{
		{Session: 5, Name: "Bob", Channel: 4, Muted: true, Deaf: true},
		{Session: 6, Name: "Carol", Muted: true, Deaf: true},
		{Session: 7, Name: "Erin"},
		{Session: 3, Name: "Victor", UserID: "uid-victor", Channel: 1},
	}, p.rosterOf(client, voiceConfig()), "a person whose channel is not known yet is in the root")
}

// A move, a deafen and a new channel are each something the panel shows.
func TestEveryNewFieldIsAVisibleChange(t *testing.T) {
	victor := voicePerson{Session: 3, Name: "Victor", UserID: "uid-victor", Channel: 1}

	for name, change := range map[string]func(*voicePerson){
		"moved":         func(p *voicePerson) { p.Channel = 2 },
		"deafened":      func(p *voicePerson) { p.Deaf = true },
		"through Matty": func(p *voicePerson) { p.Phone = true },
		"reconnected":   func(p *voicePerson) { p.Session = 4 },
	} {
		changed := victor
		change(&changed)
		require.False(t, sameRoster([]voicePerson{victor}, []voicePerson{changed}), name)
	}

	tree := voiceChannelsOf(voiceTreeClient())
	require.True(t, sameChannels(tree, voiceChannelsOf(voiceTreeClient())))
	require.True(t, sameChannels(nil, nil))

	for name, change := range map[string]func(*gumble.Client){
		"renamed":   func(c *gumble.Client) { c.Channels[1].Name = "Lounge" },
		"reordered": func(c *gumble.Client) { c.Channels[3].Position = -1 },
		"limited":   func(c *gumble.Client) { c.Channels[1].MaxUsers = 4 },
		"moved":     func(c *gumble.Client) { c.Channels[4].Parent = c.Channels[1] },
		"removed":   func(c *gumble.Client) { delete(c.Channels, 3) },
		"made":      func(c *gumble.Client) { c.Channels[5] = &gumble.Channel{ID: 5, Name: "Raid", Parent: c.Channels[0]} },
	} {
		client := voiceTreeClient()
		change(client)
		require.False(t, sameChannels(tree, voiceChannelsOf(client)), name)
	}

	// A description is not drawn, so it is not a change.
	described := voiceTreeClient()
	described.Channels[1].Description = "Say hello"
	require.True(t, sameChannels(tree, voiceChannelsOf(described)))
}

// A channel change alone republishes the roster, with the new tree on it.
func TestAChannelChangeIsPublished(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)
	p.setConfiguration(voiceConfig())
	p.rosterCh = make(chan struct{}, 1)

	client := voiceTreeClient()
	epoch := p.beginVoiceEpoch()
	p.publishVoice(client, p.getConfiguration(), epoch)
	<-p.rosterCh
	require.Len(t, p.voiceOf(p.getConfiguration()).Channels, 5)

	p.publishVoice(client, p.getConfiguration(), epoch)
	require.Empty(t, p.rosterCh, "nothing changed")

	client.Channels[1].Name = "Lounge"
	p.publishVoice(client, p.getConfiguration(), epoch)
	require.Len(t, p.rosterCh, 1, "a renamed channel is a change")
	require.Equal(t, "Lounge", p.voiceOf(p.getConfiguration()).Channels[3].Name)
}

// Somebody here through Matty is the account that joined, whatever the voice
// server calls them, and is shown so at once: a session that never moves,
// mutes or speaks sends no event that would rebuild the roster.
func TestAPhoneSessionShowsTheAccountThatJoined(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetUserByUsername", "dave").Return(&model.User{Id: "uid-dave", Username: "dave"}, nil)
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)
	p.setConfiguration(voiceConfig())
	p.rosterCh = make(chan struct{}, 1)

	client := voiceTreeClient()
	client.Users[12] = &gumble.User{Session: 12, Name: "dave (Matty)", Channel: client.Channels[1]}
	client.Users[13] = &gumble.User{Session: 13, Name: "Dave", Channel: client.Channels[2]}
	p.setVoiceClient(client)
	p.publishVoice(client, p.getConfiguration(), p.beginVoiceEpoch())

	people := func() []voicePerson { return p.voiceOf(p.getConfiguration()).People }
	require.Equal(t, []voicePerson{
		{Session: 13, Name: "Dave", UserID: "uid-dave", Channel: 2},
		{Session: 12, Name: "dave (Matty)", Channel: 1},
	}, people(), "unknown, the name rules match the name to nobody")

	remove := p.putVoicePhone(12, "uid-dave")
	require.Eventually(t, func() bool {
		return len(people()) == 2 && people()[1].UserID == "uid-dave" && people()[1].Phone
	}, 5*time.Second, 10*time.Millisecond)
	require.False(t, people()[0].Phone, "the desktop Dave is not here through Matty")
	require.Equal(t, map[string]string{"uid-dave": voiceOpen}, voiceByUser(p.voiceOf(p.getConfiguration())))

	remove()
	require.Eventually(t, func() bool {
		return len(people()) == 2 && people()[1].UserID == "" && !people()[1].Phone
	}, 5*time.Second, 10*time.Millisecond, "taken out, the session is a name again")
}

// A session that ends late takes out its own entry and never the one that
// has since been given the same number.
func TestThePhoneRegistryRemovesByIdentity(t *testing.T) {
	var registry voicePhoneRegistry

	_, ok := registry.userOf(12)
	require.False(t, ok, "empty and ready as it is")

	first := registry.put(12, "uid-dave")
	second := registry.put(12, "uid-bob")

	require.False(t, first(), "the first entry was replaced")
	userID, ok := registry.userOf(12)
	require.True(t, ok)
	require.Equal(t, "uid-bob", userID)
	require.Equal(t, 1, registry.count())

	require.True(t, second())
	require.False(t, second(), "once only")
	_, ok = registry.userOf(12)
	require.False(t, ok)
	require.Zero(t, registry.count())
}

// /access says whether a Matty may talk while voice is on, and nothing about
// voice while it is off.
func TestAccessSaysWhetherTalkIsOn(t *testing.T) {
	s := newAccessScene(t)

	var off map[string]any
	require.NoError(t, json.Unmarshal(s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil).Body.Bytes(), &off))
	require.NotContains(t, off, "voice", "voice switched off says nothing of it")

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableVoice, cfg.MumbleServer = true, "192.168.1.20"
	s.p.setConfiguration(cfg)

	var on struct {
		Voice *voiceWord `json:"voice"`
	}
	require.NoError(t, json.Unmarshal(s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil).Body.Bytes(), &on))
	require.Equal(t, &voiceWord{Talk: "off", Reason: "Talking from Matty is switched off on this server."}, on.Voice)
}
