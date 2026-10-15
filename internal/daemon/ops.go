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
	"github.com/readeem/omihomo/internal/state"
)

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
	case protocol.OpSubscriptions:
		if err := decode(req.Args, &struct{}{}); err != nil {
			return nil, err
		}
		return d.subscriptionList(), nil
	case protocol.OpSubscriptionAdd:
		var args protocol.SubscriptionURLArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.addSubscription(ctx, args.URL)
	case protocol.OpSubscriptionUpdate, protocol.OpSubscriptionUse, protocol.OpSubscriptionRemove:
		var args protocol.NameArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		switch req.Op {
		case protocol.OpSubscriptionUpdate:
			return d.updateSubscription(ctx, args.Name)
		case protocol.OpSubscriptionUse:
			return d.activateSubscription(ctx, args.Name)
		}
		return d.removeSubscription(ctx, args.Name)
	case protocol.OpRuleAdd:
		var args protocol.RuleArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.addRule(ctx, args)
	case protocol.OpRuleRemove:
		var args protocol.IndexArgs
		if err := decode(req.Args, &args); err != nil {
			return nil, err
		}
		return d.removeRule(ctx, args.Index)
	default:
		return nil, fail(protocol.CodeUnknownOp, "unknown op %q", req.Op)
	}
}

func (d *server) status() protocol.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	routed := []string{}
	for _, p := range bypass(d.settings) {
		routed = append(routed, p.String())
	}
	active := ""
	if sub := d.activeSub(); sub != nil {
		active = sub.Name
	}
	return protocol.Status{
		State:              d.state,
		Detail:             d.detail,
		ActiveSubscription: active,
		PrimaryGroup:       d.settings.PrimaryGroup,
		Mode:               d.settings.Mode,
		TUN:                d.settings.TUN,
		TUNRedirect:        d.settings.TUNRedirect,
		Tailscale:          d.settings.Tailscale,
		TailscalePresent:   d.cfg.tailscalePresent(),
		TailnetProxy:       d.settings.TailnetProxy,
		StartedAt:          d.startedAt,
		TUNBypass:          routed,
	}
}

var stateStreams = []string{protocol.StreamStatus, protocol.StreamGroups, protocol.StreamRules, protocol.StreamSubscriptions}

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
	case protocol.StreamSubscriptions:
		return d.subscriptionList()
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
	if err := d.startLocked(ctx); err != nil {
		return nil, err
	}
	if err := d.saveRunning(true); err != nil {
		return nil, err
	}
	return d.status(), nil
}

// startLocked starts the proxy on the active profile. The caller holds
// opMu.
func (d *server) startLocked(ctx context.Context) *protocol.Error {
	d.mu.Lock()
	d.restorePending = false
	d.mu.Unlock()
	if d.running() {
		return nil
	}
	d.setState(protocol.StateStarting, "")
	d.publish(ctx)
	d.mu.Lock()
	cfg := runConfig(d.settings, d.profile)
	d.mu.Unlock()
	err := d.ensureGeodata(ctx, cfg.Profile)
	if err == nil {
		err = d.core.Apply(ctx, cfg)
	}
	if err != nil {
		d.log.Error("start the proxy", "err", err)
		d.setState(protocol.StateStopped, err.Error())
		d.publish(ctx)
		return coreError(err)
	}
	d.log.Info("proxy started")
	d.setState(protocol.StateOn, "")
	d.publish(ctx)
	return nil
}

func (d *server) stop(ctx context.Context) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	d.restorePending = false
	d.mu.Unlock()
	if err := d.core.Stop(ctx); err != nil {
		d.log.Error("stop the proxy", "err", err)
		return nil, coreError(err)
	}
	d.log.Info("proxy stopped")
	d.setState(protocol.StateStopped, "")
	d.publish(ctx)
	if err := d.saveRunning(false); err != nil {
		return nil, err
	}
	return d.status(), nil
}

