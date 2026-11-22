// Package mihomo hosts the mihomo proxy engine in this process behind
// core.Core. It is the only package that imports mihomo.
//
// mihomo keeps its state in package globals: a process hosts at most one
// Core, and New sets mihomo's home directory for the whole process. Nothing
// is fetched by mihomo itself: the core config names no controller, no
// dashboard and no geodata source, so geodata the rules need must already be
// in the home directory.
package mihomo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	mihomoprofile "github.com/metacubex/mihomo/component/profile"
	"github.com/metacubex/mihomo/component/profile/cachefile"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/constant/features"
	"github.com/metacubex/mihomo/dns"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/listener/inner"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/connlog"
	"github.com/readeem/omihomo/internal/core/xray"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/uplink"
)

// Mihomo compiles domain and process regexes through regexp2. Set this once,
// before any core is constructed, including regexes nested in downloaded rules.
func init() { regexp2.DefaultMatchTimeout = 25 * time.Millisecond }

var (
	delayURL     = core.TraceIPv4URL
	delayIPv6URL = core.TraceIPv6URL
)

const (
	drainTick         = 20 * time.Millisecond
	delayTimeout      = 5 * time.Second
	delayBatchTimeout = 30 * time.Second
	// Probes in flight across all tests, including any still unwinding after timeout.
	delayParallel = 16
)

// Core is core.Core over the mihomo engine hosted in this process.
type Core struct {
	downloadDNS resolver.Resolver
	uplink      uplink.Network
	log         *slog.Logger
	tap         *logTap
	xrayNetwork xray.Network
	delaySlots  chan struct{}
	historyDir  string
	nextSession uint64
	capture     atomic.Pointer[connlog.History]

	// applyMu serializes everything that parses or applies a config:
	// parsing mutates mihomo's globals while it runs.
	applyMu   sync.Mutex
	reloadIP  bool
	reloadASN bool

	mu      sync.Mutex
	running bool
	current *runtime
	history *connlog.History
	// generation counts commits and stops, so a drain knows when the
	// stop it follows is over.
	generation uint64
}

var (
	_ core.Core = (*Core)(nil)

	hosted atomic.Bool
)

// New hosts mihomo with home as its directory for state and geodata. home
// must be an absolute path to a directory only this process's account can
// write; it is created if missing. A process can call New once.
func New(home string, log *slog.Logger) (*Core, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("the core's home %q is not absolute", home)
	}
	if err := privateDir(home); err != nil {
		return nil, err
	}
	if !hosted.CompareAndSwap(false, true) {
		return nil, errors.New("mihomo is already hosted in this process")
	}
	if log == nil {
		log = slog.Default()
	}
	constant.SetHomeDir(home)
	mihomoprofile.StoreSelected.Store(true)
	c := &Core{historyDir: home, log: log, tap: startLogTap(log), current: &runtime{}, xrayNetwork: xrayNetwork{}, delaySlots: make(chan struct{}, delayParallel)}
	statistic.SetConnectionObserver(c.observeConnection)
	return c, nil
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the core's home: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect the core's home: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be a directory owned by uid %d and writable by nobody else", dir, os.Geteuid())
	}
	return nil
}

func (c *Core) Apply(ctx context.Context, cfg core.Config) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	if cfg.TUN && !cfg.TUNRedirect && !features.WithGVisor {
		return errors.New("TUN without redirect needs the gVisor stack, which this build lacks")
	}
	next, err := prepare(cfg)
	if err != nil {
		return fmt.Errorf("%w: %w", core.ErrInvalid, err)
	}
	c.mu.Lock()
	wasRunning, previous := c.running, c.current.cfg
	c.mu.Unlock()
	if !wasRunning {
		c.reloadDatabases()
		c.nextSession++
		c.capture.Store(connlog.New(c.historyDir, c.nextSession))
	}

	if err := c.run(next); err != nil {
		if !wasRunning {
			c.capture.Swap(nil).Discard()
			c.stop()
			return err
		}
		if rerr := c.restore(previous); rerr != nil {
			c.capture.Store(nil)
			return fmt.Errorf("%w; the previous config did not come back either (%v): %w", err, rerr, core.ErrStopped)
		}
		return err
	}
	return nil
}

