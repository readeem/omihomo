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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/component/profile/cachefile"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/constant/features"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/listener/inner"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

// delayURL is what a delay test fetches, as the widget always has.
var delayURL = "http://www.gstatic.com/generate_204"

const (
	drainTick    = 20 * time.Millisecond
	delayTimeout = 5 * time.Second
	// Proxies tested at once by one delay test.
	delayParallel = 16
)

// Core is core.Core over the mihomo engine hosted in this process.
type Core struct {
	log *slog.Logger
	tap *logTap

	// applyMu serializes everything that parses or applies a config:
	// parsing mutates mihomo's globals while it runs.
	applyMu sync.Mutex

	mu      sync.Mutex
	running bool
	current *runtime
	// generation counts commits and stops, so a drain knows when the
	// stop it follows is over.
	generation uint64
}

var _ core.Core = (*Core)(nil)

var hosted atomic.Bool

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
	C.SetHomeDir(home)
	return &Core{log: log, tap: startLogTap(log), current: &runtime{}}, nil
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
		return err
	}
	c.mu.Lock()
	wasRunning, previous := c.running, c.current.cfg
	c.mu.Unlock()

	if err := c.run(next); err != nil {
		if !wasRunning {
			c.stop()
			return err
		}
		if rerr := c.restore(previous); rerr != nil {
			return fmt.Errorf("%w; the previous config did not come back either (%v): %w", err, rerr, core.ErrStopped)
		}
		return err
	}
	return nil
}

// afterRestoreStop lets a test act between a restore's stop and its apply.
var afterRestoreStop = func() {}

// run applies rt and commits it once everything it asks for came up.
func (c *Core) run(rt *runtime) error {
	stop := c.tap.watch()
	executor.ApplyConfig(rt.parsed, true)
	if err := started(rt.cfg, stop()); err != nil {
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

// runtime is a parsed config and what the core reports about it: rules in
// match order with their sources, the groups in profile order, and the type
// each proxy and group was given.
type runtime struct {
	cfg    core.Config
	parsed *config.Config
	rules  []protocol.Rule
	groups []string
	types  map[string]string
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
	rt := &runtime{cfg: cfg, parsed: parsed, rules: rules, types: map[string]string{
		"DIRECT": "direct", "REJECT": "reject", "REJECT-DROP": "reject-drop", "PASS": "pass", "COMPATIBLE": "compatible",
	}}
	for _, p := range cfg.Profile.Proxies {
		rt.types[p.Name] = p.Type
	}
	for _, g := range cfg.Profile.Groups {
		if g.Name != globalGroup {
			rt.types[g.Name] = g.Type
			rt.groups = append(rt.groups, g.Name)
		}
	}
	return rt, nil
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
	defer c.mu.Unlock()
	c.running, c.current = true, rt
	c.generation++
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
	c.stop()
	return nil
}

func (c *Core) stop() {
	c.mu.Lock()
	c.running, c.current = false, &runtime{}
	c.generation++
	generation := c.generation
	c.mu.Unlock()
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
	deadline := time.Now().Add(C.DefaultTCPTimeout + time.Second)
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
func (c *Core) state() (groups []string, types map[string]string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, nil, core.ErrNotRunning
	}
	return c.current.groups, c.current.types, nil
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

func (c *Core) Groups(ctx context.Context) ([]protocol.Group, error) {
	names, types, err := c.state()
	if err != nil {
		return nil, err
	}
	proxies := tunnel.Proxies()
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
		group := protocol.Group{Name: name, Type: types[name], Now: g.Now(), Selectable: selectable,
			Members: make([]protocol.Proxy, 0, len(members))}
		for _, m := range members {
			group.Members = append(group.Members, member(m, types))
		}
		out = append(out, group)
	}
	return out, nil
}

func member(p C.Proxy, types map[string]string) protocol.Proxy {
	typ, ok := types[p.Name()]
	if !ok {
		typ = strings.ToLower(p.Type().String())
	}
	out := protocol.Proxy{Name: p.Name(), Type: typ, Alive: p.AliveForTestUrl("")}
	if history := p.DelayHistory(); len(history) > 0 && out.Alive {
		// A test faster than a millisecond still measured something.
		out.Delay = max(1, int(history[len(history)-1].Delay))
	}
	return out
}

func (c *Core) Select(ctx context.Context, group, proxy string) error {
	// A reload in between would restore the cached choice into a new
	// selector before this one is cached.
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	g, err := c.group(group)
	if err != nil {
		return err
	}
	selector, ok := g.(*outboundgroup.Selector)
	if !ok {
		return fmt.Errorf("selectable group %q: %w", group, core.ErrNotFound)
	}
	if !slices.ContainsFunc(g.Proxies(), func(p C.Proxy) bool { return p.Name() == proxy }) {
		return fmt.Errorf("proxy %q in group %q: %w", proxy, group, core.ErrNotFound)
	}
	if err := selector.Set(proxy); err != nil {
		return fmt.Errorf("select %q in %q: %w", proxy, group, err)
	}
	cachefile.Cache().SetSelected(group, proxy)
	return nil
}

func (c *Core) TestDelay(ctx context.Context, group string, proxies []string) (map[string]int, error) {
	g, err := c.group(group)
	if err != nil {
		return nil, err
	}
	members := map[string]C.Proxy{}
	for _, p := range g.Proxies() {
		members[p.Name()] = p
	}
	var tested []C.Proxy
	if len(proxies) == 0 {
		tested = g.Proxies()
	}
	for _, name := range proxies {
		p, ok := members[name]
		if !ok {
			return nil, fmt.Errorf("proxy %q in group %q: %w", name, group, core.ErrNotFound)
		}
		tested = append(tested, p)
	}

	ctx, cancel := context.WithTimeout(ctx, delayTimeout)
	defer cancel()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string]int, len(tested))
		sem = make(chan struct{}, delayParallel)
	)
	for _, p := range tested {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			delay, err := p.URLTest(ctx, delayURL, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[p.Name()] = 0
			} else {
				out[p.Name()] = max(1, int(delay))
			}
		})
	}
	wg.Wait()
	return out, nil
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
	if _, _, err := c.state(); err != nil {
		return protocol.Connections{}, err
	}
	snap := statistic.DefaultManager.Snapshot()
	infos := snap.Connections
	slices.SortFunc(infos, func(a, b *statistic.TrackerInfo) int { return b.Start.Compare(a.Start) })
	out := protocol.Connections{
		UploadTotal:   snap.UploadTotal,
		DownloadTotal: snap.DownloadTotal,
		Open:          len(infos),
		Connections:   make([]protocol.Connection, 0, min(len(infos), core.MaxConnections)),
	}
	for _, info := range infos[:min(len(infos), core.MaxConnections)] {
		out.Connections = append(out.Connections, connection(info))
	}
	return out, nil
}

func connection(info *statistic.TrackerInfo) protocol.Connection {
	m := info.Metadata
	conn := protocol.Connection{
		ID:              info.UUID.String(),
		Network:         m.NetWork.String(),
		Inbound:         strings.ToLower(m.Type.String()),
		Host:            m.Host,
		Process:         m.Process,
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
	if len(info.Chain) > 0 {
		conn.Chain = info.Chain[0]
	}
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
	if _, _, err := c.state(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("dial %s: only TCP goes through the core", network)
	}
	conn, err := inner.HandleTcp(tunnel.Tunnel, address, "")
	if err != nil {
		return nil, fmt.Errorf("dial %s through the core: %w", address, err)
	}
	return conn, nil
}
