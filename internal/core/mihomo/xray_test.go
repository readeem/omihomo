package mihomo

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/xray"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestXrayPreferredKeepsAnAutomaticGroupConnectionAcrossRuleEdits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var peers sync.WaitGroup
	defer func() { listener.Close(); peers.Wait() }()
	// The fixture speaks VLESS without dialing the requested destination.
	provenance := make(chan string, 1)
	peers.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			peers.Go(func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 22)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				if header[17] != 0 || header[18] != 1 || binary.BigEndian.Uint16(header[19:21]) != 8080 || header[21] != 1 {
					return
				}
				if _, err := io.CopyN(io.Discard, conn, 5); err != nil {
					return
				}
				provenance <- conn.RemoteAddr().String()
				if _, err := conn.Write([]byte{0, 0, 'x'}); err != nil {
					return
				}
				io.Copy(conn, conn)
			})
		}
	})

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	cfg := core.Config{Mode: protocol.ModeRule, XrayPreferred: true, Profile: core.Profile{
		Proxies: []core.Proxy{
			{Name: "Native VLESS", Type: "vless", Options: map[string]any{"server": "127.0.0.1", "port": n, "uuid": "92d696db-0166-464e-91bb-b65f72b3d665"}},
			{Name: "Mihomo only", Type: "direct"},
		},
		Groups: []core.Group{{Name: "Automatic", Type: "load-balance", Members: []string{"Native VLESS"}}},
		Rules: []core.Rule{
			{Type: "IP-CIDR", Value: "127.0.0.0/8", Target: "Mihomo only", Source: protocol.SourceUser},
			{Type: "MATCH", Target: "Automatic", Source: protocol.SourceSubscription},
		},
	}}
	if os.Getenv("OMIHOMO_TEST_ACCELERATED_TUN") == "1" {
		cfg.TUN, cfg.TUNRedirect = true, true
	}
	previousNetwork := shared.xrayNetwork
	observed := &observedXrayNetwork{Network: previousNetwork}
	shared.xrayNetwork = observed
	t.Cleanup(func() { shared.xrayNetwork = previousNetwork })
	c := running(t, cfg)
	groups, err := c.Groups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Type != "load-balance" {
		t.Fatalf("groups: %+v, %v", groups, err)
	}
	dial := c.DialContext
	if cfg.TUN {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", "192.0.2.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	var response [1]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 'x' {
		t.Fatalf("response %q: %v", response, err)
	}
	if peer := <-provenance; !observed.opened(peer) {
		t.Fatal("the automatic group sent traffic through Mihomo")
	}
	cfg.Profile.Rules[0].Value = "10.0.0.0/8"
	if err := c.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply a rule edit: %v", err)
	}
	if _, err := conn.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 'q' {
		t.Fatalf("response after rule edit %q: %v", response, err)
	}
	connections, err := c.Connections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(connections.Connections, func(conn protocol.Connection) bool { return conn.DestinationIP == "192.0.2.1" })
	if i < 0 {
		t.Fatalf("Xray connection missing from Mihomo: %+v", connections)
	}
	if err := c.CloseConnections(ctx, []string{connections.Connections[i].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(response[:]); err == nil {
		t.Fatal("connection is still usable after close")
	}
}

type observedXrayNetwork struct {
	xray.Network
	peers sync.Map
}

func (n *observedXrayNetwork) DialContext(ctx context.Context, address string, opts xray.SocketOptions) (net.Conn, error) {
	conn, err := n.Network.DialContext(ctx, address, opts)
	if err == nil {
		n.peers.Store(conn.LocalAddr().String(), true)
	}
	return conn, err
}

func (n *observedXrayNetwork) opened(address string) bool {
	_, ok := n.peers.Load(address)
	return ok
}
