package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// channelOf is a channel id the shape Mattermost makes them, 26 letters and
// digits, that still reads as a name in a failure.
func channelOf(name string) string {
	return (name + strings.Repeat("x", 26))[:26]
}

var (
	townSquare = channelOf("townsquare")
	offTopic   = channelOf("offtopic")
)

// shareServer is the part of Mattermost a share touches — channels, who is in
// them and may post there, teams, bots and plugins, and the posts themselves
// — with both hooks run the way Mattermost runs them: MessageWillBePosted on
// a copy of the post inside CreatePost [v9 S/app/post.go:304-305], and
// MessageWillBeUpdated inside UpdatePost on a copy of the stored post with
// the update's message and props, nil refusing it [v9 S/app/post.go:628-707].
// Both cross the plugin RPC boundary as gob, the way go-plugin carries them:
// see hookPosted and hookUpdated. A hook that worked only in process would
// pass a test that called it in process.
//
// The hooks run in the same goroutine as the call that set them off, where
// on a real server they arrive over RPC while that call waits. So a lock the
// share route or the marker held across CreatePost or UpdatePost, and that a
// hook then wanted, would hang the test rather than pass it.
type shareServer struct {
	t *testing.T
	p *Plugin

	lock     sync.Mutex
	siteURL  string
	restrict string

	channels     map[string]*model.Channel
	members      map[string]map[string]bool
	posters      map[string]map[string]bool
	admins       map[string]bool
	teams        map[string][]string
	teamsFail    map[string]bool
	groupFails   bool
	onGetChannel func()

	bots    map[string]*model.Bot
	plugins []string

	posts        map[string]*model.Post
	sent         []*model.Post
	creates      int
	createFail   *model.AppError
	duringCreate func()
	nextPost     int
	gets         int

	deleted    []string
	deleteFail *model.AppError

	updates      map[string]int
	beforeUpdate func(postID string)
	lose         int

	// Posts GetPost answers as not found, and as the server's trouble.
	getFails  map[string]bool
	getBroken map[string]bool
}

func newShareServer(t *testing.T, w *clipsWorld) *shareServer {
	t.Helper()

	srv := &shareServer{
		t:         t,
		p:         w.p,
		siteURL:   "https://chat.example.com/",
		restrict:  model.DirectMessageAny,
		channels:  map[string]*model.Channel{},
		members:   map[string]map[string]bool{},
		posters:   map[string]map[string]bool{},
		admins:    map[string]bool{},
		teams:     map[string][]string{},
		teamsFail: map[string]bool{},
		bots:      map[string]*model.Bot{},
		posts:     map[string]*model.Post{},
		updates:   map[string]int{},
		getFails:  map[string]bool{},
		getBroken: map[string]bool{},
	}

	// Registered before startClipsIn's own, so this is the one answered:
	// the files where startClipsIn would put them, and the two settings a
	// share reads.
	dir := t.TempDir()
	w.api.On("GetConfig").Return(func() *model.Config {
		cfg := clipsStorageConfig(dir, model.ImageDriverLocal)

		srv.lock.Lock()
		defer srv.lock.Unlock()

		cfg.ServiceSettings.SiteURL = model.NewPointer(srv.siteURL)
		cfg.TeamSettings.RestrictDirectMessage = model.NewPointer(srv.restrict)

		return cfg
	})

	w.api.On("GetChannel", mock.Anything).Return(srv.getChannel)
	w.api.On("GetChannelMember", mock.Anything, mock.Anything).Return(srv.getChannelMember)
	w.api.On("HasPermissionToChannel", mock.Anything, mock.Anything, mock.Anything).Return(srv.hasPermissionToChannel)
	w.api.On("GetChannelMembers", mock.Anything, mock.Anything, mock.Anything).Return(srv.getChannelMembers)
	w.api.On("GetTeamsForUser", mock.Anything).Return(srv.getTeamsForUser)
	w.api.On("CreatePost", mock.Anything).Return(srv.createPost)
	w.api.On("UpdatePost", mock.Anything).Return(srv.updatePost)
	w.api.On("GetPost", mock.Anything).Return(srv.getPost)
	w.api.On("DeletePost", mock.Anything).Return(srv.deletePost)
	w.api.On("GetBot", mock.Anything, mock.Anything).Return(srv.getBot)
	w.api.On("GetPlugins").Return(srv.getPlugins)

	return srv
}

// overRPC carries a value across the plugin RPC boundary as go-plugin's
// net/rpc does, as gob: from is encoded and decoded into into, which keeps
// whatever into already held that the encoding left out — every field that
// is zero, and every map entry that is not sent.
func overRPC(from, into any) error {
	var wire bytes.Buffer
	if err := gob.NewEncoder(&wire).Encode(from); err != nil {
		return err
	}

	return gob.NewDecoder(&wire).Decode(into)
}

// hookPosted is MessageWillBePosted as Mattermost calls it, on every version
// from 9.0 to master: a copy of the post goes over to the plugin, and the
// answer comes back decoded into that same copy [v9 server/public/plugin/
// client_rpc.go:639-647; master :1126-1135], so a field the hook left zero,
// or a prop it deleted, is as Mattermost sent it. It answers the post
// Mattermost carries on with [v9 S/app/post.go:304-324], and the reason when
// the hook refused it.
func hookPosted(p *Plugin, post *model.Post) (*model.Post, string, error) {
	sent := post.ForPlugin()

	var args plugin.Z_MessageWillBePostedArgs
	if err := overRPC(&plugin.Z_MessageWillBePostedArgs{A: &plugin.Context{}, B: sent}, &args); err != nil {
		return nil, "", err
	}

	replacement, reason := p.MessageWillBePosted(args.A, args.B)

	returns := plugin.Z_MessageWillBePostedReturns{A: sent}
	if err := overRPC(&plugin.Z_MessageWillBePostedReturns{A: replacement, B: reason}, &returns); err != nil {
		return nil, "", err
	}

	return returns.A, returns.B, nil
}

// hookUpdated is MessageWillBeUpdated as Mattermost calls it, on every
// version plugin.json allows: the answer is decoded into a post of its own
// [v9.3.0 server/public/plugin/client_rpc.go:679-690; v11.11.1 :935-947], so
// a nil comes back nil and a prop left out is gone.
func hookUpdated(p *Plugin, newPost, oldPost *model.Post) (*model.Post, string, error) {
	var args plugin.Z_MessageWillBeUpdatedArgs
	if err := overRPC(&plugin.Z_MessageWillBeUpdatedArgs{A: &plugin.Context{}, B: newPost.ForPlugin(), C: oldPost.ForPlugin()}, &args); err != nil {
		return nil, "", err
	}

	result, reason := p.MessageWillBeUpdated(args.A, args.B, args.C)

	var returns plugin.Z_MessageWillBeUpdatedReturns
	if err := overRPC(&plugin.Z_MessageWillBeUpdatedReturns{A: result, B: reason}, &returns); err != nil {
		return nil, "", err
	}

	return returns.A, returns.B, nil
}

// posted and updated are hookPosted and hookUpdated in a test's own
// goroutine.
func posted(t *testing.T, p *Plugin, post *model.Post) (*model.Post, string) {
	t.Helper()

	result, reason, err := hookPosted(p, post)
	require.NoError(t, err)

	return result, reason
}

func updated(t *testing.T, p *Plugin, newPost, oldPost *model.Post) (*model.Post, string) {
	t.Helper()

	result, reason, err := hookUpdated(p, newPost, oldPost)
	require.NoError(t, err)

	return result, reason
}

func (srv *shareServer) set(change func(srv *shareServer)) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	change(srv)
}

// addChannel puts a channel on the server with these people in it, each of
// whom may post there.
func (srv *shareServer) addChannel(channel *model.Channel, members ...string) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.channels[channel.Id] = channel
	srv.members[channel.Id] = map[string]bool{}
	srv.posters[channel.Id] = map[string]bool{}
	for _, member := range members {
		srv.members[channel.Id][member] = true
		srv.posters[channel.Id][member] = true
	}
}

func notFound(where string) *model.AppError {
	return model.NewAppError(where, "app.missing", nil, "", http.StatusNotFound)
}

func (srv *shareServer) getChannel(channelID string) (*model.Channel, *model.AppError) {
	srv.lock.Lock()
	during := srv.onGetChannel
	srv.lock.Unlock()

	if during != nil {
		during()
	}

	srv.lock.Lock()
	defer srv.lock.Unlock()

	channel, found := srv.channels[channelID]
	if !found {
		return nil, notFound("GetChannel")
	}
	copied := *channel

	return &copied, nil
}

func (srv *shareServer) getChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	if !srv.members[channelID][userID] {
		return nil, notFound("GetChannelMember")
	}

	return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
}

// hasPermissionToChannel is Mattermost's, which looks at roles and nothing
// else: neither whether the channel is archived nor whether the person is in
// it, and a system admin has create_post in every channel [v9
// S/app/authorization.go].
func (srv *shareServer) hasPermissionToChannel(userID, channelID string, permission *model.Permission) bool {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	if permission == nil || permission.Id != model.PermissionCreatePost.Id {
		return false
	}

	return srv.admins[userID] || srv.posters[channelID][userID]
}

// addBot makes somebody a bot with this owner: a user id, a plugin's id, or
// anything else.
func (srv *shareServer) addBot(w *clipsWorld, userID, username, ownerID string) {
	w.person(userID)
	w.changeUser(userID, func(user *model.User) {
		user.IsBot = true
		user.Username = username
	})

	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.bots[userID] = &model.Bot{UserId: userID, Username: username, OwnerId: ownerID}
}

func (srv *shareServer) getBot(userID string, _ bool) (*model.Bot, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	bot, found := srv.bots[userID]
	if !found {
		return nil, notFound("GetBot")
	}
	copied := *bot

	return &copied, nil
}

func (srv *shareServer) getPlugins() ([]*model.Manifest, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	manifests := []*model.Manifest{{Id: manifestID}}
	for _, pluginID := range srv.plugins {
		manifests = append(manifests, &model.Manifest{Id: pluginID})
	}

	return manifests, nil
}

func (srv *shareServer) getChannelMembers(channelID string, _, _ int) (model.ChannelMembers, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	if srv.groupFails {
		return nil, model.NewAppError("GetChannelMembers", "app.failed", nil, "injected failure", http.StatusInternalServerError)
	}

	members := model.ChannelMembers{}
	for userID := range srv.members[channelID] {
		members = append(members, model.ChannelMember{ChannelId: channelID, UserId: userID})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UserId < members[j].UserId })

	return members, nil
}

