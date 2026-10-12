// Package clashfile reads the part of a Clash-format YAML file a
// core.Profile models: proxies, proxy groups, rules and inline rule
// providers. It lets a development daemon run a real subscription before the
// daemon downloads and allowlists its own; it allowlists nothing, so only
// root's own files belong in it.
package clashfile

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

type file struct {
	Proxies       []map[string]any          `yaml:"proxies"`
	Groups        []map[string]any          `yaml:"proxy-groups"`
	Rules         []string                  `yaml:"rules"`
	RuleProviders map[string]map[string]any `yaml:"rule-providers"`
}

// Parse reads data into a profile whose rules are all the subscription's.
// What the profile cannot carry is left out and named in skipped: proxy
// providers, which a group's "use" names, and rule providers that are not
// inline, along with the RULE-SET rules that match against them.
func Parse(data []byte) (p core.Profile, skipped []string, err error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return core.Profile{}, nil, fmt.Errorf("read the YAML: %w", err)
	}
	for i, m := range f.Proxies {
		name, typ, err := nameAndType(m)
		if err != nil {
			return core.Profile{}, nil, fmt.Errorf("proxy %d: %w", i+1, err)
		}
		opts := maps.Clone(m)
		delete(opts, "name")
		delete(opts, "type")
		p.Proxies = append(p.Proxies, core.Proxy{Name: name, Type: typ, Options: opts})
	}
	for i, m := range f.Groups {
		name, typ, err := nameAndType(m)
		if err != nil {
			return core.Profile{}, nil, fmt.Errorf("proxy group %d: %w", i+1, err)
		}
		opts := maps.Clone(m)
		var members []string
		if raw, ok := opts["proxies"].([]any); ok {
			for _, v := range raw {
				s, ok := v.(string)
				if !ok {
					return core.Profile{}, nil, fmt.Errorf("proxy group %q: a member is not a name", name)
				}
				members = append(members, s)
			}
		}
		if _, ok := opts["use"]; ok {
			skipped = append(skipped, fmt.Sprintf("the proxy providers group %q uses", name))
		}
		for _, k := range []string{"name", "type", "proxies", "use"} {
			delete(opts, k)
		}
		p.Groups = append(p.Groups, core.Group{Name: name, Type: typ, Members: members, Options: opts})
	}

	inline := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(f.RuleProviders)) {
		m := f.RuleProviders[name]
		behavior, _ := m["behavior"].(string)
		raw, isList := m["payload"].([]any)
		if m["type"] != "inline" || !isList {
			skipped = append(skipped, fmt.Sprintf("rule provider %q, which is not inline", name))
			continue
		}
		set := core.RuleSet{Name: name, Behavior: behavior}
		for _, v := range raw {
			s, ok := v.(string)
			if !ok {
				return core.Profile{}, nil, fmt.Errorf("rule provider %q: a payload line is not a string", name)
			}
			set.Payload = append(set.Payload, s)
		}
		inline[name] = true
		p.RuleSets = append(p.RuleSets, set)
	}

	for _, line := range f.Rules {
		r, err := ParseRule(line)
		if err != nil {
			return core.Profile{}, nil, err
		}
		if r.Type == "RULE-SET" && !inline[r.Value] {
			skipped = append(skipped, fmt.Sprintf("rule %q", line))
			continue
		}
		r.Source = protocol.SourceSubscription
		p.Rules = append(p.Rules, r)
	}
	return p, skipped, nil
}

func nameAndType(m map[string]any) (name, typ string, err error) {
	name, _ = m["name"].(string)
	typ, _ = m["type"].(string)
	if name == "" || typ == "" {
		return "", "", fmt.Errorf("no name or type")
	}
	return name, typ, nil
}

// commaValues are the rule types whose value may hold commas, as mihomo
// reads them: the target is the last field, and there are no params.
var commaValues = []string{"NOT", "OR", "AND", "SUB-RULE", "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX"}

// ParseRule splits a rule line such as "DOMAIN-SUFFIX,example.com,Proxy" or
// "AND,((DOMAIN,a.com),(NETWORK,UDP)),REJECT" the way mihomo does. Source is
// left empty.
func ParseRule(line string) (core.Rule, error) {
	fields := strings.Split(line, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	typ := strings.ToUpper(fields[0])
	switch {
	case typ == "MATCH" && len(fields) == 2 && fields[1] != "":
		return core.Rule{Type: typ, Target: fields[1]}, nil
	case slices.Contains(commaValues, typ) && len(fields) >= 3 && fields[len(fields)-1] != "":
		return core.Rule{Type: typ, Value: strings.Join(fields[1:len(fields)-1], ","), Target: fields[len(fields)-1]}, nil
	case typ != "MATCH" && !slices.Contains(commaValues, typ) && len(fields) >= 3 && fields[1] != "" && fields[2] != "":
		return core.Rule{Type: typ, Value: fields[1], Target: fields[2], Params: fields[3:]}, nil
	}
	return core.Rule{}, fmt.Errorf("rule %q is malformed", line)
}
