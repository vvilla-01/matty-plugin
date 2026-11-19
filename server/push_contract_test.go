package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// The contract's push fixtures (contract/README.md, "Push notifications"):
// every answer of the push routes, /access with push on, the refusals a
// Matty acts on, the admins' card, and one plaintext of each type, decrypted
// from what reached a push server. Written with the fixed push key; the push
// server's host is spelt push.example.net and a hello's nonce is spelt out.
func TestContractPush(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	key := s.p.pushes().publicKey()

	const host = "push.example.net"
	const nonce = "AAAAAAAAAAAAAAAAAAAAAA"
	fixture := func(name string, raw []byte, hello string) {
		t.Helper()
		raw = bytes.ReplaceAll(raw, []byte(server.host), []byte(host))
		if hello != "" {
			raw = bytes.ReplaceAll(raw, []byte(hello), []byte(nonce))
		}
		require.NotContains(t, string(raw), "/push/", name)
		contractFixture(t, name, raw)
	}
	answer := func(name string, recorder *httptest.ResponseRecorder, status int) {
		t.Helper()
		require.Equal(t, status, recorder.Code, recorder.Body.String())
		fixture(name, recorder.Body.Bytes(), "")
	}
	plaintext := func() []byte {
		t.Helper()
		push := phone.nextRaw(t)
		decrypted, err := webpush.Decrypt(push.body, phone.private, phone.auth)
		require.NoError(t, err)
		return decrypted
	}

	answer("access-push-on.json", s.call(http.MethodGet, "/api/v1/access", sceneLetIn, "sess-erin", nil), http.StatusOK)

	answer("push-pending.json", s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)), http.StatusOK)
	hello := plaintext()
	var helloPayload pushHelloPayload
	require.NoError(t, json.Unmarshal(hello, &helloPayload))
	fixture("push-payload-hello.json", hello, helloPayload.Nonce)

	stale := s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": nonce})
	answer("refusal-push-conflict.json", stale, http.StatusConflict)
	s.advance(time.Second)
	answer("push-confirmed.json", s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": helloPayload.Nonce}), http.StatusOK)

	full := message("post-1", "chan-1", "bob: lunch?")
	full.RootId, full.IsCRTEnabled = "root-1", true
	s.p.NotificationWillBePushed(full, sceneLinked)
	fixture("push-payload-message.json", plaintext(), "")

	s.p.NotificationWillBePushed(&model.PushNotification{
		Type: model.PushTypeMessage, PostId: "post-2", ChannelId: "chan-1", TeamId: "team-1", SenderId: sceneLinked,
		IsIdLoaded: true, Message: "You've received a new message.",
	}, sceneLinked)
	fixture("push-payload-message-id-loaded.json", plaintext(), "")

	s.p.NotificationWillBePushed(&model.PushNotification{Type: model.PushTypeClear, ChannelId: "chan-1", RootId: "root-1", IsCRTEnabled: true}, sceneLinked)
	fixture("push-payload-clear.json", plaintext(), "")

	answer("push-test.json", s.call(http.MethodPost, mePushTestPath, sceneLinked, "sess-dave", nil), http.StatusAccepted)
	fixture("push-payload-test.json", plaintext(), "")

	answer("push-state-confirmed.json", s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil), http.StatusOK)

	// How the other states read.
	setDevice := func(edit func(device *pushDevice)) {
		_, err := s.p.updatePushDoc(sceneLinked, func(doc *pushDoc, _ time.Time) bool {
			edit(&doc.Devices[doc.find("sess-dave")])
			return true
		})
		require.NoError(t, err)
	}
	since := s.clock().UnixMilli()
	setDevice(func(device *pushDevice) {
		device.State, device.Status, device.Since = pushStateRefused, http.StatusForbidden, since
	})
	answer("push-state-refused.json", s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil), http.StatusOK)
	setDevice(func(device *pushDevice) {
		device.State, device.Status, device.Kind = pushStateFailing, 0, "unreachable"
	})
	answer("push-state-failing.json", s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil), http.StatusOK)
	setDevice(func(device *pushDevice) { device.Key = testPushKeyOther(t) })
	answer("push-state-refresh.json", s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil), http.StatusOK)
	setDevice(func(device *pushDevice) { device.State, device.Kind, device.Key = pushStateConfirmed, "", key })

	// The admins' card.
	answer("admin-push.json", s.call(http.MethodGet, adminPushPath, sceneLockedAdmin, "", nil), http.StatusOK)

	// A refusal for another key, and the ended push.
	moved := newTestPhone(t, server, "dave-moved")
	wrongKey := moved.registration(testPushKeyOther(t))
	answer("refusal-push-key.json", s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", wrongKey), http.StatusBadRequest)
	s.endSession("sess-dave")
	s.p.NotificationWillBePushed(message("post-3", "chan-1", "after"), sceneLinked)
	fixture("push-payload-ended.json", plaintext(), "")
	answer("push-state-none.json", s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil), http.StatusOK)

	// Push switched off.
	cfg := s.p.getConfiguration().Clone()
	cfg.EnablePush = false
	s.p.setConfiguration(cfg)
	answer("refusal-push-off.json", s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(key)), http.StatusServiceUnavailable)
}

// testPushKeyOther is another push key's public half: a key this server
// no longer has.
func testPushKeyOther(t *testing.T) string {
	t.Helper()

	scalar := [32]byte{31: 7}
	key, err := webpush.ParseKey(scalar[:])
	require.NoError(t, err)

	return key.Public()
}
