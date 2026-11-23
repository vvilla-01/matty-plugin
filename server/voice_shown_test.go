package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
)

// Channels to show: an admin lists the Mumble channels Matty and the panel
// are told of, and the people in any other channel are not in voice as far
// as either is told.

// A line is a channel's name or its path from below the root, without regard
// to case; a channel listed brings the channels under it; a shown channel
// whose parent is not shown is a top of its own, its depth counted from
// there; and the lines that name nothing are said.
func TestTheChannelsShownAreTheOnesListedAndUnder(t *testing.T) {
	tree := voiceChannelsOf(voiceTreeClient())
	shown := func(setting string) ([]voiceChannel, []string) {
		cfg := &configuration{VoiceChannelsShown: setting}
		channels, card := shownVoiceChannels(tree, cfg.voiceChannelsShown())

		return channels, card.unmatched
	}

	channels, unmatched := shown("")
	assert.Equal(t, tree, channels, "blank: every channel")
	assert.Nil(t, unmatched)

	channels, unmatched = shown(" TEST \n\n")
	assert.Equal(t, tree, channels, "the root by its own name, and everything under it")
	assert.Nil(t, unmatched)

	channels, unmatched = shown("gaming")
	assert.Equal(t, []voiceChannel{
		{ID: 2, Name: "Gaming", Depth: 0},
		{ID: 4, Name: "Duo", Parent: parentOf(2), Depth: 1, Max: 2},
	}, channels)
	assert.Nil(t, unmatched)

	channels, unmatched = shown("Lobby\n gaming / DUO \nAFK/Raid\nTest/Gaming\nlobby")
	assert.Equal(t, []voiceChannel{
		{ID: 4, Name: "Duo", Depth: 0, Max: 2},
		{ID: 1, Name: "Lobby", Depth: 0},
	}, channels, "a path from below the root, in the tree's order")
	assert.Equal(t, []string{"AFK/Raid", "Test/Gaming"}, unmatched, "as written, a repeat said once")

	channels, unmatched = shown("Nowhere")
	assert.Empty(t, channels)
	assert.Equal(t, []string{"Nowhere"}, unmatched)
}

// The edges of the rule: a name matches every channel so named, anywhere,
// and the card says so, naming each as a line would name it alone; a path
// matches its channel alone, a "/" at the start making a single name a path
// and one at the end left out; a path never names the root; a channel whose
// own name holds "/" is named by no line, and is shown only through a
// listed channel above it; case is folded as Unicode folds it, name by
// name.
func TestALineIsANameOrAPathAndOnlyThat(t *testing.T) {
	tree := []voiceChannel{
		{ID: 0, Name: "Root"},
		{ID: 1, Name: "Lobby", Parent: parentOf(0), Depth: 1},
		{ID: 2, Name: "Gaming", Parent: parentOf(0), Depth: 1},
		{ID: 3, Name: "Duo", Parent: parentOf(2), Depth: 2},
		{ID: 4, Name: "Staff", Parent: parentOf(0), Depth: 1},
		{ID: 5, Name: "Lobby", Parent: parentOf(4), Depth: 2},
		{ID: 6, Name: "Gaming/Duo", Parent: parentOf(4), Depth: 2},
		{ID: 7, Name: "ΟΔΟΣ", Parent: parentOf(0), Depth: 1},
		{ID: 8, Name: "Root", Parent: parentOf(4), Depth: 2},
	}
	shown := func(setting string) ([]uint32, voiceShownCard) {
		cfg := &configuration{VoiceChannelsShown: setting}
		channels, card := shownVoiceChannels(tree, cfg.voiceChannelsShown())

		var ids []uint32
		for _, channel := range channels {
			ids = append(ids, channel.ID)
		}

		return ids, card
	}

	ids, card := shown("lobby")
	assert.Equal(t, []uint32{1, 5}, ids, "a name: every channel so named")
	assert.Equal(t, []voiceSeveral{{Line: "lobby", Paths: []string{"/Lobby", "Staff/Lobby"}}}, card.several, "and the card says which")
	assert.Nil(t, card.unmatched)

	ids, card = shown("/Lobby")
	assert.Equal(t, []uint32{1}, ids, "a leading slash: the Lobby right under the root alone")
	assert.Nil(t, card.several)

	ids, _ = shown("Staff/Lobby")
	assert.Equal(t, []uint32{5}, ids)

	ids, _ = shown("Gaming/Duo")
	assert.Equal(t, []uint32{3}, ids, "a path, not the channel whose name is that text")

	for _, line := range []string{"Staff/Gaming/Duo", "/Staff/Gaming/Duo"} {
		ids, card = shown(line)
		assert.Empty(t, ids, line)
		assert.Equal(t, []string{line}, card.unmatched, "no line names a channel whose own name holds a slash")
	}
	ids, _ = shown("Staff")
	assert.Equal(t, []uint32{4, 5, 6, 8}, ids, "it is shown through the channel above it")

	ids, card = shown("Gaming/Duo/\n /Gaming \nGaming/")
	assert.Equal(t, []uint32{2, 3}, ids, "a slash at the end is left out")
	assert.Nil(t, card.unmatched)

	ids, card = shown("Root")
	assert.Len(t, ids, len(tree), "the root by its own name brings everything")
	assert.Equal(t, []voiceSeveral{{Line: "Root", Paths: []string{"Root (the root)", "Staff/Root"}}}, card.several)

	ids, card = shown("Root/Lobby\n/\nGaming//Duo\nοδος")
	assert.Equal(t, []uint32{7}, ids, "final sigma folds to sigma")
	assert.Equal(t, []string{"Root/Lobby", "/", "Gaming//Duo"}, card.unmatched, "a path never names the root, and an empty name names nothing")

	// The root and a channel right under it of the same name are told
	// apart on the card.
	_, card = shownVoiceChannels([]voiceChannel{{ID: 0, Name: "Lobby"}, {ID: 1, Name: "Lobby", Parent: parentOf(0), Depth: 1}}, []string{"lobby"})
	assert.Equal(t, []voiceSeveral{{Line: "lobby", Paths: []string{"Lobby (the root)", "/Lobby"}}}, card.several)
}

