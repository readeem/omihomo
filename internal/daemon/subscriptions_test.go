package daemon_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/protocol"
)

// origin is an HTTPS server whose answers a test changes as it goes.
type origin struct {
	*httptest.Server
	mu    sync.Mutex
	pages map[string]page
	hits  map[string]int
}

type page struct {
	status int
	header map[string]string
	body   string
}

func newOrigin(t *testing.T) *origin {
	o := &origin{pages: map[string]page{}, hits: map[string]int{}}
	o.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		p, ok := o.pages[r.URL.Path]
		o.hits[r.URL.Path]++
		o.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		for k, v := range p.header {
			w.Header().Set(k, v)
		}
		if p.status != 0 {
			w.WriteHeader(p.status)
		}
		fmt.Fprint(w, p.body)
	}))
	t.Cleanup(o.Close)
	return o
}

func (o *origin) set(path string, p page) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pages[path] = p
}

func (o *origin) count(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hits[path]
}

// url is a subscription URL on o, with an access token in it.
func (o *origin) url(path string) string { return o.URL + path + "?token=s3cret" }

func (o *origin) opts() []daemon.ConfigOpt {
	return []daemon.ConfigOpt{daemon.WithTransport(o.Client().Transport)}
}

// clock is a settable time.Now.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

const workYAML = `
mixed-port: 1080
proxies:
  - {name: Tokyo, type: trojan, server: tokyo.example, port: 443, password: secret}
  - {name: Berlin, type: trojan, server: berlin.example, port: 443, password: secret}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo, Berlin, DIRECT]}
rules:
  - DOMAIN-SUFFIX,example.com,DIRECT
  - MATCH,Proxy
`

func titled(title string) map[string]string {
	return map[string]string{
		"Profile-Title":           "base64:" + base64.StdEncoding.EncodeToString([]byte(title)),
		"Subscription-Userinfo":   "upload=10; download=20; total=1000; expire=1800000000",
		"Profile-Update-Interval": "6",
	}
}

func names(p core.Profile) (proxies, groups []string) {
	for _, px := range p.Proxies {
		proxies = append(proxies, px.Name)
	}
	for _, g := range p.Groups {
		groups = append(groups, g.Name)
	}
	return proxies, groups
}

func TestSubscriptionsEndToEnd(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{header: titled("Work VPN"), body: workYAML})
	o.set("/home", page{body: "trojan://pw@home.example:443#Home\n"})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)

	sub := call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	if sub.Name != "Work VPN" || !sub.Active || sub.Upload == nil || *sub.Upload != 10 || sub.Interval != 6*3600 || sub.UpdatedAt == 0 {
		t.Fatalf("added %+v", sub)
	}
	if err := callErr(t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")}); err.Code != protocol.CodeConflict {
		t.Fatalf("adding a URL twice = %v", err)
	}
	// A later subscription does not take the active slot.
	home := call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/home")})
	if home.Active || !strings.HasPrefix(o.URL, "https://"+home.Name) {
		t.Fatalf("second subscription %+v, want it named after the host", home)
	}

	st := call[protocol.Status](t, c, protocol.OpStart, nil)
	if st.ActiveSubscription != "Work VPN" {
		t.Fatalf("status = %+v", st)
	}
	cfg, _ := h.core.Config()
	if proxies, groups := names(cfg.Profile); !slices.Equal(proxies, []string{"Tokyo", "Berlin"}) || !slices.Equal(groups, []string{"Proxy"}) {
		t.Fatalf("core runs %v %v", proxies, groups)
	}

	// An update reaches the running core.
	o.set("/work", page{header: titled("Renamed"), body: strings.ReplaceAll(workYAML, "Berlin", "Paris")})
	h.clock.advance(time.Minute)
	sub = call[protocol.Subscription](t, c, protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "Work VPN"})
	cfg, _ = h.core.Config()
	if proxies, _ := names(cfg.Profile); sub.Name != "Work VPN" || !slices.Contains(proxies, "Paris") {
		t.Fatalf("after update: %+v runs %v", sub, proxies)
	}

	// Switching runs the other subscription, wrapped in one group.
	st = call[protocol.Status](t, c, protocol.OpSubscriptionUse, protocol.NameArgs{Name: home.Name})
	cfg, _ = h.core.Config()
	if proxies, groups := names(cfg.Profile); st.ActiveSubscription != home.Name ||
		!slices.Equal(proxies, []string{"Home"}) || !slices.Equal(groups, []string{"Proxy"}) {
		t.Fatalf("after activate: %+v runs %v %v", st, proxies, groups)
	}

	if err := callErr(t, c, protocol.OpSubscriptionRemove, protocol.NameArgs{Name: home.Name}); err.Code != protocol.CodeConflict {
		t.Fatalf("removing the running subscription = %v", err)
	}
	call[protocol.Empty](t, c, protocol.OpSubscriptionRemove, protocol.NameArgs{Name: "Work VPN"})
	list := call[protocol.SubscriptionList](t, c, protocol.OpSubscriptions, nil)
	if len(list.Subscriptions) != 1 || list.Subscriptions[0].Name != home.Name || !list.Subscriptions[0].Active {
		t.Fatalf("list = %+v", list)
	}
	if err := callErr(t, c, protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "Work VPN"}); err.Code != protocol.CodeNotFound {
		t.Fatalf("updating a removed subscription = %v", err)
	}
}

