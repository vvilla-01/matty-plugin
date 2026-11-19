package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// The key is made once, by whichever node writes it first, and every other
// node, and every later start, uses that one.
func TestThePushKeyIsMadeOnceAndShared(t *testing.T) {
	first, store := kvBackedPlugin()
	first.loadPushKey()
	require.Len(t, store[kvPushKey], 32)
	public := first.pushes().publicKey()
	require.Len(t, public, webpush.PublicKeyLength)

	again, _ := kvBackedPluginOn(store)
	again.loadPushKey()
	require.Equal(t, public, again.pushes().publicKey(), "a restart keeps the key")

	// Two nodes starting together: the one that loses the write adopts the
	// winner's key.
	loser, store, faults := kvBackedPluginWithFaultsOn(map[string][]byte{}, newKVFaults())
	winner := testPushKey(t)
	faults.interfere = func(key string, store map[string][]byte) {
		if key == kvPushKey {
			store[key] = winner.Bytes()
		}
	}
	loser.loadPushKey()
	require.Equal(t, winner.Public(), loser.pushes().publicKey())
	require.Equal(t, winner.Bytes(), store[kvPushKey])
}

// /access says on, with the key, only when the switch is on, the key is
// loaded and Mattermost decides to push; otherwise off, with why, naming the
// console where that is the reason. It reads nothing from the store for it.
func TestAccessSaysWhetherPushIsOn(t *testing.T) {
	s := newPushScene(t)

	access := func() pushWord {
		recorder := s.call(http.MethodGet, "/api/v1/access", sceneLetIn, "sess-erin", nil)
		require.Equal(t, http.StatusOK, recorder.Code)
		var answer accessResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
		return answer.Push
	}

	require.Equal(t, pushWord{State: "off", Reason: pushOffText}, access())

	s.configure(nil)
	require.Equal(t, pushWord{State: "on", Key: testPushKey(t).Public()}, access())

	s.resetReads()
	s.p.pushWordNow(s.p.getConfiguration())
	require.Zero(t, s.totalReads(), "no store read")

	// Mattermost's own switch, read again once a settings load says so.
	s.lock.Lock()
	s.config = mattermostPushing(false, model.GenericNotificationServer)
	s.lock.Unlock()
	require.Equal(t, "on", access().State, "worked out at most once a minute")
	s.p.pushConfigured(s.p.getConfiguration())
	word := access()
	require.Equal(t, "off", word.State)
	require.Empty(t, word.Key)
	require.Contains(t, word.Reason, "System Console > Environment > Push Notification Server")

	// A hosted push server without the licence for it.
	s.lock.Lock()
	s.config = mattermostPushing(true, model.MHPNSGlobal)
	s.lock.Unlock()
	s.advance(pushCanPushEvery)
	require.Equal(t, pushWord{State: "off", Reason: pushMattermostLicenceText}, access())
	s.lock.Lock()
	s.license = &model.License{Features: &model.Features{MHPNS: model.NewPointer(true)}}
	s.lock.Unlock()
	s.advance(pushCanPushEvery)
	require.Equal(t, "on", access().State)

	// No key yet.
	s.p.pushes().mu.Lock()
	s.p.pushes().key = nil
	s.p.pushes().mu.Unlock()
	require.Equal(t, pushWord{State: "off", Reason: pushNotReadyText}, access())
}

// An admin's /me says when push is on here and Mattermost pushes to nobody;
// nobody else's does.
func TestThePushAlertIsForAdmins(t *testing.T) {
	s := newPushScene(t)
	s.configure(nil)
	s.lock.Lock()
	s.config = mattermostPushing(false, "")
	s.lock.Unlock()
	s.p.pushConfigured(s.p.getConfiguration())

	me := func(userID string) meResponse {
		recorder := s.call(http.MethodGet, "/api/v1/me", userID, "", nil)
		require.Equal(t, http.StatusOK, recorder.Code)
		var answer meResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
		return answer
	}

	require.Equal(t, pushMattermostOffText, me(sceneLockedAdmin).PushAlert)
	require.Empty(t, me(sceneLinked).PushAlert)

	cfg := s.p.getConfiguration().Clone()
	cfg.EnablePush = false
	s.p.setConfiguration(cfg)
	require.Empty(t, me(sceneLockedAdmin).PushAlert, "nothing to say while push is off here")
}

