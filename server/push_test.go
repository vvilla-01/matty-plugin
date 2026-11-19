package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// The push scene: the access scene's people and store, a clock that stands
// still until a test moves it, sessions a test can end or make unreadable,
// Mattermost's own push settings, and push servers on this machine, listed
// as the admin would list them, that keep what reaches them and answer as a
// test says.

// testPushKey is the push key every test signs with: fixed, so that the
// contract's fixtures hold its public half.
func testPushKey(t *testing.T) *webpush.Key {
	t.Helper()

	scalar := sha256.Sum256([]byte("a push key for tests only"))
	key, err := webpush.ParseKey(scalar[:])
	require.NoError(t, err)

	return key
}

type pushScene struct {
	*accessScene
	t *testing.T

	sessions     map[string]*model.Session
	sessionFails map[string]bool
	sessionReads map[string]int
	config       *model.Config
	license      *model.License

	servers []*fakePushServer
}

func newPushScene(t *testing.T) *pushScene {
	t.Helper()

	s := &pushScene{
		accessScene:  newAccessScene(t),
		t:            t,
		sessions:     map[string]*model.Session{},
		sessionFails: map[string]bool{},
		sessionReads: map[string]int{},
		config:       mattermostPushing(true, model.GenericNotificationServer),
	}

	// Mattermost's settings and licence as this scene says.
	calls := s.api.ExpectedCalls[:0]
	for _, call := range s.api.ExpectedCalls {
		if call.Method != "GetConfig" {
			calls = append(calls, call)
		}
	}
	s.api.ExpectedCalls = calls
	s.api.On("GetConfig").Return(func() *model.Config {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.config
	}).Maybe()
	s.api.On("GetLicense").Return(func() *model.License {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.license
	}).Maybe()
	s.api.On("GetSession", mock.Anything).Return(func(id string) (*model.Session, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		s.sessionReads[id]++
		if s.sessionFails[id] {
			return nil, model.NewAppError("GetSession", "app.session.get.app_error", nil, "injected failure", http.StatusInternalServerError)
		}
		session, known := s.sessions[id]
		if !known {
			return nil, model.NewAppError("GetSession", "app.session.missing", nil, "", http.StatusNotFound)
		}
		copied := *session

		return &copied, nil
	}).Maybe()

	r := s.p.pushes()
	r.now = s.clock
	r.retryWaits = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}
	r.messageWaits = make([]time.Duration, len(pushMessageRetryWaits))
	for i := range r.messageWaits {
		r.messageWaits[i] = 5 * time.Millisecond
	}
	r.noSubscriberWaits = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}
	r.noSubscriberEvery = 5 * time.Millisecond
	r.helloDelay = time.Millisecond
	r.sweepPace = time.Millisecond
	r.schedule = func(cluster.JobPluginAPI, func()) (clipsJob, error) { return nil, nil }
	r.setKey(testPushKey(t))

	t.Cleanup(s.p.stopPush)

	return s
}

func mattermostPushing(on bool, server string) *model.Config {
	config := &model.Config{}
	config.EmailSettings.SendPushNotifications = model.NewPointer(on)
	config.EmailSettings.PushNotificationServer = model.NewPointer(server)

	return config
}

func (s *pushScene) clock() time.Time {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.now
}

// configure switches push on, with these push servers listed, and starts
// sending.
func (s *pushScene) configure(edit func(cfg *configuration)) {
	cfg := s.p.getConfiguration().Clone()
	cfg.EnablePush = true
	var hosts []string
	for _, server := range s.servers {
		hosts = append(hosts, server.host)
	}
	cfg.PushAllowedHosts = strings.Join(hosts, "\n")
	if edit != nil {
		edit(cfg)
	}
	cfg.normalize()
	s.p.setConfiguration(cfg)
	s.p.pushConfigured(cfg)
}

func (s *pushScene) start() {
	s.p.startPush()
}

// session makes a live session for somebody.
func (s *pushScene) session(id, userID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.sessions[id] = &model.Session{Id: id, UserId: userID, ExpiresAt: model.GetMillis() + time.Hour.Milliseconds()}
	delete(s.sessionFails, id)
}

func (s *pushScene) endSession(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.sessions[id].ExpiresAt = model.GetMillis() - 1
}

func (s *pushScene) failSession(id string, failing bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.sessionFails[id] = failing
}

// readsOfSession is how many times a session was read.
func (s *pushScene) readsOfSession(id string) int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.sessionReads[id]
}

// call is a request as a phone's session makes it.
func (s *pushScene) call(method, target, userID, session string, body any) *httptest.ResponseRecorder {
	var text string
	switch value := body.(type) {
	case nil:
	case string:
		text = value
	default:
		encoded, err := json.Marshal(value)
		require.NoError(s.t, err)
		text = string(encoded)
	}

	request := httptest.NewRequest(method, target, strings.NewReader(text))
	request.Header.Set("Mattermost-User-Id", userID)
	recorder := httptest.NewRecorder()
	s.p.ServeHTTP(&plugin.Context{SessionId: session}, recorder, request)

	return recorder
}

func (s *pushScene) view(recorder *httptest.ResponseRecorder) pushStateView {
	s.t.Helper()
	require.Equal(s.t, http.StatusOK, recorder.Code, recorder.Body.String())

	var view pushStateView
	require.NoError(s.t, json.Unmarshal(recorder.Body.Bytes(), &view))

	return view
}

func (s *pushScene) doc(userID string) *pushDoc {
	s.t.Helper()

	s.faults.lock.Lock()
	raw := append([]byte(nil), s.store[pushUserKey(userID)]...)
	s.faults.lock.Unlock()

	return decodePushDoc(raw)
}

