package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// pushReady is a scene with push on, sending, and Dave's phone registered
// and confirmed for his session.
func pushReady(t *testing.T) (*pushScene, *fakePushServer, *testPhone) {
	t.Helper()

	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	s.register(phone, sceneLinked, "sess-dave")

	return s, server, phone
}

// What Mattermost would push goes to the phone, encrypted to it, as the
// contract spells it, with the headers a push server needs; the hook changes
// nothing of Mattermost's own push.
func TestTheHookSendsWhatMattermostWouldPush(t *testing.T) {
	s, _, phone := pushReady(t)

	n := message("post-1", "chan-1", "bob: lunch?")
	n.RootId, n.IsCRTEnabled, n.FromWebhook = "root-1", true, "true"
	replacement, rejection := s.p.NotificationWillBePushed(n, sceneLinked)
	require.Nil(t, replacement)
	require.Empty(t, rejection)

	raw := phone.nextRaw(t)
	require.Equal(t, "aes128gcm", raw.header.Get("Content-Encoding"))
	require.Equal(t, "application/octet-stream", raw.header.Get("Content-Type"))
	require.Equal(t, "86400", raw.header.Get("TTL"))
	require.Equal(t, "high", raw.header.Get("Urgency"))
	require.Equal(t, "MattyPlugin", raw.header.Get("User-Agent"))
	require.Empty(t, raw.header.Get("Topic"))
	require.True(t, strings.HasPrefix(raw.header.Get("Authorization"), "vapid t="))
	require.True(t, strings.HasSuffix(raw.header.Get("Authorization"), ", k="+testPushKey(t).Public()))
	require.LessOrEqual(t, len(raw.body), webpush.MaxBody)

	plaintext, err := webpush.Decrypt(raw.body, phone.private, phone.auth)
	require.NoError(t, err)
	require.JSONEq(t, `{"v":1,"type":"message","user":"`+sceneLinked+`","post":"post-1","channel":"chan-1","root":"root-1","team":"team-1",
		"sender":"uid-bob","sender_name":"bob","channel_name":"Town Square","channel_type":"O","text":"bob: lunch?","crt":true,
		"id_loaded":false,"from_webhook":true,"at":`+pushNumber(s.clock().UnixMilli())+`}`, string(plaintext))

	// In id-loaded mode Mattermost's sender is the recipient: left out.
	idLoaded := &model.PushNotification{Type: model.PushTypeMessage, PostId: "post-2", ChannelId: "chan-1", SenderId: sceneLinked, IsIdLoaded: true, Message: "You've received a new message."}
	s.p.NotificationWillBePushed(idLoaded, sceneLinked)
	payload := phone.next(t)
	require.Equal(t, "", payload["sender"])
	require.Equal(t, true, payload["id_loaded"])

	// A clear.
	s.p.NotificationWillBePushed(&model.PushNotification{Type: model.PushTypeClear, ChannelId: "chan-1", RootId: "root-1", IsCRTEnabled: true}, sceneLinked)
	require.Equal(t, map[string]any{"v": 1.0, "type": "clear", "user": sceneLinked, "channel": "chan-1", "root": "root-1", "crt": true, "at": float64(s.clock().UnixMilli())}, phone.next(t))

	// Not sent: another plugin's push, a badge, somebody with no device,
	// anything while the switch is off.
	calls := message("post-3", "chan-1", "ring")
	calls.SubType = model.PushSubTypeCalls
	s.p.NotificationWillBePushed(calls, sceneLinked)
	s.p.NotificationWillBePushed(&model.PushNotification{Type: model.PushTypeUpdateBadge, Badge: 3}, sceneLinked)
	require.Equal(t, int64(1), s.p.pushes().droppedNow()["other plugin"])

	cfg := s.p.getConfiguration().Clone()
	cfg.EnablePush = false
	s.p.setConfiguration(cfg)
	s.p.NotificationWillBePushed(message("post-5", "chan-1", "off"), sceneLinked)
	phone.nothing(t, 80*time.Millisecond)
}

func pushNumber(n int64) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

// The hook offers pushes only for people in this node's set, once it has
// read the index; while it cannot be trusted (unread or full), for
// everybody. It reads nothing.
func TestTheHookOffersOnlyForPeopleWithADevice(t *testing.T) {
	s, _, _ := pushReady(t)
	r := s.p.pushes()

	require.True(t, r.offered(sceneLinked))
	require.False(t, r.offered(sceneLetIn))

	s.resetReads()
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "not for Erin"), sceneLetIn)
	require.Zero(t, s.totalReads(), "the hook reads nothing")
	r.mu.Lock()
	require.Empty(t, r.intake)
	r.peopleFull = true
	r.mu.Unlock()
	require.True(t, r.offered(sceneLetIn), "a full index is not trusted")

	r.mu.Lock()
	r.peopleFull, r.peopleKnown = false, false
	r.mu.Unlock()
	require.True(t, r.offered(sceneLetIn), "nor an index never read")

	// Another node confirms Erin's phone: this one hears of it within its
	// next read of the index.
	require.NoError(t, s.p.changePushPeople(func(doc *pushPeopleDoc) bool { doc.People[sceneLetIn] = 1; return true }))
	s.p.refreshPushPeople()
	require.True(t, r.offered(sceneLetIn))
	require.False(t, r.offered(sceneLocked))
}

// Text too long for one push is cut on a character boundary, with "…".
func TestALongMessageIsCutToFit(t *testing.T) {
	n := message("post-1", "chan-1", strings.Repeat("ありがとう <&> ", 600))
	plaintext := messagePayload(n, sceneLinked, time.UnixMilli(1))
	require.LessOrEqual(t, len(plaintext), webpush.MaxPlaintext)
	require.Greater(t, len(plaintext), webpush.MaxPlaintext-16, "as much as fits")

	var payload pushMessagePayload
	require.NoError(t, json.Unmarshal(plaintext, &payload))
	require.True(t, utf8.ValidString(payload.Text))
	require.True(t, strings.HasSuffix(payload.Text, "…"))
	require.True(t, strings.HasPrefix(n.Message, strings.TrimSuffix(payload.Text, "…")))

	short := messagePayload(message("post-2", "chan-1", "hi"), sceneLinked, time.UnixMilli(1))
	require.Contains(t, string(short), `"text":"hi"`)
}

// A clear takes out the messages for the same conversation still waiting,
// in the hook's queue and in each device's lane, and nothing else.
func TestAClearTakesOutWaitingMessages(t *testing.T) {
	r := newPushRuntime()
	r.running = true

	waiting := func(kind, conv string) *pushJob {
		return &pushJob{kind: kind, user: sceneLinked, session: "sess-dave", conv: conv}
	}
	r.enqueue(waiting(pushKindMessage, "chan-1|"), false)
	r.enqueue(waiting(pushKindMessage, "chan-2|"), false)
	r.enqueue(waiting(pushKindMessage, "chan-1|root-1"), false)
	r.enqueue(waiting(pushKindTest, "chan-1|"), false)
	r.enqueue(waiting(pushKindClear, "chan-1|"), false)

	var left []string
	for _, job := range r.lanes["sess-dave"].jobs {
		left = append(left, job.kind+" "+job.conv)
	}
	require.Equal(t, []string{"message chan-2|", "message chan-1|root-1", "test chan-1|", "clear chan-1|"}, left)

	// A lane keeps 32 jobs, dropping the oldest.
	for range pushLaneKept + 3 {
		r.enqueue(waiting(pushKindMessage, "chan-3|"), false)
	}
	require.Len(t, r.lanes["sess-dave"].jobs, pushLaneKept)
	require.Equal(t, int64(7), r.dropped["queue full"])

	// The hook's own queue.
	p, _ := kvBackedPlugin()
	cfg := &configuration{EnablePush: true}
	p.setConfiguration(cfg)
	q := p.pushes()
	q.mu.Lock()
	q.running, q.peopleKnown, q.people = true, true, map[string]bool{sceneLinked: true}
	q.mu.Unlock()
	p.offerPush(message("post-1", "chan-1", "one"), sceneLinked)
	p.offerPush(message("post-2", "chan-2", "two"), sceneLinked)
	p.offerPush(&model.PushNotification{Type: model.PushTypeClear, ChannelId: "chan-1"}, sceneLinked)
	q.mu.Lock()
	defer q.mu.Unlock()
	require.Len(t, q.intake, 2)
	require.Equal(t, "chan-2|", q.intake[0].conv)
	require.Equal(t, pushKindClear, q.intake[1].kind)
}

// What each answer of a push server leads to (design 1.5).
func TestWhatAPushServersAnswerLeadsTo(t *testing.T) {
	t.Run("404 and 410 drop the device, with no ended push", func(t *testing.T) {
		for _, status := range []int{http.StatusNotFound, http.StatusGone} {
			s, server, phone := pushReady(t)
			server.answer(phone.id, fakeAnswer{status: status})
			s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
			eventually(t, "the device is dropped", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
			phone.nothing(t, 50*time.Millisecond)
		}
	})

	t.Run("401 and 403 are refused, with the status, and not tried again", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			s, server, phone := pushReady(t)
			server.answer(phone.id, fakeAnswer{status: status})
			s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
			eventually(t, "refused", func() bool {
				device := s.device(sceneLinked, "sess-dave")
				return device != nil && device.State == pushStateRefused
			})
			view := s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil))
			require.Equal(t, pushStateView{State: pushStateRefused, Host: phone.server.host, Status: status, Since: s.clock().UnixMilli()}, view)
			require.Equal(t, 2, server.requests(), "the hello, and the one refused")

			s.p.NotificationWillBePushed(message("post-2", "chan-1", "y"), sceneLinked)
			phone.nothing(t, 50*time.Millisecond)
			require.Equal(t, 2, server.requests(), "nothing more goes to a refused device")
		}
	})

	t.Run("429, 5xx and 507 are tried again, six more times at most", func(t *testing.T) {
		s, server, phone := pushReady(t)
		server.answer(phone.id, fakeAnswer{status: 500}, fakeAnswer{status: 507}, fakeAnswer{status: http.StatusTooManyRequests})
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		require.Equal(t, "post-1", phone.next(t)["post"], "through on the fourth try")
		require.Equal(t, 5, server.requests())
		require.Zero(t, s.device(sceneLinked, "sess-dave").Trouble, "a push that went through is no failure")

		for range 1 + len(pushMessageRetryWaits) {
			server.answer(phone.id, fakeAnswer{status: 503})
		}
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "y"), sceneLinked)
		eventually(t, "a failed send is written", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
		require.Equal(t, 12, server.requests(), "one try and six more")
		device := s.device(sceneLinked, "sess-dave")
		require.Equal(t, pushStateConfirmed, device.State)
		require.Equal(t, "answer", device.Kind)

		// The next one that goes through clears it.
		s.p.NotificationWillBePushed(message("post-3", "chan-1", "z"), sceneLinked)
		require.Equal(t, "post-3", phone.next(t)["post"])
		eventually(t, "the trouble is over", func() bool { return s.device(sceneLinked, "sess-dave").Trouble == 0 })
	})

	t.Run("a message job more than five minutes old is not tried again", func(t *testing.T) {
		s, server, phone := pushReady(t)
		s.p.pushes().mu.Lock()
		s.p.pushes().messageWaits = []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}
		s.p.pushes().mu.Unlock()
		server.answer(phone.id, fakeAnswer{status: 500}, fakeAnswer{status: 500})
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		r := s.p.pushes()
		eventually(t, "waiting to try again", func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.parked == 1
		})
		s.advance(pushMessageRetryAge + time.Second)
		eventually(t, "the second try", func() bool { return server.requests() == 3 })
		eventually(t, "written as failed", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
		phone.nothing(t, 150*time.Millisecond)
		require.Equal(t, 3, server.requests())
	})

	t.Run("another 4xx and a redirect are permanent; ten in a row suspend the device", func(t *testing.T) {
		s, server, phone := pushReady(t)
		for range pushFailuresToSuspend - 1 {
			server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest})
		}
		server.answer(phone.id, fakeAnswer{status: http.StatusFound})
		for i := range pushFailuresToSuspend {
			before := server.requests()
			s.p.NotificationWillBePushed(message("post-"+string(rune('a'+i)), "chan-"+string(rune('a'+i)), "x"), sceneLinked)
			eventually(t, "sent once", func() bool { return server.requests() == before+1 })
		}
		eventually(t, "suspended", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateFailing })
		require.Equal(t, 1+pushFailuresToSuspend, server.requests(), "permanent: never tried again")
		view := s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil))
		require.Equal(t, pushStateFailing, view.State)
		require.Equal(t, "answer", view.Kind)

		s.p.NotificationWillBePushed(message("post-z", "chan-z", "x"), sceneLinked)
		phone.nothing(t, 50*time.Millisecond)

		// Registering again (the app's PUT) puts it back to work.
		s.register(phone, sceneLinked, "sess-dave")
		s.p.NotificationWillBePushed(message("post-y", "chan-y", "back"), sceneLinked)
		require.Equal(t, "post-y", phone.next(t)["post"])
	})

	t.Run("an hour of nothing but failures suspends it, once there are three", func(t *testing.T) {
		s, server, phone := pushReady(t)
		server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest}, fakeAnswer{status: http.StatusBadRequest}, fakeAnswer{status: http.StatusBadRequest})
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "the first failure", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })

		// Two failures an hour apart do not suspend it.
		s.advance(pushFailingToSuspend)
		s.p.NotificationWillBePushed(message("post-2", "chan-2", "y"), sceneLinked)
		eventually(t, "the second failure", func() bool {
			r := s.p.pushes()
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.failRun["sess-dave"] == 2
		})
		require.Equal(t, pushStateConfirmed, s.device(sceneLinked, "sess-dave").State)

		// The third does.
		s.p.NotificationWillBePushed(message("post-3", "chan-3", "z"), sceneLinked)
		eventually(t, "suspended", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateFailing })
	})

	t.Run("Retry-After rests the phone it came for, not the push server", func(t *testing.T) {
		s, server, phone := pushReady(t)
		r := s.p.pushes()
		s.session("sess-dave-2", sceneLinked)
		other := newTestPhone(t, server, "dave-2")
		s.register(other, sceneLinked, "sess-dave-2")
		s.session("sess-bob", sceneLinkedAdmin)
		bob := newTestPhone(t, server, "bob")
		s.register(bob, sceneLinkedAdmin, "sess-bob")
		laneRest := func(key string) (time.Time, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			until, known := r.laneRest[key]
			return until, known
		}

		// Dave's first phone is answered 429 with Retry-After 30; his other
		// phone gets the same push at once.
		server.answer(phone.id, fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "30"})
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		require.Equal(t, "post-1", other.next(t)["post"])
		eventually(t, "the phone's lane rests", func() bool {
			until, _ := laneRest("sess-dave")
			return until.Equal(s.clock().Add(30 * time.Second))
		})

		// Bob's push to the same push server does not wait, nor does Dave's
		// next push to his other phone; his first phone's waits.
		s.p.NotificationWillBePushed(message("post-2", "chan-2", "y"), sceneLinkedAdmin)
		require.Equal(t, "post-2", bob.next(t)["post"])
		s.p.NotificationWillBePushed(message("post-3", "chan-3", "z"), sceneLinked)
		require.Equal(t, "post-3", other.next(t)["post"])
		phone.nothing(t, 100*time.Millisecond)
		func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			require.Empty(t, r.hostRest, "one person's Retry-After rests no push server")
			lane := r.lanes["sess-dave"]
			require.True(t, lane != nil && !lane.busy && len(lane.jobs) == 2, "both of Dave's pushes wait in that phone's lane")
		}()

		s.advance(30 * time.Second)
		r.pump(s.p)
		require.Equal(t, "post-1", phone.next(t)["post"])
		require.Equal(t, "post-3", phone.next(t)["post"])
		_, known := laneRest("sess-dave")
		require.False(t, known, "let go of once over")
	})
}

