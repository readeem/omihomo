package xray

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	proxyman "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
	xhttp "github.com/xtls/xray-core/proxy/http"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/uplink"
)

func (c *Core) DialDownload(ctx context.Context, network, address string) (net.Conn, error) {
	c.mu.Lock()
	r, selections := c.current, c.selections
	c.mu.Unlock()
	if r == nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: core.ErrNotRunning}
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("downloads require TCP")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return nil, errors.New("invalid download port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		ip, err = r.lookupDownload(ctx, host, r.plan.resolve(r.plan.dnsTarget, selections))
		if err != nil {
			return nil, err
		}
	}
	if err := uplink.CheckDownloadAddress(ip); err != nil {
		return nil, err
	}
	return r.openDownload(ctx, tcpDestination(ip.Unmap().String(), uint16(n)), r.plan.resolve(globalName, selections))
}

var (
	errDownloadDNS = errors.New("Xray could not resolve the download")
	errDialDNSExit = errors.New("Xray could not connect to the download resolver")
)

func (r *runtime) lookupDownload(ctx context.Context, host, tag string) (netip.Addr, error) {
	// Xray's native DNS dispatcher loses exit dial errors before returning them.
	// Use the same DoH endpoints and DNS exit with a synchronous dial callback.
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		DialContext: func(dialCtx context.Context, _, address string) (net.Conn, error) {
			endpoint, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, errDownloadDNS
			}
			attempt, cancel := context.WithTimeout(dialCtx, 2250*time.Millisecond)
			defer cancel()
			conn, err := r.openDownload(attempt, tcpDestination(endpoint.Addr().String(), endpoint.Port()), tag)
			if uplink.Recoverable(err) {
				return nil, errDialDNSExit
			}
			return conn, err
		},
	}
	defer transport.CloseIdleConnections()

	for _, endpoint := range dnsEndpoints {
		ip, err := resolveDownload(ctx, transport, endpoint, host)
		if !errors.Is(err, errDialDNSExit) {
			return ip, err
		}
	}
	return netip.Addr{}, &net.OpError{Op: "dial", Net: "tcp", Err: errDialDNSExit}
}

func resolveDownload(ctx context.Context, transport *http.Transport, endpoint, host string) (netip.Addr, error) {
	const mediaType = "application/dns-message"
	var first netip.Addr
	for _, family := range []uint16{dns.TypeA, dns.TypeAAAA} {
		query := new(dns.Msg).SetQuestion(dns.Fqdn(host), family)
		data, err := query.Pack()
		if err != nil {
			return netip.Addr{}, errDownloadDNS
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return netip.Addr{}, errDownloadDNS
		}
		req.Header.Set("Content-Type", mediaType)
		req.Header.Set("Accept", mediaType)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			// TLS and HTTP also return network errors. Only the dial
			// callback can authorize recovery.
			if errors.Is(err, errDialDNSExit) {
				return netip.Addr{}, errDialDNSExit
			}
			return netip.Addr{}, errDownloadDNS
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, dns.MaxMsgSize+1))
		resp.Body.Close()
		var reply dns.Msg
		if err != nil || resp.StatusCode != http.StatusOK || len(data) > dns.MaxMsgSize ||
			reply.Unpack(data) != nil || !reply.Response || reply.Id != query.Id ||
			reply.Truncated || reply.Rcode != dns.RcodeSuccess || len(reply.Question) != 1 ||
			reply.Question[0] != query.Question[0] {
			return netip.Addr{}, errDownloadDNS
		}
		for _, answer := range reply.Answer {
			var raw net.IP
			switch answer := answer.(type) {
			case *dns.A:
				raw = answer.A
			case *dns.AAAA:
				raw = answer.AAAA
			default:
				continue
			}
			ip, ok := netip.AddrFromSlice(raw)
			if !ok || uplink.CheckDownloadAddress(ip) != nil {
				return netip.Addr{}, uplink.ErrDownloadAddress
			}
			if !first.IsValid() {
				first = ip
			}
		}
	}
	if !first.IsValid() {
		return netip.Addr{}, errDownloadDNS
	}
	return first, nil
}

var errDownloadExit = errors.New("Xray could not connect using the primary exit")

