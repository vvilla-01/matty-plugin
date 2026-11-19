package main

import (
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
)

// The moment these tests happen at. Nothing here reads the clock: every
// function under test is handed the time.
var clipsNow = time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC)

// A server with people on it, a real compare-and-set store, and accounts that
// can be deactivated, turned into bots, deleted outright or made to fail to
// load, part way through a test.
type clipsWorld struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults

	// The republish timers, held: see clipsWorldOn.
	timers *fakeClipsTimers

	lock      sync.Mutex
	users     map[string]*model.User
	userFails map[string]bool
	lookups   map[string]int
}

// newClipsWorld is a server where each of people has an account and has
// linked a stream of their own, "uid-bob" linking "videokey-bob".
func newClipsWorld(t *testing.T, people ...string) *clipsWorld {
	t.Helper()

	return clipsWorldOn(t, map[string][]byte{}, people...)
}

// clipsWorldOn is a fresh process over an existing store: a restart.
func clipsWorldOn(t *testing.T, store map[string][]byte, people ...string) *clipsWorld {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(store)
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	w := &clipsWorld{
		p:         p,
		api:       api,
		store:     store,
		faults:    faults,
		users:     map[string]*model.User{},
		userFails: map[string]bool{},
		lookups:   map[string]int{},
	}

	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		w.lock.Lock()
		defer w.lock.Unlock()

		w.lookups[userID]++
		if w.userFails[userID] {
			return nil, model.NewAppError("GetUser", "app.user.get.app_error", nil, "injected failure", http.StatusInternalServerError)
		}

		user, found := w.users[userID]
		if !found {
			return nil, model.NewAppError("GetUser", "app.user.missing.app_error", nil, "", http.StatusNotFound)
		}
		copied := *user

		return &copied, nil
	})

	cfg := &configuration{EnableClips: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	// Held from the start, not only once a test starts Clips: a test that
	// marks the store running and writes would otherwise arm a real
	// republish timer, which goes off two seconds later into an API that
	// has no PublishWebSocketEvent, and on a slow enough machine kills the
	// whole test binary.
	_, w.timers = holdClipsTimers(p)

	for _, userID := range people {
		w.person(userID)
		w.link(userID, keyOf(userID))
	}

	return w
}

// keyOf is the stream key a test person links: "uid-bob" streams as
// "videokey-bob".
func keyOf(userID string) string {
	return "videokey-" + strings.TrimPrefix(userID, "uid-")
}

func (w *clipsWorld) person(userID string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	name := strings.TrimPrefix(userID, "uid-")
	w.users[userID] = &model.User{Id: userID, Username: name, FirstName: strings.ToUpper(name[:1]) + name[1:]}
}

func (w *clipsWorld) changeUser(userID string, change func(user *model.User)) {
	w.lock.Lock()
	defer w.lock.Unlock()

	change(w.users[userID])
}

func (w *clipsWorld) deleteUser(userID string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	delete(w.users, userID)
}

func (w *clipsWorld) failUser(userID string, failing bool) {
	w.lock.Lock()
	defer w.lock.Unlock()

	w.userFails[userID] = failing
}

func (w *clipsWorld) lookupsOf(userID string) int {
	w.lock.Lock()
	defer w.lock.Unlock()

	return w.lookups[userID]
}

func (w *clipsWorld) kvSet(key string, value []byte) {
	w.faults.lock.Lock()
	defer w.faults.lock.Unlock()

	w.store[key] = value
}

func (w *clipsWorld) kvGet(key string) []byte {
	w.faults.lock.Lock()
	defer w.faults.lock.Unlock()

	return append([]byte(nil), w.store[key]...)
}

// link is a link as setLink leaves it: both directions, so that somebody
// else proving the key finds who had it, and that the agreement rule finds
// them linked. What the access gate remembers about them goes, as it does
// when handleLink links somebody.
func (w *clipsWorld) link(userID, key string) {
	w.kvSet(kvLinkByUserPrefix+userID, []byte(key))
	w.kvSet(kvLinkByKeyPrefix+normalizeKey(key), []byte(userID))
	w.p.accessForget(userID)
}

// unlink is what an unlink does to Clips: the link goes, and Clips is told.
func (w *clipsWorld) unlink(userID string) {
	w.faults.lock.Lock()
	delete(w.store, kvLinkByKeyPrefix+normalizeKey(string(w.store[kvLinkByUserPrefix+userID])))
	delete(w.store, kvLinkByUserPrefix+userID)
	w.faults.lock.Unlock()

	w.p.clipsLinkChanged(userID)
}

func (w *clipsWorld) relink(userID string) {
	w.link(userID, keyOf(userID))
	w.p.clipsLinkChanged(userID)
}

func (w *clipsWorld) seed(t *testing.T, doc *clipsDoc) {
	t.Helper()

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	w.kvSet(kvClips, raw)
}

func (w *clipsWorld) stored(t *testing.T) *clipsDoc {
	t.Helper()

	doc, err := decodeClipsDoc(w.kvGet(kvClips))
	require.NoError(t, err)

	return doc
}

func (w *clipsWorld) holder(t *testing.T) string {
	t.Helper()

	doc := w.stored(t)
	if doc.Holder == nil {
		return ""
	}

	return doc.Holder.UserID
}

// recompute is the no-op write that works Best Clipper out again.
func (w *clipsWorld) recompute(t *testing.T, now time.Time) {
	t.Helper()

	require.NoError(t, w.p.recomputeBestClipper(now))
}

// counts is who counts for the stored document, as this node's cache has it.
func (w *clipsWorld) counts(t *testing.T) func(string) bool {
	t.Helper()

	doc := w.stored(t)

	return w.p.clips().verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible
}

// votesAt is a set of upvotes, the first cast at the moment given and each
// after it a second later.
func votesAt(at time.Time, voters ...string) map[string]int64 {
	votes := map[string]int64{}
	for i, voter := range voters {
		votes[voter] = at.Add(time.Duration(i) * time.Second).UnixMilli()
	}

	return votes
}

// testClip is a 45 s clip of Dave's stream, published at the moment given.
func testClip(id, clipperID string, publishedAt time.Time, voters map[string]int64) clipEntry {
	return clipEntry{
		ID:         id,
		ClipperID:  clipperID,
		StreamerID: "uid-dave",
		StreamName: "Dave",
		Handle:     someHandle,
		StreamTag:  "tag-of-daves-stream",
		Title:      "clip " + id,
		CreatedAt:  publishedAt.UnixMilli(),
		StartAt:    publishedAt.Add(-time.Minute).UnixMilli(),
		DurationMs: 45000,
		Width:      1280,
		Height:     720,
		Codecs:     "avc1.64001f,opus",
		Bytes:      1000,
		Voters:     voters,
	}
}

func requireRefusal(t *testing.T, err error, status int, code clipErrorCode) {
	t.Helper()

	var refusal *clipError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, status, refusal.status)
	require.Equal(t, code, refusal.code)

	answeredStatus, answeredCode := clipErrorFor(err)
	require.Equal(t, status, answeredStatus)
	require.Equal(t, code, answeredCode)
}

const (
	clipA = "00000000000000a1"
	clipB = "00000000000000b2"
	clipC = "00000000000000c3"
	clipD = "00000000000000d4"
)

// A vote is the state somebody wants, not a toggle: asking twice is asking
// once, and asking for what is already so writes nothing at all.
func TestVotesAreDesiredStateAndIdempotent(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	upvotes, voted, err := w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.NoError(t, err)
	require.Equal(t, 1, upvotes)
	require.True(t, voted)

	doc := w.stored(t)
	require.Equal(t, map[string]int64{"uid-dave": clipsNow.UnixMilli()}, doc.Clips[0].Voters)
	require.EqualValues(t, 2, doc.Rev)

	// Again, later: nothing moves, not even when the vote was cast.
	before := w.kvGet(kvClips)
	upvotes, voted, err = w.p.voteClip(clipA, "uid-dave", true, clipsNow.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, upvotes)
	require.True(t, voted)
	require.Equal(t, before, w.kvGet(kvClips))

	upvotes, voted, err = w.p.voteClip(clipA, "uid-dave", false, clipsNow.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, upvotes)
	require.False(t, voted)
	require.Empty(t, w.stored(t).Clips[0].Voters)

	before = w.kvGet(kvClips)
	upvotes, voted, err = w.p.voteClip(clipA, "uid-dave", false, clipsNow.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, upvotes)
	require.False(t, voted)
	require.Equal(t, before, w.kvGet(kvClips))
}

