package hosting

import (
	"net"
	"sync"
	"time"
)

type trackedConn struct {
	net.Conn
	m       *Manager
	s       *session
	udp     bool
	idleFor time.Duration
	timer   *time.Timer
	once    sync.Once
}

func (c *trackedConn) touch() {
	if c.timer != nil {
		c.timer.Reset(c.idleFor)
	}
}

func (c *trackedConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if n > MaxDatagram && c.udp {
			c.m.countOversized()
			continue
		}
		if n > 0 || c.udp && err == nil {
			c.touch()
		}
		return n, err
	}
}

func (c *trackedConn) Write(p []byte) (int, error) {
	if c.udp && len(p) > MaxDatagram {
		c.m.countOversized()
		return 0, ErrOversized
	}
	n, err := c.Conn.Write(p)
	if n > 0 || c.udp && err == nil {
		c.touch()
	}
	return n, err
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		if c.timer != nil {
			c.timer.Stop()
		}
		err = c.Conn.Close()
		c.m.mu.Lock()
		c.m.releaseLocked(c.s)
		c.m.unlock()
	})
	return err
}

func (m *Manager) countOversized() {
	m.mu.Lock()
	satInc(&m.oversizedN)
	m.dirty = true
	m.unlock()
}