// A Retry-After rests the whole push server only for a run of them from two
// people with nothing delivered there in between, until the later of the
// two; a hello goes all the while; the rests are let go of once over.
func TestARetryAfterRestsAPushServerOnlyForTwoPeople(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.session("sess-erin", sceneLetIn)
	dave, bob, erin := newTestPhone(t, server, "dave"), newTestPhone(t, server, "bob"), newTestPhone(t, server, "erin")
	s.register(dave, sceneLinked, "sess-dave")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	s.register(erin, sceneLetIn, "sess-erin")
	resting := func(lane string) bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.laneRest[lane].After(s.clock())
	}
	hostRest := func() (time.Time, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		until, known := r.hostRest[server.host]
		return until, known
	}

	// Dave's push is answered with a Retry-After, then Erin's is delivered,
	// then Bob's is answered with one: the delivery ended the run.
	server.answer(dave.id, fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "30"})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "Dave's lane rests", func() bool { return resting("sess-dave") })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLetIn)
	require.Equal(t, "post-2", erin.next(t)["post"])
	server.answer(bob.id, fakeAnswer{status: http.StatusServiceUnavailable, retryAfter: "20"})
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinkedAdmin)
	eventually(t, "Bob's lane rests", func() bool { return resting("sess-bob") })
	_, rests := hostRest()
	require.False(t, rests, "a delivery in between: the push server does not rest")

	// Erin's is answered with one while Bob's rest runs, with nothing
	// delivered since: the push server rests until the later of the two.
	server.answer(erin.id, fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "40"})
	s.p.NotificationWillBePushed(message("post-4", "chan-4", "x"), sceneLetIn)
	eventually(t, "the push server rests", func() bool {
		until, _ := hostRest()
		return until.Equal(s.clock().Add(40 * time.Second))
	})

	// A hello goes while its device's lane rests and its push server rests.
	moved := newTestPhone(t, server, "dave-moved")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", moved.registration(r.publicKey()))).State)
	require.Equal(t, pushKindHello, moved.next(t)["type"])
	dave.nothing(t, 50*time.Millisecond)
	bob.nothing(t, 10*time.Millisecond)
	erin.nothing(t, 10*time.Millisecond)

	// Once over, everything waiting goes, and the rests are let go of.
	s.advance(40 * time.Second)
	r.pump(s.p)
	require.Equal(t, "post-1", dave.next(t)["post"])
	require.Equal(t, "post-3", bob.next(t)["post"])
	require.Equal(t, "post-4", erin.next(t)["post"])
	eventually(t, "nothing is sending", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.busy == 0 && r.queued == 0 && r.parked == 0
	})
	r.trim()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.laneRest)
	require.Empty(t, r.hostRest)
	require.Empty(t, r.retryRun)
}

// What counts toward a host's run of Retry-After answers: another person's
// rest that still runs. One person's own, however many, never rests the
// host; a rest that is over counts for nothing; of three people, the two
// rests that end last are kept.
func TestWhatCountsTowardARetryAfterRun(t *testing.T) {
	r := newPushRuntime()
	now := time.UnixMilli(10_000_000)
	at := func(seconds int) time.Time { return now.Add(time.Duration(seconds) * time.Second) }

	_, rests := r.retryBreaker("h", "uid-a", at(30), now)
	require.False(t, rests)
	_, rests = r.retryBreaker("h", "uid-a", at(40), now)
	require.False(t, rests, "one person's own")
	until, rests := r.retryBreaker("h", "uid-b", at(20), now)
	require.True(t, rests)
	require.Equal(t, at(40), until, "the later of the two")

	r.retryRun = map[string]pushRetryRun{}
	r.retryBreaker("h", "uid-a", at(10), now)
	_, rests = r.retryBreaker("h", "uid-b", at(30), at(10))
	require.False(t, rests, "the other rest is over")

	r.retryRun = map[string]pushRetryRun{}
	r.retryBreaker("h", "uid-a", at(50), now)
	r.retryBreaker("h", "uid-b", at(40), now)
	r.retryBreaker("h", "uid-c", at(30), now)
	until, rests = r.retryBreaker("h", "uid-a", at(5), now)
	require.True(t, rests)
	require.Equal(t, at(40), until, "the other person's rest, the later of the two")
	require.Equal(t, [2]pushPersonRest{{"uid-a", at(50)}, {"uid-b", at(40)}}, r.retryRun["h"].rests)
	until, rests = r.retryBreaker("h", "uid-c", at(45), now)
	require.True(t, rests)
	require.Equal(t, at(50), until)
	require.Equal(t, [2]pushPersonRest{{"uid-a", at(50)}, {"uid-c", at(45)}}, r.retryRun["h"].rests)
}

// Two people whose phones go on being answered with a Retry-After (each at a
// limit of its own, as with ntfy's limits by subscriber) rest the push
// server once, when the second one's first such answer comes while the
// first one's rest runs. Their next ones rest only their own lanes, so
// everybody else's pushes to that push server go on; a delivery to one of
// them makes its next Retry-After count again.
func TestPhonesAtTheirOwnLimitDoNotKeepRestingAPushServer(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.session("sess-erin", sceneLetIn)
	dave, bob, erin := newTestPhone(t, server, "dave"), newTestPhone(t, server, "bob"), newTestPhone(t, server, "erin")
	s.register(dave, sceneLinked, "sess-dave")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	s.register(erin, sceneLetIn, "sess-erin")
	r.mu.Lock()
	r.messageWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	r.mu.Unlock()
	laneRest := func(lane string) time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.laneRest[lane]
	}
	hostRests := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRest[server.host].After(s.clock())
	}
	limit := fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "60"}

	// The first answers of both: the push server rests, once.
	server.answer(dave.id, limit, limit, limit)
	server.answer(bob.id, limit, limit, limit)
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "Dave's lane rests", func() bool { return laneRest("sess-dave").Equal(s.clock().Add(time.Minute)) })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinkedAdmin)
	eventually(t, "the push server rests", hostRests)

	// A minute on both are tried again and answered the same: only their
	// own lanes rest now, and Erin's push goes at once.
	s.advance(time.Minute)
	r.pump(s.p)
	eventually(t, "both lanes rest again", func() bool {
		until := s.clock().Add(time.Minute)
		return laneRest("sess-dave").Equal(until) && laneRest("sess-bob").Equal(until)
	})
	require.False(t, hostRests(), "no new rest of the push server")
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLetIn)
	require.Equal(t, "post-3", erin.next(t)["post"])

	// And again a minute on, whatever was delivered to somebody else.
	s.advance(time.Minute)
	r.pump(s.p)
	eventually(t, "both lanes rest a third time", func() bool {
		until := s.clock().Add(time.Minute)
		return laneRest("sess-dave").Equal(until) && laneRest("sess-bob").Equal(until)
	})
	require.False(t, hostRests())

	// Once a push to each of them was delivered, their next Retry-After
	// answers count again, and together rest the push server.
	s.advance(time.Minute)
	r.pump(s.p)
	require.Equal(t, "post-1", dave.next(t)["post"])
	require.Equal(t, "post-2", bob.next(t)["post"])
	server.answer(dave.id, limit)
	server.answer(bob.id, limit)
	s.p.NotificationWillBePushed(message("post-4", "chan-4", "x"), sceneLinkedAdmin)
	eventually(t, "Bob's lane rests", func() bool { return laneRest("sess-bob").Equal(s.clock().Add(time.Minute)) })
	require.False(t, hostRests(), "one person's")
	s.p.NotificationWillBePushed(message("post-5", "chan-5", "x"), sceneLinked)
	eventually(t, "the push server rests again", hostRests)
}

// A hello answered with a Retry-After rests nothing and counts for nothing
// toward its push server's run: nothing in a hello's lane waits for a rest.
func TestAHellosRetryAfterRestsNothing(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	bob := newTestPhone(t, server, "bob")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	r.mu.Lock()
	r.retryWaits = []time.Duration{time.Hour}
	r.messageWaits = []time.Duration{time.Hour}
	r.mu.Unlock()

	dave := newTestPhone(t, server, "dave")
	server.answer(dave.id, fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "30"})
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", dave.registration(r.publicKey()))).State)
	eventually(t, "the hello waits to be tried again", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.parked == 1 && r.busy == 0
	})
	server.answer(bob.id, fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "30"})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinkedAdmin)
	eventually(t, "Bob's lane rests", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.laneRest["sess-bob"].After(s.clock())
	})

	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.hostRest, "the hello's Retry-After is nobody's rest")
	require.Len(t, r.laneRest, 1)
	require.Len(t, r.limited, 1)
	require.Contains(t, r.limited, "sess-bob")
}

// Of the jobs waiting to be tried again, one person's are at most
// pushParkedPerPerson, so that they leave room for everybody else's; and a
// hello waits whatever waits already.
func TestOnePersonsRetriesLeaveRoomForOthers(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	s.configure(nil)
	s.start()
	wait := time.Hour
	job := func(kind, user string) *pushJob {
		return &pushJob{kind: kind, user: user, session: "sess-" + user, target: pushTarget{host: "push.example", pending: kind == pushKindHello}}
	}
	tries := 0

	for range pushParkedPerPerson {
		require.True(t, s.p.park(job(pushKindMessage, "uid-a"), wait, &tries))
	}
	require.False(t, s.p.park(job(pushKindMessage, "uid-a"), wait, &tries), "one person's share")
	require.False(t, s.p.park(job(pushKindClear, "uid-a"), wait, &tries))
	require.True(t, s.p.park(job(pushKindMessage, "uid-b"), wait, &tries), "somebody else's still has room")
	require.True(t, s.p.park(job(pushKindHello, "uid-a"), wait, &tries), "a hello always waits")
	require.EqualValues(t, 2, r.droppedNow()["too many waiting to try again"])

	r.mu.Lock()
	r.parked = pushParkedKept
	r.mu.Unlock()
	require.False(t, s.p.park(job(pushKindMessage, "uid-c"), wait, &tries), "the node's share")
	require.True(t, s.p.park(job(pushKindHello, "uid-c"), wait, &tries), "a hello always waits")

	r.mu.Lock()
	defer r.mu.Unlock()
	require.Equal(t, map[string]int{"uid-a": pushParkedPerPerson, "uid-b": 1}, r.parkedBy, "hellos are nobody's share")
}

// A hello that cannot be delivered says so on the pending side, which GET
// /me/push reports; one to an address the push server says is no more reads
// as failing, with the status, and leaves a confirmed main side as it was.
func TestAHelloThatDoesNotArrive(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	key := s.p.pushes().publicKey()

	refused := newTestPhone(t, server, "refused")
	server.answer(refused.id, fakeAnswer{status: http.StatusForbidden})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", refused.registration(key)))
	eventually(t, "refused", func() bool {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State == pushStateRefused
	})
	require.Equal(t, http.StatusForbidden, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).Status)

	// A first registration whose hello is answered 410.
	gone := newTestPhone(t, server, "gone")
	server.answer(gone.id, fakeAnswer{status: http.StatusGone})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", gone.registration(key)))
	eventually(t, "failing", func() bool {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State == pushStateFailing
	})
	require.Equal(t, pushStateView{State: pushStateFailing, Host: server.host, Kind: "answer", Since: s.clock().UnixMilli()},
		s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)))
	device := s.device(sceneLinked, "sess-dave")
	require.Equal(t, http.StatusGone, device.Pending.Status)
	require.Equal(t, pushStatePending, device.State, "no main side yet")
	s.api.AssertCalled(t, "LogInfo", "A phone's first push could not be delivered", "host", server.host, "kind", "answer", "status", http.StatusGone, "reason", "status")

	// A confirmed phone whose next address is answered 404 keeps its
	// main side, and its pushes go there.
	confirmed := newTestPhone(t, server, "confirmed")
	s.register(confirmed, sceneLinked, "sess-dave")
	missing := newTestPhone(t, server, "missing")
	server.answer(missing.id, fakeAnswer{status: http.StatusNotFound})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", missing.registration(key)))
	eventually(t, "failing", func() bool {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State == pushStateFailing
	})
	device = s.device(sceneLinked, "sess-dave")
	require.Equal(t, http.StatusNotFound, device.Pending.Status)
	require.Equal(t, pushStateConfirmed, device.State)
	require.Equal(t, confirmed.endpoint, device.Endpoint)
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	require.Equal(t, "post-1", confirmed.next(t)["post"])
}

