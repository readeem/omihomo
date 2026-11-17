package xray

import (
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func (p *plan) makeRoutes(cfg core.Config) {
	p.routes = []route{
		{match: fields{"inboundTag": []string{bootstrapTag}}, target: directName},
	}
	p.dnsTarget = cfg.DNSDestination
	if p.dnsTarget == "" {
		p.dnsTarget = globalName
	} else if p.types[p.dnsTarget] == "" {
		p.warn("DNS destination %q is unavailable with Xray (DNS blocked)", p.dnsTarget)
		p.dnsTarget = rejectName
	}
	p.routes = append(p.routes, route{match: fields{"inboundTag": []string{dnsQueryTag}}, target: p.dnsTarget})
	if cfg.TUN {
		p.routes = append(p.routes, route{match: fields{"inboundTag": []string{tunTag}, "port": "53"}, native: dnsTag})
	}
	all := fields{"network": "tcp,udp"}
	switch cfg.Mode {
	case protocol.ModeDirect:
		p.routes = append(p.routes, route{match: all, target: directName})
	case protocol.ModeGlobal:
		p.routes = append(p.routes, route{match: all, target: globalName})
	}

	rules := cfg.Profile.Rules
	if !cfg.OmihomoRulesDisabled {
		lead := 0
		for lead < len(rules) && rules[lead].Source == protocol.SourceUser {
			lead++
		}
		rules = slices.Concat(rules[:lead], core.IntegrationRules(cfg), rules[lead:])
	}
	sets := map[string]core.RuleSet{}
	for _, set := range cfg.Profile.RuleSets {
		sets[set.Name] = set
	}
	type setKey struct{ name, options string }
	compiled := map[setKey][]fields{}
	// Count expanded conditions even when rules share a compiled set. Xray
	// builds one copy of its native matcher for each reference.
	budget := 100_000
	fallback := directName
	for i, r := range rules {
		var matches []fields
		switch {
		case p.types[r.Target] == "" && r.Target != globalName:
		case budget == 0:
			p.warn("Rule %d (%s) skipped (Xray rule limit)", i+1, r.Type)
			if r.Type == "MATCH" {
				fallback = rejectName
			}
			continue
		default:
			if r.Type == "RULE-SET" {
				set := sets[r.Value]
				cost := len(set.Payload)
				if cost == 0 {
					break
				}
				if cost > budget {
					p.warn("Rule %d (%s) skipped (Xray rule limit)", i+1, r.Type)
					continue
				}
				budget -= cost
				key := setKey{r.Value, strings.Join(r.Params, ",")}
				var cached bool
				if matches, cached = compiled[key]; !cached {
					matches = ruleSetMatches(set, r.Params)
					compiled[key] = matches
				}
			} else if match := ruleMatch(r.Type, r.Value, r.Params); match != nil {
				matches = []fields{match}
				budget--
			}
		}
		if len(matches) == 0 {
			p.warn("Rule %d (%s) skipped", i+1, r.Type)
			if r.Type == "MATCH" {
				fallback = rejectName
			}
			continue
		}
		p.rules = append(p.rules, protocol.Rule{Type: r.Type, Value: r.Value, Target: r.Target,
			Params: strings.Join(r.Params, " "), Source: r.Source})
		if cfg.Mode == protocol.ModeRule {
			for _, match := range matches {
				p.routes = append(p.routes, route{match: match, target: r.Target})
			}
		}
	}
	p.routes = append(p.routes, route{match: all, target: fallback})
}

func ruleMatch(typ, value string, params []string) fields {
	noResolve := slices.Equal(params, []string{"no-resolve"})
	src := slices.Equal(params, []string{"src"}) || slices.Equal(params, []string{"no-resolve", "src"}) ||
		slices.Equal(params, []string{"src", "no-resolve"})
	if len(params) > 0 && !noResolve && !src {
		return nil
	}
	switch typ {
	case "MATCH":
		return fields{"network": "tcp,udp"}
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX":
		prefix := map[string]string{"DOMAIN": "full:", "DOMAIN-SUFFIX": "domain:", "DOMAIN-KEYWORD": "keyword:", "DOMAIN-REGEX": "regexp:"}[typ]
		if typ == "DOMAIN-REGEX" {
			if _, err := regexp.Compile(value); err != nil {
				return nil
			}
		} else if !isDomain(value) {
			return nil
		}
		return fields{"domain": []string{prefix + value}}
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "SRC-IP-CIDR6":
		if !noResolve && !src && !strings.HasPrefix(typ, "SRC-") {
			return nil
		}
		ip, err := netip.ParsePrefix(value)
		if err != nil {
			return nil
		}
		key := "ip"
		if src || strings.HasPrefix(typ, "SRC-") {
			key = "sourceIP"
		}
		return fields{key: []string{ip.Masked().String()}}
	case "DST-PORT", "SRC-PORT":
		n, err := strconv.ParseUint(value, 10, 16)
		if err != nil || n == 0 {
			return nil
		}
		key := "port"
		if typ == "SRC-PORT" {
			key = "sourcePort"
		}
		return fields{key: value}
	case "NETWORK":
		value = strings.ToLower(value)
		if value == "tcp" || value == "udp" {
			return fields{"network": value}
		}
	}
	return nil
}

func ruleSetMatches(set core.RuleSet, params []string) []fields {
	var matches []fields
	var domains, ips []string
	for _, entry := range set.Payload {
		switch set.Behavior {
		case "domain":
			value, typ := entry, "full:"
			if suffix, ok := strings.CutPrefix(value, "+."); ok {
				value, typ = suffix, "domain:"
			}
			if !isDomain(value) {
				return nil
			}
			domains = append(domains, typ+value)
		case "ipcidr":
			if !slices.Contains(params, "no-resolve") && !slices.Contains(params, "src") {
				return nil
			}
			ip, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil
			}
			ips = append(ips, ip.Masked().String())
		case "classical":
			parts := strings.Split(entry, ",")
			if len(parts) < 2 {
				return nil
			}
			m := ruleMatch(parts[0], parts[1], slices.Concat(parts[2:], params))
			if m == nil {
				return nil
			}
			matches = append(matches, m)
		}
	}
	if len(domains) != 0 {
		matches = append(matches, fields{"domain": domains})
	}
	if len(ips) != 0 {
		ipField := "ip"
		if slices.Contains(params, "src") {
			ipField = "sourceIP"
		}
		matches = append(matches, fields{ipField: ips})
	}
	return matches
}
