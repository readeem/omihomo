package xray

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/serial"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

const (
	directTag    = "direct"
	rejectTag    = "reject"
	dnsTag       = "dns-internal"
	dnsQueryTag  = "dns-query"
	bootstrapTag = "dns-bootstrap"
	tunTag       = "tun"
	globalName   = "GLOBAL"
	directName   = "DIRECT"
	rejectName   = "REJECT"
)

var dnsEndpoints = [...]string{"https://1.1.1.1/dns-query", "https://1.0.0.1/dns-query"}

type route struct {
	match  fields
	target string
	native string
}

type plan struct {
	settings  core.Config
	groups    []core.Group
	byGroup   map[string]core.Group
	types     map[string]string
	tags      map[string]string
	primary   string
	outbounds []*xcore.OutboundHandlerConfig
	routes    []route
	dnsTarget string
	rules     []protocol.Rule
	bootstrap []string
	warnings  []string
	skipped   int
}

func makePlan(cfg core.Config) *plan {
	p := &plan{
		settings: cfg,
		byGroup:  map[string]core.Group{},
		types:    map[string]string{directName: "direct", rejectName: "reject", "REJECT-DROP": "reject"},
		tags:     map[string]string{directName: directTag, rejectName: rejectTag, "REJECT-DROP": rejectTag},
		primary:  directName,
	}
	for i, px := range cfg.Profile.Proxies {
		tag := proxyTag(i)
		out, reason := proxyOutbound(tag, px, cfg.TUN)
		if reason != "" {
			p.warn("Config %q skipped (%s)", px.Name, reason)
			continue
		}
		p.types[px.Name], p.tags[px.Name] = px.Type, tag
		p.outbounds = append(p.outbounds, out)
		server := fields(px.Options).string("server")
		if isDomain(server) {
			p.bootstrap = append(p.bootstrap, "full:"+strings.ToLower(server))
		}
	}
	p.setGroups(cfg)
	p.makeRoutes(cfg)
	if cfg.TUNRedirect {
		p.warn("TUN acceleration is unavailable with Xray")
	}
	if p.skipped > len(p.warnings) {
		p.warnings = append(p.warnings, fmt.Sprintf("%d more Xray compatibility warnings", p.skipped-len(p.warnings)))
	}
	return p
}

func (p *plan) warn(format string, args ...any) {
	p.skipped++
	if len(p.warnings) < 64 {
		p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
	}
}

func (p *plan) setGroups(cfg core.Config) {
	for _, g := range cfg.Profile.Groups {
		if g.Type != "select" || g.Name == globalName || p.tags[g.Name] != "" {
			p.warn("Group %q skipped (unsupported group type %s)", g.Name, g.Type)
			continue
		}
		if g.Options["disable-udp"] == true {
			p.warn("Group %q skipped (cannot disable UDP for an Xray group)", g.Name)
			continue
		}
		p.byGroup[g.Name] = g
	}

	visited := map[string]bool{}
	var usable func(name string, depth int) bool
	usable = func(name string, depth int) bool {
		if _, ok := p.tags[name]; ok {
			return true
		}
		g, ok := p.byGroup[name]
		if !ok || depth > 64 {
			return false
		}
		if visited[name] {
			return len(g.Members) > 0
		}
		visited[name] = true
		// A lookup made while following this group's own members must
		// treat the group as empty, so a back edge cannot survive.
		members := g.Members
		g.Members = nil
		p.byGroup[name] = g
		for _, member := range members {
			if usable(member, depth+1) {
				g.Members = append(g.Members, member)
			}
		}
		p.byGroup[name] = g
		if len(g.Members) == 0 {
			p.warn("Group %q skipped (no compatible configs)", name)
			delete(p.byGroup, name)
			return false
		}
		return true
	}
	for _, g := range cfg.Profile.Groups {
		if _, ok := p.byGroup[g.Name]; ok && usable(g.Name, 0) {
			p.types[g.Name] = g.Type
			p.groups = append(p.groups, p.byGroup[g.Name])
		}
	}

	if len(cfg.Profile.Proxies) > 0 || len(cfg.Profile.Groups) > 0 {
		p.primary = rejectName
		for _, px := range cfg.Profile.Proxies {
			if _, ok := p.tags[px.Name]; ok {
				p.primary = px.Name
				break
			}
		}
	}
	if len(p.groups) > 0 {
		p.primary = p.groups[0].Name
		for _, g := range p.groups {
			if g.Options["hidden"] != true {
				p.primary = g.Name
				break
			}
		}
	}
	if _, ok := p.byGroup[cfg.PrimaryGroup]; ok {
		p.primary = cfg.PrimaryGroup
	} else if cfg.PrimaryGroup != "" {
		p.warn("Primary group %q is unavailable with Xray", cfg.PrimaryGroup)
	}
}