func TestSubscriptionURLsMustBeHTTPSAndStayPrivate(t *testing.T) {
	o := newOrigin(t)
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	if err := callErr(t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: "http://example.test/s?token=s3cret"}); err.Code != protocol.CodeInvalid || strings.Contains(err.Message, "s3cret") {
		t.Fatalf("plain http = %v", err)
	}
	for path, p := range map[string]page{"/missing": {}, "/garbage": {body: "\x00\x01 not a subscription"}, "/broken": {body: "proxies: INVALID"}} {
		if p.body != "" {
			o.set(path, p)
		}
		err := callErr(t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url(path)})
		want := protocol.CodeRejected
		if path == "/missing" {
			want = protocol.CodeFetch
		}
		if err.Code != want || strings.Contains(err.Message, "s3cret") {
			t.Errorf("%s = %v, want %s", path, err, want)
		}
	}
	if list := call[protocol.SubscriptionList](t, c, protocol.OpSubscriptions, nil); len(list.Subscriptions) != 0 {
		t.Fatalf("a failed add left %+v", list)
	}
	o.set("/work", page{body: workYAML})
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	raw, err := json.Marshal(call[json.RawMessage](t, c, protocol.OpSubscriptions, nil))
	if err != nil || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("the list shows the URL: %s", raw)
	}
	entries, _ := os.ReadDir(h.stateDir)
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v", e.Name(), info.Mode())
		}
	}
}

