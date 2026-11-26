package mixray

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/readeem/mixray/component/process"
	"golang.org/x/sys/unix"
)

func TestProcessLookupMatchesLocalUDPSockets(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ network, bind, source string }{
		{"udp4", "127.0.0.1:0", "127.0.0.1"},
		{"udp6", "[::1]:0", "::1"},
		{"udp4", "0.0.0.0:0", "127.0.0.1"},
		{"udp6", "[::]:0", "::1"},
		{"udp", "[::]:0", "127.0.0.1"},
	} {
		t.Run(tc.network+"/"+tc.bind, func(t *testing.T) {
			socket, err := net.ListenPacket(tc.network, tc.bind)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			port := socket.LocalAddr().(*net.UDPAddr).Port
			_, name, err := process.FindProcessName("udp", netip.MustParseAddr(tc.source), port, netip.AddrPort{})
			if err != nil || filepath.Base(name) != filepath.Base(exe) {
				t.Fatalf("lookup = %q, %v, want %q", name, err, filepath.Base(exe))
			}
		})
	}
}

func TestProcessLookupDistinguishesTCPSocketsByPeer(t *testing.T) {
	first, second := tcpEcho(t), tcpEcho(t)
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}, Control: reuseAddress, Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp4", first.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	dialer.LocalAddr = conn.LocalAddr()
	other, err := dialer.DialContext(t.Context(), "tcp4", second.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	source := conn.LocalAddr().(*net.TCPAddr).AddrPort()
	for _, socket := range []net.Conn{conn, other} {
		peer := socket.RemoteAddr().(*net.TCPAddr).AddrPort()
		if _, name, err := process.FindProcessName("tcp", source.Addr(), int(source.Port()), peer); err != nil || name == "" {
			t.Fatalf("lookup with peer = %q, %v", name, err)
		}
	}
	if _, name, err := process.FindProcessName("tcp", source.Addr(), int(source.Port()), netip.AddrPort{}); !errors.Is(err, process.ErrAmbiguousSocket) || name != "" {
		t.Fatalf("lookup without peer = %q, %v, want ambiguity", name, err)
	}
}

func TestProcessLookupReportsAmbiguousUDPSenders(t *testing.T) {
	cfg := net.ListenConfig{Control: reuseAddress}
	listener, err := cfg.ListenPacket(t.Context(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	target, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	other, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	port := listener.LocalAddr().(*net.UDPAddr).Port
	source := netip.MustParseAddr("127.0.0.1")
	dialer := net.Dialer{LocalAddr: &net.UDPAddr{IP: net.IP(source.AsSlice()), Port: port}, Control: reuseAddress}
	conn, err := dialer.DialContext(t.Context(), "udp4", other.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer := target.LocalAddr().(*net.UDPAddr).AddrPort()
	if _, name, err := process.FindProcessName("udp", source, port, peer); !errors.Is(err, process.ErrAmbiguousSocket) || name != "" {
		t.Fatalf("lookup with two UDP senders = %q, %v, want ambiguity", name, err)
	}
}

func reuseAddress(_, _ string, conn syscall.RawConn) error {
	var sockErr error
	if err := conn.Control(func(fd uintptr) { sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1) }); err != nil {
		return err
	}
	return sockErr
}

func TestProcessLookupRejectsNonmatchingUDPSockets(t *testing.T) {
	for _, tc := range []struct{ network, bind, source string }{
		{"udp4", "127.0.0.1:0", "127.0.0.2"},
		{"udp4", "0.0.0.0:0", "192.0.2.1"},
		{"udp6", "[::]:0", "127.0.0.1"},
	} {
		t.Run(tc.network+"/"+tc.bind, func(t *testing.T) {
			socket, err := net.ListenPacket(tc.network, tc.bind)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			port := socket.LocalAddr().(*net.UDPAddr).Port
			_, name, err := process.FindProcessName("udp", netip.MustParseAddr(tc.source), port, netip.AddrPort{})
			if !errors.Is(err, process.ErrNotFound) || name != "" {
				t.Fatalf("lookup = %q, %v, want socket not found", name, err)
			}
		})
	}
}
