package uplink

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/sagernet/netlink"
	"golang.org/x/sys/unix"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, unavailable("resolved response is too large")
	}
	return b.Buffer.Write(p)
}

func discover(ctx context.Context) ([]Link, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Main-table defaults remain visible while TUN installs its policy table.
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil, unavailable("cannot discover default uplinks")
	}
	slices.SortFunc(routes, func(a, b netlink.Route) int { return cmp.Compare(a.Priority, b.Priority) })
	var links []Link
	indices := map[int]int{}
	for _, route := range routes {
		if route.Dst != nil {
			ones, _ := route.Dst.Mask.Size()
			if ones != 0 {
				continue
			}
		}
		if route.Type != unix.RTN_UNICAST {
			continue
		}
		if _, exists := indices[route.LinkIndex]; exists {
			continue
		}
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			continue
		}
		switch link.Type() {
		case "device", "bridge", "bond", "vlan", "veth", "ppp":
		default:
			continue
		}
		attrs := link.Attrs()
		if attrs.Flags&net.FlagUp == 0 || attrs.Flags&net.FlagLoopback != 0 || attrs.Name == "omihomo" || attrs.Name == "tailscale0" {
			continue
		}
		addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			continue
		}
		entry := Link{Name: attrs.Name}
		for _, address := range addresses {
			if ip, ok := netip.AddrFromSlice(address.IP); ok && address.Flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED) == 0 && (ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast()) {
				ones, _ := address.Mask.Size()
				entry.Addresses = append(entry.Addresses, netip.PrefixFrom(ip.Unmap(), ones))
			}
		}
		if len(entry.Addresses) == 0 {
			continue
		}
		indices[route.LinkIndex] = len(links)
		links = append(links, entry)
		if len(links) == 8 {
			break
		}
	}
	if len(links) == 0 {
		return nil, unavailable("no default uplink is available")
	}
	data, err := commandOutput(ctx, "/usr/bin/busctl", "--system", "--json=short", "get-property",
		"org.freedesktop.resolve1", "/org/freedesktop/resolve1", "org.freedesktop.resolve1.Manager", "DNS")
	if err == nil {
		if resolved, err := resolvedLinks(data, links, indices); err == nil {
			links = resolved
		}
	}
	// NetworkManager may retain DHCP DNS while resolved has only tunnel DNS.
	for i := range links {
		if len(links[i].DNS) != 0 {
			continue
		}
		data, err := commandOutput(ctx, "/usr/bin/nmcli", "--terse", "--escape", "no", "--get-values", "IP4.DNS,IP6.DNS", "device", "show", links[i].Name)
		if err != nil {
			continue
		}
		for field := range strings.FieldsSeq(string(data)) {
			if ip, err := netip.ParseAddr(field); err == nil {
				addServer(links, i, ip)
			}
		}
	}
	links = slices.DeleteFunc(links, func(link Link) bool { return len(link.DNS) == 0 })
	if len(links) == 0 {
		return nil, unavailable("no per-link uplink DNS is available")
	}
	return links, nil
}

func resolvedLinks(data []byte, links []Link, indices map[int]int) ([]Link, error) {
	var result struct {
		Type string              `json:"type"`
		Data [][]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Type != "a(iiay)" {
		return nil, unavailable("invalid resolved DNS response")
	}
	for _, record := range result.Data {
		if len(record) != 3 {
			return nil, unavailable("invalid resolved DNS record")
		}
		var index, family int
		var octets []byte
		if json.Unmarshal(record[0], &index) != nil || json.Unmarshal(record[1], &family) != nil || json.Unmarshal(record[2], &octets) != nil {
			return nil, unavailable("invalid resolved DNS address")
		}
		position, ok := indices[index]
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(octets)
		if !ok || family != unix.AF_INET && family != unix.AF_INET6 || ip.Is4() != (family == unix.AF_INET) {
			continue
		}
		addServer(links, position, ip)
	}

	return links, nil
}

func addServer(links []Link, position int, ip netip.Addr) {
	ip = ip.Unmap()
	if !(ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast()) || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return
	}
	for _, link := range links {
		for _, address := range link.Addresses {
			if address.Addr() == ip.WithZone("") {
				return
			}
		}
	}
	if len(links[position].DNS) >= 8 {
		return
	}
	if ip.IsLinkLocalUnicast() {
		ip = ip.WithZone(links[position].Name)
	}
	server := netip.AddrPortFrom(ip, 53)
	if !slices.Contains(links[position].DNS, server) {
		links[position].DNS = append(links[position].DNS, server)
	}
}

func commandOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	var output boundedOutput
	command.Stdout, command.Stderr, command.WaitDelay = &output, io.Discard, time.Second
	if err := command.Run(); err != nil {
		return nil, unavailable("cannot read uplink DNS")
	}
	return output.Bytes(), nil
}

func unavailable(reason string) error { return fmt.Errorf("%w: %s", ErrUnavailable, reason) }
