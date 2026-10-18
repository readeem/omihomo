// Package client talks to the omihomo daemon over its socket.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/readeem/omihomo/internal/protocol"
)

// DefaultSocket is where the daemon listens.
const DefaultSocket = "/run/omihomo/omihomo.sock"

// SocketPath is DefaultSocket unless OMIHOMO_SOCKET names another.
func SocketPath() string {
	if p := os.Getenv("OMIHOMO_SOCKET"); p != "" {
		return p
	}
	return DefaultSocket
}

// VersionError means the daemon speaks another protocol version.
type VersionError struct{ Daemon int }

func (e *VersionError) Error() string {
	return fmt.Sprintf("the daemon speaks protocol %d and this client %d; update the core", e.Daemon, protocol.Version)
}

// ErrClosed is returned by calls on a client whose connection has ended.
var ErrClosed = errors.New("the connection to the daemon closed")

// Client is one connection to the daemon. It is safe for concurrent use.
type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
	// Hello is the daemon's greeting.
	Hello protocol.Hello

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan protocol.Envelope
	writeMu sync.Mutex

	events    chan protocol.Envelope
	done      chan struct{}
	closeOnce sync.Once
}

// Dial connects to the daemon at path and reads its greeting. A refusal is
// returned as a *protocol.Error, a version mismatch as a *VersionError.
func Dial(ctx context.Context, path string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", path, err)
	}
	c := &Client{
		conn:    conn,
		scanner: protocol.NewScanner(conn),
		pending: map[uint64]chan protocol.Envelope{},
		events:  make(chan protocol.Envelope, 256),
		done:    make(chan struct{}),
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.SetReadDeadline(time.Now()) })
	env, err := c.read()
	if !stop() {
		conn.Close()
		return nil, fmt.Errorf("read the daemon's greeting: %w", ctx.Err())
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read the daemon's greeting: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	switch env.Type {
	case protocol.TypeRefused:
		conn.Close()
		if env.Error == nil {
			return nil, &protocol.Error{Code: protocol.CodeForbidden, Message: "the daemon refused this client"}
		}
		return nil, env.Error
	case protocol.TypeHello:
	default:
		conn.Close()
		return nil, fmt.Errorf("unexpected greeting %q", env.Type)
	}
	c.Hello = protocol.Hello{Type: env.Type, Protocol: env.Protocol, Version: env.Version}
	if env.Protocol != protocol.Version {
		conn.Close()
		return nil, &VersionError{Daemon: env.Protocol}
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) read() (protocol.Envelope, error) {
	var env protocol.Envelope
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return env, err
		}
		return env, ErrClosed
	}
	if err := json.Unmarshal(c.scanner.Bytes(), &env); err != nil {
		return env, fmt.Errorf("malformed message from the daemon: %w", err)
	}
	return env, nil
}

func (c *Client) readLoop() {
	defer c.Close()
	defer close(c.events)
	for {
		env, err := c.read()
		if err != nil {
			return
		}
		switch env.Type {
		case protocol.TypeReply:
			c.mu.Lock()
			ch := c.pending[env.ID]
			delete(c.pending, env.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- env
			}
		case protocol.TypeEvent:
			select {
			case c.events <- env:
			default:
				// State streams push only changes, so a dropped event
				// would leave the reader stale for good. A reader that
				// falls this far behind is disconnected instead.
				return
			}
		}
	}
}

// Events delivers pushes for subscribed streams. It closes with the
// connection, which ends if the reader lets 256 events queue up.
func (c *Client) Events() <-chan protocol.Envelope { return c.events }

// Close ends the connection.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
	return nil
}

// Call runs op with args and decodes its result into result, which may be
// nil. A failure the daemon reports is a *protocol.Error.
func (c *Client) Call(ctx context.Context, op string, args, result any) error {
	req := protocol.Request{Op: op}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return fmt.Errorf("encode %s args: %w", op, err)
		}
		req.Args = raw
	}
	ch := make(chan protocol.Envelope, 1)
	c.mu.Lock()
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()

	line, err := protocol.Line(req)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", op, err)
	}
	// A call cancelled before it is sent must not run.
	c.writeMu.Lock()
	if err := ctx.Err(); err != nil {
		c.writeMu.Unlock()
		return fmt.Errorf("%s: %w", op, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.conn.SetWriteDeadline(deadline)
	_, err = c.conn.Write(line)
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("send %s request: %w", op, err)
	}

	var env protocol.Envelope
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", op, ctx.Err())
	case <-c.done:
		// The reply may have landed just before the connection closed.
		select {
		case env = <-ch:
		default:
			return fmt.Errorf("%s: %w", op, ErrClosed)
		}
	case env = <-ch:
	}
	if env.Error != nil {
		return env.Error
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", op, err)
	}
	return nil
}
