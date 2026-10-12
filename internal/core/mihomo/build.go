package mihomo

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/metacubex/mihomo/common/orderedmap"
	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
	T "github.com/metacubex/mihomo/tunnel"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

// What Omihomo runs around a subscription: everything here belongs to the
// core, and nothing in a Profile can change it.
const (
	tailscaleListener = "omihomo-tailscale"
	// Tailscale's interface, MagicDNS resolver and domain. Without the first,
	// TUN routes what Tailscale already handles; without the others,
	// dns-hijack swallows the *.ts.net lookups only Tailscale can answer.
	tailscaleInterface = "tailscale0"
	tailscaleDNS       = "100.100.100.100"
	tailscaleDomain    = "+.ts.net"
	// Sending the tailnet through the proxy means TUN has to see it before
	// Tailscale's peer routes do. Tailscale's ip rules sit at 5210-5250 and
	// 5270, and sing-tun takes the index plus up to ten.
	tailnetRange        = "100.64.0.0/10"
	tailnetTUNRuleIndex = 5260
	// mihomo names its adapter Meta, as every other mihomo-based client does.
	tunDevice = "omihomo"
	// auto-redirect hands TCP to the kernel through nftables, and only then
	// may the stack be mixed: mixed without the redirect carries no TCP at
	// all. Without it, gVisor tunnels everything in userspace.
	tunRedirectStack   = C.TunMixed
	tunCompatibleStack = C.TunGvisor
	// The global group, which global mode and Omihomo's own rules use.
	globalGroup = "GLOBAL"
)

var (
	// The loopback proxy for applications that do not use TUN.
	mixedPort = 7890
	// The loopback proxy tailscaled dials when the Tailscale integration is
	// on. It is Omihomo's own listener, so tailscaled's drop-in never depends
	// on the subscription.
	tailscalePort = 7899
)

var defaultNameservers = []string{"https://1.1.1.1/dns-query", "https://1.0.0.1/dns-query"}

