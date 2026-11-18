package xray_test

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/freedom"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/xray"
)

func TestHysteria2CarriesStreamsAndDatagrams(t *testing.T) {
	const burst = 16
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()

	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	roots := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(roots, pemCert, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", roots)

	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	packetDone := make(chan struct{})
	go func() {
		defer close(packetDone)
		p := make([]byte, 2048)
		_, peer, err := packet.ReadFrom(p)
		if err != nil {
			return
		}
		for i := range burst {
			p = binary.BigEndian.AppendUint32(p[:0], uint32(i))
			if _, err := packet.WriteTo(p, peer); err != nil {
				return
			}
		}
	}()
	defer func() { packet.Close(); <-packetDone }()

	origin, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	originDone := make(chan struct{})
	go func() {
		defer close(originDone)
		conn, err := origin.Accept()
		if err == nil {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			io.CopyN(conn, conn, 4)
		}
	}()
	defer func() { origin.Close(); <-originDone }()

	portHolder, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := portHolder.LocalAddr().(*net.UDPAddr).Port
	portHolder.Close()

	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "hysteria",
			"settings": map[string]any{"version": 2, "users": []any{map[string]string{"auth": "test"}}},
			"streamSettings": map[string]any{
				"network": "hysteria", "security": "tls",
				"hysteriaSettings": map[string]int{"version": 2},
				"tlsSettings": map[string]any{"certificates": []any{map[string]any{
					"certificate": []string{string(pemCert)}, "key": []string{string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))},
				}}, "alpn": []string{"h3"}},
				"finalmask": map[string]any{"udp": []any{map[string]any{
					"type": "salamander", "settings": map[string]string{"password": "test mask"},
				}}},
			},
		}},
	}
	startServer(t, settings)

	engine, err := xray.New([]core.Proxy{{Name: "h2", Type: "hysteria2", Options: map[string]any{
		"server": "127.0.0.1", "port": port, "password": "test", "obfs": "salamander", "obfs-password": "test mask",
	}}}, localNetwork{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	out := engine.Get("h2")
	if out == nil || !out.SupportUDP() {
		t.Fatal("Hysteria 2 with Salamander is missing its Xray outbound")
	}
	stream, err := out.DialContext(ctx, "127.0.0.1", uint16(origin.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	var echo [4]byte
	if _, err := io.ReadFull(stream, echo[:]); err != nil || string(echo[:]) != "ping" {
		t.Fatalf("TCP echo %q: %v", echo, err)
	}

	target := packet.LocalAddr().(*net.UDPAddr)
	datagrams, err := out.ListenPacket(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer datagrams.Close()
	stop := context.AfterFunc(ctx, func() { datagrams.Close() })
	defer stop()
	if _, err := datagrams.WriteTo([]byte("pong"), target); err != nil {
		t.Fatal(err)
	}
	for i := range burst {
		n, source, err := datagrams.ReadFrom(echo[:])
		if err != nil || n != len(echo) || binary.BigEndian.Uint32(echo[:]) != uint32(i) || source.String() != target.String() {
			t.Fatalf("UDP datagram %d = %x from %v: %v", i, echo[:n], source, err)
		}
	}
}

func startServer(t *testing.T, settings map[string]any) {
	t.Helper()
	settings["log"] = map[string]string{"loglevel": "none"}
	settings["outbounds"] = []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
		"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
	}}}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var config conf.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	native, err := config.Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := xcore.New(native)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestPooledTransportsUseSeparateBootstrapSockets(t *testing.T) {
	const uuid = "92d696db-0166-464e-91bb-b65f72b3d665"
	for _, name := range []string{"grpc", "xhttp"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			origin, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var peers sync.WaitGroup
			defer func() { origin.Close(); peers.Wait() }()
			peers.Go(func() {
				for {
					conn, err := origin.Accept()
					if err != nil {
						return
					}
					peers.Go(func() {
						defer conn.Close()
						conn.SetDeadline(time.Now().Add(8 * time.Second))
						io.Copy(conn, conn)
					})
				}
			})

			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			startServer(t, map[string]any{"inbounds": []any{map[string]any{
				"listen": "127.0.0.1", "port": port, "protocol": "vless",
				"settings": map[string]any{"decryption": "none", "clients": []any{map[string]string{"id": uuid}}},
				"streamSettings": map[string]any{"network": name,
					"grpcSettings":  map[string]string{"serviceName": "test"},
					"xhttpSettings": map[string]string{"path": "/test", "mode": "packet-up"},
				},
			}}})

			network := &bootstrapNetwork{dialed: make(chan bool, 32)}
			engine, err := xray.New([]core.Proxy{{Name: name, Type: "vless", Options: map[string]any{
				"server": "proxy.example", "port": port, "uuid": uuid, "network": name,
				"grpc-opts":  map[string]any{"grpc-service-name": "test"},
				"xhttp-opts": map[string]any{"path": "/test", "mode": "packet-up"},
			}}}, network)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			out := engine.Get(name)
			if out == nil {
				t.Fatalf("missing native %s outbound", name)
			}
			var connections []net.Conn
			for _, dialCtx := range []context.Context{ctx, xray.WithBootstrapLookup(ctx)} {
				conn, err := out.DialContext(dialCtx, "127.0.0.1", uint16(origin.Addr().(*net.TCPAddr).Port))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				connections = append(connections, conn)
				deadline, _ := ctx.Deadline()
				conn.SetDeadline(deadline)
				if _, err := conn.Write([]byte("test")); err != nil {
					t.Fatal(err)
				}
				var response [4]byte
				if _, err := io.ReadFull(conn, response[:]); err != nil || string(response[:]) != "test" {
					t.Fatalf("%s echo %q: %v", name, response, err)
				}
			}
			seen := make(map[bool]bool)
			for len(seen) < 2 {
				select {
				case bootstrap := <-network.dialed:
					seen[bootstrap] = true
				case <-ctx.Done():
					t.Fatalf("saw bootstrap policies %v, want a physical dial for each", seen)
				}
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			for _, conn := range connections {
				var response [1]byte
				if _, err := conn.Read(response[:]); err != io.EOF {
					t.Fatalf("read after closing %s engine: %v", name, err)
				}
			}
		})
	}
}

type bootstrapNetwork struct {
	localNetwork
	dialed chan bool
}

func (n *bootstrapNetwork) DialContext(ctx context.Context, address string, options xray.SocketOptions) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	select {
	case n.dialed <- options.Bootstrap:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return n.localNetwork.DialContext(ctx, net.JoinHostPort("127.0.0.1", port), options)
}

type localNetwork struct{}

func (localNetwork) DialContext(ctx context.Context, address string, _ xray.SocketOptions) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}

func (localNetwork) ListenPacket(ctx context.Context, address string, _ xray.SocketOptions) (net.PacketConn, *net.UDPAddr, error) {
	target, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, nil, err
	}
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", "127.0.0.1:0")
	return conn, target, err
}
