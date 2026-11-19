package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
)

// The soundboard's board and routes (design/3.2.0.md §7.7-§7.9, §7.11): one
// shared board, seen, added to and renamed only by linked people in Mumble
// now; your own sounds' audio for you alone; deleting your own open to
// anybody, whatever the switches say.

// fakePermissions answers what VillaNET may do in each channel, as gumble's
// own would once Mumble had said.
type fakePermissions struct {
	lock    sync.Mutex
	whisper map[uint32]bool
	unknown map[uint32]bool
	asked   int
	onAsk   func(channel *gumble.Channel)
}

func newFakePermissions() *fakePermissions {
	return &fakePermissions{whisper: map[uint32]bool{}, unknown: map[uint32]bool{}}
}

func (f *fakePermissions) whisperIn(channel *gumble.Channel) (bool, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.unknown[channel.ID] {
		return false, false
	}
	if has, set := f.whisper[channel.ID]; set {
		return has, true
	}

	return true, true
}

func (f *fakePermissions) ask(channel *gumble.Channel) {
	f.lock.Lock()
	f.asked++
	onAsk := f.onAsk
	f.lock.Unlock()

	if onAsk != nil {
		onAsk(channel)
	}
}

func (f *fakePermissions) set(change func(f *fakePermissions)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	change(f)
}

// soundScene is the access scene with the soundboard on, and a Mumble
// connection whose state the test builds by hand: Dave (linked) and Bob (a
// linked admin) in Lounge, VillaNET parked in the root.
type soundScene struct {
	*accessScene
	client *gumble.Client
	root   *gumble.Channel
	lounge *gumble.Channel
	games  *gumble.Channel
	perms  *fakePermissions
}

func newSoundScene(t *testing.T) *soundScene {
	t.Helper()

	s := &soundScene{accessScene: newAccessScene(t), perms: newFakePermissions()}

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableVoice = true
	cfg.EnableSoundboard = true
	cfg.VoiceMatchUsernames = true
	cfg.MumbleServer = "192.0.2.20"
	cfg.MumbleUsername = "VillaNET"
	cfg.normalize()
	s.p.setConfiguration(cfg)

	s.root = &gumble.Channel{ID: 0, Name: "Root"}
	s.lounge = &gumble.Channel{ID: 7, Name: "Lounge"}
	s.games = &gumble.Channel{ID: 8, Name: "Games"}

	bot := &gumble.User{Session: 1, Name: "VillaNET", Channel: s.root}
	s.client = &gumble.Client{Self: bot, Users: gumble.Users{
		1: bot,
		2: {Session: 2, Name: "Dave", Channel: s.lounge},
		3: {Session: 3, Name: "bob", Channel: s.lounge},
	}}

	s.p.setVoiceClient(s.client)
	s.p.setVoice(voiceState{connected: true, people: []voicePerson{{Name: "Dave", UserID: sceneLinked}, {Name: "bob", UserID: sceneLinkedAdmin}}})

	st := s.p.sounds()
	st.lock.Lock()
	st.perms = s.perms
	st.whisperWait = 100 * time.Millisecond
	st.lock.Unlock()

	return s
}

// consoleHeader is what the panel and the System Console send with every
// request they make from script (madeByScript).
var consoleHeader = map[string]string{"X-Requested-With": "XMLHttpRequest"}

func (s *soundScene) user(session uint32) *gumble.User {
	return s.client.Users[session]
}

// soundBody is an upload as the panel makes it.
func soundBody(t *testing.T, name, emoji string, packets [][]byte) string {
	t.Helper()

	encoded, err := json.Marshal(map[string]any{
		"name":    name,
		"emoji":   map[string]string{"kind": "system", "name": emoji},
		"packets": base64.StdEncoding.EncodeToString(encodeSoundPackets(packets)),
	})
	require.NoError(t, err)

	return string(encoded)
}

func (s *soundScene) upload(t *testing.T, caller, name string) *httptest.ResponseRecorder {
	t.Helper()

	return s.serve(http.MethodPost, "/api/v1/sounds", caller, soundBody(t, name, "loud_sound", soundFixtures(t, "chromium")["tone1s"]), nil)
}

