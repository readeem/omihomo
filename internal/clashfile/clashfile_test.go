package clashfile

import (
	"slices"
	"testing"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestParseRule(t *testing.T) {
	for _, tc := range []struct {
		line string
		want core.Rule
	}{
		{"DOMAIN-SUFFIX,example.com,Proxy", core.Rule{Type: "DOMAIN-SUFFIX", Value: "example.com", Target: "Proxy", Params: []string{}}},
		{"ip-cidr,10.0.0.0/8,DIRECT,no-resolve", core.Rule{Type: "IP-CIDR", Value: "10.0.0.0/8", Target: "DIRECT", Params: []string{"no-resolve"}}},
		{"MATCH,Proxy", core.Rule{Type: "MATCH", Target: "Proxy"}},
		{"AND,((DOMAIN,a.com),(NETWORK,UDP)),REJECT", core.Rule{Type: "AND", Value: "((DOMAIN,a.com),(NETWORK,UDP))", Target: "REJECT"}},
		{"DOMAIN-REGEX,^a{1,3}\\.com$,Proxy", core.Rule{Type: "DOMAIN-REGEX", Value: "^a{1,3}\\.com$", Target: "Proxy"}},
	} {
		got, err := ParseRule(tc.line)
		if err != nil {
			t.Errorf("%s: %v", tc.line, err)
			continue
		}
		if got.Type != tc.want.Type || got.Value != tc.want.Value || got.Target != tc.want.Target || !slices.Equal(got.Params, tc.want.Params) {
			t.Errorf("%s = %+v, want %+v", tc.line, got, tc.want)
		}
	}
	for _, line := range []string{"", "MATCH", "MATCH,", "DOMAIN,example.com", "DOMAIN,example.com,", "AND,REJECT", "DOMAIN,,Proxy"} {
		if r, err := ParseRule(line); err == nil {
			t.Errorf("%q parsed as %+v", line, r)
		}
	}
}

func TestParse(t *testing.T) {
	p, skipped, err := Parse([]byte(`
mixed-port: 1080
external-controller: 0.0.0.0:9090
proxies:
  - {name: Tokyo, type: vless, server: tokyo.example, port: 443, uuid: 00000000-0000-0000-0000-000000000000}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo, DIRECT]}
  - {name: Remote, type: select, use: [provider]}
rule-providers:
  ads: {type: inline, behavior: domain, payload: [+.ads.example]}
  far: {type: http, behavior: domain, url: "https://rules.example/far.yaml"}
rules:
  - RULE-SET,ads,REJECT
  - RULE-SET,far,DIRECT
  - MATCH,Proxy
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Proxies) != 1 || p.Proxies[0].Name != "Tokyo" || p.Proxies[0].Type != "vless" ||
		p.Proxies[0].Options["server"] != "tokyo.example" || p.Proxies[0].Options["name"] != nil {
		t.Errorf("proxies = %+v", p.Proxies)
	}
	if len(p.Groups) != 2 || !slices.Equal(p.Groups[0].Members, []string{"Tokyo", "DIRECT"}) || p.Groups[1].Options["use"] != nil {
		t.Errorf("groups = %+v", p.Groups)
	}
	if len(p.RuleSets) != 1 || p.RuleSets[0].Name != "ads" || !slices.Equal(p.RuleSets[0].Payload, []string{"+.ads.example"}) {
		t.Errorf("rule sets = %+v", p.RuleSets)
	}
	var rules []string
	for _, r := range p.Rules {
		if r.Source != protocol.SourceSubscription {
			t.Errorf("rule %+v is not the subscription's", r)
		}
		rules = append(rules, r.Type+","+r.Value)
	}
	if !slices.Equal(rules, []string{"RULE-SET,ads", "MATCH,"}) {
		t.Errorf("rules = %v", rules)
	}
	if len(skipped) != 3 {
		t.Errorf("skipped = %q, want the provider use, the http provider and its rule", skipped)
	}
}

func TestParseRefusesWhatItCannotName(t *testing.T) {
	for _, doc := range []string{
		"proxies: [{type: vless}]",
		"proxy-groups: [{name: Proxy}]",
		"proxy-groups: [{name: Proxy, type: select, proxies: [{name: x}]}]",
		"rules: [MATCH]",
		"proxies: {not: a list}",
	} {
		if _, _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s parsed", doc)
		}
	}
}
