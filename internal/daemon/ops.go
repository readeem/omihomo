package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

type settings struct {
	mode         string
	tun          bool
	tunRedirect  bool
	tailscale    bool
	tailnetProxy bool
	primaryGroup string
}

func defaultSettings() settings { return settings{mode: protocol.ModeRule} }

// tunBypass is the address space that never leaves through TUN: loopback,
// the private, CGNAT and link-local ranges, the documentation and multicast
// blocks, and their IPv6 equivalents. Without it auto-route swallows the
// LAN, and the router, printers and local DNS stop answering once TUN is up.
//
// The multicast and reserved space stops just short of the last address:
// sing-tun cannot put a range that runs to it in an nftables interval set,
// and TUN redirect then never starts.
var tunBypass = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	tailnetRange,
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/5"),
	netip.MustParsePrefix("::/127"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/9"),
}

var (
	tailnetRange = netip.MustParsePrefix("100.64.0.0/10")
	// Tailscale's MagicDNS resolver sits inside the tailnet range but must
	// still be answered by Tailscale when the rest of the tailnet is proxied.
	tailscaleDNS = netip.MustParsePrefix("100.100.100.100/32")
)

func (s settings) bypass() []netip.Prefix {
	if !s.tailnetProxy {
		return slices.Clone(tunBypass)
	}
	out := slices.DeleteFunc(slices.Clone(tunBypass), func(p netip.Prefix) bool { return p == tailnetRange })
	return append(out, tailscaleDNS)
}

func (s settings) config(profile core.Profile) core.Config {
	return core.Config{
		Mode:         s.mode,
		TUN:          s.tun,
		TUNRedirect:  s.tunRedirect,
		Tailscale:    s.tailscale,
		TailnetProxy: s.tailnetProxy,
		PrimaryGroup: s.primaryGroup,
		TUNBypass:    s.bypass(),
		Profile:      profile,
	}
}

// Bounds on what a client may name in one call.
const (
	maxName  = 256
	maxNames = 4096
)

func fail(code, format string, args ...any) *protocol.Error {
	return &protocol.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func coreError(err error) *protocol.Error {
	switch {
	case errors.Is(err, core.ErrNotRunning):
		return fail(protocol.CodeNotRunning, "the proxy is not running")
	case errors.Is(err, core.ErrNotFound):
		return fail(protocol.CodeNotFound, "%v", err)
	case errors.Is(err, context.DeadlineExceeded):
		return fail(protocol.CodeCore, "timed out")
	default:
		return fail(protocol.CodeCore, "%v", err)
	}
}

func decode(raw json.RawMessage, v any) *protocol.Error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	if err := protocol.Decode(raw, v); err != nil {
		return fail(protocol.CodeBadRequest, "invalid args: %v", err)
	}
	return nil
}

func validName(what, name string) *protocol.Error {
	if name == "" {
		return fail(protocol.CodeInvalid, "%s is empty", what)
	}
	if len(name) > maxName || strings.ContainsFunc(name, unicode.IsControl) {
		return fail(protocol.CodeInvalid, "%s is not a valid name", what)
	}
	return nil
}

func validNames(what string, names []string) *protocol.Error {
	if len(names) > maxNames {
		return fail(protocol.CodeInvalid, "too many %ss", what)
	}
	for _, n := range names {
		if err := validName(what, n); err != nil {
			return err
		}
	}
	return nil
}

func (d *server) handle(ctx context.Context, s *session, req protocol.Request) (any, *protocol.Error) {
	switch req.Op {
	case protocol.OpStatus:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.status(), nil
	case protocol.OpSubscribe, protocol.OpUnsubscribe:
		var args protocol.StreamsArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.subscribe(ctx, s, args.Streams, req.Op == protocol.OpSubscribe)
	case protocol.OpStart:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.start(ctx)
	case protocol.OpStop:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.stop(ctx)
	case protocol.OpSetSettings:
		var args protocol.SettingsArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.setSettings(ctx, args)
	case protocol.OpGroups:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		groups, err := d.core.Groups(ctx)
		if err != nil {
			return nil, coreError(err)
		}
		return protocol.GroupList{Groups: groups}, nil
	case protocol.OpSelect:
		var args protocol.SelectArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.selectProxy(ctx, args)
	case protocol.OpDelayTest:
		var args protocol.DelayArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.testDelay(ctx, args)
	case protocol.OpRules:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.rules(ctx)
	case protocol.OpConnections:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		conns, err := d.core.Connections(ctx)
		if err != nil {
			return nil, coreError(err)
		}
		return conns, nil
	case protocol.OpCloseConnections:
		var args protocol.CloseArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.closeConnections(ctx, args)
	case protocol.OpEgress:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.egress(ctx)
	case protocol.OpSubscriptions, protocol.OpSubscriptionAdd, protocol.OpSubscriptionUpdate,
		protocol.OpSubscriptionUse, protocol.OpSubscriptionRemove, protocol.OpRuleAdd, protocol.OpRuleRemove:
		return nil, fail(protocol.CodeUnsupported, "%s is not implemented by this daemon yet", req.Op)
	default:
		return nil, fail(protocol.CodeUnknownOp, "unknown op %q", req.Op)
	}
}