func (s *soundScene) added(t *testing.T, caller, name string) string {
	t.Helper()

	recorder := s.upload(t, caller, name)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var answer map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	require.Regexp(t, `^[0-9a-f]{16}$`, answer["id"])

	return answer["id"]
}

func (s *soundScene) list(t *testing.T, caller string) soundListResponse {
	t.Helper()

	recorder := s.serve(http.MethodGet, "/api/v1/sounds", caller, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	var answer soundListResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

	return answer
}

func (s *soundScene) board(t *testing.T) *soundsDoc {
	t.Helper()

	doc, ok := decodeSounds(s.kv(kvSounds))
	require.True(t, ok)

	return doc
}

// seedSounds puts sounds on the board directly, owned as given, each with a
// blob of the blip fixture.
func (s *soundScene) seedSounds(t *testing.T, owners ...string) []string {
	t.Helper()

	doc, ok := decodeSounds(s.kv(kvSounds))
	require.True(t, ok)

	var ids []string
	for i, owner := range owners {
		id := fmt.Sprintf("%016x", len(doc.Entries)+i+1)
		blob, _ := soundBlobOf(1, soundFixtures(t, "chromium")["blip"])
		s.setKV(kvSoundPrefix+id, blob)
		doc.Entries = append(doc.Entries, soundEntry{ID: id, Owner: owner, Name: "Seeded", Emoji: soundEmoji{Kind: "system", Name: "bell", Unified: "1f514"}, Ms: 260, Bytes: 1, At: 1})
		ids = append(ids, id)
	}

	encoded, err := json.Marshal(doc)
	require.NoError(t, err)
	s.setKV(kvSounds, encoded)
	require.NoError(t, s.p.readSounds(true))

	return ids
}

func TestSoundRoutesAndClasses(t *testing.T) {
	for path, class := range map[string]map[string]routeClass{
		"/api/v1/sounds":             {http.MethodGet: routeLinked, http.MethodPost: routeLinked},
		"/api/v1/sounds/edit":        {http.MethodPost: routeLinked},
		"/api/v1/sounds/remove":      {http.MethodPost: routeOpen},
		"/api/v1/sounds/remove-mine": {http.MethodPost: routeOpen},
		"/api/v1/sounds/play":        {http.MethodPost: routeLinked},
		"/api/v1/sounds/audio":       {http.MethodGet: routeLinked},
	} {
		require.Equal(t, class, routeClasses[path], path)
	}

	s := newSoundScene(t)
	id := s.seedSounds(t, sceneLetIn)[0]

	// Off, everything but taking your own back is 404 "off".
	off := s.p.getConfiguration().Clone()
	off.EnableSoundboard = false
	s.p.setConfiguration(off)

	script := map[string]string{"X-Requested-With": "XMLHttpRequest"}
	for _, call := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/sounds"},
		{http.MethodPost, "/api/v1/sounds"},
		{http.MethodPost, "/api/v1/sounds/edit?id=" + id},
		{http.MethodPost, "/api/v1/sounds/play?id=" + id},
		{http.MethodGet, "/api/v1/sounds/audio?id=" + id},
	} {
		requireRefused(t, s.serve(call.method, call.target, sceneLinked, `{"name":"x"}`, script), http.StatusNotFound, soundCodeOff, call.target)
	}

	// Let in or locked, the gate refuses the linked routes, and lets the
	// takebacks through: Erin deletes her own sound with the board off.
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/sounds", sceneLetIn, "", nil), http.StatusForbidden, accessCodeWatchOnly, "let in")
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/sounds", sceneLocked, "", nil), http.StatusForbidden, accessCodeLocked, "locked")

	recorder := s.serve(http.MethodPost, "/api/v1/sounds/remove?id="+id, sceneLetIn, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, s.board(t).Entries)
	require.Empty(t, s.kv(kvSoundPrefix+id), "the audio goes with it")
}

