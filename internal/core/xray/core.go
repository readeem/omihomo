// Package xray adapts the official Xray-core to the daemon's proxy interface.
package xray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"

	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/dns"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/policy"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/app/stats"
	_ "github.com/xtls/xray-core/proxy/blackhole"
	_ "github.com/xtls/xray-core/proxy/dns"
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/tun"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

type Core struct {
	port       int
	parseLinks func([]byte) ([]map[string]any, error)

	mu         sync.Mutex
	current    *runtime
	selections map[string]string
}

type runtime struct {
	instance *xcore.Instance
	plan     *plan
	tun      *tunRoutes
	ctx      context.Context
	cancel   context.CancelFunc
	delays   map[string]int
	last     protocol.Traffic
	sampled  time.Time
}

var _ core.Core = (*Core)(nil)

// New configures the loopback mixed port. Zero leaves that listener off.
func New(port int, parseLinks func([]byte) ([]map[string]any, error)) *Core {
	return &Core{port: port, parseLinks: parseLinks, selections: map[string]string{}}
}

func (c *Core) Check(ctx context.Context, cfg core.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := makePlan(cfg).build(c.port, nil)
	return err
}

func (c *Core) Compatibility(cfg core.Config) core.Compatibility {
	p := makePlan(cfg)
	return core.Compatibility{Warnings: p.warnings, Rules: p.rules}
}

func (c *Core) Apply(ctx context.Context, cfg core.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := makePlan(cfg)
	built, err := p.build(c.port, c.selections)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	prev := c.current
	if prev != nil {
		c.current = nil
		if err := prev.close(); err != nil {
			return c.restore(prev.plan, err)
		}
	}
	rt, err := start(p, built)
	if err != nil {
		if prev != nil {
			return c.restore(prev.plan, err)
		}
		return err
	}
	c.current = rt
	return nil
}

func (c *Core) restore(p *plan, cause error) error {
	cfg, err := p.build(c.port, c.selections)
	if err == nil {
		c.current, err = start(p, cfg)
	}
	if err != nil {
		return errors.Join(cause, fmt.Errorf("%w: restore Xray: %w", core.ErrStopped, err))
	}
	return cause
}

func start(p *plan, cfg *xcore.Config) (*runtime, error) {
	if p.settings.TUN {
		if err := prepareTUN(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	instance, err := xcore.NewWithContext(ctx, cfg)
	if err != nil {
		cancel()
		return nil, errors.New("initialize Xray's proxy, routing, and DNS")
	}
	rt := &runtime{instance: instance, plan: p, ctx: ctx, cancel: cancel, delays: map[string]int{}}
	if err = instance.Start(); err == nil && p.settings.TUN {
		rt.tun, err = setupTUN(p.settings)
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start Xray: %w", err), rt.close())
	}
	return rt, nil
}

func (r *runtime) close() error {
	r.cancel()
	return errors.Join(r.tun.close(), r.instance.Close())
}

func (c *Core) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil
	}
	old := c.current
	c.current = nil
	return old.close()
}

func (c *Core) Groups(context.Context) ([]protocol.Group, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.current
	if r == nil {
		return nil, core.ErrNotRunning
	}

	out := make([]protocol.Group, 0, len(r.plan.groups))
	for _, g := range r.plan.groups {
		if g.Options["hidden"] == true && g.Name != r.plan.primary {
			continue
		}
		row := protocol.Group{Name: g.Name, Type: g.Type, Selectable: true, Now: r.plan.member(g, c.selections)}
		for _, name := range g.Members {
			delay, measured := r.delays[name]
			row.Members = append(row.Members, protocol.Proxy{
				Name: name, Type: r.plan.types[name], Alive: !measured || delay != 0, Delay: delay,
			})
		}
		out = append(out, row)
	}
	return out, nil
}

func (c *Core) Select(ctx context.Context, group, proxy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.current
	if r == nil {
		return core.ErrNotRunning
	}
	g, ok := r.plan.byGroup[group]
	if !ok || !slices.Contains(g.Members, proxy) {
		return fmt.Errorf("%q in group %q: %w", proxy, group, core.ErrNotFound)
	}
	next := maps.Clone(c.selections)
	next[group] = proxy
	cfg, err := r.plan.routing(next)
	if err != nil {
		return fmt.Errorf("update Xray routing: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.instance.GetFeature(routing.RouterType()).(routing.Router).AddRule(serial.ToTypedMessage(cfg), false); err != nil {
		return fmt.Errorf("apply Xray routing: %w", err)
	}
	c.selections = next
	return nil
}

func (c *Core) Rules(context.Context) ([]protocol.Rule, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil, core.ErrNotRunning
	}
	return slices.Clone(c.current.plan.rules), nil
}

