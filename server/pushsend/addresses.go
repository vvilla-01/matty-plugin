package pushsend

import "net/netip"

// namedPrefix is a range of addresses and the registry entry it comes
// from, so that a test can name what it is holding.
type namedPrefix struct {
	prefix netip.Prefix
	name   string
}

func prefix(text, name string) namedPrefix {
	return namedPrefix{prefix: netip.MustParsePrefix(text), name: name}
}

// notGlobal4 is every IPv4 range a push server that is not listed may not
// be at: IANA's IPv4 Special-Purpose Address Registry's entries that are
// not globally reachable, the ranges it marks reachable but that are no
// place for a push server (192.0.0.0/24 whole, the old 6to4 relays), and
// multicast and the reserved block.
var notGlobal4 = []namedPrefix{
	prefix("0.0.0.0/8", "this network"),
	prefix("10.0.0.0/8", "private use (RFC 1918)"),
	prefix("100.64.0.0/10", "shared address space (carrier-grade NAT)"),
	prefix("127.0.0.0/8", "loopback"),
	prefix("169.254.0.0/16", "link local"),
	prefix("172.16.0.0/12", "private use (RFC 1918)"),
	prefix("192.0.0.0/24", "IETF protocol assignments"),
	prefix("192.0.2.0/24", "documentation (TEST-NET-1)"),
	prefix("192.88.99.0/24", "deprecated 6to4 relay anycast"),
	prefix("192.168.0.0/16", "private use (RFC 1918)"),
	prefix("198.18.0.0/15", "benchmarking"),
	prefix("198.51.100.0/24", "documentation (TEST-NET-2)"),
	prefix("203.0.113.0/24", "documentation (TEST-NET-3)"),
	prefix("224.0.0.0/4", "multicast"),
	prefix("240.0.0.0/4", "reserved"),
	prefix("255.255.255.255/32", "limited broadcast"),
}

// globalUnicast6 is the only IPv6 space allocated for global unicast;
// nothing outside it is a push server.
var globalUnicast6 = netip.MustParsePrefix("2000::/3")

// notGlobal6 is every IPv6 range a push server that is not listed may not
// be at: IANA's IPv6 Special-Purpose Address Registry's entries that are
// not globally reachable, the translation prefixes (which reach IPv4
// addresses this table could not see), 6to4 and Teredo, and the old and
// the local scopes. Most lie outside globalUnicast6 and are refused by that
// alone; they are named here so that the table says what it covers.
var notGlobal6 = []namedPrefix{
	prefix("::/128", "unspecified"),
	prefix("::1/128", "loopback"),
	prefix("::/96", "IPv4-compatible (deprecated)"),
	prefix("::ffff:0:0/96", "IPv4-mapped"),
	prefix("64:ff9b::/96", "IPv4-IPv6 translation"),
	prefix("64:ff9b:1::/48", "local-use IPv4-IPv6 translation"),
	prefix("100::/64", "discard only"),
	prefix("100:0:0:1::/64", "dummy prefix"),
	prefix("2001::/23", "IETF protocol assignments"),
	prefix("2001::/32", "Teredo"),
	prefix("2001:db8::/32", "documentation"),
	prefix("2002::/16", "6to4"),
	prefix("3fff::/20", "documentation"),
	prefix("5f00::/16", "segment routing (SRv6) SIDs"),
	prefix("fc00::/7", "unique local"),
	prefix("fe80::/10", "link local"),
	prefix("fec0::/10", "site local (deprecated)"),
	prefix("ff00::/8", "multicast"),
}

// neverEvenListed is what a listed push server may still not be at: places
// no push server is and where a cloud's own metadata service is, also as
// the well-known NAT64 prefix spells them (169.254.169.254, the most
// common, is in link local, which is refused in that spelling too, whole).
var neverEvenListed = []namedPrefix{
	prefix("0.0.0.0/8", "this network"),
	prefix("169.254.0.0/16", "link local"),
	prefix("224.0.0.0/4", "multicast"),
	prefix("255.255.255.255/32", "limited broadcast"),
	prefix("100.100.100.200/32", "a cloud's instance metadata service"),
	prefix("::/128", "unspecified"),
	prefix("::/96", "IPv4-compatible (deprecated)"),
	prefix("fe80::/10", "link local"),
	prefix("ff00::/8", "multicast"),
	prefix("fd00:ec2::254/128", "a cloud's instance metadata service"),
	prefix("64:ff9b::a9fe:0/112", "link local, through NAT64"),
	prefix("64:ff9b::6464:64c8/128", "a cloud's instance metadata service, through NAT64"),
}

// addressAllowed says whether a connection may be made to addr: for a
// push server that is not listed, only a globally reachable unicast
// address; for a listed one, anything but neverEvenListed, and loopback only
// when the entry itself names this machine.
func addressAllowed(addr netip.Addr, listed, loopbackOK bool) bool {
	if !addr.IsValid() {
		return false
	}

	addr = addr.WithZone("").Unmap()

	if listed {
		if addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
			return false
		}

		if addr.IsLoopback() {
			return loopbackOK
		}

		return !inAny(addr, neverEvenListed)
	}

	if addr.Is4() {
		return !inAny(addr, notGlobal4)
	}

	return globalUnicast6.Contains(addr) && !inAny(addr, notGlobal6)
}

func inAny(addr netip.Addr, table []namedPrefix) bool {
	for _, entry := range table {
		if entry.prefix.Contains(addr) {
			return true
		}
	}

	return false
}
