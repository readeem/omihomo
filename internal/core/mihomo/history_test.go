package mihomo

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/sniffer"
	"github.com/metacubex/mihomo/constant"
	snifferType "github.com/metacubex/mihomo/constant/sniffer"
	"github.com/metacubex/mihomo/tunnel"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestHistoryIncludesProxiedDNSConnections(t *testing.T) {
	echo := tcpEcho(t)
	c := running(t, testConfig())
	dialer := tunnel.NewDNSDialer(nil, outbound.NewDirect(), "")
	conn, err := dialer.DialContext(t.Context(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "dns"); err != nil {
		t.Fatal(err)
	}
	var reply [3]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Connections(t.Context())
	if err != nil || snapshot.Open != 1 || snapshot.Summary.Upload != 3 || snapshot.Summary.Download != 3 {
		t.Fatalf("DNS snapshot = %+v, %v", snapshot, err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err = c.Connections(t.Context())
	if err != nil || snapshot.Open != 0 || snapshot.Summary.Closed != 1 {
		t.Fatalf("stopped DNS snapshot = %+v, %v", snapshot, err)
	}
	page, err := c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Process != "mihomo" || page.Rows[0].ProcessStatus != protocol.ProcessInternal || page.Rows[0].Inbound != "inner" || page.Rows[0].Download != 3 || page.Rows[0].Upload != 3 {
		t.Fatalf("DNS history = %+v, %v", page, err)
	}
}

func tcpEcho(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	return l
}

func TestHistoryDoesNotListDialsFromThePreviousSession(t *testing.T) {
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	requested := make(chan struct{}, 1)
	upstream := socks5(t, requested, release)
	cfg := testConfig()
	cfg.Profile.Proxies = append(cfg.Profile.Proxies, core.Proxy{Name: "Slow", Type: "socks5",
		Options: map[string]any{"server": "127.0.0.1", "port": upstream.Addr().(*net.TCPAddr).Port}})
	cfg.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "Slow"}}
	c := running(t, cfg)
	conn, err := c.DialContext(t.Context(), "tcp", "192.0.2.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("dial did not reach the upstream")
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(t.Context(), testConfig()); err != nil {
		t.Fatal(err)
	}
	unblock()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "old"); err != nil {
		t.Fatal(err)
	}
	var reply [3]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "old" {
		t.Fatalf("old dial did not complete: %q, %v", reply, err)
	}
	snapshot, err := c.Connections(t.Context())
	if err != nil || snapshot.Open != 0 || len(snapshot.Connections) != 0 || snapshot.Summary.Upload != 0 || snapshot.Summary.Download != 0 {
		t.Fatalf("new session contains old traffic: %+v, %v", snapshot, err)
	}
}

func TestHistoryCountsShortRejectedConnectionsWithoutSubscribers(t *testing.T) {
	cfg := testConfig()
	cfg.Profile.Groups[0].Members = []string{"REJECT"}
	cfg.Profile.Rules = []core.Rule{{Type: "DOMAIN", Value: "blocked.example", Target: "Proxy"}}
	c := running(t, cfg)
	for range 3 {
		conn, err := c.DialContext(t.Context(), "tcp", "blocked.example:443")
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		_, err = conn.Read(make([]byte, 1))
		conn.Close()
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("blocked read = %v", err)
		}
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Count != 3 || page.Rows[0].Outbound != "REJECT" || page.Rows[0].Rule != "Domain(blocked.example)" || page.Rows[0].Upload != 0 || page.Rows[0].Download != 0 {
		t.Fatalf("history = %+v", page)
	}
}

func TestHistoryKeepsFinalBytesAcrossReloadAndResetsOnStart(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			var request [7]byte
			if _, err = io.ReadFull(conn, request[:]); err == nil && string(request[:]) == "request" {
				_, err = io.WriteString(conn, "response")
			}
		}
		server <- err
	}()
	cfg := testConfig()
	cfg.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "DIRECT"}}
	c := running(t, cfg)
	conn, err := c.DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "request"); err != nil {
		t.Fatal(err)
	}
	if response, err := io.ReadAll(conn); err != nil || string(response) != "response" {
		t.Fatalf("response = %q, %v", response, err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	conn.Close()
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var summary protocol.ConnectionSummary
	for {
		conns, err := c.Connections(deadline)
		if err != nil {
			t.Fatal(err)
		}
		summary = conns.Summary
		if summary.Closed == 1 {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatalf("flow did not finish: %+v", summary)
		case <-time.After(time.Millisecond):
		}
	}
	if summary.Upload != 7 || summary.Download != 8 {
		t.Fatalf("session totals = %+v", summary)
	}
	if err := c.Apply(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{Session: summary.Session})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Count != 1 || page.Rows[0].Upload != 7 || page.Rows[0].Download != 8 {
		t.Fatalf("completed session = %+v, %v", page, err)
	}
	if err := c.Apply(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	page, err = c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || page.Session == summary.Session || len(page.Rows) != 0 {
		t.Fatalf("new session = %+v, %v", page, err)
	}
}

func TestHistoryCountsUDPAssociationWithoutChangingItsSourcePort(t *testing.T) {
	first, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	local, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "DIRECT"}}
	c := running(t, cfg)
	var source netip.AddrPort
	for i, server := range []*net.UDPConn{first, second} {
		src, dst := local.LocalAddr().(*net.UDPAddr).AddrPort(), server.LocalAddr().(*net.UDPAddr).AddrPort()
		packet := &historyPacket{local: local.LocalAddr(), reply: make(chan string, 1), payload: []byte(strings.Repeat("x", 3+i))}
		tunnel.Tunnel.HandleUDPPacket(packet, &constant.Metadata{NetWork: constant.UDP, Type: constant.SOCKS5, SrcIP: src.Addr(), SrcPort: src.Port(), DstIP: dst.Addr(), DstPort: dst.Port()})
		server.SetDeadline(time.Now().Add(5 * time.Second))
		var buf [16]byte
		n, addr, err := server.ReadFromUDP(buf[:])
		if err != nil || n != 3+i {
			t.Fatalf("UDP upload = %d, %v", n, err)
		}
		if i == 0 {
			source = addr.AddrPort()
		} else if addr.AddrPort() != source {
			t.Fatalf("UDP source port changed between destinations: %s and %s", source, addr)
		}
		if _, err := server.WriteToUDP([]byte(strings.Repeat("y", 5+i)), addr); err != nil {
			t.Fatal(err)
		}
		select {
		case reply := <-packet.reply:
			if len(reply) != 5+i {
				t.Fatalf("UDP download = %q", reply)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("UDP reply did not return")
		}
	}
	if err := c.CloseConnections(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		page, err := c.ConnectionHistory(deadline, protocol.ConnectionHistoryArgs{})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) == 1 {
			row := page.Rows[0]
			if row.Destination != "Multiple destinations" || row.Count != 1 || row.Upload != 7 || row.Download != 11 {
				t.Fatalf("UDP history = %+v", page.Rows)
			}
			if row.Process != filepath.Base(exe) || row.ProcessStatus != protocol.ProcessResolved || row.Inbound != "socks5" {
				t.Fatalf("UDP history attribution = %+v", row)
			}
			return
		}
		select {
		case <-deadline.Done():
			t.Fatalf("UDP association did not finish: %+v", page.Rows)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestHistoryReportsUDPSourceClosedBeforeLookup(t *testing.T) {
	dst, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	ingress, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	cfg := testConfig()
	cfg.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "DIRECT"}}
	c := running(t, cfg)
	client, err := net.DialUDP("udp4", nil, ingress.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	_, sendErr := client.Write([]byte("one shot"))
	if err := client.Close(); err != nil || sendErr != nil {
		t.Fatalf("send = %v, close = %v", sendErr, err)
	}
	ingress.SetReadDeadline(time.Now().Add(5 * time.Second))
	var buf [16]byte
	n, source, err := ingress.ReadFromUDPAddrPort(buf[:])
	if err != nil {
		t.Fatal(err)
	}
	target := dst.LocalAddr().(*net.UDPAddr).AddrPort()
	tunnel.Tunnel.HandleUDPPacket(&historyPacket{local: net.UDPAddrFromAddrPort(source), payload: buf[:n]}, &constant.Metadata{
		NetWork: constant.UDP, Type: constant.SOCKS5,
		SrcIP: source.Addr(), SrcPort: source.Port(), DstIP: target.Addr(), DstPort: target.Port(),
	})
	dst.SetReadDeadline(time.Now().Add(5 * time.Second))
	var forwarded [16]byte
	n, _, err = dst.ReadFromUDP(forwarded[:])
	if err != nil || string(forwarded[:n]) != "one shot" {
		t.Fatalf("forwarded = %q, %v", forwarded[:n], err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("history = %+v, %v", page, err)
	}
	row := page.Rows[0]
	if row.Process != "unknown" || row.ProcessStatus != protocol.ProcessLookupFailed || row.ProcessReason != protocol.ProcessSocketNotFound {
		t.Fatalf("UDP history attribution = %+v", row)
	}
}

type historyPacket struct {
	local   net.Addr
	reply   chan string
	payload []byte
}

func (p *historyPacket) Data() []byte        { return p.payload }
func (p *historyPacket) Drop()               {}
func (p *historyPacket) LocalAddr() net.Addr { return p.local }
func (p *historyPacket) WriteBack(data []byte, _ net.Addr) (int, error) {
	p.reply <- string(data)
	return len(data), nil
}

func TestHistoryAttributesShortTCPWithoutLiveSnapshots(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(exe)
	for _, tc := range []struct {
		name, network, bind, peer, process string
		status                             protocol.ProcessStatus
		reason                             protocol.ProcessReason
	}{
		{name: "ipv4", network: "tcp4", bind: "127.0.0.1:0", peer: "127.0.0.1", process: name, status: protocol.ProcessResolved},
		{name: "ipv6", network: "tcp6", bind: "[::1]:0", peer: "::1", process: name, status: protocol.ProcessResolved},
		{name: "dual stack", network: "tcp", bind: "[::]:0", peer: "127.0.0.1", process: name, status: protocol.ProcessResolved},
		{name: "socket not found", network: "tcp4", bind: "127.0.0.1:0", peer: "127.0.0.2", process: "unknown", status: protocol.ProcessLookupFailed, reason: protocol.ProcessSocketNotFound},
		{name: "no socket metadata", process: "unknown", status: protocol.ProcessNoLocalSocket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Profile.Rules = []core.Rule{{Type: "PROCESS-NAME", Value: name, Target: "DIRECT"}, {Type: "MATCH", Target: "DIRECT"}}
			c := running(t, cfg)
			echo := tcpEcho(t)
			var source, socketPeer netip.AddrPort
			var sourceSocket net.Conn
			if tc.network != "" {
				listener, err := net.Listen(tc.network, tc.bind)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				port := listener.Addr().(*net.TCPAddr).Port
				host := tc.peer
				if tc.reason == protocol.ProcessSocketNotFound {
					host = "127.0.0.1"
				}
				dialer := net.Dialer{Timeout: 5 * time.Second}
				client, err := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				accepted, err := listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				defer accepted.Close()
				sourceSocket = accepted
				source = netip.AddrPortFrom(netip.MustParseAddr(tc.peer), uint16(port))
				socketPeer = accepted.RemoteAddr().(*net.TCPAddr).AddrPort()
			}
			dispatcher, err := sniffer.NewDispatcher(&sniffer.Config{Enable: true, ParsePureIp: true,
				Sniffers: map[snifferType.Type]sniffer.SnifferConfig{snifferType.HTTP: {}}})
			if err != nil {
				t.Fatal(err)
			}
			tunnel.UpdateSniffer(dispatcher)
			dest := echo.Addr().(*net.TCPAddr).AddrPort()
			local, peer := net.Pipe()
			defer peer.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				tunnel.Tunnel.HandleTCPConn(&closeSourceOnRead{Conn: local, source: sourceSocket}, &constant.Metadata{
					NetWork: constant.TCP, Type: constant.HTTPS, SrcIP: source.Addr(), SrcPort: source.Port(),
					InIP: socketPeer.Addr(), InPort: socketPeer.Port(), DstIP: dest.Addr(), DstPort: dest.Port()})
			}()
			peer.SetDeadline(time.Now().Add(5 * time.Second))
			const request = "GET / HTTP/1.0\r\n\r\n"
			if _, err := io.WriteString(peer, request); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(peer, make([]byte, len(request))); err != nil {
				t.Fatal(err)
			}
			peer.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("connection did not finish")
			}
			page, err := c.ConnectionHistory(t.Context(), protocol.ConnectionHistoryArgs{})
			if err != nil || len(page.Rows) != 1 {
				t.Fatalf("history = %+v, %v", page, err)
			}
			row := page.Rows[0]
			if row.Process != tc.process || row.ProcessStatus != tc.status || row.Inbound != "https" || row.ProcessReason != tc.reason {
				t.Fatalf("history attribution = %+v, want %q %q %q", row, tc.process, tc.status, tc.reason)
			}
			if (row.Rule == "ProcessName("+name+")") != (tc.status == protocol.ProcessResolved) {
				t.Fatalf("route did not use the same attribution: %+v", row)
			}
		})
	}
}

type closeSourceOnRead struct {
	net.Conn
	source net.Conn
	once   sync.Once
}

func (c *closeSourceOnRead) Read(buf []byte) (int, error) {
	c.once.Do(func() {
		if c.source != nil {
			c.source.Close()
		}
	})
	return c.Conn.Read(buf)
}