func (c *Core) Check(ctx context.Context, cfg core.Config) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	if cfg.TUN && !cfg.TUNRedirect && !features.WithGVisor {
		return errors.New("TUN without redirect needs the gVisor stack, which this build lacks")
	}
	_, err := prepare(cfg)
	return err
}

// afterRestoreStop lets a test act between a restore's stop and its apply.
var afterRestoreStop = func() {}

// run applies rt and commits it once everything it asks for came up.
func (c *Core) run(rt *runtime) error {
	c.mu.Lock()
	previous := c.current
	c.mu.Unlock()
	if rt.cfg.Core == protocol.CoreXray {
		engine := previous.xray
		if engine == nil || !reflect.DeepEqual(previous.cfg.Profile.Proxies, rt.cfg.Profile.Proxies) {
			var err error
			engine, err = xray.New(rt.cfg.Profile.Proxies, c.xrayNetwork)
			if err != nil {
				return err
			}
		}
		rt.useXray(engine)
	}
	stop := c.tap.watch()
	// These bootstrap clients are installed before health checks can dial.
	// Only this private adapter can discover or bind to uplink DNS.
	bootstrap := &uplinkDNS{ProxyAdapter: rt.parsed.Proxies["DIRECT"], network: c.uplink}
	servers := []dns.NameServer{{Net: "tcp", Addr: "0.0.0.0:53", ProxyAdapter: bootstrap}}
	rt.parsed.DNS.DefaultNameserver = servers
	rt.parsed.DNS.ProxyServerNameserver = servers
	executor.ApplyConfig(rt.parsed, true)
	dnsProxy := rt.parsed.DNS.NameServer[0].ProxyAdapter
	if dnsProxy == nil {
		dnsProxy = rt.parsed.Proxies[globalGroup]
	}
	normalDNS := bootstrapResolver{client: downloadDNS{proxy: dnsProxy, magic: resolver.ProxyServerHostResolver}}
	c.mu.Lock()
	c.downloadDNS = normalDNS
	c.mu.Unlock()
	resolver.ProxyServerHostResolver = newBootstrapResolver(normalDNS, resolver.ProxyServerHostResolver)
	if err := started(rt.cfg, stop()); err != nil {
		if rt.xray != previous.xray {
			c.closeXray(rt.xray)
		}
		return err
	}
	c.commit(rt)
	return nil
}

// What mihomo logs, and only logs, when an inbound does not start.
const (
	tunFailed       = "Start TUN listening error: "
	mixedFailed     = "Start Mixed(http+socks) server error: "
	listenerFailedF = "Listener %s listen err: "
)

// started checks that what cfg asks for came up. mihomo applies a config
// whatever fails on the way and only logs it, so this reads what it logged.
func started(cfg core.Config, logged []string) error {
	reason := func(prefix string) string {
		for _, line := range logged {
			if r, ok := strings.CutPrefix(line, prefix); ok {
				return r
			}
		}
		return ""
	}
	if listener.GetTunConf().Enable != cfg.TUN {
		r := reason(tunFailed)
		if r == "" {
			r = "no reason was logged"
		}
		return fmt.Errorf("TUN did not start: %s", r)
	}
	if r := reason(mixedFailed); r != "" || listener.GetPorts().MixedPort == 0 {
		return fmt.Errorf("the local proxy port %d did not open: %s", mixedPort, r)
	}
	if r := reason(fmt.Sprintf(listenerFailedF, tailscaleListener)); cfg.Tailscale && r != "" {
		return fmt.Errorf("the Tailscale proxy port %d did not open: %s", tailscalePort, r)
	}
	return nil
}

