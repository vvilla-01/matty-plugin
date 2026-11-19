package pushsend

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// MaxListedHosts is the most entries a list keeps; a line past it is a bad
// line.
const MaxListedHosts = 100

// ErrHostLine is a line of the push server list that is not a host or a
// host:port.
var ErrHostLine = errors.New("not a host name, an address, or either with :port")

// HostList is the admin's list of push servers on their own network
// ("Push servers on your own network"): hosts, each with or without a port,
// in one canonical form. Its zero value lists nothing.
type HostList struct {
	entries []hostEntry
}

type hostEntry struct {
	host     string // canonical, an IPv6 address without its brackets
	port     string // "" for the scheme's own port
	loopback bool   // the entry itself names this machine
}

// ParseHostList reads the setting, one host or host:port a line. Blank
// lines are skipped. A line that is not one, or past MaxListedHosts, is left
// out, and its number (from 1) is in bad, so that the caller can say which.
// A line listed twice is kept once.
func ParseHostList(text string) (list HostList, bad []int) {
	seen := map[string]bool{}

	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := parseHostEntry(line)
		if err != nil || len(list.entries) >= MaxListedHosts {
			bad = append(bad, i+1)

			continue
		}

		if form := entry.String(); !seen[form] {
			seen[form] = true
			list.entries = append(list.entries, entry)
		}
	}

	return list, bad
}

// NormalizeHost is one line of the list in its canonical form: lower case,
// no trailing dot, a name that is not ASCII in its xn-- form, an IPv6
// address in brackets in its shortest form (an IPv4-mapped one as the IPv4
// address), and a port without leading zeros.
func NormalizeHost(line string) (string, error) {
	entry, err := parseHostEntry(strings.TrimSpace(line))
	if err != nil {
		return "", err
	}

	return entry.String(), nil
}

// String is the list as the setting holds it, one canonical entry a line.
func (l HostList) String() string {
	return strings.Join(l.Lines(), "\n")
}

// Lines is the list's entries in canonical form.
func (l HostList) Lines() []string {
	lines := make([]string, 0, len(l.entries))
	for _, entry := range l.entries {
		lines = append(lines, entry.String())
	}

	return lines
}

// Len is how many entries the list has.
func (l HostList) Len() int {
	return len(l.entries)
}

// Lists says whether a push to scheme://host[:port] goes to a listed push
// server. An entry without a port matches only the scheme's own port
// (443 for https, 80 for http); one with a port, only that port.
func (l HostList) Lists(scheme, host, port string) bool {
	_, ok := l.match(scheme, host, port)

	return ok
}

// match is the entry that lists scheme://host:port, if one does. The host
// and port are put in canonical form first, so that the URL's spelling and
// the dial's agree.
func (l HostList) match(scheme, host, port string) (hostEntry, bool) {
	if len(l.entries) == 0 {
		return hostEntry{}, false
	}

	host, err := canonicalHost(host)
	if err != nil {
		return hostEntry{}, false
	}

	port, err = canonicalPort(port)
	if err != nil {
		return hostEntry{}, false
	}

	own := defaultPort(scheme)
	if port == "" {
		port = own
	}

	if port == "" {
		return hostEntry{}, false
	}

	for _, entry := range l.entries {
		if entry.host != host {
			continue
		}

		if (entry.port == "" && port == own) || entry.port == port {
			return entry, true
		}
	}

	return hostEntry{}, false
}

func (e hostEntry) String() string {
	host := e.host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	if e.port == "" {
		return host
	}

	return host + ":" + e.port
}

func defaultPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}

	return ""
}

// parseHostEntry reads "host", "host:port", "[v6]", "[v6]:port" or a bare
// IPv6 address. A scheme, a path, a user name, a query or a fragment makes
// it no entry.
func parseHostEntry(line string) (hostEntry, error) {
	if line == "" || strings.ContainsAny(line, "/\\@?#% \t") {
		return hostEntry{}, ErrHostLine
	}

	var host, port string

	switch {
	case strings.HasPrefix(line, "["):
		end := strings.IndexByte(line, ']')
		if end < 0 {
			return hostEntry{}, ErrHostLine
		}

		host = line[1:end]
		if !strings.Contains(host, ":") {
			return hostEntry{}, ErrHostLine
		}

		rest := line[end+1:]
		if rest != "" {
			var ok bool
			if port, ok = strings.CutPrefix(rest, ":"); !ok || port == "" {
				return hostEntry{}, ErrHostLine
			}
		}
	case strings.Count(line, ":") > 1:
		host = line
	case strings.Contains(line, ":"):
		host, port, _ = strings.Cut(line, ":")
		if port == "" {
			return hostEntry{}, ErrHostLine
		}
	default:
		host = line
	}

	host, err := canonicalHost(host)
	if err != nil {
		return hostEntry{}, err
	}

	port, err = canonicalPort(port)
	if err != nil {
		return hostEntry{}, err
	}

	return hostEntry{host: host, port: port, loopback: isLoopbackHost(host)}, nil
}

// canonicalHost is a host as this package compares it: an IP address in
// netip's form (IPv4-mapped IPv6 as IPv4), or a DNS name in lower-case
// ASCII without a trailing dot, its labels letters, digits, '-' and '_'.
func canonicalHost(host string) (string, error) {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" {
		return "", ErrHostLine
	}

	if strings.Contains(host, ":") {
		addr, err := netip.ParseAddr(host)
		if err != nil || addr.Zone() != "" || !addr.Is6() {
			return "", ErrHostLine
		}

		return addr.Unmap().String(), nil
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}

	host = strings.TrimSuffix(host, ".")
	if !isASCII(host) {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return "", ErrHostLine
		}

		host = ascii
	}

	host = strings.ToLower(host)
	if host == "" || len(host) > 253 {
		return "", ErrHostLine
	}

	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrHostLine
		}

		for i := range len(label) {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return "", ErrHostLine
			}
		}
	}

	// A name that a resolver would take as an address in another form
	// (such as 127.1 or 0x7f.0.0.1) is no name.
	if ip := net.ParseIP(host); ip != nil || looksNumeric(host) {
		return "", ErrHostLine
	}

	return host, nil
}

// looksNumeric is a name whose last label is all digits or hex, which no
// real host name has and which some resolvers read as an IPv4 address.
func looksNumeric(host string) bool {
	last := host[strings.LastIndexByte(host, '.')+1:]
	if strings.HasPrefix(last, "0x") {
		return true
	}

	for i := range len(last) {
		if last[i] < '0' || last[i] > '9' {
			return false
		}
	}

	return true
}

func canonicalPort(port string) (string, error) {
	if port == "" {
		return "", nil
	}

	if len(port) > 5 {
		return "", ErrHostLine
	}

	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return "", ErrHostLine
		}
	}

	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", ErrHostLine
	}

	return strconv.Itoa(number), nil
}

// LoopbackName says whether a push address's host is a name for this
// machine itself (localhost, or a name under .localhost) that is not on the
// list: a registration refuses it as it refuses a loopback address written
// as one, and the dial refuses it by the addresses it resolves to.
func LoopbackName(raw string, rules Rules) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}

	host, err := canonicalHost(u.Hostname())
	if err != nil || !isLoopbackHost(host) {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}

	_, listed := rules.Listed.match(u.Scheme, host, u.Port())

	return !listed
}

// isLoopbackHost is a host that names this machine itself: localhost, a
// name under .localhost, or a loopback address.
func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}

	addr, err := netip.ParseAddr(host)

	return err == nil && addr.IsLoopback()
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}

	return true
}