// A session that is over drops its device at once, with one last ended
// push; one that cannot be read skips the push and drops the device only
// when it still cannot be read a minute on while another session reads; a
// store that is down deletes nothing.
func TestADeviceGoesWithItsSession(t *testing.T) {
	t.Run("over", func(t *testing.T) {
		s, _, phone := pushReady(t)
		s.endSession("sess-dave")
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		ended := phone.next(t)
		require.Equal(t, map[string]any{"v": 1.0, "type": "ended", "user": sceneLinked, "at": float64(s.clock().UnixMilli())}, ended)
		require.Nil(t, s.device(sceneLinked, "sess-dave"))
		phone.nothing(t, 50*time.Millisecond)
	})

	t.Run("somebody else's", func(t *testing.T) {
		s, _, phone := pushReady(t)
		s.session("sess-dave", sceneLetIn)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		require.Equal(t, "ended", phone.next(t)["type"])
		require.Nil(t, s.device(sceneLinked, "sess-dave"))
	})

	t.Run("unreadable", func(t *testing.T) {
		s, _, phone := pushReady(t)
		aliveElsewhere(s)
		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		phone.nothing(t, 50*time.Millisecond)

		// Readable again: the next push goes, and the mark is cleared.
		s.failSession("sess-dave", false)
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "y"), sceneLinked)
		require.Equal(t, "post-2", phone.next(t)["post"])
		eventually(t, "cleared", func() bool { return s.device(sceneLinked, "sess-dave").Suspect == 0 })

		// Unreadable twice, a minute apart, with the store up: dropped.
		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-3", "chan-1", "z"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		s.advance(30 * time.Second)
		s.p.NotificationWillBePushed(message("post-4", "chan-1", "z"), sceneLinked)
		phone.nothing(t, 50*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"), "not yet a minute")

		// A store that is down meanwhile deletes nothing.
		s.advance(30 * time.Second)
		s.faults.failWrite(pushUserKey(sceneLinked))
		s.p.NotificationWillBePushed(message("post-5", "chan-1", "z"), sceneLinked)
		phone.nothing(t, 50*time.Millisecond)
		s.faults.healWrite(pushUserKey(sceneLinked))
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))

		s.p.NotificationWillBePushed(message("post-6", "chan-1", "z"), sceneLinked)
		require.Equal(t, "ended", phone.next(t)["type"])
		eventually(t, "dropped", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
	})
}

// The daily sweep drops the devices of sessions that are over, with their
// ended push, clears registrations never confirmed, and takes people with
// nothing left off the index.
func TestTheSweepDropsWhatIsOver(t *testing.T) {
	s, server, dave := pushReady(t)
	key := s.p.pushes().publicKey()
	s.session("sess-erin", sceneLetIn)
	erin := newTestPhone(t, server, "erin")
	s.register(erin, sceneLetIn, "sess-erin")
	s.session("sess-bob", sceneLinkedAdmin)
	bob := newTestPhone(t, server, "bob")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	s.view(s.call(http.MethodPut, mePushPath, sceneLinkedAdmin, "sess-bob", newTestPhone(t, server, "bob-new").registration(key)))

	s.endSession("sess-erin")
	s.advance(pushPendingLife)
	s.p.sweepPushes(context.Background())

	require.Equal(t, "ended", erin.next(t)["type"])
	require.Nil(t, s.device(sceneLetIn, "sess-erin"))
	people, _, err := s.p.readPushPeople()
	require.NoError(t, err)
	require.NotContains(t, people.People, sceneLetIn)
	require.NotNil(t, s.device(sceneLinked, "sess-dave"))
	require.Nil(t, s.device(sceneLinkedAdmin, "sess-bob").Pending, "the leftover registration is cleared")
	dave.nothing(t, 30*time.Millisecond)
	bob.nothing(t, 30*time.Millisecond)
}

// A push server that does not answer holds up nobody else's pushes; three
// slow sends in a row with no answer, for two people, rest that host for a
// minute; and stopping does not wait for it.
func TestATarpitDelaysNobodyElse(t *testing.T) {
	s := newPushScene(t)
	slow, quick := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.session("sess-erin", sceneLetIn)
	dave := newTestPhone(t, slow, "dave")
	bob := newTestPhone(t, slow, "bob")
	erin := newTestPhone(t, quick, "erin")
	s.register(dave, sceneLinked, "sess-dave")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	s.register(erin, sceneLetIn, "sess-erin")

	setPushClient(s, 150*time.Millisecond)
	setSlowAfter(s, 50*time.Millisecond)
	slow.hold()

	for i := range 6 {
		s.p.NotificationWillBePushed(message("post-d"+string(rune('a'+i)), "chan-"+string(rune('a'+i)), "x"), sceneLinked)
	}
	s.p.NotificationWillBePushed(message("post-b", "chan-b", "x"), sceneLinkedAdmin)
	started := time.Now()
	s.p.NotificationWillBePushed(message("post-e", "chan-1", "y"), sceneLetIn)
	require.Equal(t, "post-e", erin.next(t)["post"])
	require.Less(t, time.Since(started), time.Second, "Erin's push did not wait for Dave's push server")

	// The push server Dave and Bob share never answers: after three in a
	// row it rests.
	eventually(t, "the host rests", func() bool {
		r := s.p.pushes()
		r.mu.Lock()
		defer r.mu.Unlock()
		until, resting := r.hostRest[slow.host]
		return resting && until.After(s.clock())
	})

	setPushClient(s, 10*time.Second)
	s.p.NotificationWillBePushed(message("post-d9", "chan-9", "x"), sceneLinked)
	s.advance(pushBreakerRest)
	s.p.pushes().pump(s.p)
	eventually(t, "a push is held by the push server", func() bool {
		r := s.p.pushes()
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostBusy[slow.host] > 0
	})

	stopped := make(chan struct{})
	go func() {
		s.p.stopPush()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(pushStopWait + time.Second):
		t.Fatal("stopping waited for a push server that does not answer")
	}
}

// At most so many requests at once: a lane one, a person two, a push server
// four, and eight in all.
func TestThePushLimits(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	r := s.p.pushes()
	r.registerLimiter = newRateLimiter(100, time.Minute)

	type person struct {
		user   string
		phones []*testPhone
	}
	var people []person
	for i, user := range []string{sceneLinked, sceneLetIn, sceneLinkedAdmin} {
		entry := person{user: user}
		for j := range 3 {
			session := "sess-" + string(rune('a'+i)) + string(rune('a'+j))
			s.session(session, user)
			phone := newTestPhone(t, server, session)
			s.register(phone, user, session)
			entry.phones = append(entry.phones, phone)
		}
		people = append(people, entry)
	}

	most := 0
	server.hold()
	for _, entry := range people {
		for k := range 3 {
			s.p.NotificationWillBePushed(message("post-"+string(rune('a'+k)), "chan-"+string(rune('a'+k)), "x"), entry.user)
		}
	}
	eventually(t, "the host is full", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.busy > most {
			most = r.busy
		}
		for _, n := range r.personBusy {
			require.LessOrEqual(t, n, pushPerPerson)
		}
		return r.hostBusy[server.host] == pushPerHost
	})
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	require.Equal(t, pushPerHost, r.busy, "one push server, four at once")
	r.mu.Unlock()
	server.release()

	for _, entry := range people {
		for _, phone := range entry.phones {
			for range 3 {
				phone.next(t)
			}
		}
	}
	require.LessOrEqual(t, most, pushPerHost)
}

// One phone sent more than its bucket holds loses what is over.
func TestOnePhoneIsNotFlooded(t *testing.T) {
	s, _, phone := pushReady(t)
	for i := range pushBucketBurst + 5 {
		s.p.NotificationWillBePushed(message("post", "chan-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "x"), sceneLinked)
	}
	for range pushBucketBurst {
		phone.next(t)
	}
	phone.nothing(t, 80*time.Millisecond)
	require.Equal(t, int64(5), s.p.pushes().droppedNow()["too many for one phone"])
}

// Nothing logged while pushing ever carries a push address, a key, a nonce
// or a message.
func TestPushLogsCarryNoAddressKeyOrMessage(t *testing.T) {
	s, server, phone := pushReady(t)
	server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest}, fakeAnswer{status: http.StatusGone})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "a secret message"), sceneLinked)
	eventually(t, "the failure", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "a secret message"), sceneLinked)
	eventually(t, "gone", func() bool { return s.device(sceneLinked, "sess-dave") == nil })

	// Nothing may still be sending while the calls are read.
	registration := phone.registration(s.p.pushes().publicKey())
	s.p.stopPush()
	for _, call := range s.api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		line := strings.Join(strings.Fields(strings.ReplaceAll(fmtArgs(call.Arguments), "\n", " ")), " ")
		for _, secret := range []string{"/push/", registration.P256dh, registration.Auth, "a secret message", "sess-dave"} {
			require.NotContains(t, line, secret)
		}
	}
}

// setPushClient gives the scene's sending a client with another timeout,
// under the rules as configured.
func setPushClient(s *pushScene, timeout time.Duration) {
	r := s.p.pushes()
	client := pushsend.NewClient(pushsend.Options{Rules: s.p.getConfiguration().pushRules(), RequestTimeout: timeout})
	r.mu.Lock()
	r.client = client
	r.mu.Unlock()
}

func fmtArgs(args []any) string {
	var parts []string
	for _, arg := range args {
		encoded, _ := json.Marshal(arg)
		parts = append(parts, string(encoded))
	}

	return strings.Join(parts, " ")
}

// suspendPhone has ten pushes in a row fail for good, which suspends the
// phone.
func suspendPhone(t *testing.T, s *pushScene, server *fakePushServer, phone *testPhone) {
	t.Helper()

	for range pushFailuresToSuspend {
		server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest})
	}
	for i := range pushFailuresToSuspend {
		before := server.requests()
		s.p.NotificationWillBePushed(message("post-s"+strconv.Itoa(i), "chan-s"+strconv.Itoa(i), "x"), sceneLinked)
		eventually(t, "sent once", func() bool { return server.requests() == before+1 })
	}
	eventually(t, "suspended", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateFailing })
}

// A suspended phone is sent nothing for ten minutes, then one push, once,
// with no retries; a try that fails writes nothing and the next is ten
// minutes on; one that goes through puts the phone back to work.
func TestASuspendedPhoneIsTriedEveryTenMinutes(t *testing.T) {
	s, server, phone := pushReady(t)
	suspendPhone(t, s, server, phone)
	before := server.requests()

	s.advance(pushFailingRetryEvery - time.Minute)
	s.p.NotificationWillBePushed(message("post-a", "chan-a", "x"), sceneLinked)
	phone.nothing(t, 60*time.Millisecond)
	require.Equal(t, before, server.requests(), "nothing within ten minutes")

	// The try fails: sent once, nothing written, still suspended.
	s.advance(time.Minute)
	server.answer(phone.id, fakeAnswer{status: http.StatusServiceUnavailable})
	writes := s.faults.writesTo(pushUserKey(sceneLinked))
	s.p.NotificationWillBePushed(message("post-b", "chan-b", "x"), sceneLinked)
	eventually(t, "the try", func() bool { return server.requests() == before+1 })
	phone.nothing(t, 60*time.Millisecond)
	require.Equal(t, before+1, server.requests(), "no retries of its own")
	require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)), "a failed try writes nothing")
	require.Equal(t, pushStateFailing, s.device(sceneLinked, "sess-dave").State)

	s.advance(pushFailingRetryEvery / 2)
	s.p.NotificationWillBePushed(message("post-c", "chan-c", "x"), sceneLinked)
	phone.nothing(t, 60*time.Millisecond)
	require.Equal(t, before+1, server.requests(), "the next try is ten minutes on")

	// Ten minutes after the failed try: it goes through.
	s.advance(pushFailingRetryEvery / 2)
	s.p.NotificationWillBePushed(message("post-d", "chan-d", "x"), sceneLinked)
	require.Equal(t, "post-d", phone.next(t)["post"])
	eventually(t, "confirmed again", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateConfirmed })
	device := s.device(sceneLinked, "sess-dave")
	require.Equal(t, s.clock().UnixMilli(), device.Since)
	require.Zero(t, device.Trouble)
	require.Empty(t, device.Kind)
	require.Zero(t, device.Status)
	s.api.AssertCalled(t, "LogInfo", "A push server takes a phone's pushes again, so it is sent them as before", "host", phone.server.host)

	s.p.NotificationWillBePushed(message("post-e", "chan-e", "x"), sceneLinked)
	require.Equal(t, "post-e", phone.next(t)["post"])
	r := s.p.pushes()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotContains(t, r.revived, "sess-dave")
}

// A refused phone is tried the same way, at most once an hour.
func TestARefusedPhoneIsTriedEveryHour(t *testing.T) {
	s, server, phone := pushReady(t)
	server.answer(phone.id, fakeAnswer{status: http.StatusForbidden})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "refused", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateRefused })
	before := server.requests()

	s.advance(pushRefusedRetryEvery / 2)
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	phone.nothing(t, 60*time.Millisecond)
	require.Equal(t, before, server.requests())

	s.advance(pushRefusedRetryEvery / 2)
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinked)
	require.Equal(t, "post-3", phone.next(t)["post"])
	eventually(t, "confirmed again", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateConfirmed })
}

