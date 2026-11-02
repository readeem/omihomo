package main

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/readeem/omihomo/internal/uplink"
)

func TestPrivatePolicyCapture(t *testing.T) {
	if os.Getenv("HOSTING_PRIVATE_POLICY") != "1" {
		t.Skip("requires network_namespace.py")
	}
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	host := os.Getenv("HOSTING_PARENT_NETNS")
	if host == "" || current == host {
		t.Fatal("refusing host network namespace")
	}
	listener, err := net.Listen("tcp4", "192.0.2.2:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	defer func() { listener.Close(); <-done }()
	plain := net.Dialer{Timeout: 300 * time.Millisecond}
	if conn, err := plain.Dial("tcp", listener.Addr().String()); err == nil {
		conn.Close()
		t.Fatal("unbound control escaped capture policy")
	}
	packet, err := net.ListenPacket("udp4", "192.0.2.2:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "endpoint.example.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.2")}}
		}
		w.WriteMsg(reply)
	})}
	serverDone := make(chan struct{})
	go func() { defer close(serverDone); server.ActivateAndServe() }()
	defer func() { server.Shutdown(); packet.Close(); <-serverDone }()
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) {
		return []uplink.Link{{Name: "lo", Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}, DNS: []netip.AddrPort{netip.MustParseAddrPort(packet.LocalAddr().String())}}}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	conn, err := network.DialContext(ctx, "tcp", net.JoinHostPort("endpoint.example", port))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestCancelBlockedEndpointDNS(t *testing.T) {
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	queried := make(chan struct{})
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		data := make([]byte, 512)
		if _, _, err := packet.ReadFrom(data); err == nil {
			close(queried)
		}
	}()
	defer func() { packet.Close(); <-workerDone }()
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) {
		return []uplink.Link{{Name: "lo", Addresses: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}, DNS: []netip.AddrPort{netip.MustParseAddrPort(packet.LocalAddr().String())}}}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		conn, err := network.DialContext(ctx, "tcp", "endpoint.example:443")
		if conn != nil {
			conn.Close()
		}
		finished <- err
	}()
	defer func() { cancel(); <-finished }()
	select {
	case <-queried:
	case <-time.After(time.Second):
		t.Fatal("no actual DNS query observed")
	}
	cancel()
	select {
	case err := <-finished:
		finished <- err
		if err == nil {
			t.Fatal("canceled DNS dial succeeded")
		}
	case <-time.After(time.Second):
		t.Error("canceled DNS lookup did not return within one second")
	}
}
