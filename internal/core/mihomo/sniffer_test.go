package mihomo

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestDomainRulesRecoverTLSHost(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, r.Host)
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "192.0.2.55:443" {
			t.Errorf("proxy request = %s %s; want CONNECT to the original IP", r.Method, r.Host)
			http.Error(w, "unexpected destination", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		upstream.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		done := make(chan struct{})
		go func() { io.Copy(upstream, conn); upstream.Close(); close(done) }()
		io.Copy(conn, upstream)
		conn.Close()
		<-done
	}))
	defer proxy.Close()
	cfg := testConfig()
	addr := proxy.Listener.Addr().(*net.TCPAddr)
	cfg.Profile.Proxies = append(cfg.Profile.Proxies, core.Proxy{Name: "Preferred", Type: "http",
		Options: map[string]any{"server": "127.0.0.1", "port": addr.Port}})
	cfg.Profile.Rules = []core.Rule{
		{Type: "DOMAIN-SUFFIX", Value: "first.example", Target: "Preferred", Source: protocol.SourceUser},
		{Type: "DOMAIN-SUFFIX", Value: "second.example", Target: "Preferred", Source: protocol.SourceUser},
		{Type: "MATCH", Target: "REJECT", Source: protocol.SourceSubscription},
	}
	c := running(t, cfg)
	for _, tc := range []struct{ host, cached string }{
		{host: "first.example"}, {host: "video.first.example"},
		{host: "second.example"}, {host: "video.second.example"},
		{host: "first.example", cached: "other.example"},
		{host: "second.example", cached: "other.example"},
		{host: "unmatched.example"},
	} {
		t.Run(tc.host+"/"+tc.cached, func(t *testing.T) {
			client, inbound := net.Pipe()
			done := make(chan struct{})
			t.Cleanup(func() { client.Close(); inbound.Close(); <-done })
			inbound.SetDeadline(time.Now().Add(5 * time.Second))
			go func() {
				defer close(done)
				tunnel.Tunnel.HandleTCPConn(inbound, &constant.Metadata{
					NetWork: constant.TCP, Type: constant.TUN,
					Host: tc.cached, DNSMode: constant.DNSMapping,
					SrcIP: netip.MustParseAddr("192.0.2.1"), SrcPort: 12345,
					DstIP: netip.MustParseAddr("192.0.2.55"), DstPort: 443,
				})
			}()
			conn := tls.Client(client, &tls.Config{ServerName: tc.host, InsecureSkipVerify: true})
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			err := conn.HandshakeContext(t.Context())
			if tc.host == "unmatched.example" {
				if err == nil {
					t.Fatal("an unrelated domain bypassed the REJECT fallback")
				}
				return
			}
			if err != nil {
				t.Fatalf("domain rule did not route TLS for %s through Preferred: %v", tc.host, err)
			}
			snapshot, err := c.Connections(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, connection := range snapshot.Connections {
				if connection.SourceIP != "192.0.2.1" {
					continue
				}
				found = true
				if connection.Host != tc.host || connection.Chain != "Preferred" || connection.DestinationIP != "192.0.2.55" {
					t.Errorf("connection = %+v; want recovered host %s through Preferred", connection, tc.host)
				}
			}
			if !found {
				t.Error("the routed connection is missing from the snapshot")
			}
			fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", tc.host)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != tc.host+"\n" {
				t.Fatalf("response = %q, %v; want %s", body, err, tc.host)
			}
		})
	}
}
