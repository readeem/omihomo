package mihomo

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/readeem/mixray/adapter/xray"
	"github.com/readeem/mixray/constant"
	"github.com/readeem/mixray/tunnel"

	"github.com/readeem/omihomo/internal/core"
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
	cfg := core.Config{Mode: protocol.ModeRule, Core: protocol.CoreXray, Profile: core.Profile{
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
		// A network namespace still exposes the host's pathname D-Bus socket.
		t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "system_bus_socket"))
		cfg.TUN, cfg.TUNRedirect = true, true
	}
	c := running(t, cfg)
	if xray.Engine() != constant.EngineXray {
		t.Fatalf("engine = %q, want %q", xray.Engine(), constant.EngineXray)
	}
	if _, ok := tunnel.Proxies()["Native VLESS"].Adapter().(interface{ Retire() }); !ok {
		t.Fatal("Native VLESS is not wrapped by the Xray engine")
	}
	groups, err := c.Groups(ctx, core.Config{})
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
	<-provenance
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

	// Switching from Xray to Mihomo closes active Xray connections.
	conn2, err := dial(ctx, "tcp", "192.0.2.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	conn2.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn2.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn2, response[:]); err != nil || response[0] != 'x' {
		t.Fatalf("second response %q: %v", response, err)
	}
	<-provenance
	cfg.Core = protocol.CoreMihomo
	if err := c.Apply(ctx, cfg); err != nil {
		t.Fatalf("switch to mihomo: %v", err)
	}
	if _, err := conn2.Read(response[:]); err == nil {
		t.Fatal("Xray connection stayed open after switching to Mihomo")
	}
}

func TestXrayPreferredDeliversReplyAfterClientFIN(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			peer <- err
			return
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		// Consume the VLESS request header for an IPv4 destination, then echo
		// the payload only after the transport finishes its write side.
		if _, err := io.CopyN(io.Discard, conn, 26); err != nil {
			peer <- err
			return
		}
		body, err := io.ReadAll(conn)
		if err == nil {
			_, err = conn.Write(append([]byte{0, 0}, body...))
		}
		peer <- err
	}()
	defer listener.Close()
	_ = running(t, core.Config{Mode: protocol.ModeRule, Core: protocol.CoreXray, Profile: core.Profile{
		Proxies: []core.Proxy{{Name: "native", Type: "vless", Options: map[string]any{
			"server": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port,
			"uuid": "92d696db-0166-464e-91bb-b65f72b3d665",
		}}},
		Rules: []core.Rule{{Type: "MATCH", Target: "native", Source: protocol.SourceSubscription}},
	}})
	if xray.Engine() != constant.EngineXray {
		t.Fatalf("engine = %q, want %q", xray.Engine(), constant.EngineXray)
	}
	if _, ok := tunnel.Proxies()["native"].Adapter().(interface{ Retire() }); !ok {
		t.Fatal("missing the native VLESS outbound")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mixedPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	const request = "CONNECT 192.0.2.1:80 HTTP/1.1\r\nHost: 192.0.2.1:80\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mixed listener CONNECT response: %s", response.Status)
	}
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	writer, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("native TCP does not support a write half-close")
	}
	if err := writer.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if result, err := io.ReadAll(reader); err != nil || string(result) != "payload" {
		t.Fatalf("reply after write EOF = %q, %v", result, err)
	}
	if err := <-peer; err != nil {
		t.Fatalf("peer: %v", err)
	}
}