func (d *server) status() protocol.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	bypass := []string{}
	for _, p := range d.settings.bypass() {
		bypass = append(bypass, p.String())
	}
	return protocol.Status{
		State:            d.state,
		Detail:           d.detail,
		PrimaryGroup:     d.settings.primaryGroup,
		Mode:             d.settings.mode,
		TUN:              d.settings.tun,
		TUNRedirect:      d.settings.tunRedirect,
		Tailscale:        d.settings.tailscale,
		TailscalePresent: d.cfg.tailscalePresent(),
		TailnetProxy:     d.settings.tailnetProxy,
		StartedAt:        d.startedAt,
		TUNBypass:        bypass,
	}
}

var stateStreams = []string{protocol.StreamStatus, protocol.StreamGroups, protocol.StreamRules}

var allStreams = append(slices.Clone(stateStreams), protocol.StreamTraffic, protocol.StreamConnections)

func (d *server) snapshot(ctx context.Context, stream string) any {
	switch stream {
	case protocol.StreamStatus:
		return d.status()
	case protocol.StreamGroups:
		groups, err := d.core.Groups(ctx)
		if err != nil {
			groups = []protocol.Group{}
		}
		return protocol.GroupList{Groups: groups}
	case protocol.StreamRules:
		rules, _ := d.rules(ctx)
		return rules
	}
	return nil
}

func (d *server) publish(ctx context.Context) {
	d.pubMu.Lock()
	defer d.pubMu.Unlock()
	for _, stream := range stateStreams {
		d.publishLocked(ctx, stream, nil)
	}
}

// publishLocked pushes stream to its subscribers if it changed, and to also
// regardless. The caller holds pubMu.
func (d *server) publishLocked(ctx context.Context, stream string, also *session) {
	b := d.eventLine(stream, d.snapshot(ctx, stream))
	if !bytes.Equal(d.lastPushed[stream], b) {
		d.lastPushed[stream] = b
		for _, s := range d.subscribers(stream) {
			if s != also {
				s.sendLine(b)
			}
		}
	}
	if also != nil {
		also.sendLine(b)
	}
}

// Subscribing to a state stream pushes its current value before the reply.
func (d *server) subscribe(ctx context.Context, s *session, streams []string, on bool) (any, *protocol.Error) {
	if len(streams) == 0 {
		return nil, fail(protocol.CodeInvalid, "no streams named")
	}
	for _, stream := range streams {
		if !slices.Contains(allStreams, stream) {
			return nil, fail(protocol.CodeInvalid, "unknown stream %q", stream)
		}
	}
	d.pubMu.Lock()
	defer d.pubMu.Unlock()
	var fresh []string
	d.mu.Lock()
	for _, stream := range streams {
		if on && !s.streams[stream] {
			fresh = append(fresh, stream)
		}
		if on {
			s.streams[stream] = true
		} else {
			delete(s.streams, stream)
		}
	}
	d.mu.Unlock()
	for _, stream := range fresh {
		if slices.Contains(stateStreams, stream) {
			d.publishLocked(ctx, stream, s)
		}
	}
	return protocol.Empty{}, nil
}

func (d *server) setState(state, detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = state
	d.detail = detail
	switch state {
	case protocol.StateOn:
		if d.startedAt == 0 {
			d.startedAt = time.Now().UnixMilli()
		}
	case protocol.StateStopped:
		d.startedAt = 0
	}
}

func (d *server) start(ctx context.Context) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if d.running() {
		return d.status(), nil
	}
	d.setState(protocol.StateStarting, "")
	d.publish(ctx)
	d.mu.Lock()
	cfg := d.settings.config(d.cfg.profile)
	d.mu.Unlock()
	if err := d.core.Apply(ctx, cfg); err != nil {
		d.log.Error("start the proxy", "err", err)
		d.setState(protocol.StateStopped, err.Error())
		d.publish(ctx)
		return nil, coreError(err)
	}
	d.log.Info("proxy started")
	d.setState(protocol.StateOn, "")
	d.publish(ctx)
	return d.status(), nil
}

func (d *server) stop(ctx context.Context) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if err := d.core.Stop(ctx); err != nil {
		d.log.Error("stop the proxy", "err", err)
		return nil, coreError(err)
	}
	d.log.Info("proxy stopped")
	d.setState(protocol.StateStopped, "")
	d.publish(ctx)
	return d.status(), nil
}

