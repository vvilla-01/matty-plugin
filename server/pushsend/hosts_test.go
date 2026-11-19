package pushsend

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheListIsWrittenInOneForm(t *testing.T) {
	for line, want := range map[string]string{
		"push.lan":                      "push.lan",
		"  Push.LAN.  ":                 "push.lan",
		"push.lan:8443":                 "push.lan:8443",
		"push.lan:08443":                "push.lan:8443",
		"localhost:7788":                "localhost:7788",
		"münchen.example":               "xn--mnchen-3ya.example",
		"MÜNCHEN.example:443":           "xn--mnchen-3ya.example:443",
		"192.168.1.20":                  "192.168.1.20",
		"192.168.1.20:8080":             "192.168.1.20:8080",
		"[2001:DB8:0:0::1]":             "[2001:db8::1]",
		"2001:db8:0::1":                 "[2001:db8::1]",
		"[2001:db8::1]:8443":            "[2001:db8::1]:8443",
		"[::ffff:192.168.1.20]:80":      "192.168.1.20:80",
		"[fd12:3456:789a:1::0000:0001]": "[fd12:3456:789a:1::1]",
		"under_score.lan":               "under_score.lan",
	} {
		got, err := NormalizeHost(line)
		require.NoError(t, err, line)
		require.Equal(t, want, got, line)
	}
}

func TestALineThatIsNotAHostIsRefused(t *testing.T) {
	for _, line := range []string{
		"",
		"https://push.lan",
		"push.lan/up",
		"user@push.lan",
		"push.lan?x",
		"push.lan#x",
		"push.lan:",
		"push.lan:0",
		"push.lan:65536",
		"push.lan:http",
		"push.lan:443:1",
		"[2001:db8::1",
		"[2001:db8::1]x",
		"[2001:db8::1]:",
		"[192.168.1.20]",
		"[fe80::1%eth0]",
		"push..lan",
		"-",
		"push lan",
		"push.lan\\x",
		"127.1",
		"0x7f.0.0.1",
		"10.0.0.010",
		strings.Repeat("a", 64) + ".lan",
		strings.Repeat("abc.", 64) + "lan",
		"%70ush.lan",
	} {
		_, err := NormalizeHost(line)
		require.ErrorIs(t, err, ErrHostLine, "%q", line)
	}
}

func TestParseHostListSaysWhichLinesItLeftOut(t *testing.T) {
	list, bad := ParseHostList("push.lan\n\nhttps://nope\n  PUSH.lan  \r\nlocalhost:7788\nuser@x\n[::1]")

	require.Equal(t, []string{"push.lan", "localhost:7788", "[::1]"}, list.Lines())
	require.Equal(t, "push.lan\nlocalhost:7788\n[::1]", list.String())
	require.Equal(t, 3, list.Len())
	require.Equal(t, []int{3, 6}, bad)

	var long strings.Builder
	for i := range MaxListedHosts + 2 {
		long.WriteString("h" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + ".lan" + ":" + strconv.Itoa(1000+i) + "\n")
	}

	list, bad = ParseHostList(long.String())
	require.Equal(t, MaxListedHosts, list.Len())
	require.Equal(t, []int{MaxListedHosts + 1, MaxListedHosts + 2}, bad)

	empty, bad := ParseHostList("")
	require.Equal(t, 0, empty.Len())
	require.Empty(t, bad)
	require.False(t, empty.Lists("https", "push.lan", ""))
}

func TestAnEntryWithoutAPortListsOnlyTheSchemesOwnPort(t *testing.T) {
	list, bad := ParseHostList("push.lan\nother.lan:8443\n[2001:db8::1]\nlocalhost:7788")
	require.Empty(t, bad)

	for _, c := range []struct {
		scheme, host, port string
		want               bool
	}{
		{"https", "push.lan", "", true},
		{"https", "push.lan", "443", true},
		{"https", "PUSH.lan.", "443", true},
		{"https", "push.lan", "8443", false},
		{"http", "push.lan", "", true},
		{"http", "push.lan", "80", true},
		{"http", "push.lan", "443", false},
		{"https", "push.lan", "80", false},
		{"https", "other.lan", "8443", true},
		{"http", "other.lan", "8443", true},
		{"https", "other.lan", "", false},
		{"https", "2001:db8::1", "443", true},
		{"https", "[2001:db8:0::1]", "", true},
		{"https", "2001:db8::1", "8443", false},
		{"http", "localhost", "7788", true},
		{"http", "127.0.0.1", "7788", false},
		{"https", "sub.push.lan", "", false},
		{"", "push.lan", "443", false},
		{"ftp", "push.lan", "", false},
	} {
		require.Equal(t, c.want, list.Lists(c.scheme, c.host, c.port), "%+v", c)
	}
}

func TestAnEntryKnowsWhetherItNamesThisMachine(t *testing.T) {
	for line, want := range map[string]bool{
		"localhost":          true,
		"localhost:7788":     true,
		"push.localhost":     true,
		"127.0.0.1":          true,
		"127.8.9.10:80":      true,
		"[::1]":              true,
		"[::ffff:127.0.0.1]": true,
		"push.lan":           false,
		"localhost.lan":      false,
		"192.168.1.20":       false,
	} {
		entry, err := parseHostEntry(line)
		require.NoError(t, err, line)
		require.Equal(t, want, entry.loopback, line)
	}
}

// A name for this machine is refused at registration unless it is listed:
// a loopback address written as one is refused there already, and any other
// name only at the dial.
func TestANameForThisMachineIsALoopbackName(t *testing.T) {
	unlisted := Rules{}
	listed, bad := ParseHostList("localhost:7788\npush.localhost")
	require.Empty(t, bad)
	withList := Rules{Listed: listed}

	for raw, want := range map[string]bool{
		"https://localhost/x":              true,
		"https://LOCALHOST./x":             true,
		"https://localhost:8443/x":         true,
		"http://localhost:7788/x":          true,
		"https://push.localhost/x":         true,
		"https://a.b.localhost:9/x":        true,
		"https://localhost.lan/x":          false,
		"https://ntfy.sh/x":                false,
		"https://127.0.0.1/x":              false,
		"https://[::1]/x":                  false,
		"not a url\x7f":                    false,
		"https://bad..localhost/x":         false,
		"https://mylocalhost/x":            false,
		"https://push.localhost.example/x": false,
	} {
		require.Equal(t, want, LoopbackName(raw, unlisted), raw)
	}

	for raw, want := range map[string]bool{
		"http://localhost:7788/x":    false,
		"https://localhost:7788/x":   false,
		"https://localhost/x":        true,
		"https://push.localhost/x":   false,
		"http://push.localhost/x":    false,
		"https://push.localhost:9/x": true,
		"https://a.localhost/x":      true,
	} {
		require.Equal(t, want, LoopbackName(raw, withList), raw)
	}
}