type runtime struct {
	xray    *xray.Outbounds
	cfg     core.Config
	parsed  *config.Config
	rules   []protocol.Rule
	groups  []string
	members map[string]protocol.Proxy
}

func prepare(cfg core.Config) (*runtime, error) {
	raw, rules, err := rawConfig(cfg)
	if err != nil {
		return nil, err
	}
	parsed, err := parse(raw)
	if err != nil {
		return nil, err
	}
	for name, proxy := range parsed.Proxies {
		parsed.Proxies[name] = sniffedProxy{Proxy: proxy}
	}
	if cfg.DNSDestination != "" {
		name := cfg.DNSDestination
		exists := name != globalGroup && (slices.ContainsFunc(cfg.Profile.Groups, func(g core.Group) bool { return g.Name == name }) ||
			slices.ContainsFunc(cfg.Profile.Proxies, func(p core.Proxy) bool { return p.Name == name }))
		adapter := parsed.Proxies[name]
		if !exists || adapter == nil {
			return nil, fmt.Errorf("DNS destination %q is unavailable in the active subscription", name)
		}
		// Bind the adapter: mihomo treats an unknown DNS proxy name as an interface
		// and may dial directly. Names also must not become DNS fragment parameters.
		for i := range parsed.DNS.NameServer {
			parsed.DNS.NameServer[i].ProxyAdapter = adapter
		}
	}
	// A rule set keeps the lines it can read and only logs the others,
	// which would let through what the rest should have matched.
	for _, rs := range cfg.Profile.RuleSets {
		want := 0
		for _, line := range rs.Payload {
			if line != "" {
				want++
			}
		}
		if got := parsed.RuleProviders[rs.Name].Count(); got != want {
			return nil, fmt.Errorf("rule set %q: %d of its %d lines do not parse", rs.Name, want-got, want)
		}
	}
	rt := &runtime{cfg: cfg, parsed: parsed, rules: rules, members: map[string]protocol.Proxy{
		"DIRECT": {Type: "direct"}, "REJECT": {Type: "reject"}, "REJECT-DROP": {Type: "reject-drop"},
		"PASS": {Type: "pass"}, "COMPATIBLE": {Type: "compatible"},
	}}
	for _, p := range cfg.Profile.Proxies {
		label := ""
		if p.Type == "vless" {
			network, _ := p.Options["network"].(string)
			switch network {
			case "xhttp":
				label = "XHTTP"
			case "ws":
				label = "WebSocket"
			case "grpc":
				label = "gRPC"
			default:
				reality, _ := p.Options["reality-opts"].(map[string]any)
				if key, _ := reality["public-key"].(string); key != "" {
					label = "REALITY"
				}
			}
		}
		rt.members[p.Name] = protocol.Proxy{Type: p.Type, Label: label, Revision: p.Revision()}
	}
	for _, g := range cfg.Profile.Groups {
		if g.Name != globalGroup {
			rt.members[g.Name] = protocol.Proxy{Type: g.Type}
			rt.groups = append(rt.groups, g.Name)
		}
	}
	return rt, nil
}

func prepareGroups(cfg core.Config) (*runtime, error) {
	return prepare(core.Config{
		Mode: protocol.ModeRule, PrimaryGroup: cfg.PrimaryGroup,
		Profile: core.Profile{Proxies: cfg.Profile.Proxies, Groups: cfg.Profile.Groups},
	})
}

// parse parses raw without letting the running engine see any of it.
// mihomo applies a config's general settings to the live engine while it
// parses, so that geodata loads by them, and rolls them back afterwards.
// Every config the core builds shares those settings but the mode, so the
// parse runs in the live mode and the wanted one is set on the result.
// mihomo also panics on some malformed values, such as a group's regex.
func parse(raw *config.RawConfig) (parsed *config.Config, err error) {
	mode := raw.Mode
	raw.Mode = tunnel.Mode()
	defer func() {
		if p := recover(); p != nil {
			parsed, err = nil, fmt.Errorf("the config does not parse: %v", p)
		}
	}()
	parsed, err = config.ParseRawConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("the config does not parse: %w", err)
	}
	parsed.General.Mode = mode
	return parsed, nil
}

