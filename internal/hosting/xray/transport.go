// Package xray implements native VLESS reverse using the pinned Xray codecs.
package xray

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/readeem/omihomo/internal/hosting"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/mux"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vless/encoding"
	"github.com/xtls/xray-core/transport/internet/reality"
)

var errUnavailable = errors.New("hosting relay is unavailable")

// Transport uses an injected direct dialer for endpoint DNS and every connection.
type Transport struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

// New binds relay connections to the installation's direct networking adapter.
func New(dial func(context.Context, string, string) (net.Conn, error)) *Transport {
	return &Transport{dial: dial}
}

// Run owns one reverse connection until cancellation or transport failure.
func (t *Transport) Run(ctx context.Context, relay hosting.Relay, credential string, receiver hosting.Receiver) error {
	attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := t.dial(attempt, "tcp", net.JoinHostPort(relay.Host, strconv.Itoa(relay.Port)))
	if err != nil {
		return errUnavailable
	}
	defer raw.Close()
	stop := context.AfterFunc(attempt, func() { raw.Close() })
	defer stop()
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	var public []byte
	for _, codec := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, e := codec.DecodeString(relay.Reality.PublicKey); e == nil && len(decoded) == 32 {
			public = decoded
			break
		}
	}
	if len(public) != 32 {
		return hosting.ErrAuth
	}
	short, err := hex.DecodeString(relay.Reality.ShortID)
	if err != nil {
		return hosting.ErrAuth
	}
	short = append(short, make([]byte, 8-len(short))...)
	guarded := &handshakeConn{Conn: raw, remaining: 256 << 10}
	secured, err := reality.UClient(guarded, &reality.Config{ServerName: relay.Reality.ServerName, PublicKey: public, ShortId: short, Fingerprint: "chrome", SpiderY: make([]int64, 10)}, attempt, xnet.TCPDestination(xnet.ParseAddress(relay.Host), xnet.Port(relay.Port)))
	if err != nil {
		if attempt.Err() != nil {
			return errUnavailable
		}
		_, unknownCA := errors.AsType[x509.UnknownAuthorityError](err)
		_, hostname := errors.AsType[x509.HostnameError](err)
		_, invalidCert := errors.AsType[x509.CertificateInvalidError](err)
		if unknownCA || hostname || invalidCert || err.Error() == "REALITY: processed invalid connection" {
			return hosting.ErrAuth
		}
		return errUnavailable
	}
	guarded.verified = true
	id, err := uuid.ParseString(credential)
	if err != nil {
		return hosting.ErrAuth
	}
	request := &protocol.RequestHeader{Version: 0, User: &protocol.MemoryUser{Account: &vless.MemoryAccount{ID: protocol.NewID(id)}}, Command: protocol.RequestCommandRvs}
	if err = encoding.EncodeRequestHeader(secured, request, &encoding.Addons{}); err != nil {
		return errUnavailable
	}
	if _, err = encoding.DecodeResponseHeader(secured, request); err != nil {
		return errUnavailable
	}
	stop()
	cancel()
	secured.SetDeadline(time.Time{})
	stopTunnel := context.AfterFunc(ctx, func() { secured.Close() })
	defer stopTunnel()
	receiver.Connected()
	tunnel := &connection{conn: secured, receiver: receiver, sessions: make(map[uint16]*session)}
	tunnel.output = &lockedWriter{conn: secured}
	defer tunnel.close()
	return tunnel.serve(ctx)
}

type handshakeConn struct {
	net.Conn
	remaining int
	verified  bool
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	if c.verified {
		return c.Conn.Read(p)
	}
	if c.remaining <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= n
	return n, err
}

type lockedWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (w *lockedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	defer buf.ReleaseMulti(mb)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for _, b := range mb {
		p := b.Bytes()
		for len(p) > 0 {
			n, err := w.conn.Write(p)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrNoProgress
			}
			p = p[n:]
		}
	}
	return nil
}

type connection struct {
	conn       net.Conn
	output     *lockedWriter
	receiver   hosting.Receiver
	mu         sync.Mutex
	sessions   map[uint16]*session
	workers    sync.WaitGroup
	control    uint16
	hasControl bool
}

func (c *connection) end(id uint16) error {
	frame := buf.New()
	if err := (mux.FrameMetadata{SessionID: id, SessionStatus: mux.SessionStatusEnd}).WriteTo(frame); err != nil {
		frame.Release()
		return err
	}
	return c.output.WriteMultiBuffer(buf.MultiBuffer{frame})
}