func (srv *shareServer) getTeamsForUser(userID string) ([]*model.Team, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	if srv.teamsFail[userID] {
		return nil, model.NewAppError("GetTeamsForUser", "app.failed", nil, "injected failure", http.StatusInternalServerError)
	}

	teams := []*model.Team{}
	for _, teamID := range srv.teams[userID] {
		teams = append(teams, &model.Team{Id: teamID})
	}

	return teams, nil
}

// createPost is the plugin API's CreatePost: from_plugin is added [v9
// S/app/plugin_api.go:636-644], then MessageWillBePosted sees a copy over
// RPC, and what comes back is what is saved.
func (srv *shareServer) createPost(post *model.Post) (*model.Post, *model.AppError) {
	srv.lock.Lock()
	srv.creates++
	asked := post.Clone()
	asked.SetProps(copyProps(post.GetProps()))
	srv.sent = append(srv.sent, asked)
	fail, during := srv.createFail, srv.duringCreate
	srv.lock.Unlock()

	if during != nil {
		during()
	}
	if fail != nil {
		return nil, fail
	}

	post.AddProp("from_plugin", "true")
	replacement, reason, err := hookPosted(srv.p, post)
	if err != nil {
		srv.t.Errorf("MessageWillBePosted over RPC: %v", err)
		return nil, model.NewAppError("createPost", "app.rpc", nil, err.Error(), http.StatusInternalServerError)
	}
	if reason != "" {
		return nil, model.NewAppError("createPost", "Post rejected by plugin. "+reason, nil, "", http.StatusBadRequest)
	}
	post = replacement

	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.nextPost++
	stored := post.Clone()
	stored.Id = fmt.Sprintf("post%022d", srv.nextPost)
	stored.CreateAt = clipsNow.UnixMilli()
	srv.posts[stored.Id] = stored

	return stored.Clone(), nil
}

// updatePost is the plugin API's UpdatePost, which never updates safely [v9
// S/app/plugin_api.go:723-729]: the stored post is cloned, so its type cannot
// change; a different message sets EditAt, and so do different files; the
// props, files, pin and the rest are the update's; MessageWillBeUpdated sees
// copies over RPC, and nil refuses the update [v10.11.0 S/app/post.go:
// 731-775]. An archived channel refuses it first.
//
// lose is updates that another node's write lands over straight after, as
// Mattermost's post store, which does not check versions, would let it.
func (srv *shareServer) updatePost(post *model.Post) (*model.Post, *model.AppError) {
	srv.lock.Lock()
	before := srv.beforeUpdate
	srv.beforeUpdate = nil
	srv.lock.Unlock()

	if before != nil {
		before(post.Id)
	}

	srv.lock.Lock()
	old, found := srv.posts[post.Id]
	var channel *model.Channel
	if found {
		channel = srv.channels[old.ChannelId]
	}
	srv.lock.Unlock()

	if !found {
		return nil, model.NewAppError("UpdatePost", "app.post.get.app_error", nil, "", http.StatusNotFound)
	}
	if channel != nil && channel.DeleteAt != 0 {
		return nil, model.NewAppError("UpdatePost", "api.post.update_post.can_not_update_post_in_deleted.error", nil, "", http.StatusBadRequest)
	}

	next := old.Clone()
	if next.Message != post.Message {
		next.Message = post.Message
		next.EditAt = clipsNow.UnixMilli()
	}
	next.IsPinned = post.IsPinned
	next.SetProps(post.GetProps())
	next.FileIds = append(model.StringArray(nil), post.FileIds...)
	if next.EditAt == old.EditAt && !old.FileIds.Equals(next.FileIds) {
		next.EditAt = clipsNow.UnixMilli()
	}

	result, reason, err := hookUpdated(srv.p, next, old)
	if err != nil {
		srv.t.Errorf("MessageWillBeUpdated over RPC: %v", err)
		return nil, model.NewAppError("UpdatePost", "app.rpc", nil, err.Error(), http.StatusInternalServerError)
	}
	if result == nil {
		return nil, model.NewAppError("UpdatePost", "Post rejected by plugin. "+reason, nil, "", http.StatusBadRequest)
	}

	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.updates[post.Id]++
	srv.posts[post.Id] = result.Clone()
	if srv.lose > 0 {
		srv.lose--
		srv.posts[post.Id] = old.Clone()
	}

	return result.Clone(), nil
}

func (srv *shareServer) getPost(postID string) (*model.Post, *model.AppError) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.gets++
	if srv.getBroken[postID] {
		return nil, model.NewAppError("GetPost", "app.post.get.app_error", nil, "injected failure", http.StatusInternalServerError)
	}
	post, found := srv.posts[postID]
	if !found || srv.getFails[postID] {
		return nil, notFound("GetPost")
	}

	return post.Clone(), nil
}

func (srv *shareServer) deletePost(postID string) *model.AppError {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.deleted = append(srv.deleted, postID)
	if srv.deleteFail != nil {
		return srv.deleteFail
	}
	delete(srv.posts, postID)

	return nil
}

func (srv *shareServer) post(postID string) *model.Post {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	post, found := srv.posts[postID]
	if !found {
		return nil
	}

	return post.Clone()
}

func (srv *shareServer) createCount() int {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	return srv.creates
}

func (srv *shareServer) updateCount(postID string) int {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	return srv.updates[postID]
}

func (srv *shareServer) getCount() int {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	return srv.gets
}

// authorDeletes is a post deleted by its author or a moderator, which
// nothing tells a plugin of: from then on GetPost does not find it.
func (srv *shareServer) authorDeletes(postIDs ...string) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	for _, postID := range postIDs {
		delete(srv.posts, postID)
	}
}

// putCard is a card already on the server, as another node's share would
// have left it.
func (srv *shareServer) putCard(postID, clipID string) {
	srv.lock.Lock()
	defer srv.lock.Unlock()

	srv.posts[postID] = &model.Post{Id: postID, ChannelId: townSquare, Type: clipPostType, Props: model.StringInterface{clipPropID: clipID}}
}

// newShareWorld is a server where Bob, Dave, Dan and Boss (an admin) have
// each linked a stream, and Bob has published a clip of Dave's stream. Dan
// is in team A, Dave in team B, Bob in both; everybody is in Town Square and
// Off-Topic and may post there.
func newShareWorld(t *testing.T) (*clipsWorld, *shareServer) {
	t.Helper()

	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	setClipsClock(w.p, clipsNow)
	srv := newShareServer(t, w)
	clipsAdmins(w.api, "uid-boss")
	srv.set(func(srv *shareServer) { srv.admins["uid-boss"] = true })
	startClipsIn(t, w.p, w.api, model.ImageDriverLocal)

	everybody := []string{"uid-bob", "uid-dave", "uid-dan", "uid-boss"}
	srv.addChannel(&model.Channel{Id: townSquare, Type: model.ChannelTypeOpen, Name: "town-square"}, everybody...)
	srv.addChannel(&model.Channel{Id: offTopic, Type: model.ChannelTypeOpen, Name: "off-topic"}, everybody...)
	srv.set(func(srv *shareServer) {
		srv.teams["uid-dan"] = []string{"team-a"}
		srv.teams["uid-dave"] = []string{"team-b"}
		srv.teams["uid-bob"] = []string{"team-a", "team-b"}
		srv.teams["uid-boss"] = []string{"team-a"}
	})

	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)
	entry.StreamTag = w.p.streamTag(keyOf("uid-dave"))
	publishFixtureClip(t, w.p, entry, true)

	return w, srv
}

func (w *clipsWorld) share(t *testing.T, userID, clipID, channelID string) *httptest.ResponseRecorder {
	t.Helper()

	return w.call(t, userID, clipCall{
		method: http.MethodPost,
		target: clipsPath + "/share?id=" + clipID,
		body:   `{"channelId":"` + channelID + `"}`,
	})
}

func decodeShare(t *testing.T, response *httptest.ResponseRecorder, status int) clipShareResponse {
	t.Helper()

	require.Equal(t, status, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var answer clipShareResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.NotEmpty(t, answer.PostID)

	return answer
}

// changeClip changes a stored clip, as another write would.
func (w *clipsWorld) changeClip(t *testing.T, clipID string, change func(entry *clipEntry)) {
	t.Helper()

	_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		entry := doc.clip(clipID)
		require.NotNil(t, entry)
		change(entry)

		return true, nil
	})
	require.NoError(t, err)
}

// changeStored changes the stored document behind this node's back, as
// another node's write would: this node's copy does not see it.
func (w *clipsWorld) changeStored(t *testing.T, change func(doc *clipsDoc)) {
	t.Helper()

	doc := w.stored(t)
	change(doc)
	doc.Rev++
	w.seed(t, doc)
}

// movableClock makes the handlers' clock one the test moves, starting at
// clipsNow, and answers what moves it.
func movableClock(p *Plugin) func(at time.Time) {
	var lock sync.Mutex
	current := clipsNow

	p.clips().now = func() time.Time {
		lock.Lock()
		defer lock.Unlock()

		return current
	}

	return func(at time.Time) {
		lock.Lock()
		defer lock.Unlock()

		current = at
	}
}

// tokensLeft is how many tokens somebody has left in a limiter, and whether
// they have spent any at all.
func tokensLeft(limiter *rateLimiter, userID string) (float64, bool) {
	limiter.lock.Lock()
	defer limiter.lock.Unlock()

	bucket, known := limiter.buckets[userID]
	if !known {
		return 0, false
	}

	return bucket.tokens, true
}

// logged is every log line at this level with this message.
func logged(api *plugintest.API, level, message string) []mock.Arguments {
	var lines []mock.Arguments
	for _, call := range api.Calls {
		if call.Method == level && len(call.Arguments) > 0 && call.Arguments[0] == message {
			lines = append(lines, call.Arguments)
		}
	}

	return lines
}

// requireNoStreamKeys checks that no log line, and nothing else given, holds
// a stream key, a stream tag or a handle.
func requireNoStreamKeys(t *testing.T, w *clipsWorld, texts ...string) {
	t.Helper()

	secrets := []string{}
	for _, userID := range []string{"uid-bob", "uid-dave", "uid-dan", "uid-boss"} {
		secrets = append(secrets, keyOf(userID), w.p.streamTag(keyOf(userID)))
	}
	secrets = append(secrets, someHandle)

	for _, call := range w.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			texts = append(texts, fmt.Sprint(call.Arguments...))
		}
	}
	for _, text := range texts {
		for _, secret := range secrets {
			require.NotContains(t, text, secret)
		}
	}
}

var passShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Every check before anything is posted, in A§4.2's order: the link, the
// request, a bot, the store, the clip, and then whether the caller could post
// in that channel themselves — a shared channel, membership, the channel's
// own permission, a direct message to somebody deactivated, and, where
// messages are kept within teams, a team in common. A refusal posts nothing
// and costs no token. A share that passes posts one card, as the caller, with
// exactly the props a card has and a pass that the hook takes off.
func TestShareNeedsLinkAndChannelPermission(t *testing.T) {
	dm := func(name string, members ...string) *model.Channel {
		return &model.Channel{Id: channelOf(name), Type: model.ChannelTypeDirect, Name: strings.Join(members, "__")}
	}
	gm := func(name string) *model.Channel {
		return &model.Channel{Id: channelOf(name), Type: model.ChannelTypeGroup, Name: name}
	}

	type row struct {
		name    string
		caller  string
		clip    string
		channel string
		body    string
		setUp   func(t *testing.T, w *clipsWorld, srv *shareServer)
		status  int
		code    clipErrorCode
	}

	rows := []row{
		{name: "not linked", caller: "uid-unlinked", status: http.StatusForbidden, code: clipErrLocked},
		{name: "link unreadable", setUp: func(_ *testing.T, w *clipsWorld, _ *shareServer) {
			w.faults.lock.Lock()
			w.faults.readFails[kvLinkByUserPrefix+"uid-dan"] = true
			w.faults.lock.Unlock()
		}, status: http.StatusServiceUnavailable, code: clipErrStore},
		{name: "malformed id", clip: "not-a-clip", status: http.StatusBadRequest, code: clipErrBadRequest},
		{name: "not JSON", body: `{"channelId":`, status: http.StatusBadRequest, code: clipErrBadRequest},
		{name: "not a channel id", channel: "town-square", status: http.StatusBadRequest, code: clipErrBadRequest},
		{name: "too long", body: `{"channelId":"` + townSquare + `","x":"` + strings.Repeat("y", 1100) + `"}`, status: http.StatusBadRequest, code: clipErrBadRequest},
		{name: "a bot", caller: "uid-bot", status: http.StatusForbidden, code: clipErrForbidden},
		{name: "halted", setUp: func(t *testing.T, w *clipsWorld, _ *shareServer) {
			w.kvSet(kvClips, []byte("not json"))
			_, err := w.p.readClipsFresh(clipsNow)
			require.ErrorIs(t, err, errClipsHalted)
		}, status: http.StatusServiceUnavailable, code: clipErrStore},
		{name: "never was", clip: clipC, status: http.StatusGone, code: clipErrClipGone},
		{name: "expired", clip: clipB, status: http.StatusGone, code: clipErrClipGone},

		// This node's copy, under a minute old, still has it: posting from
		// that copy would send a deleted clip's title and names to the whole
		// channel before the post was taken down again.
		{name: "deleted on another node a moment ago", setUp: func(t *testing.T, w *clipsWorld, _ *shareServer) {
			cached, err := w.p.clipsDocument(clipsNow)
			require.NoError(t, err)
			require.NotNil(t, cached.clip(clipA), "this node's copy still has it")

			w.changeStored(t, func(doc *clipsDoc) {
				doc.Clips = slices.DeleteFunc(doc.Clips, func(entry clipEntry) bool { return entry.ID == clipA })
			})
		}, status: http.StatusGone, code: clipErrClipGone},
		{name: "no such channel", channel: channelOf("nowhere"), status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "archived", channel: channelOf("archived"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(&model.Channel{Id: channelOf("archived"), Type: model.ChannelTypeOpen, DeleteAt: 1}, "uid-dan")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "shared with another workspace", channel: channelOf("shared"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(&model.Channel{Id: channelOf("shared"), Type: model.ChannelTypeOpen, Shared: model.NewPointer(true)}, "uid-dan")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		// Mattermost's permission check does not ask whether somebody is in
		// the channel, so these get past it, and only membership stops them.
		{name: "not a member, though a role lets them post", channel: channelOf("private"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(&model.Channel{Id: channelOf("private"), Type: model.ChannelTypePrivate}, "uid-bob")
			srv.set(func(srv *shareServer) { srv.posters[channelOf("private")]["uid-dan"] = true })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "an admin, in a private channel they are not in", caller: "uid-boss", channel: channelOf("private"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(&model.Channel{Id: channelOf("private"), Type: model.ChannelTypePrivate}, "uid-bob")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "an admin, in somebody else's direct message", caller: "uid-boss", channel: channelOf("dmothers"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmothers", "uid-bob", "uid-dave"), "uid-bob", "uid-dave")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "read-only", channel: channelOf("announcements"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(&model.Channel{Id: channelOf("announcements"), Type: model.ChannelTypeOpen}, "uid-dan")
			srv.set(func(srv *shareServer) { srv.posters[channelOf("announcements")]["uid-dan"] = false })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "direct message to somebody deactivated", channel: channelOf("dmgone"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			w.person("uid-gone")
			w.changeUser("uid-gone", func(user *model.User) { user.DeleteAt = 1 })
			srv.addChannel(dm("dmgone", "uid-gone", "uid-dan"), "uid-gone", "uid-dan")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "direct message to nobody", channel: channelOf("dmnobody"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmnobody", "uid-dan", "uid-nobody"), "uid-dan", "uid-nobody")
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "direct message, no team in common, kept within teams", channel: channelOf("dmapart"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmapart", "uid-dan", "uid-dave"), "uid-dan", "uid-dave")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "group message, no team in common, kept within teams", channel: channelOf("gmapart"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(gm("gmapart"), "uid-dan", "uid-dave", "uid-bob")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "teams unreadable", channel: channelOf("dmteams"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmteams", "uid-bob", "uid-dan"), "uid-bob", "uid-dan")
			srv.set(func(srv *shareServer) {
				srv.restrict = model.DirectMessageTeam
				srv.teamsFail["uid-bob"] = true
			})
		}, status: http.StatusServiceUnavailable, code: clipErrStore},
		{name: "group members unreadable", channel: channelOf("gmmembers"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(gm("gmmembers"), "uid-dan", "uid-bob")
			srv.set(func(srv *shareServer) {
				srv.restrict = model.DirectMessageTeam
				srv.groupFails = true
			})
		}, status: http.StatusServiceUnavailable, code: clipErrStore},
		{name: "direct message with somebody else's bot, no team in common, kept within teams", channel: channelOf("dmashbot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			srv.addBot(w, "uid-bobbot", "bobbot", "uid-bob")
			srv.addChannel(dm("dmashbot", "uid-bobbot", "uid-dan"), "uid-bobbot", "uid-dan")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "direct message with the bot of a plugin no longer installed, kept within teams", channel: channelOf("dmoldbot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			srv.addBot(w, "uid-oldbot", "oldbot", "com.example.removed")
			srv.addChannel(dm("dmoldbot", "uid-dan", "uid-oldbot"), "uid-dan", "uid-oldbot")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusForbidden, code: clipErrCannotPost},
		{name: "direct message with a bot that cannot be read, kept within teams", channel: channelOf("dmbadbot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			w.person("uid-badbot")
			w.changeUser("uid-badbot", func(user *model.User) { user.IsBot = true })
			srv.addChannel(dm("dmbadbot", "uid-badbot", "uid-dan"), "uid-badbot", "uid-dan")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusServiceUnavailable, code: clipErrStore},

		// And the shares that go through.
		{name: "an open channel", status: http.StatusCreated},
		{name: "a direct message", channel: channelOf("dmash"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmash", "uid-bob", "uid-dan"), "uid-bob", "uid-dan")
		}, status: http.StatusCreated},
		{name: "a direct message to themselves", channel: channelOf("dmself"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmself", "uid-dan", "uid-dan"), "uid-dan")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusCreated},
		{name: "direct message, a team in common, kept within teams", channel: channelOf("dmtogether"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmtogether", "uid-bob", "uid-dan"), "uid-bob", "uid-dan")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusCreated},
		{name: "group message, a team in common, kept within teams", channel: channelOf("gmtogether"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(gm("gmtogether"), "uid-dan", "uid-bob", "uid-boss")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusCreated},
		{name: "direct message, no team in common, open to anybody", channel: channelOf("dmanyone"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(dm("dmanyone", "uid-dan", "uid-dave"), "uid-dan", "uid-dave")
		}, status: http.StatusCreated},
		{name: "group message, no team in common, open to anybody", channel: channelOf("gmanyone"), setUp: func(_ *testing.T, _ *clipsWorld, srv *shareServer) {
			srv.addChannel(gm("gmanyone"), "uid-dan", "uid-dave", "uid-bob")
		}, status: http.StatusCreated},

		// Whom Mattermost's team rule does not count [master
		// S/app/channel.go:4303-4326]: somebody deactivated, and the bots it
		// lets message anybody.
		{name: "group message, somebody deactivated shares no team, kept within teams", channel: channelOf("gmgone"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			w.person("uid-left")
			w.changeUser("uid-left", func(user *model.User) { user.DeleteAt = 1 })
			srv.addChannel(gm("gmgone"), "uid-dan", "uid-bob", "uid-left")
			srv.set(func(srv *shareServer) {
				srv.restrict = model.DirectMessageTeam
				srv.teams["uid-left"] = []string{"team-c"}
			})
		}, status: http.StatusCreated},
		{name: "direct message with a plugin's bot, no team in common, kept within teams", channel: channelOf("dmpluginbot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			srv.addBot(w, "uid-helperbot", "helper", "com.example.helper")
			srv.addChannel(dm("dmpluginbot", "uid-helperbot", "uid-dan"), "uid-helperbot", "uid-dan")
			srv.set(func(srv *shareServer) {
				srv.restrict = model.DirectMessageTeam
				srv.plugins = []string{"com.example.helper"}
			})
		}, status: http.StatusCreated},
		{name: "direct message with their own bot, no team in common, kept within teams", channel: channelOf("dmdanbot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			srv.addBot(w, "uid-danbot", "danbot", "uid-dan")
			srv.addChannel(dm("dmdanbot", "uid-dan", "uid-danbot"), "uid-dan", "uid-danbot")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusCreated},
		{name: "direct message with Mattermost's own bot, no team in common, kept within teams", channel: channelOf("dmsystembot"), setUp: func(_ *testing.T, w *clipsWorld, srv *shareServer) {
			srv.addBot(w, "uid-systembot", model.BotSystemBotUsername, "uid-boss")
			srv.addChannel(dm("dmsystembot", "uid-dan", "uid-systembot"), "uid-dan", "uid-systembot")
			srv.set(func(srv *shareServer) { srv.restrict = model.DirectMessageTeam })
		}, status: http.StatusCreated},
		{name: "while Clips is switched off", setUp: func(_ *testing.T, w *clipsWorld, _ *shareServer) {
			cfg := *w.p.getConfiguration()
			cfg.EnableClips = false
			w.p.setConfiguration(&cfg)
		}, status: http.StatusCreated},
		{name: "by somebody stopped making clips", setUp: func(t *testing.T, w *clipsWorld, _ *shareServer) {
			_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
				return doc.block("uid-dan", clipMark{By: "uid-boss", At: clipsNow.UnixMilli()}), nil
			})
			require.NoError(t, err)
		}, status: http.StatusCreated},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			w, srv := newShareWorld(t)
			publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-31*24*time.Hour), nil), false)
			w.person("uid-unlinked")
			w.person("uid-bot")
			w.link("uid-bot", keyOf("uid-bot"))
			w.changeUser("uid-bot", func(user *model.User) { user.IsBot = true })

			caller, clipID, channelID := r.caller, r.clip, r.channel
			if caller == "" {
				caller = "uid-dan"
			}
			if clipID == "" {
				clipID = clipA
			}
			if channelID == "" {
				channelID = townSquare
			}
			body := r.body
			if body == "" {
				body = `{"channelId":"` + channelID + `"}`
			}
			if r.setUp != nil {
				r.setUp(t, w, srv)
			}

			response := w.call(t, caller, clipCall{method: http.MethodPost, target: clipsPath + "/share?id=" + clipID, body: body})

			if r.status != http.StatusCreated {
				requireClipError(t, response, r.status, r.code, r.name)
				require.Zero(t, srv.createCount(), "nothing is posted")
				_, spent := tokensLeft(w.p.clips().shareLimiter, caller)
				require.False(t, spent, "no token is spent")

				return
			}

			answer := decodeShare(t, response, http.StatusCreated)
			require.Equal(t, channelID, answer.ChannelID)
			require.Equal(t, 1, srv.createCount())

			// The names are written out rather than taken from the code's own
			// constants: the webapp's card reads these very strings.
			sent := srv.sent[0]
			require.Equal(t, caller, sent.UserId, "posted as the person who pressed Share")
			require.Equal(t, channelID, sent.ChannelId)
			require.Equal(t, "custom_villanet_clip", sent.Type)
			require.Empty(t, sent.RootId, "a post of its own, never a reply")

			keys := []string{}
			for key := range sent.GetProps() {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			require.Equal(t, []string{
				"villanet_clip_clipper", "villanet_clip_clipper_id", "villanet_clip_created_at", "villanet_clip_duration_ms", "villanet_clip_id",
				"villanet_clip_streamer", "villanet_clip_streamer_id", "villanet_clip_title", "villanet_share_nonce",
			}, keys)
			require.Regexp(t, passShape, sent.GetProp("villanet_share_nonce"))
			require.Equal(t, clipA, sent.GetProp("villanet_clip_id"))
			require.Equal(t, "clip "+clipA, sent.GetProp("villanet_clip_title"))
			require.Equal(t, int64(10000), sent.GetProp("villanet_clip_duration_ms"))
			require.Equal(t, clipsNow.Add(-time.Hour).UnixMilli(), sent.GetProp("villanet_clip_created_at"))
			require.Equal(t, "uid-bob", sent.GetProp("villanet_clip_clipper_id"))
			require.Equal(t, "Bob", sent.GetProp("villanet_clip_clipper"))
			require.Equal(t, "uid-dave", sent.GetProp("villanet_clip_streamer_id"))
			require.Equal(t, "Dave", sent.GetProp("villanet_clip_streamer"))

			// What was saved, after the hook's answer crossed back over RPC:
			// the card, as the caller, its pass blanked, which is all the
			// hook can do to a prop.
			stored := srv.post(answer.PostID)
			require.NotNil(t, stored)
			require.Equal(t, "custom_villanet_clip", stored.Type, "the pass was good, so it was not made an ordinary message")
			want := copyProps(sent.GetProps())
			want["villanet_share_nonce"] = ""
			want["from_plugin"] = "true"
			require.Equal(t, want, stored.GetProps())

			// And written down on the clip, with nothing pushed to anybody:
			// no panel shows where a clip was shared.
			require.Equal(t, []string{answer.PostID}, w.stored(t).clip(clipA).Shares)

			left, spent := tokensLeft(w.p.clips().shareLimiter, caller)
			require.True(t, spent)
			require.InDelta(t, 9.0, left, 1e-9)

			lines := logged(w.api, "LogInfo", "A clip was shared")
			require.Len(t, lines, 1)
			require.Equal(t, []any{"A clip was shared", "user_id", caller, "clip_id", clipA, "channel_id", channelID, "post_id", answer.PostID}, []any(lines[0]))
			requireNoStreamKeys(t, w, response.Body.String(), sent.Message, fmt.Sprint(sent.GetProps()))
		})
	}
}

// A share writes nothing a panel shows, so it pushes nothing to anybody.
func TestShareRepublishesNothing(t *testing.T) {
	w, _ := newShareWorld(t)
	s := w.p.clips()

	s.runLock.Lock()
	armedBefore := s.republishArmed
	s.runLock.Unlock()
	require.True(t, armedBefore, "publishing the clip armed a republish")

	// Let it go off, as the timer would.
	s.runLock.Lock()
	s.republishArmed = false
	s.runLock.Unlock()

	decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)

	s.runLock.Lock()
	defer s.runLock.Unlock()
	require.False(t, s.republishArmed, "nothing to push")
}

// The message is VillaNET's own text and a link, whatever the title and the
// names say: they are only in the props, where nothing is parsed for mentions
// or Markdown. With no Site URL there is no link, and an admin is told once
// each time the plugin starts.
func TestShareMessageCarriesNoUserText(t *testing.T) {
	for durationMs, want := range map[int64]string{
		0:       "0:00",
		499:     "0:00",
		44_499:  "0:44",
		45_000:  "0:45",
		119_600: "2:00",
		300_000: "5:00",
		600_000: "10:00",
		-5:      "0:00",
	} {
		require.Equal(t, "🎬 Matty clip · "+want, clipShareMessage(productName, "", clipCardView{ID: clipA, DurationMs: durationMs}), durationMs)
	}
	require.Equal(t,
		"🎬 Matty clip · 0:45\nWatch it: https://chat.example.com/plug/ceo.villa.matty/clip/3f9a0c2e7b1d4a55",
		clipShareMessage(productName, " https://chat.example.com// ", clipCardView{ID: "3f9a0c2e7b1d4a55", DurationMs: 45_000}))

	w, srv := newShareWorld(t)
	hostile := "@channel **x** http://e.x `y` \n z"
	w.changeClip(t, clipA, func(entry *clipEntry) {
		entry.Title = hostile
		entry.DurationMs = 45_000
	})
	w.changeUser("uid-bob", func(user *model.User) { user.Nickname = "@here Bob" })
	w.changeUser("uid-dave", func(user *model.User) { user.Nickname = "@all **Dave**" })

	decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)

	sent := srv.sent[0]
	require.Equal(t, "🎬 Matty clip · 0:45\nWatch it: https://chat.example.com/plug/ceo.villa.matty/clip/"+clipA, sent.Message)
	for _, text := range []string{"@channel", "@here", "@all", "**", "http://e.x", "`", "Bob", "Dave"} {
		require.NotContains(t, sent.Message, text)
	}
	require.Equal(t, hostile, sent.GetProp(clipPropTitle), "the title is in the props, for the card")
	require.Equal(t, "@here Bob", sent.GetProp(clipPropClipper))
	require.Equal(t, "@all **Dave**", sent.GetProp(clipPropStreamer))

	// No Site URL: no link, and one line for the admin however many shares.
	srv.set(func(srv *shareServer) { srv.siteURL = "" })
	decodeShare(t, w.share(t, "uid-dan", clipA, offTopic), http.StatusCreated)
	srv.addChannel(&model.Channel{Id: channelOf("third"), Type: model.ChannelTypeOpen}, "uid-dan")
	decodeShare(t, w.share(t, "uid-dan", clipA, channelOf("third")), http.StatusCreated)

	require.Equal(t, "🎬 Matty clip · 0:45", srv.sent[1].Message)
	require.Equal(t, "🎬 Matty clip · 0:45", srv.sent[2].Message)
	require.Len(t, logged(w.api, "LogWarn", clipNoSiteURLText), 1)

	// Said again after the plugin starts again.
	w.p.startClips()
	srv.addChannel(&model.Channel{Id: channelOf("fourth"), Type: model.ChannelTypeOpen}, "uid-dan")
	decodeShare(t, w.share(t, "uid-dan", clipA, channelOf("fourth")), http.StatusCreated)
	require.Len(t, logged(w.api, "LogWarn", clipNoSiteURLText), 2)
}

// The same person sharing the same clip into the same channel within a
// minute gets the post they already made, and nothing is posted or spent:
// a double click, a second tab, a retry after the answer was lost. Another
// channel, or a minute later, is a new share. A share that failed is not
// remembered, and two at once post once.
func TestShareIsIdempotentWithinAMinute(t *testing.T) {
	w, srv := newShareWorld(t)
	setClock := movableClock(w.p)

	first := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
	again := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusOK)
	require.Equal(t, first, again)
	require.Equal(t, 1, srv.createCount())
	left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
	require.InDelta(t, 9.0, left, 1e-9, "the repeat spent nothing")

	setClock(clipsNow.Add(59 * time.Second))
	require.Equal(t, first, decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusOK))

	elsewhere := decodeShare(t, w.share(t, "uid-dan", clipA, offTopic), http.StatusCreated)
	require.NotEqual(t, first.PostID, elsewhere.PostID)

	dave := decodeShare(t, w.share(t, "uid-dave", clipA, townSquare), http.StatusCreated)
	require.NotEqual(t, first.PostID, dave.PostID, "somebody else's share is their own")

	setClock(clipsNow.Add(time.Minute))
	later := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
	require.NotEqual(t, first.PostID, later.PostID)
	require.Equal(t, 4, srv.createCount())

	// A failure is not remembered: the next try posts.
	srv.set(func(srv *shareServer) {
		srv.createFail = model.NewAppError("CreatePost", "app.failed", nil, "injected failure", http.StatusInternalServerError)
	})
	srv.addChannel(&model.Channel{Id: channelOf("retry"), Type: model.ChannelTypeOpen}, "uid-dan")
	requireClipError(t, w.share(t, "uid-dan", clipA, channelOf("retry")), http.StatusServiceUnavailable, clipErrStore, "failed")
	srv.set(func(srv *shareServer) { srv.createFail = nil })
	decodeShare(t, w.share(t, "uid-dan", clipA, channelOf("retry")), http.StatusCreated)

	// Two at once: the second waits for the first, or comes after it, and
	// either way gets its post.
	//
	// The first is held inside CreatePost until the second has started
	// waiting for it, or has got as far as posting too, which is the fault
	// this is here to catch.
	srv.addChannel(&model.Channel{Id: channelOf("doubleclick"), Type: model.ChannelTypeOpen}, "uid-dan")
	posting := make(chan struct{}, 2)
	release := make(chan struct{})
	srv.set(func(srv *shareServer) {
		srv.duringCreate = func() {
			posting <- struct{}{}
			<-release
		}
	})
	waiting := make(chan struct{})
	var once sync.Once
	shares := &w.p.clips().shares
	shares.lock.Lock()
	shares.onWait = func() { once.Do(func() { close(waiting) }) }
	shares.lock.Unlock()

	before := srv.createCount()
	answers := make(chan *httptest.ResponseRecorder, 2)
	go func() { answers <- w.share(t, "uid-dan", clipA, channelOf("doubleclick")) }()
	<-posting
	go func() { answers <- w.share(t, "uid-dan", clipA, channelOf("doubleclick")) }()
	select {
	case <-waiting:
	case <-posting:
		t.Error("the second share posted while the first was still posting")
	}
	close(release)

	one, other := <-answers, <-answers
	codes := []int{one.Code, other.Code}
	sort.Ints(codes)
	require.Equal(t, []int{http.StatusOK, http.StatusCreated}, codes, one.Body.String()+other.Body.String())
	require.JSONEq(t, one.Body.String(), other.Body.String())
	require.Equal(t, before+1, srv.createCount(), "posted once")
}

// Ten shares in ten minutes, a person: one more a minute after the tenth. A
// share Mattermost refused, or that failed, gives its token back, so trying
// again as told costs nothing.
func TestShareRateLimitedAndRefundedOnFailure(t *testing.T) {
	w, srv := newShareWorld(t)
	setClock := movableClock(w.p)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-2*time.Hour), nil), false)

	channels := []string{}
	for i := range 11 {
		channelID := channelOf(fmt.Sprintf("room%d", i))
		srv.addChannel(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, "uid-dan")
		channels = append(channels, channelID)
	}

	// Twenty failures of each kind, every token given back.
	srv.set(func(srv *shareServer) {
		srv.createFail = model.NewAppError("CreatePost", "app.failed", nil, "injected failure", http.StatusInternalServerError)
	})
	for range 20 {
		requireClipError(t, w.share(t, "uid-dan", clipA, channels[0]), http.StatusServiceUnavailable, clipErrStore, "the server's trouble")
	}
	srv.set(func(srv *shareServer) {
		srv.createFail = model.NewAppError("createPost", "api.post.create_post.can_not_post_to_deleted.error", nil, "", http.StatusBadRequest)
	})
	for range 20 {
		requireClipError(t, w.share(t, "uid-dan", clipA, channels[0]), http.StatusForbidden, clipErrCannotPost, "refused")
	}
	failures := logged(w.api, "LogWarn", "Could not post a shared clip")
	require.Len(t, failures, 40)
	require.Equal(t, []any{"Could not post a shared clip", "user_id", "uid-dan", "clip_id", clipA, "channel_id", channels[0], "error"}, []any(failures[0][:8]))
	require.Contains(t, failures[0][8], "injected failure")
	srv.set(func(srv *shareServer) { srv.createFail = nil })
	left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
	require.InDelta(t, 10.0, left, 1e-9)

	for i := range 10 {
		decodeShare(t, w.share(t, "uid-dan", clipA, channels[i]), http.StatusCreated)
	}

	creates := srv.createCount()
	requireClipError(t, w.share(t, "uid-dan", clipB, channels[10]), http.StatusTooManyRequests, clipErrRateLimited, "the eleventh")
	require.Equal(t, creates, srv.createCount(), "nothing posted")

	// Ten in ten minutes is one a minute: 59 seconds on there is still
	// none, and two minutes on there is one.
	setClock(clipsNow.Add(59 * time.Second))
	requireClipError(t, w.share(t, "uid-dan", clipB, channels[10]), http.StatusTooManyRequests, clipErrRateLimited, "59 s later")
	require.Equal(t, creates, srv.createCount(), "nothing posted")
	setClock(clipsNow.Add(2 * time.Minute))
	decodeShare(t, w.share(t, "uid-dan", clipB, channels[10]), http.StatusCreated)

	// Somebody else has their own ten.
	srv.addChannel(&model.Channel{Id: channelOf("daves"), Type: model.ChannelTypeOpen}, "uid-dave")
	decodeShare(t, w.share(t, "uid-dave", clipB, channelOf("daves")), http.StatusCreated)
}

// A clip is in ten posts at most, so every one can be told when it goes. The
// eleventh is refused before anything is posted or spent — unless it is the
// same share again. One that races past that check, with another node's
// tenth landing while it posts, is taken down again.
//
// Posts deleted since do not count: Mattermost tells a plugin of none, so a
// full clip's posts are asked about, once a minute at most, and those it no
// longer has are taken off. Otherwise anybody could share a clip ten times,
// delete the ten posts, and leave it unsharable for the rest of its life.
func TestSharesAreCappedAtTen(t *testing.T) {
	rooms := func(srv *shareServer, n int) []string {
		channels := []string{}
		for i := range n {
			channelID := channelOf(fmt.Sprintf("room%d", i))
			srv.addChannel(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, "uid-dan", "uid-dave")
			channels = append(channels, channelID)
		}

		return channels
	}
	fill := func(t *testing.T, w *clipsWorld, channels []string) []string {
		t.Helper()

		posts := []string{}
		for i := range 10 {
			posts = append(posts, decodeShare(t, w.share(t, "uid-dan", clipA, channels[i]), http.StatusCreated).PostID)
		}
		require.Equal(t, posts, w.stored(t).clip(clipA).Shares)

		return posts
	}

	t.Run("the eleventh", func(t *testing.T) {
		w, srv := newShareWorld(t)
		channels := rooms(srv, 11)
		posts := fill(t, w, channels)

		creates, gets := srv.createCount(), srv.getCount()
		requireClipError(t, w.share(t, "uid-dave", clipA, channels[10]), http.StatusConflict, clipErrShareCap, "the eleventh")
		require.Equal(t, creates, srv.createCount(), "nothing posted")
		require.Equal(t, gets+10, srv.getCount(), "each of the ten asked about once")
		_, spent := tokensLeft(w.p.clips().shareLimiter, "uid-dave")
		require.False(t, spent, "nothing spent")

		// Asked again within the minute, nobody is asked about again.
		requireClipError(t, w.share(t, "uid-dave", clipA, channels[10]), http.StatusConflict, clipErrShareCap, "again")
		require.Equal(t, gets+10, srv.getCount())

		// The same share again is still answered with its post.
		require.Equal(t, posts[9], decodeShare(t, w.share(t, "uid-dan", clipA, channels[9]), http.StatusOK).PostID)
		require.Equal(t, creates, srv.createCount())
	})

	t.Run("posts deleted since do not count", func(t *testing.T) {
		w, srv := newShareWorld(t)
		posts := fill(t, w, rooms(srv, 10))

		srv.authorDeletes(posts...)
		dave := decodeShare(t, w.share(t, "uid-dave", clipA, townSquare), http.StatusCreated)
		require.Equal(t, []string{dave.PostID}, w.stored(t).clip(clipA).Shares, "the ten deleted ones taken off")
	})

	t.Run("one that cannot be read still counts", func(t *testing.T) {
		w, srv := newShareWorld(t)
		posts := fill(t, w, rooms(srv, 10))

		srv.authorDeletes(posts[:9]...)
		srv.set(func(srv *shareServer) { srv.getBroken[posts[9]] = true })

		decodeShare(t, w.share(t, "uid-dave", clipA, townSquare), http.StatusCreated)
		shares := w.stored(t).clip(clipA).Shares
		require.Len(t, shares, 2)
		require.Equal(t, posts[9], shares[0], "an answer that is not a 404 says nothing either way")
	})

	t.Run("looked at again a minute later", func(t *testing.T) {
		w, srv := newShareWorld(t)
		setClock := movableClock(w.p)
		channels := rooms(srv, 11)
		posts := fill(t, w, channels)

		requireClipError(t, w.share(t, "uid-dave", clipA, channels[10]), http.StatusConflict, clipErrShareCap, "full")

		srv.authorDeletes(posts[3])
		setClock(clipsNow.Add(59 * time.Second))
		requireClipError(t, w.share(t, "uid-dave", clipA, channels[10]), http.StatusConflict, clipErrShareCap, "within the minute")

		setClock(clipsNow.Add(time.Minute))
		dave := decodeShare(t, w.share(t, "uid-dave", clipA, channels[10]), http.StatusCreated)
		want := append(append(append([]string{}, posts[:3]...), posts[4:]...), dave.PostID)
		require.Equal(t, want, w.stored(t).clip(clipA).Shares)
	})

	// The race: nine here, and another node's tenth lands while this one
	// posts.
	t.Run("raced past the check", func(t *testing.T) {
		w, srv := newShareWorld(t)
		earlier := []string{}
		for i := range 9 {
			postID := fmt.Sprintf("earlier-post-%d", i)
			srv.putCard(postID, clipA)
			earlier = append(earlier, postID)
		}
		w.changeClip(t, clipA, func(entry *clipEntry) { entry.Shares = earlier })
		srv.putCard("another-nodes-post", clipA)
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() {
				w.changeStored(t, func(doc *clipsDoc) {
					entry := doc.clip(clipA)
					entry.Shares = append(entry.Shares, "another-nodes-post")
				})
			}
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusConflict, clipErrShareCap, "raced")
		require.Equal(t, 1, srv.createCount())
		require.Len(t, srv.deleted, 1, "taken down again")
		require.Nil(t, srv.post(srv.deleted[0]))
		require.NotContains(t, w.stored(t).clip(clipA).Shares, srv.deleted[0])
		left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
		require.InDelta(t, 10.0, left, 1e-9, "and the token given back")
		require.Empty(t, logged(w.api, "LogWarn", "Could not remember a shared clip post"), "a refusal, not a failure")
	})

	// The same race, but one of the ten has been deleted: it is taken off,
	// and the share stays.
	t.Run("raced past the check, with a post deleted since", func(t *testing.T) {
		w, srv := newShareWorld(t)
		earlier := []string{}
		for i := range 9 {
			postID := fmt.Sprintf("earlier-post-%d", i)
			srv.putCard(postID, clipA)
			earlier = append(earlier, postID)
		}
		w.changeClip(t, clipA, func(entry *clipEntry) { entry.Shares = earlier })
		srv.authorDeletes(earlier[4])
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() {
				w.changeStored(t, func(doc *clipsDoc) {
					entry := doc.clip(clipA)
					entry.Shares = append(entry.Shares, "another-nodes-post")
				})
			}
		})
		srv.putCard("another-nodes-post", clipA)

		dan := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
		require.Empty(t, srv.deleted)
		want := append(append(append([]string{}, earlier[:4]...), earlier[5:]...), "another-nodes-post", dan.PostID)
		require.Equal(t, want, w.stored(t).clip(clipA).Shares)
	})
}