// Registering: a hello goes to the new address, and only its nonce
// confirms it; once confirmed, the same registration again changes nothing
// and sends nothing; a new one waits for its own hello while the confirmed
// one stays in use.
func TestARegistrationIsConfirmedByItsHello(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	key := s.p.pushes().publicKey()

	pending := s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)))
	require.Equal(t, pushStateView{State: pushStatePending, Host: server.host, Since: s.clock().UnixMilli()}, pending)
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)

	hello := phone.next(t)
	require.Equal(t, sceneLinked, hello["user"])
	nonce, _ := hello["nonce"].(string)
	require.Len(t, nonce, 22)

	wrong := s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": strings.Repeat("A", 22)})
	requireRefused(t, wrong, http.StatusConflict, codeConflict, "a wrong nonce")
	otherSession := s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-other", map[string]any{"nonce": nonce})
	requireRefused(t, otherSession, http.StatusConflict, codeConflict, "another session's")

	confirmed := s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": nonce}))
	require.Equal(t, pushStateConfirmed, confirmed.State)
	require.Equal(t, server.host, confirmed.Host)
	people, _, err := s.p.readPushPeople()
	require.NoError(t, err)
	require.Contains(t, people.People, sceneLinked)

	again := s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": nonce})
	requireRefused(t, again, http.StatusConflict, codeConflict, "a nonce answers once")

	// The same registration: confirmed, no hello, no write.
	before := server.requests()
	writes := s.faults.writesTo(pushUserKey(sceneLinked))
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key))).State)
	phone.nothing(t, 50*time.Millisecond)
	require.Equal(t, before, server.requests())
	require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)), "seen moves at most once a day")

	// A day on, seen moves.
	s.advance(pushSeenEvery)
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	require.Equal(t, s.clock().UnixMilli(), s.device(sceneLinked, "sess-dave").Seen)

	// A new address waits for its own hello, and the confirmed one is used
	// meanwhile.
	moved := newTestPhone(t, server, "dave-moved")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", moved.registration(key))).State)
	newHello := moved.next(t)
	require.NotEqual(t, nonce, newHello["nonce"], "a fresh nonce")
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "hello"), sceneLinked)
	require.Equal(t, "post-1", phone.next(t)["post"], "the confirmed address still gets pushes")
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": newHello["nonce"]})).State)
	s.p.NotificationWillBePushed(message("post-2", "chan-1", "again"), sceneLinked)
	require.Equal(t, "post-2", moved.next(t)["post"])
	phone.nothing(t, 50*time.Millisecond)
}

// A pending registration not confirmed within ten minutes is gone; the
// confirmed one it would have replaced stays.
func TestAnUnconfirmedRegistrationExpires(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	key := s.p.pushes().publicKey()

	phone := newTestPhone(t, server, "dave")
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)))
	hello := phone.next(t)
	s.advance(pushPendingLife)
	require.Equal(t, pushStateNone, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	requireRefused(t, s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": hello["nonce"]}),
		http.StatusConflict, codeConflict, "an expired hello")

	s.register(phone, sceneLinked, "sess-dave")
	moved := newTestPhone(t, server, "dave-moved")
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", moved.registration(key)))
	moved.next(t)
	s.advance(pushPendingLife)
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	require.Nil(t, s.device(sceneLinked, "sess-dave").Pending)
}

