package mihomo

import (
	"net/netip"
	"slices"
	"testing"

	C "github.com/metacubex/mihomo/constant"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestTheCoreOwnsEverythingButTheProfile(t *testing.T) {
	cfg := testConfig()
	cfg.Profile.Proxies[0].Options = map[string]any{"name": "Spoofed", "type": "http"}
	raw, _, err := rawConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if raw.ExternalController != "" || raw.ExternalControllerUnix != "" || raw.Secret != "" ||
		raw.ExternalUIURL != "" || raw.AllowLan || raw.IPTables.Enable || raw.GeoAutoUpdate {
		t.Errorf("the config opens something up: %+v", raw)
	}
	if raw.GeoXUrl.GeoIp != "" || raw.GeoXUrl.Mmdb != "" || raw.GeoXUrl.GeoSite != "" || raw.GeoXUrl.ASN != "" {
		t.Errorf("mihomo may fetch geodata itself: %+v", raw.GeoXUrl)
	}
	if raw.Proxy[0]["name"] != "Local" || raw.Proxy[0]["type"] != "direct" {
		t.Errorf("options overrode the proxy's name and type: %v", raw.Proxy[0])
	}
	if raw.MixedPort != mixedPort || len(raw.Listeners) != 0 {
		t.Errorf("inbounds = %d, %v; want only the local proxy port", raw.MixedPort, raw.Listeners)
	}
}

func TestTUNShape(t *testing.T) {
	bypass := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct {
		redirect, tailnet bool
		stack             C.TUNStack
		ruleIndex         int
	}{
		{false, false, C.TunGvisor, 0},
		{true, false, C.TunMixed, 0},
		{false, true, C.TunGvisor, tailnetTUNRuleIndex},
	} {
		cfg := testConfig()
		cfg.TUN, cfg.TUNRedirect, cfg.TailnetProxy, cfg.TUNBypass = true, tc.redirect, tc.tailnet, bypass
		raw, _, err := rawConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		tun := raw.Tun
		if !tun.Enable || tun.Device != tunDevice || !tun.AutoRoute || tun.AutoRedirect != tc.redirect ||
			tun.Stack != tc.stack || tun.IPRoute2RuleIndex != tc.ruleIndex ||
			!slices.Equal(tun.RouteExcludeAddress, bypass) || !slices.Equal(tun.ExcludeInterface, []string{tailscaleInterface}) {
			t.Errorf("redirect=%v tailnet=%v: tun = %+v", tc.redirect, tc.tailnet, tun)
		}
	}
}

func TestTailscaleListener(t *testing.T) {
	cfg := testConfig()
	cfg.Tailscale = true
	raw, _, err := rawConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Listeners) != 1 || raw.Listeners[0]["listen"] != "127.0.0.1" || raw.Listeners[0]["port"] != tailscalePort {
		t.Errorf("listeners = %v, want Omihomo's loopback listener for tailscaled", raw.Listeners)
	}
}

func TestPrimaryGroupBacksGlobal(t *testing.T) {
	groups := []core.Group{{Name: "Auto", Type: "url-test"}, {Name: "Proxy", Type: "select"}, {Name: "Other", Type: "select"}}
	for _, tc := range []struct{ want, got string }{
		{"", "Proxy"}, {"Other", "Other"}, {"Auto", "Auto"}, {"Gone", "Proxy"},
	} {
		if got := primaryGroup(groups, tc.want); got != tc.got {
			t.Errorf("primary %q = %q, want %q", tc.want, got, tc.got)
		}
	}
	if got := primaryGroup(groups[:1], ""); got != "Auto" {
		t.Errorf("without a select group = %q, want the first group", got)
	}

	cfg := testConfig()
	cfg.PrimaryGroup = "Auto"
	cfg.TailnetProxy = true
	raw, rules, err := rawConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	global := raw.ProxyGroup[0]
	if global["name"] != globalGroup || !slices.Equal(global["proxies"].([]any), []any{"Auto"}) {
		t.Errorf("first group = %v, want GLOBAL on the primary group", global)
	}
	for _, g := range raw.ProxyGroup[1:] {
		if g["name"] == globalGroup {
			t.Errorf("the profile's own GLOBAL survived: %v", g)
		}
	}
	if rules[1].Source != protocol.SourceOmihomo || rules[1].Target != globalGroup {
		t.Errorf("rules = %+v, want the tailnet rules after the owner's", rules)
	}

	cfg.Profile.Groups = nil
	cfg.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "DIRECT", Source: protocol.SourceSubscription}}
	raw, rules, err = rawConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.ProxyGroup) != 0 || len(rules) != 1 {
		t.Errorf("without groups: groups %v, rules %+v; want no GLOBAL and no rules through it", raw.ProxyGroup, rules)
	}
}

func TestRuleLine(t *testing.T) {
	for _, tc := range []struct {
		rule core.Rule
		want string
	}{
		{core.Rule{Type: "MATCH", Target: "Proxy"}, "MATCH,Proxy"},
		{core.Rule{Type: "IP-CIDR", Value: "10.0.0.0/8", Target: "DIRECT", Params: []string{"no-resolve"}}, "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve"},
		{core.Rule{Type: "AND", Value: "((DOMAIN,a.com),(NETWORK,UDP))", Target: "REJECT"}, "AND,((DOMAIN,a.com),(NETWORK,UDP)),REJECT"},
	} {
		if got, err := ruleLine(tc.rule); err != nil || got != tc.want {
			t.Errorf("%+v = %q, %v; want %q", tc.rule, got, err, tc.want)
		}
	}
	for _, r := range []core.Rule{
		{Type: "DOMAIN", Value: "example.com,DIRECT", Target: "REJECT"},
		{Type: "DOMAIN", Value: "a.com", Target: "Proxy,no-resolve"},
		{Type: "DOMAIN,a.com", Target: "Proxy"},
		{Type: "DOMAIN", Value: "a.com", Target: ""},
		{Type: "DOMAIN", Value: "", Target: "Proxy"},
		{Type: "DOMAIN", Value: "a.com\nMATCH,REJECT", Target: "Proxy"},
		{Type: "DOMAIN", Value: "a.com", Target: "Proxy", Params: []string{"src,x"}},
		{Type: "DOMAIN", Value: "a.com", Target: "Proxy", Params: []string{"DIRECT"}},
		{Type: "AND", Value: "((DOMAIN,a.com))", Target: "Proxy", Params: []string{"no-resolve"}},
		{Type: "MATCH", Value: "x", Target: "Proxy"},
	} {
		if got, err := ruleLine(r); err == nil {
			t.Errorf("%+v was written as %q", r, got)
		}
	}
}

func TestGroupNumbersAreBounded(t *testing.T) {
	for _, v := range []any{-1, int64(-5), uint64(1 << 63), 1e18, 0.5, "300", 7*24*60*60 + 1} {
		g := core.Group{Name: "Auto", Type: "url-test", Options: map[string]any{"interval": v}}
		if err := checkGroupNumbers(g); err == nil {
			t.Errorf("interval %v (%T) passed", v, v)
		}
	}
	for _, v := range []any{0, 300, int64(300), float64(300)} {
		g := core.Group{Name: "Auto", Type: "url-test", Options: map[string]any{"interval": v, "timeout": 5000, "tolerance": 50}}
		if err := checkGroupNumbers(g); err != nil {
			t.Errorf("interval %v (%T): %v", v, v, err)
		}
	}
}