// Every share's post is written down on its clip, or it does not stay up: a
// post nobody can tell about its clip going would show a card for ever. A
// clip that went while the post was made, or a store that will not take the
// write, takes the post down again and gives the token back.
func TestShareIsWithdrawnWhenItCannotBeTracked(t *testing.T) {
	t.Run("the clip went meanwhile", func(t *testing.T) {
		w, srv := newShareWorld(t)
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() {
				w.changeStored(t, func(doc *clipsDoc) { doc.Clips = []clipEntry{} })
			}
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusGone, clipErrClipGone, "gone")
		require.Len(t, srv.deleted, 1)
		require.Nil(t, srv.post(srv.deleted[0]))
		left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
		require.InDelta(t, 10.0, left, 1e-9)
		require.Empty(t, logged(w.api, "LogWarn", "Could not remember a shared clip post"), "a refusal, not a failure")
	})

	t.Run("the write failed", func(t *testing.T) {
		w, srv := newShareWorld(t)
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() {
				w.faults.lock.Lock()
				w.faults.readFails[kvClips] = true
				w.faults.lock.Unlock()
			}
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusServiceUnavailable, clipErrStore, "store")
		require.Len(t, srv.deleted, 1)
		require.Nil(t, srv.post(srv.deleted[0]))
		left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
		require.InDelta(t, 10.0, left, 1e-9)

		lines := logged(w.api, "LogWarn", "Could not remember a shared clip post")
		require.Len(t, lines, 1)
		require.Contains(t, fmt.Sprint(lines[0]...), srv.deleted[0])
		require.Empty(t, logged(w.api, "LogWarn", "Could not withdraw a shared clip post"))

		// Answered again later, it is a share like any other.
		w.faults.lock.Lock()
		delete(w.faults.readFails, kvClips)
		w.faults.lock.Unlock()
		srv.set(func(srv *shareServer) { srv.duringCreate = nil })
		decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
	})

	// A step 14 refusal that is the store's, not the share's: the document
	// has no room for one more post id. The sharer is told to try again, not
	// that there is no room for another clip, and an admin is told.
	t.Run("the document is full", func(t *testing.T) {
		w, srv := newShareWorld(t)
		w.changeStored(t, func(doc *clipsDoc) {
			entry := doc.clip(clipA)
			entry.StreamNote = "n"
			raw, err := json.Marshal(doc)
			require.NoError(t, err)
			entry.StreamNote = strings.Repeat("n", 1+maxClipsDocBytes-20-len(raw))
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusServiceUnavailable, clipErrStore, "full")
		require.Len(t, srv.deleted, 1)
		require.Nil(t, srv.post(srv.deleted[0]))
		require.Empty(t, w.stored(t).clip(clipA).Shares)
		left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
		require.InDelta(t, 10.0, left, 1e-9)

		lines := logged(w.api, "LogWarn", "Could not remember a shared clip post")
		require.Len(t, lines, 1)
		require.Equal(t, []any{
			"Could not remember a shared clip post", "user_id", "uid-dan", "clip_id", clipA,
			"channel_id", townSquare, "post_id", srv.deleted[0], "error", "clip_cap",
		}, []any(lines[0]))
	})

	// The pass is good for 30 seconds from when it is handed out, which is
	// after every check and any wait: the request's own start is none of its
	// business. A post the hook did not take as a card is not counted as a
	// share: it is taken down again, and the token given back.
	t.Run("the pass ran out before the hook saw it", func(t *testing.T) {
		w, srv := newShareWorld(t)
		setClock := movableClock(w.p)
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() { setClock(clipsNow.Add(30 * time.Second)) }
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusServiceUnavailable, clipErrStore, "ran out")
		require.Len(t, srv.deleted, 1, "the plain message it became is taken down")
		require.Nil(t, srv.post(srv.deleted[0]))
		require.Empty(t, w.stored(t).clip(clipA).Shares)
		left, _ := tokensLeft(w.p.clips().shareLimiter, "uid-dan")
		require.InDelta(t, 10.0, left, 1e-9)

		lines := logged(w.api, "LogWarn", "Could not post a shared clip")
		require.Len(t, lines, 1)
		require.Equal(t, []any{
			"Could not post a shared clip", "user_id", "uid-dan", "clip_id", clipA,
			"channel_id", townSquare, "error", "the post was not taken as a clip card",
		}, []any(lines[0]))
	})

	t.Run("the pass is still good at 29 seconds", func(t *testing.T) {
		w, srv := newShareWorld(t)
		setClock := movableClock(w.p)
		srv.set(func(srv *shareServer) {
			srv.duringCreate = func() { setClock(clipsNow.Add(29 * time.Second)) }
		})

		shared := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
		require.Equal(t, "custom_villanet_clip", srv.post(shared.PostID).Type)
	})

	t.Run("the pass is timed from when it is handed out", func(t *testing.T) {
		w, srv := newShareWorld(t)
		setClock := movableClock(w.p)
		srv.set(func(srv *shareServer) {
			srv.onGetChannel = func() { setClock(clipsNow.Add(25 * time.Second)) }
			srv.duringCreate = func() { setClock(clipsNow.Add(35 * time.Second)) }
		})

		shared := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
		require.Equal(t, "custom_villanet_clip", srv.post(shared.PostID).Type, "ten seconds after it was handed out")
		require.Empty(t, srv.deleted)
	})

	t.Run("and could not be taken down", func(t *testing.T) {
		w, srv := newShareWorld(t)
		srv.set(func(srv *shareServer) {
			srv.deleteFail = model.NewAppError("DeletePost", "app.failed", nil, "injected failure", http.StatusInternalServerError)
			srv.duringCreate = func() {
				w.changeStored(t, func(doc *clipsDoc) { doc.Clips = []clipEntry{} })
			}
		})

		requireClipError(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusGone, clipErrClipGone, "gone")
		require.Len(t, srv.deleted, 1)

		lines := logged(w.api, "LogWarn", "Could not withdraw a shared clip post")
		require.Len(t, lines, 1)
		require.Equal(t, "post_id", lines[0][1])
		require.Equal(t, srv.deleted[0], lines[0][2])
	})
}