// What the routes refuse, each with a sentence that quotes nothing it was
// sent, and what they allow while push is off.
func TestThePushRoutesRefuse(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	key := s.p.pushes().publicKey()
	good := phone.registration(key)

	for _, call := range []struct{ method, path string }{
		{http.MethodPut, mePushPath}, {http.MethodPost, mePushConfirmPath}, {http.MethodPost, mePushTestPath},
	} {
		recorder := s.call(call.method, call.path, sceneLinked, "sess-dave", good)
		requireRefused(t, recorder, http.StatusServiceUnavailable, codeUnavailable, call.path)
		require.Equal(t, pushOffText, codedError(t, recorder).Error)
	}
	require.Equal(t, pushStateNone, s.view(s.call(http.MethodDelete, mePushPath, sceneLinked, "sess-dave", nil)).State, "a takeback works while off")
	require.Equal(t, pushStateNone, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)

	s.configure(nil)
	s.start()

	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "", good), http.StatusBadRequest, codeBadRequest, "no session")
	requireRefused(t, s.call(http.MethodPost, mePushPath, sceneLinked, "sess-dave", good), http.StatusMethodNotAllowed, codeMethodNotAllowed, "POST")

	bad := func(edit func(*pushRegistration)) *pushRegistration {
		copied := good
		edit(&copied)
		return &copied
	}
	cases := map[string]*pushRegistration{
		"not listed, plain http": bad(func(r *pushRegistration) { r.Endpoint = "http://push.example.org/up/secret-topic" }),
		"a private address":      bad(func(r *pushRegistration) { r.Endpoint = "https://10.1.2.3/up/secret-topic" }),
		"localhost":              bad(func(r *pushRegistration) { r.Endpoint = "https://localhost/up/secret-topic" }),
		"a name under localhost": bad(func(r *pushRegistration) { r.Endpoint = "https://push.localhost:8443/up/secret-topic" }),
		"a user name":            bad(func(r *pushRegistration) { r.Endpoint = "https://who:secret-topic@push.example.org/up" }),
		"a fragment":             bad(func(r *pushRegistration) { r.Endpoint = "https://push.example.org/up#secret-topic" }),
		"too long": bad(func(r *pushRegistration) {
			r.Endpoint = "https://push.example.org/" + strings.Repeat("secret-topic", 90)
		}),
		"another scheme":           bad(func(r *pushRegistration) { r.Endpoint = "ftp://push.example.org/secret-topic" }),
		"a short public key":       bad(func(r *pushRegistration) { r.P256dh = r.P256dh[:40] }),
		"a public key not a point": bad(func(r *pushRegistration) { r.P256dh = "BA" + strings.Repeat("A", 85) }),
		"a short secret":           bad(func(r *pushRegistration) { r.Auth = "c2VjcmV0" }),
	}
	for about, registration := range cases {
		recorder := s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", registration)
		requireRefused(t, recorder, http.StatusBadRequest, codeBadRequest, about)
		if strings.Contains(about, "localhost") {
			require.Equal(t, pushEndpointText(pushsend.ErrEndpointAddress), codedError(t, recorder).Error, about)
		}
		require.NotContains(t, recorder.Body.String(), "secret-topic", about)
		require.NotContains(t, recorder.Body.String(), registration.P256dh, about)
	}

	stale := s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", bad(func(r *pushRegistration) { r.Key = key[:86] + map[bool]string{true: "A", false: "B"}[key[86] != 'A'] }))
	requireRefused(t, stale, http.StatusBadRequest, codePushKey, "another key")
	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", "{"), http.StatusBadRequest, codeBadRequest, "not JSON")
	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", `{"endpoint":"`+strings.Repeat("x", maxPushBody)+`"}`),
		http.StatusRequestEntityTooLarge, codeTooLarge, "over 4 KiB")
	require.Equal(t, 0, server.requests(), "nothing refused reached a push server")

	// Six registrations in ten minutes a person; the same one again is free.
	for i := range 6 {
		other := newTestPhone(t, server, "dave-"+string(rune('a'+i)))
		require.Equal(t, http.StatusOK, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", other.registration(key)).Code)
	}
	limited := s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", good)
	requireRefused(t, limited, http.StatusTooManyRequests, codeRateLimited, "the seventh")
	require.Positive(t, codedError(t, limited).RetryAfterMs)

	// A test needs a confirmed device, and three a minute.
	s.session("sess-erin", sceneLetIn)
	requireRefused(t, s.call(http.MethodPost, mePushTestPath, sceneLetIn, "sess-erin", nil), http.StatusConflict, codeConflict, "no device")
	erin := newTestPhone(t, server, "erin")
	s.register(erin, sceneLetIn, "sess-erin")
	for range 3 {
		require.Equal(t, http.StatusAccepted, s.call(http.MethodPost, mePushTestPath, sceneLetIn, "sess-erin", nil).Code)
		require.Equal(t, pushKindTest, erin.next(t)["type"])
	}
	requireRefused(t, s.call(http.MethodPost, mePushTestPath, sceneLetIn, "sess-erin", nil), http.StatusTooManyRequests, codeRateLimited, "the fourth test")
}