func TestTheBoardAddingAndEditingNeedMumble(t *testing.T) {
	s := newSoundScene(t)
	id := s.seedSounds(t, sceneLinked)[0]

	board := s.list(t, sceneLinked)
	require.True(t, board.Voice.In)
	require.Equal(t, "Lounge", board.Voice.Channel, "the channel, to the person in it")
	require.True(t, board.Voice.Can)
	require.Len(t, board.Sounds, 1)
	require.NotNil(t, board.Limits)
	require.Equal(t, 10000, int(board.Limits.CooldownMs))

	// Dave leaves Mumble.
	s.user(2).Name = "Somebody"

	board = s.list(t, sceneLinked)
	require.False(t, board.Voice.In)
	require.Equal(t, soundCodeNotInVoice, board.Voice.Why)
	require.Empty(t, board.Voice.Channel)
	require.Nil(t, board.Sounds, "no board for somebody not in Mumble")
	require.Nil(t, board.Limits)

	requireRefused(t, s.upload(t, sceneLinked, "Airhorn"), http.StatusForbidden, soundCodeNotInVoice, "add")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/edit?id="+id, sceneLinked, `{"name":"Renamed"}`, nil), http.StatusForbidden, soundCodeNotInVoice, "edit")

	// Back, muted: in Mumble in any state is in.
	s.user(2).Name = "dave"
	s.user(2).SelfMuted = true
	board = s.list(t, sceneLinked)
	require.True(t, board.Voice.In)
	require.False(t, board.Voice.Can)
	require.Equal(t, soundCodeMuted, board.Voice.Why)
	require.Len(t, board.Sounds, 1, "muted people still see the board, greyed")
	s.added(t, sceneLinked, "Airhorn")

	// Mumble not connected.
	s.p.setVoiceClient(nil)
	board = s.list(t, sceneLinked)
	require.Equal(t, soundCodeBotOffline, board.Voice.Why)
	require.Nil(t, board.Sounds)
	requireRefused(t, s.upload(t, sceneLinked, "Airhorn"), http.StatusServiceUnavailable, soundCodeBotOffline, "add, offline")
}

func TestAddingASound(t *testing.T) {
	s := newSoundScene(t)
	id := s.added(t, sceneLinked, "  Airhorn  ")

	doc := s.board(t)
	require.Len(t, doc.Entries, 1)
	entry := doc.Entries[0]
	require.Equal(t, id, entry.ID)
	require.Equal(t, sceneLinked, entry.Owner)
	require.Equal(t, "Airhorn", entry.Name)
	require.Equal(t, soundEmoji{Kind: "system", Name: "loud_sound", Unified: "1f50a"}, entry.Emoji, "the server's own lookup")
	require.EqualValues(t, 1020, entry.Ms)
	require.InDelta(t, -21, entry.Level, 2)

	blob, ok := readSoundBlob(s.kv(kvSoundPrefix + id))
	require.True(t, ok)
	require.Len(t, blob.packets, 51)
	require.Equal(t, entry.Bytes, len(blob.section))

	board := s.list(t, sceneLinked)
	require.Equal(t, []soundListItem{{ID: id, Name: "Airhorn", Emoji: entry.Emoji, Ms: 1020, Owner: "Dave", Mine: true}}, board.Sounds)
	require.Equal(t, maxSoundsPerPerson-1, board.Limits.Left)
	require.Equal(t, s.p.sounds().version(), board.V)

	other := s.list(t, sceneLinkedAdmin)
	require.False(t, other.Sounds[0].Mine)

	// The name: one line, sixteen at most, without the author's own key.
	for about, name := range map[string]string{
		"empty":     "   ",
		"seventeen": strings.Repeat("a", 17),
		"two lines": "Air\nhorn",
		"a control": "Air\u0007horn",
	} {
		requireRefused(t, s.upload(t, sceneLinked, name), http.StatusBadRequest, soundCodeBadName, about)
	}
	s.added(t, sceneLinked, "my videokey-dave")
	require.Equal(t, "my my stream", s.board(t).Entries[1].Name, "the stream key never reaches the board")

	// The emoji, by the server's own lookup: a name it does not know, or a
	// custom emoji with custom emoji off, is refused.
	for _, emoji := range []string{"not_an_emoji", ""} {
		body := soundBody(t, "Bell", emoji, soundFixtures(t, "chromium")["tone1s"])
		requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, body, nil), http.StatusBadRequest, soundCodeBadEmoji, emoji)
	}
	custom := strings.Replace(soundBody(t, "Bell", "party_parrot", soundFixtures(t, "chromium")["tone1s"]), `"system"`, `"custom"`, 1)
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, custom, nil), http.StatusBadRequest, soundCodeBadEmoji, "custom, off")

	// Strict JSON: nothing the server did not ask for.
	body := strings.Replace(soundBody(t, "Bell", "bell", soundFixtures(t, "chromium")["tone1s"]), `"name":"bell"`, `"name":"bell","unified":"1f4a9"`, 1)
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, body, nil), http.StatusBadRequest, soundCodeBadSound, "an extra field")

	// And the audio itself.
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, soundBody(t, "Loud", "bell", soundFixtures(t, "chromium")["loud"]), nil),
		http.StatusBadRequest, soundCodeTooLoud, "too loud")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, soundBody(t, "Quiet", "bell", soundFixtures(t, "chromium")["quiet"]), nil),
		http.StatusBadRequest, soundCodeSilent, "silent")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds", sceneLinked, soundBody(t, "Short", "bell", soundFixtures(t, "chromium")["tone1s"][:12]), nil),
		http.StatusBadRequest, soundCodeBadSound, "too short")
	require.Len(t, s.board(t).Entries, 2)
}