// A post of VillaNET's type is a card only when this node made it, a moment
// ago, for exactly that post. Anything else of that type — made through the
// REST API, or copied by Mattermost when a thread moves — arrives as an
// ordinary message, never refused, which would abandon the move. A post that
// asks through its props to be drawn as a card is drawn as itself. Every
// other post goes past with a comparison and a lookup and nothing else.
//
// Every check here is of what Mattermost carries on with once the hook's
// answer has come back over RPC, decoded into the post it sent: a hook that
// deleted a prop or cleared the type would pass a test that called it in
// process, and change nothing on a real server.
func TestForgedClipPostsAreDowngraded(t *testing.T) {
	w, _ := newShareWorld(t)
	s := w.p.clips()

	card := func(pass string) *model.Post {
		post := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Type: "custom_villanet_clip", Message: "🎬 Matty clip · 0:10"}
		post.SetProps(model.StringInterface{
			"villanet_clip_id":          clipA,
			"villanet_clip_title":       "forged",
			"villanet_clip_clipper":     "Bob",
			"villanet_clip_duration_ms": int64(10000),
			"villanet_clip_gone":        "deleted",
			"type":                      "custom_villanet_clip",
			"villanet_whatever":         "x",
			"from_plugin":               "true",
			"keep":                      "this",
		})
		if pass != "" {
			post.AddProp("villanet_share_nonce", pass)
		}

		return post
	}
	requireDowngraded := func(post *model.Post, reason string, about string) {
		t.Helper()

		require.Empty(t, reason, "%s: never refused", about)
		require.NotNil(t, post, about)
		require.Equal(t, "custom_villanet_clip_copy", post.Type, "%s: a type no card is drawn for", about)
		for key, value := range post.GetProps() {
			if strings.HasPrefix(key, "villanet_") || key == "type" {
				require.Equal(t, "", value, "%s: %s blanked", about, key)
			}
		}
		require.Equal(t, "this", post.GetProp("keep"), about)
		require.Equal(t, "🎬 Matty clip · 0:10", post.Message, about)
	}
	issue := func(value string, pass sharePass) {
		s.shares.issue(value, pass, clipsNow)
	}
	good := sharePass{expires: clipsNow.Add(30 * time.Second), userID: "uid-dan", channelID: townSquare, clipID: clipA}

	post, reason := posted(t, w.p, card(""))
	requireDowngraded(post, reason, "no pass")

	post, reason = posted(t, w.p, card("0123456789abcdef"))
	requireDowngraded(post, reason, "a pass nobody issued")

	issue("aaaaaaaaaaaaaaaa", good)
	post, reason = posted(t, w.p, card("aaaaaaaaaaaaaaaa"))
	require.Empty(t, reason)
	require.Equal(t, "custom_villanet_clip", post.Type, "a good pass")
	require.Equal(t, "", post.GetProp("villanet_share_nonce"), "blanked")
	require.Equal(t, clipA, post.GetProp("villanet_clip_id"))

	post, reason = posted(t, w.p, card("aaaaaaaaaaaaaaaa"))
	requireDowngraded(post, reason, "a pass used twice")

	issue("bbbbbbbbbbbbbbbb", good)
	setClipsClock(w.p, clipsNow.Add(30*time.Second))
	post, reason = posted(t, w.p, card("bbbbbbbbbbbbbbbb"))
	requireDowngraded(post, reason, "a pass run out")
	setClipsClock(w.p, clipsNow)

	for about, pass := range map[string]sharePass{
		"another person":  {expires: good.expires, userID: "uid-dave", channelID: townSquare, clipID: clipA},
		"another channel": {expires: good.expires, userID: "uid-dan", channelID: offTopic, clipID: clipA},
		"another clip":    {expires: good.expires, userID: "uid-dan", channelID: townSquare, clipID: clipB},
	} {
		issue("cccccccccccccccc", pass)
		post, reason = posted(t, w.p, card("cccccccccccccccc"))
		requireDowngraded(post, reason, about)
	}

	issue("dddddddddddddddd", good)
	reply := card("dddddddddddddddd")
	reply.RootId = "some-thread"
	post, reason = posted(t, w.p, reply)
	requireDowngraded(post, reason, "a reply")

	// Ordinary posts asking to be drawn as a card: by the type's name, or by
	// something that is not a string at all, which webapps up to 9.11 turn
	// into one when they look the card up.
	for about, claim := range map[string]any{
		"by name":         "custom_villanet_clip",
		"in a list":       []any{"custom_villanet_clip"},
		"in a list twice": []any{[]any{"custom_villanet_clip"}},
		"as a number":     int64(7),
	} {
		ordinary := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Message: "hello"}
		ordinary.SetProps(model.StringInterface{"type": claim, "villanet_clip_id": clipA, "keep": "this"})

		post, reason = posted(t, w.p, ordinary)
		require.Empty(t, reason, about)
		require.NotNil(t, post, about)
		require.Equal(t, "", post.Type, about)
		require.Equal(t, "", post.GetProp("type"), "%s: blanked", about)
		require.Equal(t, clipA, post.GetProp("villanet_clip_id"), "%s: it is not a card, so nothing else is its business", about)
		require.Equal(t, "this", post.GetProp("keep"), about)
	}

	// In process, the props map a post is handed in with, which copies of
	// the post share, is not the one changed.
	ordinary := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Message: "hello"}
	ordinary.SetProps(model.StringInterface{"type": "custom_villanet_clip"})
	handedIn := ordinary.GetProps()
	post, _ = w.p.MessageWillBePosted(nil, ordinary)
	require.Equal(t, "", post.GetProp("type"))
	require.Equal(t, "custom_villanet_clip", handedIn["type"])

	// Everything else: nil, "", and not one call to Mattermost.
	api := &plugintest.API{}
	bare := &Plugin{}
	bare.SetAPI(api)
	for _, other := range []*model.Post{
		{Message: "hello"},
		{Type: "custom_somebody_else", Message: "theirs"},
		{Type: "custom_villanet_clip_copy", Message: "a copy"},
		{Type: model.PostTypeJoinChannel},
		{Message: "props", Props: model.StringInterface{"type": "custom_somebody_else", "villanet_clip_id": clipA}},
		{Message: "blank", Props: model.StringInterface{"type": ""}},
		{Message: "null", Props: model.StringInterface{"type": nil}},
	} {
		post, reason := bare.MessageWillBePosted(nil, other)
		require.Nil(t, post)
		require.Empty(t, reason)
	}
	post, reason = bare.MessageWillBePosted(nil, nil)
	require.Nil(t, post)
	require.Empty(t, reason)
	require.Empty(t, api.Calls)
}