// A ninth session's device replaces the one least recently seen.
func TestANinthDeviceReplacesTheOldest(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	key := s.p.pushes().publicKey()
	s.p.pushes().registerLimiter = newRateLimiter(100, time.Minute)

	for i := range pushDevicesPerPerson + 1 {
		session := "sess-" + string(rune('a'+i))
		s.session(session, sceneLinked)
		phone := newTestPhone(t, server, session)
		require.Equal(t, http.StatusOK, s.call(http.MethodPut, mePushPath, sceneLinked, session, phone.registration(key)).Code)
		phone.next(t)
		s.advance(time.Second)
	}

	doc := s.doc(sceneLinked)
	require.Len(t, doc.Devices, pushDevicesPerPerson)
	require.Negative(t, doc.find("sess-a"), "the oldest went")
	require.GreaterOrEqual(t, doc.find("sess-i"), 0)
}

// Deleting takes the session's device and its waiting side away, and the
// person off the index with their last.
func TestDeletingARegistration(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	s.register(phone, sceneLinked, "sess-dave")

	require.Equal(t, pushStateNone, s.view(s.call(http.MethodDelete, mePushPath, sceneLinked, "sess-dave", nil)).State)
	s.faults.lock.Lock()
	_, kept := s.store[pushUserKey(sceneLinked)]
	s.faults.lock.Unlock()
	require.False(t, kept)
	people, _, err := s.p.readPushPeople()
	require.NoError(t, err)
	require.NotContains(t, people.People, sceneLinked)

	s.p.NotificationWillBePushed(message("post-1", "chan-1", "hello"), sceneLinked)
	phone.nothing(t, 50*time.Millisecond)
}

// The System Console's card: for system admins only; counts by push server
// and never by person.
func TestTheAdminCardCountsByPushServer(t *testing.T) {
	s := newPushScene(t)
	one, two := s.pushServer(), s.pushServer()
	s.configure(func(cfg *configuration) { cfg.PushAllowedHosts += "\nnot a host" })
	s.start()
	key := s.p.pushes().publicKey()

	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, one, "dave"), sceneLinked, "sess-dave")
	s.session("sess-erin", sceneLetIn)
	s.register(newTestPhone(t, one, "erin"), sceneLetIn, "sess-erin")
	s.session("sess-bob", sceneLinkedAdmin)
	waiting := newTestPhone(t, two, "bob")
	s.view(s.call(http.MethodPut, mePushPath, sceneLinkedAdmin, "sess-bob", waiting.registration(key)))
	waiting.next(t)

	// A confirmed phone with a run of failures is working, as GET /me/push
	// says; only one whose state is failing is failing.
	_, err := s.p.updatePushDoc(sceneLinked, func(doc *pushDoc, now time.Time) bool {
		doc.Devices[0].Trouble = now.UnixMilli()
		return true
	})
	require.NoError(t, err)
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)

	requireRefused(t, s.call(http.MethodGet, adminPushPath, sceneLinked, "", nil), http.StatusForbidden, codeForbidden, "not an admin")

	recorder := s.call(http.MethodGet, adminPushPath, sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	var view pushAdminView
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &view))
	require.True(t, view.On)
	require.True(t, view.Ready)
	require.True(t, view.Mattermost)
	require.Equal(t, 3, view.People)
	require.Equal(t, 3, view.Devices)
	require.Equal(t, []pushHostRow{
		{Host: one.host, Devices: 2, Working: 2},
		{Host: two.host, Devices: 1, Waiting: 1},
	}, view.Hosts)

	failingSince := s.clock().Add(-time.Hour).UnixMilli()
	_, err = s.p.updatePushDoc(sceneLetIn, func(doc *pushDoc, _ time.Time) bool {
		doc.Devices[0].State, doc.Devices[0].Since, doc.Devices[0].Kind = pushStateFailing, failingSince, "answer"
		return true
	})
	require.NoError(t, err)
	recorder = s.call(http.MethodGet, adminPushPath, sceneLockedAdmin, "", nil)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &view))
	require.Equal(t, []pushHostRow{
		{Host: one.host, Devices: 2, Working: 1, Failing: 1, FailingSince: failingSince},
		{Host: two.host, Devices: 1, Waiting: 1},
	}, view.Hosts)
	for _, person := range []string{sceneLinked, sceneLetIn, sceneLinkedAdmin, "dave", "erin"} {
		require.NotContains(t, recorder.Body.String(), person, "never anybody by name or id")
	}
	require.NotContains(t, recorder.Body.String(), "/push/")
}

