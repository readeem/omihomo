// Package xray supplies outbound transports for mihomo's routing engine.
package xray

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/uplink"

	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	proxyman "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/hysteria"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/hysteria"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

type SocketOptions struct {
	IPVersion string
	TFO       bool
	MPTCP     bool
	Bootstrap bool
}

type Network interface {
	DialContext(ctx context.Context, address string, options SocketOptions) (net.Conn, error)
	ListenPacket(ctx context.Context, address string, options SocketOptions) (net.PacketConn, *net.UDPAddr, error)
}

type Outbounds struct {
	instance *xcore.Instance
	ctx      context.Context
	cancel   context.CancelFunc
	byName   map[string]*Outbound
	keys     []string
}

type Outbound struct {
	ctx       context.Context
	handler   *proxyman.Handler
	bootstrap *proxyman.Handler
	udp       bool
}

type socketPolicy struct {
	ctx     context.Context
	network Network
	options SocketOptions
}

const socketPrefix = "omihomo/"

var (
	nextRuntime    atomic.Uint64
	policies       sync.Map
	errUnreachable = errors.New("could not connect through the Xray outbound")
)

type ownerContextConfig struct{}

func init() {
	internet.UseAlternativeSystemDialer(&systemDialer{})
	common.Must(common.RegisterConfig(ownerContextConfig{}, func(ctx context.Context, _ any) (any, error) {
		return ctx, nil
	}))
}

// New uses mihomo's socket policy for every outbound, including connections
// opened by a pooled Xray transport after the original dial returns.
func New(configs []core.Proxy, network Network) (*Outbounds, error) {
	ctx, cancel := context.WithCancel(context.Background())
	o := &Outbounds{ctx: ctx, cancel: cancel, byName: make(map[string]*Outbound)}
	cfg, err := (&conf.Config{LogConfig: &conf.LogConfig{LogLevel: "none"}}).Build()
	if err != nil {
		cancel()
		return nil, err
	}
	prefix := socketPrefix + strconv.FormatUint(nextRuntime.Add(1), 10) + "/"
	supported := make(map[string]core.Proxy)
	for i, p := range configs {
		tag := proxyTag(i)
		key := prefix + tag
		config, reason := proxyOutbound(tag, key, p)
		if reason != "" {
			continue
		}
		opts := fields(p.Options)
		options := SocketOptions{
			IPVersion: opts.string("ip-version"), TFO: opts["tfo"] == true, MPTCP: opts["mptcp"] == true,
		}
		policies.Store(key, socketPolicy{ctx, network, options})
		o.keys = append(o.keys, key)
		supported[tag] = p
		cfg.Outbound = append(cfg.Outbound, config)
		// Pooled transports can dial with a context unrelated to the request
		// that needs DNS. Keep recursive bootstrap on a separate pool.
		switch opts.string("network") {
		case "grpc", "xhttp", "splithttp":
			if isDomain(opts.string("server")) {
				bootstrapTag, bootstrapKey := tag+"-bootstrap", key+"-bootstrap"
				bootstrap, reason := proxyOutbound(bootstrapTag, bootstrapKey, p)
				if reason != "" {
					o.Close()
					return nil, errors.New("could not configure the Xray bootstrap outbound")
				}
				options.Bootstrap = true
				policies.Store(bootstrapKey, socketPolicy{ctx, network, options})
				o.keys = append(o.keys, bootstrapKey)
				cfg.Outbound = append(cfg.Outbound, bootstrap)
			}
		}
	}
	if len(supported) == 0 {
		return o, nil
	}
	o.instance, err = xcore.NewWithContext(ctx, cfg)
	if err == nil {
		err = o.instance.Start()
	}
	if err != nil {
		o.Close()
		// Xray config and start errors can contain credentials.
		return nil, errors.New("could not start the Xray outbounds")
	}
	manager := o.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	owner, err := xcore.CreateObject(o.instance, ownerContextConfig{})
	if err != nil {
		o.Close()
		return nil, errors.New("could not bind Xray outbounds to their owner")
	}
	for tag, proxy := range supported {
		bootstrap, _ := manager.GetHandler(tag + "-bootstrap").(*proxyman.Handler)
		o.byName[proxy.Name] = &Outbound{owner.(context.Context), manager.GetHandler(tag).(*proxyman.Handler), bootstrap, supportsUDP(proxy)}
	}
	return o, nil
}

func (o *Outbounds) Get(name string) *Outbound { return o.byName[name] }

func (o *Outbounds) Close() error {
	o.cancel()
	for _, key := range o.keys {
		policies.Delete(key)
	}
	if o.instance != nil {
		return o.instance.Close()
	}
	return nil
}

func (o *Outbound) SupportUDP() bool { return o.udp }

func supportsUDP(p core.Proxy) bool {
	switch p.Type {
	case "hysteria2", "trojan", "ss":
		return true
	}
	return false
}

