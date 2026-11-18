package xray

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

func (o *Outbound) ListenPacket(ctx context.Context, addr *net.UDPAddr) (net.PacketConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dest := xnet.DestinationFromAddr(addr)
	run, cancel, handler := o.session(ctx, dest)
	up, writer := pipe.New(pipe.WithSizeLimit(256*1024), pipe.DiscardOverflow())
	reader, down := pipe.New(pipe.WithSizeLimit(256*1024), pipe.DiscardOverflow())
	p := &packetConn{reader: reader, writer: writer, cancel: cancel, target: dest, local: &net.UDPAddr{}}
	go func() {
		stop := context.AfterFunc(run, func() { p.Close() })
		defer cancel()
		defer stop()
		defer writer.Interrupt()
		defer down.Close()
		handler.GetOutbound().Process(run, &transport.Link{Reader: up, Writer: down}, handler)
	}()
	return p, nil
}

type packetConn struct {
	reader  *pipe.Reader
	writer  *pipe.Writer
	cancel  context.CancelFunc
	target  xnet.Destination
	local   *net.UDPAddr
	mu      sync.Mutex
	pending buf.MultiBuffer
}

func (c *packetConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if len(p) > 65507 {
		return 0, errors.New("UDP payload exceeds 65507 bytes")
	}
	destination := xnet.DestinationFromAddr(addr)
	b := buf.NewWithSize(int32(len(p)))
	b.Write(p)
	b.UDP = &destination
	if err := c.writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *packetConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		b, err := c.reader.ReadMultiBuffer()
		if err != nil {
			return 0, nil, err
		}
		c.pending = b
	}
	b := c.pending[0]
	c.pending[0] = nil
	c.pending = c.pending[1:]
	defer b.Release()
	source := c.target
	if b.UDP != nil {
		source = *b.UDP
	}
	if source.Address.Family().IsDomain() {
		source = c.target
	}
	return copy(p, b.Bytes()), &net.UDPAddr{IP: source.Address.IP(), Port: int(source.Port)}, nil
}

func (c *packetConn) Close() error {
	c.cancel()
	c.reader.Interrupt()
	c.writer.Interrupt()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = buf.ReleaseMulti(c.pending)
	return nil
}

// mihomo's outbound packet adapter supplies deadlines. Overflow drops UDP
// packets and never blocks a caller on an Xray transport.
func (c *packetConn) SetDeadline(time.Time) error      { return nil }
func (c *packetConn) SetReadDeadline(time.Time) error  { return nil }
func (c *packetConn) SetWriteDeadline(time.Time) error { return nil }
func (c *packetConn) LocalAddr() net.Addr              { return c.local }