func TestSoundOwnerOnlyEditRemoveAndAudio(t *testing.T) {
	s := newSoundScene(t)
	id := s.added(t, sceneLinkedAdmin, "Drum")
	script := map[string]string{"X-Requested-With": "XMLHttpRequest"}

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/edit?id="+id, sceneLinked, `{"name":"Mine now"}`, nil), http.StatusForbidden, soundCodeNotYours, "edit")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/remove?id="+id, sceneLinked, "", nil), http.StatusForbidden, soundCodeNotYours, "remove")
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/sounds/audio?id="+id, sceneLinked, "", script), http.StatusForbidden, soundCodeNotYours, "audio")
	require.Equal(t, "Drum", s.board(t).Entries[0].Name)

	recorder := s.serve(http.MethodPost, "/api/v1/sounds/edit?id="+id, sceneLinkedAdmin, `{"name":"Drum roll","emoji":{"kind":"system","name":"drum_with_drumsticks"}}`, nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "Drum roll", s.board(t).Entries[0].Name)
	require.Equal(t, "1f941", s.board(t).Entries[0].Emoji.Unified)

	recorder = s.serve(http.MethodGet, "/api/v1/sounds/audio?id="+id, sceneLinkedAdmin, "", script)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	packets, ok := parseSoundPackets(recorder.Body.Bytes())
	require.True(t, ok, "the packets, as they were uploaded")
	require.Len(t, packets, 51)

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/edit?id=0123456789abcdef", sceneLinkedAdmin, `{"name":"x"}`, nil), http.StatusGone, soundCodeGone, "no such sound")
}

