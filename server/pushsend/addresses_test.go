package pushsend

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// lastAddr is the last address of a prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	p = p.Masked()
	bytes := p.Addr().As16()
	start := 0
	if p.Addr().Is4() {
		start = 12
	}

	bits := p.Bits()
	for i := start; i < 16; i++ {
		offset := (i - start) * 8
		switch {
		case offset >= bits:
			bytes[i] = 0xff
		case offset+8 > bits:
			bytes[i] |= 0xff >> (bits - offset)
		}
	}

	addr := netip.AddrFrom16(bytes)
	if p.Addr().Is4() {
		addr = addr.Unmap()
	}

	return addr
}

func TestEveryRangeInTheTableIsRefusedWhenNotListed(t *testing.T) {
	for _, table := range [][]namedPrefix{notGlobal4, notGlobal6} {
		for _, entry := range table {
			first := entry.prefix.Masked().Addr()
			last := lastAddr(entry.prefix)
			require.True(t, entry.prefix.Contains(last), "%s", entry.prefix)

			for _, addr := range []netip.Addr{first, first.Next(), last} {
				if !entry.prefix.Contains(addr) {
					continue
				}

				require.False(t, addressAllowed(addr, false, false), "%s (%s): %s", entry.prefix, entry.name, addr)
			}
		}
	}
}

func TestTheNamedAddressesAreRefusedWhenNotListed(t *testing.T) {
	for _, text := range []string{
		"127.0.0.1", "127.255.255.254", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.0.1",
		"100.64.0.1", "100.127.255.255", "169.254.169.254", "0.0.0.0", "0.1.2.3", "192.0.0.9",
		"192.88.99.1", "198.18.0.1", "198.19.255.255", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"239.255.255.250", "192.0.2.1", "198.51.100.1", "203.0.113.1",
		"::", "::1", "::7f00:1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"fc00::1", "fd00::1", "fd00:ec2::254", "fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1",
		"64:ff9b::7f00:1", "64:ff9b::808:808", "64:ff9b:1::1", "100::1", "100:0:0:1::1",
		"2001::1", "2001:0:4136:e378::1", "2001:2::1", "2001:db8::1", "2002:7f00:1::1", "2002:c0a8:1::1",
		"3fff::1", "5f00::1", "4000::1", "::2",
	} {
		addr := netip.MustParseAddr(text)
		require.False(t, addressAllowed(addr, false, false), text)
		require.False(t, addressAllowed(addr, false, true), text)
	}
}

func TestGloballyReachableAddressesAreAllowed(t *testing.T) {
	for _, text := range []string{
		"8.8.8.8", "1.1.1.1", "9.9.9.9", "159.69.0.1", "100.63.255.255", "100.128.0.0", "172.15.255.255",
		"172.32.0.0", "192.0.1.1", "192.169.0.1", "198.17.255.255", "198.20.0.0", "223.255.255.255",
		"11.0.0.1", "126.255.255.255", "128.0.0.1", "169.253.255.255", "::ffff:8.8.8.8",
		"2606:4700:4700::1111", "2a00:1450:4001:80b::200e", "2001:4860:4860::8888", "2400:cb00::1",
		"2001:200::1", "2003::1", "3ffe:ffff::1",
	} {
		addr := netip.MustParseAddr(text)
		require.True(t, addressAllowed(addr, false, false), text)
	}
}

func TestAListedPushServerMayBeOnTheOwnNetworkButNotEverywhere(t *testing.T) {
	for _, text := range []string{"10.1.2.3", "192.168.1.20", "172.20.0.5", "100.64.0.9", "100.100.100.199", "fd12::1", "8.8.8.8", "2606:4700::1", "198.18.0.1", "64:ff9b::a00:1"} {
		require.True(t, addressAllowed(netip.MustParseAddr(text), true, false), text)
	}

	for _, text := range []string{
		"169.254.169.254", "169.254.0.1", "0.0.0.0", "0.1.2.3", "224.0.0.251", "239.1.2.3",
		"255.255.255.255", "::", "fe80::1", "ff02::fb", "fd00:ec2::254", "::ffff:169.254.169.254", "::7f00:1",
		"100.100.100.200", "::ffff:100.100.100.200", "64:ff9b::a9fe:a9fe", "64:ff9b::169.254.169.254", "64:ff9b::a9fe:aa02", "64:ff9b::a9fe:1", "64:ff9b::6464:64c8",
	} {
		require.False(t, addressAllowed(netip.MustParseAddr(text), true, true), text)
	}

	for _, entry := range neverEvenListed {
		for _, addr := range []netip.Addr{entry.prefix.Masked().Addr(), lastAddr(entry.prefix)} {
			require.False(t, addressAllowed(addr, true, true), "%s (%s): %s", entry.prefix, entry.name, addr)
		}
	}

	// Loopback only when the entry itself names this machine.
	for _, text := range []string{"127.0.0.1", "127.0.0.53", "::1", "::ffff:127.0.0.1"} {
		require.False(t, addressAllowed(netip.MustParseAddr(text), true, false), text)
		require.True(t, addressAllowed(netip.MustParseAddr(text), true, true), text)
	}

	require.False(t, addressAllowed(netip.Addr{}, true, true))
}