// A clear takes out only the waiting messages made before it, also when it
// comes back to its lane for a retry.
func TestARetriedClearLeavesLaterMessages(t *testing.T) {
	r := newPushRuntime()
	r.running = true

	at := time.UnixMilli(1_000_000)
	job := func(kind, post string, made time.Time) *pushJob {
		return &pushJob{kind: kind, user: sceneLinked, session: "sess-dave", conv: "chan-1|", made: made, payload: []byte(post)}
	}
	r.enqueue(job(pushKindMessage, "earlier", at.Add(-time.Second)), false)
	r.enqueue(job(pushKindMessage, "later", at.Add(time.Second)), false)
	r.enqueue(job(pushKindClear, "clear", at), true)

	var left []string
	for _, waiting := range r.lanes["sess-dave"].jobs {
		left = append(left, string(waiting.payload))
	}
	require.Equal(t, []string{"clear", "later"}, left)
	require.Equal(t, 2, r.queued)
}

// Four people whose push servers never answer are slow people once their
// sends have been slow, and hold at most half the sending slots; a fifth
// person's pushes go at once.
func TestFourTarpitsDelayNobodyElse(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	r.registerLimiter = newRateLimiter(100, time.Minute)

	attackers := []string{sceneLinked, sceneLinkedAdmin, sceneLetInAdmin, sceneLockedAdmin}
	type device struct {
		user    string
		session string
		server  *fakePushServer
	}
	var devices []device
	for i, user := range attackers {
		for j := range 4 {
			devices = append(devices, device{user: user, session: "sess-" + strconv.Itoa(i) + strconv.Itoa(j), server: s.pushServer()})
		}
	}
	quick := s.pushServer()
	s.configure(nil)
	s.start()
	for _, d := range devices {
		s.session(d.session, d.user)
		s.register(newTestPhone(t, d.server, d.session), d.user, d.session)
	}
	s.session("sess-erin", sceneLetIn)
	erin := newTestPhone(t, quick, "erin")
	s.register(erin, sceneLetIn, "sess-erin")

	timeout := 400 * time.Millisecond
	setPushClient(s, timeout)
	setSlowAfter(s, timeout/2)
	for _, d := range devices {
		d.server.hold()
	}
	for _, user := range attackers {
		for k := range 3 {
			s.p.NotificationWillBePushed(message("post-"+strconv.Itoa(k), "chan-"+strconv.Itoa(k), "x"), user)
		}
	}

	// Three timeouts' worth: every attacker has had slow sends, and is a
	// slow person.
	time.Sleep(3 * timeout)
	for k := range 3 {
		require.LessOrEqual(t, slowInFlight(r), pushInFlight/2)
		started := time.Now()
		post := "post-e" + strconv.Itoa(k)
		s.p.NotificationWillBePushed(message(post, "chan-e", "y"), sceneLetIn)
		require.Equal(t, post, erin.next(t)["post"])
		require.Less(t, time.Since(started), timeout/2, "Erin's push did not wait for the attackers' push servers")
	}

	r.mu.Lock()
	for _, user := range attackers {
		require.True(t, r.slow[user].After(s.clock()), "the attacker is slow")
	}
	require.NotContains(t, r.slow, sceneLetIn)
	r.mu.Unlock()
}

// setSlowAfter sets how long a send takes to be slow.
func setSlowAfter(s *pushScene, after time.Duration) {
	r := s.p.pushes()
	r.mu.Lock()
	r.slowAfter = after
	r.mu.Unlock()
}

// slowInFlight is how many sends in flight are to slow people.
func slowInFlight(r *pushRuntime) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.slowBusy
}

// latePushServer is a push server that answers 201 after a delay, keeping
// nothing, once the delay is set; until then it answers as any.
func latePushServer(s *pushScene, delay *atomic.Int64) *fakePushServer {
	server := &fakePushServer{answers: map[string][]fakeAnswer{}, got: map[string]chan fakePush{}}
	server.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait := time.Duration(delay.Load())
		if wait == 0 {
			server.serve(w, r)
			return
		}
		select {
		case <-time.After(wait):
			w.WriteHeader(http.StatusCreated)
		case <-r.Context().Done():
		}
	}))
	s.t.Cleanup(server.close)
	parsed, err := url.Parse(server.srv.URL)
	require.NoError(s.t, err)
	server.host = parsed.Host
	s.servers = append(s.servers, server)

	return server
}

// Four people with two phones each, whose push servers answer 201 just
// before the timeout, hold at most half the sending slots, and another
// person's push goes at once all the while.
func TestSlowAnswersHoldAtMostHalfTheSlots(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	r.registerLimiter = newRateLimiter(100, time.Minute)

	var delay atomic.Int64
	attackers := []string{sceneLinked, sceneLinkedAdmin, sceneLetInAdmin, sceneLockedAdmin}
	type device struct {
		user, session string
		server        *fakePushServer
	}
	var devices []device
	for i, user := range attackers {
		for j := range 2 {
			devices = append(devices, device{user: user, session: "sess-" + strconv.Itoa(i) + strconv.Itoa(j), server: latePushServer(s, &delay)})
		}
	}
	quick := s.pushServer()
	s.configure(nil)
	s.start()
	for _, d := range devices {
		s.session(d.session, d.user)
		s.register(newTestPhone(t, d.server, d.session), d.user, d.session)
	}
	s.session("sess-erin", sceneLetIn)
	erin := newTestPhone(t, quick, "erin")
	s.register(erin, sceneLetIn, "sess-erin")

	timeout := 700 * time.Millisecond
	setPushClient(s, timeout)
	setSlowAfter(s, timeout/3)
	delay.Store(int64(timeout - 100*time.Millisecond))

	stop := make(chan struct{})
	flooded := make(chan struct{})
	defer func() {
		close(stop)
		<-flooded
	}()
	most := 0
	go func() {
		defer close(flooded)
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			case <-time.After(60 * time.Millisecond):
			}
			for _, user := range attackers {
				s.p.NotificationWillBePushed(message("post-a"+strconv.Itoa(n), "chan-a"+strconv.Itoa(n), "x"), user)
			}
		}
	}()
	watch := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		most = max(most, r.slowBusy)
		require.LessOrEqual(t, r.slowBusy, pushInFlight/2, "slow people hold at most half the slots")
	}

	// Once their sends have been slow, the four are slow people.
	time.Sleep(2 * timeout)
	for k := range 6 {
		watch()
		started := time.Now()
		post := "post-e" + strconv.Itoa(k)
		s.p.NotificationWillBePushed(message(post, "chan-e"+strconv.Itoa(k), "y"), sceneLetIn)
		require.Equal(t, post, erin.next(t)["post"])
		require.Less(t, time.Since(started), timeout/3, "Erin's push did not wait for the slow push servers")
		watch()
		time.Sleep(timeout / 4)
	}
	require.Equal(t, pushInFlight/2, most, "the slow people used their half")
	r.mu.Lock()
	require.Empty(t, r.hostRest, "an answer is not a host to rest")
	r.mu.Unlock()
}

// A person's phone whose push server works is not held up by their other
// phone, whose push server takes the connection and never answers.
func TestAQuietSecondPhoneDoesNotHoldTheFirst(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	good, quiet := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-a", sceneLinked)
	s.session("sess-b", sceneLinked)
	phoneA := newTestPhone(t, good, "a")
	s.register(phoneA, sceneLinked, "sess-a")
	s.register(newTestPhone(t, quiet, "b"), sceneLinked, "sess-b")
	timeout := 100 * time.Millisecond
	setPushClient(s, timeout)
	setSlowAfter(s, timeout/2)
	quiet.hold()

	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	require.Equal(t, "post-1", phoneA.next(t)["post"])
	eventually(t, "the quiet phone's tries are over", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return quiet.requests() >= 2+len(r.messageWaits) && r.parked == 0 && r.busy == 0
	})

	for k := range 3 {
		started := time.Now()
		post := "post-" + strconv.Itoa(k+2)
		s.p.NotificationWillBePushed(message(post, "chan-"+strconv.Itoa(k+2), "y"), sceneLinked)
		require.Equal(t, post, phoneA.next(t)["post"])
		require.Less(t, time.Since(started), timeout, "the working phone did not wait")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	require.True(t, r.slow[sceneLinked].After(s.clock()), "a slow person")
	require.Empty(t, r.hostRest, "one person's push server rests no host")
}

// One person's slow sends that get no answer do not rest a host, however
// many; once another person's does too, it rests.
func TestTheBreakerRestsAHostOnlyForTwoPeople(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	slow := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.register(newTestPhone(t, slow, "dave"), sceneLinked, "sess-dave")
	s.register(newTestPhone(t, slow, "bob"), sceneLinkedAdmin, "sess-bob")
	setPushClient(s, 100*time.Millisecond)
	setSlowAfter(s, 50*time.Millisecond)
	slow.hold()

	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	eventually(t, "Dave's tries are over", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRun[slow.host].sends == 2*(1+len(r.messageWaits)) && r.parked == 0 && r.busy == 0
	})
	r.mu.Lock()
	require.NotContains(t, r.hostRest, slow.host, "one person's sends rest no host")
	r.mu.Unlock()

	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinkedAdmin)
	eventually(t, "the host rests", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRest[slow.host].After(s.clock())
	})
	s.api.AssertCalled(t, "LogWarn", "A push server did not answer three times in a row, so nothing but check messages is sent to it for a minute", "host", slow.host)
}

// What counts toward a host's run: a send that got no answer and was slow.
// A dial the address rules refused, a send that failed at once and any
// answer do not; anything that does not count ends the run.
func TestWhatCountsTowardAHostsBreaker(t *testing.T) {
	r := newPushRuntime()
	timeout := pushsend.Outcome{Result: pushsend.Retry, Kind: pushsend.KindUnreachable, Reason: pushsend.ReasonTimeout}
	rules := pushsend.Outcome{Result: pushsend.Retry, Kind: pushsend.KindUnreachable, Reason: pushsend.ReasonRules}
	refused := pushsend.Outcome{Result: pushsend.Retry, Kind: pushsend.KindUnreachable, Reason: pushsend.ReasonConnection}
	answered := pushsend.Outcome{Result: pushsend.Delivered}

	for _, outcome := range []struct {
		what    string
		outcome pushsend.Outcome
		slow    bool
	}{{"a dial the rules refused", rules, true}, {"a send that failed at once", refused, false}, {"an answer, however slow", answered, true}} {
		for _, user := range []string{"uid-a", "uid-b", "uid-a", "uid-b"} {
			require.False(t, r.hostBreaker("h", user, outcome.outcome, outcome.slow), outcome.what)
		}
		require.Empty(t, r.hostRun, outcome.what)
	}

	require.False(t, r.hostBreaker("h", "uid-a", timeout, true))
	require.False(t, r.hostBreaker("h", "uid-b", timeout, true))
	require.False(t, r.hostBreaker("h", "uid-a", refused, false), "ends the run")
	require.Empty(t, r.hostRun)
	require.False(t, r.hostBreaker("h", "uid-a", timeout, true))
	require.False(t, r.hostBreaker("h", "uid-b", timeout, true))
	require.True(t, r.hostBreaker("h", "uid-a", timeout, true), "three, from two people")
	require.Empty(t, r.hostRun)

	// A connection refused at once, for two people, rests nothing.
	s := newPushScene(t)
	q := s.p.pushes()
	dead := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.register(newTestPhone(t, dead, "dave"), sceneLinked, "sess-dave")
	s.register(newTestPhone(t, dead, "bob"), sceneLinkedAdmin, "sess-bob")
	dead.srv.Close()
	for k := range 3 {
		s.p.NotificationWillBePushed(message("post-"+strconv.Itoa(k), "chan-"+strconv.Itoa(k), "x"), sceneLinked)
		s.p.NotificationWillBePushed(message("post-"+strconv.Itoa(k), "chan-"+strconv.Itoa(k), "x"), sceneLinkedAdmin)
	}
	eventually(t, "the tries are over", func() bool {
		return s.device(sceneLinked, "sess-dave").Trouble != 0 && s.device(sceneLinkedAdmin, "sess-bob").Trouble != 0
	})
	eventually(t, "nothing is sending", func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.parked == 0 && q.busy == 0 && q.queued == 0
	})
	q.mu.Lock()
	defer q.mu.Unlock()
	require.Empty(t, q.hostRest)
	require.Empty(t, q.hostRun)
}

// The rest of a host is written with the decision to rest it: no send to
// it starts in between, not even while the line saying so is logged.
func TestNoPushStartsBeforeTheRest(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	slow := s.pushServer()

	var once sync.Once
	logging, logged := make(chan struct{}), make(chan struct{})
	s.api.On("LogWarn", "A push server did not answer three times in a row, so nothing but check messages is sent to it for a minute", "host", mock.Anything).Run(func(mock.Arguments) {
		once.Do(func() { close(logging) })
		<-logged
	}).Return()
	calls := s.api.ExpectedCalls
	s.api.ExpectedCalls = append([]*mock.Call{calls[len(calls)-1]}, calls[:len(calls)-1]...)
	release := sync.OnceFunc(func() { close(logged) })
	defer release()

	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.register(newTestPhone(t, slow, "dave"), sceneLinked, "sess-dave")
	s.register(newTestPhone(t, slow, "bob"), sceneLinkedAdmin, "sess-bob")
	setPushClient(s, 100*time.Millisecond)
	setSlowAfter(s, 50*time.Millisecond)
	r.mu.Lock()
	r.messageWaits = []time.Duration{time.Hour, time.Hour, time.Hour}
	r.mu.Unlock()
	slow.hold()

	run := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRun[slow.host].sends
	}
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "one", func() bool { return run() == 1 })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	eventually(t, "two", func() bool { return run() == 2 })
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinkedAdmin)
	select {
	case <-logging:
	case <-time.After(5 * time.Second):
		t.Fatal("the host never rested")
	}

	// The line is being logged: a push for Dave waits in his lane.
	before := slow.requests()
	s.p.NotificationWillBePushed(message("post-4", "chan-4", "x"), sceneLinked)
	waiting := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-dave"]
		return lane != nil && len(lane.jobs) == 1
	}
	eventually(t, "the push is in Dave's lane or sent", func() bool { return waiting() || slow.requests() > before })
	r.pump(s.p)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, before, slow.requests(), "nothing went to the host")
	require.True(t, waiting())

	release()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.True(t, r.hostRest[slow.host].After(s.clock()))
}

