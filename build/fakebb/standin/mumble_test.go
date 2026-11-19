package standin

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumble/MumbleProto"
	"layeh.com/gumble/gumbleutil"
)

// mumbleServer is the stand-in on a port of its own.
func mumbleServer(t *testing.T, people ...string) (*Mumble, string) {
	t.Helper()

	server, err := NewMumble(people...)
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() { _ = server.Serve(listener) }()

	return server, listener.Addr().String()
}

// mumbleClient is a client as VillaNET makes one: gumble, taking the server's
// certificate on trust. changed is told of every change to somebody.
func mumbleDial(t *testing.T, address, name string, listener gumbleutil.Listener) *gumble.Client {
	t.Helper()

	config := gumble.NewConfig()
	config.Username = name
	config.Attach(listener)

	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, address, config, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // a certificate nobody vouches for, as Mumble's are
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect() })

	return client
}

// rosterOf is everybody a client has been told about but itself: name to
// whether they are muted by any of Mumble's five ways.
func rosterOf(client *gumble.Client) map[string]bool {
	roster := map[string]bool{}
	client.Do(func() {
		for _, user := range client.Users {
			if user != client.Self {
				roster[user.Name] = user.Muted || user.SelfMuted || user.Deafened || user.SelfDeafened || user.Suppressed
			}
		}
	})

	return roster
}

func TestMumbleTellsAClientWhoIsOn(t *testing.T) {
	_, address := mumbleServer(t, "bob", "carol=muted", "dave=deafened")

	client := mumbleDial(t, address, "VillaNET", gumbleutil.Listener{})

	require.Equal(t, map[string]bool{"bob": false, "carol": true, "dave": true}, rosterOf(client))

	client.Do(func() {
		require.NotNil(t, client.Self)
		require.Equal(t, "VillaNET", client.Self.Name)
		require.Equal(t, "Root", client.Self.Channel.Name, "parked where the server drops it")

		for _, user := range client.Users {
			if user != client.Self {
				require.Equal(t, "Voice", user.Channel.Name)
			}
		}
	})
}

func TestMumbleTellsAClientWhatChanges(t *testing.T) {
	server, address := mumbleServer(t, "bob")

	changed := make(chan struct{}, 16)
	client := mumbleDial(t, address, "VillaNET", gumbleutil.Listener{
		UserChange: func(*gumble.UserChangeEvent) { changed <- struct{}{} },
	})

	wait := func() {
		t.Helper()

		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatal("the client was not told")
		}
	}

	server.Join("carol", nil, nil)
	wait()
	require.Equal(t, map[string]bool{"bob": false, "carol": false}, rosterOf(client))

	server.Join("carol", proto.Bool(true), nil)
	wait()
	require.Equal(t, map[string]bool{"bob": false, "carol": true}, rosterOf(client))

	// Joining again changes nothing about how somebody is in voice.
	server.Join("carol", nil, nil)
	wait()
	require.Equal(t, map[string]bool{"bob": false, "carol": true}, rosterOf(client))

	server.Join("carol", proto.Bool(false), nil)
	wait()
	require.Equal(t, map[string]bool{"bob": false, "carol": false}, rosterOf(client))

	server.Leave("bob")
	wait()
	require.Equal(t, map[string]bool{"carol": false}, rosterOf(client))
}

