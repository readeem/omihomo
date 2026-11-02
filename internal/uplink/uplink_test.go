package uplink_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/uplink"
)

func dnsServer(t *testing.T, listen, ip string) netip.AddrPort {
	t.Helper()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, query *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(query)
		if query.Question[0].Qtype == dns.TypeA {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP(ip)}}
		}
		w.WriteMsg(reply)
	})
	for _, server := range []*dns.Server{{Listener: listener, Net: "tcp", Handler: handler}, {PacketConn: packet, Net: "udp", Handler: handler}} {
		ready, done := make(chan struct{}), make(chan error, 1)
		server.NotifyStartedFunc = func() { close(ready) }
		go func() { done <- server.ActivateAndServe() }()
		<-ready
		t.Cleanup(func() {
			server.Shutdown()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	return netip.MustParseAddrPort(listener.Addr().String())
}

func TestInitialDownloadUsesFreshProviderDNS(t *testing.T) {
	first, second := dnsServer(t, "127.0.0.1:0", "127.0.0.1"), dnsServer(t, "127.0.0.1:0", "127.0.0.2")
	current := first
	var discoveries atomic.Int32
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) {
		discoveries.Add(1)
		return []uplink.Link{{Name: "lo", Addresses: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}, DNS: []netip.AddrPort{current}}}, nil
	}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if r.TLS.ServerName != "example.com" || host != "example.com" {
			t.Errorf("HTTPS hostname changed: %q", r.Host)
		}
		io.WriteString(w, "subscription")
	}))
	defer server.Close()
	unavailable := func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "proxy unavailable"}
	}
	transport := fetch.RoutedTransport(unavailable, network.DialContext)
	transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	downloader := fetch.New(transport)
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	result, err := downloader.Get(t.Context(), fetch.Request{URL: "https://example.com:" + port + "/sub?token=private"})
	if err != nil || string(result.Body) != "subscription" {
		t.Fatalf("initial provider download: %v", err)
	}
	current = second
	query := new(dns.Msg)
	query.SetQuestion("changed.example.", dns.TypeA)
	reply, err := network.ExchangeContext(t.Context(), query)
	if err != nil || len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "127.0.0.2" {
		t.Fatalf("changed provider DNS was not used: %v, %v", reply, err)
	}
	if discoveries.Load() != 2 {
		t.Fatalf("discoveries = %d", discoveries.Load())
	}
}

func TestNoProviderDNSNeverUsesTheSystemResolver(t *testing.T) {
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) { return nil, errors.New("no provider") }}
	if conn, err := network.DialContext(t.Context(), "tcp", "example.com:443"); err == nil {
		conn.Close()
		t.Fatal("a missing uplink fell back to the system resolver")
	}
}