func (p *plan) member(g core.Group, selections map[string]string) string {
	if selected := selections[g.Name]; slices.Contains(g.Members, selected) {
		return selected
	}
	return g.Members[0]
}

func (p *plan) resolve(target string, selections map[string]string) string {
	if target == globalName {
		target = p.primary
	}
	for depth := 0; depth <= len(p.groups); depth++ {
		if tag, ok := p.tags[target]; ok {
			return tag
		}
		if g, ok := p.byGroup[target]; ok {
			target = p.member(g, selections)
		} else {
			break
		}
	}
	return rejectTag
}

func (p *plan) routing(selections map[string]string) (*router.Config, error) {
	cfg := &conf.RouterConfig{}
	for _, r := range p.routes {
		tag := r.native
		if tag == "" {
			tag = p.resolve(r.target, selections)
		}
		m := fields{"type": "field", "outboundTag": tag}
		maps.Copy(m, r.match)
		cfg.RuleList = append(cfg.RuleList, *raw(m))
	}
	return cfg.Build()
}

func (p *plan) build(port int, selections map[string]string) (*xcore.Config, error) {
	routing, err := p.routing(selections)
	if err != nil {
		return nil, fmt.Errorf("%w: Xray rejected the translated routing rules", core.ErrInvalid)
	}
	native := conf.Config{
		LogConfig: &conf.LogConfig{LogLevel: "none"},
		DNSConfig: &conf.DNSConfig{Tag: dnsQueryTag, UseSystemHosts: false, DisableFallbackIfMatch: true},
		Stats:     &conf.StatsConfig{},
		Policy:    &conf.PolicyConfig{System: &conf.SystemPolicy{StatsOutboundUplink: true, StatsOutboundDownlink: true}},
	}
	for _, endpoint := range dnsEndpoints {
		native.DNSConfig.Servers = append(native.DNSConfig.Servers, &conf.NameServerConfig{Address: address(endpoint)})
	}
	if len(p.bootstrap) != 0 {
		native.DNSConfig.Servers = append(native.DNSConfig.Servers, &conf.NameServerConfig{
			Address: address(dnsEndpoints[0]), Domains: p.bootstrap,
			Tag: bootstrapTag, SkipFallback: true, FinalQuery: true,
		})
	}
	if p.settings.Tailscale {
		native.DNSConfig.Servers = append(native.DNSConfig.Servers, &conf.NameServerConfig{
			Address: address("100.100.100.100"), Domains: []string{"domain:ts.net"},
			Tag: bootstrapTag, SkipFallback: true, FinalQuery: true,
		})
	}
	if p.settings.TUN {
		native.InboundConfigs = append(native.InboundConfigs, conf.InboundDetourConfig{
			Protocol: "tun", Tag: tunTag, SniffingConfig: sniffing(),
			Settings: raw(fields{"name": tunDevice, "gateway": tunGateways, "autoSystemDnsToGateway": true}),
		})
	}
	cfg, err := native.Build()
	if err != nil {
		return nil, fmt.Errorf("%w: Xray could not build the local proxy: %w", core.ErrInvalid, err)
	}
	cfg.App = append(cfg.App, serial.ToTypedMessage(routing))
	for _, listener := range []struct {
		tag     string
		port    int
		enabled bool
		udp     bool
	}{
		{"proxy", port, port != 0, true},
		{"tailscale", 7899, p.settings.Tailscale, false},
	} {
		if !listener.enabled {
			continue
		}
		inbound, err := loopback(listener.tag, listener.port, listener.udp)
		if err != nil {
			return nil, fmt.Errorf("build the Xray %s listener: %w", listener.tag, err)
		}
		cfg.Inbound = append(cfg.Inbound, inbound)
	}
	for _, out := range []struct {
		tag, kind string
		options   fields
	}{
		{rejectTag, "blackhole", nil},
		{directTag, "freedom", nil},
		{dnsTag, "dns", nil},
	} {
		handler, err := buildOutbound(out.tag, out.kind, out.options, socketSettings(p.settings.TUN))
		if err != nil {
			return nil, fmt.Errorf("build Xray's %s outbound: %w", out.tag, err)
		}
		cfg.Outbound = append(cfg.Outbound, handler)
	}
	cfg.Outbound = append(cfg.Outbound, p.outbounds...)
	return cfg, nil
}

func sniffing() *conf.SniffingConfig {
	return &conf.SniffingConfig{Enabled: true, DestOverride: []string{"http", "tls", "quic"}, RouteOnly: true}
}
