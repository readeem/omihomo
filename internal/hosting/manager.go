package hosting

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	dialTimeout = 10 * time.Second
	udpIdle     = 60 * time.Second
	backoffMin  = time.Second
	backoffMax  = 30 * time.Second
)

// Manager owns the desired hosting state, the single tunnel run and its sessions.
type Manager struct {
	transport Transport
	dial      func(context.Context, string, string) (net.Conn, error)
	changed   func()
	base      context.Context
	baseStop  context.CancelFunc

	dialTimeout, udpIdle, backoffMin, backoffMax time.Duration

	mu         sync.Mutex
	dirty      bool
	cfg        Config
	cred       string
	running    bool
	stopped    bool
	closed     bool
	latched    bool
	tun        *tunnel
	sessions   map[*session]struct{}
	active     map[string]int
	tcp, udp   int
	state      string
	reason     string
	changedAt  int64
	refused    uint64
	dropped    uint64
	oversizedN uint64
}

type tunnel struct {
	m         *Manager
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
	relay     Relay
	cred      string
	revoked   bool
	connected bool
}

type revocations struct {
	conns []net.Conn
	ready []<-chan struct{}
}

type session struct {
	t          *tunnel
	share      Share
	network    string
	cancelDial context.CancelFunc
	conn       net.Conn
	ready      chan struct{}
	revoked    bool
	released   bool
}

// New returns an idle Manager. changed is called, outside any lock, after observable
// state changes and must not block.
func New(transport Transport, changed func()) *Manager {
	base, stop := context.WithCancel(context.Background())
	m := &Manager{
		transport: transport, changed: changed, base: base, baseStop: stop,
		dialTimeout: dialTimeout, udpIdle: udpIdle, backoffMin: backoffMin, backoffMax: backoffMax,
		sessions: map[*session]struct{}{}, active: map[string]int{},
		state: StateIdle, changedAt: time.Now().UnixMilli(),
	}
	m.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: m.dialTimeout}
		return dialer.DialContext(ctx, network, target)
	}
	return m
}

func (m *Manager) unlock() {
	d := m.dirty
	m.dirty = false
	m.mu.Unlock()
	if d && m.changed != nil {
		m.changed()
	}
}

func (m *Manager) setStateLocked(state, reason string) {
	if m.state == state && m.reason == reason {
		return
	}
	if m.state != state {
		m.changedAt = time.Now().UnixMilli()
	}
	m.state, m.reason = state, reason
	m.dirty = true
}

func (m *Manager) hasEnabledLocked() bool {
	for _, s := range m.cfg.Shares {
		if s.Enabled {
			return true
		}
	}
	return false
}

func (m *Manager) desiredLocked() bool {
	return !m.closed && !m.stopped && m.running && !m.cfg.Paused &&
		m.cfg.Relay != nil && m.cred != "" && m.hasEnabledLocked()
}

func (m *Manager) idleReasonLocked() string {
	switch {
	case m.cfg.Relay == nil:
		return ""
	case m.cred == "":
		return "unprovisioned"
	case m.cfg.Paused:
		return "paused"
	case !m.running:
		return "proxy off"
	case !m.hasEnabledLocked():
		return "no shares enabled"
	}
	return "stopped"
}

func (m *Manager) settleLocked() {
	switch {
	case !m.desiredLocked():
		m.setStateLocked(StateIdle, m.idleReasonLocked())
	case m.latched:
		m.setStateLocked(StateError, "authentication refused")
	case m.tun == nil || m.tun.revoked:
		m.setStateLocked(StateConnecting, "")
	}
}