// A person is slow for ten minutes from their last slow send: a send of
// theirs that starts then counts toward the slow half; after, it does not.
func TestASlowPersonIsSlowForTenMinutes(t *testing.T) {
	s, server, phone := pushReady(t)
	r := s.p.pushes()
	slowUntil := func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.slow[sceneLinked]
	}

	setSlowAfter(s, 0)
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	phone.next(t)
	eventually(t, "slow", func() bool { return slowUntil().Equal(s.clock().Add(pushSlowFor)) })

	// Every slow send renews it.
	s.advance(5 * time.Minute)
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	phone.next(t)
	eventually(t, "renewed", func() bool { return slowUntil().Equal(s.clock().Add(pushSlowFor)) })
	setSlowAfter(s, time.Hour)

	countsSlow := func(post string) int {
		server.hold()
		s.p.NotificationWillBePushed(message(post, "chan-"+post, "x"), sceneLinked)
		eventually(t, "sending", func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.busy == 1
		})
		n := slowInFlight(r)
		server.release()
		require.Equal(t, post, phone.next(t)["post"])
		eventually(t, "sent", func() bool { return slowInFlight(r) == 0 })
		return n
	}
	s.advance(pushSlowFor - time.Second)
	require.Equal(t, 1, countsSlow("post-3"), "still slow")
	s.advance(time.Second)
	require.Equal(t, 0, countsSlow("post-4"), "ten minutes on, no longer")
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotContains(t, r.slow, sceneLinked, "let go of")
}

// A hello and a test push are tried twice in all, two seconds apart, so
// that a hello that cannot be delivered says so while the app waits; the
// hello's failure is logged.
func TestAHelloAndATestAreTriedTwice(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	require.Equal(t, 2*time.Second, pushRetryWaits[0])

	phone := newTestPhone(t, server, "dave")
	server.answer(phone.id, fakeAnswer{status: 503}, fakeAnswer{status: 503}, fakeAnswer{status: 503})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(s.p.pushes().publicKey())))
	eventually(t, "failing", func() bool {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State == pushStateFailing
	})
	phone.nothing(t, 60*time.Millisecond)
	require.Equal(t, 2, server.requests(), "the hello, once and once more")
	s.api.AssertCalled(t, "LogInfo", "A phone's first push could not be delivered", "host", server.host, "kind", "answer", "status", 503, "reason", "status")

	// A phone registered and confirmed: its test push is tried twice too.
	other := newTestPhone(t, server, "dave-2")
	s.register(other, sceneLinked, "sess-dave")
	before := server.requests()
	server.answer(other.id, fakeAnswer{status: 503}, fakeAnswer{status: 503}, fakeAnswer{status: 503})
	require.Equal(t, http.StatusAccepted, s.call(http.MethodPost, mePushTestPath, sceneLinked, "sess-dave", nil).Code)
	eventually(t, "written as failed", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
	other.nothing(t, 60*time.Millisecond)
	require.Equal(t, before+2, server.requests(), "the test push, once and once more")
}

const pushNoSubscriberLine = "A push server has no subscriber for a phone's push address yet, so its check message is tried again until the phone's UnifiedPush app connects, for ten minutes at most"

// countHostLog counts a log line that names a host, ahead of the scene's
// own.
func countHostLog(s *pushScene, level, line, host string) *atomic.Int64 {
	var n atomic.Int64
	s.api.On(level, line, "host", host).Run(func(mock.Arguments) { n.Add(1) }).Return()
	calls := s.api.ExpectedCalls
	s.api.ExpectedCalls = append([]*mock.Call{calls[len(calls)-1]}, calls[:len(calls)-1]...)

	return &n
}

// A hello answered 507 (ntfy's answer while the phone's ntfy has not
// subscribed to the address yet) waits for the phone: it is tried again
// while its pending side lives and is still the device's, with nothing
// written meanwhile, and the line saying so is logged once. A hello answered
// 507 and then another answer to try later has had its tries.
func TestAHelloWaitsForThePhonesApp(t *testing.T) {
	scene := func(t *testing.T, servers int, waits ...time.Duration) (*pushScene, []*fakePushServer, *pushRuntime) {
		s := newPushScene(t)
		var made []*fakePushServer
		for range servers {
			made = append(made, s.pushServer())
		}
		s.configure(nil)
		s.start()
		s.session("sess-dave", sceneLinked)
		r := s.p.pushes()
		r.mu.Lock()
		r.noSubscriberWaits = waits
		r.mu.Unlock()
		return s, made, r
	}
	idle := func(r *pushRuntime, parked int) func() bool {
		return func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.parked == parked && r.busy == 0
		}
	}
	state := func(s *pushScene) string {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State
	}
	register := func(s *pushScene, phone *testPhone) {
		require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(s.p.pushes().publicKey()))).State)
	}
	noSubscriber := fakeAnswer{status: http.StatusInsufficientStorage}
	const failedLine = "A phone's first push could not be delivered"

	t.Run("delivered on a later try, and confirmed", func(t *testing.T) {
		s, servers, r := scene(t, 1, 5*time.Millisecond, 300*time.Millisecond)
		server := servers[0]
		logged := countHostLog(s, "LogInfo", pushNoSubscriberLine, server.host)
		phone := newTestPhone(t, server, "dave")
		server.answer(phone.id, noSubscriber, noSubscriber)
		register(s, phone)
		writes := s.faults.writesTo(pushUserKey(sceneLinked))

		eventually(t, "answered 507 twice, and waiting", func() bool { return server.requests() == 2 && idle(r, 1)() })
		require.Equal(t, pushStatePending, state(s), "it reads as waiting")
		require.Empty(t, s.device(sceneLinked, "sess-dave").Pending.State)
		require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)), "nothing written")

		hello := phone.next(t)
		require.Equal(t, pushKindHello, hello["type"])
		require.Equal(t, 3, server.requests())
		confirmed := s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": hello["nonce"]}))
		require.Equal(t, pushStateConfirmed, confirmed.State)
		require.Equal(t, int64(1), logged.Load(), "logged once for the hello, not for each try")
		s.api.AssertNotCalled(t, "LogInfo", failedLine, "host", server.host, "kind", "answer", "status", http.StatusInsufficientStorage, "reason", "status")
	})

	t.Run("507 and then 503 is failing", func(t *testing.T) {
		s, servers, _ := scene(t, 1, 5*time.Millisecond)
		server := servers[0]
		phone := newTestPhone(t, server, "dave")
		server.answer(phone.id, noSubscriber, fakeAnswer{status: http.StatusServiceUnavailable}, fakeAnswer{status: http.StatusServiceUnavailable})
		register(s, phone)
		eventually(t, "failing", func() bool { return state(s) == pushStateFailing })
		phone.nothing(t, 60*time.Millisecond)
		require.Equal(t, 2, server.requests(), "no third try")
		require.Equal(t, http.StatusServiceUnavailable, s.device(sceneLinked, "sess-dave").Pending.Status)
		s.api.AssertCalled(t, "LogInfo", failedLine, "host", server.host, "kind", "answer", "status", http.StatusServiceUnavailable, "reason", "status")
	})

	t.Run("not tried again once a new registration replaced it", func(t *testing.T) {
		s, servers, r := scene(t, 2, 5*time.Millisecond, 300*time.Millisecond)
		old, next := servers[0], servers[1]
		first := newTestPhone(t, old, "first")
		old.answer(first.id, noSubscriber, noSubscriber)
		register(s, first)
		eventually(t, "waiting to be tried again", func() bool { return old.requests() == 2 && idle(r, 1)() })

		second := newTestPhone(t, next, "second")
		register(s, second)
		require.Equal(t, pushKindHello, second.next(t)["type"])
		eventually(t, "the old hello's tries are over", idle(r, 0))
		first.nothing(t, 400*time.Millisecond)
		require.Equal(t, 2, old.requests(), "the old hello, which would have gone through, was not tried again")
		require.Equal(t, pushStatePending, state(s))
		s.api.AssertNotCalled(t, "LogInfo", failedLine, "host", old.host, "kind", "answer", "status", http.StatusInsufficientStorage, "reason", "status")
	})

	t.Run("not tried again once confirmed", func(t *testing.T) {
		s, servers, r := scene(t, 1, 5*time.Millisecond, 300*time.Millisecond)
		server := servers[0]
		phone := newTestPhone(t, server, "dave")
		server.answer(phone.id, noSubscriber, noSubscriber)
		register(s, phone)
		eventually(t, "waiting to be tried again", func() bool { return server.requests() == 2 && idle(r, 1)() })

		nonce := s.device(sceneLinked, "sess-dave").Pending.Nonce
		require.Equal(t, pushStateConfirmed, s.view(s.call(http.MethodPost, mePushConfirmPath, sceneLinked, "sess-dave", map[string]any{"nonce": nonce})).State)
		eventually(t, "its tries are over", idle(r, 0))
		phone.nothing(t, 400*time.Millisecond)
		require.Equal(t, 2, server.requests())
		require.Equal(t, pushStateConfirmed, state(s))
	})

	t.Run("not tried again once lapsed", func(t *testing.T) {
		s, servers, r := scene(t, 1, 5*time.Millisecond, 300*time.Millisecond)
		server := servers[0]
		phone := newTestPhone(t, server, "dave")
		server.answer(phone.id, noSubscriber, noSubscriber)
		register(s, phone)
		eventually(t, "waiting to be tried again", func() bool { return server.requests() == 2 && idle(r, 1)() })

		s.advance(pushPendingLife)
		eventually(t, "its tries are over", idle(r, 0))
		phone.nothing(t, 400*time.Millisecond)
		require.Equal(t, 2, server.requests())
		require.Equal(t, pushStateNone, state(s))
		s.api.AssertNotCalled(t, "LogInfo", failedLine, "host", server.host, "kind", "answer", "status", http.StatusInsufficientStorage, "reason", "status")
	})

	t.Run("no try after its pending side lapses", func(t *testing.T) {
		s, servers, r := scene(t, 1, 300*time.Millisecond, 300*time.Millisecond)
		server := servers[0]
		phone := newTestPhone(t, server, "dave")
		server.answer(phone.id, noSubscriber, noSubscriber)
		register(s, phone)
		writes := s.faults.writesTo(pushUserKey(sceneLinked))
		eventually(t, "waiting to be tried again", func() bool { return server.requests() == 1 && idle(r, 1)() })

		// The second try comes 200 ms before the pending side lapses: the
		// third would come after, and is not made.
		s.advance(pushPendingLife - 200*time.Millisecond)
		eventually(t, "the second try, and nothing waiting", func() bool { return server.requests() == 2 && idle(r, 0)() })
		phone.nothing(t, 400*time.Millisecond)
		require.Equal(t, 2, server.requests())
		require.Equal(t, pushStatePending, state(s), "nothing written")
		require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)))
		s.api.AssertNotCalled(t, "LogInfo", failedLine, "host", server.host, "kind", "answer", "status", http.StatusInsufficientStorage, "reason", "status")
	})
}

// steppedPushServer is a push server that, once armed, moves the scene's
// clock with each request to the moment pushMessageRetryWaits would have it
// tried, counted from when it was armed, and closes the connection without
// an answer until refuseFor has passed.
func steppedPushServer(s *pushScene, refuseFor time.Duration) (*fakePushServer, func()) {
	server := &fakePushServer{answers: map[string][]fakeAnswer{}, got: map[string]chan fakePush{}}
	var lock sync.Mutex
	var armed bool
	var base time.Time
	var tries int
	server.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		on, offset := armed, time.Duration(0)
		if on {
			for _, wait := range pushMessageRetryWaits[:min(tries, len(pushMessageRetryWaits))] {
				offset += wait
			}
			tries++
		}
		lock.Unlock()

		if on {
			s.lock.Lock()
			s.now = base.Add(offset)
			s.lock.Unlock()
			if offset < refuseFor {
				server.lock.Lock()
				server.count++
				server.lock.Unlock()
				if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
					_ = conn.Close()
				}
				return
			}
		}
		server.serve(w, r)
	}))
	s.t.Cleanup(server.close)
	parsed, err := url.Parse(server.srv.URL)
	require.NoError(s.t, err)
	server.host = parsed.Host
	s.servers = append(s.servers, server)

	return server, func() {
		lock.Lock()
		defer lock.Unlock()
		armed, base, tries = true, s.clock(), 0
	}
}