func TestRemoveMineIsOpenAndOwnOnly(t *testing.T) {
	s := newSoundScene(t)
	ids := s.seedSounds(t, sceneLetIn, sceneLinked, sceneLetIn, sceneLocked)

	off := s.p.getConfiguration().Clone()
	off.EnableSoundboard = false
	s.p.setConfiguration(off)

	remove := func(caller string) int {
		recorder := s.serve(http.MethodPost, "/api/v1/sounds/remove-mine", caller, "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

		var answer map[string]int
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

		return answer["deleted"]
	}

	require.Equal(t, 2, remove(sceneLetIn), "let in, with the soundboard off")
	require.Equal(t, 0, remove(sceneLetIn), "nothing left")
	require.Equal(t, 0, remove(sceneLockedAdmin), "nothing of theirs")

	left := []string{}
	for _, entry := range s.board(t).Entries {
		left = append(left, entry.ID)
	}
	require.Equal(t, []string{ids[1], ids[3]}, left, "only the caller's own")
	require.Empty(t, s.kv(kvSoundPrefix+ids[0]))
	require.NotEmpty(t, s.kv(kvSoundPrefix+ids[1]))

	require.Equal(t, 1, remove(sceneLocked), "locked, too")
}

func TestSoundBounds(t *testing.T) {
	s := newSoundScene(t)

	mine := make([]string, maxSoundsPerPerson)
	for i := range mine {
		mine[i] = sceneLinked
	}
	s.seedSounds(t, mine...)
	requireRefused(t, s.upload(t, sceneLinked, "Seventeenth"), http.StatusConflict, soundCodeYoursFull, "sixteen a person")
	require.Zero(t, s.list(t, sceneLinked).Limits.Left)

	others := make([]string, maxSoundsInAll-maxSoundsPerPerson)
	for i := range others {
		others[i] = fmt.Sprintf("%026d", i)
	}
	s.seedSounds(t, others...)
	requireRefused(t, s.upload(t, sceneLinkedAdmin, "One more"), http.StatusConflict, soundCodeFull, "a hundred and twenty in all")
	require.Len(t, s.board(t).Entries, maxSoundsInAll)
}

func TestBlockedPeopleCannotAddOrPlay(t *testing.T) {
	s := newSoundScene(t)
	id := s.added(t, sceneLinked, "Airhorn")

	block := func(username string, blocked bool) *httptest.ResponseRecorder {
		return s.serve(http.MethodPost, "/api/v1/admin/sounds/block", sceneLockedAdmin, fmt.Sprintf(`{"username":"@%s","blocked":%t}`, username, blocked), nil)
	}

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/admin/sounds/block", sceneLinked, `{"username":"bob","blocked":true}`, nil), http.StatusForbidden, "forbidden", "not an admin")

	recorder := block("dave", true)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.JSONEq(t, `{"ok":true,"userId":"`+sceneLinked+`"}`, recorder.Body.String())
	require.Contains(t, s.board(t).Blocked, sceneLinked)
	require.Equal(t, sceneLockedAdmin, s.board(t).Blocked[sceneLinked].By)

	requireRefused(t, s.upload(t, sceneLinked, "Again"), http.StatusForbidden, soundCodeBlocked, "add")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/play?id="+id, sceneLinked, "", nil), http.StatusForbidden, soundCodeBlocked, "play")
	require.True(t, s.list(t, sceneLinked).Blocked)
	require.Len(t, s.board(t).Entries, 1, "their sounds stay until deleted")

	requireRefused(t, block("nobody", true), http.StatusNotFound, "no_such_user", "no such user")

	require.Equal(t, http.StatusOK, block("dave", false).Code)
	require.NotContains(t, s.board(t).Blocked, sceneLinked)
	s.added(t, sceneLinked, "Again")

	// Fifty at most.
	doc := s.board(t)
	doc.Blocked = map[string]soundBlock{}
	for i := range maxSoundsBlocked {
		doc.Blocked[fmt.Sprintf("%026d", i)] = soundBlock{By: sceneLockedAdmin, At: 1}
	}
	encoded, err := json.Marshal(doc)
	require.NoError(t, err)
	s.setKV(kvSounds, encoded)
	requireRefused(t, block("dave", true), http.StatusConflict, "full", "fifty blocked")
}

// The first sound on a server is added from an empty board, so an empty board
// must reach the panel as an empty list. 3.2.0 left it out of the answer, and
// the panel showed a sheet with nothing in it and no way to add anything.
func TestAnEmptyBoardIsSentAsAnEmptyList(t *testing.T) {
	s := newSoundScene(t)

	recorder := s.serve(http.MethodGet, "/api/v1/sounds", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &raw))
	require.Contains(t, raw, "sounds", "an empty board is still a board")
	require.JSONEq(t, "[]", string(raw["sounds"]))
}

func TestAnUnreadableBoardIsQuarantinedAndRestartedByAnAdmin(t *testing.T) {
	s := newSoundScene(t)
	ids := s.seedSounds(t, sceneLinked)
	bad := []byte(`{"v":1,"entries":[`)
	s.setKV(kvSounds, bad)
	require.NoError(t, s.p.readSounds(true))

	require.Equal(t, bad, s.kv(kvQuarantinePrefix+kvSounds), "kept aside")
	require.Empty(t, s.list(t, sceneLinked).Sounds, "the board reads as empty")
	requireRefused(t, s.upload(t, sceneLinked, "Airhorn"), http.StatusServiceUnavailable, soundCodeStore, "no adds")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/sounds/play?id="+ids[0], sceneLinked, "", nil), http.StatusServiceUnavailable, soundCodeStore, "no plays")

	var admin adminSoundsResponse
	recorder := s.serve(http.MethodGet, "/api/v1/admin/sounds", sceneLockedAdmin, "", consoleHeader)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &admin))
	require.False(t, admin.Readable)

	recorder = s.serve(http.MethodPost, "/api/v1/admin/sounds/restart", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, s.board(t).Entries)
	require.NotEmpty(t, s.kv(kvSoundPrefix+ids[0]), "the old audio waits for the next start's sweep")
	require.Equal(t, bad, s.kv(kvQuarantinePrefix+kvSounds))

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/admin/sounds/restart", sceneLockedAdmin, "", nil), http.StatusConflict, "readable", "only while unreadable")
	s.added(t, sceneLinked, "Airhorn")
}