// saveRunning records whether the proxy should be on after a restart.
func (d *server) saveRunning(on bool) *protocol.Error {
	d.mu.Lock()
	next := d.settings
	d.mu.Unlock()
	if next.Running == on {
		return nil
	}
	next.Running = on
	return d.saveSettings(next)
}

// saveSettings stores next and makes it current. The caller holds opMu.
func (d *server) saveSettings(next state.Settings) *protocol.Error {
	if err := d.store.SaveSettings(next); err != nil {
		d.log.Error("save the settings", "err", err)
		return fail(protocol.CodeInternal, "the settings could not be saved: %v", err)
	}
	d.mu.Lock()
	d.settings = next
	d.mu.Unlock()
	return nil
}

// reapply runs next settings and profile on a running core, or does
// nothing while it is stopped. A core that cannot put the previous config
// back is shown stopped. The caller holds opMu.
func (d *server) reapply(ctx context.Context, next state.Settings, p core.Profile) *protocol.Error {
	if !d.running() {
		return nil
	}
	cfg := runConfig(next, p)
	err := d.ensureGeodata(ctx, cfg.Profile)
	if err == nil {
		err = d.core.Apply(ctx, cfg)
	}
	if err != nil {
		d.log.Error("apply a change", "err", err)
		if errors.Is(err, core.ErrStopped) {
			d.setState(protocol.StateStopped, err.Error())
			d.publish(ctx)
		}
		return coreError(err)
	}
	return nil
}

func (d *server) setSettings(ctx context.Context, args protocol.SettingsArgs) (any, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next, p := d.settings, d.profile
	d.mu.Unlock()

	if args.Mode != nil {
		switch *args.Mode {
		case protocol.ModeRule, protocol.ModeGlobal, protocol.ModeDirect:
			next.Mode = *args.Mode
		default:
			return nil, fail(protocol.CodeInvalid, "mode must be rule, global or direct")
		}
	}
	if args.TUN != nil {
		next.TUN = *args.TUN
	}
	if args.TUNRedirect != nil {
		next.TUNRedirect = *args.TUNRedirect
	}
	if args.Tailscale != nil {
		next.Tailscale = *args.Tailscale
	}
	if args.TailnetProxy != nil {
		next.TailnetProxy = *args.TailnetProxy
	}
	if (args.Tailscale != nil && *args.Tailscale || args.TailnetProxy != nil && *args.TailnetProxy) &&
		!d.cfg.tailscalePresent() {
		return nil, fail(protocol.CodeConflict, "tailscaled is not installed")
	}
	if args.PrimaryGroup != nil {
		name := *args.PrimaryGroup
		if name != "" {
			if err := validName("group", name); err != nil {
				return nil, err
			}
			if d.running() {
				groups, err := d.core.Groups(ctx)
				if err != nil {
					return nil, coreError(err)
				}
				if !slices.ContainsFunc(groups, func(g protocol.Group) bool { return g.Name == name && g.Selectable }) {
					return nil, fail(protocol.CodeNotFound, "no selectable group %q", name)
				}
			}
		}
		next.PrimaryGroup = name
	}

	if err := d.reapply(ctx, next, p); err != nil {
		return nil, err
	}
	if err := d.saveSettings(next); err != nil {
		return nil, err
	}
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

// rules are the runtime's rules, or the owner's own while the proxy is
// stopped.
func (d *server) rules(ctx context.Context) (protocol.RuleList, *protocol.Error) {
	rules, err := d.core.Rules(ctx)
	if errors.Is(err, core.ErrNotRunning) {
		d.mu.Lock()
		defer d.mu.Unlock()
		out := []protocol.Rule{}
		for _, r := range d.settings.Rules {
			out = append(out, protocol.Rule{Type: r.Type, Value: r.Value, Target: r.Target, Source: protocol.SourceUser})
		}
		return protocol.RuleList{Rules: out}, nil
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