// A shared post's message cannot be edited; anything else about it can be —
// a pin — but its props and files come back as they were, whatever the edit
// added or changed, and a mark waiting to land is applied whoever's edit it
// is. Only a message edit is ever refused. Each edit's answer crosses back
// over RPC, as on Mattermost.
func TestClipPostPropsCannotBeEdited(t *testing.T) {
	w, srv := newShareWorld(t)
	shared := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
	original := srv.post(shared.PostID)
	require.NotNil(t, original)

	edit := func(change func(post *model.Post)) (*model.Post, string) {
		next := original.Clone()
		next.SetProps(copyProps(original.GetProps()))
		change(next)

		return updated(t, w.p, next, original.Clone())
	}

	post, reason := edit(func(post *model.Post) { post.Message += "\nWatch it: https://elsewhere.example/" })
	require.Nil(t, post)
	require.Equal(t, "Shared clip posts can't be edited.", reason)

	post, reason = edit(func(post *model.Post) { post.IsPinned = true })
	require.Empty(t, reason)
	require.NotNil(t, post, "a pin passes")
	require.True(t, post.IsPinned)
	require.Equal(t, original.GetProps(), post.GetProps())

	// Whatever the author patches on — VillaNET's own props, Mattermost's
	// attachments and integration props, a button on the card's header, app
	// buttons and blocks on a phone, a bot's styling — nothing of it stays,
	// and nothing VillaNET put there goes.
	post, reason = edit(func(post *model.Post) {
		props := post.GetProps()
		props["villanet_clip_title"] = "pwned"
		props["villanet_clip_gone"] = "deleted"
		delete(props, "villanet_clip_clipper")
		props["villanet_new"] = "x"
		props["type"] = "custom_somebody_else"
		props["attachments"] = []any{map[string]any{"text": "@channel"}}
		props["override_username"] = "Boss"
		props["override_icon_url"] = "https://example.com/i.png"
		props["override_icon_emoji"] = "crown"
		props["webhook_display_name"] = "Boss"
		props["from_webhook"] = "true"
		props["mentionHighlightDisabled"] = true
		props["card"] = "**VillaNET notice:** this clip moved, watch it at https://evil.example/"
		props["app_bindings"] = []any{map[string]any{"app_id": "x", "label": "Watch here"}}
		props["mm_blocks"] = []any{map[string]any{"type": "button"}}
		props["blocks"] = []any{map[string]any{"type": "section"}}
		props["cards"] = []any{map[string]any{"title": "x"}}
		props["from_bot"] = "true"
		props["remove_link_preview"] = "false"
		post.SetProps(props)
	})
	require.Empty(t, reason)
	require.NotNil(t, post)
	require.Equal(t, original.GetProps(), post.GetProps(), "exactly the props it had")

	post, reason = edit(func(post *model.Post) { post.SetProps(nil) })
	require.Empty(t, reason)
	require.Equal(t, original.GetProps(), post.GetProps(), "props emptied, and all of them put back")

	// Files added by an edit, which Mattermost allows, are taken off again,
	// and the edit time with them.
	post, reason = edit(func(post *model.Post) {
		post.FileIds = model.StringArray{"fakefileid00000000000000000"}
		post.EditAt = clipsNow.UnixMilli()
	})
	require.Empty(t, reason)
	require.Empty(t, post.FileIds)
	require.Zero(t, post.EditAt, "no (edited)")

	// Hiding the link preview only ever takes something away.
	post, reason = edit(func(post *model.Post) { post.AddProp("remove_link_preview", "true") })
	require.Empty(t, reason)
	want := copyProps(original.GetProps())
	want["remove_link_preview"] = "true"
	require.Equal(t, want, post.GetProps())

	// Ordinary posts: anything goes, but asking to be drawn as a card, by
	// name or by anything that is not a string.
	for about, claim := range map[string]any{
		"by name":   "custom_villanet_clip",
		"in a list": []any{"custom_villanet_clip"},
	} {
		plain := &model.Post{Id: "plain", Message: "hello"}
		next := plain.Clone()
		next.Message = "hello again"
		next.SetProps(model.StringInterface{"type": claim, "keep": "this"})
		post, reason = updated(t, w.p, next, plain)
		require.Empty(t, reason, about)
		require.Equal(t, "hello again", post.Message, about)
		require.Equal(t, model.StringInterface{"keep": "this"}, post.GetProps(), about)
	}
	plain := &model.Post{Id: "plain", Message: "hello"}
	next := plain.Clone()
	next.SetProps(model.StringInterface{"type": "custom_somebody_else", "card": "theirs"})
	post, reason = updated(t, w.p, next, plain)
	require.Empty(t, reason)
	require.Equal(t, model.StringInterface{"type": "custom_somebody_else", "card": "theirs"}, post.GetProps(), "nothing of VillaNET's business")

	// A mark waiting to land is applied to whoever's update lands first, and
	// the marker's own update then changes nothing. Dan's patch of the title
	// lands between the mark going pending and the marker's update.
	var afterAuthor model.StringInterface
	srv.set(func(srv *shareServer) {
		srv.beforeUpdate = func(postID string) {
			patch := srv.post(postID)
			props := copyProps(patch.GetProps())
			props[clipPropTitle] = "dan's own title"
			delete(props, clipPropGone)
			patch.SetProps(props)

			_, appErr := srv.updatePost(patch)
			require.Nil(t, appErr)

			afterAuthor = copyProps(srv.post(postID).GetProps())
		}
	})

	w.p.markSharesGone([]settledClip{{ID: clipA, Reason: clipGoneClipper, Shares: []string{shared.PostID}}})

	require.Equal(t, "deleted", afterAuthor["villanet_clip_gone"], "the author's patch carried the mark")
	for _, key := range scrubbedOnDelete {
		require.NotContains(t, afterAuthor, key)
	}
	requireNamesGone(t, afterAuthor)
	marked := srv.post(shared.PostID)
	require.Equal(t, afterAuthor, marked.GetProps(), "and the marker's own update changed nothing")
	require.Equal(t, original.Message, marked.Message)
	require.Zero(t, marked.EditAt)
	require.Equal(t, 2, srv.updateCount(shared.PostID))
	requireNoPendingMarks(t, w.p)

	// With nothing pending, a patch carrying a mark is put back.
	post, reason = edit(func(post *model.Post) {
		props := post.GetProps()
		props[clipPropGone] = "expired"
		post.SetProps(props)
	})
	require.Empty(t, reason)
	require.Nil(t, post.GetProp(clipPropGone))

	// Never nil, but for a message edit.
	for i, change := range []func(post *model.Post){
		func(*model.Post) {},
		func(post *model.Post) { post.IsPinned = !post.IsPinned },
		func(post *model.Post) { post.SetProps(model.StringInterface{}) },
		func(post *model.Post) { post.FileIds = []string{"file"} },
		func(post *model.Post) { post.HasReactions = true },
	} {
		post, reason := edit(change)
		require.NotNil(t, post, i)
		require.Empty(t, reason, i)
	}
}