func (c *Core) TestDelay(ctx context.Context, group string, proxies []string) (map[string]int, error) {
	c.mu.Lock()
	r := c.current
	if r == nil {
		c.mu.Unlock()
		return nil, core.ErrNotRunning
	}
	g, ok := r.plan.byGroup[group]
	selections := c.selections
	c.mu.Unlock()
	if !ok {
		return nil, core.ErrNotFound
	}
	if len(proxies) == 0 {
		proxies = g.Members
	}
	for _, name := range proxies {
		if !slices.Contains(g.Members, name) {
			return nil, fmt.Errorf("config %q: %w", name, core.ErrNotFound)
		}
	}
	out := make(map[string]int, len(proxies))
	for _, name := range proxies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := time.Now()
		tag := r.plan.resolve(name, selections)
		// Probe a fixed URL. Remote group options never choose a URL for a
		// privileged process to request.
		const endpoint = "https://www.gstatic.com/generate_204"
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return r.dial(ctx, network, address, tag)
		}}
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		out[name] = 0
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				out[name] = max(1, int(time.Since(start).Milliseconds()))
			}
		}
		cancel()
	}
	c.mu.Lock()
	if c.current == r {
		maps.Copy(r.delays, out)
	}
	c.mu.Unlock()
	return out, nil
}

func (r *runtime) totals() protocol.Traffic {
	var total protocol.Traffic
	r.instance.GetFeature(stats.ManagerType()).(stats.Manager).VisitCounters(func(name string, c stats.Counter) bool {
		switch {
		case len(name) >= 6 && name[len(name)-6:] == "uplink":
			total.Up += c.Value()
		case len(name) >= 8 && name[len(name)-8:] == "downlink":
			total.Down += c.Value()
		}
		return true
	})
	return total
}

func (c *Core) Traffic(context.Context) (protocol.Traffic, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.current
	if r == nil {
		return protocol.Traffic{}, core.ErrNotRunning
	}
	total, now := r.totals(), time.Now()
	var rate protocol.Traffic
	if span := now.Sub(r.sampled).Seconds(); !r.sampled.IsZero() && span > 0 {
		rate.Up = int64(float64(total.Up-r.last.Up) / span)
		rate.Down = int64(float64(total.Down-r.last.Down) / span)
	}
	r.last, r.sampled = total, now
	return rate, nil
}

func (c *Core) Connections(context.Context) (protocol.Connections, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return protocol.Connections{}, core.ErrNotRunning
	}
	totals := c.current.totals()
	return protocol.Connections{UploadTotal: totals.Up, DownloadTotal: totals.Down, Connections: []protocol.Connection{}}, nil
}

func (c *Core) CloseConnections(context.Context, []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return core.ErrNotRunning
	}
	return errors.New("Xray does not expose individual connections")
}

func (c *Core) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.mu.Lock()
	r := c.current
	c.mu.Unlock()
	if r == nil {
		return nil, core.ErrNotRunning
	}
	return r.dial(ctx, network, address, "")
}

func (r *runtime) dial(ctx context.Context, network, address string, outbound string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("dial %s: only TCP goes through the core", network)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return nil, errors.New("invalid dial port")
	}
	if !safeHost(host) {
		return nil, errors.New("invalid dial address")
	}
	if r.ctx.Err() != nil {
		return nil, core.ErrNotRunning
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(r.ctx, cancel)
	if outbound != "" {
		ctx = session.SetForcedOutboundTagToContext(ctx, outbound)
	}
	proxy, err := xcore.Dial(ctx, r.instance, tcpDestination(host, uint16(n)))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	// Xray's in-process net.Conn has no deadlines. The pipe gives HTTP
	// callers real deadlines and closes both directions when either ends.
	client, relay := net.Pipe()
	closeRelay := sync.OnceFunc(func() {
		relay.Close()
		proxy.Close()
	})
	disarm := context.AfterFunc(ctx, closeRelay)
	go func() {
		defer stop()
		defer cancel()
		defer disarm()
		var wg sync.WaitGroup
		wg.Go(func() {
			defer closeRelay()
			io.Copy(relay, proxy)
		})
		io.Copy(proxy, relay)
		closeRelay()
		wg.Wait()
	}()
	return client, nil
}

func (c *Core) ParseLinks(data []byte) ([]map[string]any, error) {
	if c.parseLinks == nil {
		return nil, errors.New("share links are unavailable")
	}
	return c.parseLinks(data)
}

func (c *Core) Geodata(core.Profile) []core.GeodataFile { return nil }

func (c *Core) InstallGeodata(core.GeodataFile, string, core.Profile) error {
	return errors.New("Xray does not use downloaded geodata")
}