func (m *Manager) startLocked() {
	if m.tun != nil || !m.desiredLocked() || m.latched {
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	t := &tunnel{m: m, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		relay: *m.cfg.Relay, cred: m.cred}
	m.tun = t
	m.setStateLocked(StateConnecting, "")
	go m.runLoop(t)
}

func (m *Manager) revokeLocked(t *tunnel) revocations {
	if t == nil {
		return revocations{}
	}
	t.revoked = true
	t.cancel()
	return m.revokeSessionsLocked(func(s *session) bool { return s.t == t })
}

func (m *Manager) revokeSessionsLocked(pick func(*session) bool) revocations {
	var revoked revocations
	for s := range m.sessions {
		if !pick(s) {
			continue
		}
		s.revoked = true
		if s.cancelDial != nil {
			s.cancelDial()
		}
		if s.conn != nil {
			revoked.conns = append(revoked.conns, s.conn)
		}
		revoked.ready = append(revoked.ready, s.ready)
		m.releaseLocked(s)
	}
	return revoked
}

func (m *Manager) releaseLocked(s *session) {
	if s.released {
		return
	}
	s.released = true
	delete(m.sessions, s)
	if s.network == ProtoTCP {
		m.tcp--
	} else {
		m.udp--
	}
	if m.active[s.share.ID]--; m.active[s.share.ID] <= 0 {
		delete(m.active, s.share.ID)
	}
	m.dirty = true
}

func closeAll(ctx context.Context, revoked revocations) error {
	for _, c := range revoked.conns {
		_ = c.Close()
	}
	for _, ready := range revoked.ready {
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) allowedLocked(s *session) bool {
	for _, sh := range m.cfg.Shares {
		if sh.ID == s.share.ID {
			return sh.Enabled && sh.ExposedPort == s.share.ExposedPort && sh.LocalAddress == s.share.LocalAddress &&
				sh.LocalPort == s.share.LocalPort && sh.covers(s.network)
		}
	}
	return false
}

// Apply validates and installs the desired state. Revoked access is closed before it returns.
// ctx bounds waits for revoked admissions and a superseded tunnel run to finish.
func (m *Manager) Apply(ctx context.Context, cfg Config, credential string, running, retry bool) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	if credential != "" {
		if c, err := ValidateCredential([]byte(credential)); err != nil || c != credential {
			return invalid("credential")
		}
	}
	cfg = cfg.clone()

	m.mu.Lock()
	if m.closed {
		m.unlock()
		return ErrClosed
	}
	changedID := (m.cfg.Relay == nil) != (cfg.Relay == nil) || (cfg.Relay != nil && *cfg.Relay != *m.cfg.Relay) || m.cred != credential
	m.cfg, m.cred, m.running, m.stopped = cfg, credential, running, false
	m.dirty = true
	if changedID || retry {
		m.latched = false
	}
	old := m.tun
	var conns revocations
	switch {
	case old != nil && (changedID || !m.desiredLocked()):
		conns = m.revokeLocked(old)
	case old != nil:
		conns = m.revokeSessionsLocked(func(s *session) bool { return !m.allowedLocked(s) })
		if retry {
			select {
			case old.wake <- struct{}{}:
			default:
			}
		}
	}
	m.settleLocked()
	m.startLocked()
	revoked := old != nil && old.revoked
	m.unlock()

	if err := closeAll(ctx, conns); err != nil {
		return err
	}
	if revoked {
		return wait(ctx, old)
	}
	return nil
}

// Stop closes the current tunnel and its sessions without changing the desired
// configuration; the next Apply resumes it.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.stopped = true
	t := m.tun
	conns := m.revokeLocked(t)
	m.settleLocked()
	m.unlock()
	if err := closeAll(ctx, conns); err != nil {
		return err
	}
	return wait(ctx, t)
}

// Close permanently shuts hosting down and waits for the tunnel run to end.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.baseStop()
	t := m.tun
	conns := m.revokeLocked(t)
	m.settleLocked()
	m.unlock()
	_ = closeAll(context.Background(), conns)
	_ = wait(context.Background(), t)
}

func wait(ctx context.Context, t *tunnel) error {
	if t == nil {
		return nil
	}
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot returns the current configuration and status, without the credential.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{
		Config: m.cfg.clone(), State: m.state, Reason: m.reason, ChangedAt: m.changedAt,
		Active: m.tcp + m.udp, Refused: m.refused, Dropped: m.dropped, Oversized: m.oversizedN,
		Shares: make([]ShareStatus, 0, len(m.cfg.Shares)),
	}
	for _, sh := range m.cfg.Shares {
		s.Shares = append(s.Shares, ShareStatus{Share: sh, Active: m.active[sh.ID]})
	}
	return s
}

