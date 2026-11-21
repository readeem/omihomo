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
type requestCancellation struct{}

// RoutedTransport retries connection establishment once through the uplink.
// TLS and HTTP run after dialing, so their failures cannot trigger recovery.
func RoutedTransport(proxy, direct func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	transport := defaultTransport()
	// A recovered direct connection must not keep bypassing a healthy proxy.
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// net/http detaches dialing from request cancellation to reuse connections.
		// This transport cannot reuse them, and recovery belongs to one download.
		if request, ok := ctx.Value(requestCancellation{}).(context.Context); ok {
			ctx = request
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