func TestTheOrphanSweep(t *testing.T) {
	s := newSoundScene(t)
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	named := s.seedSounds(t, sceneLinked)[0]

	oldBlob, _ := soundBlobOf(now.Add(-11*time.Minute).UnixMilli(), soundFixtures(t, "chromium")["blip"])
	newBlob, _ := soundBlobOf(now.Add(-9*time.Minute).UnixMilli(), soundFixtures(t, "chromium")["blip"])
	s.setKV(kvSoundPrefix+"00000000000000aa", oldBlob)
	s.setKV(kvSoundPrefix+"00000000000000bb", newBlob)
	s.setKV(kvSoundPrefix+"00000000000000cc", []byte("no header at all"))
	board := s.kv(kvSounds)

	sweep := func() {
		s.p.soundsState = nil
		s.p.soundsOnce = sync.Once{}
		s.p.startSounds(now)
	}

	// Never after a read that failed.
	s.faults.failRead(kvSounds)
	sweep()
	s.faults.healRead(kvSounds)
	require.NotEmpty(t, s.kv(kvSoundPrefix+"00000000000000aa"))

	// Never after a read that would not decode.
	s.setKV(kvSounds, []byte("not json"))
	sweep()
	require.NotEmpty(t, s.kv(kvSoundPrefix+"00000000000000aa"))
	require.NotEmpty(t, s.kv(kvSoundPrefix+named))

	// After a good read, the old orphans go; a blob under ten minutes old,
	// perhaps another node's upload in progress, stays.
	s.setKV(kvSounds, board)
	sweep()
	require.NotEmpty(t, s.kv(kvSoundPrefix+named), "named")
	require.Empty(t, s.kv(kvSoundPrefix+"00000000000000aa"), "old")
	require.NotEmpty(t, s.kv(kvSoundPrefix+"00000000000000bb"), "nine minutes old")
	require.Empty(t, s.kv(kvSoundPrefix+"00000000000000cc"), "a header that cannot be read counts as old")

	logged := false
	for _, call := range s.api.Calls {
		if call.Method == "LogInfo" && call.Arguments.Get(0) == "Deleted sounds the soundboard no longer names" {
			require.Equal(t, 2, call.Arguments.Get(2))
			logged = true
		}
	}
	require.True(t, logged, "the count is logged")

	// With the soundboard off, no sweep at all.
	off := s.p.getConfiguration().Clone()
	off.EnableSoundboard = false
	s.p.setConfiguration(off)
	s.setKV(kvSoundPrefix+"00000000000000dd", oldBlob)
	sweep()
	require.NotEmpty(t, s.kv(kvSoundPrefix+"00000000000000dd"))
}

