package xray

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/readeem/omihomo/internal/core"
)

const (
	tunDevice   = "omihomo"
	tunTable    = 20224
	tunProtocol = 222
	tunMark     = 0x400000
)

var tunGateways = []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"}

type tunRoutes struct {
	routes []netlink.Route
	rules  []*netlink.Rule
}

func setupTUN(cfg core.Config) (*tunRoutes, error) {
	link, err := netlink.LinkByName(tunDevice)
	if err != nil {
		return nil, fmt.Errorf("find Xray's TUN: %w", err)
	}

	t := &tunRoutes{}
	if err := t.install(link.Attrs().Index, cfg); err != nil {
		return nil, errors.Join(err, t.close())
	}
	return t, nil
}

func (t *tunRoutes) addRoute(r netlink.Route) error {
	if err := netlink.RouteAdd(&r); err != nil {
		return fmt.Errorf("add Xray TUN route %v: %w", r.Dst, err)
	}
	t.routes = append(t.routes, r)
	return nil
}

func (t *tunRoutes) install(index int, cfg core.Config) error {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		prefix := netip.PrefixFrom(netip.IPv4Unspecified(), 0)
		if family == netlink.FAMILY_V6 {
			prefix = netip.PrefixFrom(netip.IPv6Unspecified(), 0)
		}
		if err := t.addRoute(netlink.Route{Table: tunTable, Protocol: tunProtocol, LinkIndex: index, Dst: ipNet(prefix)}); err != nil {
			return err
		}
	}
	for _, bypass := range cfg.TUNBypass {
		if err := t.addRoute(netlink.Route{Table: tunTable, Protocol: tunProtocol, Type: unix.RTN_THROW, Dst: ipNet(bypass)}); err != nil {
			return err
		}
	}

	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		// Leaving Xray's sockets marked avoids its global dial controller,
		// which can accumulate when an embedded instance is restarted.
		mask := uint32(tunMark)
		rule := netlink.NewRule()
		rule.Family, rule.Table, rule.Mark, rule.Mask, rule.Invert = family, tunTable, tunMark, &mask, true
		rule.Protocol = tunProtocol
		rule.Priority = 9000
		if cfg.TailnetProxy {
			// Run before Tailscale's unmarked lookup, after its bypass rules.
			rule.Priority = 5260
		}
		if err := netlink.RuleAdd(rule); err != nil {
			return fmt.Errorf("add Xray TUN policy: %w", err)
		}
		t.rules = append(t.rules, rule)
	}
	return nil
}

func ipNet(prefix netip.Prefix) *net.IPNet {
	return &net.IPNet{IP: prefix.Masked().Addr().AsSlice(), Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())}
}

func (t *tunRoutes) close() error {
	if t == nil {
		return nil
	}
	var out error
	for _, rule := range t.rules {
		out = errors.Join(out, netlink.RuleDel(rule))
	}
	for _, route := range t.routes {
		out = errors.Join(out, netlink.RouteDel(&route))
	}
	return out
}

func prepareTUN() error {
	if _, err := netlink.LinkByName(tunDevice); err == nil {
		return errors.New("the Xray TUN interface is already in use")
	} else if _, missing := errors.AsType[netlink.LinkNotFoundError](err); !missing {
		return fmt.Errorf("check Xray's TUN interface: %w", err)
	}
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := netlink.RuleList(family)
		if err != nil {
			return err
		}
		for _, r := range rules {
			if r.Table != tunTable {
				continue
			}
			if r.Protocol != tunProtocol || r.Mark != tunMark || r.Mask == nil || *r.Mask != tunMark || !r.Invert {
				return fmt.Errorf("routing table %d is in use", tunTable)
			}
			if err := netlink.RuleDel(&r); err != nil {
				return fmt.Errorf("clean up Xray's old TUN rule: %w", err)
			}
		}
		routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: tunTable}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return err
		}
		for _, r := range routes {
			if r.Protocol != tunProtocol || r.Type != unix.RTN_THROW {
				return fmt.Errorf("routing table %d is in use", tunTable)
			}
			if err := netlink.RouteDel(&r); err != nil {
				return fmt.Errorf("clean up Xray's old TUN route: %w", err)
			}
		}
	}
	return nil
}
