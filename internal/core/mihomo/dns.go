package mihomo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/constant"
	engineDNS "github.com/metacubex/mihomo/dns"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/miekg/dns"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/uplink"
)

type uplinkDNS struct {
	constant.ProxyAdapter
	network uplink.Network
}

func (d *uplinkDNS) DialContext(ctx context.Context, _ *constant.Metadata) (constant.Conn, error) {
	conn, err := d.network.DialDNS(ctx)
	if err != nil {
		return nil, err
	}
	return outbound.NewConn(conn, d), nil
}

type bootstrapDNS struct {
	normal   resolver.Resolver
	provider resolver.Resolver
	mu       sync.Mutex
	active   map[dns.Question]bool
}

func (d *bootstrapDNS) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	if len(query.Question) != 1 {
		return nil, fmt.Errorf("bootstrap DNS needs one question")
	}
	question := query.Question[0]
	d.mu.Lock()
	if d.active[question] {
		d.mu.Unlock()
		return d.provider.ExchangeContext(ctx, query)
	}
	d.active[question] = true
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.active, question); d.mu.Unlock() }()
	// Mihomo resets contexts inside DNS singleflight. A repeated in-flight
	// question must bootstrap independently rather than wait on its own proxy.
	attempt, cancel := context.WithTimeout(ctx, uplink.Timeout)
	answer, err := d.normal.ExchangeContext(attempt, query)
	cancel()
	if err == nil || ctx.Err() != nil || !uplink.Recoverable(err) {
		return answer, err
	}
	return d.provider.ExchangeContext(ctx, query)
}
func (d *bootstrapDNS) Address() string  { return "omihomo-bootstrap" }
func (d *bootstrapDNS) ResetConnection() {}

type bootstrapResolver struct{ client *bootstrapDNS }

func (r bootstrapResolver) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return lookupIPs(ctx, host, engineDNS.NewResolverFromClient(r.client))
}
func (r bootstrapResolver) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return engineDNS.NewResolverFromClient(r.client).LookupIPv4(ctx, host)
}
func (r bootstrapResolver) LookupIPv6(ctx context.Context, host string) ([]netip.Addr, error) {
	return engineDNS.NewResolverFromClient(r.client).LookupIPv6(ctx, host)
}
func (r bootstrapResolver) ResolveECH(ctx context.Context, host string) ([]byte, error) {
	return engineDNS.NewResolverFromClient(r.client).ResolveECH(ctx, host)
}
func (r bootstrapResolver) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	return r.client.ExchangeContext(ctx, query)
}
func (r bootstrapResolver) Invalid() bool    { return true }
func (r bootstrapResolver) ClearCache()      {}
func (r bootstrapResolver) ResetConnection() {}

// DialDownload resolves the destination locally with application DNS and uses
// the primary exit, regardless of traffic rules. The synchronous dial exposes
// connectivity failures before HTTPS begins, unlike the inner tunnel pipe.
func (c *Core) DialDownload(ctx context.Context, network, address string) (net.Conn, error) {
	if _, _, err := c.state(); err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: err}
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("downloads require TCP")
	}
	metadata := &constant.Metadata{NetWork: constant.TCP, Type: constant.INNER}
	if err := metadata.SetRemoteAddress(address); err != nil {
		return nil, err
	}
	if !metadata.Resolved() {
		ips, err := lookupIPs(ctx, metadata.Host, resolver.DefaultResolver)
		if err != nil {
			return nil, err
		}
		metadata.DstIP = ips[0]
	}
	// HTTP retains the original hostname for SNI, certificates and Host.
	metadata.Host = ""
	proxy := tunnel.Proxies()[globalGroup]
	if proxy == nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: core.ErrNotRunning}
	}
	return proxy.DialContext(ctx, metadata)
}

func newBootstrapResolver(normal, provider resolver.Resolver) bootstrapResolver {
	return bootstrapResolver{client: &bootstrapDNS{normal: normal, provider: provider, active: map[dns.Question]bool{}}}
}

// Mihomo's combined LookupIP replaces both underlying failures with
// ErrIPNotFound. Keep them so TLS/authentication errors cannot permit recovery.
func lookupIPs(ctx context.Context, host string, r resolver.Resolver) ([]netip.Addr, error) {
	if r == nil {
		return nil, &net.OpError{Op: "lookup", Err: core.ErrNotRunning}
	}
	ips, first := r.LookupIPv4(ctx, host)
	if len(ips) > 0 {
		return ips, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	ips, second := r.LookupIPv6(ctx, host)
	if len(ips) > 0 {
		return ips, nil
	}
	if first == nil && second == nil {
		return nil, resolver.ErrIPNotFound
	}
	return nil, errors.Join(first, second)
}