func (r *runtime) openDownload(ctx context.Context, dest xnet.Destination, tag string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tag == rejectTag {
		return nil, errors.New("Xray's primary exit rejects downloads")
	}
	manager := r.instance.GetFeature(outbound.ManagerType()).(outbound.Manager)
	h, ok := manager.GetHandler(tag).(*proxyman.Handler)
	if !ok {
		return nil, errDownloadExit
	}
	proxy := h.GetOutbound()
	_, isHTTP := proxy.(*xhttp.Client)
	deadline, _ := ctx.Deadline()
	dial := &downloadDialer{Dialer: h, ready: make(chan struct{}), waitForHTTP: isHTTP, deadline: deadline}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	runCtx = context.WithValue(runCtx, downloadDialKey{}, dial)
	stop := context.AfterFunc(r.ctx, cancel)
	client, relay := net.Pipe()
	disarm := context.AfterFunc(runCtx, func() { relay.Close() })
	runCtx = session.ContextWithOutbounds(runCtx, []*session.Outbound{{Tag: tag, Target: dest, OriginalTarget: dest}})
	runCtx = session.ContextWithInbound(runCtx, &session.Inbound{CanSpliceCopy: 3})
	reader := &buf.TimeoutWrapperReader{Reader: buf.NewReader(relay)}
	var input buf.Reader = reader
	if isHTTP {
		input = &downloadHTTPReader{TimeoutWrapperReader: reader, onConnected: dial.connected}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		defer cancel()
		defer disarm()
		defer relay.Close()
		proxy.Process(runCtx, &transport.Link{Reader: input, Writer: buf.NewWriter(relay)}, dial)
	}()
	select {
	case <-dial.ready:
		return client, nil
	case <-done:
	case <-r.ctx.Done():
	case <-ctx.Done():
	}
	client.Close()
	cancel()
	if dial.connectivityFailure() {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errDownloadExit}
	}
	return nil, errDownloadExit
}

type downloadDialer struct {
	internet.Dialer
	ready       chan struct{}
	once        sync.Once
	waitForHTTP bool
	deadline    time.Time
	opened      atomic.Bool
	networkErr  atomic.Bool
}

func (d *downloadDialer) connected() {
	d.once.Do(func() { close(d.ready) })
}

func (d *downloadDialer) Dial(ctx context.Context, dest xnet.Destination) (stat.Connection, error) {
	conn, err := d.Dialer.Dial(ctx, dest)
	if err == nil && !d.waitForHTTP {
		d.connected()
	}
	return conn, err
}

func (d *downloadDialer) connectivityFailure() bool {
	return d.networkErr.Load() && !d.opened.Load()
}

type downloadDialKey struct{}

type downloadSystemDialer struct{ internet.DefaultSystemDialer }

func init() {
	// Only this adapter uses Xray. Install once before any instance runs
	// and delegate ordinary traffic to the unmodified upstream dialer.
	internet.UseAlternativeSystemDialer(&downloadSystemDialer{})
}

func (d *downloadSystemDialer) Dial(ctx context.Context, src xnet.Address, dest xnet.Destination, opts *internet.SocketConfig) (net.Conn, error) {
	download, ok := ctx.Value(downloadDialKey{}).(*downloadDialer)
	if !ok || dest.Network != xnet.Network_TCP {
		return d.DefaultSystemDialer.Dial(ctx, src, dest, opts)
	}
	// Keep raw connect inside the download budget. Record its result at
	// this boundary because native transports also handle TLS and auth.
	timeout := 2 * time.Second
	if !download.deadline.IsZero() {
		timeout = min(timeout, time.Until(download.deadline)-100*time.Millisecond)
		if timeout <= 0 {
			return nil, errDownloadExit
		}
	}
	attempt, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := d.DefaultSystemDialer.Dial(attempt, src, dest, opts)
	if err == nil {
		download.opened.Store(true)
	} else if op, ok := errors.AsType[*net.OpError](err); ok && op.Op == "dial" && uplink.Recoverable(err) {
		download.networkErr.Store(true)
	}
	return conn, err
}

type downloadHTTPReader struct {
	*buf.TimeoutWrapperReader
	onConnected func()
}

func (r *downloadHTTPReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	// HTTP may reuse an existing transport without calling Dial. Its
	// regular reader is only used after the proxy accepts CONNECT.
	r.onConnected()
	return r.TimeoutWrapperReader.ReadMultiBuffer()
}