// Votes cast at the same moment from different goroutines all land: every
// write is a compare-and-set of the whole document, and a lost race is tried
// again rather than written over.
func TestSimultaneousVotesAreAllKept(t *testing.T) {
	voters := []string{"uid-v1", "uid-v2", "uid-v3", "uid-v4", "uid-v5", "uid-v6", "uid-v7", "uid-v8"}
	w := newClipsWorld(t, append([]string{"uid-bob"}, voters...)...)
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	start := make(chan struct{})
	errs := make(chan error, len(voters))
	var group sync.WaitGroup

	for _, voter := range voters {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start

			_, _, err := w.p.voteClip(clipA, voter, true, clipsNow)
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	doc := w.stored(t)
	require.Len(t, doc.Clips[0].Voters, len(voters))
	for _, voter := range voters {
		require.Contains(t, doc.Clips[0].Voters, voter)
	}
	require.EqualValues(t, 1+len(voters), doc.Rev, "one write per vote")
	require.Equal(t, "uid-bob", doc.Holder.UserID)
	require.Equal(t, len(voters), displayedUpvotes(&doc.Clips[0], w.counts(t)))
}

// Nobody upvotes their own clip, and no bot upvotes anything. Somebody whose
// account cannot be read is not guessed about either way.
func TestNoSelfOrBotVotes(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-robot", "uid-dave")
	w.changeUser("uid-robot", func(user *model.User) { user.IsBot = true })
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})
	before := w.kvGet(kvClips)

	_, _, err := w.p.voteClip(clipA, "uid-bob", true, clipsNow)
	requireRefusal(t, err, http.StatusForbidden, clipErrOwnClip)

	_, _, err = w.p.voteClip(clipA, "uid-robot", true, clipsNow)
	requireRefusal(t, err, http.StatusForbidden, clipErrForbidden)

	w.failUser("uid-dave", true)
	_, _, err = w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.ErrorIs(t, err, errStore)
	status, code := clipErrorFor(err)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, clipErrStore, code)

	require.Equal(t, before, w.kvGet(kvClips))
}

// A clip past its time takes no votes, up or down, even before the sweep has
// removed it; nor does one that is not there.
func TestNoVotesOnExpiredClips(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	old := testClip(clipA, "uid-bob", clipsNow.Add(-31*24*time.Hour), votesAt(clipsNow.Add(-30*24*time.Hour), "uid-dave"))
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{old}, Retention: retentionRecord{Days: 30}})
	before := w.kvGet(kvClips)

	for _, up := range []bool{true, false} {
		_, _, err := w.p.voteClip(clipA, "uid-dave", up, clipsNow)
		requireRefusal(t, err, http.StatusGone, clipErrClipGone)
	}

	_, _, err := w.p.voteClip(clipB, "uid-dave", true, clipsNow)
	requireRefusal(t, err, http.StatusGone, clipErrClipGone)

	require.Equal(t, before, w.kvGet(kvClips))
}

// Best Clipper as a rule on scores: the most upvotes, at least one; a holder
// level with the best keeps it; otherwise whoever got there first, then the
// smaller id; nobody who does not count.
func TestBestClipperTieKeepsHolder(t *testing.T) {
	everybody := func(string) bool { return true }
	scores := map[string]clipperScore{
		"uid-bob":  {up: 3, clips: 2, reachedAt: 300},
		"uid-dave": {up: 3, clips: 1, reachedAt: 100},
		"uid-dan":  {up: 2, clips: 5, reachedAt: 50},
	}

	require.Equal(t, "uid-bob", pickBestClipper(scores, "uid-bob", everybody), "level with the best, the holder keeps it")
	require.Equal(t, "uid-dave", pickBestClipper(scores, "uid-dan", everybody), "a holder behind loses it")

	// Through the document: Dave draws level with Bob and Bob keeps it; one
	// more vote and it is Dave's.
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4")
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-90*time.Minute), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-100*time.Minute), "uid-v3")),
		},
		Holder: &clipHolder{UserID: "uid-bob", Since: clipsNow.Add(-90 * time.Minute).UnixMilli()},
	})

	_, _, err := w.p.voteClip(clipB, "uid-v4", true, clipsNow)
	require.NoError(t, err)
	require.Equal(t, "uid-bob", w.holder(t), "2 each, and Dave got there first, but Bob holds it")

	_, _, err = w.p.voteClip(clipB, "uid-bob", true, clipsNow.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "uid-dave", w.holder(t))
	require.Equal(t, clipsNow.Add(time.Minute).UnixMilli(), w.stored(t).Holder.Since)
}

// With nobody holding it, a tie goes to whoever reached the score first, and
// then to the smaller user id, so every node picks the same person.
func TestBestClipperFirstToReachWinsWithoutHolder(t *testing.T) {
	everybody := func(string) bool { return true }

	require.Equal(t, "uid-dave", pickBestClipper(map[string]clipperScore{
		"uid-bob":  {up: 4, reachedAt: 900},
		"uid-dave": {up: 4, reachedAt: 400},
	}, "", everybody))

	require.Equal(t, "uid-bob", pickBestClipper(map[string]clipperScore{
		"uid-bob":  {up: 4, reachedAt: 400},
		"uid-dave": {up: 4, reachedAt: 400},
	}, "", everybody))

	// A holder who no longer counts is no holder: the rule for nobody applies.
	require.Equal(t, "uid-dave", pickBestClipper(map[string]clipperScore{
		"uid-bob":  {up: 4, reachedAt: 100},
		"uid-dave": {up: 4, reachedAt: 400},
		"uid-dan":  {up: 4, reachedAt: 500},
	}, "uid-dan", func(userID string) bool { return userID != "uid-bob" && userID != "uid-dan" }))

	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-10*time.Minute), "uid-v1")),
		testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-20*time.Minute), "uid-v2")),
	}})

	w.recompute(t, clipsNow)
	require.Equal(t, "uid-dave", w.holder(t))
}

// Nobody holds Best Clipper on no upvotes: not when there are clips but no
// votes, and not once the only vote is taken back.
func TestNobodyHoldsItAtZero(t *testing.T) {
	require.Empty(t, pickBestClipper(map[string]clipperScore{"uid-bob": {up: 0, clips: 9}}, "uid-bob", func(string) bool { return true }))

	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	w.recompute(t, clipsNow)
	require.Empty(t, w.holder(t))
	require.EqualValues(t, 1, w.stored(t).Rev, "nothing to change, nothing written")

	_, _, err := w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.NoError(t, err)
	require.Equal(t, "uid-bob", w.holder(t))

	_, _, err = w.p.voteClip(clipA, "uid-dave", false, clipsNow.Add(time.Minute))
	require.NoError(t, err)
	require.Empty(t, w.holder(t))
	require.Nil(t, w.stored(t).Holder)
}

// The vote that makes somebody Best Clipper puts them there in the same
// write: one compare-and-set, holding both the vote and the holder.
func TestHolderIsRecomputedInTheSameWrite(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 7, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	_, _, err := w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.NoError(t, err)

	w.api.AssertNumberOfCalls(t, "KVCompareAndSet", 1)

	doc := w.stored(t)
	require.EqualValues(t, 8, doc.Rev)
	require.Contains(t, doc.Clips[0].Voters, "uid-dave")
	require.Equal(t, &clipHolder{UserID: "uid-bob", Since: clipsNow.UnixMilli()}, doc.Holder)

	w.api.AssertCalled(t, "LogInfo", "Best Clipper changed", "from", "", "to", "uid-bob", "upvotes", 1)
}

// A clip that runs its course keeps its upvotes for its clipper: banked when
// the sweep settles it, and counted as they were before that.
func TestExpiredVotesStillCount(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3")
	published := clipsNow.Add(-31 * 24 * time.Hour)
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", published, votesAt(published.Add(time.Hour), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-dave", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-30*time.Minute), "uid-v3")),
		},
		Retention: retentionRecord{Days: 30},
	})

	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t), "past its time but not swept, it counts as it is")

	outcome, err := w.p.expireClips(clipsNow)
	require.NoError(t, err)
	require.Equal(t, []settledClip{{ID: clipA, ClipperID: "uid-bob", Reason: clipGoneExpired, Banked: 2, Bytes: 1000}}, outcome.Settled)
	require.Empty(t, outcome.Deferred)

	doc := w.stored(t)
	require.Len(t, doc.Clips, 1)
	require.Equal(t, bankedScore{Up: 2, Clips: 1, LastUpAt: published.Add(time.Hour + time.Second).UnixMilli()}, doc.Banked["uid-bob"])
	require.Equal(t, "uid-bob", w.holder(t))

	view := bestClipperViewOf(doc, true, doc.Retention, clipsNow.UnixMilli(), w.counts(t), w.p.clipNames(clipsNow, true))
	require.Equal(t, 2, view.Upvotes)
	require.Equal(t, 1, view.Clips)
	require.Nil(t, view.BestClip, "nothing of Bob's left to watch")

	// Settling again settles nothing.
	again, err := w.p.expireClips(clipsNow)
	require.NoError(t, err)
	require.Empty(t, again.Settled)
	require.Equal(t, doc.Rev, w.stored(t).Rev)
}

