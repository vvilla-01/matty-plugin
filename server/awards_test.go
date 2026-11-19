package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Awards by your name (design/3.2.0.md §3): which awards somebody wants
// beside their name, up to four in their order, or nothing chosen, which is
// Automatic and exactly 3.1.0. Public like the awards themselves, and so
// carried on the snapshot, with nothing else of the document.

// someUserID is a Mattermost-shaped id, 26 lower-case letters and digits.
func someUserID(letter string) string {
	return strings.Repeat(letter, 26)
}

func storedAwardPrefs(t *testing.T, s *accessScene) *awardPrefsDoc {
	t.Helper()

	doc, ok := decodeAwardPrefs(s.kv(kvAwardPrefs))
	require.True(t, ok)

	return doc
}

func TestAwardChoiceValidation(t *testing.T) {
	s := newAccessScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1, 10"
	s.p.setConfiguration(cfg)
	watts := s.p.channelAchievementID("videokey-watts")

	choose := func(body string) (int, string) {
		recorder := s.serve(http.MethodPost, "/api/v1/awards/display", sceneLinked, body, nil)

		return recorder.Code, codedErrorOrEmpty(recorder)
	}

	for about, body := range map[string]string{
		"five":                    `{"show":["alltime","month","snooper","clipper","past"]}`,
		"twice":                   `{"show":["month","month"]}`,
		"a form there is not":     `{"show":["crown"]}`,
		"a fan of somebody short": `{"show":["fan:abc"]}`,
		"a fan in capitals":       `{"show":["fan:` + strings.ToUpper(someUserID("a")) + `"]}`,
		"a family there is not":   `{"show":["ach:nope"]}`,
		"a channel never set":     `{"show":["ach:ch-000000000000"]}`,
		"not a list":              `{"show":"month"}`,
	} {
		status, code := choose(body)
		require.Equal(t, http.StatusBadRequest, status, about)
		require.Contains(t, []string{"bad_choice", "bad_request"}, code, about)
	}

	status, code := choose(`{"show":["month"],"by":"` + sceneLinkedAdmin + `"}`)
	require.Equal(t, http.StatusBadRequest, status, "nothing but the choice")
	require.Equal(t, "bad_request", code)

	status, _ = choose(`{"show":["month","` + strings.Repeat("x", 3000) + `"]}`)
	require.Equal(t, http.StatusBadRequest, status, "2 KB at most")

	// The past-month medals, a fan of anybody, the built-in families and a
	// channel that has a line now: whether they are held is not asked.
	status, _ = choose(`{"show":["past","fan:` + someUserID("b") + `","ach:streamed","ach:` + watts + `"]}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"past", "fan:" + someUserID("b"), "ach:streamed", "ach:" + watts}, storedAwardPrefs(t, s).Entries[sceneLinked].Show)

	// An empty list is a choice: nothing but the headphones.
	status, _ = choose(`{"show":[]}`)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, storedAwardPrefs(t, s).Entries[sceneLinked].Show)
	require.Empty(t, storedAwardPrefs(t, s).Entries[sceneLinked].Show)
	require.Contains(t, string(s.kv(kvAwardPrefs)), `"show":[]`)

	// null is Automatic, and Automatic is no entry at all.
	status, _ = choose(`{"show":null}`)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, storedAwardPrefs(t, s).Entries, sceneLinked)

	// Choosing is for linked people; the gate says so before anything else.
	for _, caller := range []string{sceneLetIn, sceneLocked} {
		recorder := s.serve(http.MethodPost, "/api/v1/awards/display", caller, `{"show":["month"]}`, nil)
		require.Equal(t, http.StatusForbidden, recorder.Code, caller)
	}
}

// A 24/7 channel family somebody chose can leave the catalogue: its line is
// removed, or its key leaves the 24/7 stream keys. The panel sends the whole
// list with every change, the family's dimmed chip included, so every later
// change must still save: the family is taken out, as nobody can hold it
// again and the catalogue is what an ach: id is checked against, and the
// answer says what was stored. Nobody can choose a family that is not there.
func TestAChosenFamilyThatLeavesTheCatalogueComesOutWithoutStoppingAnyChange(t *testing.T) {
	s := newAccessScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1, 10"
	s.p.setConfiguration(cfg)
	watts := "ach:" + s.p.channelAchievementID("videokey-watts")

	choose := func(caller string, show ...string) (int, []string) {
		encoded, err := json.Marshal(map[string][]string{"show": show})
		require.NoError(t, err)

		recorder := s.serve(http.MethodPost, "/api/v1/awards/display", caller, string(encoded), nil)
		var answer awardDisplayResponse
		_ = json.Unmarshal(recorder.Body.Bytes(), &answer)

		return recorder.Code, answer.Show
	}

	status, _ := choose(sceneLinked, watts, "month")
	require.Equal(t, http.StatusOK, status)

	retired := s.p.getConfiguration().Clone()
	retired.ChannelAchievements = ""
	s.p.setConfiguration(retired)

	status, stored := choose(sceneLinked, watts, "month", "snooper")
	require.Equal(t, http.StatusOK, status, "adding another award still saves")
	require.Equal(t, []string{"month", "snooper"}, stored, "and the answer says the family came out")
	require.Equal(t, []string{"month", "snooper"}, storedAwardPrefs(t, s).Entries[sceneLinked].Show)

	status, _ = choose(sceneLinked, "snooper", watts, "month")
	require.Equal(t, http.StatusBadRequest, status, "once out, it cannot come back")
	status, _ = choose(sceneLinkedAdmin, watts)
	require.Equal(t, http.StatusBadRequest, status, "nor can anybody else choose it")
	status, _ = choose(sceneLinked, "month", "ach:ch-000000000000")
	require.Equal(t, http.StatusBadRequest, status, "nor a family that never was")
	status, stored = choose(sceneLinked, "snooper", "month")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"snooper", "month"}, stored)
}

func TestAwardChoicesRideTheSnapshotSortedAndAlone(t *testing.T) {
	s := newAccessScene(t)

	require.NoError(t, s.p.setAwardChoice(someUserID("z"), []string{"month", "past"}, 1760300000000))
	require.NoError(t, s.p.setAwardChoice(someUserID("a"), []string{}, 1760300000001))
	require.NoError(t, s.p.setAwardChoice(someUserID("m"), []string{"ach:streamed"}, 1760300000002))

	current := &snapshot{}
	s.p.addAwardChoices(current)

	encoded, err := json.Marshal(current.AwardChoices)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"userId":"`+someUserID("a")+`","show":[]},
		{"userId":"`+someUserID("m")+`","show":["ach:streamed"]},
		{"userId":"`+someUserID("z")+`","show":["month","past"]}
	]`, string(encoded))
	require.Equal(t, someUserID("a"), current.AwardChoices[0].UserID, "in user id order, every time")
	require.NotContains(t, string(encoded), "1760300000", "never when it was made")
}

