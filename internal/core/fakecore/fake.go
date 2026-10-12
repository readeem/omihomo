// Package fakecore is an in-memory Core for tests and for running the daemon
// without a proxy engine. It proxies nothing: DialContext connects directly.
package fakecore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

// Delays are what TestDelay reports for each fixture proxy; 0 is a failure.
var Delays = map[string]int{"Tokyo": 120, "Frankfurt": 0, "Amsterdam": 85, "DIRECT": 3}

// Core is a fake core.Core with a fixed set of groups, rules and
// connections. The zero value is not usable; call New.
type Core struct {
	mu          sync.Mutex
	running     bool
	cfg         core.Config
	applyErr    error
	groups      []protocol.Group
	connections []protocol.Connection
}

var _ core.Core = (*Core)(nil)

// New returns a stopped fake core with the fixture loaded.
func New() *Core {
	c := &Core{}
	c.reset()
	return c
}

func (c *Core) reset() {
	member := func(name, typ string) protocol.Proxy {
		return protocol.Proxy{Name: name, Type: typ, Alive: true}
	}
	c.groups = []protocol.Group{
		{Name: "Proxy", Type: "select", Now: "Tokyo", Selectable: true, Members: []protocol.Proxy{
			member("Tokyo", "vless"), member("Frankfurt", "trojan"), member("Amsterdam", "hysteria2"),
			member("Auto", "url-test"), member("DIRECT", "direct"),
		}},
		{Name: "Auto", Type: "url-test", Now: "Amsterdam", Members: []protocol.Proxy{
			member("Tokyo", "vless"), member("Frankfurt", "trojan"), member("Amsterdam", "hysteria2"),
		}},
	}
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC).UnixMilli()
	c.connections = []protocol.Connection{
		{ID: "c1", Network: "tcp", Inbound: "tun", Host: "example.com", Process: "firefox",
			SourceIP: "198.18.0.1", SourcePort: 50000, DestinationIP: "93.184.215.14", DestinationPort: 443,
			Chain: "Tokyo", Rule: "Match", Upload: 1200, Download: 48000, StartedAt: start},
		{ID: "c2", Network: "udp", Inbound: "tun", Host: "dns.google", Process: "",
			SourceIP: "198.18.0.1", SourcePort: 50001, DestinationIP: "8.8.8.8", DestinationPort: 443,
			Chain: "DIRECT", Rule: "DomainSuffix(google)", Upload: 300, Download: 900, StartedAt: start},
	}
}

// FailApply makes every following Apply fail with err, or succeed again
// with nil.
func (c *Core) FailApply(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyErr = err
}

// Config is the config the core last applied, and whether it is running.
func (c *Core) Config() (core.Config, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg, c.running
}

func (c *Core) Apply(ctx context.Context, cfg core.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.applyErr != nil {
		if errors.Is(c.applyErr, core.ErrStopped) {
			c.running = false
		}
		return c.applyErr
	}
	if !c.running {
		c.reset()
	}
	c.cfg = cfg
	c.running = true
	return nil
}

func (c *Core) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	return nil
}

func (c *Core) Groups(ctx context.Context) ([]protocol.Group, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, core.ErrNotRunning
	}
	out := make([]protocol.Group, len(c.groups))
	for i, g := range c.groups {
		g.Members = slices.Clone(g.Members)
		out[i] = g
	}
	return out, nil
}

func (c *Core) group(name string) *protocol.Group {
	for i := range c.groups {
		if c.groups[i].Name == name {
			return &c.groups[i]
		}
	}
	return nil
}

func (c *Core) Select(ctx context.Context, group, proxy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return core.ErrNotRunning
	}
	g := c.group(group)
	if g == nil || !g.Selectable {
		return fmt.Errorf("selectable group %q: %w", group, core.ErrNotFound)
	}
	if !slices.ContainsFunc(g.Members, func(p protocol.Proxy) bool { return p.Name == proxy }) {
		return fmt.Errorf("proxy %q in group %q: %w", proxy, group, core.ErrNotFound)
	}
	g.Now = proxy
	return nil
}

func (c *Core) TestDelay(ctx context.Context, group string, proxies []string) (map[string]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, core.ErrNotRunning
	}
	g := c.group(group)
	if g == nil {
		return nil, fmt.Errorf("group %q: %w", group, core.ErrNotFound)
	}
	out := map[string]int{}
	for _, m := range g.Members {
		if len(proxies) > 0 && !slices.Contains(proxies, m.Name) {
			continue
		}
		out[m.Name] = Delays[m.Name]
	}
	for _, name := range proxies {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("proxy %q in group %q: %w", name, group, core.ErrNotFound)
		}
	}
	for i := range c.groups {
		for j, m := range c.groups[i].Members {
			if d, ok := out[m.Name]; ok {
				c.groups[i].Members[j].Delay = d
				c.groups[i].Members[j].Alive = d > 0
			}
		}
	}
	return out, nil
}

func (c *Core) Rules(ctx context.Context) ([]protocol.Rule, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil, core.ErrNotRunning
	}
	return []protocol.Rule{
		{Type: "DOMAIN-SUFFIX", Value: "google.com", Target: "DIRECT", Source: protocol.SourceSubscription},
		{Type: "MATCH", Value: "", Target: "Proxy", Source: protocol.SourceSubscription},
	}, nil
}

func (c *Core) Connections(ctx context.Context) (protocol.Connections, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return protocol.Connections{}, core.ErrNotRunning
	}
	out := protocol.Connections{Open: len(c.connections), Connections: slices.Clone(c.connections)}
	for _, conn := range c.connections {
		out.UploadTotal += conn.Upload
		out.DownloadTotal += conn.Download
	}
	return out, nil
}

func (c *Core) CloseConnections(ctx context.Context, ids []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return core.ErrNotRunning
	}
	c.connections = slices.DeleteFunc(c.connections, func(conn protocol.Connection) bool {
		return ids == nil || slices.Contains(ids, conn.ID)
	})
	return nil
}

func (c *Core) Traffic(ctx context.Context) (protocol.Traffic, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return protocol.Traffic{}, core.ErrNotRunning
	}
	return protocol.Traffic{Up: 2048, Down: 65536}, nil
}

func (c *Core) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		return nil, core.ErrNotRunning
	}
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