// Only what is modelled reaches the core: no listeners, controller or
// files, and providers arrive downloaded over HTTPS and expanded.
func TestASubscriptionCannotReachPastTheAllowlist(t *testing.T) {
	o := newOrigin(t)
	o.set("/full", page{body: `
external-controller: 0.0.0.0:9090
external-controller-unix: /tmp/open.sock
secret: chosen
allow-lan: true
listeners: [{name: open, type: mixed, listen: 0.0.0.0, port: 1081}]
ntp: {enable: true, write-to-system: true, server: ntp.example}
proxies:
  - {name: Key, type: ssh, server: ssh.example, port: 22, username: u, private-key: /etc/shadow}
  - {name: Tokyo, type: trojan, server: tokyo.example, port: 443, password: p, interface-name: eth0, routing-mark: 1}
proxy-providers:
  remote: {type: http, url: "` + o.URL + `/provider", path: ./settings.json}
  plain: {type: http, url: "http://provider.example/p"}
  local: {type: file, path: /etc/passwd}
proxy-groups:
  - {name: Proxy, type: select, proxies: [Tokyo, Key], use: [remote, plain, local]}
rule-providers:
  ads: {type: http, behavior: domain, url: "` + o.URL + `/ads", path: ./owner}
rules:
  - RULE-SET,ads,REJECT
  - SUB-RULE,(NETWORK,tcp),sub
  - MATCH,Proxy
`})
	o.set("/provider", page{body: "proxies:\n  - {name: Remote, type: trojan, server: r.example, port: 443, password: p}\n"})
	o.set("/ads", page{body: "payload:\n  - +.ads.example\n"})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/full")})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	cfg, _ := h.core.Config()
	p := cfg.Profile
	if proxies, _ := names(p); !slices.Equal(proxies, []string{"Key", "Tokyo", "Remote"}) {
		t.Fatalf("proxies %v", proxies)
	}
	for _, px := range p.Proxies {
		for _, key := range []string{"private-key", "interface-name", "routing-mark", "path"} {
			if _, ok := px.Options[key]; ok {
				t.Errorf("%s keeps %s", px.Name, key)
			}
		}
	}
	if g := p.Groups[0]; !slices.Equal(g.Members, []string{"Tokyo", "Key", "Remote"}) || g.Options["use"] != nil {
		t.Errorf("group %+v", g)
	}
	if len(p.RuleSets) != 1 || p.RuleSets[0].Name != "ads" || !slices.Equal(p.RuleSets[0].Payload, []string{"+.ads.example"}) {
		t.Errorf("rule sets %+v", p.RuleSets)
	}
	var types []string
	for _, r := range p.Rules {
		types = append(types, r.Type)
	}
	if !slices.Equal(types, []string{"RULE-SET", "MATCH"}) {
		t.Errorf("rules %v", types)
	}
	if o.count("/provider") != 1 || o.count("/ads") != 1 {
		t.Errorf("providers fetched %d and %d times", o.count("/provider"), o.count("/ads"))
	}
}

func TestAFailedRefreshKeepsWhatWorks(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	before, _ := h.core.Config()

	for i, tc := range []struct {
		page page
		code string
	}{
		{page{body: "proxies: INVALID"}, protocol.CodeRejected},
		{page{status: http.StatusInternalServerError}, protocol.CodeFetch},
		{page{body: strings.ReplaceAll(workYAML, "Berlin", "Paris")}, protocol.CodeCore},
	} {
		o.set("/work", tc.page)
		if tc.code == protocol.CodeCore {
			h.core.FailApply(fmt.Errorf("the core refuses"))
		}
		h.clock.advance(time.Minute)
		err := callErr(t, c, protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "127.0.0.1"})
		if err.Code != tc.code {
			t.Fatalf("%d: update = %v, want %s", i, err, tc.code)
		}
		h.core.FailApply(nil)
		after, running := h.core.Config()
		if p, _ := names(after.Profile); !running || !slices.Equal(p, []string{"Tokyo", "Berlin"}) {
			t.Fatalf("%d: the core runs %v", i, p)
		}
		list := call[protocol.SubscriptionList](t, c, protocol.OpSubscriptions, nil)
		if s := list.Subscriptions[0]; s.Error == "" || s.NextUpdate <= h.clock.Now().UnixMilli() {
			t.Fatalf("%d: after the failure %+v", i, s)
		}
	}
	_ = before

	// What was stored before still activates after a restart.
	call[protocol.Status](t, c, protocol.OpStop, nil)
	h.restart(t)
	c = h.dial(t)
	call[protocol.Status](t, c, protocol.OpStart, nil)
	after, _ := h.core.Config()
	if p, _ := names(after.Profile); !slices.Equal(p, []string{"Tokyo", "Berlin"}) {
		t.Fatalf("after a restart the core runs %v", p)
	}
}

func TestManualRefreshesAreSpacedOut(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	name := protocol.NameArgs{Name: "127.0.0.1"}
	if err := callErr(t, c, protocol.OpSubscriptionUpdate, name); err.Code != protocol.CodeConflict {
		t.Fatalf("an update right after the add = %v", err)
	}
	h.clock.advance(time.Minute)
	o.set("/work", page{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "3600"}})
	callErr(t, c, protocol.OpSubscriptionUpdate, name)
	h.clock.advance(10 * time.Minute)
	o.set("/work", page{body: workYAML})
	if err := callErr(t, c, protocol.OpSubscriptionUpdate, name); err.Code != protocol.CodeConflict || !strings.Contains(err.Message, "asked to wait") {
		t.Fatalf("an update the server asked to wait for = %v", err)
	}
	h.clock.advance(time.Hour)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionUpdate, name)
	if n := o.count("/work"); n != 3 {
		t.Fatalf("the server was asked %d times", n)
	}
}