func (c *Core) commit(rt *runtime) {
	rt.parsed = nil
	c.mu.Lock()
	previous := c.current.xray
	oldHistory := c.history
	c.history = c.capture.Load()
	newHistory := c.history
	c.running, c.current = true, rt
	c.generation++
	c.mu.Unlock()
	if oldHistory != nil && oldHistory != newHistory {
		oldHistory.Discard()
	}
	if previous != rt.xray {
		c.closeXray(previous)
	}
}

// restore puts back the config that ran before a failed apply, or leaves
// the core stopped. It stops first: mihomo keeps a listener that failed
// halfway, closed, and would not open it again for the same config.
func (c *Core) restore(previous core.Config) error {
	c.stop()
	afterRestoreStop()
	rt, err := prepare(previous)
	if err == nil {
		err = c.run(rt)
	}
	if err != nil {
		c.log.Error("restore the previous config", "err", err)
		c.stop()
	}
	return err
}

func (c *Core) Stop(ctx context.Context) error {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	c.capture.Store(nil)
	c.stop()
	return nil
}

func (c *Core) closeXray(outbounds *xray.Outbounds) {
	if outbounds != nil {
		if err := outbounds.Close(); err != nil {
			c.log.Warn("close Xray outbounds", "err", err)
		}
	}
}

func (c *Core) stop() {
	c.mu.Lock()
	previous := c.current.xray
	c.running, c.current = false, &runtime{}
	c.generation++
	generation := c.generation
	c.mu.Unlock()
	c.closeXray(previous)
	parsed, err := parse(stoppedConfig())
	if err != nil {
		// The stopped config is fixed, so this is a bug. TUN is what must
		// not outlive a stop: close it directly.
		c.log.Error("parse the stopped config", "err", err)
		tun := listener.GetTunConf()
		tun.Enable = false
		listener.ReCreateTun(tun, tunnel.Tunnel)
	} else {
		executor.ApplyConfig(parsed, true)
	}
	closeAll()
	go c.drain(generation)
}

// drain closes the connections that open after a stop. mihomo dials
// without a context it would cancel and tracks a connection only once the
// dial is through, so a dial in flight at the stop lands later through the
// old proxy. A dial gives up after C.DefaultTCPTimeout; until then, and
// while the core stays stopped, whatever lands is closed. What it sends
// within one tick of landing still gets out.
func (c *Core) drain(generation uint64) {
	deadline := time.Now().Add(constant.DefaultTCPTimeout + time.Second)
	tick := time.NewTicker(drainTick)
	defer tick.Stop()
	for now := range tick.C {
		if !c.drainOnce(generation, now.After(deadline)) {
			return
		}
	}
}

// drainOnce closes every connection if the stop of generation still
// holds, under the apply lock, so an apply that starts the engine again
// cannot open connections in between. It reports whether to go on.
func (c *Core) drainOnce(generation uint64, last bool) bool {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	c.mu.Lock()
	stopped := c.generation == generation
	c.mu.Unlock()
	if !stopped || last {
		return false
	}
	closeAll()
	return true
}

func closeAll() {
	statistic.DefaultManager.Range(func(t statistic.Tracker) bool {
		t.Close()
		return true
	})
}

// state returns the running config's description, or ErrNotRunning.
func (c *Core) state() (groups []string, members map[string]protocol.Proxy, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, nil, core.ErrNotRunning
	}
	return c.current.groups, c.current.members, nil
}

