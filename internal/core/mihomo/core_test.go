package mihomo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	T "github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

// mihomo is process-global, so every test shares one core and leaves it
// stopped.
var shared *Core

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "omihomo-core-")
	if err != nil {
		panic(err)
	}
	// The real ports may belong to a mihomo already running here.
	mixedPort, tailscalePort = freePort(), freePort()
	shared, err = New(home, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		panic(err)
	}
	code := m.Run()
	shared.Stop(context.Background())
	os.RemoveAll(home)
	os.Exit(code)
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func running(t *testing.T, cfg core.Config) *Core {
	t.Helper()
	if err := shared.Apply(t.Context(), cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	t.Cleanup(func() { shared.Stop(context.Background()) })
	return shared
}

// A profile that runs without a network: Local reaches everything
// directly, Dead is a SOCKS proxy nothing listens on.
func profile() core.Profile {
	return core.Profile{
		Proxies: []core.Proxy{
			{Name: "Local", Type: "direct"},
			{Name: "Dead", Type: "socks5", Options: map[string]any{"server": "127.0.0.1", "port": 1}},
		},
		Groups: []core.Group{
			{Name: "Proxy", Type: "select", Members: []string{"Local", "Dead", "Auto"}},
			{Name: "Auto", Type: "url-test", Members: []string{"Local", "Dead"},
				Options: map[string]any{"url": "http://127.0.0.1:1/", "interval": 3600}},
			{Name: "GLOBAL", Type: "select", Members: []string{"Dead"}},
		},
		Rules: []core.Rule{
			{Type: "DOMAIN-SUFFIX", Value: "owner.example", Target: "DIRECT", Source: protocol.SourceUser},
			{Type: "RULE-SET", Value: "blocked", Target: "REJECT", Source: protocol.SourceSubscription},
			{Type: "MATCH", Target: "Proxy", Source: protocol.SourceSubscription},
		},
		RuleSets: []core.RuleSet{{Name: "blocked", Behavior: "domain", Payload: []string{"+.blocked.example"}}},
	}
}

func testConfig() core.Config {
	return core.Config{Mode: protocol.ModeRule, Profile: profile()}
}

func TestAStoppedCoreRefusesCalls(t *testing.T) {
	c := running(t, testConfig())
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("a second stop: %v", err)
	}
	if _, err := c.Groups(t.Context()); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("groups = %v, want ErrNotRunning", err)
	}
	if _, err := c.Rules(t.Context()); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("rules = %v, want ErrNotRunning", err)
	}
	if _, err := c.DialContext(t.Context(), "tcp", "127.0.0.1:1"); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("dial = %v, want ErrNotRunning", err)
	}
}

func TestGroupsFollowTheProfile(t *testing.T) {
	c := running(t, testConfig())
	groups, err := c.Groups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	if !slices.Equal(names, []string{"Proxy", "Auto"}) {
		t.Fatalf("groups = %v, want the profile's order without GLOBAL", names)
	}
	proxy := groups[0]
	if proxy.Type != "select" || !proxy.Selectable {
		t.Errorf("Proxy = %+v, want a selectable select group", proxy)
	}
	var types []string
	for _, m := range proxy.Members {
		types = append(types, m.Name+":"+m.Type)
	}
	if !slices.Equal(types, []string{"Local:direct", "Dead:socks5", "Auto:url-test"}) {
		t.Errorf("members = %v", types)
	}
	if auto := groups[1]; auto.Selectable || auto.Type != "url-test" {
		t.Errorf("Auto = %+v, want an automatic group", auto)
	}
}

func TestSelect(t *testing.T) {
	c := running(t, testConfig())
	if err := c.Select(t.Context(), "Proxy", "Dead"); err != nil {
		t.Fatal(err)
	}
	groups, _ := c.Groups(t.Context())
	if groups[0].Now != "Dead" {
		t.Errorf("Proxy now = %q, want Dead", groups[0].Now)
	}
	c.Stop(t.Context())
	c = running(t, testConfig())
	if groups, _ := c.Groups(t.Context()); groups[0].Now != "Dead" {
		t.Errorf("Proxy now after a restart = %q, want the selection kept", groups[0].Now)
	}
	for _, tc := range []struct{ group, proxy string }{
		{"Proxy", "Nowhere"}, {"Nowhere", "Local"}, {"Auto", "Local"}, {"GLOBAL", "Dead"},
	} {
		if err := c.Select(t.Context(), tc.group, tc.proxy); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("select %s in %s = %v, want ErrNotFound", tc.proxy, tc.group, err)
		}
	}
}