func (d *server) setSettings(ctx context.Context, args protocol.SettingsArgs) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next := d.settings
	d.mu.Unlock()

	if args.Mode != nil {
		switch *args.Mode {
		case protocol.ModeRule, protocol.ModeGlobal, protocol.ModeDirect:
			next.mode = *args.Mode
		default:
			return nil, fail(protocol.CodeInvalid, "mode must be rule, global or direct")
		}
	}
	if args.TUN != nil {
		next.tun = *args.TUN
	}
	if args.TUNRedirect != nil {
		next.tunRedirect = *args.TUNRedirect
	}
	if args.Tailscale != nil {
		next.tailscale = *args.Tailscale
	}
	if args.TailnetProxy != nil {
		next.tailnetProxy = *args.TailnetProxy
	}
	if (args.Tailscale != nil && *args.Tailscale || args.TailnetProxy != nil && *args.TailnetProxy) &&
		!d.cfg.tailscalePresent() {
		return nil, fail(protocol.CodeConflict, "tailscaled is not installed")
	}
	running := d.running()
	if args.PrimaryGroup != nil {
		name := *args.PrimaryGroup
		if name != "" {
			if err := validName("group", name); err != nil {
				return nil, err
			}
			if running {
				groups, err := d.core.Groups(ctx)
				if err != nil {
					return nil, coreError(err)
				}
				if !slices.ContainsFunc(groups, func(g protocol.Group) bool { return g.Name == name && g.Selectable }) {
					return nil, fail(protocol.CodeNotFound, "no selectable group %q", name)
				}
			}
		}
		next.primaryGroup = name
	}

	if running {
		if err := d.core.Apply(ctx, next.config(d.cfg.profile)); err != nil {
			d.log.Error("apply settings", "err", err)
			if errors.Is(err, core.ErrStopped) {
				d.setState(protocol.StateStopped, err.Error())
				d.publish(ctx)
			}
			return nil, coreError(err)
		}
	}
	d.mu.Lock()
	d.settings = next
	d.mu.Unlock()
	d.publish(ctx)
	return d.status(), nil
}

func (d *server) selectProxy(ctx context.Context, args protocol.SelectArgs) (any, *protocol.Error) {
	if err := validName("group", args.Group); err != nil {
		return nil, err
	}
	if err := validName("proxy", args.Proxy); err != nil {
		return nil, err
	}
	if err := d.core.Select(ctx, args.Group, args.Proxy); err != nil {
		return nil, coreError(err)
	}
	d.publish(ctx)
	groups, err := d.core.Groups(ctx)
	if err != nil {
		return nil, coreError(err)
	}
	for _, g := range groups {
		if g.Name == args.Group {
			return g, nil
		}
	}
	return nil, fail(protocol.CodeNotFound, "group %q is gone", args.Group)
}

func (d *server) testDelay(ctx context.Context, args protocol.DelayArgs) (any, *protocol.Error) {
	if err := validName("group", args.Group); err != nil {
		return nil, err
	}
	if err := validNames("proxy", args.Proxies); err != nil {
		return nil, err
	}
	delays, err := d.core.TestDelay(ctx, args.Group, args.Proxies)
	if err != nil {
		return nil, coreError(err)
	}
	d.publish(ctx)
	return protocol.DelayResult{Delays: delays}, nil
}

func (d *server) rules(ctx context.Context) (protocol.RuleList, *protocol.Error) {
	rules, err := d.core.Rules(ctx)
	if errors.Is(err, core.ErrNotRunning) {
		return protocol.RuleList{Rules: []protocol.Rule{}}, nil
	}
	if err != nil {
		return protocol.RuleList{}, coreError(err)
	}
	return protocol.RuleList{Rules: rules}, nil
}

func (d *server) closeConnections(ctx context.Context, args protocol.CloseArgs) (any, *protocol.Error) {
	if args.All == (len(args.IDs) > 0) {
		return nil, fail(protocol.CodeInvalid, "name either connection ids or all")
	}
	ids := args.IDs
	if args.All {
		ids = nil
	} else if err := validNames("connection id", ids); err != nil {
		return nil, err
	}
	if err := d.core.CloseConnections(ctx, ids); err != nil {
		return nil, coreError(err)
	}
	return protocol.Empty{}, nil
}

func (d *server) egress(ctx context.Context) (any, *protocol.Error) {
	if !d.running() {
		return nil, fail(protocol.CodeNotRunning, "the proxy is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		Proxy:             nil,
		DialContext:       d.core.DialContext,
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.egressURL, nil)
	if err != nil {
		return nil, fail(protocol.CodeInternal, "%v", err)
	}
	began := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fail(protocol.CodeCore, "egress check failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fail(protocol.CodeCore, "egress check failed: %s", resp.Status)
	}
	ip, err := traceIP(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fail(protocol.CodeCore, "egress check failed: %v", err)
	}
	latency := max(1, int(time.Since(began).Milliseconds()))
	return protocol.Egress{IP: ip, Latency: latency}, nil
}

func traceIP(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		value, ok := strings.CutPrefix(scanner.Text(), "ip=")
		if !ok {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			return "", errors.New("the trace has no valid address")
		}
		return addr.String(), nil
	}
	return "", errors.New("the trace has no address")
}