func (c *Core) group(name string) (outboundgroup.ProxyGroup, error) {
	groups, _, err := c.state()
	if err != nil {
		return nil, err
	}
	if !slices.Contains(groups, name) {
		return nil, fmt.Errorf("group %q: %w", name, core.ErrNotFound)
	}
	p, ok := tunnel.Proxies()[name]
	if !ok {
		return nil, fmt.Errorf("group %q: %w", name, core.ErrNotFound)
	}
	g, ok := p.Adapter().(outboundgroup.ProxyGroup)
	if !ok {
		return nil, fmt.Errorf("group %q: %w", name, core.ErrNotFound)
	}
	return g, nil
}

func (c *Core) Groups(ctx context.Context, cfg core.Config) ([]protocol.Group, error) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	names, entries, err := c.state()
	proxies := tunnel.Proxies()
	if errors.Is(err, core.ErrNotRunning) && len(cfg.Profile.Groups) > 0 {
		rt, err := prepareGroups(cfg)
		if err != nil {
			return nil, err
		}
		names, entries, proxies = rt.groups, rt.members, rt.parsed.Proxies
		for name, selected := range cachefile.Cache().SelectedMap() {
			if p := proxies[name]; p != nil {
				if selector, ok := p.Adapter().(outboundgroup.SelectAble); ok {
					selector.ForceSet(selected)
				}
			}
		}
	} else if err != nil {
		return nil, err
	}
	out := make([]protocol.Group, 0, len(names))
	for _, name := range names {
		p, ok := proxies[name]
		if !ok {
			continue
		}
		g, ok := p.Adapter().(outboundgroup.ProxyGroup)
		if !ok || g.Hidden() {
			continue
		}
		_, selectable := g.(*outboundgroup.Selector)
		members := g.Proxies()
		group := protocol.Group{Name: name, Type: entries[name].Type, Now: g.Now(), Selectable: selectable,
			Members: make([]protocol.Proxy, 0, len(members))}
		for _, m := range members {
			group.Members = append(group.Members, member(m, entries))
		}
		out = append(out, group)
	}
	return out, nil
}

func member(p constant.Proxy, entries map[string]protocol.Proxy) protocol.Proxy {
	out, ok := entries[p.Name()]
	if !ok {
		out.Type = strings.ToLower(p.Type().String())
	}
	out.Name, out.Alive = p.Name(), p.AliveForTestUrl("")
	if history := p.DelayHistory(); len(history) > 0 && out.Alive {
		// A test faster than a millisecond still measured something.
		out.Delay = max(1, int(history[len(history)-1].Delay))
	}
	return out
}

func (c *Core) Select(ctx context.Context, cfg core.Config, group, proxy string) error {
	// A reload in between would restore the cached choice into a new
	// selector before this one is cached.
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	g, err := c.group(group)
	if errors.Is(err, core.ErrNotRunning) && len(cfg.Profile.Groups) > 0 {
		rt, err := prepareGroups(cfg)
		if err != nil {
			return err
		}
		p := rt.parsed.Proxies[group]
		if p == nil || !slices.Contains(rt.groups, group) {
			return fmt.Errorf("group %q: %w", group, core.ErrNotFound)
		}
		g, _ = p.Adapter().(outboundgroup.ProxyGroup)
	} else if err != nil {
		return err
	}
	selector, ok := g.(*outboundgroup.Selector)
	if !ok {
		return fmt.Errorf("selectable group %q: %w", group, core.ErrNotFound)
	}
	if !slices.ContainsFunc(g.Proxies(), func(p constant.Proxy) bool { return p.Name() == proxy }) {
		return fmt.Errorf("proxy %q in group %q: %w", proxy, group, core.ErrNotFound)
	}
	if err := selector.Set(proxy); err != nil {
		return fmt.Errorf("select %q in %q: %w", proxy, group, err)
	}
	cachefile.Cache().SetSelected(group, proxy)
	return nil
}

