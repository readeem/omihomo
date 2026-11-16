package mihomo

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/metacubex/http"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/quic-go"
	"github.com/metacubex/quic-go/http3"
	"github.com/metacubex/tls"
)

func TestHysteriaReceivesDatagramsInPeerOrder(t *testing.T) {
	previous := goruntime.GOMAXPROCS(1)
	t.Cleanup(func() { goruntime.GOMAXPROCS(previous) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: cert.Certificate, PrivateKey: cert.PrivateKey}},
		NextProtos:   []string{"h3"},
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	const batchSize, batches = 16, 16
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			conn, err := listener.Accept(ctx)
			if err != nil {
				return err
			}
			defer conn.CloseWithError(0, "")
			server := &http3.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/auth" || r.Header.Get("Hysteria-Auth") != "test" {
					w.WriteHeader(403)
					return
				}
				w.Header().Set("Hysteria-UDP", "true")
				w.Header().Set("Hysteria-CC-RX", "0")
				w.WriteHeader(233)
			})}
			served := make(chan error, 1)
			go func() { served <- server.ServeQUICConn(conn) }()
			defer func() { conn.CloseWithError(0, ""); <-served }()
			for batch := range batches {
				request, err := conn.ReceiveDatagram(ctx)
				if err != nil {
					return err
				}
				if len(request) < 9 {
					return fmt.Errorf("short Hysteria datagram: %d bytes", len(request))
				}
				for offset := range batchSize {
					// Echo the session and destination header, replacing only the payload.
					response := append([]byte(nil), request[:len(request)-4]...)
					response = binary.BigEndian.AppendUint32(response, uint32(batch*batchSize+offset))
					if err := conn.SendDatagram(response); err != nil {
						return err
					}
				}
			}
			_, err = conn.ReceiveDatagram(ctx)
			return err
		}()
	}()
	t.Cleanup(func() { cancel(); listener.Close(); <-done })
	proxy, err := outbound.NewHysteria2(outbound.Hysteria2Option{
		Name: "Ordered", Server: "127.0.0.1", Port: listener.Addr().(*net.UDPAddr).Port,
		Password: "test", SkipCertVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	packets, err := proxy.ListenPacketContext(ctx, &constant.Metadata{
		NetWork: constant.UDP, DstIP: netip.MustParseAddr("192.0.2.1"), DstPort: 7777,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer packets.Close()
	packets.SetReadDeadline(time.Now().Add(5 * time.Second))
	destination := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 7777}
	buffer := make([]byte, 4)
	for batch := range batches {
		if _, err := packets.WriteTo(buffer, destination); err != nil {
			t.Fatal(err)
		}
		for offset := range batchSize {
			n, _, err := packets.ReadFrom(buffer)
			if err != nil {
				t.Fatal(err)
			}
			want := uint32(batch*batchSize + offset)
			if n != 4 || binary.BigEndian.Uint32(buffer) != want {
				t.Fatalf("received datagram %d = %x; want sequence %d", want, buffer[:n], want)
			}
		}
	}
}