func TestRulesCarryTheirSources(t *testing.T) {
	cfg := testConfig()
	cfg.Tailscale = true
	c := running(t, cfg)
	rules, err := c.Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rules {
		got = append(got, r.Source+" "+r.Type+","+r.Value+","+r.Target)
	}
	want := []string{
		"user DOMAIN-SUFFIX,owner.example,DIRECT",
		"omihomo IP-CIDR,100.64.0.0/10,DIRECT",
		"omihomo IP-CIDR,fd7a:115c:a1e0::/48,DIRECT",
		"omihomo DOMAIN-SUFFIX,tailscale.com,GLOBAL",
		"omihomo DOMAIN-SUFFIX,tailscale.io,GLOBAL",
		"subscription RULE-SET,blocked,REJECT",
		"subscription MATCH,,Proxy",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	old := delayURL
	delayURL = srv.URL
	defer func() { delayURL = old }()

	c := running(t, testConfig())
	delays, err := c.TestDelay(t.Context(), "Proxy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if delays["Local"] <= 0 || delays["Dead"] != 0 || len(delays) != 3 {
		t.Errorf("delays = %v, want Local measured, Dead failed, every member tested", delays)
	}
	groups, _ := c.Groups(t.Context())
	for _, m := range groups[0].Members {
		if m.Name == "Local" && (!m.Alive || m.Delay <= 0) {
			t.Errorf("Local after the test = %+v, want alive with a delay", m)
		}
		if m.Name == "Dead" && (m.Alive || m.Delay != 0) {
			t.Errorf("Dead after the test = %+v, want dead", m)
		}
	}
	if delays, err := c.TestDelay(t.Context(), "Proxy", []string{"Local"}); err != nil || len(delays) != 1 {
		t.Errorf("one proxy = %v, %v", delays, err)
	}
	if _, err := c.TestDelay(t.Context(), "Proxy", []string{"Nowhere"}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown proxy = %v, want ErrNotFound", err)
	}
	if _, err := c.TestDelay(t.Context(), "Nowhere", nil); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown group = %v, want ErrNotFound", err)
	}
}

func TestDialGoesThroughTheRules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ip=192.0.2.7\n")
	}))
	defer srv.Close()
	c := running(t, testConfig())
	// Selections outlive a config, so an earlier test may have left Dead.
	if err := c.Select(t.Context(), "Proxy", "Local"); err != nil {
		t.Fatal(err)
	}

	conn, err := c.DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "ip=192.0.2.7") {
		t.Fatalf("read %q, %v; want the server's answer", buf[:n], err)
	}

	conns, err := c.Connections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if conns.Open != 1 || len(conns.Connections) != 1 {
		t.Fatalf("connections = %+v, want the one dial", conns)
	}
	got := conns.Connections[0]
	if got.Chain != "Local" || got.Inbound != "inner" || got.Network != "tcp" || got.Rule != "Match" {
		t.Errorf("connection = %+v, want inner tcp matched to Local", got)
	}
	if err := c.CloseConnections(t.Context(), []string{"gone", got.ID}); err != nil {
		t.Fatal(err)
	}
	if conns, _ := c.Connections(t.Context()); conns.Open != 0 {
		t.Errorf("open after closing = %d", conns.Open)
	}
	if _, err := c.DialContext(t.Context(), "udp", srv.Listener.Addr().String()); err == nil {
		t.Error("a UDP dial succeeded")
	}
}

func TestAConfigThatDoesNotParseChangesNothing(t *testing.T) {
	c := running(t, testConfig())
	bad := testConfig()
	bad.Profile.Rules = append(bad.Profile.Rules[:1:1], core.Rule{Type: "MATCH", Target: "Nowhere", Source: protocol.SourceSubscription})
	if err := c.Apply(t.Context(), bad); err == nil {
		t.Fatal("a rule naming an unknown group applied")
	}
	rules, err := c.Rules(t.Context())
	if err != nil || len(rules) != 3 {
		t.Errorf("rules after the failure = %v, %v; want the previous three", rules, err)
	}
	if groups, err := c.Groups(t.Context()); err != nil || len(groups) != 2 {
		t.Errorf("groups after the failure = %v, %v", groups, err)
	}
}

func TestAFailedTUNKeepsThePreviousConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root TUN would start")
	}
	c := running(t, testConfig())
	withTUN := testConfig()
	withTUN.TUN = true
	err := c.Apply(t.Context(), withTUN)
	if err == nil || !strings.Contains(err.Error(), "TUN did not start") {
		t.Fatalf("apply with TUN unprivileged = %v, want a TUN failure", err)
	}
	if strings.Contains(err.Error(), "no reason was logged") {
		t.Errorf("the failure does not say why: %v", err)
	}
	if _, err := c.Groups(t.Context()); err != nil {
		t.Errorf("the previous config is gone: %v", err)
	}

	c.Stop(t.Context())
	if err := c.Apply(t.Context(), withTUN); err == nil {
		t.Fatal("TUN started unprivileged")
	}
	if _, err := c.Groups(t.Context()); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("a failed start left the core running: %v", err)
	}
}

func TestOneCorePerProcess(t *testing.T) {
	if _, err := New(t.TempDir(), nil); err == nil {
		t.Fatal("a second core was created")
	}
}

func TestTraffic(t *testing.T) {
	c := running(t, testConfig())
	if _, err := c.Traffic(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestAnEmptyProfileRunsDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := running(t, core.Config{Mode: protocol.ModeRule})
	if groups, err := c.Groups(t.Context()); err != nil || len(groups) != 0 {
		t.Fatalf("groups = %v, %v", groups, err)
	}
	conn, err := c.DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	if n, err := conn.Read(buf); err != nil || !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
}

// mihomo applies a config's general settings to the live engine while it
// parses; a config that never applies must not route anything meanwhile.
func TestARejectedConfigNeverRoutes(t *testing.T) {
	c := running(t, testConfig())
	bad := testConfig()
	bad.Mode = protocol.ModeDirect
	for i := range 5000 {
		bad.Profile.Rules = append(bad.Profile.Rules, core.Rule{Type: "DOMAIN", Value: fmt.Sprintf("d%d.example", i), Target: "Proxy"})
	}
	bad.Profile.Rules = append(bad.Profile.Rules, core.Rule{Type: "MATCH", Target: "Nowhere"})

	done := make(chan struct{})
	leaked := make(chan string, 1)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if m := T.Mode(); m != T.Rule {
				leaked <- m.String()
				return
			}
		}
	}()
	err := c.Apply(t.Context(), bad)
	close(done)
	if err == nil {
		t.Fatal("the bad config applied")
	}
	select {
	case m := <-leaked:
		t.Fatalf("the engine ran in %s mode while a rejected config parsed", m)
	default:
	}
	if m := T.Mode(); m != T.Rule {
		t.Errorf("mode after the failure = %s", m)
	}
}

func TestAMalformedRegexFailsTheApply(t *testing.T) {
	c := running(t, testConfig())
	bad := testConfig()
	bad.Profile.Groups[1].Options["filter"] = "["
	if err := c.Apply(t.Context(), bad); err == nil {
		t.Fatal("a group with a broken filter applied")
	}
	if _, err := c.Groups(t.Context()); err != nil {
		t.Errorf("the previous config is gone: %v", err)
	}
}

func TestATakenPortFailsTheApply(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			c := running(t, testConfig())
			c.Stop(t.Context())
			addr := fmt.Sprintf("127.0.0.1:%d", mixedPort)
			var taken io.Closer
			var err error
			if network == "tcp" {
				taken, err = net.Listen("tcp", addr)
			} else {
				taken, err = net.ListenPacket("udp", addr)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer taken.Close()
			err = c.Apply(t.Context(), testConfig())
			if err == nil || !strings.Contains(err.Error(), "did not open") {
				t.Fatalf("apply with %s taken = %v", network, err)
			}
			if _, err := c.Groups(t.Context()); !errors.Is(err, core.ErrNotRunning) {
				t.Errorf("the core runs with no way in: %v", err)
			}
		})
	}
}