// A message or a clear that may go through later is tried after 2, 10 and
// 60 seconds and then once a minute, never later than five minutes after it
// was made; one that never goes through is one failure of its phone.
func TestAMessageIsTriedForFiveMinutes(t *testing.T) {
	var at time.Duration
	var tries []time.Duration
	for _, wait := range pushMessageRetryWaits {
		at += wait
		tries = append(tries, at)
	}
	require.Equal(t, []time.Duration{2 * time.Second, 12 * time.Second, 72 * time.Second, 132 * time.Second, 192 * time.Second, 252 * time.Second}, tries)
	require.Greater(t, at+time.Minute, pushMessageRetryAge, "one more minute would be past the five")

	ready := func(t *testing.T, refuseFor time.Duration) (*pushScene, *fakePushServer, *testPhone, func()) {
		s := newPushScene(t)
		server, arm := steppedPushServer(s, refuseFor)
		s.configure(nil)
		s.start()
		s.session("sess-dave", sceneLinked)
		phone := newTestPhone(t, server, "dave")
		s.register(phone, sceneLinked, "sess-dave")
		return s, server, phone, arm
	}
	idle := func(r *pushRuntime) func() bool {
		return func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.parked == 0 && r.busy == 0 && r.queued == 0
		}
	}
	failRun := func(r *pushRuntime) int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.failRun["sess-dave"]
	}

	t.Run("a push server that refuses connections for 90 seconds", func(t *testing.T) {
		s, server, phone, arm := ready(t, 90*time.Second)
		before := server.requests()
		arm()
		start := s.clock()
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		require.Equal(t, "post-1", phone.next(t)["post"])
		require.Equal(t, before+5, server.requests(), "refused at 0, 2, 12 and 72 seconds, delivered at 132")
		require.Equal(t, start.Add(132*time.Second), s.clock())
		eventually(t, "nothing more to send", idle(s.p.pushes()))
		require.Zero(t, s.device(sceneLinked, "sess-dave").Trouble)
	})

	t.Run("no try after five minutes, and one failure", func(t *testing.T) {
		s, server, phone, arm := ready(t, time.Hour)
		r := s.p.pushes()
		before := server.requests()
		arm()
		start := s.clock()
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "written as failed", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
		eventually(t, "nothing more to send", idle(r))
		phone.nothing(t, 60*time.Millisecond)
		require.Equal(t, before+7, server.requests(), "at 0, 2, 12, 72, 132, 192 and 252 seconds")
		require.Equal(t, start.Add(252*time.Second), s.clock())
		require.Equal(t, 1, failRun(r), "every try, one failure")
		require.Equal(t, pushStateConfirmed, s.device(sceneLinked, "sess-dave").State)
	})

	t.Run("a clear, counted from when it was made", func(t *testing.T) {
		s, server, phone := pushReady(t)
		r := s.p.pushes()
		r.mu.Lock()
		r.messageWaits = []time.Duration{300 * time.Millisecond, 300 * time.Millisecond, 300 * time.Millisecond}
		r.mu.Unlock()
		server.answer(phone.id, fakeAnswer{status: http.StatusServiceUnavailable}, fakeAnswer{status: http.StatusServiceUnavailable})
		before := server.requests()
		s.p.NotificationWillBePushed(&model.PushNotification{Type: model.PushTypeClear, ChannelId: "chan-1"}, sceneLinked)
		eventually(t, "waiting to be tried again", func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.parked == 1
		})

		// Its second try comes 100 ms before five minutes from when it was
		// made: the third would come after, and is not made.
		s.advance(pushMessageRetryAge - 100*time.Millisecond)
		eventually(t, "written as failed", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
		eventually(t, "nothing more to send", idle(r))
		phone.nothing(t, 400*time.Millisecond)
		require.Equal(t, before+2, server.requests())
		require.Equal(t, 1, failRun(r))
	})
}

// A hello waits for nothing sent to the address it is to replace: while the
// main side's push server takes the connection and never answers, with
// pushes waiting behind it, the hello to another address goes at once.
func TestAHelloWaitsForNothingOfTheOldAddress(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	quiet, next := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, quiet, "dave"), sceneLinked, "sess-dave")
	timeout := time.Second
	setPushClient(s, timeout)
	quiet.hold()

	for k := range 3 {
		s.p.NotificationWillBePushed(message("post-"+strconv.Itoa(k), "chan-"+strconv.Itoa(k), "x"), sceneLinked)
	}
	eventually(t, "one push sending and two waiting", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-dave"]
		return lane != nil && lane.busy && len(lane.jobs) == 2
	})

	started := time.Now()
	phone := newTestPhone(t, next, "dave-new")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(r.publicKey()))).State)
	require.Equal(t, pushKindHello, phone.next(t)["type"])
	require.Less(t, time.Since(started), timeout, "the hello did not wait for the old address's push server")
	require.Equal(t, 1, next.requests(), "on its first try")
}

// Once a new address is confirmed, the phone's pushes wait for nothing made
// for the address before: what waits for it in the lane is taken out, and a
// retry for it that comes back is not sent. The address is let go of after
// pushReplacedFor.
func TestAConfirmedAddressWaitsForNothingOfTheOldOne(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	quiet, next := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, quiet, "dave"), sceneLinked, "sess-dave")
	timeout := 500 * time.Millisecond
	setPushClient(s, timeout)
	r.mu.Lock()
	r.messageWaits = []time.Duration{300 * time.Millisecond, 300 * time.Millisecond, 300 * time.Millisecond}
	r.mu.Unlock()
	quiet.hold()

	for k := range 4 {
		s.p.NotificationWillBePushed(message("post-"+strconv.Itoa(k), "chan-"+strconv.Itoa(k), "x"), sceneLinked)
	}
	eventually(t, "one push sending and three waiting", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-dave"]
		return lane != nil && lane.busy && len(lane.jobs) == 3
	})

	moved := newTestPhone(t, next, "dave-moved")
	s.register(moved, sceneLinked, "sess-dave")
	started := time.Now()
	s.p.NotificationWillBePushed(message("post-new", "chan-new", "x"), sceneLinked)
	require.Equal(t, "post-new", moved.next(t)["post"])
	require.Less(t, time.Since(started), 2*timeout, "it waited for no more than the send already on its way")

	// The send that was on its way is not tried again.
	time.Sleep(2 * timeout)
	require.Equal(t, 2, quiet.requests(), "the hello and the first try of the push that was on its way")
	func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		require.Zero(t, r.queued)
		require.Zero(t, r.parked)
		require.Equal(t, moved.endpoint, r.replaced["sess-dave"].endpoint)
	}()

	s.advance(pushReplacedFor)
	r.trim()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotContains(t, r.replaced, "sess-dave")
}

// A failure of the address a phone had before it registered again counts
// nothing toward the new address's run.
func TestAFailureOfTheOldAddressCountsForNothing(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	old, next := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, old, "dave")
	s.register(phone, sceneLinked, "sess-dave")

	// A push to the old address is on its way when the new one is
	// confirmed, and fails for good after.
	old.answer(phone.id, fakeAnswer{status: http.StatusBadRequest})
	old.hold()
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "sending to the old address", func() bool { return old.requests() == 2 })
	moved := newTestPhone(t, next, "dave-moved")
	s.register(moved, sceneLinked, "sess-dave")
	old.release()
	eventually(t, "the old address failed", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.busy == 0
	})

	r.mu.Lock()
	run, counted := r.failRun["sess-dave"]
	r.mu.Unlock()
	require.False(t, counted, "a run of %d", run)
	device := s.device(sceneLinked, "sess-dave")
	require.Equal(t, pushStateConfirmed, device.State)
	require.Equal(t, moved.endpoint, device.Endpoint)
	require.Zero(t, device.Trouble)
}

// A hello is not held back by its push server's rest: tried at once, and
// once more, it reads as failing within the two tries, while a message for
// that push server waits for the rest to end.
func TestAHelloIsNotHeldByARest(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	host := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.session("sess-erin", sceneLetIn)
	s.register(newTestPhone(t, host, "dave"), sceneLinked, "sess-dave")
	bob := newTestPhone(t, host, "bob")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	setPushClient(s, 100*time.Millisecond)
	setSlowAfter(s, 50*time.Millisecond)

	// Two people's pushes get no answer: the breaker rests the host.
	host.hold()
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinkedAdmin)
	eventually(t, "the host rests", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRest[host.host].After(s.clock())
	})
	eventually(t, "nothing is sending", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.busy == 0
	})
	host.release()

	// Erin registers there; her push server answers 503.
	erin := newTestPhone(t, host, "erin")
	host.answer(erin.id, fakeAnswer{status: 503}, fakeAnswer{status: 503})
	before := host.requests()
	s.view(s.call(http.MethodPut, mePushPath, sceneLetIn, "sess-erin", erin.registration(r.publicKey())))
	eventually(t, "failing", func() bool {
		return s.view(s.call(http.MethodGet, mePushPath, sceneLetIn, "sess-erin", nil)).State == pushStateFailing
	})
	require.Equal(t, before+2, host.requests(), "the hello, once and once more, and nothing else")

	// Bob's push still waits for the rest.
	bob.nothing(t, 50*time.Millisecond)
	r.mu.Lock()
	lane := r.lanes["sess-bob"]
	require.True(t, lane != nil && !lane.busy && len(lane.jobs) == 1, "Bob's push waits in his lane")
	r.mu.Unlock()
	s.advance(pushBreakerRest)
	r.pump(s.p)
	require.Equal(t, "post-2", bob.next(t)["post"])
}

// Pushes waiting for a host that rested go by themselves once the rest is
// over, with nothing else passing the lanes.
func TestPushesGoWhenTheRestIsOver(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	host := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.session("sess-bob", sceneLinkedAdmin)
	s.register(newTestPhone(t, host, "dave"), sceneLinked, "sess-dave")
	bob := newTestPhone(t, host, "bob")
	s.register(bob, sceneLinkedAdmin, "sess-bob")
	setPushClient(s, 100*time.Millisecond)
	setSlowAfter(s, 50*time.Millisecond)
	rest := time.Second
	r.mu.Lock()
	r.messageWaits = []time.Duration{time.Hour, time.Hour, time.Hour}
	r.breakerRest = rest
	r.mu.Unlock()
	host.hold()

	run := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRun[host.host].sends
	}
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "one", func() bool { return run() == 1 })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	eventually(t, "two", func() bool { return run() == 2 })
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinkedAdmin)
	eventually(t, "the host rests, and nothing is sending", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hostRest[host.host].After(s.clock()) && r.busy == 0
	})
	host.release()

	s.p.NotificationWillBePushed(message("post-4", "chan-4", "x"), sceneLinkedAdmin)
	eventually(t, "Bob's push waits in his lane", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-bob"]
		return lane != nil && !lane.busy && len(lane.jobs) == 1
	})
	s.advance(rest)
	require.Equal(t, "post-4", bob.next(t)["post"])
}

// The line that a phone's first push could not be delivered is about the
// hello the server waits for, not one a later registration took the place
// of; such a hello is not tried again.
func TestASupersededHelloIsNotLoggedAsFailed(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	r.mu.Lock()
	r.retryWaits = []time.Duration{300 * time.Millisecond}
	r.mu.Unlock()
	idle := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.parked == 0 && r.busy == 0
	}

	first := newTestPhone(t, server, "first")
	server.answer(first.id, fakeAnswer{status: 503}, fakeAnswer{status: 503})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", first.registration(r.publicKey())))
	eventually(t, "the first hello waits to be tried again", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.parked == 1
	})
	second := newTestPhone(t, server, "second")
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", second.registration(r.publicKey())))
	require.Equal(t, pushKindHello, second.next(t)["type"])
	eventually(t, "the first hello's tries are over", func() bool { return server.requests() == 2 && idle() })
	time.Sleep(400 * time.Millisecond)
	require.Equal(t, 2, server.requests(), "the first hello is not tried again once replaced")

	s.api.AssertNotCalled(t, "LogInfo", "A phone's first push could not be delivered", "host", server.host, "kind", "answer", "status", 503, "reason", "status")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
}

// A hello's second try already on its way when a later registration takes
// its pending side's place, and then answered no: nothing is written or
// logged for it, and the new registration still waits for its own hello.
func TestAHelloOnItsWayWhenReplacedIsNotLoggedAsFailed(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	r.mu.Lock()
	r.retryWaits = []time.Duration{300 * time.Millisecond}
	r.mu.Unlock()

	first := newTestPhone(t, server, "first")
	server.answer(first.id, fakeAnswer{status: 503}, fakeAnswer{status: 503})
	s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", first.registration(r.publicKey())))
	eventually(t, "the first hello waits to be tried again", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.parked == 1
	})
	server.hold()
	eventually(t, "its second try is on its way", func() bool { return server.requests() == 2 })

	second := newTestPhone(t, server, "second")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", second.registration(r.publicKey()))).State)
	server.release()
	require.Equal(t, pushKindHello, second.next(t)["type"])
	eventually(t, "nothing is sending", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.parked == 0 && r.busy == 0
	})

	require.Equal(t, 3, server.requests())
	s.api.AssertNotCalled(t, "LogInfo", "A phone's first push could not be delivered", "host", server.host, "kind", "answer", "status", 503, "reason", "status")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodGet, mePushPath, sceneLinked, "sess-dave", nil)).State)
	require.Empty(t, s.device(sceneLinked, "sess-dave").Pending.State, "the new pending side is as it was made")
}

// A delivery clears the phone's trouble also when this node noted a try of
// it that put nothing back to work: a push to the address it had before it
// registered again, on its way then, refused.
func TestADeliveryClearsTroubleAfterARefusedOldAddress(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	old, server := s.pushServer(), s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, old, "dave")
	s.register(phone, sceneLinked, "sess-dave")

	old.answer(phone.id, fakeAnswer{status: http.StatusForbidden})
	old.hold()
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "sending to the old address", func() bool { return old.requests() == 2 })
	moved := newTestPhone(t, server, "dave-moved")
	s.register(moved, sceneLinked, "sess-dave")
	old.release()
	eventually(t, "the old address refused", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		_, noted := r.revived["sess-dave"]
		return noted && r.busy == 0
	})
	device := s.device(sceneLinked, "sess-dave")
	require.Equal(t, pushStateConfirmed, device.State)
	require.Equal(t, moved.endpoint, device.Endpoint)

	server.answer(moved.id, fakeAnswer{status: http.StatusBadRequest})
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	eventually(t, "a failure", func() bool { return s.device(sceneLinked, "sess-dave").Trouble != 0 })
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinked)
	require.Equal(t, "post-3", moved.next(t)["post"])
	eventually(t, "the trouble is over", func() bool { return s.device(sceneLinked, "sess-dave").Trouble == 0 })
	require.Empty(t, s.device(sceneLinked, "sess-dave").Kind)
}

