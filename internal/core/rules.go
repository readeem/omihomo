package core

import "github.com/readeem/omihomo/internal/protocol"

// IntegrationRules describes generated routing rules independently of whether
// their category is enabled, so disabled rules remain inspectable.
func IntegrationRules(cfg Config) []Rule {
	var out []Rule
	if len(cfg.Profile.Groups) == 0 {
		return out
	}
	add := func(typ, value, target string, params ...string) {
		out = append(out, Rule{Type: typ, Value: value, Target: target, Params: params, Source: protocol.SourceOmihomo})
	}
	if cfg.TailnetProxy {
		add("IP-CIDR", "100.64.0.0/10", "GLOBAL", "no-resolve")
		add("DOMAIN-SUFFIX", "ts.net", "GLOBAL")
	}
	if cfg.Tailscale {
		add("IP-CIDR", "100.64.0.0/10", "DIRECT", "no-resolve")
		add("IP-CIDR", "fd7a:115c:a1e0::/48", "DIRECT", "no-resolve")
		add("DOMAIN-SUFFIX", "tailscale.com", "GLOBAL")
		add("DOMAIN-SUFFIX", "tailscale.io", "GLOBAL")
	}
	return out
}