// The clipper or an admin taking a clip down takes its upvotes with it; its
// streamer taking it down leaves them with the clipper.
func TestClipperAndAdminDeleteVoidStreamerDeleteBanks(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-v1", "uid-v2", "uid-v3")
	voted := clipsNow.Add(-time.Hour)
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(voted, "uid-v1")),
		testClip(clipB, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(voted, "uid-v1", "uid-v2")),
		testClip(clipC, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(voted, "uid-v1", "uid-v2", "uid-v3")),
	}})

	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))

	outcome, err := w.p.settleClips(map[string]string{
		clipA: clipGoneClipper,
		clipB: clipGoneAdmin,
		clipC: clipGoneStreamer,
	}, clipsNow)
	require.NoError(t, err)
	require.Equal(t, []settledClip{
		{ID: clipA, ClipperID: "uid-bob", Reason: clipGoneClipper, Bytes: 1000},
		{ID: clipB, ClipperID: "uid-bob", Reason: clipGoneAdmin, Bytes: 1000},
		{ID: clipC, ClipperID: "uid-bob", Reason: clipGoneStreamer, Banked: 3, Bytes: 1000},
	}, outcome.Settled)

	doc := w.stored(t)
	require.Empty(t, doc.Clips)
	require.Equal(t, bankedScore{Up: 3, Clips: 1, LastUpAt: voted.Add(2 * time.Second).UnixMilli()}, doc.Banked["uid-bob"])
	require.Equal(t, "uid-bob", w.holder(t), "three upvotes kept")
	w.api.AssertNumberOfCalls(t, "KVCompareAndSet", 2)

	// Gone is gone: settling the same ids again writes nothing.
	again, err := w.p.settleClips(map[string]string{clipC: clipGoneStreamer}, clipsNow)
	require.NoError(t, err)
	require.Empty(t, again.Settled)
	w.api.AssertNumberOfCalls(t, "KVCompareAndSet", 2)
}

// A clip already past its time is settled as expired whoever asks, so a
// clipper's delete racing the sweep banks its votes rather than voiding them.
func TestClipperDeletingAnExpiredClipBanksIt(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-v1", "uid-v2")
	published := clipsNow.Add(-30*24*time.Hour - time.Minute)
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", published, votesAt(published.Add(time.Hour), "uid-v1", "uid-v2")),
	}, Retention: retentionRecord{Days: 30}})

	for _, reason := range []string{clipGoneClipper, clipGoneAdmin} {
		t.Run(reason, func(t *testing.T) {
			w := clipsWorldOn(t, map[string][]byte{kvClips: w.kvGet(kvClips)}, "uid-bob", "uid-v1", "uid-v2")

			outcome, err := w.p.settleClips(map[string]string{clipA: reason}, clipsNow)
			require.NoError(t, err)
			require.Len(t, outcome.Settled, 1)
			require.Equal(t, clipGoneExpired, outcome.Settled[0].Reason)
			require.Equal(t, 2, outcome.Settled[0].Banked)
			require.Equal(t, 2, w.stored(t).Banked["uid-bob"].Up)
		})
	}
}

// A deactivated account holds nothing, and its votes count for nobody. A
// deletion is the same, and is an answer rather than a lookup that failed.
func TestDeactivatedUsersCannotHoldIt(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2", "uid-v3")),
		testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v4")),
	}})

	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))

	// Deactivated: noticed once what was known of Bob is ten minutes old.
	w.changeUser("uid-bob", func(user *model.User) { user.DeleteAt = clipsNow.UnixMilli() })
	w.recompute(t, clipsNow.Add(5*time.Minute))
	require.Equal(t, "uid-bob", w.holder(t), "still known as active")

	later := clipsNow.Add(clipPeopleTTL + time.Second)
	w.recompute(t, later)
	require.Equal(t, "uid-dave", w.holder(t))

	// And a voter deleted outright stops counting, as an answer: nothing is
	// held up waiting to find out who they were.
	w.deleteUser("uid-v4")
	outcome, err := w.p.settleClips(map[string]string{clipB: clipGoneStreamer}, later.Add(clipPeopleTTL))
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, 0, outcome.Settled[0].Banked)
	require.Empty(t, outcome.Deferred)
	require.Empty(t, w.holder(t), "nobody left with a counted upvote")
}

// Somebody an admin blocked cannot hold Best Clipper, and it moves in the
// same write as the block. Unblocking gives it back.
func TestBlockedUsersCannotHoldIt(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2")),
		testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v3")),
	}})
	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))

	mark := clipMark{By: "uid-admin", At: clipsNow.UnixMilli()}
	_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		return doc.block("uid-bob", mark), nil
	})
	require.NoError(t, err)

	doc := w.stored(t)
	require.Equal(t, mark, doc.Blocked["uid-bob"])
	require.Equal(t, "uid-dave", doc.Holder.UserID)
	w.api.AssertNumberOfCalls(t, "KVCompareAndSet", 2)

	// Blocked again: the first block, and who made it, stand.
	_, err = w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		return doc.block("uid-bob", clipMark{By: "uid-other-admin", At: 1}), nil
	})
	require.NoError(t, err)
	require.Equal(t, mark, w.stored(t).Blocked["uid-bob"])

	_, err = w.p.clipsCAS(clipsNow.Add(time.Minute), func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		return doc.unblock("uid-bob"), nil
	})
	require.NoError(t, err)
	require.Nil(t, w.stored(t).Blocked)
	require.Equal(t, "uid-bob", w.holder(t))
}

// Only votes from people who count are counted, on the clip and toward Best
// Clipper: not a bot's, not a deactivated or deleted account's, not from
// somebody unlinked or blocked.
func TestIneligibleVotersDoNotCount(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-fan", "uid-robot", "uid-gone", "uid-left", "uid-blocked")
	w.person("uid-unlinked")
	w.changeUser("uid-robot", func(user *model.User) { user.IsBot = true })
	w.changeUser("uid-left", func(user *model.User) { user.DeleteAt = 1 })
	w.deleteUser("uid-gone")

	voters := votesAt(clipsNow.Add(-time.Hour), "uid-fan", "uid-robot", "uid-gone", "uid-left", "uid-unlinked", "uid-blocked")
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips:   []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), voters)},
		Blocked: map[string]clipMark{"uid-blocked": {By: "uid-admin", At: 1}},
	})

	w.recompute(t, clipsNow)
	doc := w.stored(t)
	counts := w.counts(t)

	require.Equal(t, 1, displayedUpvotes(&doc.Clips[0], counts))
	require.Equal(t, 1, clipperScores(doc, counts)["uid-bob"].up)
	require.Equal(t, "uid-bob", doc.Holder.UserID, "everybody is known, so the holder moved")
	require.Len(t, doc.Clips[0].Voters, 6, "the votes themselves are kept")

	outcome, err := w.p.expireClips(clipsNow.Add(31 * 24 * time.Hour))
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, 1, outcome.Settled[0].Banked)
}

// Resetting somebody's score drops what they banked and every vote already
// cast on their clips, toward Best Clipper only: the clips still show their
// upvotes, and votes cast after the reset count.
func TestScoreResetIgnoresEarlierVotes(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4", "uid-v5")
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", clipsNow.Add(-3*time.Hour), votesAt(clipsNow.Add(-2*time.Hour), "uid-v1", "uid-v2", "uid-v3")),
			testClip(clipB, "uid-dave", clipsNow.Add(-3*time.Hour), votesAt(clipsNow.Add(-2*time.Hour), "uid-v4")),
		},
		Banked: map[string]bankedScore{"uid-bob": {Up: 5, Clips: 2, LastUpAt: 1}},
	})
	w.recompute(t, clipsNow.Add(-time.Hour))
	require.Equal(t, "uid-bob", w.holder(t))

	resetAt := clipsNow.Add(-time.Hour)
	_, err := w.p.clipsCAS(resetAt, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		return doc.resetScore("uid-bob", clipMark{By: "uid-admin", At: write.now}), nil
	})
	require.NoError(t, err)

	doc := w.stored(t)
	counts := w.counts(t)
	require.Equal(t, bankedScore{}, doc.Banked["uid-bob"])
	require.Equal(t, 0, clipperScores(doc, counts)["uid-bob"].up)
	require.Equal(t, 3, displayedUpvotes(&doc.Clips[0], counts), "what the clip shows is untouched")
	require.Equal(t, "uid-dave", doc.Holder.UserID, "moved in the same write as the reset")

	// Two new votes for Bob, after the reset, count.
	_, _, err = w.p.voteClip(clipA, "uid-v4", true, clipsNow)
	require.NoError(t, err)
	_, _, err = w.p.voteClip(clipA, "uid-v5", true, clipsNow.Add(time.Second))
	require.NoError(t, err)

	doc = w.stored(t)
	require.Equal(t, 2, clipperScores(doc, w.counts(t))["uid-bob"].up)
	require.Equal(t, "uid-bob", doc.Holder.UserID)

	// Banking a reset clipper's clip banks only what counts.
	outcome, err := w.p.settleClips(map[string]string{clipA: clipGoneStreamer}, clipsNow.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 2, outcome.Settled[0].Banked)
}