func TestClearingAChoiceIsOpenToEverybodyAndRemovesTheEntry(t *testing.T) {
	s := newAccessScene(t)

	// Chosen while linked; since let in, or locked out.
	require.NoError(t, s.p.setAwardChoice(sceneLetIn, []string{"month"}, 1))
	require.NoError(t, s.p.setAwardChoice(sceneLocked, []string{"snooper"}, 2))

	for _, caller := range []string{sceneLetIn, sceneLocked} {
		recorder := s.serve(http.MethodDelete, "/api/v1/awards/display", caller, "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, caller)
		require.NotContains(t, storedAwardPrefs(t, s).Entries, caller, "%s is back on Automatic", caller)
	}

	// Somebody with nothing chosen clears nothing, and writes nothing.
	writes := s.faults.writesTo(kvAwardPrefs)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/awards/display", sceneLocked, "", nil).Code)
	require.Equal(t, writes, s.faults.writesTo(kvAwardPrefs))

	current := &snapshot{}
	s.p.addAwardChoices(current)
	require.Nil(t, current.AwardChoices, "nobody chose")
}

func TestUnreadableAwardPrefsMeanAutomatic(t *testing.T) {
	s := newAccessScene(t)
	bad := []byte(`{"v":1,"entries":{"x":`)
	s.setKV(kvAwardPrefs, bad)
	s.p.refreshAwardDocs(true)

	current := &snapshot{}
	s.p.addAwardChoices(current)
	require.Nil(t, current.AwardChoices, "nobody chose, as far as anybody can tell")
	require.Equal(t, bad, s.kv(kvQuarantinePrefix+kvAwardPrefs), "kept aside")

	var mine awardsMineResponse
	recorder := s.serve(http.MethodGet, "/api/v1/awards/mine", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &mine))
	require.False(t, mine.Readable)
	require.Nil(t, mine.Show)
	require.JSONEq(t, `{"show":null,"readable":false,"held":[]}`, recorder.Body.String(), "and nothing about biggest fans, which has no opt-out")

	// Clearing, which is open, writes nothing over it.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/awards/display", sceneLocked, "", nil).Code)
	require.Equal(t, bad, s.kv(kvAwardPrefs))

	// A linked person's choice starts a fresh document; the bad one stays
	// aside.
	require.Equal(t, http.StatusOK, s.serve(http.MethodPost, "/api/v1/awards/display", sceneLinked, `{"show":["month"]}`, nil).Code)
	require.Equal(t, []string{"month"}, storedAwardPrefs(t, s).Entries[sceneLinked].Show)
	require.Equal(t, bad, s.kv(kvQuarantinePrefix+kvAwardPrefs))
}