// A push that was waiting when its phone was suspended, and goes through,
// puts the phone back to work at once.
func TestAWaitingPushThatGoesThroughRevivesThePhone(t *testing.T) {
	s, server, phone := pushReady(t)
	r := s.p.pushes()
	for range pushFailuresToSuspend {
		server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest})
	}
	for i := range pushFailuresToSuspend - 1 {
		before := server.requests()
		s.p.NotificationWillBePushed(message("post-s"+strconv.Itoa(i), "chan-s"+strconv.Itoa(i), "x"), sceneLinked)
		eventually(t, "sent once", func() bool { return server.requests() == before+1 })
	}

	// The tenth failure is held while another push waits behind it.
	server.hold()
	before := server.requests()
	s.p.NotificationWillBePushed(message("post-10", "chan-10", "x"), sceneLinked)
	eventually(t, "the tenth is sending", func() bool { return server.requests() == before+1 })
	s.p.NotificationWillBePushed(message("post-w", "chan-w", "x"), sceneLinked)
	eventually(t, "the next one waits", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-dave"]
		return lane != nil && lane.busy && len(lane.jobs) == 1
	})
	server.release()

	require.Equal(t, "post-w", phone.next(t)["post"])
	eventually(t, "back to work", func() bool { return s.device(sceneLinked, "sess-dave").State == pushStateConfirmed })
	s.api.AssertCalled(t, "LogWarn", "Pushes to a phone kept failing, so it is tried with one of its pushes at most every ten minutes until one goes through", "host", phone.server.host, "kind", "answer", "status", http.StatusBadRequest)
	s.p.NotificationWillBePushed(message("post-n", "chan-n", "x"), sceneLinked)
	require.Equal(t, "post-n", phone.next(t)["post"])
}

// Dropping a device lets go of what this node remembers of its sends.
func TestDroppingADeviceForgetsItsPushes(t *testing.T) {
	s, _, phone := pushReady(t)
	r := s.p.pushes()
	r.mu.Lock()
	r.failRun["sess-dave"] = 2
	r.revived["sess-dave"] = s.clock()
	r.mu.Unlock()

	s.endSession("sess-dave")
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	require.Equal(t, "ended", phone.next(t)["type"])
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotContains(t, r.failRun, "sess-dave")
	require.NotContains(t, r.revived, "sess-dave")
}

// Clears are not metered by a phone's bucket.
func TestClearsAreNotMetered(t *testing.T) {
	s, _, phone := pushReady(t)
	for i := range pushBucketBurst + 5 {
		s.p.NotificationWillBePushed(&model.PushNotification{Type: model.PushTypeClear, ChannelId: "chan-" + strconv.Itoa(i)}, sceneLinked)
	}
	for range pushBucketBurst + 5 {
		require.Equal(t, "clear", phone.next(t)["type"])
	}
	require.Zero(t, s.p.pushes().droppedNow()["too many for one phone"])
}

// The ended push is tried again like any other.
func TestTheEndedPushIsTriedAgain(t *testing.T) {
	s, server, phone := pushReady(t)
	server.answer(phone.id, fakeAnswer{status: http.StatusServiceUnavailable}, fakeAnswer{status: http.StatusTooManyRequests})
	s.endSession("sess-dave")
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)

	require.Equal(t, "ended", phone.next(t)["type"])
	require.Equal(t, 4, server.requests(), "the hello, and the ended push three times")
	require.Nil(t, s.device(sceneLinked, "sess-dave"))
}

// A delivery clears a run of failures that began after its job was made.
func TestADeliveryClearsTroubleItsJobDidNotSee(t *testing.T) {
	s, server, phone := pushReady(t)
	r := s.p.pushes()
	server.hold()
	server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest}, fakeAnswer{status: http.StatusCreated})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "y"), sceneLinked)
	eventually(t, "the second waits behind the first", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		lane := r.lanes["sess-dave"]
		return lane != nil && lane.busy && len(lane.jobs) == 1 && lane.jobs[0].trouble == 0
	})
	server.release()

	require.Equal(t, "post-2", phone.next(t)["post"])
	eventually(t, "the trouble is over", func() bool { return s.device(sceneLinked, "sess-dave").Trouble == 0 })
	require.Empty(t, s.device(sceneLinked, "sess-dave").Kind)
}

// A good read clears a suspect mark this node made after the job was made,
// and the sweep clears one another node made.
func TestAGoodReadClearsASuspectMark(t *testing.T) {
	s, _, _ := pushReady(t)
	s.failSession("sess-dave", true)
	s.p.pushSessionUnread(sceneLinked, "sess-dave")
	require.NotZero(t, s.device(sceneLinked, "sess-dave").Suspect)

	s.failSession("sess-dave", false)
	require.True(t, s.p.pushSessionStands(&pushJob{user: sceneLinked, session: "sess-dave"}))
	require.Zero(t, s.device(sceneLinked, "sess-dave").Suspect)

	_, err := s.p.updatePushDoc(sceneLinked, func(doc *pushDoc, now time.Time) bool {
		doc.Devices[0].Suspect = now.UnixMilli()
		return true
	})
	require.NoError(t, err)
	s.p.sweepPushes(context.Background())
	require.Zero(t, s.device(sceneLinked, "sess-dave").Suspect)
	require.NotNil(t, s.device(sceneLinked, "sess-dave"))
}

// A session that could not be read is looked at again a little over a
// minute later: still unreadable while another session reads, the device is
// dropped and told so with no other push; readable, it stays.
func TestALoggedOutSessionIsLookedAtAgain(t *testing.T) {
	t.Run("still unreadable", func(t *testing.T) {
		s, server, phone := pushReady(t)
		aliveElsewhere(s)
		r := s.p.pushes()
		r.mu.Lock()
		r.suspectWait = 20 * time.Millisecond
		r.mu.Unlock()

		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		phone.nothing(t, 60*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"), "not before the grace")

		s.advance(pushSuspectGrace)
		require.Equal(t, "ended", phone.next(t)["type"])
		eventually(t, "dropped", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
		require.Equal(t, 2, server.requests(), "the hello and the ended push")
		s.api.AssertCalled(t, "LogInfo", "A phone's push registration was dropped", "why", "its session could not be read while others could", "host", server.host)
	})

	t.Run("readable again", func(t *testing.T) {
		s, _, phone := pushReady(t)
		r := s.p.pushes()
		r.mu.Lock()
		r.suspectWait = 20 * time.Millisecond
		r.mu.Unlock()

		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		s.failSession("sess-dave", false)
		s.advance(pushSuspectGrace)
		eventually(t, "cleared", func() bool { return s.device(sceneLinked, "sess-dave").Suspect == 0 })
		phone.nothing(t, 60*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))
		r.mu.Lock()
		defer r.mu.Unlock()
		require.Empty(t, r.suspects)
	})
}

// The memory the senders keep is let go of: rests and slow people that are
// over, runs of hosts with no lane or send, runs of Retry-After answers
// whose rests are over, old tries, and what a gone or deleted device left.
func TestWhatTheSendersRememberIsTrimmed(t *testing.T) {
	r := newPushRuntime()
	now := time.UnixMilli(10_000_000)
	r.now = func() time.Time { return now }
	r.running = true
	r.enqueue(&pushJob{kind: pushKindMessage, user: "uid-busy", session: "sess-busy", target: pushTarget{host: "busy.example"}}, false)

	r.hostRest = map[string]time.Time{"over.example": now.Add(-time.Second), "on.example": now.Add(time.Second)}
	r.slow = map[string]time.Time{"uid-over": now, "uid-on": now.Add(time.Second)}
	r.hostBusy = map[string]int{"sending.example": 1}
	r.hostRun = map[string]pushHostRun{"busy.example": {sends: 1}, "sending.example": {sends: 1}, "idle.example": {sends: 2}}
	r.revived = map[string]time.Time{"sess-old": now.Add(-pushRefusedRetryEvery), "sess-new": now.Add(-time.Minute)}
	r.laneRest = map[string]time.Time{"sess-over": now, "sess-on": now.Add(time.Second)}
	live := pushRetryRun{rests: [2]pushPersonRest{{user: "uid-a", until: now.Add(time.Second)}}}
	over := pushRetryRun{rests: [2]pushPersonRest{{user: "uid-a", until: now}, {user: "uid-b", until: now.Add(-time.Second)}}}
	r.retryRun = map[string]pushRetryRun{"busy.example": live, "sending.example": over, "idle.example": live}
	r.limited = map[string]time.Time{"sess-old": now.Add(-pushLimitedKept), "sess-new": now.Add(-time.Minute)}
	r.trim()

	require.Equal(t, map[string]time.Time{"on.example": now.Add(time.Second)}, r.hostRest)
	require.Equal(t, map[string]time.Time{"uid-on": now.Add(time.Second)}, r.slow)
	require.Equal(t, map[string]pushHostRun{"busy.example": {sends: 1}, "sending.example": {sends: 1}}, r.hostRun)
	require.Equal(t, map[string]time.Time{"sess-new": now.Add(-time.Minute)}, r.revived)
	require.Equal(t, map[string]time.Time{"sess-on": now.Add(time.Second)}, r.laneRest)
	require.Equal(t, map[string]pushRetryRun{"busy.example": live, "idle.example": live}, r.retryRun, "a run lasts as long as its rests, with or without a lane waiting")
	require.Equal(t, map[string]time.Time{"sess-new": now.Add(-time.Minute)}, r.limited)

	// A failure's run goes with its device: gone, deleted.
	s, server, phone := pushReady(t)
	q := s.p.pushes()
	failRun := func() int {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.failRun)
	}
	server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest}, fakeAnswer{status: http.StatusGone})
	s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
	eventually(t, "a run", func() bool { return failRun() == 1 })
	s.p.NotificationWillBePushed(message("post-2", "chan-2", "x"), sceneLinked)
	eventually(t, "gone", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
	require.Zero(t, failRun())

	s.register(phone, sceneLinked, "sess-dave")
	server.answer(phone.id, fakeAnswer{status: http.StatusBadRequest})
	s.p.NotificationWillBePushed(message("post-3", "chan-3", "x"), sceneLinked)
	eventually(t, "a run", func() bool { return failRun() == 1 })
	s.view(s.call(http.MethodDelete, mePushPath, sceneLinked, "sess-dave", nil))
	require.Zero(t, failRun())
}

// The sweep's cluster job returns at once, and a stop ends a sweep under
// way.
func TestTheSweepRunsOnItsOwn(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	var run func()
	r.schedule = func(_ cluster.JobPluginAPI, f func()) (clipsJob, error) {
		run = f
		return nil, nil
	}
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	s.register(newTestPhone(t, server, "dave"), sceneLinked, "sess-dave")
	r.mu.Lock()
	r.sweepPace = time.Hour
	r.mu.Unlock()

	returned := make(chan struct{})
	go func() {
		run()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("the job's callback waited for the sweep")
	}

	stopped := make(chan struct{})
	go func() {
		s.p.stopPush()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(pushStopWait):
		t.Fatal("stopping waited for the sweep")
	}
	s.api.AssertNotCalled(t, "LogWarn", "Push notifications were still sending when the plugin stopped")
}

// Switched off, nothing is sent, whatever its kind, and the sweep still
// drops what is over.
func TestNothingIsSentWhileSwitchedOff(t *testing.T) {
	s, server, phone := pushReady(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.EnablePush = false
	s.p.setConfiguration(cfg)

	s.p.queueTest(sceneLinked, *s.device(sceneLinked, "sess-dave"))
	s.endSession("sess-dave")
	s.p.sweepPushes(context.Background())

	require.Nil(t, s.device(sceneLinked, "sess-dave"))
	phone.nothing(t, 80*time.Millisecond)
	require.Equal(t, 1, server.requests(), "only the hello, from before")
	require.Equal(t, int64(2), s.p.pushes().droppedNow()["switched off"], "the test and the ended push")
}

// An address whose hello never came back is sent no ended push.
func TestAnUnconfirmedAddressGetsNoEndedPush(t *testing.T) {
	s := newPushScene(t)
	server := s.pushServer()
	s.configure(nil)
	s.start()
	s.session("sess-dave", sceneLinked)
	phone := newTestPhone(t, server, "dave")
	require.Equal(t, pushStatePending, s.view(s.call(http.MethodPut, mePushPath, sceneLinked, "sess-dave", phone.registration(s.p.pushes().publicKey()))).State)
	require.Equal(t, pushKindHello, phone.next(t)["type"])

	s.endSession("sess-dave")
	s.p.sweepPushes(context.Background())
	require.Nil(t, s.device(sceneLinked, "sess-dave"))
	phone.nothing(t, 80*time.Millisecond)
}

// All the lanes of a node together hold at most pushQueuedKept jobs.
func TestTheQueueHasABound(t *testing.T) {
	r := newPushRuntime()
	r.running = true
	for i := range pushQueuedKept {
		r.enqueue(&pushJob{kind: pushKindMessage, user: "uid-" + strconv.Itoa(i), session: "sess-" + strconv.Itoa(i/pushLaneKept)}, false)
	}
	r.enqueue(&pushJob{kind: pushKindMessage, user: "uid-new", session: "sess-new"}, false)

	total := 0
	for _, lane := range r.lanes {
		total += len(lane.jobs)
	}
	require.Equal(t, pushQueuedKept, total)
	require.Equal(t, total, r.queued)
	require.Equal(t, int64(1), r.dropped["queue full"])
	require.NotContains(t, r.lanes, "sess-new")
}

// The sweep puts back in the index a person with a row who is missing from
// it, and writes again a row last written 30 days ago whose session stands,
// so that it lives as long as the session.
func TestTheSweepKeepsRegistrations(t *testing.T) {
	s, _, _ := pushReady(t)
	require.NoError(t, s.p.changePushPeople(func(doc *pushPeopleDoc) bool { delete(doc.People, sceneLinked); return true }))

	s.p.sweepPushes(context.Background())
	people, _, err := s.p.readPushPeople()
	require.NoError(t, err)
	require.Contains(t, people.People, sceneLinked)

	writes := s.faults.writesTo(pushUserKey(sceneLinked))
	s.advance(pushRowRenewAfter - time.Hour)
	s.p.sweepPushes(context.Background())
	require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)), "written recently enough")

	s.advance(time.Hour)
	s.p.sweepPushes(context.Background())
	require.Equal(t, writes+1, s.faults.writesTo(pushUserKey(sceneLinked)))
	require.Equal(t, s.clock().UnixMilli(), s.doc(sceneLinked).Written)
}