// The holder is in the document, so a restart keeps it, and the recompute a
// new process does with nothing cached leaves it where it is.
func TestHolderSurvivesRestart(t *testing.T) {
	people := []string{"uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4"}
	w := newClipsWorld(t, people...)
	w.seed(t, &clipsDoc{
		V: 1, Rev: 4,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-90*time.Minute), "uid-v3", "uid-v4")),
		},
		Holder: &clipHolder{UserID: "uid-bob", Since: 1},
	})
	before := w.kvGet(kvClips)

	restarted := clipsWorldOn(t, w.store, people...)
	restarted.recompute(t, clipsNow)

	require.Equal(t, before, restarted.kvGet(kvClips), "a tie keeps the holder, so nothing is written")
	require.Equal(t, "uid-bob", restarted.holder(t))
}

// Somebody nobody has managed to look up yet never moves Best Clipper: not
// the holder, on a cold cache with the user store failing, and not a voter.
// Writes still land; the holder waits until everybody is known.
func TestUnknownVerdictNeverMovesTheHolder(t *testing.T) {
	people := []string{"uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4", "uid-v5"}
	w := newClipsWorld(t, people...)
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-90*time.Minute), "uid-v3", "uid-v4")),
		},
		Holder: &clipHolder{UserID: "uid-bob", Since: 1},
	})

	// A cold start with Bob's account failing to load: counting Bob out
	// would hand Dave a tied title for good.
	w.failUser("uid-bob", true)
	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))
	require.EqualValues(t, 1, w.stored(t).Rev)

	// Dave pulls ahead while Bob is still unknown: the vote lands, the holder
	// waits.
	_, _, err := w.p.voteClip(clipB, "uid-v5", true, clipsNow.Add(time.Second))
	require.NoError(t, err)
	doc := w.stored(t)
	require.Contains(t, doc.Clips[1].Voters, "uid-v5")
	require.Equal(t, "uid-bob", doc.Holder.UserID)

	// Once Bob can be looked up, the lead decides it.
	w.failUser("uid-bob", false)
	w.recompute(t, clipsNow.Add(clipLookUpRetry+time.Second))
	require.Equal(t, "uid-dave", w.holder(t))

	// What was known stands through a later failure: Dave is not unseated by
	// a failing lookup of Dave.
	w.failUser("uid-dave", true)
	w.recompute(t, clipsNow.Add(clipPeopleTTL+clipLookUpRetry+2*time.Second))
	require.Equal(t, "uid-dave", w.holder(t))
	require.Positive(t, w.lookupsOf("uid-dave"))
}

// A clip whose votes are to be banked waits while anybody who voted for it
// cannot be looked up: banking freezes the count, and a vote left out of it
// would be lost for good. The sweep tries again; voiding never waits.
func TestBankingWaitsForKnownVoters(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-v1", "uid-v2")
	published := clipsNow.Add(-31 * 24 * time.Hour)
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", published, votesAt(published.Add(time.Hour), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-v2")),
			testClip(clipC, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-v2")),
		},
		Retention: retentionRecord{Days: 30},
	})
	w.failUser("uid-v2", true)

	outcome, err := w.p.expireClips(clipsNow)
	require.NoError(t, err)
	require.Empty(t, outcome.Settled)
	require.Equal(t, []string{clipA}, outcome.Deferred)
	require.Len(t, w.stored(t).Clips, 3)
	require.Nil(t, w.stored(t).Banked)

	outcome, err = w.p.settleClips(map[string]string{clipB: clipGoneStreamer, clipC: clipGoneClipper}, clipsNow)
	require.NoError(t, err)
	require.Equal(t, []string{clipB}, outcome.Deferred, "a streamer's delete banks, so it waits")
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, clipC, outcome.Settled[0].ID, "a clipper's delete voids, so it does not")

	w.failUser("uid-v2", false)
	later := clipsNow.Add(clipLookUpRetry + time.Second)

	outcome, err = w.p.expireClips(later)
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, 2, outcome.Settled[0].Banked)
	require.Empty(t, outcome.Deferred)

	outcome, err = w.p.settleClips(map[string]string{clipB: clipGoneStreamer}, later)
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, bankedScore{Up: 3, Clips: 2, LastUpAt: clipsNow.Add(-time.Minute).UnixMilli()}, w.stored(t).Banked["uid-bob"])
}

// Votes count only while the voter is linked. Unlinking keeps the vote on the
// clip and stops it counting, at once on this node.
func TestUnlinkedVotesStopCounting(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dave", "uid-dan")),
	}})
	w.recompute(t, clipsNow)
	require.Equal(t, 2, displayedUpvotes(&w.stored(t).Clips[0], w.counts(t)))

	w.unlink("uid-dan")
	w.recompute(t, clipsNow.Add(time.Second))

	doc := w.stored(t)
	require.Contains(t, doc.Clips[0].Voters, "uid-dan", "the vote is kept")
	require.Equal(t, 1, displayedUpvotes(&doc.Clips[0], w.counts(t)))
	require.Equal(t, 1, clipperScores(doc, w.counts(t))["uid-bob"].up)

	// A clip banked meanwhile banks only what counted then.
	outcome, err := w.p.settleClips(map[string]string{clipA: clipGoneStreamer}, clipsNow.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, outcome.Settled[0].Banked)
}

// A holder who unlinks loses Best Clipper, worked out again in the background
// as soon as the link goes, and their clips and their score are kept for when
// they come back.
func TestUnlinkedHolderLosesBestClipper(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4")
	setClipsClock(w.p, clipsNow)
	w.p.clips().markRunning()
	t.Cleanup(w.p.clips().stopRunning)

	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2", "uid-v3")),
		testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v4")),
	}})
	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))

	w.unlink("uid-bob")
	w.p.clips().background.Wait()
	require.Positive(t, w.timers.count(), "the change was put in front of the panels, on a timer the test holds")

	doc := w.stored(t)
	require.Equal(t, "uid-dave", doc.Holder.UserID)
	require.Len(t, doc.Clips, 2)
	require.Equal(t, 3, clipperScores(doc, func(string) bool { return true })["uid-bob"].up, "the votes are all still there")
}

// Relinking brings it all back: the holder's title, and the votes of voters
// who relink.
func TestRelinkRestoresVotesAndHolder(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-v1", "uid-v2", "uid-v3", "uid-v4")
	setClipsClock(w.p, clipsNow)
	w.p.clips().markRunning()
	t.Cleanup(w.p.clips().stopRunning)

	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v2")),
		testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v3")),
	}})
	w.recompute(t, clipsNow)
	require.Equal(t, "uid-bob", w.holder(t))

	// Bob's voters both unlink: 0 to 1, and Dave has it.
	w.unlink("uid-v1")
	w.unlink("uid-v2")
	w.p.clips().background.Wait()
	require.Equal(t, "uid-dave", w.holder(t))

	// One comes back: 1 each, and the holder keeps it.
	w.relink("uid-v1")
	w.p.clips().background.Wait()
	require.Equal(t, "uid-dave", w.holder(t))

	// Both: 2 to 1, Bob's again.
	w.relink("uid-v2")
	w.p.clips().background.Wait()
	require.Equal(t, "uid-bob", w.holder(t))

	// The holder themselves, out and back.
	w.unlink("uid-bob")
	w.p.clips().background.Wait()
	require.Equal(t, "uid-dave", w.holder(t))

	w.relink("uid-bob")
	w.p.clips().background.Wait()
	require.Equal(t, "uid-bob", w.holder(t))
	require.Positive(t, w.timers.count(), "republished on a timer the test holds")
}

// Before Clips has started, and after it has stopped, a link change touches
// nothing but memory: no lookups, no writes, no goroutines.
func TestClipsLinkChangedDoesNothingWhileStopped(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dave")),
	}})

	w.p.clipsLinkChanged("uid-dave")
	w.p.clips().background.Wait()

	w.p.clips().markRunning()
	w.p.clips().stopRunning()
	w.p.clipsLinkChanged("uid-dave")
	w.p.clips().background.Wait()

	w.api.AssertNotCalled(t, "GetUser", mock.Anything)
	w.api.AssertNotCalled(t, "KVGet", mock.Anything)
	w.api.AssertNotCalled(t, "KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything)
}

// setClipsClock fixes the clock the clips store gives handlers and the
// background recompute.
func setClipsClock(p *Plugin, at time.Time) {
	p.clips().now = func() time.Time { return at }
}