func (c *Core) TestDelay(ctx context.Context, group string, proxies []string, except string) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := c.group(group)
	if err != nil {
		return nil, err
	}
	tested := g.Proxies()
	if except != "" {
		tested = slices.DeleteFunc(slices.Clone(tested), func(p constant.Proxy) bool { return p.Name() == except })
	}
	if len(proxies) != 0 {
		members := make(map[string]constant.Proxy, len(tested))
		for _, p := range tested {
			members[p.Name()] = p
		}
		tested = make([]constant.Proxy, 0, len(proxies))
		for _, name := range proxies {
			p, ok := members[name]
			if !ok {
				return nil, fmt.Errorf("proxy %q in group %q: %w", name, group, core.ErrNotFound)
			}
			tested = append(tested, p)
		}
	}

	waves := (len(tested) + delayParallel - 1) / delayParallel
	batch, cancel := context.WithTimeout(ctx, min(delayBatchTimeout, time.Duration(waves+1)*delayTimeout))
	defer cancel()
	type measurement struct {
		name  string
		delay int
	}
	results := make(chan measurement, len(tested))
	out := make(map[string]int, len(tested))
	started := 0
	ipv4URL, ipv6URL := delayURL, delayIPv6URL
schedule:
	for _, p := range tested {
		select {
		case <-batch.Done():
			break schedule
		case c.delaySlots <- struct{}{}:
		}
		if batch.Err() != nil {
			<-c.delaySlots
			break
		}
		started++
		go func() {
			defer func() { <-c.delaySlots }()
			probe, cancel := context.WithTimeout(batch, delayTimeout)
			defer cancel()
			delay := probeDelay(probe, p, ipv4URL, ipv6URL)
			if batch.Err() == nil || delay > 0 {
				results <- measurement{p.Name(), delay}
			}
		}()
	}

	for range started {
		select {
		case result := <-results:
			out[result.name] = result.delay
		case <-batch.Done():
			for len(results) != 0 {
				result := <-results
				out[result.name] = result.delay
			}
			return out, ctx.Err()
		}
	}
	return out, ctx.Err()
}

func probeDelay(ctx context.Context, proxy constant.Proxy, ipv4URL, ipv6URL string) int {
	// Leave time for an IPv6-only exit when the IPv4 dial times out.
	ipv4, cancel := context.WithTimeout(ctx, delayTimeout/2)
	delay := measureDelay(ipv4, proxy, ipv4URL)
	cancel()
	if delay == 0 && ctx.Err() == nil {
		delay = measureDelay(ctx, proxy, ipv6URL)
	}
	return delay
}

func measureDelay(ctx context.Context, proxy constant.Proxy, url string) int {
	if ctx.Err() != nil {
		return 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	metadata := &constant.Metadata{NetWork: constant.TCP, Type: constant.INNER}
	if err := metadata.SetRemoteAddress(net.JoinHostPort(req.URL.Hostname(), port)); err != nil {
		return 0
	}
	began := time.Now()
	// URLTest writes to mihomo's health history even when canceled. Open the
	// connection directly so a manual check cannot change automatic routing.
	conn, err := proxy.DialContext(ctx, metadata)
	if err != nil {
		return 0
	}
	defer conn.Close()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:       func(context.Context, string, string) (net.Conn, error) { return conn, nil },
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0
	}
	return max(1, int(time.Since(began).Milliseconds()))
}

func (c *Core) Rules(ctx context.Context) ([]protocol.Rule, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, core.ErrNotRunning
	}
	return slices.Clone(c.current.rules), nil
}

