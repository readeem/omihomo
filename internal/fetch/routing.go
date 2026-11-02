package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/readeem/omihomo/internal/uplink"
)

var ErrDirectRequired = errors.New("direct download needs approval")

type directPermission struct{}

// RoutedTransport retries connection establishment once through the uplink.
// TLS and HTTP run after dialing, so their failures cannot trigger recovery.
func RoutedTransport(proxy, direct func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	transport := defaultTransport()
	// A recovered direct connection must not keep bypassing a healthy proxy.
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		attempt, cancel := context.WithTimeout(ctx, uplink.Timeout)
		conn, err := proxy(attempt, network, address)
		cancel()
		if err == nil || ctx.Err() != nil || !uplink.Recoverable(err) {
			return conn, err
		}
		if allowed, _ := ctx.Value(directPermission{}).(bool); !allowed {
			return nil, ErrDirectRequired
		}
		return direct(ctx, network, address)
	}
	return transport
}
