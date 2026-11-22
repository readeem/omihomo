package daemon

import (
	"maps"
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

// renames carries the renames of p over to next and adds the configs next
// renames: one config of p that next lacks, whose content exactly one config
// new in next carries. A name next uses again is no longer an alias.
func renames(p, next core.Profile) map[string]string {
	gone, added := map[string][]string{}, map[string][]string{}
	for _, px := range p.Proxies {
		if !hasTarget(next, px.Name) {
			gone[px.Revision()] = append(gone[px.Revision()], px.Name)
		}
	}
	for _, px := range next.Proxies {
		if !hasTarget(p, px.Name) {
			added[px.Revision()] = append(added[px.Revision()], px.Name)
		}
	}
	step := map[string]string{}
	for revision, from := range gone {
		if to := added[revision]; len(from) == 1 && len(to) == 1 {
			step[from[0]] = to[0]
		}
	}
	out := map[string]string{}
	for from, to := range p.Renames {
		if renamed, ok := step[to]; ok {
			to = renamed
		}
		out[from] = to
	}
	maps.Copy(out, step)
	maps.DeleteFunc(out, func(from, to string) bool {
		return hasTarget(next, from) || !slices.ContainsFunc(next.Proxies, func(px core.Proxy) bool { return px.Name == to })
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolve is the name p gives what the owner's settings call name.
func resolve(p core.Profile, name string) string {
	if to, ok := p.Renames[name]; ok {
		return to
	}
	return name
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
		if r.Type != "SUB-RULE" {
			r.Target = resolve(p, r.Target)
		}
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

func (d *server) runConfig(s state.Settings, p core.Profile) core.Config {
	rules, _ := compileRules(s.Rules, p)
	if !s.SubscriptionRulesDisabled {
		rules = append(rules, p.Rules...)
	}
	p.Rules = rules
	return core.Config{
		Core: d.preferredCore(s.Core),
		Mode: s.Mode, TUN: s.TUN, TUNRedirect: s.TUNRedirect, Tailscale: s.Tailscale,
		TailnetProxy: s.TailnetProxy, OmihomoRulesDisabled: s.OmihomoRulesDisabled,
		PrimaryGroup: s.PrimaryGroup, DNSDestination: resolve(p, s.DNSDestination), TUNBypass: bypass(s), Profile: p,
	}
}