func (m *Manager) runLoop(t *tunnel) {
	delay := m.backoffMin
	for t.ctx.Err() == nil {
		t.connectedReset()
		err := m.transport.Run(t.ctx, t.relay, t.cred, t)
		if t.ctx.Err() != nil {
			break
		}
		if errors.Is(err, ErrAuth) {
			m.mu.Lock()
			if m.tun == t && !t.revoked {
				m.latched = true
			}
			m.mu.Unlock()
			break
		}
		m.mu.Lock()
		conns := m.revokeSessionsLocked(func(s *session) bool { return s.t == t })
		m.setStateLocked(StateReconnecting, reasonFor(err))
		reset := t.connected
		m.unlock()
		_ = closeAll(context.Background(), conns)
		if reset {
			delay = m.backoffMin
		}
		timer := time.NewTimer(min(delay+rand.N(delay/4+1), m.backoffMax))
		select {
		case <-t.ctx.Done():
		case <-t.wake:
		case <-timer.C:
		}
		timer.Stop()
		delay = min(delay*2, m.backoffMax)
	}

	m.mu.Lock()
	t.revoked = true
	conns := m.revokeSessionsLocked(func(s *session) bool { return s.t == t })
	if m.tun == t {
		m.tun = nil
	}
	m.settleLocked()
	m.startLocked()
	m.unlock()
	_ = closeAll(context.Background(), conns)
	close(t.done)
}

func (t *tunnel) connectedReset() {
	t.m.mu.Lock()
	t.connected = false
	t.m.mu.Unlock()
}

func reasonFor(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return "relay closed the connection"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "relay timed out"
	}
	return "relay unavailable"
}

func (m *Manager) liveLocked(t *tunnel) bool {
	return m.tun == t && !t.revoked && t.ctx.Err() == nil
}

// Connected implements Receiver.
func (t *tunnel) Connected() {
	m := t.m
	m.mu.Lock()
	if m.liveLocked(t) {
		t.connected = true
		m.setStateLocked(StateConnected, "")
	}
	m.unlock()
}

// Drop implements Receiver.
func (t *tunnel) Drop(oversized bool) {
	m := t.m
	m.mu.Lock()
	if m.liveLocked(t) {
		if oversized {
			satInc(&m.oversizedN)
		} else {
			satInc(&m.dropped)
		}
		m.dirty = true
	}
	m.unlock()
}

func satInc(p *uint64) {
	if *p != math.MaxUint64 {
		*p++
	}
}

// Open implements Receiver: it admits exactly the virtual address, network and exposed
// port of an enabled share and dials that share's loopback target directly.
func (t *tunnel) Open(ctx context.Context, d Destination) (net.Conn, error) {
	m := t.m
	m.mu.Lock()
	if !m.liveLocked(t) {
		m.mu.Unlock()
		return nil, ErrRefused
	}
	var share *Share
	if d.Address == m.cfg.Relay.Address && (d.Network == ProtoTCP || d.Network == ProtoUDP) {
		for i := range m.cfg.Shares {
			sh := &m.cfg.Shares[i]
			if sh.Enabled && sh.ExposedPort == d.Port && sh.covers(d.Network) {
				share = sh
				break
			}
		}
	}
	if share == nil || (d.Network == ProtoTCP && m.tcp >= MaxTCP) || (d.Network == ProtoUDP && m.udp >= MaxUDP) {
		satInc(&m.refused)
		m.dirty = true
		m.unlock()
		return nil, ErrRefused
	}
	dctx, cancel := context.WithCancel(ctx)
	s := &session{t: t, share: *share, network: d.Network, cancelDial: cancel, ready: make(chan struct{})}
	defer close(s.ready)
	m.sessions[s] = struct{}{}
	m.active[share.ID]++
	if d.Network == ProtoTCP {
		m.tcp++
	} else {
		m.udp++
	}
	m.dirty = true
	target := net.JoinHostPort(share.LocalAddress, strconv.Itoa(share.LocalPort))
	m.unlock()

	c, err := m.dial(dctx, d.Network, target)
	cancel()

	m.mu.Lock()
	if err != nil || s.revoked || !m.liveLocked(t) {
		if err == nil {
			s.revoked = true
		}
		m.releaseLocked(s)
		m.unlock()
		if err == nil {
			_ = c.Close()
			return nil, ErrRefused
		}
		return nil, ErrUnavailable
	}
	tc := &trackedConn{Conn: c, m: m, s: s, udp: d.Network == ProtoUDP}
	if tc.udp {
		tc.idleFor = m.udpIdle
		tc.timer = time.AfterFunc(m.udpIdle, func() { _ = tc.Close() })
	}
	s.conn = tc
	m.mu.Unlock()
	return tc, nil
}
