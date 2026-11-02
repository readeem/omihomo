package profile

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

// links reads scheme://password@server:port#name lines, base64 or not.
func links(data []byte) ([]map[string]any, error) {
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err == nil {
		data = decoded
	}
	var out []map[string]any
	for line := range strings.Lines(string(data)) {
		u, err := url.Parse(strings.TrimSpace(line))
		if err != nil || u.Scheme == "" || u.Hostname() == "" {
			continue
		}
		port, _ := strconv.Atoi(u.Port())
		out = append(out, map[string]any{"name": u.Fragment, "type": u.Scheme, "server": u.Hostname(),
			"port": port, "password": u.User.Username()})
	}
	if len(out) == 0 {
		return nil, errors.New("no links")
	}
	return out, nil
}

func build(t *testing.T, body string, bodies map[string][]byte) (core.Profile, []string) {
	t.Helper()
	doc, err := Read([]byte(body), links)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	p, dropped, err := doc.Build(bodies)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p, dropped
}

func proxyNames(p core.Profile) []string {
	var out []string
	for _, px := range p.Proxies {
		out = append(out, px.Name)
	}
	return out
}

func ruleLines(p core.Profile) []string {
	var out []string
	for _, r := range p.Rules {
		out = append(out, strings.Join(append([]string{r.Type, r.Value, r.Target}, r.Params...), ","))
	}
	return out
}

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

// A full config keeps its own groups and rules; only what is modelled of
// it survives.
func TestAFullConfigKeepsItsGroupsAndRules(t *testing.T) {
	p, _ := build(t, `
mixed-port: 1080
allow-lan: true
bind-address: "*"
external-controller: 0.0.0.0:9090
external-controller-unix: /tmp/open.sock
secret: chosen
iptables: {enable: true}
tunnels: [tcp,127.0.0.1:6553,example.test:53,Local]
listeners: [{name: open, type: mixed, listen: 0.0.0.0, port: 1081}]
ntp: {enable: true, write-to-system: true}
dns: {listen: 0.0.0.0:53, enhanced-mode: fake-ip}
tun: {enable: true}
hosts: {a.example: 10.0.0.1}
proxies:
  - name: Local
    type: direct
proxy-groups:
  - name: Local select
    type: select
    proxies: [Local]
    interface-name: eth0
    routing-mark: 7
rules:
  - DOMAIN,a.example,DIRECT,no-resolve,weird
  - MATCH,Local select
`, nil)
	if !slices.Equal(proxyNames(p), []string{"Local"}) || len(p.Groups) != 1 || p.Groups[0].Name != "Local select" {
		t.Fatalf("profile %+v", p)
	}
	if len(p.Groups[0].Options) != 0 {
		t.Errorf("group options %v", p.Groups[0].Options)
	}
	if got := ruleLines(p); !slices.Equal(got, []string{"DOMAIN,a.example,DIRECT,no-resolve", "MATCH,,Local select"}) {
		t.Errorf("rules %v", got)
	}
	for _, r := range p.Rules {
		if r.Source != protocol.SourceSubscription {
			t.Errorf("rule %+v is not the subscription's", r)
		}
	}
}

// A rule may send traffic through GLOBAL, which the core always runs.
func TestRulesMayTargetGlobal(t *testing.T) {
	p, _ := build(t, `
proxies:
  - {name: Local, type: direct}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Local, REJECT]}
rules:
  - MATCH,GLOBAL
`, nil)
	if got := ruleLines(p); !slices.Equal(got, []string{"MATCH,,GLOBAL"}) {
		t.Errorf("rules %v", got)
	}
}

// Bare proxies, from share links or a provider-style YAML, run through one
// select group every connection uses.
func TestBareProxiesAreWrapped(t *testing.T) {
	plain := "trojan://pw@a.example:443#Tokyo\ntrojan://pw@b.example:443#Berlin\n"
	for name, body := range map[string]string{
		"links":  plain,
		"base64": base64.StdEncoding.EncodeToString([]byte(plain)),
		"yaml":   "proxies:\n  - {name: Tokyo, type: trojan, server: a.example, port: 443, password: pw}\n  - {name: Berlin, type: trojan, server: b.example, port: 443, password: pw}\n",
	} {
		p, _ := build(t, body, nil)
		if !slices.Equal(proxyNames(p), []string{"Tokyo", "Berlin"}) || len(p.Groups) != 1 ||
			p.Groups[0].Name != "Proxy" || !slices.Equal(p.Groups[0].Members, []string{"Tokyo", "Berlin"}) ||
			!slices.Equal(ruleLines(p), []string{"MATCH,,Proxy"}) {
			t.Errorf("%s: %+v", name, p)
		}
	}
}

