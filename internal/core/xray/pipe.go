package xray

import (
	"errors"
	"net"
	"time"

	"github.com/xtls/xray-core/common/buf"
)

type streamPipe struct {
	net.Conn
	writer net.Conn
}

func streamPipePair() (*streamPipe, *streamPipe) {
	uplinkReader, uplinkWriter := net.Pipe()
	downlinkReader, downlinkWriter := net.Pipe()
	return &streamPipe{downlinkReader, uplinkWriter}, &streamPipe{uplinkReader, downlinkWriter}
}

func (p *streamPipe) Write(b []byte) (int, error) { return p.writer.Write(b) }
func (p *streamPipe) CloseWrite() error           { return p.writer.Close() }

func (p *streamPipe) Close() error {
	return errors.Join(p.Conn.Close(), p.CloseWrite())
}

func (p *streamPipe) SetDeadline(t time.Time) error {
	return errors.Join(p.SetReadDeadline(t), p.SetWriteDeadline(t))
}

func (p *streamPipe) SetWriteDeadline(t time.Time) error {
	return p.writer.SetWriteDeadline(t)
}

type relayWriter struct {
	buf.Writer
	conn *streamPipe
}

func (w relayWriter) Close() error { return w.conn.CloseWrite() }