// scrubbedOnDelete are the props a deleted clip's posts lose, written out as
// A§4.5 names them rather than taken from the code's own list, which a test
// that used it could never catch shrinking.
var scrubbedOnDelete = []string{"villanet_clip_title", "villanet_clip_clipper", "villanet_clip_clipper_id", "villanet_clip_streamer", "villanet_clip_streamer_id"}

// requireNamesGone checks that a deleted clip's post names nobody, and says
// no more than that a clip of some length was shared here, once, and went.
func requireNamesGone(t *testing.T, props model.StringInterface) {
	t.Helper()

	text := fmt.Sprint(props)
	for _, name := range []string{"Bob", "Dave", "uid-bob", "uid-dave", "clip " + clipA} {
		require.NotContains(t, text, name)
	}
	for _, kept := range []string{"villanet_clip_id", "villanet_clip_duration_ms", "villanet_clip_created_at"} {
		require.Contains(t, props, kept)
	}
}

func requireNoPendingMarks(t *testing.T, p *Plugin) {
	t.Helper()

	pending := 0
	p.clips().shares.marks.Range(func(_, _ any) bool {
		pending++
		return true
	})
	require.Zero(t, pending, "nothing left pending")
}

// When a shared clip goes, every post it was shared in says so. Expiry keeps
// the title and the names, so the card can still say what it was; a delete by
// its clipper, its streamer or an admin takes them out. The message never
// changes, so no post shows "(edited)". A mark lost to another node's update
// is put back, at most three times in all. Posts that are not this clip's
// card are left alone, and one that cannot be read is skipped.
func TestSettledClipsMarkTheirSharedPostsGone(t *testing.T) {
	// shareAround shares the clip into two channels, and puts beside those
	// posts an ordinary post, another clip's card and a post that cannot be
	// read, as if they had been shared too.
	shareAround := func(t *testing.T, w *clipsWorld, srv *shareServer) ([]string, []string) {
		t.Helper()

		shares := []string{
			decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID,
			decodeShare(t, w.share(t, "uid-dave", clipA, offTopic), http.StatusCreated).PostID,
		}

		srv.set(func(srv *shareServer) {
			srv.posts["ordinary-post"] = &model.Post{Id: "ordinary-post", ChannelId: townSquare, Message: "hi", Props: model.StringInterface{clipPropID: clipA}}
			srv.posts["other-card"] = &model.Post{Id: "other-card", ChannelId: townSquare, Type: clipPostType, Props: model.StringInterface{clipPropID: clipB, clipPropTitle: "theirs"}}
			srv.posts["unreadable"] = &model.Post{Id: "unreadable", ChannelId: townSquare, Type: clipPostType, Props: model.StringInterface{clipPropID: clipA}}
			srv.getFails["unreadable"] = true
		})
		others := []string{"ordinary-post", "other-card", "unreadable", "never-was"}
		w.changeClip(t, clipA, func(entry *clipEntry) {
			entry.Shares = append(entry.Shares, others...)
		})

		return shares, others
	}

	requireMarked := func(t *testing.T, w *clipsWorld, srv *shareServer, shares, others []string, value string, before map[string]*model.Post) {
		t.Helper()

		w.p.clips().background.Wait()

		for _, postID := range shares {
			post := srv.post(postID)
			require.NotNil(t, post)
			require.Equal(t, value, post.GetProp(clipPropGone), postID)
			require.Equal(t, before[postID].Message, post.Message, "the message never changes")
			require.Zero(t, post.EditAt, "no (edited)")
			require.Equal(t, clipA, post.GetProp(clipPropID))
			require.Equal(t, before[postID].GetProp(clipPropDurationMs), post.GetProp(clipPropDurationMs))
			require.Equal(t, before[postID].GetProp(clipPropCreatedAt), post.GetProp(clipPropCreatedAt))

			for _, key := range scrubbedOnDelete {
				if value == "deleted" {
					require.NotContains(t, post.GetProps(), key, "%s: %s", postID, key)
				} else {
					require.NotEmpty(t, post.GetProp(key), "%s: %s", postID, key)
					require.Equal(t, before[postID].GetProp(key), post.GetProp(key), "%s: %s", postID, key)
				}
			}
			if value == "deleted" {
				requireNamesGone(t, post.GetProps())
			}
		}

		for _, postID := range others {
			require.Zero(t, srv.updateCount(postID), postID)
			if before[postID] != nil {
				require.Equal(t, before[postID].GetProps(), srv.post(postID).GetProps(), postID)
			}
		}

		requireNoPendingMarks(t, w.p)

		lines := logged(w.api, "LogInfo", "Shared clip posts marked")
		require.Len(t, lines, 1)
		require.Equal(t, []any{"Shared clip posts marked", "clip_id", clipA, "posts", 2}, []any(lines[0][:5]))
		require.Empty(t, logged(w.api, "LogInfo", "A shared clip post could not be marked"), "the others are skipped, not failed")
	}

	snapshot := func(srv *shareServer, ids ...[]string) map[string]*model.Post {
		posts := map[string]*model.Post{}
		for _, list := range ids {
			for _, postID := range list {
				if post := srv.post(postID); post != nil {
					posts[postID] = post
				}
			}
		}

		return posts
	}

	t.Run("expired", func(t *testing.T) {
		w, srv := newShareWorld(t)
		shares, others := shareAround(t, w, srv)
		before := snapshot(srv, shares, others)

		report := w.p.sweepClipsAt(context.Background(), clipsNow.Add(31*24*time.Hour))
		require.Equal(t, 1, report.Expired)

		requireMarked(t, w, srv, shares, others, "expired", before)
		require.Equal(t, []any{"reason", clipGoneExpired}, []any(logged(w.api, "LogInfo", "Shared clip posts marked")[0][5:7]))
	})

	for _, c := range []struct {
		name   string
		reason string
		remove func(t *testing.T, w *clipsWorld) *httptest.ResponseRecorder
	}{
		{name: "deleted by its clipper", reason: clipGoneClipper, remove: func(t *testing.T, w *clipsWorld) *httptest.ResponseRecorder {
			return w.call(t, "uid-bob", clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + clipA})
		}},
		{name: "deleted by its streamer", reason: clipGoneStreamer, remove: func(t *testing.T, w *clipsWorld) *httptest.ResponseRecorder {
			return w.call(t, "uid-dave", clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + clipA})
		}},
		{name: "deleted by an admin", reason: clipGoneAdmin, remove: func(t *testing.T, w *clipsWorld) *httptest.ResponseRecorder {
			return w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/clip?id=" + clipA})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, srv := newShareWorld(t)
			shares, others := shareAround(t, w, srv)
			before := snapshot(srv, shares, others)

			response := c.remove(t, w)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Nil(t, w.stored(t).clip(clipA))

			requireMarked(t, w, srv, shares, others, "deleted", before)
			require.Equal(t, []any{"reason", c.reason}, []any(logged(w.api, "LogInfo", "Shared clip posts marked")[0][5:7]))
		})
	}

	t.Run("a mark lost to another node", func(t *testing.T) {
		w, srv := newShareWorld(t)
		postID := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID

		srv.set(func(srv *shareServer) { srv.lose = 2 })
		w.p.markSharesGone([]settledClip{{ID: clipA, Reason: clipGoneClipper, Shares: []string{postID}}})
		require.Equal(t, "deleted", srv.post(postID).GetProp(clipPropGone), "the third time")
		require.Equal(t, 3, srv.updateCount(postID))
		requireNoPendingMarks(t, w.p)

		// Already marked: nothing more is done.
		w.p.markSharesGone([]settledClip{{ID: clipA, Reason: clipGoneClipper, Shares: []string{postID}}})
		require.Equal(t, 3, srv.updateCount(postID))
	})

	t.Run("a mark lost every time", func(t *testing.T) {
		w, srv := newShareWorld(t)
		postID := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID

		srv.set(func(srv *shareServer) { srv.lose = 5 })
		w.p.markSharesGone([]settledClip{{ID: clipA, Reason: clipGoneExpired, Shares: []string{postID}}})
		require.Nil(t, srv.post(postID).GetProp(clipPropGone))
		require.Equal(t, 3, srv.updateCount(postID), "three times and no more")
		requireNoPendingMarks(t, w.p)

		lines := logged(w.api, "LogInfo", "A shared clip post could not be marked")
		require.Len(t, lines, 1)
		require.Equal(t, []any{
			"A shared clip post could not be marked", "post_id", postID, "clip_id", clipA,
			"mark", "expired", "error", "another update landed over the mark",
		}, []any(lines[0]))
	})

	t.Run("an archived channel", func(t *testing.T) {
		w, srv := newShareWorld(t)
		postID := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID

		srv.set(func(srv *shareServer) { srv.channels[townSquare].DeleteAt = 1 })
		w.p.markSharesGone([]settledClip{{ID: clipA, Reason: clipGoneAdmin, Shares: []string{postID}}})
		require.Nil(t, srv.post(postID).GetProp(clipPropGone), "Mattermost refuses it, and the post keeps what it had")
		require.Equal(t, "clip "+clipA, srv.post(postID).GetProp(clipPropTitle))
		requireNoPendingMarks(t, w.p)

		// Said where an admin will see it, since the post still names the
		// clip and its people.
		lines := logged(w.api, "LogInfo", "A shared clip post could not be marked")
		require.Len(t, lines, 1)
		require.Equal(t, []any{"A shared clip post could not be marked", "post_id", postID, "clip_id", clipA, "mark", "deleted", "error"}, []any(lines[0][:8]))
		require.Contains(t, lines[0][8], "can_not_update_post_in_deleted")
	})
}