func (c *connection) serve(ctx context.Context) error {
	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		var meta mux.FrameMetadata
		if err := meta.Unmarshal(c.conn, true); err != nil {
			return errUnavailable
		}
		var payload []byte
		if meta.Option.Has(mux.OptionData) {
			var size [2]byte
			if _, err := io.ReadFull(c.conn, size[:]); err != nil {
				return errUnavailable
			}
			n := int(size[0])<<8 | int(size[1])
			if n > buf.Size {
				c.receiver.Drop(true)
				if _, err := io.CopyN(io.Discard, c.conn, int64(n)); err != nil {
					return errUnavailable
				}
				continue
			}
			payload = make([]byte, n)
			if _, err := io.ReadFull(c.conn, payload); err != nil {
				return errUnavailable
			}
		}
		c.mu.Lock()
		current := c.sessions[meta.SessionID]
		c.mu.Unlock()
		switch meta.SessionStatus {
		case mux.SessionStatusNew:
			if current != nil {
				return errUnavailable
			}
			if meta.Target.Network == xnet.Network_UDP && meta.Target.Port == 0 && meta.Target.Address.Family().IsDomain() && meta.Target.Address.Domain() == "reverse" {
				if c.hasControl && c.control != meta.SessionID {
					if err := c.end(meta.SessionID); err != nil {
						return errUnavailable
					}
					continue
				}
				c.control, c.hasControl = meta.SessionID, true
				continue
			}
			if !meta.Target.Address.Family().IsIP() || (meta.Target.Network != xnet.Network_TCP && meta.Target.Network != xnet.Network_UDP) {
				if err := c.end(meta.SessionID); err != nil {
					return errUnavailable
				}
				continue
			}
			destination := hosting.Destination{Address: meta.Target.Address.String(), Port: int(meta.Target.Port), Network: meta.Target.Network.SystemString()}
			local, err := c.receiver.Open(ctx, destination)
			if err != nil {
				if err = c.end(meta.SessionID); err != nil {
					return errUnavailable
				}
				continue
			}
			packet := meta.Target.Network == xnet.Network_UDP
			transfer := protocol.TransferTypeStream
			if packet {
				transfer = protocol.TransferTypePacket
			}
			current = &session{owner: c, id: meta.SessionID, local: local, packet: packet, destination: meta.Target, queue: make(chan []byte, 64), done: make(chan struct{}), reply: mux.NewResponseWriter(meta.SessionID, c.output, transfer)}
			c.mu.Lock()
			c.sessions[meta.SessionID] = current
			c.mu.Unlock()
			c.workers.Add(2)
			go current.read()
			go current.write()
		case mux.SessionStatusKeep:
			if c.hasControl && c.control == meta.SessionID {
				continue
			}
			if current == nil {
				if err := c.end(meta.SessionID); err != nil {
					return errUnavailable
				}
				continue
			}
			if meta.Target.Network != xnet.Network_Unknown && meta.Target.String() != current.destination.String() {
				current.close()
				continue
			}
		case mux.SessionStatusEnd:
			if c.hasControl && c.control == meta.SessionID {
				c.hasControl = false
			}
			if current != nil {
				current.close()
			}
			continue
		case mux.SessionStatusKeepAlive:
			if len(payload) > 0 {
				c.receiver.Drop(false)
			}
			continue
		default:
			return errUnavailable
		}
		if current == nil || !meta.Option.Has(mux.OptionData) {
			continue
		}
		if current.packet {
			if len(payload) > hosting.MaxDatagram {
				c.receiver.Drop(true)
				continue
			}
			if len(payload) == 0 {
				c.receiver.Drop(false)
				continue
			}
			select {
			case <-current.done:
			case current.queue <- payload:
			default:
				c.receiver.Drop(false)
			}
		} else {
			select {
			case <-current.done:
			case current.queue <- payload:
			default:
				current.close()
			}
		}
	}
}

func (c *connection) close() {
	c.conn.Close()
	c.mu.Lock()
	sessions := make([]*session, 0, len(c.sessions))
	for _, s := range c.sessions {
		sessions = append(sessions, s)
	}
	c.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
	c.workers.Wait()
}

type session struct {
	owner       *connection
	id          uint16
	local       net.Conn
	packet      bool
	destination xnet.Destination
	queue       chan []byte
	done        chan struct{}
	once        sync.Once
	reply       *mux.Writer
}

func (s *session) close() {
	s.once.Do(func() {
		s.local.Close()
		close(s.done)
		s.owner.mu.Lock()
		if s.owner.sessions[s.id] == s {
			delete(s.owner.sessions, s.id)
		}
		s.owner.mu.Unlock()
	})
}

func (s *session) read() {
	defer s.owner.workers.Done()
	defer s.close()
	defer s.reply.Close()
	size := hosting.MaxDatagram
	if s.packet {
		size++
	}
	data := make([]byte, size)
	for {
		n, err := s.local.Read(data)
		if n > 0 || s.packet && err == nil {
			if s.packet && n == 0 {
				s.owner.receiver.Drop(false)
				continue
			}
			if n > hosting.MaxDatagram {
				s.owner.receiver.Drop(true)
				continue
			}
			b := buf.New()
			b.Write(data[:n])
			if err := s.reply.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) write() {
	defer s.owner.workers.Done()
	defer s.close()
	for {
		select {
		case <-s.done:
			return
		case p := <-s.queue:
			if s.packet {
				if _, err := s.local.Write(p); err != nil {
					return
				}
				continue
			}
			for len(p) > 0 {
				n, err := s.local.Write(p)
				if err != nil || n == 0 {
					return
				}
				p = p[n:]
			}
		}
	}
}
