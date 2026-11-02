// Package uplink reaches the network without the proxy or the system DNS stub.
package uplink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/unix"
)

const Timeout = 5 * time.Second

var ErrUnavailable = errors.New("uplink DNS is unavailable")

// Link is a current default uplink, never persisted or supplied by a client.
type Link struct {
	Name      string
	Addresses []netip.Prefix
	DNS       []netip.AddrPort
}

// Network discovers links for each operation so DHCP and route changes take
// effect without restarting. Discover nil uses systemd-resolved and netlink.
type Network struct {
	Discover func(context.Context) ([]Link, error)
}

func (n Network) links(ctx context.Context) ([]Link, error) {
	if n.Discover != nil {
		return n.Discover(ctx)
	}
	return discover(ctx)
}

// Recoverable only admits connectivity failures. In particular, a certificate,
// TLS protocol, proxy authentication or HTTP error is never recovery permission.
func Recoverable(err error) bool {
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &verify) || errors.As(err, &authority) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &record) || errors.As(err, &alert) {
		return false
	}
	var op *net.OpError
	var lookup *net.DNSError
	if errors.As(err, &lookup) && lookup.IsNotFound {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &op) || errors.As(err, &lookup)
}

func dial(ctx context.Context, link Link, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	var source netip.Addr
	for _, prefix := range link.Addresses {
		if prefix.Addr().Is4() == remote.Is4() && prefix.Addr().IsLinkLocalUnicast() == remote.IsLinkLocalUnicast() {
			source = prefix.Addr()
			break
		}
	}
	if source.IsLinkLocalUnicast() {
		source = source.WithZone(link.Name)
	}
	if !source.IsValid() {
		return nil, errors.New("uplink has no address for this family")
	}
	d := net.Dialer{Timeout: Timeout, Control: func(_, _ string, conn syscall.RawConn) error {
		var bindErr error
		err := conn.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), link.Name) })
		if err != nil {
			return err
		}
		return bindErr
	}}
	// Binding the source as well as the device avoids sing-tun's rule for
	// locally originated IPv4 packets whose source is still unspecified.
	if network == "udp" {
		d.LocalAddr = &net.UDPAddr{IP: net.IP(source.AsSlice()), Zone: source.Zone()}
	} else {
		d.LocalAddr = &net.TCPAddr{IP: net.IP(source.AsSlice()), Zone: source.Zone()}
	}
	return d.DialContext(ctx, network, address)
}

func exchange(ctx context.Context, link Link, server netip.AddrPort, query *dns.Msg) (*dns.Msg, error) {
	for _, network := range []string{"udp", "tcp"} {
		conn, err := dial(ctx, link, network, server.String())
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			conn.SetDeadline(deadline)
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		client := dns.Client{Net: network}
		answer, _, err := client.ExchangeWithConnContext(ctx, query, &dns.Conn{Conn: conn})
		stop()
		conn.Close()
		if err != nil {
			return nil, err
		}
		if !answer.Truncated {
			return answer, nil
		}
	}
	return nil, errors.New("truncated uplink DNS response")
}

func (n Network) ExchangeContext(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	links, err := n.links(ctx)
	if err != nil {
		return nil, err
	}
	return queryLinks(ctx, links, query)
}

func queryLinks(ctx context.Context, links []Link, query *dns.Msg) (*dns.Msg, error) {
	var last error = ErrUnavailable
	for _, link := range links {
		for _, server := range link.DNS {
			attempt, cancel := context.WithTimeout(ctx, time.Second)
			answer, err := exchange(attempt, link, server, query)
			cancel()
			if err == nil {
				return answer, nil
			}
			last = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	return nil, last
}

func lookup(ctx context.Context, links []Link, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	var ips []netip.Addr
	var last error
	for _, typ := range []uint16{dns.TypeA, dns.TypeAAAA} {
		query := new(dns.Msg)
		query.SetQuestion(dns.Fqdn(host), typ)
		reply, err := queryLinks(ctx, links, query)
		if err != nil {
			last = err
			continue
		}
		for _, rr := range reply.Answer {
			switch rr := rr.(type) {
			case *dns.A:
				if ip, ok := netip.AddrFromSlice(rr.A); ok {
					ips = append(ips, ip.Unmap())
				}
			case *dns.AAAA:
				if ip, ok := netip.AddrFromSlice(rr.AAAA); ok {
					ips = append(ips, ip)
				}
			}
		}
	}
	if len(ips) > 0 {
		return ips, nil
	}
	if last != nil {
		return nil, last
	}
	return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
}

func (n Network) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	links, err := n.links(ctx)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := lookup(ctx, links, host)
	if err != nil {
		return nil, err
	}
	var last error = ErrUnavailable
	for _, ip := range ips {
		for _, link := range links {
			conn, err := dial(ctx, link, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
	}
	return nil, last
}

// DialDNS is used by the embedded engine's bootstrap resolver. TCP keeps DNS
// framing in the engine while discovery and bound sockets stay at this seam.
func (n Network) DialDNS(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	links, err := n.links(ctx)
	if err != nil {
		return nil, err
	}
	var last error = ErrUnavailable
	for _, link := range links {
		for _, server := range link.DNS {
			attempt, cancel := context.WithTimeout(ctx, time.Second)
			conn, err := dial(attempt, link, "tcp", server.String())
			cancel()
			if err == nil {
				return conn, nil
			}
			last = err
		}
	}
	return nil, last
}