// rawConfig translates cfg into what mihomo parses. It never reads a file.
func rawConfig(cfg core.Config) (*config.RawConfig, []protocol.Rule, error) {
	mode, ok := map[string]T.TunnelMode{
		protocol.ModeRule:   T.Rule,
		protocol.ModeGlobal: T.Global,
		protocol.ModeDirect: T.Direct,
	}[cfg.Mode]
	if !ok {
		return nil, nil, fmt.Errorf("unknown mode %q", cfg.Mode)
	}
	p := cfg.Profile

	raw := config.DefaultRawConfig()
	raw.Mode = mode
	raw.MixedPort = mixedPort
	raw.AllowLan = false
	raw.LogLevel = log.WARNING
	raw.FindProcessMode = process.FindProcessAlways
	raw.Profile.StoreSelected = true
	// Geodata comes from the daemon. Without a source, a missing file fails
	// the config instead of mihomo fetching it as root.
	raw.GeoAutoUpdate = false
	raw.GeoXUrl = config.RawGeoXUrl{}
	raw.ExternalUIURL = ""

	raw.DNS.Enable = true
	raw.DNS.EnhancedMode = C.DNSMapping
	raw.DNS.Fallback = []string{}
	raw.DNS.DefaultNameserver = slices.Clone(defaultNameservers)
	raw.DNS.ProxyServerNameserver = slices.Clone(defaultNameservers)
	raw.DNS.NameServer = nil
	for _, ns := range defaultNameservers {
		raw.DNS.NameServer = append(raw.DNS.NameServer, ns+"#"+globalGroup)
	}
	raw.DNS.NameServerPolicy = orderedmap.New[string, any]()
	raw.DNS.NameServerPolicy.Set(tailscaleDomain, tailscaleDNS)

	raw.Tun.Enable = cfg.TUN
	raw.Tun.Device = tunDevice
	raw.Tun.AutoRoute = true
	raw.Tun.AutoDetectInterface = true
	raw.Tun.AutoRedirect = cfg.TUNRedirect
	raw.Tun.Stack = tunCompatibleStack
	if cfg.TUNRedirect {
		raw.Tun.Stack = tunRedirectStack
	}
	raw.Tun.DNSHijack = []string{"any:53"}
	raw.Tun.RouteExcludeAddress = slices.Clone(cfg.TUNBypass)
	raw.Tun.ExcludeInterface = []string{tailscaleInterface}
	if cfg.TailnetProxy {
		raw.Tun.IPRoute2RuleIndex = tailnetTUNRuleIndex
	}

	if cfg.Tailscale {
		raw.Listeners = []map[string]any{{
			"name": tailscaleListener, "type": "mixed", "listen": "127.0.0.1",
			"port": tailscalePort, "udp": false, "users": []any{},
		}}
	}

	for _, px := range p.Proxies {
		m := maps.Clone(px.Options)
		if m == nil {
			m = map[string]any{}
		}
		m["name"], m["type"] = px.Name, px.Type
		raw.Proxy = append(raw.Proxy, m)
	}

	var groups []core.Group
	for _, g := range p.Groups {
		if g.Name != globalGroup {
			groups = append(groups, g)
		}
	}
	primary := primaryGroup(groups, cfg.PrimaryGroup)
	if primary != "" {
		raw.ProxyGroup = append(raw.ProxyGroup, map[string]any{
			"name": globalGroup, "type": "select", "proxies": []any{primary},
		})
	}
	for _, g := range groups {
		if err := checkGroupNumbers(g); err != nil {
			return nil, nil, err
		}
		m := maps.Clone(g.Options)
		if m == nil {
			m = map[string]any{}
		}
		members := make([]any, len(g.Members))
		for i, name := range g.Members {
			members[i] = name
		}
		m["name"], m["type"], m["proxies"] = g.Name, g.Type, members
		raw.ProxyGroup = append(raw.ProxyGroup, m)
	}

	if len(p.RuleSets) > 0 {
		raw.RuleProvider = map[string]map[string]any{}
	}
	for _, rs := range p.RuleSets {
		if _, dup := raw.RuleProvider[rs.Name]; dup {
			return nil, nil, fmt.Errorf("rule set %q is defined twice", rs.Name)
		}
		payload := make([]any, len(rs.Payload))
		for i, line := range rs.Payload {
			payload[i] = line
		}
		raw.RuleProvider[rs.Name] = map[string]any{
			"type": "inline", "behavior": rs.Behavior, "payload": payload,
		}
	}

	rules := withOmihomoRules(p.Rules, cfg, primary)
	listed := make([]protocol.Rule, 0, len(rules))
	for _, r := range rules {
		line, err := ruleLine(r)
		if err != nil {
			return nil, nil, err
		}
		raw.Rule = append(raw.Rule, line)
		listed = append(listed, protocol.Rule{Type: r.Type, Value: r.Value, Target: r.Target, Source: r.Source})
	}
	return raw, listed, nil
}

// groupNumbers bounds the numeric group options. mihomo turns them into
// tickers and durations without checking them, and a health check that
// starts with a bad one panics where nothing can recover it.
var groupNumbers = map[string]int64{
	"interval":         7 * 24 * 60 * 60, // seconds
	"timeout":          5 * 60 * 1000,    // milliseconds
	"max-failed-times": 1000,
	"tolerance":        65535, // milliseconds
}

func checkGroupNumbers(g core.Group) error {
	for key, limit := range groupNumbers {
		v, ok := g.Options[key]
		if !ok {
			continue
		}
		var n int64
		switch v := v.(type) {
		case int:
			n = int64(v)
		case int64:
			n = v
		case uint64:
			n = int64(min(v, uint64(limit)+1))
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("group %q: %s is not a whole number", g.Name, key)
			}
			n = int64(v)
		default:
			return fmt.Errorf("group %q: %s is not a number", g.Name, key)
		}
		if n < 0 || n > limit {
			return fmt.Errorf("group %q: %s must be between 0 and %d", g.Name, key, limit)
		}
	}
	return nil
}

