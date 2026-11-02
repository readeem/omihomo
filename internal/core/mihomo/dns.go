package mihomo

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/constant"
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

type bootstrapQuery struct{}

type bootstrapDNS struct {
	normal   resolver.Resolver
	provider resolver.Resolver
}

func (d *bootstrapDNS) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	if ctx.Value(bootstrapQuery{}) != nil {
		return d.provider.ExchangeContext(ctx, query)
	}
	answer, err := d.normal.ExchangeContext(context.WithValue(ctx, bootstrapQuery{}, true), query)
	if err == nil || ctx.Err() != nil || !uplink.Recoverable(err) {
		return answer, err
	}
	return d.provider.ExchangeContext(ctx, query)
}

type questionClient interface {
	ExchangeContext(context.Context, *dns.Msg) (*dns.Msg, error)
}

type bootstrapResolver struct{ client questionClient }

var _ resolver.Resolver = bootstrapResolver{}

func (r bootstrapResolver) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return lookupIPs(ctx, host, r)
}
func (r bootstrapResolver) lookup(ctx context.Context, host string, typ uint16) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is4() == (typ == dns.TypeA) {
			return []netip.Addr{ip.Unmap()}, nil
		}
		return nil, resolver.ErrIPNotFound
	}
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(host), typ)
	answer, err := r.ExchangeContext(ctx, query)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	for _, rr := range answer.Answer {
		switch rr := rr.(type) {
		case *dns.A:
			if typ == dns.TypeA {
				if ip, ok := netip.AddrFromSlice(rr.A); ok {
					ips = append(ips, ip.Unmap())
				}
			}
		case *dns.AAAA:
			if typ == dns.TypeAAAA {
				if ip, ok := netip.AddrFromSlice(rr.AAAA); ok {
					ips = append(ips, ip)
				}
			}
		}
	}
	if len(ips) == 0 {
		return nil, resolver.ErrIPNotFound
	}
	return ips, nil
}
func (r bootstrapResolver) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return r.lookup(ctx, host, dns.TypeA)
}
func (r bootstrapResolver) LookupIPv6(ctx context.Context, host string) ([]netip.Addr, error) {
	return r.lookup(ctx, host, dns.TypeAAAA)
}
func (r bootstrapResolver) ResolveECH(ctx context.Context, host string) ([]byte, error) {
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(host), dns.TypeHTTPS)
	answer, err := r.ExchangeContext(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, rr := range answer.Answer {
		if resource, ok := rr.(*dns.HTTPS); ok {
			for _, value := range resource.Value {
				if config, ok := value.(*dns.SVCBECHConfig); ok {
					return config.ECH, nil
				}
			}
		}
	}
	return nil, errors.New("no ECH config found in DNS records")
}
func (r bootstrapResolver) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	if len(query.Question) != 1 {
		return nil, errors.New("DNS needs one question")
	}
	return r.client.ExchangeContext(ctx, query)
}
func (r bootstrapResolver) Invalid() bool    { return true }
func (r bootstrapResolver) ClearCache()      {}
func (r bootstrapResolver) ResetConnection() {}

// The engine's batch resolver discards all but its first endpoint error and
// resets contexts. Recovery needs every failure and the recursive dial context.
type downloadDNS struct {
	proxy constant.ProxyAdapter
	magic resolver.Resolver
}

func (d downloadDNS) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	if strings.HasSuffix(strings.ToLower(query.Question[0].Name), ".ts.net.") {
		return d.magic.ExchangeContext(ctx, query)
	}
	wire, err := query.Pack()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		metadata := &constant.Metadata{NetWork: constant.TCP, Type: constant.INNER}
		if err := metadata.SetRemoteAddress(address); err != nil {
			return nil, err
		}
		return d.proxy.DialContext(ctx, metadata)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: uplink.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var failures []error
	for _, endpoint := range defaultNameservers {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(wire))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/dns-message")
		response, err := client.Do(request)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("DNS server answered HTTP %d", response.StatusCode)
		}
		if err == nil && len(body) >= 65536 {
			err = errors.New("DNS response exceeds size limit")
		}
		answer := new(dns.Msg)
		if err == nil {
			err = answer.Unpack(body)
		}
		if err == nil && (answer.Id != query.Id || len(answer.Question) != 1 || answer.Question[0] != query.Question[0]) {
			err = errors.New("DNS response does not match question")
		}
		if err == nil {
			return answer, nil
		}
		failures = append(failures, err)
	}
	return nil, errors.Join(failures...)
}

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
		c.mu.Lock()
		dnsResolver := c.downloadDNS
		c.mu.Unlock()
		ips, err := lookupIPs(ctx, metadata.Host, dnsResolver)
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
	return bootstrapResolver{client: &bootstrapDNS{normal: normal, provider: provider}}
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
		return nil, errors.Join(first, ctx.Err())
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