func TestUplinkBypassesTunnelDNSInterception(t *testing.T) {
	switch os.Getenv("OMIHOMO_UPLINK_TEST") {
	case "provider":
		dnsServer(t, "192.0.2.53:53", "192.0.2.53")
		dnsServer(t, "192.0.2.54:53", "192.0.2.54")
		listener, err := net.Listen("tcp", "192.0.2.53:443")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := listener.Accept()
			if err == nil {
				conn.Write([]byte("ok"))
				conn.Close()
			}
		}()
		t.Cleanup(func() { listener.Close(); <-done })
		fmt.Println("ready")
		io.Copy(io.Discard, os.Stdin)
	case "client":
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		provider := exec.CommandContext(ctx, "ip", "netns", "exec", "provider", binary, "-test.run=^TestUplinkBypassesTunnelDNSInterception$")
		provider.Env = append(os.Environ(), "OMIHOMO_UPLINK_TEST=provider")
		input, err := provider.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := provider.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		provider.Stderr = os.Stderr
		if err := provider.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			input.Close()
			if err := provider.Wait(); err != nil {
				t.Error(err)
			}
		})
		scanner := bufio.NewScanner(output)
		if !scanner.Scan() || scanner.Text() != "ready" {
			t.Fatal("provider did not become ready")
		}
		go io.Copy(io.Discard, output)
		link, err := net.InterfaceByName("uplink0")
		if err != nil {
			t.Fatal(err)
		}
		tunnel, err := net.InterfaceByName("omihomo")
		if err != nil {
			t.Fatal(err)
		}
		stub := filepath.Join(t.TempDir(), "busctl")
		writeDNS := func(last byte) {
			t.Helper()
			body := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' '{"type":"a(iiay)","data":[[0,2,[192,0,2,99]],[%d,2,[198,18,0,1]],[%d,2,[192,0,2,%d]]]}'
`, tunnel.Index, link.Index, last)
			if err := os.WriteFile(stub, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		writeDNS(53)
		if out, err := exec.CommandContext(ctx, "mount", "--bind", stub, "/usr/bin/busctl").CombinedOutput(); err != nil {
			t.Fatalf("bind resolved fixture: %v %s", err, out)
		}
		query := new(dns.Msg)
		query.SetQuestion("bootstrap.example.", dns.TypeA)
		if _, _, err := (&dns.Client{Timeout: 100 * time.Millisecond}).ExchangeContext(ctx, query, "192.0.2.53:53"); err == nil {
			t.Fatal("the unbound control unexpectedly bypassed tunnel policy")
		}
		network := uplink.Network{}
		reply, err := network.ExchangeContext(ctx, query)
		if err != nil || len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "192.0.2.53" {
			t.Fatalf("bound UDP bootstrap: %v %v", reply, err)
		}
		connection, err := network.DialDNS(ctx)
		if err != nil {
			t.Fatal(err)
		}
		reply, _, err = (&dns.Client{Net: "tcp"}).ExchangeWithConnContext(ctx, query, &dns.Conn{Conn: connection})
		connection.Close()
		if err != nil || len(reply.Answer) != 1 {
			t.Fatalf("bound TCP bootstrap: %v %v", reply, err)
		}
		connection, err = network.DialContext(ctx, "tcp", "bootstrap.example:443")
		if err != nil {
			t.Fatal(err)
		}
		connection.SetDeadline(time.Now().Add(time.Second))
		response, err := io.ReadAll(connection)
		connection.Close()
		if err != nil || string(response) != "ok" {
			t.Fatalf("direct download connection: %s %v", response, err)
		}
		if _, err := os.Stat("/usr/bin/nmcli"); err == nil {
			nm := filepath.Join(t.TempDir(), "nmcli")
			if err := os.WriteFile(nm, []byte("#!/bin/sh\nprintf '%s\n' '127.0.0.53' '100.100.100.100' '192.0.2.54'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.CommandContext(ctx, "mount", "--bind", nm, "/usr/bin/nmcli").CombinedOutput(); err != nil {
				t.Fatalf("bind NetworkManager fixture: %v %s", err, out)
			}
			if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\n' '{\"type\":\"a(iiay)\",\"data\":[[0,2,[192,0,2,99]]]}'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		} else {
			writeDNS(54)
		}
		reply, err = network.ExchangeContext(ctx, query)
		if err != nil || len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "192.0.2.54" {
			t.Fatalf("changed uplink DNS: %v %v", reply, err)
		}
	default:
		for _, tool := range []string{"unshare", "ip", "mount", "nft"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skip("network namespace test needs " + tool)
			}
		}
		if err := exec.Command("unshare", "--user", "--map-root-user", "--net", "true").Run(); err != nil {
			t.Skip("unprivileged network namespaces are unavailable")
		}
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)
		command := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--mount", "--net", "sh", "-eu", "-c", `
mount --make-rprivate /
mount -t tmpfs tmpfs /run
ip netns add provider
ip link add uplink0 type veth peer name peer0
ip link set peer0 netns provider
ip addr add 192.0.2.2/24 dev uplink0
ip link set uplink0 up
ip route add default dev uplink0
ip -n provider addr add 192.0.2.53/24 dev peer0
ip -n provider addr add 192.0.2.54/24 dev peer0
ip -n provider link set peer0 up
ip -n provider link set lo up
ip tuntap add dev omihomo mode tun
ip addr add 198.18.0.1/30 dev omihomo
ip link set omihomo up
ip route add default dev omihomo table 2022
ip rule add priority 9000 not dport 53 table main suppress_prefixlength 0
ip rule add priority 9001 iif lo from 0.0.0.0/32 table 2022
ip rule add priority 9001 iif lo from 198.18.0.0/30 table 2022
nft add table ip test
nft 'add chain ip test output { type nat hook output priority mangle; policy accept; }'
nft 'add rule ip test output oifname "omihomo" meta l4proto tcp redirect to :9'
exec "$1" -test.run='^TestUplinkBypassesTunnelDNSInterception$'
`, "uplink-test", binary)
		command.Env = append(os.Environ(), "OMIHOMO_UPLINK_TEST=client")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated uplink recovery: %v\n%s", err, output)
		}
	}
}