func TestTheEmojiIsResolvedByTheServer(t *testing.T) {
	api := &plugintest.API{}
	enabled := true
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{EnableCustomEmoji: &enabled}})
	api.On("GetEmojiByName", "party_parrot").Return(&model.Emoji{Id: "emojiid00000000000000000000", Name: "party_parrot"}, nil)
	api.On("GetEmojiByName", "gone_parrot").Return(&model.Emoji{Id: "emojiid00000000000000000001", Name: "gone_parrot", DeleteAt: 1}, nil)
	api.On("GetEmojiByName", mock.Anything).Return(nil, model.NewAppError("GetEmojiByName", "app.missing", nil, "", http.StatusNotFound))

	p := &Plugin{}
	p.SetAPI(api)

	emoji, ok := p.resolveSoundEmoji(soundEmojiRequest{Kind: "system", Name: ":tada:"})
	require.True(t, ok)
	require.Equal(t, soundEmoji{Kind: "system", Name: "tada", Unified: "1f389"}, emoji)

	emoji, ok = p.resolveSoundEmoji(soundEmojiRequest{Kind: "custom", Name: "party_parrot"})
	require.True(t, ok)
	require.Equal(t, soundEmoji{Kind: "custom", Name: "party_parrot", ID: "emojiid00000000000000000000"}, emoji)

	for about, request := range map[string]soundEmojiRequest{
		"no such system emoji":    {Kind: "system", Name: "not_an_emoji"},
		"a system name as custom": {Kind: "custom", Name: "tada"},
		"a deleted custom emoji":  {Kind: "custom", Name: "gone_parrot"},
		"a kind there is not":     {Kind: "unicode", Name: "tada"},
		"a custom name as system": {Kind: "system", Name: "party_parrot"},
		"a name longer than any":  {Kind: "system", Name: strings.Repeat("a", 65)},
	} {
		_, ok := p.resolveSoundEmoji(request)
		require.False(t, ok, about)
	}

	// Custom emoji switched off in Mattermost: none.
	off := &plugintest.API{}
	off.On("GetConfig").Return(&model.Config{})
	p.SetAPI(off)
	_, ok = p.resolveSoundEmoji(soundEmojiRequest{Kind: "custom", Name: "party_parrot"})
	require.False(t, ok)
	off.AssertNotCalled(t, "GetEmojiByName", mock.Anything)
}

// The forty the add flow suggests (design/3.2.0.md §7.13) are all system
// emoji Mattermost has.
func TestTheSuggestedEmojiAllExist(t *testing.T) {
	suggested := strings.Fields("loud_sound tada clap joy rofl scream skull fire 100 boom drum_with_drumsticks trumpet bell " +
		"rotating_light ghost robot_face goat rooster cat dog wolf cow frog see_no_evil eyes +1 -1 wave pray " +
		"muscle heart broken_heart zzz face_palm crown trophy video_game popcorn beer rocket")
	require.Len(t, suggested, 40)

	for _, name := range suggested {
		_, found := model.SystemEmojis[name]
		require.True(t, found, "%s is a system emoji", name)
	}
}

func TestTheAudioRoutesNeedThePanelAndCarryFileHeaders(t *testing.T) {
	s := newSoundScene(t)
	id := s.added(t, sceneLinked, "Airhorn")
	script := map[string]string{"X-Requested-With": "XMLHttpRequest"}

	for _, route := range []struct{ caller, target string }{
		{sceneLinked, "/api/v1/sounds/audio?id=" + id},
		{sceneLockedAdmin, "/api/v1/admin/sounds/audio?id=" + id},
	} {
		// A Markdown image, a hundred times over: no read of the sound and
		// no token spent.
		s.p.forgetSoundBlob(id)
		s.resetReads()
		for range 100 {
			recorder := s.serve(http.MethodGet, route.target, route.caller, "", nil)
			require.Equal(t, http.StatusForbidden, recorder.Code, route.target)
			require.Equal(t, "Ask from the panel.", codedError(t, recorder).Error)
		}
		require.Zero(t, s.readsOf(kvSoundPrefix+id), "nothing read")

		recorder := s.serve(http.MethodGet, route.target, route.caller, "", script)
		require.Equal(t, http.StatusOK, recorder.Code, "%s: the meter was never touched: %s", route.target, recorder.Body.String())
		require.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "default-src 'none'; sandbox", recorder.Header().Get("Content-Security-Policy"))
		require.Equal(t, "private, no-cache", recorder.Header().Get("Cache-Control"))

		etag := recorder.Header().Get("ETag")
		require.Regexp(t, `^"[0-9a-f]{64}"$`, etag)

		again := map[string]string{"X-Requested-With": "XMLHttpRequest", "If-None-Match": etag}
		recorder = s.serve(http.MethodGet, route.target, route.caller, "", again)
		require.Equal(t, http.StatusNotModified, recorder.Code, "a repeat Listen is cheap, after the checks ran again")
		require.Empty(t, recorder.Body.Bytes())
	}

	// The checks do run again: somebody blocked from the soundboard still
	// hears their own sound, but a deleted one is gone, cache or not.
	recorder := s.serve(http.MethodPost, "/api/v1/sounds/remove?id="+id, sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/sounds/audio?id="+id, sceneLinked, "", script), http.StatusGone, soundCodeGone, "deleted")
}