// The list of push servers is kept in its canonical form, and a line that is
// not a host is left out, by number.
func TestTheListOfPushServersIsNormalised(t *testing.T) {
	cfg := &configuration{PushAllowedHosts: "NTFY.Home.LAN.\n\nhttps://push.example.org/\n[::FFFF:192.168.1.30]:08080\n192.168.1.40", PushContact: "  mailto:admin@example.com "}
	problem := pushHostsProblem(cfg.PushAllowedHosts)
	cfg.normalize()

	require.Equal(t, "ntfy.home.lan\n192.168.1.30:8080\n192.168.1.40", cfg.PushAllowedHosts)
	require.Equal(t, "Line 3 of Push servers on your own network is not a host or host:port, or past the first 100 entries, and left out.", problem)
	require.Equal(t, "mailto:admin@example.com", cfg.PushContact)

	cfg.PushContact = "admin@example.com"
	cfg.normalize()
	require.Empty(t, cfg.PushContact, "a contact that is neither mailto: nor https: is blanked")
	require.Empty(t, pushHostsProblem("a.example\nb.example"))
	require.Contains(t, pushHostsProblem("http://a\nb c"), "Lines 1, 2 ")
}

// A bad line is logged by number when the settings load, and once.
func TestABadLineOfPushServersIsLogged(t *testing.T) {
	s := newPushScene(t)
	stored := map[string]any{"pushallowedhosts": "ok.example\nhttps://not/a/host"}
	s.api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{Plugins: map[string]map[string]any{manifestID: stored}}}).Maybe()
	s.api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		cfg, ok := args.Get(0).(*configuration)
		require.True(t, ok)
		cfg.PushAllowedHosts = "ok.example\nhttps://not/a/host"
	}).Return(nil).Maybe()

	logged := func() int {
		n := 0
		for _, call := range s.api.Calls {
			if call.Method == "LogWarn" && strings.HasPrefix(call.Arguments.String(0), "Line 2 of Push servers") {
				n++
			}
		}
		return n
	}

	require.NoError(t, s.p.OnConfigurationChange())
	require.NoError(t, s.p.OnConfigurationChange())
	require.Equal(t, 1, logged())
	require.Equal(t, "ok.example", s.p.getConfiguration().PushAllowedHosts)
	require.Equal(t, "ok.example", s.p.pushes().sendClient().Rules().Listed.String(), "the client sends under the saved list")
}

// The ids a fixture names for its people are the scene's, never anybody's
// push address or key.
func TestNoPushAnswerCarriesAnAddressOrAKey(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	registration := phone.registration(s.p.pushes().publicKey())

	for _, recorder := range []*bytes.Buffer{
		s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", registration).Body,
		s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil).Body,
	} {
		require.NotContains(t, recorder.String(), "/push/")
		require.NotContains(t, recorder.String(), registration.P256dh)
		require.NotContains(t, recorder.String(), registration.Auth)
	}
}

// The index of people with a device is written with no expiry: it is kept
// right by the joins, the leaves and the sweep, and one that expired would
// stop every push on the server. A person's own row still expires.
func TestTheIndexOfPeopleNeverExpires(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, server, "dave"), sceneLinked, "sess-dave")
	require.Equal(t, http.StatusOK, s.call(http.MethodDelete, mePushPath, sceneLinked, "sess-dave", nil).Code)

	expiries := map[string][]int64{}
	for _, call := range s.api.Calls {
		if call.Method != "KVSetWithOptions" {
			continue
		}
		options, ok := call.Arguments.Get(2).(model.PluginKVSetOptions)
		require.True(t, ok)
		key := call.Arguments.String(0)
		expiries[key] = append(expiries[key], options.ExpireInSeconds)
	}
	require.Len(t, expiries[kvPushPeople], 2, "a join and a leave")
	for _, expiry := range expiries[kvPushPeople] {
		require.Zero(t, expiry)
	}
	require.NotEmpty(t, expiries[pushUserKey(sceneLinked)])
	for _, expiry := range expiries[pushUserKey(sceneLinked)] {
		require.Equal(t, pushRowSeconds, expiry)
	}
}