func (s *pushScene) device(userID, session string) *pushDevice {
	doc := s.doc(userID)
	if i := doc.find(session); i >= 0 {
		return &doc.Devices[i]
	}

	return nil
}

// register registers a phone for a session and confirms its hello, as Matty
// does, and returns the hello.
func (s *pushScene) register(phone *testPhone, userID, session string) map[string]any {
	s.t.Helper()

	view := s.view(s.call(http.MethodPut, mePushPath, userID, session, phone.registration(s.p.pushes().publicKey())))
	require.Equal(s.t, pushStatePending, view.State)

	hello := phone.next(s.t)
	require.Equal(s.t, pushKindHello, hello["type"])
	confirmed := s.view(s.call(http.MethodPost, mePushConfirmPath, userID, session, map[string]any{"nonce": hello["nonce"]}))
	require.Equal(s.t, pushStateConfirmed, confirmed.State)

	return hello
}

// fakePushServer is a push server on this machine.
type fakePushServer struct {
	srv  *httptest.Server
	host string

	lock    sync.Mutex
	answers map[string][]fakeAnswer
	held    chan struct{}
	got     map[string]chan fakePush
	count   int
}

type fakeAnswer struct {
	status     int
	retryAfter string
}

type fakePush struct {
	header http.Header
	body   []byte
}

func (s *pushScene) pushServer() *fakePushServer {
	server := &fakePushServer{answers: map[string][]fakeAnswer{}, got: map[string]chan fakePush{}}
	server.srv = httptest.NewServer(http.HandlerFunc(server.serve))
	s.t.Cleanup(server.close)

	parsed, err := url.Parse(server.srv.URL)
	require.NoError(s.t, err)
	server.host = parsed.Host
	s.servers = append(s.servers, server)

	return server
}

func (f *fakePushServer) close() {
	f.release()
	f.srv.Close()
}

func (f *fakePushServer) inbox(id string) chan fakePush {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.got[id] == nil {
		f.got[id] = make(chan fakePush, 64)
	}

	return f.got[id]
}

func (f *fakePushServer) serve(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/push/")
	body, _ := io.ReadAll(r.Body)

	f.lock.Lock()
	held := f.held
	f.count++
	answer := fakeAnswer{status: http.StatusCreated}
	if queue := f.answers[id]; len(queue) > 0 {
		answer = queue[0]
		f.answers[id] = queue[1:]
	}
	f.lock.Unlock()

	if held != nil {
		select {
		case <-held:
		case <-r.Context().Done():
			return
		}
	}

	if answer.retryAfter != "" {
		w.Header().Set("Retry-After", answer.retryAfter)
	}
	w.WriteHeader(answer.status)
	if answer.status >= 200 && answer.status < 300 {
		f.inbox(id) <- fakePush{header: r.Header.Clone(), body: body}
	}
}

// answer queues what the next pushes to an id are answered.
func (f *fakePushServer) answer(id string, answers ...fakeAnswer) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.answers[id] = append(f.answers[id], answers...)
}

// hold makes every request wait, a tarpit, until release.
func (f *fakePushServer) hold() {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.held = make(chan struct{})
}

func (f *fakePushServer) release() {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.held != nil {
		close(f.held)
		f.held = nil
	}
}

func (f *fakePushServer) requests() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.count
}

// testPhone is a Matty registered at a push server: its keys, and its
// address there.
type testPhone struct {
	server   *fakePushServer
	id       string
	private  *ecdh.PrivateKey
	auth     []byte
	endpoint string
}

func newTestPhone(t *testing.T, server *fakePushServer, id string) *testPhone {
	t.Helper()

	private, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)

	return &testPhone{server: server, id: id, private: private, auth: auth, endpoint: server.srv.URL + "/push/" + id}
}

func (ph *testPhone) registration(key string) pushRegistration {
	return pushRegistration{
		Endpoint: ph.endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(ph.private.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(ph.auth),
		Key:      key,
	}
}

// next is the next push that reached the phone, decrypted.
func (ph *testPhone) next(t *testing.T) map[string]any {
	t.Helper()

	select {
	case push := <-ph.server.inbox(ph.id):
		plaintext, err := webpush.Decrypt(push.body, ph.private, ph.auth)
		require.NoError(t, err)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(plaintext, &payload))

		return payload
	case <-time.After(5 * time.Second):
		t.Fatalf("no push reached %s", ph.id)
		return nil
	}
}

// nextRaw is the next push as it arrived.
func (ph *testPhone) nextRaw(t *testing.T) fakePush {
	t.Helper()

	select {
	case push := <-ph.server.inbox(ph.id):
		return push
	case <-time.After(5 * time.Second):
		t.Fatalf("no push reached %s", ph.id)
		return fakePush{}
	}
}

// nothing is that no push reaches the phone for a while.
func (ph *testPhone) nothing(t *testing.T, within time.Duration) {
	t.Helper()

	select {
	case push := <-ph.server.inbox(ph.id):
		plaintext, _ := webpush.Decrypt(push.body, ph.private, ph.auth)
		t.Fatalf("a push reached %s: %s", ph.id, plaintext)
	case <-time.After(within):
	}
}

// eventually waits for a condition the senders bring about.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// message is a push Mattermost would make for a post.
func message(post, channel, text string) *model.PushNotification {
	return &model.PushNotification{
		Type: model.PushTypeMessage, Version: model.PushMessageV2, Category: model.CategoryCanReply,
		PostId: post, ChannelId: channel, TeamId: "team-1", SenderId: "uid-bob", SenderName: "bob",
		ChannelName: "Town Square", ChannelType: model.ChannelTypeOpen, Message: text,
	}
}