// A clip one person already published of the same moment of the same stream
// is not published again: more than half of either is a duplicate.
func TestDuplicateClipRefused(t *testing.T) {
	base := clipsNow.Add(-time.Hour)
	existing := testClip(clipA, "uid-bob", base, nil)
	existing.StartAt = base.UnixMilli()
	existing.DurationMs = 60000

	candidate := func(id, clipper, handle string, startOffset, length time.Duration) clipEntry {
		entry := testClip(id, clipper, clipsNow, nil)
		entry.Handle = handle
		entry.StartAt = base.Add(startOffset).UnixMilli()
		entry.DurationMs = length.Milliseconds()

		return entry
	}

	for _, c := range []struct {
		about     string
		entry     clipEntry
		duplicate bool
	}{
		{"two thirds of the same minute", candidate(clipB, "uid-bob", someHandle, 20*time.Second, time.Minute), true},
		{"a short clip inside it", candidate(clipB, "uid-bob", someHandle, 10*time.Second, 10*time.Second), true},
		{"a long clip around it", candidate(clipB, "uid-bob", someHandle, -time.Minute, 3*time.Minute), true},
		{"a third of it", candidate(clipB, "uid-bob", someHandle, 40*time.Second, time.Minute), false},
		{"exactly half is not more than half", candidate(clipB, "uid-bob", someHandle, 30*time.Second, time.Minute), false},
		{"right after it", candidate(clipB, "uid-bob", someHandle, time.Minute, time.Minute), false},
		{"somebody else's", candidate(clipB, "uid-dave", someHandle, 0, time.Minute), false},
		{"another stream", candidate(clipB, "uid-bob", strings.Repeat("f", 32), 0, time.Minute), false},
	} {
		doc := &clipsDoc{V: 1, Clips: []clipEntry{existing}}

		changed, err := doc.addClip(c.entry)
		if c.duplicate {
			requireRefusal(t, err, http.StatusConflict, clipErrDuplicate)
			require.False(t, changed, c.about)
			require.Len(t, doc.Clips, 1, c.about)
		} else {
			require.NoError(t, err, c.about)
			require.True(t, changed, c.about)
			require.Len(t, doc.Clips, 2, c.about)
		}
	}

	require.InDelta(t, 2.0/3.0, overlapShare(0, 60, 20, 60), 1e-9)
	require.Zero(t, overlapShare(0, 60, 60, 60))
	require.Zero(t, overlapShare(0, 0, 0, 60))

	// Refused in the write itself, for a publish that raced past the check
	// before it.
	w := newClipsWorld(t, "uid-bob")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{existing}})
	_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		return doc.addClip(candidate(clipB, "uid-bob", someHandle, 0, time.Minute))
	})
	requireRefusal(t, err, http.StatusConflict, clipErrDuplicate)
	require.Len(t, w.stored(t).Clips, 1)
}

// Publishing a clip whose id is already there changes nothing, so a publish
// retried after it landed is harmless.
func TestDuplicateIdAppendIsANoOp(t *testing.T) {
	w := newClipsWorld(t, "uid-bob")
	entry := testClip(clipA, "uid-bob", clipsNow, nil)

	add := func(entry clipEntry) {
		t.Helper()

		_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
			return doc.addClip(entry)
		})
		require.NoError(t, err)
	}

	add(entry)
	stored := w.kvGet(kvClips)
	require.Len(t, w.stored(t).Clips, 1)

	again := entry
	again.Title = "a different title"
	again.StartAt += 100000
	add(again)

	require.Equal(t, stored, w.kvGet(kvClips))
	require.Equal(t, entry.Title, w.stored(t).Clips[0].Title)
}

// The caps hold in the write: thirty clips a person, five hundred in all, and
// a document no bigger than a megabyte — which a write that shrinks it, such
// as a deletion, is never refused for.
func TestClipCapsAreKeptInTheWrite(t *testing.T) {
	doc := &clipsDoc{V: 1}
	for i := 0; i < maxClipsPerUser; i++ {
		entry := testClip(clipIDFor(i), "uid-bob", clipsNow, nil)
		entry.Handle = clipIDFor(i)
		_, err := doc.addClip(entry)
		require.NoError(t, err)
	}
	_, err := doc.addClip(testClip(clipIDFor(999), "uid-bob", clipsNow, nil))
	requireRefusal(t, err, http.StatusConflict, clipErrClipCap)

	doc = &clipsDoc{V: 1}
	for i := 0; i < maxClips; i++ {
		entry := testClip(clipIDFor(i), "uid-"+clipIDFor(i), clipsNow, nil)
		doc.Clips = append(doc.Clips, entry)
	}
	_, err = doc.addClip(testClip(clipIDFor(9999), "uid-newcomer", clipsNow, nil))
	requireRefusal(t, err, http.StatusConflict, clipErrClipCap)

	// A document at the size limit takes no new vote, but gives up a clip.
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	big := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)
	big.StreamNote = strings.Repeat("x", maxClipsDocBytes)
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{big, testClip(clipB, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	_, _, err = w.p.voteClip(clipB, "uid-dave", true, clipsNow)
	requireRefusal(t, err, http.StatusConflict, clipErrClipCap)

	outcome, err := w.p.settleClips(map[string]string{clipA: clipGoneAdmin}, clipsNow)
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Len(t, w.stored(t).Clips, 1)
}

func clipIDFor(i int) string {
	return fmt.Sprintf("%016x", i)
}

// Settling hands back the posts each clip was shared in, as the document held
// them in the write that landed, not in an attempt that lost a race.
func TestSettleCapturesSharesFromTheWriteThatLanded(t *testing.T) {
	w := newClipsWorld(t, "uid-bob")
	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)
	entry.Shares = []string{"post-1"}
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{entry}})

	// Another node shares it once more between this node's read and its
	// write.
	interfered := false
	w.faults.setInterfere(func(key string, store map[string][]byte) {
		if key != kvClips || interfered {
			return
		}
		interfered = true

		doc, err := decodeClipsDoc(store[key])
		require.NoError(t, err)
		doc.Clips[0].Shares = append(doc.Clips[0].Shares, "post-2")
		doc.Rev++
		raw, err := json.Marshal(doc)
		require.NoError(t, err)
		store[key] = raw
	})

	outcome, err := w.p.settleClips(map[string]string{clipA: clipGoneClipper}, clipsNow)
	require.NoError(t, err)
	require.True(t, interfered)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, []string{"post-1", "post-2"}, outcome.Settled[0].Shares)
	require.Empty(t, w.stored(t).Clips)
}

// Lowering how long clips are kept removes nothing for a day; clips published
// after the change go by the new setting at once.
func TestRetentionShorteningWaits24Hours(t *testing.T) {
	t0 := clipsNow.UnixMilli()
	rec := effectiveRetention(retentionRecord{}, 30, t0)
	require.Equal(t, retentionRecord{Days: 30}, rec, "the first record has no grace")

	lowered := effectiveRetention(rec, 3, t0)
	require.Equal(t, retentionRecord{Days: 3, PrevDays: 30, ChangedAt: t0}, lowered)
	require.Equal(t, lowered, effectiveRetention(lowered, 3, t0+5*dayMs), "nothing more to do")

	tenDaysOld := t0 - 10*dayMs
	require.Equal(t, t0+retentionGraceMs, clipExpiresAt(tenDaysOld, lowered))

	fourDaysOld := t0 - 4*dayMs
	require.Equal(t, t0+retentionGraceMs, clipExpiresAt(fourDaysOld, lowered), "already past the new setting, kept for the grace")

	oneDayOld := t0 - dayMs
	require.Equal(t, oneDayOld+3*dayMs, clipExpiresAt(oneDayOld, lowered), "the new setting already keeps it longer than the grace")

	newer := t0 + time.Hour.Milliseconds()
	require.Equal(t, newer+3*dayMs, clipExpiresAt(newer, lowered), "published after the change")

	// Through the sweep: the record is stored, and nothing goes until the
	// day is up.
	w := newClipsWorld(t, "uid-bob")
	cfg := w.p.getConfiguration().Clone()
	cfg.ClipRetentionDays = 3
	w.p.setConfiguration(cfg)

	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{
		testClip(clipA, "uid-bob", time.UnixMilli(tenDaysOld), nil),
	}, Retention: retentionRecord{Days: 30}})

	outcome, err := w.p.expireClips(clipsNow)
	require.NoError(t, err)
	require.Empty(t, outcome.Settled)
	require.Equal(t, lowered, w.stored(t).Retention)

	outcome, err = w.p.expireClips(clipsNow.Add(23 * time.Hour))
	require.NoError(t, err)
	require.Empty(t, outcome.Settled)

	outcome, err = w.p.expireClips(clipsNow.Add(24 * time.Hour))
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, clipGoneExpired, outcome.Settled[0].Reason)
}

