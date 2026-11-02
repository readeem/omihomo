package uplink_test

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/readeem/omihomo/internal/uplink"
)

func TestDownloadAddresses(t *testing.T) {
	for _, address := range []string{
		"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.1.1",
		"192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:192.168.1.1", "64:ff9b::7f00:1", "64:ff9b:1::a00:1", "100::1",
		"2001::1", "2001:db8::1", "2002:7f00:1::1", "3fff::1", "fc00::1", "fe80::1", "fe80::1%lo", "ff02::1",
	} {
		t.Run(address, func(t *testing.T) {
			if err := uplink.CheckDownloadAddress(netip.MustParseAddr(address)); !errors.Is(err, uplink.ErrDownloadAddress) {
				t.Fatalf("allowed %s: %v", address, err)
			}
		})
	}
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "::ffff:8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if err := uplink.CheckDownloadAddress(netip.MustParseAddr(address)); err != nil {
			t.Errorf("blocked %s: %v", address, err)
		}
	}
	if err := uplink.CheckDownloadAddress(netip.Addr{}); !errors.Is(err, uplink.ErrDownloadAddress) {
		t.Fatal("invalid address allowed")
	}
	if uplink.Recoverable(fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Err: uplink.ErrDownloadAddress})) {
		t.Fatal("policy refusal enabled fallback")
	}
}