// A reload whose listener fails puts the previous config back, local
// proxy port included.
func TestAFailedListenerKeepsThePreviousConfig(t *testing.T) {
	c := running(t, testConfig())
	taken, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tailscalePort))
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	withTailscale := testConfig()
	withTailscale.Tailscale = true
	if err := c.Apply(t.Context(), withTailscale); err == nil || !strings.Contains(err.Error(), "Tailscale") {
		t.Fatalf("apply with the Tailscale port taken = %v", err)
	}
	rules, err := c.Rules(t.Context())
	if err != nil || len(rules) != 3 {
		t.Fatalf("rules after the failure = %v, %v; want the previous config's", rules, err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", mixedPort), time.Second)
	if err != nil {
		t.Fatalf("the local proxy port is closed after the restore: %v", err)
	}
	conn.Close()
}

func TestARuleSetWithUnreadableLinesFailsTheApply(t *testing.T) {
	for _, rs := range []core.RuleSet{
		{Name: "blocked", Behavior: "classical", Payload: []string{"DOMAIN,a.example", "GEOIP,CN"}},
		{Name: "blocked", Behavior: "ipcidr", Payload: []string{"10.0.0.0/8", "not an address"}},
		{Name: "blocked", Behavior: "domain", Payload: []string{"a.example", "a/b"}},
	} {
		cfg := testConfig()
		cfg.Profile.RuleSets = []core.RuleSet{rs}
		if err := shared.Apply(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "do not parse") {
			t.Errorf("%s set with a bad line = %v", rs.Behavior, err)
		}
	}
	if _, err := shared.Groups(t.Context()); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("a refused config started the core: %v", err)
	}
}

func TestABadIntervalFailsTheApply(t *testing.T) {
	cfg := testConfig()
	cfg.Profile.Groups[1].Options["interval"] = -1
	if err := shared.Apply(t.Context(), cfg); err == nil {
		t.Fatal("a negative health check interval applied")
	}
}

func TestACancelledDialNeverConnects(t *testing.T) {
	c := running(t, testConfig())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if conn, err := c.DialContext(ctx, "tcp", "127.0.0.1:1"); err == nil {
		conn.Close()
		t.Fatal("a cancelled dial connected")
	}
}

func TestARestoreThatFailsStops(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root TUN would start")
	}
	withTailscale := testConfig()
	withTailscale.Tailscale = true
	c := running(t, withTailscale)
	var taken net.Listener
	afterRestoreStop = func() {
		taken, _ = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tailscalePort))
	}
	defer func() {
		afterRestoreStop = func() {}
		if taken != nil {
			taken.Close()
		}
	}()
	bad := withTailscale
	bad.Profile.RuleSets = nil
	bad.Profile.Rules = []core.Rule{{Type: "MATCH", Target: "Proxy"}}
	bad.TUN = true
	err := c.Apply(t.Context(), bad)
	if !errors.Is(err, core.ErrStopped) {
		t.Fatalf("apply = %v, want ErrStopped", err)
	}
	if taken == nil {
		t.Fatal("the test could not take the port")
	}
	if _, err := c.Groups(t.Context()); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("the core claims to run: %v", err)
	}
}

// socks5 answers a CONNECT only once released, then echoes.
func socks5(t *testing.T, release <-chan struct{}) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 512)
				if _, err := io.ReadAtLeast(conn, buf, 3); err != nil {
					return
				}
				conn.Write([]byte{5, 0})
				if _, err := conn.Read(buf); err != nil {
					return
				}
				<-release
				conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				io.Copy(conn, conn)
			}()
		}
	}()
	t.Cleanup(func() { l.Close() })
	return l
}

func TestADialInFlightAtStopIsClosed(t *testing.T) {
	release := make(chan struct{})
	upstream := socks5(t, release)
	cfg := testConfig()
	port := upstream.Addr().(*net.TCPAddr).Port
	cfg.Profile.Proxies = append(cfg.Profile.Proxies, core.Proxy{Name: "Slow", Type: "socks5",
		Options: map[string]any{"server": "127.0.0.1", "port": port}})
	cfg.Profile.Groups[0].Members = append(cfg.Profile.Groups[0].Members, "Slow")
	c := running(t, cfg)
	if err := c.Select(t.Context(), "Proxy", "Slow"); err != nil {
		t.Fatal(err)
	}
	conn, err := c.DialContext(t.Context(), "tcp", "192.0.2.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // the dial reaches the upstream
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(release)
	go io.WriteString(conn, "after-stop")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("the late connection stayed open: %v", err)
	}
	if conns := statistic.DefaultManager.Snapshot().Connections; len(conns) != 0 {
		t.Errorf("%d connections outlived the stop", len(conns))
	}
}

// A drain belongs to its stop: a start right after must keep its
// connections.
func TestADrainSparesTheNextStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := running(t, testConfig())
	c.Stop(t.Context())
	c = running(t, testConfig())
	if err := c.Select(t.Context(), "Proxy", "Local"); err != nil {
		t.Fatal(err)
	}
	conn, err := c.DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(10 * drainTick)
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	if n, err := conn.Read(buf); err != nil || !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
		t.Fatalf("read %q, %v: the old stop's drain closed the new connection", buf[:n], err)
	}
}
