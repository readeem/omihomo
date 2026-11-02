package uplink

import (
	"errors"
	"net/netip"
)

var (
	ErrDownloadAddress = errors.New("downloads require a public address")
	downloadIPv6       = netip.MustParsePrefix("2000::/3")
	downloadBlocked    = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001::/23"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
	}
)

// CheckDownloadAddress checks the address that will actually be dialed, after
// resolution. DNS servers and proxy endpoints are configured independently;
// they may be private, but cannot exempt a downloaded URL from this policy.
func CheckDownloadAddress(ip netip.Addr) error {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ErrDownloadAddress
	}
	// Only native global IPv6 is accepted: translation and transition ranges
	// can embed a forbidden IPv4 address or reach a local translator.
	if ip.Is6() && !downloadIPv6.Contains(ip) {
		return ErrDownloadAddress
	}
	for _, prefix := range downloadBlocked {
		if prefix.Contains(ip) {
			return ErrDownloadAddress
		}
	}
	return nil
}
