package mixray

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/readeem/mixray/tunnel/statistic"

	"github.com/readeem/omihomo/internal/core/connlog"
	"github.com/readeem/omihomo/internal/protocol"
)

func (c *Core) observeConnection() func(*statistic.TrackerInfo) func() {
	history := c.capture.Load()
	if history == nil {
		return nil
	}
	return func(info *statistic.TrackerInfo) func() {
		m := info.Metadata
		host := m.RuleHost()
		if host == "" && m.DstIP.IsValid() {
			host = m.DstIP.String()
		}
		name, status, reason := processAttribution(m)
		if name == "" {
			name = "unknown"
		}
		rule := info.Rule
		if info.RulePayload != "" {
			rule += "(" + info.RulePayload + ")"
		}
		outbound := info.Chain.Last()
		finish := history.Start(info.UUID.String(), connlog.Key{
			Process: name, ProcessStatus: status, ProcessReason: reason, Inbound: strings.ToLower(m.Type.String()),
			Destination: net.JoinHostPort(host, strconv.Itoa(int(m.DstPort))),
			Network:     m.NetWork.String(), Outbound: outbound, Rule: rule, Blocked: isBlocked(outbound),
		}, func() (int64, int64) { return info.UploadTotal.Load(), info.DownloadTotal.Load() }, info.MultipleDestinations.Load)
		if finish != nil {
			info.Session.Store(history.Session())
		}
		return finish
	}
}

func isBlocked(outbound string) bool {
	return outbound == "REJECT" || outbound == "REJECT-DROP"
}

func (c *Core) ConnectionHistory(ctx context.Context, args protocol.ConnectionHistoryArgs) (protocol.ConnectionHistory, error) {
	c.mu.Lock()
	history := c.history
	c.mu.Unlock()
	if history != nil {
		return history.Page(ctx, args)
	}
	if args.Session != 0 {
		return protocol.ConnectionHistory{}, connlog.ErrSessionChanged
	}
	return protocol.ConnectionHistory{Rows: []protocol.ConnectionHistoryRow{}}, nil
}

func (c *Core) ClearConnectionLog() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.history != nil {
		c.history.Clear()
	}
}
