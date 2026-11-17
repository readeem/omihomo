package xray

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/socks"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

const loopbackTransport = "omihomo-loopback"

func init() {
	common.Must(internet.RegisterProtocolConfigCreator(loopbackTransport, func() any { return nil }))
	common.Must(internet.RegisterTransportListener(loopbackTransport, listenLoopback))
}

func loopback(tag string, port int, udp bool) (*xcore.InboundHandlerConfig, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid local proxy port")
	}
	sniff, err := sniffing().Build()
	if err != nil {
		return nil, err
	}
	local := address("127.0.0.1").Build()
	return &xcore.InboundHandlerConfig{
		Tag: tag,
		ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
			Listen:           local,
			PortList:         &xnet.PortList{Range: []*xnet.PortRange{xnet.SinglePortRange(xnet.Port(port))}},
			StreamSettings:   &internet.StreamConfig{ProtocolName: loopbackTransport},
			SniffingSettings: sniff,
		}),
		ProxySettings: serial.ToTypedMessage(&socks.ServerConfig{
			AuthType: socks.AuthType_NO_AUTH, UdpEnabled: udp, Address: local,
		}),
	}, nil
}

type loopbackListener struct {
	*net.TCPListener
	mu    sync.Mutex
	conns map[*loopbackConn]struct{}
}

type loopbackConn struct {
	*net.TCPConn
	listener *loopbackListener
}

func listenLoopback(_ context.Context, _ xnet.Address, port xnet.Port, _ *internet.MemoryStreamConfig, handle internet.ConnHandler) (internet.Listener, error) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		return nil, err
	}
	l := &loopbackListener{TCPListener: listener, conns: map[*loopbackConn]struct{}{}}
	go l.accept(handle)
	return l, nil
}

func (l *loopbackListener) accept(handle func(stat.Connection)) {
	for {
		conn, err := l.AcceptTCP()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		// The native mixed SOCKS/HTTP handler reads a byte before it sets
		// its handshake deadline. Bound that initial read as well.
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		tracked := &loopbackConn{TCPConn: conn, listener: l}
		l.mu.Lock()
		if l.conns == nil {
			l.mu.Unlock()
			conn.Close()
			return
		}
		l.conns[tracked] = struct{}{}
		l.mu.Unlock()
		handle(tracked)
	}
}

func (l *loopbackListener) Close() error {
	err := l.TCPListener.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	for conn := range l.conns {
		conn.TCPConn.Close()
	}
	l.conns = nil
	return err
}

func (c *loopbackConn) Close() error {
	err := c.TCPConn.Close()
	c.listener.mu.Lock()
	delete(c.listener.conns, c)
	c.listener.mu.Unlock()
	return err
}