func TestSubscriptionsRefreshThemselves(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML, header: map[string]string{"Profile-Update-Interval": "1"}})
	h := serve(t, os.Getuid(), append(o.opts(), daemon.WithRefreshCheck(10*time.Millisecond))...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	call[protocol.Empty](t, c, protocol.OpSubscribe, protocol.StreamsArgs{Streams: []string{protocol.StreamSubscriptions}})
	next(t, c, protocol.StreamSubscriptions)

	time.Sleep(50 * time.Millisecond)
	if n := o.count("/work"); n != 1 {
		t.Fatalf("refreshed before it was due: %d requests", n)
	}
	// A failed refresh with no answer is retried soon; one that worked
	// waits the server's interval.
	o.set("/work", page{status: http.StatusBadGateway})
	h.clock.advance(time.Hour)
	waitFor(t, func() bool { return o.count("/work") == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := o.count("/work"); n != 2 {
		t.Fatalf("a refused refresh was retried right away: %d requests", n)
	}
	o.set("/work", page{body: workYAML, header: map[string]string{"Profile-Update-Interval": "1"}})
	h.clock.advance(time.Hour)
	waitFor(t, func() bool { return o.count("/work") == 3 })
	for {
		var list protocol.SubscriptionList
		json.Unmarshal(next(t, c, protocol.StreamSubscriptions), &list)
		if s := list.Subscriptions[0]; s.Error == "" && s.UpdatedAt == h.clock.Now().UnixMilli() {
			break
		}
	}
}

func TestSettingsRulesAndTheRunningStateSurviveARestart(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	call[protocol.Status](t, c, protocol.OpSetSettings, protocol.SettingsArgs{Mode: ptr("global"), TUN: ptr(true), PrimaryGroup: ptr("Proxy")})
	call[protocol.RuleList](t, c, protocol.OpRuleAdd, protocol.RuleArgs{Type: "domain-suffix", Value: "intranet.example", Target: "DIRECT"})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	o.Close()

	h.restart(t)
	c = h.dial(t)
	var st protocol.Status
	waitFor(t, func() bool {
		st = call[protocol.Status](t, c, protocol.OpStatus, nil)
		return st.State == protocol.StateOn
	})
	cfg, _ := h.core.Config()
	if st.Mode != "global" || !st.TUN || st.PrimaryGroup != "Proxy" || cfg.Mode != "global" || !cfg.TUN ||
		len(cfg.Profile.Rules) == 0 || cfg.Profile.Rules[0].Value != "intranet.example" || cfg.Profile.Rules[0].Type != "DOMAIN-SUFFIX" {
		t.Fatalf("after a restart %+v runs %+v", st, cfg)
	}

	// Stopped stays stopped.
	call[protocol.Status](t, c, protocol.OpStop, nil)
	h.restart(t)
	time.Sleep(50 * time.Millisecond)
	if _, running := h.core.Config(); running {
		t.Fatal("a stopped proxy came back on")
	}
}

func TestOwnRules(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML})
	h := serve(t, os.Getuid(), o.opts()...)
	c := h.dial(t)
	if err := callErr(t, c, protocol.OpRuleAdd, protocol.RuleArgs{Type: "DOMAIN", Value: "a.example", Target: "Proxy"}); err.Code != protocol.CodeNotFound {
		t.Fatalf("a target no subscription has = %v", err)
	}
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	for _, r := range []protocol.RuleArgs{
		{Type: "IP-CIDR", Value: "not-a-cidr", Target: "DIRECT"},
		{Type: "PROCESS-NAME", Value: "Firefox,REJECT", Target: "Proxy"},
		{Type: "IP-CIDR", Value: "10.0.0.0/8", Target: "DIRECT,no-resolve"},
		{Type: "DST-PORT", Value: "70000", Target: "DIRECT"},
		{Type: "MATCH", Value: "x", Target: "DIRECT"},
		{Type: "DOMAIN", Value: " a.example", Target: "DIRECT"},
	} {
		if err := callErr(t, c, protocol.OpRuleAdd, r); err.Code != protocol.CodeInvalid {
			t.Errorf("%+v = %v", r, err)
		}
	}
	list := call[protocol.RuleList](t, c, protocol.OpRuleAdd, protocol.RuleArgs{Type: "PROCESS-NAME", Value: "steam", Target: "Proxy"})
	list = call[protocol.RuleList](t, c, protocol.OpRuleAdd, protocol.RuleArgs{Type: "IP-CIDR", Value: "10.0.0.0/8", Target: "DIRECT"})
	if len(list.Rules) != 2 || list.Rules[0].Value != "steam" || list.Rules[0].Source != protocol.SourceUser {
		t.Fatalf("rules while stopped = %+v", list)
	}
	call[protocol.Status](t, c, protocol.OpStart, nil)
	cfg, _ := h.core.Config()
	if len(cfg.Profile.Rules) < 2 || cfg.Profile.Rules[1].Value != "10.0.0.0/8" || cfg.Profile.Rules[1].Source != protocol.SourceUser {
		t.Fatalf("core rules %+v", cfg.Profile.Rules)
	}
	list = call[protocol.RuleList](t, c, protocol.OpRuleRemove, protocol.IndexArgs{Index: 1})
	cfg, _ = h.core.Config()
	if cfg.Profile.Rules[0].Value != "10.0.0.0/8" || list.Rules[0].Value != "10.0.0.0/8" {
		t.Fatalf("after removing rule 1: %+v", cfg.Profile.Rules)
	}
	if err := callErr(t, c, protocol.OpRuleRemove, protocol.IndexArgs{Index: 2}); err.Code != protocol.CodeNotFound {
		t.Fatalf("removing a rule that is not there = %v", err)
	}

	// A change the core refuses is not kept.
	h.core.FailApply(fmt.Errorf("refused"))
	if err := callErr(t, c, protocol.OpRuleAdd, protocol.RuleArgs{Type: "DOMAIN", Value: "b.example", Target: "DIRECT"}); err.Code != protocol.CodeCore {
		t.Fatalf("a refused rule = %v", err)
	}
	h.core.FailApply(nil)
	if list := call[protocol.RuleList](t, c, protocol.OpRules, nil); slices.ContainsFunc(list.Rules, func(r protocol.Rule) bool { return r.Value == "b.example" }) {
		t.Fatalf("the refused rule was kept: %+v", list)
	}
}

