package mihomo

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

func trackedProcess(t *testing.T, m *constant.Metadata) {
	t.Helper()
	local, peer := net.Pipe()
	tracker := statistic.NewTCPTracker(outbound.NewConn(local, outbound.NewDirect()), statistic.DefaultManager, m, nil, 0, 0, true)
	t.Cleanup(func() { tracker.Close(); peer.Close() })
}

func connectionProcess(t *testing.T, c *Core) string {
	t.Helper()
	snapshot, err := c.Connections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Connections) != 1 {
		t.Fatalf("got %d connections, want one", len(snapshot.Connections))
	}
	return snapshot.Connections[0].Process
}

func TestConnectionsRetryProcessLookupAndKeepTheResolvedName(t *testing.T) {
	c := running(t, testConfig())
	find := c.findProcess
	t.Cleanup(func() { c.findProcess = find })
	ip := netip.MustParseAddr("192.0.2.1")
	trackedProcess(t, &constant.Metadata{NetWork: constant.TCP, Type: constant.TUN, SrcIP: ip, SrcPort: 12345})
	calls := 0
	c.findProcess = func(network string, source netip.Addr, port int) (uint32, string, error) {
		calls++
		if network != "tcp" || source.Unmap() != ip || port != 12345 {
			t.Fatalf("unexpected socket: %s %v %d", network, source, port)
		}
		if calls <= 2 {
			return 0, "", process.ErrNotFound
		}
		return 1000, "/usr/bin/example", nil
	}
	if got := connectionProcess(t, c); got != "" {
		t.Fatalf("first lookup = %q, want unresolved", got)
	}
	for range 4 {
		if got := connectionProcess(t, c); got != "example" {
			t.Fatalf("retried lookup = %q, want example", got)
		}
	}
	if calls != 3 {
		t.Fatalf("lookup calls = %d, want 3 (failed IPv4 and mapped IPv6, then success)", calls)
	}
}

func TestConnectionsBoundFailedProcessLookups(t *testing.T) {
	c := running(t, testConfig())
	find := c.findProcess
	t.Cleanup(func() { c.findProcess = find })
	trackedProcess(t, &constant.Metadata{NetWork: constant.UDP, Type: constant.TUN, SrcIP: netip.MustParseAddr("2001:db8::1"), SrcPort: 12345})
	calls := 0
	c.findProcess = func(network string, source netip.Addr, port int) (uint32, string, error) {
		calls++
		if network != "udp" || !source.Is6() {
			t.Fatalf("unexpected socket: %s %v", network, source)
		}
		return 0, "", process.ErrNotFound
	}
	for range 6 {
		if got := connectionProcess(t, c); got != "" {
			t.Fatalf("failed lookup = %q", got)
		}
	}
	if calls != 3 {
		t.Fatalf("lookup calls = %d, want 3", calls)
	}
}

func TestConnectionsDoNotLookUpInternalOrAlreadyNamedProcesses(t *testing.T) {
	for _, m := range []*constant.Metadata{
		{NetWork: constant.TCP, Type: constant.INNER, SrcIP: netip.MustParseAddr("127.0.0.1"), SrcPort: 12345},
		{NetWork: constant.TCP, Type: constant.TUN, Process: "known", SrcIP: netip.MustParseAddr("127.0.0.1"), SrcPort: 12345},
		{NetWork: constant.TCP, Type: constant.TUN},
	} {
		t.Run(m.Process+m.Type.String()+m.SrcIP.String(), func(t *testing.T) {
			c := running(t, testConfig())
			find := c.findProcess
			t.Cleanup(func() { c.findProcess = find })
			c.findProcess = func(string, netip.Addr, int) (uint32, string, error) {
				t.Fatal("unexpected process lookup")
				return 0, "", nil
			}
			trackedProcess(t, m)
			if got := connectionProcess(t, c); got != m.Process {
				t.Fatalf("process = %q, want %q", got, m.Process)
			}
		})
	}
}

func TestConnectionsFindSocketProcesses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"tcp4", "tcp6", "udp4", "udp6", "udp"} {
		t.Run(network, func(t *testing.T) {
			c := running(t, testConfig())
			var addr net.Addr
			transport := constant.TCP
			if network[:3] == "tcp" {
				listener, err := net.Listen(network, "localhost:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				addr = listener.Addr()
			} else {
				transport = constant.UDP
				host := "localhost:0"
				if network == "udp" {
					host = ":0"
				}
				socket, err := net.ListenPacket(network, host)
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
				addr = socket.LocalAddr()
			}
			source := netip.MustParseAddrPort(addr.String())
			if network == "udp" {
				source = netip.AddrPortFrom(netip.MustParseAddr("198.18.0.1"), source.Port())
			}
			trackedProcess(t, &constant.Metadata{NetWork: transport, Type: constant.TUN, SrcIP: source.Addr(), SrcPort: source.Port()})
			if got := connectionProcess(t, c); got != filepath.Base(exe) {
				t.Fatalf("process = %q, want %q", got, filepath.Base(exe))
			}
		})
	}
}