// A person with a device who is missing from the index (it was lost, a join
// failed, or they were the oldest when it was full) is put back by the next
// GET /me/push or the same PUT, and gets pushes again; one who is there
// costs no write.
func TestAPersonMissingFromTheIndexIsPutBack(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	s.register(phone, sceneLinked, "sess-dave")
	key := s.p.pushes().publicKey()

	lose := func() {
		s.faults.lock.Lock()
		delete(s.store, kvPushPeople)
		s.faults.lock.Unlock()
		s.p.refreshPushPeople()
		s.p.NotificationWillBePushed(message("post-lost", "chan-1", "lost"), sceneLinked)
		phone.nothing(t, 50*time.Millisecond)
	}
	inIndex := func() bool {
		people, _, err := s.p.readPushPeople()
		require.NoError(t, err)
		_, in := people.People[sceneLinked]
		return in
	}

	writes := s.faults.writesTo(kvPushPeople)
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	require.Equal(t, writes, s.faults.writesTo(kvPushPeople), "no write for a person who is there")

	lose()
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	require.True(t, inIndex(), "GET /me/push puts them back")
	s.p.refreshPushPeople()
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "back"), sceneLinked)
	require.Equal(t, "post-1", phone.next(t)["post"])

	lose()
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key))).State)
	require.True(t, inIndex(), "the same PUT puts them back")
	s.p.refreshPushPeople()
	s.p.NotificationWillBePushed(message("post-2", "chan-1", "back again"), sceneLinked)
	require.Equal(t, "post-2", phone.next(t)["post"])
}

// Every write of a person's row says when it was made.
func TestEveryWriteOfAPersonsRowSaysWhen(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	key := s.p.pushes().publicKey()

	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)))
	require.Equal(t, s.clock().UnixMilli(), s.doc(sceneLinked).Written)
	hello := phone.next(t)

	s.advance(time.Minute)
	s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": hello["nonce"]}))
	require.Equal(t, s.clock().UnixMilli(), s.doc(sceneLinked).Written)

	// A read that writes nothing leaves it as it was.
	written := s.doc(sceneLinked).Written
	s.advance(time.Minute)
	s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil))
	require.Equal(t, written, s.doc(sceneLinked).Written)

	s.advance(pushSeenEvery)
	s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil))
	require.Equal(t, s.clock().UnixMilli(), s.doc(sceneLinked).Written)
}

// After the server's key changes, the PUT with the new key answers pending,
// as GET does until its hello is confirmed; and while the server has no key
// at all, GET answers from what is stored rather than refresh.
func TestThePutAfterAKeyChangeAnswersPending(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, server, "dave"), sceneLinked, "sess-dave")

	r := s.p.pushes()
	r.mu.Lock()
	r.key = nil
	r.mu.Unlock()
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State, "no key is not another key")

	scalar := [32]byte{31: 7}
	other, err := webpush.ParseKey(scalar[:])
	require.NoError(t, err)
	r.setKey(other)
	require.Equal(t, pushStateRefresh, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)

	moved := newTestPhone(t, server, "dave-moved")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", moved.registration(other.Public()))).State)
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)

	hello := moved.next(t)
	require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": hello["nonce"]})).State)

	// A pending side with the old key, once the key changes again, is
	// registered afresh.
	again := newTestPhone(t, server, "dave-again")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", again.registration(other.Public()))).State)
	again.next(t)
	r.setKey(testPushKey(t))
	require.Equal(t, pushStateRefresh, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
}