func TestGeodataIsDownloadedBeforeTheCoreNeedsIt(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: strings.Replace(workYAML, "  - MATCH,Proxy", "  - GEOIP,NL,DIRECT\n  - MATCH,Proxy", 1)})
	dir := t.TempDir()
	geoip := filepath.Join(dir, "geoip.metadb")
	c0 := fakecore.New()
	c0.SetGeodata(func(p core.Profile) []core.GeodataFile {
		if slices.ContainsFunc(p.Rules, func(r core.Rule) bool { return r.Type == "GEOIP" }) {
			return []core.GeodataFile{{Path: geoip, URL: o.URL + "/geoip.metadb"}}
		}
		return nil
	})
	h := serveCore(t, os.Getuid(), c0, o.opts()...)
	c := h.dial(t)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	if err := callErr(t, c, protocol.OpStart, nil); err.Code != protocol.CodeCore || !strings.Contains(err.Message, "geoip.metadb") {
		t.Fatalf("start without reachable geodata = %v", err)
	}
	o.set("/geoip.metadb", page{body: "mmdb"})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	if b, err := os.ReadFile(geoip); err != nil || string(b) != "mmdb" {
		t.Fatalf("geodata = %q, %v", b, err)
	}
	call[protocol.Status](t, c, protocol.OpStop, nil)
	call[protocol.Status](t, c, protocol.OpStart, nil)
	// Once when it was missing, once when it came: never again.
	if n := o.count("/geoip.metadb"); n != 2 {
		t.Fatalf("geodata requested %d times", n)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