// A push key that could not be loaded is tried again on the tick until
// there is one.
func TestThePushKeyIsTriedAgainOnTheTick(t *testing.T) {
	s := newPushScene(t)
	r := s.p.pushes()
	r.mu.Lock()
	r.key, r.signer, r.peopleEvery = nil, nil, 5*time.Millisecond
	r.mu.Unlock()
	s.faults.failRead(kvPushKey)
	s.p.loadPushKey()
	s.start()

	time.Sleep(30 * time.Millisecond)
	require.Empty(t, r.publicKey())
	s.faults.healRead(kvPushKey)
	eventually(t, "the key is loaded", func() bool { return r.publicKey() != "" })
}

// aliveElsewhere makes Erin's session, and a request with it, so that this
// node remembers a session that reads besides Dave's.
func aliveElsewhere(s *pushScene) {
	s.session("sess-erin", sceneLetIn)
	s.call(http.MethodGet, mePushPath, sceneLetIn, "sess-erin", nil)
}

// aliveNow is the sessions this node remembers alive, newest first.
func aliveNow(r *pushRuntime) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.alive)
}

// countLog counts a log line with no details, ahead of the scene's own.
func countLog(s *pushScene, level, line string) *atomic.Int64 {
	var n atomic.Int64
	s.api.On(level, line).Run(func(mock.Arguments) { n.Add(1) }).Return()
	calls := s.api.ExpectedCalls
	s.api.ExpectedCalls = append([]*mock.Call{calls[len(calls)-1]}, calls[:len(calls)-1]...)

	return &n
}

const pushUnreadLine = "Sessions could not be read just now, so no phone was taken as signed out"

// A session that cannot be read is taken as over only when another session
// this node saw alive reads meanwhile: Mattermost answers a store that fails
// as it answers a session that is no more.
func TestNoSessionIsTakenAsOverWithoutEvidence(t *testing.T) {
	t.Run("none remembered", func(t *testing.T) {
		s, server, phone := pushReady(t)
		r := s.p.pushes()
		warned := countLog(s, "LogWarn", pushUnreadLine)
		r.mu.Lock()
		r.suspectWait = 20 * time.Millisecond
		r.mu.Unlock()
		require.Equal(t, []string{"sess-dave"}, aliveNow(r), "only the suspect itself")

		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		marked := s.device(sceneLinked, "sess-dave").Suspect
		s.advance(pushSuspectGrace)

		// The look after the grace drops nothing, writes nothing, sends
		// nothing and schedules no other look.
		before := s.readsOfSession("sess-dave")
		eventually(t, "looked at", func() bool { return s.readsOfSession("sess-dave") > before })
		writes := s.faults.writesTo(pushUserKey(sceneLinked))
		time.Sleep(100 * time.Millisecond)
		looked := s.readsOfSession("sess-dave")
		phone.nothing(t, 100*time.Millisecond)
		require.Equal(t, looked, s.readsOfSession("sess-dave"), "no look after it")
		require.Equal(t, writes, s.faults.writesTo(pushUserKey(sceneLinked)))
		require.Equal(t, marked, s.device(sceneLinked, "sess-dave").Suspect, "still suspect")
		require.Zero(t, warned.Load(), "nothing to say when none is remembered")
		require.Equal(t, 1, server.requests(), "the hello only")

		// A post made with Erin's session: the next push drops it.
		s.session("sess-erin", sceneLetIn)
		s.p.MessageHasBeenPosted(&plugin.Context{SessionId: "sess-erin"}, &model.Post{Message: "hi", UserId: sceneLetIn})
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "y"), sceneLinked)
		require.Equal(t, "ended", phone.next(t)["type"])
		eventually(t, "dropped", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
		s.api.AssertCalled(t, "LogInfo", "A phone's push registration was dropped", "why", "its session could not be read while others could", "host", server.host)
	})

	t.Run("the store failing for every session", func(t *testing.T) {
		s, _, phone := pushReady(t)
		r := s.p.pushes()
		warned := countLog(s, "LogWarn", pushUnreadLine)
		aliveElsewhere(s)
		s.failSession("sess-dave", true)
		s.failSession("sess-erin", true)

		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		s.advance(pushSuspectGrace)
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "x"), sceneLinked)
		eventually(t, "warned", func() bool { return warned.Load() == 1 })
		require.NotContains(t, aliveNow(r), "sess-erin", "one that could not be read is forgotten")
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))

		// Once in ten minutes.
		s.call(http.MethodGet, mePushPath, sceneLetIn, "sess-erin", nil)
		s.p.NotificationWillBePushed(message("post-3", "chan-1", "x"), sceneLinked)
		eventually(t, "erin tried again", func() bool { return !slices.Contains(aliveNow(r), "sess-erin") })
		require.Equal(t, int64(1), warned.Load())
		// Ten minutes on, the mark is older than two looks' wait: made
		// anew, and a minute after that the store is tried again.
		s.advance(pushUnreadWarnEvery)
		s.p.NotificationWillBePushed(message("post-4", "chan-1", "x"), sceneLinked)
		eventually(t, "marked anew", func() bool { return s.device(sceneLinked, "sess-dave").Suspect == s.clock().UnixMilli() })
		s.advance(pushSuspectGrace)
		s.call(http.MethodGet, mePushPath, sceneLetIn, "sess-erin", nil)
		s.p.NotificationWillBePushed(message("post-4b", "chan-1", "x"), sceneLinked)
		eventually(t, "warned again", func() bool { return warned.Load() == 2 })
		phone.nothing(t, 50*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))

		// The store answers again: the mark is cleared and pushes go.
		s.failSession("sess-dave", false)
		s.failSession("sess-erin", false)
		s.p.NotificationWillBePushed(message("post-5", "chan-1", "y"), sceneLinked)
		require.Equal(t, "post-5", phone.next(t)["post"])
		eventually(t, "cleared", func() bool { return s.device(sceneLinked, "sess-dave").Suspect == 0 })
	})

	t.Run("gone ones are forgotten, three a look", func(t *testing.T) {
		s, _, phone := pushReady(t)
		r := s.p.pushes()
		s.session("sess-1", sceneLetIn)
		s.session("sess-2", sceneLetIn)
		for _, id := range []string{"sess-1", "sess-2", "sess-3", "sess-4", "sess-5"} {
			r.noteAlive(id)
		}
		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		s.advance(pushSuspectGrace)

		s.p.NotificationWillBePushed(message("post-2", "chan-1", "x"), sceneLinked)
		eventually(t, "three tried", func() bool { return s.readsOfSession("sess-3") == 1 })
		eventually(t, "forgotten", func() bool { return len(aliveNow(r)) == 3 })
		require.Equal(t, []string{"sess-2", "sess-1", "sess-dave"}, aliveNow(r))
		require.Equal(t, 1, s.readsOfSession("sess-5"))
		require.Equal(t, 1, s.readsOfSession("sess-4"))
		require.Zero(t, s.readsOfSession("sess-2"), "at most three a look")
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))

		s.p.NotificationWillBePushed(message("post-3", "chan-1", "x"), sceneLinked)
		require.Equal(t, "ended", phone.next(t)["type"])
		require.Nil(t, s.device(sceneLinked, "sess-dave"))
		require.Equal(t, 1, s.readsOfSession("sess-2"))
		require.Zero(t, s.readsOfSession("sess-1"))
	})

	t.Run("a mark left from an outage", func(t *testing.T) {
		s, server, phone := pushReady(t)
		r := s.p.pushes()
		aliveElsewhere(s)
		r.mu.Lock()
		r.suspectWait = 20 * time.Millisecond
		r.mu.Unlock()
		suspect := func() int64 {
			if device := s.device(sceneLinked, "sess-dave"); device != nil {
				return device.Suspect
			}
			return -1
		}

		// No session can be read: the mark stays, and nothing is dropped.
		s.failSession("sess-dave", true)
		s.failSession("sess-erin", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return suspect() > 0 })
		s.advance(pushSuspectGrace)
		eventually(t, "Erin tried and forgotten", func() bool { return !slices.Contains(aliveNow(r), "sess-erin") })

		// Half an hour on, Erin's session reads again and Dave's still does
		// not: the old mark is made anew and looked at again, and nothing
		// is dropped yet.
		s.advance(30 * time.Minute)
		s.failSession("sess-erin", false)
		s.call(http.MethodGet, mePushPath, sceneLetIn, "sess-erin", nil)
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "x"), sceneLinked)
		eventually(t, "marked anew", func() bool { return suspect() == s.clock().UnixMilli() })
		reads := s.readsOfSession("sess-dave")
		eventually(t, "looked at again", func() bool { return s.readsOfSession("sess-dave") > reads })
		phone.nothing(t, 50*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))

		// A minute on, at its look, still unreadable while Erin's reads.
		s.advance(pushSuspectGrace)
		require.Equal(t, "ended", phone.next(t)["type"])
		eventually(t, "dropped", func() bool { return s.device(sceneLinked, "sess-dave") == nil })
		require.Equal(t, 2, server.requests(), "the hello and the ended push")
	})

	t.Run("readable when read again", func(t *testing.T) {
		s, _, phone := pushReady(t)
		s.session("sess-erin", sceneLetIn)
		s.p.pushes().noteAlive("sess-erin")
		s.lock.Lock()
		erin := *s.sessions["sess-erin"]
		s.lock.Unlock()
		s.api.On("GetSession", "sess-erin").Run(func(mock.Arguments) { s.failSession("sess-dave", false) }).Return(&erin, nil)
		calls := s.api.ExpectedCalls
		s.api.ExpectedCalls = append([]*mock.Call{calls[len(calls)-1]}, calls[:len(calls)-1]...)

		s.failSession("sess-dave", true)
		s.p.NotificationWillBePushed(message("post-1", "chan-1", "x"), sceneLinked)
		eventually(t, "suspect", func() bool { return s.device(sceneLinked, "sess-dave").Suspect != 0 })
		s.advance(pushSuspectGrace)
		s.p.NotificationWillBePushed(message("post-2", "chan-1", "x"), sceneLinked)
		require.Equal(t, "post-2", phone.next(t)["post"], "a session that stands gets its push")
		eventually(t, "cleared", func() bool { return s.device(sceneLinked, "sess-dave").Suspect == 0 })
		phone.nothing(t, 50*time.Millisecond)
		require.NotNil(t, s.device(sceneLinked, "sess-dave"))
	})

	t.Run("the sweep", func(t *testing.T) {
		s, _, phone := pushReady(t)
		_, err := s.p.updatePushDoc(sceneLinked, func(doc *pushDoc, now time.Time) bool {
			doc.Devices[0].Suspect = now.Add(-pushSuspectGrace).UnixMilli()
			return true
		})
		require.NoError(t, err)
		s.failSession("sess-dave", true)

		s.p.sweepPushes(context.Background())
		require.NotNil(t, s.device(sceneLinked, "sess-dave"), "none remembered")
		phone.nothing(t, 50*time.Millisecond)

		aliveElsewhere(s)
		s.p.sweepPushes(context.Background())
		require.Equal(t, "ended", phone.next(t)["type"])
		require.Nil(t, s.device(sceneLinked, "sess-dave"))
	})
}

// The sessions seen alive: at most sixteen, each once, newest first, noted
// from a request with a session, a post made with one, and a read of a
// device's session that stood.
func TestTheSessionsSeenAliveAreRemembered(t *testing.T) {
	r := newPushRuntime()
	r.noteAlive("a")
	r.noteAlive("b")
	r.noteAlive("a")
	r.noteAlive("")
	require.Equal(t, []string{"a", "b"}, aliveNow(r))
	for i := range 20 {
		r.noteAlive("s" + strconv.Itoa(i))
	}
	r.noteAlive("s10")
	alive := aliveNow(r)
	require.Len(t, alive, pushAliveKept)
	require.Equal(t, []string{"s10", "s19", "s18", "s17", "s16", "s15", "s14", "s13", "s12", "s11", "s9", "s8", "s7", "s6", "s5", "s4"}, alive)
	require.Equal(t, []string{"s10", "s18", "s17"}, r.aliveBut("s19", pushAliveTried))
	r.forgetAlive("s18")
	require.NotContains(t, aliveNow(r), "s18")

	s := newPushScene(t)
	q := s.p.pushes()
	s.call(http.MethodGet, "/api/v1/me", sceneLinked, "", nil)
	require.Empty(t, aliveNow(q), "no session, nothing noted")

	s.call(http.MethodGet, "/api/v1/me", sceneLinked, "sess-request", nil)
	require.Equal(t, []string{"sess-request"}, aliveNow(q))

	s.p.MessageHasBeenPosted(&plugin.Context{SessionId: "sess-post"}, &model.Post{Message: "hi", UserId: sceneLinked})
	s.p.MessageHasBeenPosted(&plugin.Context{}, &model.Post{Message: "by a plugin", UserId: sceneLinked})
	require.Equal(t, []string{"sess-post", "sess-request"}, aliveNow(q))

	s.session("sess-read", sceneLinked)
	require.True(t, s.p.checkPushSession(sceneLinked, "sess-read", false))
	require.False(t, s.p.checkPushSession(sceneLinked, "sess-none", false))
	s.session("sess-over", sceneLinked)
	s.endSession("sess-over")
	require.False(t, s.p.checkPushSession(sceneLinked, "sess-over", false))
	require.Equal(t, []string{"sess-read", "sess-post", "sess-request"}, aliveNow(q))
}