// The card names at most five of the channels a line matches, and counts the
// rest. Of Channels to show at most 200 lines are used, and a line longer
// than 256 characters is not used at all: the card names it by its first 40
// characters, and counts the lines after the 200th.
func TestChannelsToShowIsBounded(t *testing.T) {
	tree := []voiceChannel{{ID: 0, Name: "Root"}}
	for id := uint32(1); id <= 7; id++ {
		parent := id - 1
		tree = append(tree, voiceChannel{ID: id, Name: "Room", Parent: &parent, Depth: int(id)})
	}
	_, card := shownVoiceChannels(tree, []string{"room"})
	assert.Equal(t, []voiceSeveral{{Line: "room", Paths: []string{"/Room", "Room/Room", "Room/Room/Room", "Room/Room/Room/Room", "Room/Room/Room/Room/Room"}, More: 2}}, card.several)

	var setting strings.Builder
	for i := range 250 {
		fmt.Fprintf(&setting, "Channel %d\n", i)
	}
	read := (&configuration{VoiceChannelsShown: setting.String()}).voiceShownSetting()
	require.Len(t, read.lines, 200)
	assert.Equal(t, "Channel 199", read.lines[199])
	assert.Equal(t, 50, read.dropped)
	assert.Nil(t, read.tooLong)

	// Line 201 names the Lobby, which is not shown, and the card says that
	// line and the rest are not used.
	lobby := []voiceChannel{{ID: 0, Name: "Root"}, {ID: 1, Name: "Lobby", Parent: parentOf(0), Depth: 1}}
	setting.Reset()
	for i := range 200 {
		fmt.Fprintf(&setting, "Typo %d\n", i)
	}
	setting.WriteString("Lobby\n")
	read = (&configuration{VoiceChannelsShown: setting.String()}).voiceShownSetting()
	channels, card := read.shown(lobby)
	assert.Empty(t, channels, "line 201 is not used")
	assert.Len(t, card.unmatched, 200)
	assert.NotContains(t, card.unmatched, "Lobby")
	assert.Equal(t, 1, read.dropped)

	// A path longer than 256 characters is not used, so it does not name the
	// channel above the one it names, whose whole branch would be shown.
	a, b := strings.Repeat("a", 200), strings.Repeat("b", 55)
	deep := []voiceChannel{
		{ID: 0, Name: "Root"},
		{ID: 1, Name: a, Parent: parentOf(0), Depth: 1},
		{ID: 2, Name: b, Parent: parentOf(1), Depth: 2},
		{ID: 3, Name: "Duo", Parent: parentOf(2), Depth: 3},
		{ID: 4, Name: "Staff", Parent: parentOf(2), Depth: 3},
	}
	long := a + "/" + b + "/Duo"
	require.Equal(t, 260, len(long))
	read = (&configuration{VoiceChannelsShown: long}).voiceShownSetting()
	assert.Nil(t, read.lines)
	assert.Equal(t, []string{strings.Repeat("a", 40) + "…"}, read.tooLong)
	channels, card = read.shown(deep)
	assert.Empty(t, channels, "nothing is shown, as for a line that names nothing")
	assert.Nil(t, card.unmatched)

	// Beside a line that is used, the one that is too long is left out.
	read = (&configuration{VoiceChannelsShown: long + "\nDuo\n" + strings.Repeat("ΟΔΟΣ", 64) + "\n" + strings.Repeat("ΟΔΟΣ", 65)}).voiceShownSetting()
	assert.Equal(t, []string{"Duo", strings.Repeat("ΟΔΟΣ", 64)}, read.lines, "256 characters is not too long")
	assert.Equal(t, []string{strings.Repeat("a", 40) + "…", strings.Repeat("ΟΔΟΣ", 10) + "…"}, read.tooLong)
	channels, _ = read.shown(deep)
	assert.Equal(t, []voiceChannel{{ID: 3, Name: "Duo", Depth: 0}}, channels, "only Duo was listed")

	// No line at all: every channel.
	channels, card = (&configuration{VoiceChannelsShown: " \n"}).voiceShownSetting().shown(deep)
	assert.Equal(t, deep, channels)
	assert.Equal(t, voiceShownCard{}, card)
}

