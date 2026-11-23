package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The one port serves Broadcast Box, the stand-in Steam and the stand-in push
// server, each at its own addresses.
func TestTheOnePortServesEveryStandIn(t *testing.T) {
	b := &box{streams: map[string]*stream{}, sessions: map[string]*session{}, inH264: map[string]bool{}}
	server := httptest.NewServer(b.routes([]string{"alice"}, ""))
	defer server.Close()

	for _, address := range []string{"/api/status", "/fake/steam", "/fake/push"} {
		response, err := http.Get(server.URL + address)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s: %d", address, response.StatusCode)
		}
	}

	// A message to an endpoint reaches the push server, which wants it encrypted.
	response, err := http.Post(server.URL+"/push/abc", "application/octet-stream", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /push/abc without Content-Encoding: %d", response.StatusCode)
	}
}

// A token is a stream's own name, as the plugin sends it (in base64) or as it
// is typed into curl, for a stream there is already and for one there is not.
func TestATokenIsItsStreamsName(t *testing.T) {
	for _, name := range []string{"victor", "alice", "bob", "carol", "a", "stream_2.b-c", "Ünïcode"} {
		if got := keyUnder(base64.StdEncoding.EncodeToString([]byte(name))); got != name {
			t.Errorf("%q in base64 is the stream %q", name, got)
		}
		if keyUnder(name) != name {
			t.Errorf("%q as it is typed is the stream %q", name, keyUnder(name))
		}
	}

	// What is not base64 of a name is its own name: a real token's shape, and
	// base64 of something no stream is called.
	for _, token := range []string{
		"11111111-1111-4111-8111-111111111111",
		base64.StdEncoding.EncodeToString([]byte("not a name!")),
		base64.StdEncoding.EncodeToString([]byte{0xff, 0x00}),
		"",
	} {
		if got := keyUnder(token); got != token {
			t.Errorf("%q is the stream %q", token, got)
		}
	}
}
