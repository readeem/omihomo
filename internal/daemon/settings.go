package daemon

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/state"
)

var (
	// tunBypass is the address space that never leaves through TUN: loopback,
	// the private, CGNAT and link-local ranges, the documentation and multicast
	// blocks, and their IPv6 equivalents. Without it auto-route swallows the
	// LAN, and the router, printers and local DNS stop answering once TUN is up.
	//
	// The multicast and reserved space stops just short of the last address:
	// sing-tun cannot put a range that runs to it in an nftables interval set,
	// and TUN redirect then never starts.
	tunBypass = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		tailnetRange,
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/5"),
		netip.MustParsePrefix("::/127"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("ff00::/9"),
	}

	tailnetRange = netip.MustParsePrefix("100.64.0.0/10")
	// Tailscale's MagicDNS resolver sits inside the tailnet range but must
	// still be answered by Tailscale when the rest of the tailnet is proxied.
	tailscaleDNS = netip.MustParsePrefix("100.100.100.100/32")
)

func bypass(s state.Settings) []netip.Prefix {
	if !s.TailnetProxy {
		return slices.Clone(tunBypass)
	}
	out := slices.DeleteFunc(slices.Clone(tunBypass), func(p netip.Prefix) bool { return p == tailnetRange })
	return append(out, tailscaleDNS)
}

// builtinTargets are the targets every profile has.
var builtinTargets = []string{"DIRECT", "REJECT", "REJECT-DROP"}

// hasTarget reports whether a rule in p may send traffic to name.
func hasTarget(p core.Profile, name string) bool {
	return slices.Contains(builtinTargets, name) ||
		slices.ContainsFunc(p.Groups, func(g core.Group) bool { return g.Name == name }) ||
		slices.ContainsFunc(p.Proxies, func(px core.Proxy) bool { return px.Name == name })
}

func ruleActive(p core.Profile, r protocol.RuleEntry) bool {
	if r.Type == "SUB-RULE" {
		_, ok := p.SubRules[r.Target]
		return ok
	}
	if r.Type == "RULE-SET" && !slices.ContainsFunc(p.RuleSets, func(s core.RuleSet) bool { return s.Name == r.Value }) {
		return false
	}
	return hasTarget(p, r.Target)
}

// compileRules retains saved rules for editing while excluding disabled or
// unavailable entries from the configuration passed to the core.
func compileRules(entries []protocol.RuleEntry, p core.Profile) ([]core.Rule, []protocol.Rule) {
	var effective []core.Rule
	listed := []protocol.Rule{}
	add := func(r protocol.RuleEntry, category string, disabled bool) {
		off := disabled || r.Disabled
		inactive := off || !ruleActive(p, r)
		listed = append(listed, protocol.Rule{Type: r.Type, Value: r.Value, Target: r.Target, Params: r.Params,
			Source: protocol.SourceUser, Index: len(listed) + 1, Category: category, Disabled: r.Disabled, Inactive: inactive})
		switch {
		case disabled:
			listed[len(listed)-1].InactiveReason = "Category disabled"
		case r.Disabled:
			listed[len(listed)-1].InactiveReason = "Rule disabled"
		case inactive:
			listed[len(listed)-1].InactiveReason = "Unavailable target or rule provider"
		}
		if !inactive {
			effective = append(effective, core.Rule{Type: r.Type, Value: r.Value, Target: r.Target, Params: strings.Fields(r.Params), Source: protocol.SourceUser})
		}
	}
	for _, e := range entries {
		if e.Category == nil {
			add(e, "", false)
			continue
		}
		for _, r := range e.Category.Rules {
			add(r, e.Category.ID, e.Disabled)
		}
	}
	return effective, listed
}

func runConfig(s state.Settings, p core.Profile) core.Config {
	rules, _ := compileRules(s.Rules, p)
	if !s.SubscriptionRulesDisabled {
		rules = append(rules, p.Rules...)
	}
	p.Rules = rules
	return core.Config{
		Mode: s.Mode, TUN: s.TUN, TUNRedirect: s.TUNRedirect, Tailscale: s.Tailscale,
		TailnetProxy: s.TailnetProxy, OmihomoRulesDisabled: s.OmihomoRulesDisabled,
		PrimaryGroup: s.PrimaryGroup, DNSDestination: s.DNSDestination, TUNBypass: bypass(s), Profile: p,
	}
}