// What is published holds only the shown channels and the people in them:
// the roster, the people index (the headphones) and the card's lines that
// name nothing. The fixture is the contract's filtered tree.
func TestPeopleInAChannelNotShownAreNotInVoice(t *testing.T) {
	api := &plugintest.API{}
	for _, name := range []string{"victor", "bob", "carol", "erin"} {
		api.On("GetUserByUsername", name).Return(&model.User{Id: "u-" + name, Username: name}, nil)
	}
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})

	p := &Plugin{}
	p.SetAPI(api)
	cfg := voiceConfig()
	cfg.VoiceChannelsShown = "Gaming\nLobby\nLoby"
	p.setConfiguration(cfg)
	p.rosterCh = make(chan struct{}, 1)

	client := voiceTreeClient()
	client.Users[3] = &gumble.User{Session: 3, Name: "Victor", Channel: client.Channels[1]}
	client.Users[5] = &gumble.User{Session: 5, Name: "Bob", Channel: client.Channels[4], SelfDeafened: true}
	client.Users[6] = &gumble.User{Session: 6, Name: "Carol", Channel: client.Channels[3]}
	client.Users[7] = &gumble.User{Session: 7, Name: "Erin"}
	client.Users[12] = &gumble.User{Session: 12, Name: "dave (Matty)", Channel: client.Channels[2]}
	p.voicePhones.put(12, "u-dave")

	epoch := p.beginVoiceEpoch()
	p.publishVoice(client, p.getConfiguration(), epoch)
	<-p.rosterCh

	voice := p.voiceOf(p.getConfiguration())
	require.Equal(t, []voiceChannel{
		{ID: 2, Name: "Gaming", Depth: 0},
		{ID: 4, Name: "Duo", Parent: parentOf(2), Depth: 1, Max: 2},
		{ID: 1, Name: "Lobby", Depth: 0},
	}, voice.Channels)
	require.Equal(t, []voicePerson{
		{Session: 5, Name: "Bob", UserID: "u-bob", Channel: 4, Muted: true, Deaf: true},
		{Session: 12, Name: "dave (Matty)", UserID: "u-dave", Channel: 2, Phone: true},
		{Session: 3, Name: "Victor", UserID: "u-victor", Channel: 1},
	}, voice.People, "Carol in AFK and Erin in the root are left out")

	people := peopleIndex(nil, voice, nil)
	assert.Equal(t, map[string]personView{
		"u-bob":    {Voice: voiceMuted},
		"u-dave":   {Voice: voiceOpen},
		"u-victor": {Voice: voiceOpen},
	}, people, "no headphones for anybody in a channel not shown")
	assert.Equal(t, []string{"Loby"}, p.getVoice().unmatched)
	assert.Nil(t, p.getVoice().several)

	encoded, err := json.Marshal(struct {
		Voice  *voiceView            `json:"voice"`
		People map[string]personView `json:"people"`
	}{voice, people})
	require.NoError(t, err)
	contractFixture(t, "voice-shown.json", encoded)

	// The line put right: the tree is the same, so nothing is pushed, and
	// the card has nothing more to say.
	cfg = cfg.Clone()
	cfg.VoiceChannelsShown = "Gaming\nLobby"
	p.setConfiguration(cfg)
	p.publishVoice(client, p.getConfiguration(), epoch)
	assert.Empty(t, p.rosterCh, "nothing anybody sees changed")
	assert.Nil(t, p.getVoice().unmatched)

	// Carol's channel listed too: she is in voice again.
	cfg = cfg.Clone()
	cfg.VoiceChannelsShown = "Gaming\nLobby\nafk"
	p.setConfiguration(cfg)
	p.publishVoice(client, p.getConfiguration(), epoch)
	require.Len(t, p.rosterCh, 1)
	assert.Len(t, p.voiceOf(p.getConfiguration()).People, 4)
}

