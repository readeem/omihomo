package mihomo

import (
	"context"
	"net"
	"net/netip"
	"strconv"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/constant"

	"github.com/readeem/omihomo/internal/core/xray"
)

func (rt *runtime) useXray(engine *xray.Outbounds) {
	rt.xray = engine
	for _, entry := range rt.cfg.Profile.Proxies {
		native := engine.Get(entry.Name)
		if native == nil {
			continue
		}
		// Groups and their health checks retain these proxy objects when
		// parsed. Swap their adapters before publishing the configuration.
		p := rt.parsed.Proxies[entry.Name].(sniffedProxy).Proxy.(*adapter.Proxy)
		p.ProxyAdapter = &preferredOutbound{
			ProxyAdapter: p.ProxyAdapter.(outbound.ProxyAdapter),
			native:       native,
		}
	}
}

type preferredOutbound struct {
	outbound.ProxyAdapter
	native *xray.Outbound
}

func (p *preferredOutbound) DialContext(ctx context.Context, metadata *constant.Metadata) (constant.Conn, error) {
	target := sniffedDestination(metadata)
	host := target.Host
	if host == "" {
		host = target.DstIP.String()
	}
	if ctx.Value(bootstrapQuery{}) != nil {
		ctx = xray.WithBootstrapLookup(ctx)
	}
	conn, err := p.native.DialContext(ctx, host, target.DstPort)
	if err != nil {
		return nil, err
	}
	return outbound.NewConn(conn, p), nil
}

func (p *preferredOutbound) ListenPacketContext(ctx context.Context, metadata *constant.Metadata) (constant.PacketConn, error) {
	target := sniffedDestination(metadata).Clone()
	if err := p.ResolveUDP(ctx, target); err != nil {
		return nil, err
	}
	conn, err := p.native.ListenPacket(ctx, target.UDPAddr())
	if err != nil {
		return nil, err
	}
	return outbound.NewPacketConn(conn, p), nil
}

type xrayNetwork struct{}

var _ xray.Network = xrayNetwork{}

func (xrayNetwork) DialContext(ctx context.Context, address string, options xray.SocketOptions) (net.Conn, error) {
	if options.Bootstrap {
		ctx = context.WithValue(ctx, bootstrapQuery{}, true)
	}
	opts := []dialer.Option{
		dialer.WithResolver(resolver.ProxyServerHostResolver),
		dialer.WithTFO(options.TFO), dialer.WithMPTCP(options.MPTCP),
	}
	switch options.IPVersion {
	case "ipv4":
		opts = append(opts, dialer.WithOnlySingleStack(true))
	case "ipv6":
		opts = append(opts, dialer.WithOnlySingleStack(false))
	case "ipv4-prefer":
		opts = append(opts, dialer.WithPreferIPv4())
	case "ipv6-prefer":
		opts = append(opts, dialer.WithPreferIPv6())
	}
	return dialer.DialContext(ctx, "tcp", address, opts...)
}

func (xrayNetwork) ListenPacket(ctx context.Context, address string, options xray.SocketOptions) (net.PacketConn, *net.UDPAddr, error) {
	if options.Bootstrap {
		ctx = context.WithValue(ctx, bootstrapQuery{}, true)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		r := resolver.ProxyServerHostResolver
		switch options.IPVersion {
		case "ipv4":
			ip, err = resolver.ResolveIPv4WithResolver(ctx, host, r)
		case "ipv6":
			ip, err = resolver.ResolveIPv6WithResolver(ctx, host, r)
		case "ipv4-prefer":
			ip, err = resolver.ResolveIPv4WithResolver(ctx, host, r)
			if err != nil {
				ip, err = resolver.ResolveIPv6WithResolver(ctx, host, r)
			}
		case "ipv6-prefer":
			ip, err = resolver.ResolveIPv6WithResolver(ctx, host, r)
			if err != nil {
				ip, err = resolver.ResolveIPv4WithResolver(ctx, host, r)
			}
		default:
			ip, err = resolver.ResolveIPWithResolver(ctx, host, r)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	remote := netip.AddrPortFrom(ip.Unmap(), uint16(n))
	network := "udp4"
	if remote.Addr().Is6() {
		network = "udp6"
	}
	conn, err := dialer.ListenPacket(ctx, network, "", remote)
	return conn, net.UDPAddrFromAddrPort(remote), err
}