// A store that fails on the push routes is answered 503 with the code
// `store`, never `unavailable`, which says push is off; the same PUT among
// them, when it could not read.
func TestAStoreFailureOnThePushRoutesIsCodedStore(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	hello := s.register(phone, sceneLinked, "sess-dave")
	key := s.p.pushes().publicKey()
	row := pushUserKey(sceneLinked)

	// The same PUT, when the row reads once and then no more.
	reads := 0
	s.faults.setOnRead(func(read string) {
		if read == row {
			reads++
			if reads > 1 {
				s.faults.readFails[row] = true
			}
		}
	})
	s.advance(pushSeenEvery)
	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)),
		http.StatusServiceUnavailable, accessCodeStore, "the same PUT")
	s.faults.setOnRead(nil)

	s.faults.failRead(row)
	moved := newTestPhone(t, server, "dave-moved")
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, mePushPath, phone.registration(key)},
		{http.MethodPut, mePushPath, moved.registration(key)},
		{http.MethodGet, mePushPath, nil},
		{http.MethodDelete, mePushPath, nil},
		{http.MethodPost, mePushConfirmPath, map[string]any{"nonce": hello["nonce"]}},
		{http.MethodPost, mePushTestPath, nil},
	} {
		recorder := s.call(call.method, call.path, sceneLinked, "sess-dave", call.body)
		requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, call.method+" "+call.path)
		require.Equal(t, pushStoreText, codedError(t, recorder).Error)
	}
	requireRefused(t, s.call(http.MethodGet, adminPushPath, sceneLockedAdmin, "", nil), http.StatusServiceUnavailable, accessCodeStore, "the card")
	s.faults.healRead(row)

	s.faults.failRead(kvPushPeople)
	requireRefused(t, s.call(http.MethodGet, adminPushPath, sceneLockedAdmin, "", nil), http.StatusServiceUnavailable, accessCodeStore, "the card's index")
}

// A push key that could not be loaded is tried again until there is one,
// and once there is one, trying again reads nothing.
func TestThePushKeyIsTriedAgainUntilThereIsOne(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	faults.failRead(kvPushKey)
	p.loadPushKey()
	require.Empty(t, p.pushes().publicKey())
	p.retryPushKey()
	require.Empty(t, p.pushes().publicKey())
	require.Empty(t, store[kvPushKey], "nothing made while the stored one could not be read")

	faults.healRead(kvPushKey)
	p.retryPushKey()
	public := p.pushes().publicKey()
	require.Len(t, public, webpush.PublicKeyLength)

	reads := 0
	faults.setOnRead(func(string) { reads++ })
	p.retryPushKey()
	require.Zero(t, reads)
	require.Equal(t, public, p.pushes().publicKey())
}

// Under the app check's `require`, taking your own registration back always
// works; registering and asking how it stands stay behind the check.
func TestTakingARegistrationBackPassesTheAppCheck(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	_, err := a.p.updatePushDoc(sceneLinked, func(doc *pushDoc, now time.Time) bool {
		doc.Devices = append(doc.Devices, pushDevice{
			Session: davesPhone, Endpoint: "https://push.example.org/up/x", Key: "k", Host: "push.example.org",
			State: pushStateConfirmed, Since: now.UnixMilli(), Created: now.UnixMilli(), Seen: now.UnixMilli(),
		})
		return true
	})
	require.NoError(t, err)

	for _, method := range []string{http.MethodPut, http.MethodGet} {
		refused := a.call(method, mePushPath, sceneLinked, davesPhone, mattyAgent, map[string]any{})
		require.Equal(t, http.StatusForbidden, refused.Code, method)
		require.Equal(t, codeAppUnverified, codedErrorOrEmpty(refused), method)
	}
	for _, path := range []string{mePushConfirmPath, mePushTestPath} {
		require.Equal(t, codeAppUnverified, codedErrorOrEmpty(a.call(http.MethodPost, path, sceneLinked, davesPhone, mattyAgent, map[string]any{})), path)
	}

	taken := a.call(http.MethodDelete, mePushPath, sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusOK, taken.Code, taken.Body.String())
	doc, err := a.p.readPushDoc(sceneLinked)
	require.NoError(t, err)
	require.Empty(t, doc.Devices)
}

