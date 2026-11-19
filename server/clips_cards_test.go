package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// The thumbs up on a shared clip's card (design/3.2.0.md §9): the cards route
// says how many count, whether this reader voted, and whether they may.

func cardsFor(t *testing.T, w *clipsWorld, userID, ids string) map[string]*clipCardView {
	t.Helper()

	response := w.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + ids})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer clipCardsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer.Cards
}

// clipFor is one clip as a reader is given it by GET /clips/clip.
func clipFor(t *testing.T, w *clipsWorld, userID, id string) clipView {
	t.Helper()

	response := w.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + id})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var clip clipView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &clip))

	return clip
}

func TestClipCardsCarryTheCount(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-erin", "uid-alice")
	clipsAdmins(w.api)

	votes := votesAt(clipsNow.Add(-time.Minute), "uid-dave", "uid-dan", "uid-erin", "uid-alice", "uid-bob")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votes), true)

	require.Equal(t, 4, cardsFor(t, w, "uid-dave", clipA)[clipA].Upvotes, "four who count; the clipper's own never")

	// When it goes, as the clip itself says it: no client multiplies days.
	card := cardsFor(t, w, "uid-dave", clipA)[clipA]
	require.Equal(t, clipFor(t, w, "uid-dave", clipA).ExpiresAt, card.ExpiresAt)
	require.Greater(t, card.ExpiresAt, clipsNow.UnixMilli())

	// Alice unlinks: their vote stays, and stops counting.
	w.unlink("uid-alice")
	w.p.accessForget("uid-dave")
	require.Equal(t, 3, cardsFor(t, w, "uid-dave", clipA)[clipA].Upvotes)

	// The count is the same for every reader.
	for _, reader := range []string{"uid-dan", "uid-bob", "uid-erin"} {
		require.Equal(t, 3, cardsFor(t, w, reader, clipA)[clipA].Upvotes, reader)
	}
}

func TestClipCardsSayVotedAndCanVotePerReader(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-robot")
	clipsAdmins(w.api)
	w.changeUser("uid-robot", func(user *model.User) { user.IsBot = true })

	w.person("uid-guest")
	w.kvSet(kvLetIn, []byte(`{"version":1,"people":{"uid-guest":{"at":1,"by":"uid-boss"}}}`))

	// The guest voted while they were linked, and has been let in since.
	votes := votesAt(clipsNow.Add(-time.Minute), "uid-dan", "uid-guest")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votes), true)

	type seen struct {
		voted, canVote bool
	}
	for reader, want := range map[string]seen{
		"uid-dave":  {voted: false, canVote: true},
		"uid-dan":   {voted: true, canVote: true},
		"uid-bob":   {voted: false, canVote: false}, // their own clip
		"uid-guest": {voted: true, canVote: false},  // let in: an old vote shows, and that is all
		"uid-robot": {voted: false, canVote: false}, // a bot
	} {
		card := cardsFor(t, w, reader, clipA)[clipA]
		require.NotNil(t, card, reader)
		require.Equal(t, want, seen{voted: card.Voted, canVote: card.CanVote}, reader)
	}

	// Nobody locked gets a card at all.
	w.person("uid-stranger")
	response := w.call(t, "uid-stranger", clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + clipA})
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestClipCardsAreNotCached(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	clipsAdmins(w.api)
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), true)

	response := w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + clipA})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "per reader, so never kept by a browser or a proxy")
}
