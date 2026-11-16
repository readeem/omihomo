package mihomo

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestRealityClientHelloOffersHybridKeyExchange(t *testing.T) {
	for _, tc := range []struct {
		name, fingerprint string
		hybrid            *bool
		wantHybrid        bool
	}{
		{name: "chrome default", fingerprint: "chrome", wantHybrid: true},
		{name: "firefox default", fingerprint: "firefox", wantHybrid: true},
		{name: "explicit hybrid", fingerprint: "chrome", hybrid: new(true), wantHybrid: true},
		{name: "explicit legacy", fingerprint: "chrome", hybrid: new(false)},
		{name: "legacy fingerprint preserved", fingerprint: "firefox120", hybrid: new(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hellos := make(chan bool, 1)
			srv := httptest.NewUnstartedServer(http.NotFoundHandler())
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			srv.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				hellos <- slices.Contains(hello.SupportedCurves, tls.X25519MLKEM768)
				return nil, errors.New("client hello captured")
			}}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			key, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			reality := map[string]any{"public-key": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "short-id": "0123456789abcdef"}
			if tc.hybrid != nil {
				reality["support-x25519mlkem768"] = *tc.hybrid
			}
			original := maps.Clone(reality)
			portNumber, err := strconv.Atoi(port)
			if err != nil {
				t.Fatal(err)
			}
			cfg := core.Config{Mode: protocol.ModeRule, Profile: core.Profile{
				Proxies: []core.Proxy{{Name: "Reality", Type: "vless", Options: map[string]any{
					"server": host, "port": portNumber, "uuid": "00000000-0000-4000-8000-000000000001",
					"tls": true, "servername": "donor.example", "client-fingerprint": tc.fingerprint,
					"reality-opts": reality,
				}}},
				Groups: []core.Group{{Name: "Reality test", Type: "select", Members: []string{"Reality"}}},
				Rules:  []core.Rule{{Type: "MATCH", Target: "Reality test", Source: protocol.SourceSubscription}},
			}}
			c := running(t, cfg)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			conn, _ := c.DialContext(ctx, "tcp", "192.0.2.1:80")
			if conn != nil {
				conn.Close()
			}
			select {
			case got := <-hellos:
				if got != tc.wantHybrid {
					t.Errorf("hybrid key exchange offered = %v, want %v", got, tc.wantHybrid)
				}
			case <-ctx.Done():
				t.Fatal("the REALITY client did not send a TLS ClientHello")
			}
			if !reflect.DeepEqual(reality, original) || cfg.Profile.Proxies[0].Options["client-fingerprint"] != tc.fingerprint {
				t.Error("the engine changed the cached subscription options")
			}
		})
	}
}