func (c *Core) Connections(ctx context.Context) (protocol.Connections, error) {
	c.mu.Lock()
	running, history := c.running, c.history
	c.mu.Unlock()
	out := protocol.Connections{Connections: []protocol.Connection{}}
	if history == nil {
		return out, nil
	}
	summary, open := history.Summary()
	out.Summary = summary
	out.UploadTotal, out.DownloadTotal = out.Summary.Upload, out.Summary.Download
	if !running {
		return out, nil
	}
	var infos []*statistic.TrackerInfo
	statistic.DefaultManager.Range(func(t statistic.Tracker) bool {
		if info := t.Info(); info.Session.Load() == history.Session() {
			infos = append(infos, info)
		}
		return true
	})
	slices.SortFunc(infos, func(a, b *statistic.TrackerInfo) int { return b.Start.Compare(a.Start) })
	out.Open = open
	out.Connections = make([]protocol.Connection, 0, min(len(infos), core.MaxConnections))
	var rowBytes int
	for _, info := range infos[:min(len(infos), core.MaxConnections)] {
		conn := connection(info)
		// Reserve room for the session summary and worst-case JSON escaping.
		size := 384 + 6*(len(conn.ID)+len(conn.Network)+len(conn.Inbound)+len(conn.Host)+len(conn.Process)+
			len(conn.SourceIP)+len(conn.DestinationIP)+len(conn.Chain)+len(conn.Rule)+len(conn.ProcessStatus)+len(conn.ProcessReason))
		if size > protocol.MaxLine/2 {
			continue
		}
		if rowBytes+size > protocol.MaxLine/2 {
			break
		}
		rowBytes += size
		out.Connections = append(out.Connections, conn)
	}
	return out, nil
}

func connection(info *statistic.TrackerInfo) protocol.Connection {
	m := info.Metadata
	name, status, reason := processAttribution(m)
	conn := protocol.Connection{
		ID:              info.UUID.String(),
		Network:         m.NetWork.String(),
		Inbound:         strings.ToLower(m.Type.String()),
		Host:            m.RuleHost(),
		Process:         name,
		ProcessStatus:   status,
		ProcessReason:   reason,
		SourcePort:      int(m.SrcPort),
		DestinationPort: int(m.DstPort),
		Rule:            info.Rule,
		Upload:          info.UploadTotal.Load(),
		Download:        info.DownloadTotal.Load(),
		StartedAt:       info.Start.UnixMilli(),
	}
	if m.SrcIP.IsValid() {
		conn.SourceIP = m.SrcIP.String()
	}
	if m.DstIP.IsValid() {
		conn.DestinationIP = m.DstIP.String()
	}
	if info.MultipleDestinations.Load() {
		conn.Host, conn.DestinationIP, conn.DestinationPort = "Multiple destinations", "", 0
	}
	conn.Chain = info.Chain.Last()
	if info.RulePayload != "" {
		conn.Rule += "(" + info.RulePayload + ")"
	}
	return conn
}

func (c *Core) CloseConnections(ctx context.Context, ids []string) error {
	if _, _, err := c.state(); err != nil {
		return err
	}
	if ids == nil {
		closeAll()
		return nil
	}
	for _, id := range ids {
		if t := statistic.DefaultManager.Get(id); t != nil {
			t.Close()
		}
	}
	return nil
}

func (c *Core) Traffic(ctx context.Context) (protocol.Traffic, error) {
	if _, _, err := c.state(); err != nil {
		return protocol.Traffic{}, err
	}
	up, down := statistic.DefaultManager.Now()
	return protocol.Traffic{Up: up, Down: down}, nil
}

func (c *Core) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return c.dial(ctx, "", address)
	default:
		return nil, fmt.Errorf("dial %s: only TCP goes through the core", network)
	}
}

func (c *Core) DialProxy(ctx context.Context, proxy, address string) (net.Conn, error) {
	return c.dial(ctx, proxy, address)
}

func (c *Core) dial(ctx context.Context, proxy, address string) (net.Conn, error) {
	if _, _, err := c.state(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := inner.HandleTcp(tunnel.Tunnel, address, proxy)
	if err != nil {
		return nil, fmt.Errorf("dial %s through the core: %w", address, err)
	}
	return conn, nil
}
