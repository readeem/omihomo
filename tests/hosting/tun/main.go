package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/miekg/dns"
	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/uplink"
)

func peer() error {
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "203.0.113.2:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				var p [4]byte
				if _, err := io.ReadFull(c, p[:]); err == nil {
					c.Write(p[:])
				}
			}()
		}
	}()
	packet, err := net.ListenPacket("udp4", "203.0.113.2:53")
	if err != nil {
		return err
	}
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(q)
		if len(q.Question) == 1 && q.Question[0].Qtype == dns.TypeA {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("203.0.113.2")}}
		}
		w.WriteMsg(reply)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	defer packet.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"tcp": listener.Addr().(*net.TCPAddr).Port}); err != nil {
		return err
	}
	_, err = bufio.NewReader(os.Stdin).ReadString('\n')
	return err
}

func host() error {
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil || os.Getenv("HOSTING_PARENT_NETNS") == "" || current == os.Getenv("HOSTING_PARENT_NETNS") {
		return fmt.Errorf("requires private network namespace")
	}
	engine, err := mihomo.New(os.Getenv("HOSTING_CORE_DIR"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	defer engine.Stop(context.Background())
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) {
		return []uplink.Link{{Name: "uplink0", Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}, DNS: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.2:53")}}}, nil
	}}
	endpoint := net.JoinHostPort("endpoint.example", os.Getenv("HOSTING_PEER_PORT"))
	for _, mode := range []string{protocol.ModeGlobal, protocol.ModeDirect, protocol.ModeRule} {
		cfg := core.Config{Mode: mode, TUN: true, Profile: core.Profile{Groups: []core.Group{{Name: "Fixture", Type: "select", Members: []string{"REJECT"}}}, Rules: []core.Rule{{Type: "MATCH", Target: "REJECT"}}}}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := engine.Apply(ctx, cfg)
		cancel()
		if err != nil {
			return err
		}
		if _, err := net.InterfaceByName("omihomo"); err != nil {
			return fmt.Errorf("real TUN absent: %w", err)
		}
		if mode != protocol.ModeDirect {
			c, err := net.DialTimeout("tcp", net.JoinHostPort("203.0.113.2", os.Getenv("HOSTING_PEER_PORT")), time.Second)
			if err == nil {
				c.SetDeadline(time.Now().Add(time.Second))
				c.Write([]byte("test"))
				var p [4]byte
				_, err = io.ReadFull(c, p[:])
				c.Close()
				if err == nil {
					return fmt.Errorf("unbound control bypassed TUN rejection in %s", mode)
				}
			}
		}
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		c, err := network.DialContext(ctx, "tcp", endpoint)
		cancel()
		if err != nil {
			return fmt.Errorf("bound DNS/TCP failed in %s: %w", mode, err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write([]byte("test"))
		var p [4]byte
		_, err = io.ReadFull(c, p[:])
		c.Close()
		if err != nil || string(p[:]) != "test" {
			return fmt.Errorf("bound round trip failed in %s", mode)
		}
		fmt.Println("PASS: bound uplink DNS/TCP bypasses actual mihomo TUN in", mode)
	}
	return nil
}

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "peer" {
		err = peer()
	} else {
		err = host()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