// Raising the setting during the grace keeps the grace for what the raise
// does not already cover, and putting it back keeps everything.
func TestRaisingDuringGraceKeepsGrace(t *testing.T) {
	t0 := clipsNow.UnixMilli()
	lowered := effectiveRetention(retentionRecord{Days: 30}, 3, t0)

	raised := effectiveRetention(lowered, 10, t0+2*time.Hour.Milliseconds())
	require.Equal(t, retentionRecord{Days: 10, PrevDays: 30, ChangedAt: t0}, raised)

	twentyDaysOld := t0 - 20*dayMs
	require.Equal(t, t0+retentionGraceMs, clipExpiresAt(twentyDaysOld, raised), "still in the grace")
	require.Equal(t, t0+5*dayMs, clipExpiresAt(t0-5*dayMs, raised), "the raise covers it")

	restored := effectiveRetention(lowered, 30, t0+3*time.Hour.Milliseconds())
	require.Equal(t, retentionRecord{Days: 30, PrevDays: 30, ChangedAt: t0}, restored)
	require.Equal(t, twentyDaysOld+30*dayMs, clipExpiresAt(twentyDaysOld, restored), "put back: everything kept")

	// Lowering again during the grace starts the day again.
	again := effectiveRetention(lowered, 1, t0+20*time.Hour.Milliseconds())
	require.Equal(t, retentionRecord{Days: 1, PrevDays: 30, ChangedAt: t0 + 20*time.Hour.Milliseconds()}, again)

	// After the grace a raise is just a raise.
	after := effectiveRetention(lowered, 10, t0+retentionGraceMs+1)
	require.Equal(t, retentionRecord{Days: 10}, after)

	// And a lowering after the grace grants the setting it replaces.
	lowerAgain := effectiveRetention(lowered, 2, t0+retentionGraceMs+1)
	require.Equal(t, retentionRecord{Days: 2, PrevDays: 3, ChangedAt: t0 + retentionGraceMs + 1}, lowerAgain)
}

// A document that will not decode stops every write to it, for good in this
// process: its bytes stay as they are, a copy is quarantined, it is said once,
// and readers go on with the last good copy this node had.
func TestUnreadableClipsDocHaltsWrites(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 3, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	good, err := w.p.clipsDocument(clipsNow)
	require.NoError(t, err)
	require.EqualValues(t, 3, good.Rev)

	garbage := []byte(`{"v":1,"rev":4,"clips":[{"id":`)
	w.kvSet(kvClips, garbage)

	_, _, err = w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.ErrorIs(t, err, errClipsHalted)
	status, code := clipErrorFor(err)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, clipErrStore, code)

	require.Equal(t, garbage, w.kvGet(kvClips), "never written over")
	require.Equal(t, garbage, w.kvGet(kvQuarantinePrefix+kvClips))
	halted, reason := w.p.clips().halt.state()
	require.True(t, halted)
	require.Equal(t, "the clips document could not be read", reason)

	// From now on nothing even reads it.
	var reads []string
	w.faults.lock.Lock()
	w.faults.onRead = func(key string) { reads = append(reads, key) }
	w.faults.lock.Unlock()

	_, err = w.p.settleClips(map[string]string{clipA: clipGoneClipper}, clipsNow)
	require.ErrorIs(t, err, errClipsHalted)
	_, err = w.p.expireClips(clipsNow)
	require.ErrorIs(t, err, errClipsHalted)
	require.ErrorIs(t, w.p.recomputeBestClipper(clipsNow), errClipsHalted)
	_, err = w.p.readClipsFresh(clipsNow)
	require.ErrorIs(t, err, errClipsHalted)

	doc, err := w.p.clipsDocument(clipsNow.Add(time.Hour))
	require.NoError(t, err)
	require.Same(t, good, doc, "the last good copy")

	w.faults.lock.Lock()
	require.Empty(t, reads)
	w.faults.lock.Unlock()

	w.api.AssertNumberOfCalls(t, "LogError", 2) // the quarantine, and the halt
	w.api.AssertCalled(t, "LogError", "Clips have stopped so that the stored clips are not overwritten",
		"reason", "the clips document could not be read", "error", mock.Anything)

	// A restart with nothing cached has nothing to serve.
	restarted := clipsWorldOn(t, w.store, "uid-bob")
	_, err = restarted.p.clipsDocument(clipsNow)
	require.ErrorIs(t, err, errClipsHalted)

	// A document from a newer version is as unreadable.
	newer := clipsWorldOn(t, map[string][]byte{kvClips: []byte(`{"v":2,"rev":9,"clips":[]}`)}, "uid-bob")
	require.ErrorIs(t, newer.p.recomputeBestClipper(clipsNow), errClipsHalted)
	require.Equal(t, `{"v":2,"rev":9,"clips":[]}`, string(newer.kvGet(kvClips)))
}