func TestMumbleKeepsWhatARealConnectionSends(t *testing.T) {
	server, address := mumbleServer(t, "bob")

	heard := make(chan string, 1)
	client := mumbleDial(t, address, "VillaNET", gumbleutil.Listener{
		TextMessage: func(event *gumble.TextMessageEvent) { heard <- event.Sender.Name + ": " + event.Message },
	})

	require.Equal(t, []string{"VillaNET"}, server.Connected())

	// The chat bridge, both ways.
	client.Do(func() {
		client.Send(&gumble.TextMessage{Channels: []*gumble.Channel{client.Channels[0]}, Message: "from Mattermost"})
	})
	require.Eventually(t, func() bool { return len(server.Said()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"from Mattermost"}, server.Said())

	require.True(t, server.Say("bob", "from Mumble"))
	require.False(t, server.Say("nobody", "from nowhere"))
	select {
	case line := <-heard:
		require.Equal(t, "bob: from Mumble", line)
	case <-time.After(5 * time.Second):
		t.Fatal("the client was not sent the message")
	}

	// A sound played from the soundboard: voice packets over the same connection.
	for sequence := range 3 {
		require.NoError(t, client.Conn.WriteAudio(4, 1, int64(sequence), sequence == 2, []byte{0xf8, 0xff, 0xfe}, nil, nil, nil))
	}
	require.Eventually(t, func() bool { return server.Heard() == 3 }, 5*time.Second, 10*time.Millisecond)

	// What a channel lets an ordinary user do, when asked.
	permission := make(chan gumble.Permission, 1)
	client.Config.Attach(gumbleutil.Listener{
		ChannelChange: func(event *gumble.ChannelChangeEvent) {
			if event.Type.Has(gumble.ChannelChangePermission) {
				if held := event.Channel.Permission(); held != nil {
					permission <- *held
				}
			}
		},
	})
	client.Do(func() { client.Channels[1].RequestPermission() })
	select {
	case held := <-permission:
		require.True(t, held.Has(gumble.PermissionWhisper))
		require.True(t, held.Has(gumble.PermissionSpeak))
		require.False(t, held.Has(gumble.PermissionWrite))
	case <-time.After(5 * time.Second):
		t.Fatal("no answer about the channel's permissions")
	}

	require.NoError(t, client.Disconnect())
	require.Eventually(t, func() bool { return len(server.Connected()) == 0 }, 5*time.Second, 10*time.Millisecond)
}

// A client drops a server it has heard nothing from for twenty seconds, so
// the stand-in has to answer pings, and in the order Mumble does: the channels
// before anybody in them, and the client's own entry before it is told it is
// in step. This reads the packets as they come.
func TestMumbleSpeaksInMumblesOrderAndAnswersPings(t *testing.T) {
	_, address := mumbleServer(t, "bob")

	raw, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // as above
	require.NoError(t, err)
	defer raw.Close()

	conn := gumble.NewConn(raw)
	conn.Timeout = 5 * time.Second
	require.NoError(t, conn.WriteProto(&MumbleProto.Version{Version: proto.Uint32(1<<16 | 3<<8)}))
	require.NoError(t, conn.WriteProto(&MumbleProto.Authenticate{Username: proto.String("VillaNET"), Opus: proto.Bool(true)}))

	var kinds []uint16
	var own uint32
	channels, users := map[uint32]bool{}, map[uint32]string{}
	for own == 0 {
		kind, data, readErr := conn.ReadPacket()
		require.NoError(t, readErr)
		kinds = append(kinds, kind)

		switch kind {
		case 7:
			var channel MumbleProto.ChannelState
			require.NoError(t, proto.Unmarshal(data, &channel))
			if channel.Parent != nil {
				require.True(t, channels[channel.GetParent()], "a channel's parent comes first")
			}
			channels[channel.GetChannelId()] = true
		case 9:
			var user MumbleProto.UserState
			require.NoError(t, proto.Unmarshal(data, &user))
			require.True(t, channels[user.GetChannelId()], "somebody's channel comes before them")
			users[user.GetSession()] = user.GetName()
		case 5:
			var sync MumbleProto.ServerSync
			require.NoError(t, proto.Unmarshal(data, &sync))
			own = sync.GetSession()
			require.Equal(t, "VillaNET", users[own], "a client's own entry comes before it is told it is in step")
			require.EqualValues(t, mumbleBandwidth, sync.GetMaxBandwidth())
		}
	}
	require.EqualValues(t, 0, kinds[0], "the server's version first")

	names := []string{}
	for _, name := range users {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, []string{"VillaNET", "bob"}, names)

	require.NoError(t, conn.WriteProto(&MumbleProto.Ping{Timestamp: proto.Uint64(424242)}))
	for {
		kind, data, readErr := conn.ReadPacket()
		require.NoError(t, readErr)
		if kind != mumblePing {
			continue
		}

		var pong MumbleProto.Ping
		require.NoError(t, proto.Unmarshal(data, &pong))
		require.EqualValues(t, 424242, pong.GetTimestamp())

		break
	}
}

func TestMumbleIsSteeredOverHTTP(t *testing.T) {
	server, address := mumbleServer(t, "bob")
	client := mumbleDial(t, address, "VillaNET", gumbleutil.Listener{})

	mux := http.NewServeMux()
	server.Routes(mux)
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)

	type answer struct {
		People    []VoicePerson `json:"people"`
		Connected []string      `json:"connected"`
		Heard     int           `json:"heard"`
		Said      []string      `json:"said"`
	}
	call := func(method, query string) (int, answer) {
		t.Helper()

		request, err := http.NewRequest(method, web.URL+"/fake/voice?"+query, nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()

		var said answer
		if response.StatusCode == http.StatusOK {
			require.NoError(t, json.NewDecoder(response.Body).Decode(&said))
		}

		return response.StatusCode, said
	}

	status, said := call(http.MethodPost, "name=carol&muted=true")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []VoicePerson{{Name: "bob"}, {Name: "carol", Muted: true}}, said.People)
	require.Equal(t, []string{"VillaNET"}, said.Connected)
	require.Eventually(t, func() bool { return len(rosterOf(client)) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, map[string]bool{"bob": false, "carol": true}, rosterOf(client))

	// Naming somebody without saying how leaves them as they are.
	_, said = call(http.MethodPost, "name=carol")
	require.Equal(t, []VoicePerson{{Name: "bob"}, {Name: "carol", Muted: true}}, said.People)

	_, said = call(http.MethodPost, "name=carol&deafened=true&muted=false")
	require.Equal(t, []VoicePerson{{Name: "bob"}, {Name: "carol", Deafened: true}}, said.People)
	require.Eventually(t, func() bool { return rosterOf(client)["carol"] }, 5*time.Second, 10*time.Millisecond, "deafened is muted too")

	_, said = call(http.MethodPost, "name=bob&on=false")
	require.Equal(t, []VoicePerson{{Name: "carol", Deafened: true}}, said.People)
	require.Eventually(t, func() bool { return len(rosterOf(client)) == 1 }, 5*time.Second, 10*time.Millisecond)

	// A GET changes nothing.
	status, said = call(http.MethodGet, "name=dave")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, said.People, 1)

	status, _ = call(http.MethodPost, "muted=true")
	require.Equal(t, http.StatusBadRequest, status)
}
