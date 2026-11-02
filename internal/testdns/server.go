package testdns

import (
	"github.com/miekg/dns"
	"net"
	"net/netip"
	"testing"
)

func Serve(t *testing.T, listen, ip string) netip.AddrPort {
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