func TestUnusableContentIsRejected(t *testing.T) {
	for _, body := range []string{"", "garbage", "\x00\x01", "proxies: INVALID", "rules: {a: b}",
		"proxies:\n  - {name: Mars, type: openvpn, server: a.example, port: 1}\n"} {
		doc, err := Read([]byte(body), links)
		if err == nil {
			_, _, err = doc.Build(nil)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q = %v", body, err)
		}
	}
	// A group that loses every member would reroute the rules that use it.
	doc, err := Read([]byte(`
proxies:
  - {name: Mars, type: openvpn, server: a.example, port: 1}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Mars]}
rules: [MATCH,Proxy]
`), links)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := doc.Build(nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `"Proxy"`) {
		t.Errorf("an emptied group = %v", err)
	}
}

// Providers are downloaded by the daemon over HTTPS and expanded here;
// nothing points the core at a file or a download of its own.
func TestProvidersAreExpanded(t *testing.T) {
	doc, err := Read([]byte(`
proxies:
  - {name: Tokyo, type: trojan, server: a.example, port: 443, password: pw}
proxy-providers:
  remote:
    type: http
    url: https://p.example/list
    path: ./settings.json
    filter: "HK|JP"
    override: {additional-prefix: "[r] "}
  plain: {type: http, url: http://p.example/list}
  local: {type: file, path: /etc/passwd}
  inline:
    type: inline
    payload:
      - {name: Inline, type: trojan, server: i.example, port: 443, password: pw}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo, Missing], use: [remote, plain, local, inline]}
  - {name: All, type: url-test, include-all: true, exclude-filter: Inline, url: https://cp.example/204, interval: "300"}
rule-providers:
  ads: {type: http, behavior: domain, url: https://r.example/ads, path: ./owner}
  text: {type: http, behavior: ipcidr, format: text, url: https://r.example/ip}
  binary: {type: http, behavior: domain, format: mrs, url: https://r.example/b}
  inline: {type: inline, behavior: classical, payload: ["DOMAIN,c.example"]}
  local: {type: file, behavior: domain, path: /etc/hosts}
rules:
  - RULE-SET,ads,REJECT
  - RULE-SET,text,DIRECT
  - RULE-SET,binary,REJECT
  - RULE-SET,local,REJECT
  - RULE-SET,inline,DIRECT
  - SUB-RULE,(NETWORK,tcp),sub
  - DOMAIN,x.example,Nowhere
  - MATCH,Proxy
`), links)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, d := range doc.Downloads() {
		keys = append(keys, d.Key()+"="+d.URL)
	}
	// mrs is not read, so it is not downloaded either.
	if !slices.Equal(keys, []string{"proxy:remote=https://p.example/list", "rule:ads=https://r.example/ads",
		"rule:text=https://r.example/ip"}) {
		t.Fatalf("downloads %v", keys)
	}
	p, dropped, err := doc.Build(map[string][]byte{
		"proxy:remote": []byte("trojan://pw@h.example:443#HK 1\ntrojan://pw@u.example:443#US 1\ntrojan://pw@j.example:443#JP 1\n"),
		"rule:ads":     []byte("payload:\n  - +.ads.example\n"),
		"rule:text":    []byte("# comment\n10.0.0.0/8\n\n192.168.0.0/16\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Providers expand in name order; a group lists them in its own.
	if got := proxyNames(p); !slices.Equal(got, []string{"Tokyo", "Inline", "[r] HK 1", "[r] JP 1"}) {
		t.Errorf("proxies %v", got)
	}
	if g := p.Groups[0]; !slices.Equal(g.Members, []string{"Tokyo", "[r] HK 1", "[r] JP 1", "Inline"}) {
		t.Errorf("group %s members %v", g.Name, g.Members)
	}
	if g := p.Groups[1]; !slices.Equal(g.Members, []string{"Tokyo", "[r] HK 1", "[r] JP 1"}) ||
		g.Options["interval"] != 300 || g.Options["url"] != "https://cp.example/204" {
		t.Errorf("group %s %v %v", g.Name, g.Members, g.Options)
	}
	var sets []string
	for _, rs := range p.RuleSets {
		sets = append(sets, rs.Name+":"+strings.Join(rs.Payload, " "))
	}
	if !slices.Equal(sets, []string{"ads:+.ads.example", "inline:DOMAIN,c.example", "text:10.0.0.0/8 192.168.0.0/16"}) {
		t.Errorf("rule sets %v", sets)
	}
	if got := ruleLines(p); !slices.Equal(got, []string{"RULE-SET,ads,REJECT", "RULE-SET,text,DIRECT", "RULE-SET,inline,DIRECT", "MATCH,,Proxy"}) {
		t.Errorf("rules %v", got)
	}
	if len(dropped) == 0 {
		t.Error("nothing reported as dropped")
	}
	for _, what := range dropped {
		if strings.Contains(what, "pw") {
			t.Errorf("dropped %q quotes a credential", what)
		}
	}
}

func TestProxyNamesStayUsable(t *testing.T) {
	p, _ := build(t, `
proxies:
  - {name: GLOBAL, type: direct}
  - {name: DIRECT, type: direct}
  - {name: "a,b", type: direct}
  - {name: " padded", type: direct}
  - {name: Same, type: direct}
  - {name: Same, type: direct}
proxy-providers:
  inline: {type: inline, payload: [{name: Same, type: direct}]}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Same], use: [inline]}
rules: [MATCH,Proxy]
`, nil)
	if got := proxyNames(p); !slices.Equal(got, []string{"Same", "Same 2"}) {
		t.Errorf("proxies %v", got)
	}
}

func TestSubscriptionNames(t *testing.T) {
	header := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	b64 := func(s string) string { return "base64:" + base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, tc := range []struct {
		h    http.Header
		url  string
		want string
	}{
		{header("Profile-Title", b64("Omega Hangout")), "https://a.example/s", "Omega Hangout"},
		{header("Profile-Title", "../../etc/passwd"), "https://a.example/s", "etc passwd"},
		{header("Profile-Title", "a\x00b\tc"), "https://a.example/s", "abc"},
		{header("Profile-Title", "base64:!!", "Content-Disposition", `attachment; filename="Work.yaml"`), "https://a.example/s", "Work.yaml"},
		{header(), "https://user:pw@a.example:8443/s?token=x", "a.example"},
		{header("Profile-Title", strings.Repeat("x", 100)), "https://a.example/s", strings.Repeat("x", 64)},
	} {
		if got := Name(tc.h, tc.url); got != tc.want {
			t.Errorf("Name(%v) = %q, want %q", tc.h, got, tc.want)
		}
	}
}

func TestUsageAndInterval(t *testing.T) {
	h := http.Header{}
	h.Set("Subscription-Userinfo", "upload=10; download=20;total=30; expire=; bogus")
	u := ReadUsage(h)
	if u.Upload == nil || *u.Upload != 10 || *u.Download != 20 || *u.Total != 30 || u.Expire != nil {
		t.Errorf("usage %+v", u)
	}
	for value, want := range map[string]string{"": "12h0m0s", "6": "6h0m0s", "-1": "12h0m0s", "x": "12h0m0s",
		"99999999999": "8760h0m0s"} {
		h.Set("Profile-Update-Interval", value)
		if got := Interval(h).String(); got != want {
			t.Errorf("interval %q = %s, want %s", value, got, want)
		}
	}
}

// Values that would crash or stall the root daemon are refused.
func TestHostileValuesAreRefused(t *testing.T) {
	for _, m := range []map[string]any{
		{"name": "h", "type": "hysteria", "server": "a.example", "ports": "443,444", "hop-interval": -1, "auth-str": "x", "up": "1", "down": "1"},
		{"name": "h", "type": "hysteria", "server": "a.example", "ports": "443,444", "hop-interval": 1 << 40, "auth-str": "x", "up": "1", "down": "1"},
		{"name": "h2", "type": "hysteria2", "server": "a.example", "ports": "443,444", "hop-interval": "5-99999999999", "password": "x"},
		{"name": "h2", "type": "hysteria2", "server": "a.example", "ports": "443,444", "hop-interval": "-1", "password": "x"},
		{"name": "t", "type": "trojan", "server": "a.example", "port": 443, "password": "x", "smux": map[string]any{"enabled": true, "max-streams": -5}},
	} {
		if _, err := Proxy(m); err == nil {
			t.Errorf("%v was kept", m)
		}
	}
	px, err := Proxy(map[string]any{"name": "t", "type": "trojan", "server": "a.example", "port": 443, "password": "x",
		"certificate": "-----BEGIN/../cache.db", "private-key": "-----BEGIN CERTIFICATE-----\nnot base64!\n-----END CERTIFICATE-----"})
	if err != nil || px.Options["certificate"] != nil || px.Options["private-key"] != nil {
		t.Errorf("a path that looks like PEM was kept: %v %v", px.Options, err)
	}

	p, _ := build(t, `
proxies:
  - {name: Tokyo, type: trojan, server: a.example, port: 443, password: pw}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo]}
rule-providers:
  c: {type: inline, behavior: classical, payload: ["DOMAIN-REGEX,^(a+)+$", "DOMAIN,ok.example"]}
rules:
  - DOMAIN-REGEX,^(a+)+$,REJECT
  - AND,((NETWORK,tcp),(PROCESS-NAME-REGEX,^(a+)+$)),REJECT
  - DOMAIN,foo-regex.example,DIRECT
  - RULE-SET,c,REJECT
  - MATCH,Proxy
`, nil)
	if got := ruleLines(p); !slices.Equal(got, []string{"DOMAIN-REGEX,^(a+)+$,REJECT", "AND,((NETWORK,tcp),(PROCESS-NAME-REGEX,^(a+)+$)),REJECT", "DOMAIN,foo-regex.example,DIRECT", "RULE-SET,c,REJECT", "MATCH,,Proxy"}) {
		t.Errorf("rules %v", got)
	}
	if !slices.Equal(p.RuleSets[0].Payload, []string{"DOMAIN-REGEX,^(a+)+$", "DOMAIN,ok.example"}) {
		t.Errorf("rule set %v", p.RuleSets[0].Payload)
	}
}

// A filter that backtracks forever fails the subscription in bounded time.
func TestCatastrophicFiltersAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("proxies:\n")
	for i := range 200 {
		fmt.Fprintf(&b, "  - {name: %s%d, type: direct}\n", strings.Repeat("a", 30), i)
	}
	b.WriteString("proxy-groups:\n  - {name: All, type: select, include-all: true, filter: '^(a+)+$'}\nrules: [MATCH,All]\n")
	doc, err := Read([]byte(b.String()), links)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if _, _, err := doc.Build(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("build = %v", err)
	}
	if d := time.Since(began); d > 2*filterBudget {
		t.Fatalf("filters ran %s", d)
	}
}

// A provider that is missing or unreadable fails the subscription instead
// of quietly dropping the rules it feeds.
func TestAnUnreadableProviderFailsTheSubscription(t *testing.T) {
	doc, err := Read([]byte(`
proxies:
  - {name: Tokyo, type: trojan, server: a.example, port: 443, password: pw}
proxy-providers:
  remote: {type: http, url: https://p.example/list}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo], use: [remote]}
rule-providers:
  ads: {type: http, behavior: domain, url: https://r.example/ads}
rules:
  - RULE-SET,ads,REJECT
  - MATCH,Proxy
`), links)
	if err != nil {
		t.Fatal(err)
	}
	good := map[string][]byte{"proxy:remote": []byte("trojan://pw@h.example:443#HK\n"), "rule:ads": []byte("payload: [+.ads.example]\n")}
	if _, _, err := doc.Build(good); err != nil {
		t.Fatalf("complete = %v", err)
	}
	for key, body := range map[string][]byte{"proxy:remote": []byte("<html>busy</html>"), "rule:ads": []byte("<html>busy</html>")} {
		bodies := maps.Clone(good)
		bodies[key] = body
		if _, _, err := doc.Build(bodies); !errors.Is(err, ErrInvalid) {
			t.Errorf("unreadable %s = %v", key, err)
		}
		delete(bodies, key)
		if _, _, err := doc.Build(bodies); !errors.Is(err, ErrInvalid) {
			t.Errorf("missing %s = %v", key, err)
		}
	}
}