func TestSoundListsSayNoStore(t *testing.T) {
	s := newSoundScene(t)
	s.added(t, sceneLinked, "Airhorn")

	for _, call := range []struct{ caller, target string }{
		{sceneLinked, "/api/v1/sounds"},
		{sceneLockedAdmin, "/api/v1/admin/sounds"},
	} {
		recorder := s.serve(http.MethodGet, call.target, call.caller, "", consoleHeader)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"), call.target)
	}

	var admin adminSoundsResponse
	recorder := s.serve(http.MethodGet, "/api/v1/admin/sounds", sceneLockedAdmin, "", consoleHeader)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &admin))
	require.True(t, admin.On)
	require.True(t, admin.Readable)
	require.Len(t, admin.Sounds, 1)
	require.True(t, admin.Sounds[0].OK)
	require.Equal(t, "Dave", admin.Sounds[0].Owner)
	require.Equal(t, adminSoundTotals{Sounds: 1, Bytes: admin.Sounds[0].Bytes, People: 1}, admin.Totals)

	// A sound whose audio fails its checksum is marked.
	blob := s.kv(kvSoundPrefix + admin.Sounds[0].ID)
	blob[len(blob)-1] ^= 0xFF
	s.setKV(kvSoundPrefix+admin.Sounds[0].ID, blob)
	recorder = s.serve(http.MethodGet, "/api/v1/admin/sounds", sceneLockedAdmin, "", consoleHeader)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &admin))
	require.False(t, admin.Sounds[0].OK)

	// And somebody who is not an admin sees none of it.
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/admin/sounds", sceneLinked, "", consoleHeader), http.StatusForbidden, "forbidden", "not an admin")
}

// A re-read of the board that fetched it from just before a block must not
// put the old board back over the block on this node, where the blocked
// person's presses would play for up to 30 s.
func TestARereadOfTheBoardNeverPutsBackWhatABlockReplaced(t *testing.T) {
	s := newSoundScene(t)
	s.added(t, sceneLinkedAdmin, "Airhorn")

	var blocked chan error
	hookRead(t, s.p, kvSounds, func() {
		blocked = meanwhile(func() error {
			return s.p.changeSounds(func(doc *soundsDoc) (bool, error) {
				doc.Blocked = map[string]soundBlock{sceneLinkedAdmin: {By: sceneLockedAdmin, At: 1}}
				return true, nil
			})
		})
	})
	require.NoError(t, s.p.readSounds(true))
	require.NotNil(t, blocked, "the block was made during the read")
	require.NoError(t, <-blocked)

	require.Contains(t, s.board(t).Blocked, sceneLinkedAdmin, "the store has the block")
	held, readable, err := s.p.board()
	require.NoError(t, err)
	require.True(t, readable)
	require.Contains(t, held.Blocked, sceneLinkedAdmin, "and so does this node's copy")
}

// The console's list reads every sound's audio to check it, so a Markdown
// image pointing at it, rendered in an admin's browser, must read nothing at
// all (design/3.2.0.md §2.2 and §7.15).
func TestTheAdminSoundListAnswersOnlyTheConsoleAndReadsNothingOtherwise(t *testing.T) {
	s := newSoundScene(t)
	s.seedSounds(t, sceneLinked, sceneLinked, sceneLinkedAdmin)

	for n := range 30 {
		s.resetReads()
		recorder := s.serve(http.MethodGet, fmt.Sprintf("/api/v1/admin/sounds?n=%d", n), sceneLockedAdmin, "", nil)
		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Equal(t, "Ask from the panel.", codedError(t, recorder).Error)
		require.Zero(t, s.totalReads(), "an image in a post reads nothing")
	}

	s.resetReads()
	recorder := s.serve(http.MethodGet, "/api/v1/admin/sounds", sceneLockedAdmin, "", consoleHeader)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Positive(t, s.totalReads(), "the console itself still gets the whole view")
}