// primaryGroup is the group named want, else the first select group, else
// the first group.
func primaryGroup(groups []core.Group, want string) string {
	if want != "" && slices.ContainsFunc(groups, func(g core.Group) bool { return g.Name == want }) {
		return want
	}
	for _, g := range groups {
		if g.Type == "select" {
			return g.Name
		}
	}
	if len(groups) > 0 {
		return groups[0].Name
	}
	return ""
}

// withOmihomoRules puts the integrations' rules after the owner's leading
// rules and before everything else. They send traffic through the global
// group, so without a primary group there are none.
func withOmihomoRules(rules []core.Rule, cfg core.Config, primary string) []core.Rule {
	var own []core.Rule
	add := func(typ, value, target string, params ...string) {
		own = append(own, core.Rule{Type: typ, Value: value, Target: target, Params: params, Source: protocol.SourceOmihomo})
	}
	if primary != "" && cfg.TailnetProxy {
		add("IP-CIDR", tailnetRange, globalGroup, "no-resolve")
		add("DOMAIN-SUFFIX", "ts.net", globalGroup)
	}
	if primary != "" && cfg.Tailscale {
		add("IP-CIDR", "100.64.0.0/10", "DIRECT", "no-resolve")
		add("IP-CIDR", "fd7a:115c:a1e0::/48", "DIRECT", "no-resolve")
		add("DOMAIN-SUFFIX", "tailscale.com", globalGroup)
		add("DOMAIN-SUFFIX", "tailscale.io", globalGroup)
	}
	lead := 0
	for lead < len(rules) && rules[lead].Source == protocol.SourceUser {
		lead++
	}
	return slices.Concat(rules[:lead], own, rules[lead:])
}

// commaValues are the rule types whose value may hold commas: mihomo reads
// their target from the last field and gives them no params.
var commaValues = []string{"NOT", "OR", "AND", "SUB-RULE", "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX"}

// ruleParams are the trailing flags mihomo knows.
var ruleParams = []string{"no-resolve", "src"}

// ruleLine writes r the way mihomo splits a rule, refusing any rule whose
// fields mihomo would read differently, such as a value whose commas move
// the target.
func ruleLine(r core.Rule) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("rule %s,%s,%s %s", r.Type, r.Value, r.Target, why)
	}
	typ := strings.ToUpper(r.Type)
	for _, f := range append([]string{r.Type, r.Value, r.Target}, r.Params...) {
		if strings.ContainsAny(f, "\r\n") || f != strings.TrimSpace(f) {
			return bad("has a line break or surrounding space")
		}
	}
	if typ == "" || strings.Contains(typ, ",") || r.Target == "" || strings.Contains(r.Target, ",") {
		return bad("is malformed")
	}
	for _, p := range r.Params {
		if !slices.Contains(ruleParams, p) {
			return bad("has an unknown param " + p)
		}
	}
	switch {
	case typ == "MATCH":
		if r.Value != "" || len(r.Params) > 0 {
			return bad("has a value or params, which MATCH takes none of")
		}
		return typ + "," + r.Target, nil
	case slices.Contains(commaValues, typ):
		if r.Value == "" || len(r.Params) > 0 {
			return bad("needs a value and takes no params")
		}
	case r.Value == "" || strings.Contains(r.Value, ","):
		return bad("needs a value without commas")
	}
	return strings.Join(append([]string{typ, r.Value, r.Target}, r.Params...), ","), nil
}

// stoppedConfig is what runs while the proxy is off: no inbounds, no TUN and
// no DNS, so nothing reaches the engine.
func stoppedConfig() *config.RawConfig {
	raw := config.DefaultRawConfig()
	raw.Mode = T.Direct
	raw.LogLevel = log.WARNING
	raw.FindProcessMode = process.FindProcessAlways
	raw.GeoXUrl = config.RawGeoXUrl{}
	raw.ExternalUIURL = ""
	raw.DNS.Enable = false
	raw.Tun.Enable = false
	raw.Tun.Device = tunDevice
	return raw
}