// While push is switched on but this node has no push key yet, the routes
// that need it answer 503 coded `store` (worth sending again); `unavailable`
// is for push being off: switched off, or Mattermost pushing to nobody.
func TestThePushKeyNotReadyIsCodedStore(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	hello := s.register(phone, sceneLinked, "sess-dave")
	key := s.p.pushes().publicKey()

	calls := []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, mePushPath, phone.registration(key)},
		{http.MethodPost, mePushConfirmPath, map[string]any{"nonce": hello["nonce"]}},
		{http.MethodPost, mePushTestPath, nil},
	}

	r := s.p.pushes()
	r.mu.Lock()
	kept := r.key
	r.key = nil
	r.mu.Unlock()
	for _, call := range calls {
		recorder := s.call(call.method, call.path, sceneLinked, "sess-dave", call.body)
		requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, call.method+" "+call.path)
		require.Equal(t, pushNotReadyText, codedError(t, recorder).Error)
	}
	r.mu.Lock()
	r.key = kept
	r.mu.Unlock()

	s.lock.Lock()
	s.config = mattermostPushing(false, model.GenericNotificationServer)
	s.lock.Unlock()
	s.p.pushConfigured(s.p.getConfiguration())
	for _, call := range calls {
		recorder := s.call(call.method, call.path, sceneLinked, "sess-dave", call.body)
		requireRefused(t, recorder, http.StatusServiceUnavailable, codeUnavailable, call.method+" "+call.path)
		require.Equal(t, pushMattermostOffText, codedError(t, recorder).Error)
	}
}

// The same registration as the session's pending side, again under ten
// seconds after that side was made, is answered as it stands: the same
// nonce, no new hello, and not counted against the limit. Later, it makes a
// new hello.
func TestTheSamePutAtOnceMakesNoNewHello(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	key := s.p.pushes().publicKey()
	phone := newTestPhone(t, server, "dave")

	first := s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)))
	require.Equal(t, pushStatePending, first.State)
	hello := phone.next(t)
	nonce := s.device(sceneLinked, "sess-dave").Pending.Nonce
	for range 8 {
		s.advance(time.Second)
		again := s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)))
		require.Equal(t, first, again)
	}
	phone.nothing(t, 50*time.Millisecond)
	require.Equal(t, 1, server.requests(), "one hello")
	require.Equal(t, nonce, s.device(sceneLinked, "sess-dave").Pending.Nonce)

	// Ten seconds after the side was made, the same PUT is a new one.
	s.advance(2 * time.Second)
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key))).State)
	again := phone.next(t)
	require.NotEqual(t, hello["nonce"], again["nonce"])
	require.NotEqual(t, nonce, s.device(sceneLinked, "sess-dave").Pending.Nonce)

	// Four more new ones make six in ten minutes; the same as the last, at
	// once, still answers; a seventh new one does not.
	var last *testPhone
	for i := range 4 {
		last = newTestPhone(t, server, "dave-"+strconv.Itoa(i))
		require.Equal(t, http.StatusOK, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", last.registration(key)).Code)
		last.next(t)
	}
	require.Equal(t, http.StatusOK, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", last.registration(key)).Code)
	last.nothing(t, 50*time.Millisecond)
	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", newTestPhone(t, server, "dave-7").registration(key)),
		http.StatusTooManyRequests, codeRateLimited, "the seventh")

	// Another session's PUT of the same address is its own.
	s.session("sess-dave-2", sceneLinked)
	requireRefused(t, s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave-2", last.registration(key)),
		http.StatusTooManyRequests, codeRateLimited, "not this session's pending side")
}

// Under the app check's `require`, the gate's own store failure answers 503
// coded `store` on every route it guards, the push routes among them: worth
// asking again, never push being off.
func TestTheAppCheckGatesStoreFailureIsCodedStore(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	a.faults.failRead(kvAppCheckResultPrefix + davesPhone)

	for _, call := range []struct{ method, path string }{
		{http.MethodPut, mePushPath}, {http.MethodGet, mePushPath}, {http.MethodPost, mePushConfirmPath},
		{http.MethodPost, mePushTestPath}, {http.MethodGet, "/api/v1/streams"},
	} {
		recorder := a.call(call.method, call.path, sceneLinked, davesPhone, mattyAgent, map[string]any{})
		requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, call.method+" "+call.path)
		require.Equal(t, "This server could not read its app check records. Try again in a moment.", codedError(t, recorder).Error)
	}
}