type bootstrapLookup struct{}

func WithBootstrapLookup(ctx context.Context) context.Context {
	return context.WithValue(ctx, bootstrapLookup{}, true)
}

type sessionContext struct {
	context.Context
	owner context.Context
}

func (c sessionContext) Value(key any) any {
	if value := c.Context.Value(key); value != nil {
		return value
	}
	return c.owner.Value(key)
}

func (o *Outbound) session(ctx context.Context, dest xnet.Destination) (context.Context, context.CancelFunc, *proxyman.Handler) {
	handler := o.handler
	if o.bootstrap != nil && ctx.Value(bootstrapLookup{}) == true {
		handler = o.bootstrap
	}
	run, cancel := context.WithCancel(sessionContext{context.WithoutCancel(ctx), o.ctx})
	stop := context.AfterFunc(o.ctx, cancel)
	run = session.ContextWithInbound(run, &session.Inbound{})
	run = session.ContextWithOutbounds(run, []*session.Outbound{{
		Tag: handler.Tag(), Target: dest, OriginalTarget: dest,
	}})
	return run, func() { stop(); cancel() }, handler
}

func (o *Outbound) DialContext(ctx context.Context, host string, port uint16) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, cancel, handler := o.session(ctx, tcpDestination(host, port))
	client, relay := net.Pipe()
	disarm := context.AfterFunc(run, func() { relay.Close() })
	ready := make(chan struct{})
	deadline, _ := ctx.Deadline()
	d := &tcpAttempt{Dialer: handler, ready: ready, deadline: deadline}
	run = context.WithValue(run, attemptKey{}, d)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		defer disarm()
		defer relay.Close()
		reader := &buf.TimeoutWrapperReader{Reader: buf.NewReader(relay)}
		handler.GetOutbound().Process(run, &transport.Link{Reader: reader, Writer: buf.NewWriter(relay)}, d)
	}()
	select {
	case <-ready:
		return client, nil
	case <-done:
	case <-ctx.Done():
	case <-o.ctx.Done():
	}
	client.Close()
	cancel()
	if d.failed.Load() && !d.opened.Load() {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errUnreachable}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", errUnreachable, err)
	}
	return nil, errUnreachable
}

type attemptKey struct{}

type tcpAttempt struct {
	internet.Dialer
	ready    chan struct{}
	once     sync.Once
	deadline time.Time
	opened   atomic.Bool
	failed   atomic.Bool
}

func (d *tcpAttempt) connected() { d.once.Do(func() { close(d.ready) }) }

func (d *tcpAttempt) Dial(ctx context.Context, dest xnet.Destination) (stat.Connection, error) {
	conn, err := d.Dialer.Dial(ctx, dest)
	if err == nil {
		d.connected()
	}
	return conn, err
}

type systemDialer struct{ internet.DefaultSystemDialer }

func (d *systemDialer) Dial(ctx context.Context, src xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	if opts == nil || !strings.HasPrefix(opts.Interface, socketPrefix) {
		return d.DefaultSystemDialer.Dial(ctx, src, dest, opts)
	}
	slot, ok := policies.Load(opts.Interface)
	if !ok {
		return nil, net.ErrClosed
	}
	policy := slot.(socketPolicy)
	timeout := 5 * time.Second
	attempt, _ := ctx.Value(attemptKey{}).(*tcpAttempt)
	if attempt != nil && !attempt.deadline.IsZero() {
		timeout = min(timeout, time.Until(attempt.deadline))
	}
	dial, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(policy.ctx, cancel)
	defer stop()
	if policy.ctx.Err() != nil {
		return nil, net.ErrClosed
	}
	if dest.Network == xnet.Network_UDP {
		packet, address, err := policy.network.ListenPacket(dial, dest.NetAddr(), policy.options)
		if err != nil {
			return nil, err
		}
		return &xnet.PacketConnWrapper{
			PacketConn: &trackedPacket{PacketConn: packet, stop: context.AfterFunc(policy.ctx, func() { packet.Close() })},
			Dest:       address,
		}, nil
	}
	conn, err := policy.network.DialContext(dial, dest.NetAddr(), policy.options)
	if attempt != nil {
		if err == nil {
			attempt.opened.Store(true)
		} else if uplink.Recoverable(err) || errors.Is(err, context.DeadlineExceeded) {
			attempt.failed.Store(true)
		}
	}
	if err != nil {
		return nil, err
	}
	return &trackedConn{Conn: conn, stop: context.AfterFunc(policy.ctx, func() { conn.Close() })}, nil
}

type trackedConn struct {
	net.Conn
	stop func() bool
}

func (c *trackedConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

type trackedPacket struct {
	net.PacketConn
	stop func() bool
}

func (c *trackedPacket) Close() error {
	c.stop()
	return c.PacketConn.Close()
}
