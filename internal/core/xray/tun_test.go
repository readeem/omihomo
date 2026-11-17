package xray_test

import (
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/xray"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestLinuxTUNRestartsAndRemovesRoutes(t *testing.T) {
	if os.Getenv("OMIHOMO_TEST_XRAY_TUN") != "1" {
		t.Skip("run in an isolated network namespace with CAP_NET_ADMIN and a resolver fixture")
	}
	ctx := t.Context()
	c := xray.New(freePort(t), nil)
	t.Cleanup(func() { c.Stop(context.Background()) })
	cfg := core.Config{
		Mode: protocol.ModeDirect, TUN: true,
		TUNBypass: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}
	for range 2 {
		if err := c.Apply(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		if link, err := net.InterfaceByName("omihomo"); err != nil || link.Flags&net.FlagUp == 0 {
			t.Fatalf("TUN device is not up: %v, %v", link, err)
		}
		for _, dst := range []string{"192.0.2.30", "2001:db8::30"} {
			out, err := exec.CommandContext(ctx, "ip", "route", "get", dst).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "dev omihomo") {
				t.Fatalf("route for %s: %s (%v)", dst, out, err)
			}
		}
		if out, err := exec.CommandContext(ctx, "ip", "route", "get", "10.0.0.30").CombinedOutput(); err == nil && strings.Contains(string(out), "dev omihomo") {
			t.Fatalf("bypass routes into TUN: %s", out)
		}
		if err := c.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := net.InterfaceByName("omihomo"); err == nil {
			t.Fatal("TUN device survived stop")
		}
		for _, version := range []string{"-4", "-6"} {
			out, err := exec.CommandContext(ctx, "ip", version, "rule", "show").CombinedOutput()
			if err != nil || strings.Contains(string(out), "20224") {
				t.Fatalf("TUN rule survived stop: %s (%v)", out, err)
			}
		}
	}
}