// A join or a move to a channel that is not shown is refused as a channel
// that does not exist; a phone that a Mumble admin moves into one stays
// connected, told the channel it is in. The card names the lines that name
// no channel.
func TestAChannelNotShownCannotBeJoined(t *testing.T) {
	s := newVoiceScene(t)
	channels, _ := shownVoiceChannels(relayVoiceChannels(), []string{"Gaming"})
	several := []voiceSeveral{{Line: "Duo", Paths: []string{"Gaming/Duo", "AFK/Duo"}}}
	s.p.setVoice(voiceState{connected: true, channels: channels, unmatched: []string{"Gamng"}, several: several})

	offer := newTestVoicePhone(t, 4).offer()
	notShown := s.call("/api/v1/voice/join", sceneLinked, map[string]any{"channel": 1, "sdp": offer})
	requireRefused(t, notShown, http.StatusNotFound, codeNotFound, "Lobby is not shown")
	assert.Equal(t, voiceNoChannelText, codedError(t, notShown).Error)

	dave := newTestVoicePhone(t, 4)
	answer := s.join(dave, sceneLinked, 4, false)
	assert.EqualValues(t, 4, answer.Channel)

	moved := s.call("/api/v1/voice/move", sceneLinked, map[string]any{"channel": 3})
	requireRefused(t, moved, http.StatusNotFound, codeNotFound, "AFK is not shown")

	s.relay.moveOther(answer.Session, 1)
	require.Eventually(t, func() bool { return dave.self().Channel == 1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, s.p.voiceTalk().count(), "still connected")
	_, ended := dave.ended()
	assert.False(t, ended)

	// Joining again where the session sits (a new connection of the phone's
	// own), and a move to where it is, are not refused: nothing moves.
	again := newTestVoicePhone(t, 4)
	rejoined := s.join(again, sceneLinked, 1, false)
	assert.Equal(t, answer.Session, rejoined.Session, "the same session, taken over")
	assert.EqualValues(t, 1, rejoined.Channel)
	stays := s.call("/api/v1/voice/move", sceneLinked, map[string]any{"channel": 1})
	require.Equal(t, http.StatusOK, stays.Code, stays.Body.String())
	assert.Equal(t, 1, s.p.voiceTalk().count())

	card := s.serve(http.MethodGet, "/api/v1/admin/voice", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, card.Code, card.Body.String())
	var view voiceAdminView
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	assert.Equal(t, []string{"Gamng"}, view.Unmatched)
	assert.Equal(t, several, view.Several)
	assert.Nil(t, view.TooLong)
	assert.Zero(t, view.Dropped)

	// The lines that are not used, as the setting has them, connected or
	// not: one too long, and 2 after the first 200.
	cfg := s.p.getConfiguration().Clone()
	lines := []string{strings.Repeat("Gaming/", 37) + "Duo"}
	for i := range 201 {
		lines = append(lines, fmt.Sprintf("Room %d", i))
	}
	cfg.VoiceChannelsShown = strings.Join(lines, "\n")
	s.p.setConfiguration(cfg)
	s.p.setVoice(voiceState{})
	card = s.serve(http.MethodGet, "/api/v1/admin/voice", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, card.Code, card.Body.String())
	view = voiceAdminView{}
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	assert.False(t, view.Connected)
	assert.Equal(t, []string{"Gaming/Gaming/Gaming/Gaming/Gaming/Gamin…"}, view.TooLong)
	assert.Equal(t, 2, view.Dropped)
}