func TestAwardPrefsCasRetries(t *testing.T) {
	s := newAccessScene(t)

	// Another node writes a choice of its own in the moment between this
	// node reading the document and writing it back, once.
	raced := false
	s.faults.setInterfere(func(key string, store map[string][]byte) {
		if key != kvAwardPrefs || raced {
			return
		}
		raced = true
		store[key] = []byte(`{"v":1,"entries":{"` + someUserID("q") + `":{"show":["clipper"],"at":5}}}`)
	})

	require.Equal(t, http.StatusOK, s.serve(http.MethodPost, "/api/v1/awards/display", sceneLinked, `{"show":["month"]}`, nil).Code)
	s.faults.setInterfere(nil)

	stored := storedAwardPrefs(t, s)
	require.True(t, raced)
	require.Equal(t, []string{"clipper"}, stored.Entries[someUserID("q")].Show, "the other node's write is kept")
	require.Equal(t, []string{"month"}, stored.Entries[sceneLinked].Show, "and this one lands on top of it")
}

// Choices are bounded: only linked people write them, and five hundred is
// room for five hundred linked people.
func TestAwardPrefsAreBounded(t *testing.T) {
	s := newAccessScene(t)

	entries := map[string]awardPref{}
	for i := range maxAwardPrefs {
		entries[fmt.Sprintf("%026d", i)] = awardPref{Show: []string{}, At: 1}
	}
	full, err := json.Marshal(awardPrefsDoc{Version: awardDocsVersion, Entries: entries})
	require.NoError(t, err)
	s.setKV(kvAwardPrefs, full)

	recorder := s.serve(http.MethodPost, "/api/v1/awards/display", sceneLinked, `{"show":["month"]}`, nil)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, "full", codedErrorOrEmpty(recorder))
	require.Equal(t, awardFull, codedError(t, recorder).Error)
}

// The 3.1.0 golden scene, with a document that holds nobody's choice: what
// every client is sent is what the file says, byte for byte. Until 3.6.0 that
// was 3.1.0's snapshot with the people index 3.5.0 added; it is now the
// snapshot and, beside it, the boards that were in it (see goldenJSON).
func TestNobodyChoseIsTheOldSnapshot(t *testing.T) {
	newMediaFake(t)
	g := newGoldenScene(t, &configuration{TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret})
	g.store[kvAwardPrefs] = []byte(`{"v":1,"entries":{}}`)

	g.p.pollOnce(t.Context(), true)
	g.p.pollOnce(t.Context(), true)

	requireGolden(t, "testdata/snapshot-310.golden.json", goldenJSON(t, g.p))
}

// With achievements, fans and the soundboard off, and nobody having chosen,
// the snapshot and the boards are that same file's, and the only key 3.2.0
// adds to a poll's reads is the choices' small document, on the first poll
// only: the copy is kept for a minute. 3.2.1 reads no list of fan opt-outs at
// all.
func TestWithEveryNewThingOffTheSnapshotIs310s(t *testing.T) {
	newMediaFake(t)
	g := newGoldenScene(t, &configuration{TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret})

	g.p.pollOnce(t.Context(), true)
	first := g.readKeys()
	g.p.pollOnce(t.Context(), true)
	second := g.readKeys()[len(first):]

	requireGolden(t, "testdata/snapshot-310.golden.json", goldenJSON(t, g.p))

	newKeys := []string{kvAwardPrefs, kvFansTotals, kvChanwatchTotals, kvSounds}
	countIn := func(keys []string, key string) int {
		count := 0
		for _, read := range keys {
			if read == key || strings.HasPrefix(read, kvSoundPrefix) && key == kvSounds {
				count++
			}
		}

		return count
	}

	require.Equal(t, 1, countIn(first, kvAwardPrefs))
	require.Zero(t, countIn(first, kvFanOptouts)+countIn(second, kvFanOptouts), "no poll reads the old opt-outs")
	for _, key := range newKeys {
		require.Zero(t, countIn(second, key), "%s is not read again within the minute", key)
	}
	for _, key := range newKeys[1:] {
		require.Zero(t, countIn(first, key), "%s is never read while its feature is off", key)
	}

	for key := range g.store {
		for _, written := range newKeys[1:] {
			require.NotEqual(t, written, key, "nothing new is written")
		}
		require.False(t, strings.HasPrefix(key, kvSoundPrefix))
	}
}