// This node's copy of the document is read again only once it is a minute
// old, a write here replaces it at once, and an older copy never replaces a
// newer one.
func TestClipsCacheTakesOnlyNewerRevs(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave")
	w.seed(t, &clipsDoc{V: 1, Rev: 5, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

	var reads []string
	w.faults.lock.Lock()
	w.faults.onRead = func(key string) {
		if key == kvClips {
			reads = append(reads, key)
		}
	}
	w.faults.lock.Unlock()
	readCount := func() int {
		w.faults.lock.Lock()
		defer w.faults.lock.Unlock()

		return len(reads)
	}

	require.Nil(t, w.p.clips().cached(), "nothing is read until somebody asks")

	doc, err := w.p.clipsDocument(clipsNow)
	require.NoError(t, err)
	require.EqualValues(t, 5, doc.Rev)
	require.Equal(t, 1, readCount())

	// Another node writes.
	w.seed(t, &clipsDoc{V: 1, Rev: 6, Clips: []clipEntry{testClip(clipB, "uid-dave", clipsNow, nil)}})

	doc, err = w.p.clipsDocument(clipsNow.Add(59 * time.Second))
	require.NoError(t, err)
	require.EqualValues(t, 5, doc.Rev)
	require.Equal(t, 1, readCount())

	doc, err = w.p.clipsDocument(clipsNow.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 6, doc.Rev)
	require.Equal(t, 2, readCount())

	// An older document offered later is not taken.
	w.p.clips().offer(&clipsDoc{V: 1, Rev: 4}, clipsNow.Add(61*time.Second))
	require.EqualValues(t, 6, w.p.clips().cached().Rev)

	// A write through this node is the copy at once.
	_, _, err = w.p.voteClip(clipB, "uid-bob", true, clipsNow.Add(62*time.Second))
	require.NoError(t, err)
	require.EqualValues(t, 7, w.p.clips().cached().Rev)
	require.Contains(t, w.p.clips().cached().Clips[0].Voters, "uid-bob")

	// A re-read that fails keeps the copy.
	w.faults.failRead(kvClips)
	doc, err = w.p.clipsDocument(clipsNow.Add(5 * time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 7, doc.Rev)

	// The fresh read used before drafts and publishes fails closed.
	_, err = w.p.readClipsFresh(clipsNow.Add(5 * time.Minute))
	require.ErrorIs(t, err, errStore)

	// With nothing cached, a failed read is an error, not an empty list.
	cold := clipsWorldOn(t, map[string][]byte{}, "uid-bob")
	cold.faults.failRead(kvClips)
	_, err = cold.p.clipsDocument(clipsNow)
	require.ErrorIs(t, err, errStore)
}

// Who counts is looked up once per ten minutes, a failing lookup keeps what
// was known and is not retried for a minute, and a deleted account is an
// answer.
func TestEligibilityIsCachedForTenMinutesAndKeptOnErrors(t *testing.T) {
	w := newClipsWorld(t, "uid-bob")
	s := w.p.clips()
	verdict := func(userID string) clipVerdict {
		return s.verdictsFor([]string{userID}, nil).people[userID]
	}

	w.p.lookUpClipPeople([]string{"uid-bob"}, clipsNow, true)
	require.Equal(t, clipVerdict{eligible: true, known: true}, verdict("uid-bob"))
	require.Equal(t, 1, w.lookupsOf("uid-bob"))

	w.p.lookUpClipPeople([]string{"uid-bob"}, clipsNow.Add(9*time.Minute), true)
	require.Equal(t, 1, w.lookupsOf("uid-bob"))

	w.failUser("uid-bob", true)
	w.p.lookUpClipPeople([]string{"uid-bob"}, clipsNow.Add(10*time.Minute), true)
	require.Equal(t, 2, w.lookupsOf("uid-bob"))
	require.Equal(t, clipVerdict{eligible: true, known: true}, verdict("uid-bob"), "what was known stands")

	w.p.lookUpClipPeople([]string{"uid-bob"}, clipsNow.Add(10*time.Minute+30*time.Second), true)
	require.Equal(t, 2, w.lookupsOf("uid-bob"), "not asked again straight away")

	w.p.lookUpClipPeople([]string{"uid-bob"}, clipsNow.Add(11*time.Minute), true)
	require.Equal(t, 3, w.lookupsOf("uid-bob"))

	// Never looked up successfully: not counted, and not known.
	w.person("uid-new")
	w.link("uid-new", "videokey-new")
	w.failUser("uid-new", true)
	w.p.lookUpClipPeople([]string{"uid-new"}, clipsNow, true)
	require.Equal(t, clipVerdict{}, verdict("uid-new"))

	// Deleted outright: known, and not counted.
	w.p.lookUpClipPeople([]string{"uid-nobody"}, clipsNow, true)
	require.Equal(t, clipVerdict{known: true}, verdict("uid-nobody"))

	// Somebody not linked: known, and not counted, whatever their account.
	w.person("uid-loner")
	w.p.lookUpClipPeople([]string{"uid-loner"}, clipsNow, true)
	require.Equal(t, clipVerdict{known: true}, verdict("uid-loner"))

	// The gate's own answer counts as a lookup of the link.
	w.person("uid-caller")
	w.link("uid-caller", "videokey-caller")
	recorder := httptest.NewRecorder()
	_, linked := w.p.requireClipsAccess(recorder, "uid-caller", false, false)
	require.True(t, linked)
	facts := s.factsOf("uid-caller")
	require.True(t, facts.linkKnown)
	require.True(t, facts.linked)
	require.False(t, facts.userKnown)

	// Somebody let in is let through to look, and is not linked for it.
	w.person("uid-guest")
	_, err := w.p.letIn("uid-guest", "uid-boss", clipsNow)
	require.NoError(t, err)
	_, allowed := w.p.requireClipsAccess(httptest.NewRecorder(), "uid-guest", true, false)
	require.True(t, allowed)
	facts = s.factsOf("uid-guest")
	require.True(t, facts.linkKnown)
	require.False(t, facts.linked, "let in is not linked, for votes as for everything else")
}

// The media routes' and the cards' access cache keeps a yes for ten seconds,
// never a failure, and forgets a yes the moment the link changes — including
// a change that lands while the yes is being read. A no is the gate's to keep,
// for two seconds; this cache never holds one.
func TestAccessSeenCacheKeepsOnlyFreshPositives(t *testing.T) {
	w := newClipsWorld(t, "uid-bob")
	w.person("uid-loner")
	current := clipsNow
	w.p.clips().now = func() time.Time { return current }

	var lock sync.Mutex
	var reads []string
	var during func(key string)
	w.faults.lock.Lock()
	w.faults.onRead = func(key string) {
		lock.Lock()
		defer lock.Unlock()

		if strings.HasPrefix(key, kvLinkByUserPrefix) {
			reads = append(reads, key)
		}
		if during != nil {
			during(key)
		}
	}
	w.faults.lock.Unlock()
	gate := func(userID string) (string, bool, int) {
		lock.Lock()
		reads = nil
		lock.Unlock()

		level, key, err := w.p.accessSeenOf(userID)

		lock.Lock()
		defer lock.Unlock()

		return key, err == nil && level == accessLinked, len(reads)
	}

	key, linked, read := gate("uid-bob")
	require.Equal(t, "videokey-bob", key)
	require.True(t, linked)
	require.Equal(t, 1, read)

	current = clipsNow.Add(9 * time.Second)
	key, linked, read = gate("uid-bob")
	require.Equal(t, "videokey-bob", key)
	require.True(t, linked)
	require.Zero(t, read, "a yes is kept")

	current = clipsNow.Add(10 * time.Second)
	_, _, read = gate("uid-bob")
	require.Equal(t, 1, read, "for ten seconds")

	for range 3 {
		_, linked, read = gate("uid-loner")
		require.False(t, linked)
		require.Equal(t, 1, read, "a no is never kept here")
	}

	w.faults.failRead(kvLinkByUserPrefix + "uid-bob")
	current = clipsNow.Add(time.Minute)
	for range 2 {
		_, linked, read = gate("uid-bob")
		require.False(t, linked)
		require.Equal(t, 1, read, "nor a failure")
	}
	w.faults.healRead(kvLinkByUserPrefix + "uid-bob")

	_, linked, _ = gate("uid-bob")
	require.True(t, linked)
	w.unlink("uid-bob")
	_, linked, read = gate("uid-bob")
	require.False(t, linked, "an unlink is seen at once")
	require.Equal(t, 1, read)

	// A yes read just as the link changes is not kept.
	w.link("uid-bob", "videokey-bob")
	lock.Lock()
	during = func(key string) {
		if key == kvLinkByUserPrefix+"uid-bob" {
			w.p.clipsLinkChanged("uid-bob")
		}
	}
	lock.Unlock()
	_, linked, _ = gate("uid-bob")
	require.True(t, linked)

	lock.Lock()
	during = nil
	lock.Unlock()
	_, linked, read = gate("uid-bob")
	require.True(t, linked)
	require.Equal(t, 1, read, "read again, because the first yes may have been the old link")
}

// The streamer of a stream is whoever has linked its key now, however its case
// was typed. Somebody else proving the key takes that from them at once.
func TestStreamerOfFollowsTheCurrentLink(t *testing.T) {
	w := newClipsWorld(t, "uid-bob")
	w.person("uid-dave")
	w.link("uid-dave", "VideoKey-Dave")
	tag := w.p.streamTag("videokey-dave")

	streamer, err := w.p.streamerOf("uid-dave", tag)
	require.NoError(t, err)
	require.True(t, streamer, "a key in another case is the same stream")
	require.True(t, w.p.streamOf("VIDEOKEY-DAVE", tag))

	streamer, err = w.p.streamerOf("uid-bob", tag)
	require.NoError(t, err)
	require.False(t, streamer)

	for _, nobody := range []string{"", "uid-nobody"} {
		streamer, err = w.p.streamerOf(nobody, tag)
		require.NoError(t, err)
		require.False(t, streamer)
	}
	require.False(t, w.p.streamOf("videokey-dave", ""), "an empty tag is nobody's stream")
	require.False(t, w.p.streamOf("", ""))

	// Bob proves Dave's key.
	require.NoError(t, w.p.setLink("uid-bob", "videokey-dave", "token-of-daves-stream"))

	streamer, err = w.p.streamerOf("uid-dave", tag)
	require.NoError(t, err)
	require.False(t, streamer)

	streamer, err = w.p.streamerOf("uid-bob", tag)
	require.NoError(t, err)
	require.True(t, streamer)

	w.faults.failRead(kvLinkByUserPrefix + "uid-bob")
	_, err = w.p.streamerOf("uid-bob", tag)
	require.Error(t, err, "a link that cannot be read is not a no")
}

// What a reader is handed about a clip says what they may do with it, and
// never carries the stream tag or the posts it was shared in.
func TestClipViewsCarryNoTagOrShares(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan"))
	entry.StreamTag = w.p.streamTag("videokey-dave")
	entry.Shares = []string{"post-id-of-a-share"}
	entry.Reports = map[string]int64{"uid-dan": 1}
	entry.Thumb = "a1b2c3d4"
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{entry}})
	w.recompute(t, clipsNow)

	kept := retentionRecord{Days: 30}
	counts := w.counts(t)
	names := w.p.clipNames(clipsNow, true)

	clipper := clipViewOf(&entry, clipViewer{UserID: "uid-bob", Tag: w.p.streamTag("videokey-bob")}, kept, counts, names)
	require.True(t, clipper.Mine)
	require.False(t, clipper.CanVote)
	require.False(t, clipper.CanReport)
	require.True(t, clipper.CanDelete)
	require.False(t, clipper.OfMyStream)
	require.Equal(t, 1, clipper.Upvotes)
	require.Equal(t, entry.CreatedAt+30*dayMs, clipper.ExpiresAt)
	require.Equal(t, clipPerson{UserID: "uid-bob", Username: "bob", DisplayName: "Bob", Name: "Bob"}, clipper.Clipper)
	require.Equal(t, clipPerson{UserID: "uid-dave", Username: "dave", DisplayName: "Dave", Name: "Dave"}, clipper.Streamer)
	require.Nil(t, clipper.Reports)

	streamer := clipViewOf(&entry, clipViewer{UserID: "uid-dave", Tag: w.p.streamTag("VideoKey-Dave")}, kept, counts, names)
	require.True(t, streamer.OfMyStream)
	require.True(t, streamer.CanDelete)
	require.True(t, streamer.CanVote)

	voter := clipViewOf(&entry, clipViewer{UserID: "uid-dan", Tag: w.p.streamTag("videokey-dan")}, kept, counts, names)
	require.True(t, voter.Voted)
	require.True(t, voter.Reported)
	require.False(t, voter.CanDelete)

	nobodysTag := clipViewOf(&entry, clipViewer{UserID: "uid-dan"}, kept, counts, names)
	require.False(t, nobodysTag.OfMyStream)

	admin := clipViewOf(&entry, clipViewer{UserID: "uid-boss", Admin: true}, kept, counts, names)
	require.True(t, admin.CanDelete)
	require.Nil(t, admin.Reports)

	bot := clipViewOf(&entry, clipViewer{UserID: "uid-robot", Bot: true}, kept, counts, names)
	require.False(t, bot.CanVote)
	require.False(t, bot.CanReport)

	console := clipViewOf(&entry, clipViewer{UserID: "uid-boss", Console: true}, kept, counts, names)
	require.True(t, console.CanDelete)
	require.NotNil(t, console.Reports)
	require.Equal(t, 1, *console.Reports)

	card := clipCardOf(&entry, names)
	require.Equal(t, clipCardView{
		ID: clipA, Title: entry.Title, CreatedAt: entry.CreatedAt, DurationMs: 45000, Thumb: "a1b2c3d4",
		Clipper:  clipPerson{UserID: "uid-bob", Username: "bob", DisplayName: "Bob", Name: "Bob"},
		Streamer: clipPerson{UserID: "uid-dave", Username: "dave", DisplayName: "Dave", Name: "Dave"},
	}, card)

	for _, view := range []any{clipper, streamer, admin, console, card} {
		encoded, err := json.Marshal(view)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), entry.StreamTag)
		require.NotContains(t, string(encoded), "post-id-of-a-share")
		require.NotContains(t, string(encoded), "videokey-")
		require.NotContains(t, strings.ToLower(string(encoded)), "streamtag")
		require.NotContains(t, strings.ToLower(string(encoded)), "shares")
	}

	// A stream nobody was attributed to, and people who cannot be read.
	orphan := entry
	orphan.StreamerID = ""
	orphan.StreamName = ""
	orphan.ClipperID = "uid-nobody"
	orphanView := clipCardOf(&orphan, names)
	require.Equal(t, clipPerson{Name: unnamedClipStream}, orphanView.Streamer)
	require.Equal(t, clipPerson{UserID: "uid-nobody", Name: unknownClipPerson}, orphanView.Clipper)

	unreadable := entry
	unreadable.StreamerID = "uid-nobody"
	require.Equal(t, clipPerson{UserID: "uid-nobody", Name: "Dave"}, clipStreamerOf(&unreadable, names))
}