// A pass and a pending mark are each removed by whoever made them; the
// clean-up on insert is only for one left behind, which goes once it is 30
// seconds or 10 minutes old, and not before.
func TestSharePassesAndMarksArePruned(t *testing.T) {
	w, _ := newShareWorld(t)
	shares := &w.p.clips().shares

	held := func(m *sync.Map, key string) bool {
		_, found := m.Load(key)
		return found
	}

	shares.issue("aaaaaaaaaaaaaaaa", sharePass{expires: clipsNow.Add(30 * time.Second)}, clipsNow)
	shares.issue("bbbbbbbbbbbbbbbb", sharePass{expires: clipsNow.Add(59 * time.Second)}, clipsNow.Add(29*time.Second))
	require.True(t, held(&shares.passes, "aaaaaaaaaaaaaaaa"), "29 seconds on, still good")
	shares.issue("cccccccccccccccc", sharePass{expires: clipsNow.Add(60 * time.Second)}, clipsNow.Add(30*time.Second))
	require.False(t, held(&shares.passes, "aaaaaaaaaaaaaaaa"), "30 seconds on, gone")
	require.True(t, held(&shares.passes, "bbbbbbbbbbbbbbbb"))
	require.True(t, held(&shares.passes, "cccccccccccccccc"))

	shares.holdMark("post-1", pendingMark{clipID: clipA, value: "expired", at: clipsNow})
	shares.holdMark("post-2", pendingMark{clipID: clipA, value: "expired", at: clipsNow.Add(9*time.Minute + 59*time.Second)})
	require.True(t, held(&shares.marks, "post-1"), "not yet ten minutes")
	shares.holdMark("post-3", pendingMark{clipID: clipA, value: "expired", at: clipsNow.Add(10 * time.Minute)})
	require.False(t, held(&shares.marks, "post-1"), "ten minutes, gone")
	require.True(t, held(&shares.marks, "post-2"))
	require.True(t, held(&shares.marks, "post-3"))
}
