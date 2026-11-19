package pushsend

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// MaxEndpointBytes is the longest push address a phone may register, as
// UnifiedPush bounds it.
const MaxEndpointBytes = 1000

// Why a push address is refused. None of them quotes the address: it is a
// capability, which anyone holding it can push to.
var (
	ErrEndpointTooLong    = errors.New("the push address is longer than 1000 bytes")
	ErrEndpointCharacters = errors.New("the push address has a character that is not printable ASCII")
	ErrEndpointNotURL     = errors.New("the push address is not an absolute web address with a host")
	ErrEndpointScheme     = errors.New("the push address does not start with https://")
	ErrEndpointPlainHTTP  = errors.New("the push address uses http://, which only a push server on this server's list may")
	ErrEndpointUserinfo   = errors.New("the push address carries a user name")
	ErrEndpointFragment   = errors.New("the push address carries a fragment")
	ErrEndpointNotListed  = errors.New("the push address is not at a push server on this server's list, and only those are allowed")
	ErrEndpointAddress    = errors.New("the push address is an internet address that this server does not send to")
)

// Rules are what the admin set: the list of push servers on their own
// network, and whether only those may be used.
type Rules struct {
	Listed     HostList
	OnlyListed bool
}

// Endpoint is a push address that passed ValidateEndpoint.
type Endpoint struct {
	// URL is the address exactly as given.
	URL string

	// Scheme is "https" or "http".
	Scheme string

	// Host is the push server's host in canonical form, with its port
	// when that is not the scheme's own: what logs and the admin's card
	// name it by.
	Host string

	// Listed says whether the push server is on the admin's list.
	Listed bool

	// literal says whether the host is written as an IP address rather
	// than a name.
	literal bool
}

// ValidateEndpoint checks a push address a phone registers, and again
// before every send: at most 1000 bytes of printable ASCII, an absolute
// URL with a host, https (or http only for a listed push server), no user
// name and no fragment, a listed push server when only those are allowed,
// and, when the host is written as an IP address, one the dial would allow.
// A host given by name is checked when it is dialled, address by address.
func ValidateEndpoint(raw string, rules Rules) (Endpoint, error) {
	if len(raw) > MaxEndpointBytes {
		return Endpoint{}, ErrEndpointTooLong
	}

	if raw == "" {
		return Endpoint{}, ErrEndpointNotURL
	}

	for i := range len(raw) {
		if raw[i] <= ' ' || raw[i] >= 0x7f {
			return Endpoint{}, ErrEndpointCharacters
		}
	}

	if strings.Contains(raw, "#") {
		return Endpoint{}, ErrEndpointFragment
	}

	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return Endpoint{}, ErrEndpointNotURL
	}

	if u.User != nil {
		return Endpoint{}, ErrEndpointUserinfo
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return Endpoint{}, ErrEndpointScheme
	}

	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return Endpoint{}, ErrEndpointNotURL
	}

	port, err := canonicalPort(u.Port())
	if err != nil {
		return Endpoint{}, ErrEndpointNotURL
	}

	entry, listed := rules.Listed.match(u.Scheme, host, port)

	if u.Scheme == "http" && !listed {
		return Endpoint{}, ErrEndpointPlainHTTP
	}

	if rules.OnlyListed && !listed {
		return Endpoint{}, ErrEndpointNotListed
	}

	addr, err := netip.ParseAddr(host)
	literal := err == nil
	if literal && !addressAllowed(addr, listed, listed && entry.loopback) {
		return Endpoint{}, ErrEndpointAddress
	}

	return Endpoint{URL: raw, Scheme: u.Scheme, Host: hostForm(host, port, u.Scheme), Listed: listed, literal: literal}, nil
}

// hostForm is host[:port] as Endpoint.Host gives it.
func hostForm(host, port, scheme string) string {
	entry := hostEntry{host: host, port: port}
	if port == defaultPort(scheme) {
		entry.port = ""
	}

	return entry.String()
}

// HostOf is the push server's host as Endpoint.Host gives it, for an
// address that may not pass ValidateEndpoint (any more): "" when it has no
// readable host.
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}

	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return ""
	}

	port, err := canonicalPort(u.Port())
	if err != nil {
		return ""
	}

	return hostForm(host, port, u.Scheme)
}
