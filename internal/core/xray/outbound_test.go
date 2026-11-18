package xray_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
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
		for range 2 {
			conn, err := origin.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			io.CopyN(conn, conn, 4)
			conn.Close()
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

	targetPort := uint16(origin.Addr().(*net.TCPAddr).Port)
	bootstrapped := make(chan struct{}, 1)
	var out *xray.Outbound
	network := packetBootstrapNetwork{bootstrap: func(ctx context.Context) error {
		conn, err := out.DialContext(xray.WithBootstrapLookup(ctx), "127.0.0.1", targetPort)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		if _, err := conn.Write([]byte("boot")); err != nil {
			return err
		}
		var echo [4]byte
		if _, err := io.ReadFull(conn, echo[:]); err != nil {
			return err
		}
		bootstrapped <- struct{}{}
		return nil
	}}
	engine, err := xray.New([]core.Proxy{
		{Name: "h2", Type: "hysteria2", Options: map[string]any{
			"server": "proxy.example", "sni": "127.0.0.1", "port": port, "password": "test",
			"obfs": "salamander", "obfs-password": "test mask",
		}},
		{Name: "h2-unverified", Type: "hysteria2", Options: map[string]any{
			"server": "127.0.0.1", "port": port, "password": "test", "skip-cert-verify": true,
		}},
	}, network)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if engine.Get("h2-unverified") != nil {
		t.Fatal("Hysteria 2 with skip-cert-verify should retain its Mihomo adapter")
	}
	out = engine.Get("h2")
	if out == nil {
		t.Fatal("Hysteria 2 with Salamander is missing its Xray outbound")
	}
	stream, err := out.DialContext(ctx, "127.0.0.1", targetPort)
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
	select {
	case <-bootstrapped:
	default:
		t.Fatal("the cold hostname-based outbound did not bootstrap through itself")
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

func TestVLESSTransportsCarryDatagrams(t *testing.T) {
	const uuid = "92d696db-0166-464e-91bb-b65f72b3d665"
	for _, network := range []string{"tcp", "xhttp"} {
		t.Run(network, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()

			packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			packetDone := make(chan struct{})
			go func() {
				defer close(packetDone)
				buf := make([]byte, 2048)
				n, peer, err := packet.ReadFrom(buf)
				if err == nil {
					_, _ = packet.WriteTo(append([]byte("reply:"), buf[:n]...), peer)
				}
			}()
			defer func() { packet.Close(); <-packetDone }()

			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			stream := map[string]any{"network": network}
			if network == "xhttp" {
				stream["xhttpSettings"] = map[string]string{"mode": "packet-up"}
			}
			startServer(t, map[string]any{"inbounds": []any{map[string]any{
				"listen": "127.0.0.1", "port": port, "protocol": "vless",
				"settings":       map[string]any{"decryption": "none", "clients": []any{map[string]string{"id": uuid}}},
				"streamSettings": stream,
			}}})

			options := map[string]any{
				"server": "127.0.0.1", "port": port, "uuid": uuid, "network": network,
			}
			if network == "xhttp" {
				options["alpn"] = []any{"http/1.1"}
				options["xhttp-opts"] = map[string]any{"mode": "packet-up"}
			}
			engine, err := xray.New([]core.Proxy{{Name: network, Type: "vless", Options: options}}, localNetwork{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			out := engine.Get(network)
			if out == nil {
				t.Fatal("missing native VLESS outbound")
			}
			target := packet.LocalAddr().(*net.UDPAddr)
			datagrams, err := out.ListenPacket(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			defer datagrams.Close()
			if _, err := datagrams.WriteTo([]byte("ping"), target); err != nil {
				t.Fatal(err)
			}
			var got [32]byte
			n, source, err := datagrams.ReadFrom(got[:])
			if err != nil || string(got[:n]) != "reply:ping" || source.String() != target.String() {
				t.Fatalf("UDP reply = %q from %v: %v", got[:n], source, err)
			}
		})
	}
}

type packetBootstrapNetwork struct {
	localNetwork
	bootstrap func(context.Context) error
}

func (n packetBootstrapNetwork) ListenPacket(ctx context.Context, address string, opts xray.SocketOptions) (net.PacketConn, *net.UDPAddr, error) {
	if !opts.Bootstrap {
		if err := n.bootstrap(ctx); err != nil {
			return nil, nil, err
		}
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, err
	}
	return n.localNetwork.ListenPacket(ctx, net.JoinHostPort("127.0.0.1", port), opts)
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
					"grpcSettings":  map[string]string{"serviceName": "GunService"},
					"xhttpSettings": map[string]string{"path": "/test", "mode": "packet-up"},
				},
			}}})

			network := &bootstrapNetwork{dialed: make(chan bool, 32)}
			engine, err := xray.New([]core.Proxy{{Name: name, Type: "vless", Options: map[string]any{
				"server": "proxy.example", "port": port, "uuid": uuid, "network": name,
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

func TestWebSocketTLSUsesTheConfiguredHost(t *testing.T) {
	names := make(chan string, 4)
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		names <- hello.ServerName
		return nil, io.EOF
	}}
	server.StartTLS()
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	for _, kind := range []string{"vless", "vmess"} {
		t.Run(kind, func(t *testing.T) {
			engine, err := xray.New([]core.Proxy{
				webSocketProxy(kind, port, ""),
				webSocketProxy(kind, port, "tls.example"),
			}, localNetwork{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			for _, tc := range []struct{ name, want string }{
				{"", "websocket.example"},
				{"tls.example", "tls.example"},
			} {
				out := engine.Get(tc.name)
				if out == nil {
					t.Fatalf("missing native %s WebSocket outbound", kind)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				dialed := make(chan error, 1)
				go func() {
					conn, err := out.DialContext(ctx, "127.0.0.1", 1)
					if err == nil {
						conn.Close()
					}
					dialed <- err
				}()
				select {
				case name := <-names:
					if name != tc.want {
						t.Errorf("TLS name = %q, want %q", name, tc.want)
					}
				case err := <-dialed:
					t.Fatalf("dial finished before TLS negotiation: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("no TLS ClientHello")
				}
				cancel()
				<-dialed
			}
		})
	}
}

func webSocketProxy(kind string, port int, name string) core.Proxy {
	return core.Proxy{Name: name, Type: kind, Options: map[string]any{
		"server": "127.0.0.1", "port": port, "uuid": "92d696db-0166-464e-91bb-b65f72b3d665",
		"network": "ws", "tls": true, "servername": name,
		"ws-opts": map[string]any{"headers": map[string]any{"hOsT": "websocket.example"}},
	}}
}

func TestGRPCPreservesRequestFields(t *testing.T) {
	for _, service := range []string{"edge/proxy", "/custom"} {
		t.Run(service, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type requestFields struct{ authority, path, agent string }
			seen := make(chan requestFields, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- requestFields{r.Host, r.URL.RequestURI(), r.UserAgent()}
				w.Header().Set("Content-Type", "application/grpc")
			}))
			server.Config.Protocols = new(http.Protocols)
			server.Config.Protocols.SetUnencryptedHTTP2(true)
			server.Start()
			defer server.Close()
			engine, err := xray.New([]core.Proxy{{Name: "gun", Type: "vless", Options: map[string]any{
				"server": "127.0.0.1", "port": server.Listener.Addr().(*net.TCPAddr).Port,
				"network": "grpc", "uuid": "92d696db-0166-464e-91bb-b65f72b3d665", "servername": "grpc.example",
				"grpc-opts": map[string]any{"grpc-service-name": service, "grpc-user-agent": "chrome"},
			}}}, localNetwork{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			out := engine.Get("gun")
			if out == nil {
				t.Fatal("missing native gRPC outbound")
			}
			conn, err := out.DialContext(ctx, "127.0.0.1", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			path := "/edge/proxy/Tun"
			if service == "/custom" {
				path = "/custom"
			}
			select {
			case got := <-seen:
				if want := (requestFields{"grpc.example", path, "chrome"}); got != want {
					t.Fatalf("request fields = %+v, want %+v", got, want)
				}
			case <-ctx.Done():
				t.Fatal("peer did not receive a gRPC request")
			}
		})
	}
}

func TestGRPCReceivesReplyAfterWriteEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		// One gRPC message carrying a Hunk with the VLESS reply header and payload.
		w.Write([]byte("\x00\x00\x00\x00\x09\x0a\x07\x00\x00reply"))
		w.Header().Set("Grpc-Status", "0")
	}))
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	defer server.Close()

	engine, err := xray.New([]core.Proxy{{Name: "gun", Type: "vless", Options: map[string]any{
		"server": "127.0.0.1", "port": server.Listener.Addr().(*net.TCPAddr).Port,
		"network": "grpc", "uuid": "92d696db-0166-464e-91bb-b65f72b3d665",
	}}}, localNetwork{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	conn, err := engine.Get("gun").DialContext(ctx, "127.0.0.1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if _, err := io.WriteString(conn, "request"); err != nil {
		t.Fatal(err)
	}
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "reply" {
		t.Fatalf("reply after write EOF = %q, %v", reply, err)
	}
}

func TestClosingXHTTPConnectionCancelsItsRequests(t *testing.T) {
	for _, cleartextHTTP2 := range []bool{false, true} {
		name := "HTTP1"
		if cleartextHTTP2 {
			name = "h2c"
		}
		t.Run(name, func(t *testing.T) { testXHTTPRequests(t, cleartextHTTP2) })
	}
}

func testXHTTPRequests(t *testing.T, cleartextHTTP2 bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	get, post := make(chan struct{}, 1), make(chan struct{}, 2)
	canceled := make(chan string, 3)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			get <- struct{}{}
		} else {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return
			}
			post <- struct{}{}
		}
		select {
		case <-r.Context().Done():
			canceled <- r.Method
		case <-ctx.Done():
		}
	}))
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetHTTP1(!cleartextHTTP2)
	server.Config.Protocols.SetUnencryptedHTTP2(cleartextHTTP2)
	server.Start()
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	options := map[string]any{
		"server": "127.0.0.1", "port": port, "uuid": "92d696db-0166-464e-91bb-b65f72b3d665",
		"network": "xhttp", "xhttp-opts": map[string]any{"mode": "packet-up"},
	}
	if !cleartextHTTP2 {
		options["alpn"] = []any{"http/1.1"}
	}
	engine, err := xray.New([]core.Proxy{{Name: "split", Type: "vless", Options: options}}, localNetwork{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	out := engine.Get("split")
	if out == nil {
		t.Fatal("missing the native XHTTP outbound")
	}
	conn, err := out.DialContext(ctx, "127.0.0.1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetWriteDeadline(deadline)
	if _, err := conn.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	for _, request := range []<-chan struct{}{get, post} {
		select {
		case <-request:
		case <-ctx.Done():
			t.Fatal("XHTTP did not start both requests")
		}
	}
	if _, err := conn.Write([]byte("next packet")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-post:
	case <-ctx.Done():
		t.Fatal("second XHTTP upload was blocked by the first response")
	}
	conn.Close()
	seen := make(map[string]int)
	for range 3 {
		select {
		case method := <-canceled:
			seen[method]++
		case <-ctx.Done():
			t.Fatalf("server saw client cancellation for %v, want one GET and two POSTs", seen)
		}
	}
	if seen[http.MethodGet] != 1 || seen[http.MethodPost] != 2 {
		t.Fatalf("canceled requests: %v", seen)
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