// The award in the snapshot: the holder and their score, and their best clip
// still here by id alone; nothing while Clips is off, and nothing when nobody
// holds it.
func TestBestClipperHiddenWhileClipsOff(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-v1", "uid-v2", "uid-v3")
	w.seed(t, &clipsDoc{
		V: 1, Rev: 1,
		Clips: []clipEntry{
			testClip(clipA, "uid-bob", clipsNow.Add(-3*time.Hour), votesAt(clipsNow.Add(-2*time.Hour), "uid-v1", "uid-v2")),
			testClip(clipB, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-v1", "uid-v3")),
			testClip(clipC, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-v2")),
			testClip(clipD, "uid-bob", clipsNow.Add(-time.Minute), nil),
		},
		Banked: map[string]bankedScore{"uid-bob": {Up: 4, Clips: 1, LastUpAt: 1}},
	})
	w.recompute(t, clipsNow)

	doc := w.stored(t)
	kept := effectiveRetention(doc.Retention, 30, clipsNow.UnixMilli())
	names := w.p.clipNames(clipsNow, true)

	require.Nil(t, bestClipperViewOf(doc, false, kept, clipsNow.UnixMilli(), w.counts(t), names))
	require.Equal(t, "uid-bob", doc.Holder.UserID, "hidden, and still held")

	view := bestClipperViewOf(doc, true, kept, clipsNow.UnixMilli(), w.counts(t), names)
	require.Equal(t, &bestClipperView{
		UserID: "uid-bob", Username: "bob", DisplayName: "Bob",
		Upvotes: 9, Clips: 5, Since: clipsNow.UnixMilli(),
		BestClip: &bestClipRef{ID: clipB},
	}, view, "two clips on 2: the newer")

	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"bestClip":{"id":"`+clipB+`"}`)
	require.NotContains(t, string(encoded), "title")

	require.Nil(t, bestClipperViewOf(&clipsDoc{V: 1}, true, kept, clipsNow.UnixMilli(), w.counts(t), names))
	require.Nil(t, bestClipperViewOf(nil, true, kept, clipsNow.UnixMilli(), w.counts(t), names))
}

// What every snapshot says about Clips: the count and newest of the clips
// still here, and a version that moves with what a list would show and with
// nothing else, the same however the document's maps were ordered.
func TestClipsVersionMovesWithWhatTheListShows(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	cfg := w.p.getConfiguration()
	expired := testClip(clipA, "uid-bob", clipsNow.Add(-31*24*time.Hour), nil)
	first := testClip(clipB, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-dave", "uid-dan"))
	second := testClip(clipC, "uid-dave", clipsNow.Add(-time.Hour), nil)
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{expired, first, second}, Retention: retentionRecord{Days: 30}})
	w.recompute(t, clipsNow)

	now := clipsNow.UnixMilli()
	viewOf := func(doc *clipsDoc) *clipsView {
		return clipsViewOf(doc, cfg, true, now, w.p.clips().verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible, w.p.handleSecret)
	}

	view := viewOf(w.stored(t))
	require.Equal(t, 2, view.Count, "the expired one is not counted")
	require.Equal(t, second.CreatedAt, view.Newest)
	require.Len(t, view.Version, 16)
	require.True(t, view.Enabled)
	require.Equal(t, 120, view.LengthSeconds)
	require.Equal(t, 30, view.RetentionDays)

	for range 20 {
		require.Equal(t, view.Version, viewOf(w.stored(t)).Version, "decoded afresh, maps in another order")
	}

	moved := func(change func(doc *clipsDoc)) string {
		doc := w.stored(t)
		change(doc)

		return viewOf(doc).Version
	}
	require.NotEqual(t, view.Version, moved(func(doc *clipsDoc) { doc.Clips[2].Title = "renamed" }))
	require.NotEqual(t, view.Version, moved(func(doc *clipsDoc) { doc.Clips[2].Thumb = "deadbeef" }))
	require.NotEqual(t, view.Version, moved(func(doc *clipsDoc) { delete(doc.Clips[1].Voters, "uid-dan") }))
	require.NotEqual(t, view.Version, moved(func(doc *clipsDoc) { doc.Clips = doc.Clips[:2] }))
	require.Equal(t, view.Version, moved(func(doc *clipsDoc) { doc.Clips[2].Shares = []string{"post"} }), "a share is not something a list shows")
	require.Equal(t, view.Version, moved(func(doc *clipsDoc) { doc.Clips[1].Reports = map[string]int64{"uid-dan": 1} }))

	// When a clip goes is on its card, so a change in how long clips are
	// kept moves the version; the time passing does not.
	shorter := cfg.Clone()
	shorter.ClipRetentionDays = 20
	kept := clipsViewOf(w.stored(t), shorter, true, now, w.counts(t), w.p.handleSecret)
	require.Equal(t, view.Count, kept.Count, "the same clips, kept for less long")
	require.NotEqual(t, view.Version, kept.Version)

	// Until the sweep stores that change, a clip old enough to be in its
	// grace goes a day after now, whenever now is: the version does not
	// move with it on every poll.
	graced := w.stored(t)
	graced.Clips = append(graced.Clips, testClip(clipD, "uid-dave", clipsNow.Add(-25*24*time.Hour), nil))
	minuteOn := now + int64(time.Minute/time.Millisecond)
	require.Equal(t,
		clipsViewOf(graced, shorter, true, now, w.counts(t), w.p.handleSecret).Version,
		clipsViewOf(graced, shorter, true, minuteOn, w.counts(t), w.p.handleSecret).Version)
	later := clipsViewOf(w.stored(t), cfg, true, now+int64(time.Hour/time.Millisecond), w.counts(t), w.p.handleSecret)
	require.Equal(t, view.Version, later.Version, "an hour later, nothing on a card has moved")

	// Keyed: another installation's secret gives another version, and with
	// no secret there is none, rather than one anybody could work out.
	stored := w.stored(t)
	counts := w.counts(t)
	require.NotEqual(t, view.Version, clipsViewOf(stored, cfg, true, now, counts, []byte("another installation's secret!!")).Version)
	require.Empty(t, clipsViewOf(stored, cfg, true, now, counts, nil).Version)
	require.Equal(t, 2, clipsViewOf(stored, cfg, true, now, counts, nil).Count, "the count is there all the same")

	// An unset configuration, before Mattermost has said anything, keeps clips
	// for the default rather than for no days at all.
	unset := clipsViewOf(w.stored(t), &configuration{}, false, now, w.counts(t), w.p.handleSecret)
	require.Equal(t, clipRetentionDefaultDays, unset.RetentionDays)
	require.Equal(t, 2, unset.Count)
	bare := clipsWorldOn(t, map[string][]byte{kvClips: w.kvGet(kvClips)}, "uid-bob", "uid-dave", "uid-dan")
	bare.p.setConfiguration(nil)
	_, _, err := bare.p.voteClip(clipC, "uid-bob", true, clipsNow)
	require.NoError(t, err, "a clip an hour old has not expired")

	empty := viewOf(&clipsDoc{V: 1})
	require.Equal(t, &clipsView{Enabled: true, LengthSeconds: 120, RetentionDays: 30}, empty)
	require.Equal(t, &clipsView{LengthSeconds: 120, RetentionDays: 30}, clipsViewOf(nil, cfg, false, now, nil, w.p.handleSecret), "nothing read yet")
}
