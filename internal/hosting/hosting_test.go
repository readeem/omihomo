package hosting

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testCred = "123e4567-e89b-12d3-a456-426614174000"
	testKey  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	virtual  = "10.77.0.2"
)

func init() {
	interfacePrefixes = func() ([]netip.Prefix, error) {
		return []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, nil
	}
}

func relay() *Relay {
	return &Relay{Version: 1, Label: "friends", Host: "relay.example.com", Port: 443, Address: virtual,
		Reality: Reality{ServerName: "cdn.example.com", PublicKey: testKey, ShortID: "ab12"}}
}

func share(id string, local, exposed int, proto string, on bool) Share {
	return Share{ID: id, Label: id, LocalAddress: "127.0.0.1", LocalPort: local, ExposedPort: exposed, Protocol: proto, Enabled: on}
}

func TestValidate(t *testing.T) {
	bad := map[string]func(*Config){
		"public virtual":  func(c *Config) { c.Relay.Address = "8.8.8.8" },
		"lan overlap":     func(c *Config) { c.Relay.Address = "192.168.1.5" },
		"cgnat":           func(c *Config) { c.Relay.Address = "100.64.0.2" },
		"version":         func(c *Config) { c.Relay.Version = 2 },
		"port":            func(c *Config) { c.Relay.Port = 0 },
		"local host":      func(c *Config) { c.Relay.Host = "foo.local" },
		"short key":       func(c *Config) { c.Relay.Reality.PublicKey = "AAAA" },
		"odd short id":    func(c *Config) { c.Relay.Reality.ShortID = "abc" },
		"long short id":   func(c *Config) { c.Relay.Reality.ShortID = strings.Repeat("a", 18) },
		"empty sni":       func(c *Config) { c.Relay.Reality.ServerName = "" },
		"long label":      func(c *Config) { c.Relay.Label = strings.Repeat("é", 33) },
		"wildcard target": func(c *Config) { c.Shares[0].LocalAddress = "0.0.0.0" },
		"lan target":      func(c *Config) { c.Shares[0].LocalAddress = "192.168.1.5" },
		"alt loopback":    func(c *Config) { c.Shares[0].LocalAddress = "127.1" },
		"duplicate tuple": func(c *Config) { c.Shares = append(c.Shares, share("b", 2, 25565, ProtoBoth, false)) },
		"too many": func(c *Config) {
			for i := range MaxShares {
				c.Shares = append(c.Shares, share("s"+strconv.Itoa(i), 1, 1000+i, ProtoTCP, false))
			}
		},
	}
	for name, mut := range bad {
		c := Config{Relay: relay(), Shares: []Share{share("a", 25565, 25565, ProtoTCP, true)}}
		mut(&c)
		if err := Validate(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	good := Config{Relay: relay(), Shares: []Share{share("a", 1, 25565, ProtoTCP, true), share("b", 2, 25565, ProtoUDP, true)}}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Config{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCredential(t *testing.T) {
	if got, err := ValidateCredential([]byte(testCred + "\r\n")); err != nil || got != testCred {
		t.Fatalf("got %q %v", got, err)
	}
	for _, s := range []string{"", testCred + "x", strings.ToUpper(testCred), " " + testCred, "123e4567e89b12d3a456426614174000", strings.Repeat("a", 300)} {
		if _, err := ValidateCredential([]byte(s)); err == nil {
			t.Errorf("accepted %q", s)
		} else if strings.Contains(err.Error(), "123e") {
			t.Error("error echoes input")
		}
	}
}

type fakeTransport struct {
	runs    atomic.Int32
	live    atomic.Int32
	maxLive atomic.Int32
	recv    chan Receiver
	script  chan error
	creds   chan string
}

func newFake() *fakeTransport {
	return &fakeTransport{recv: make(chan Receiver, 16), script: make(chan error, 16), creds: make(chan string, 16)}
}

func (f *fakeTransport) Run(ctx context.Context, _ Relay, cred string, r Receiver) error {
	f.runs.Add(1)
	n := f.live.Add(1)
	defer f.live.Add(-1)
	for {
		m := f.maxLive.Load()
		if n <= m || f.maxLive.CompareAndSwap(m, n) {
			break
		}
	}
	f.creds <- cred
	select {
	case err := <-f.script:
		return err
	default:
	}
	f.recv <- r
	<-ctx.Done()
	time.Sleep(5 * time.Millisecond)
	return ctx.Err()
}

func (f *fakeTransport) next(t *testing.T) Receiver {
	t.Helper()
	select {
	case r := <-f.recv:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("transport not started")
		return nil
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	dl := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(dl) {
			t.Fatal("timeout: " + what)
		}
		time.Sleep(time.Millisecond)
	}
}

func tcpEcho(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func udpEcho(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, a, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(buf[:n], a)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func start(t *testing.T, cfg Config) (*Manager, *fakeTransport, Receiver) {
	t.Helper()
	f := newFake()
	m := New(f, nil)
	t.Cleanup(m.Close)
	if err := m.Apply(context.Background(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	return m, f, f.next(t)
}

func dest(port int, network string) Destination {
	return Destination{Address: virtual, Port: port, Network: network}
}

func echoOnce(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo %q: %v", buf, err)
	}
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("connection still open: %v", err)
	}
}

func TestAdmissionIsExact(t *testing.T) {
	tp, up := tcpEcho(t), udpEcho(t)
	cfg := Config{Relay: relay(), Shares: []Share{share("g", tp, 7000, ProtoTCP, true), share("u", up, 7001, ProtoUDP, true), share("off", tp, 7002, ProtoTCP, false)}}
	m, _, r := start(t, cfg)

	c, err := r.Open(context.Background(), dest(7000, "tcp"))
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, c, "hello")
	u, err := r.Open(context.Background(), dest(7001, "udp"))
	if err != nil {
		t.Fatal(err)
	}
	echoOnce(t, u, "ping")

	refused := []Destination{
		{Address: virtual, Port: 7000, Network: "udp"},
		{Address: virtual, Port: 7001, Network: "tcp"},
		{Address: virtual, Port: 7002, Network: "tcp"},
		{Address: virtual, Port: 7003, Network: "tcp"},
		{Address: "10.77.0.3", Port: 7000, Network: "tcp"},
		{Address: "127.0.0.1", Port: tp, Network: "tcp"},
		{Address: "010.077.000.002", Port: 7000, Network: "tcp"},
		{Address: virtual, Port: 7000, Network: "tcp4"},
	}
	for _, d := range refused {
		if conn, err := r.Open(context.Background(), d); !errors.Is(err, ErrRefused) {
			t.Errorf("%+v admitted (%v)", d, err)
			if conn != nil {
				conn.Close()
			}
		}
	}
	s := m.Snapshot()
	if s.Refused != uint64(len(refused)) || s.Active != 2 || s.State != StateIdle && s.State != StateConnecting {
		t.Fatalf("snapshot %+v", s)
	}
	if s.Shares[0].Active != 1 || s.Shares[1].Active != 1 {
		t.Fatalf("per-share %+v", s.Shares)
	}
	c.Close()
	u.Close()
	if m.Snapshot().Active != 0 {
		t.Fatal("slots not released")
	}
}

func TestEditsRevokeBeforeReturn(t *testing.T) {
	a, b := tcpEcho(t), tcpEcho(t)
	cfg := Config{Relay: relay(), Shares: []Share{share("a", a, 7000, ProtoTCP, true), share("b", b, 7001, ProtoTCP, true), share("d", a, 7002, ProtoTCP, false)}}
	m, f, r := start(t, cfg)
	ctx := context.Background()
	open := func(port int) net.Conn {
		c, err := r.Open(ctx, dest(port, "tcp"))
		if err != nil {
			t.Fatal(err)
		}
		echoOnce(t, c, "x")
		return c
	}
	ca, cb := open(7000), open(7001)
	next := cfg.clone()
	next.Shares[2].Label = "renamed"
	next.Shares = append(next.Shares, share("n", a, 7003, ProtoBoth, true))
	if err := m.Apply(ctx, next, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	echoOnce(t, ca, "y")
	echoOnce(t, cb, "y")
	if f.runs.Load() != 1 {
		t.Fatal("tunnel restarted")
	}
	next = next.clone()
	next.Shares[0].LocalPort = b
	if err := m.Apply(ctx, next, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, ca)
	echoOnce(t, cb, "z")
	if m.Snapshot().Active != 1 {
		t.Fatalf("active %d", m.Snapshot().Active)
	}
	next = next.clone()
	next.Shares[1].Enabled = false
	if err := m.Apply(ctx, next, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, cb)
	if _, err := r.Open(ctx, dest(7001, "tcp")); !errors.Is(err, ErrRefused) {
		t.Fatalf("disabled share admitted: %v", err)
	}
	if f.runs.Load() != 1 {
		t.Fatal("tunnel restarted")
	}
}

func TestPauseProxyOffAndCredentialReplacement(t *testing.T) {
	tp := tcpEcho(t)
	cfg := Config{Relay: relay(), Shares: []Share{share("a", tp, 7000, ProtoTCP, true)}}
	for name, step := range map[string]func(*Manager) error{
		"pause": func(m *Manager) error {
			c := cfg.clone()
			c.Paused = true
			return m.Apply(context.Background(), c, testCred, true, false)
		},
		"proxy off": func(m *Manager) error { return m.Apply(context.Background(), cfg, testCred, false, false) },
		"credential": func(m *Manager) error {
			return m.Apply(context.Background(), cfg, "00000000-0000-4000-8000-000000000000", true, false)
		},
		"stop": func(m *Manager) error { return m.Stop(context.Background()) },
	} {
		t.Run(name, func(t *testing.T) {
			m, f, r := start(t, cfg)
			c, err := r.Open(context.Background(), dest(7000, "tcp"))
			if err != nil {
				t.Fatal(err)
			}
			if err := step(m); err != nil {
				t.Fatal(err)
			}
			expectClosed(t, c)
			if f.live.Load() != 0 && name != "credential" {
				t.Fatal("run not joined")
			}
			if _, err := r.Open(context.Background(), dest(7000, "tcp")); !errors.Is(err, ErrRefused) {
				t.Fatalf("old run opened a target: %v", err)
			}
			if name == "credential" {
				if got := <-f.creds; got != testCred {
					t.Fatal("first run credential")
				}
				<-f.creds
				f.next(t)
				if f.maxLive.Load() != 1 {
					t.Fatal("duplicate tunnels")
				}
				return
			}
			if s := m.Snapshot(); s.State != StateIdle || s.Config.Relay == nil || !s.Config.Shares[0].Enabled {
				t.Fatalf("snapshot %+v", s)
			}
			if err := step(m); err != nil { // idempotent
				t.Fatal(err)
			}
		})
	}
}

func TestUnprovisionedAndIdle(t *testing.T) {
	f := newFake()
	m := New(f, nil)
	defer m.Close()
	cfg := Config{Relay: relay(), Shares: []Share{share("a", 1, 7000, ProtoTCP, true)}}
	if err := m.Apply(context.Background(), cfg, "", true, false); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.State != StateIdle || s.Reason != "unprovisioned" || f.runs.Load() != 0 {
		t.Fatalf("%+v", s)
	}
	if err := m.Apply(context.Background(), Config{}, "", true, false); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.State != StateIdle || s.Reason != "" {
		t.Fatalf("%+v", s)
	}
	bad := cfg.clone()
	bad.Shares[0].LocalAddress = "0.0.0.0"
	if err := m.Apply(context.Background(), bad, testCred, true, false); err == nil {
		t.Fatal("invalid accepted")
	}
	if m.Snapshot().Config.Relay != nil {
		t.Fatal("invalid apply mutated state")
	}
}

func TestRetryBackoffAndAuthLatch(t *testing.T) {
	f := newFake()
	m := New(f, nil)
	defer m.Close()
	m.backoffMin, m.backoffMax = time.Millisecond, 4*time.Millisecond
	cfg := Config{Relay: relay(), Shares: []Share{share("a", 1, 7000, ProtoTCP, true)}}
	f.script <- errors.New("dial failed: secret-" + testCred)
	f.script <- &net.DNSError{IsTimeout: true}
	if err := m.Apply(context.Background(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	r := f.next(t)
	if f.runs.Load() != 3 {
		t.Fatalf("runs %d", f.runs.Load())
	}
	r.Connected()
	if s := m.Snapshot(); s.State != StateConnected || s.Reason != "" {
		t.Fatalf("%+v", s)
	}

	f.script <- ErrAuth
	if err := m.Apply(context.Background(), cfg, "00000000-0000-4000-8000-000000000001", true, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "auth error", func() bool { s := m.Snapshot(); return s.State == StateError && s.Reason == "authentication refused" })
	runs := f.runs.Load()
	if err := m.Apply(context.Background(), cfg, "00000000-0000-4000-8000-000000000001", true, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if f.runs.Load() != runs {
		t.Fatal("auth refusal retried")
	}
	if err := m.Apply(context.Background(), cfg, "00000000-0000-4000-8000-000000000001", true, true); err != nil {
		t.Fatal(err)
	}
	f.next(t)
	if f.runs.Load() != runs+1 {
		t.Fatal("explicit retry did not restart")
	}
}

func TestReconnectReasonIsSanitized(t *testing.T) {
	cfg := Config{Relay: relay(), Shares: []Share{share("a", 1, 7000, ProtoTCP, true)}}
	f2 := newFake()
	m2 := New(f2, nil)
	defer m2.Close()
	m2.backoffMin, m2.backoffMax = time.Hour, time.Hour
	f2.script <- errors.New(testCred)
	if err := m2.Apply(context.Background(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reconnecting", func() bool { return m2.Snapshot().State == StateReconnecting })
	if s := m2.Snapshot(); strings.Contains(s.Reason, "123e") || s.Reason == "" {
		t.Fatalf("reason %q", s.Reason)
	}
}

func TestUDPAssociationsIdleAndDatagramLimit(t *testing.T) {
	up := udpEcho(t)
	f := newFake()
	m := New(f, nil)
	defer m.Close()
	m.udpIdle = 80 * time.Millisecond
	cfg := Config{Relay: relay(), Shares: []Share{share("u", up, 7001, ProtoUDP, true)}}
	if err := m.Apply(context.Background(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	r := f.next(t)
	a, _ := r.Open(context.Background(), dest(7001, "udp"))
	b, _ := r.Open(context.Background(), dest(7001, "udp"))
	if a == nil || b == nil {
		t.Fatal("open")
	}
	echoOnce(t, a, "from-a")
	echoOnce(t, b, "from-b")
	if _, err := a.Write(make([]byte, MaxDatagram+1)); !errors.Is(err, ErrOversized) {
		t.Fatalf("oversized write: %v", err)
	}
	if m.Snapshot().Oversized != 1 {
		t.Fatal("oversized not counted")
	}
	r.Drop(false)
	r.Drop(true)
	if s := m.Snapshot(); s.Dropped != 1 || s.Oversized != 2 {
		t.Fatalf("%+v", s)
	}
	for range 4 {
		time.Sleep(40 * time.Millisecond)
		echoOnce(t, a, "keep")
	}
	buf := make([]byte, 1)
	b.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := b.Read(buf); err == nil {
		t.Fatal("idle association survived")
	}
	eventually(t, "idle release", func() bool { return m.Snapshot().Active == 1 })
}

func TestSessionLimitsAndSlotRelease(t *testing.T) {
	tp := tcpEcho(t)
	cfg := Config{Relay: relay(), Shares: []Share{share("a", tp, 7000, ProtoTCP, true)}}
	m, _, r := start(t, cfg)
	var conns []net.Conn
	for i := range MaxTCP {
		c, err := r.Open(context.Background(), dest(7000, "tcp"))
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	if _, err := r.Open(context.Background(), dest(7000, "tcp")); !errors.Is(err, ErrRefused) {
		t.Fatalf("over limit admitted: %v", err)
	}
	conns[0].Close()
	c, err := r.Open(context.Background(), dest(7000, "tcp"))
	if err != nil {
		t.Fatalf("slot not reusable: %v", err)
	}
	c.Close()
	if s := m.Snapshot(); s.Refused != 1 || s.Active != MaxTCP-1 {
		t.Fatalf("%+v", s)
	}
}

func TestFailedDialReleasesSlotAndCancelStopsDial(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg := Config{Relay: relay(), Shares: []Share{share("a", port, 7000, ProtoTCP, true)}}
	m, _, r := start(t, cfg)
	if _, err := r.Open(context.Background(), dest(7000, "tcp")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if m.Snapshot().Active != 0 {
		t.Fatal("slot leaked")
	}
}

func TestChangedRunsOutsideLockAndStopDoesNotAlterConfig(t *testing.T) {
	f := newFake()
	var m *Manager
	var calls atomic.Int32
	m = New(f, func() { calls.Add(1); _ = m.Snapshot() })
	cfg := Config{Relay: relay(), Shares: []Share{share("a", 1, 7000, ProtoTCP, true)}}
	if err := m.Apply(context.Background(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	f.next(t)
	if calls.Load() == 0 {
		t.Fatal("no callback")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); len(s.Config.Shares) != 1 || s.State != StateIdle {
		t.Fatalf("%+v", s)
	}
	m.Close()
	m.Close()
	if err := m.Apply(context.Background(), cfg, testCred, true, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
	if f.live.Load() != 0 {
		t.Fatal("run survived Close")
	}
}

func TestDisableWaitsForAnAdmissionAlreadyDialing(t *testing.T) {
	f := newFake()
	m := New(f, nil)
	defer m.Close()
	local, peer := net.Pipe()
	defer peer.Close()
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	m.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return local, nil
	}
	cfg := Config{Relay: relay(), Shares: []Share{share("a", 25565, 7000, ProtoTCP, true)}}
	if err := m.Apply(t.Context(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	r := <-f.recv
	opened := make(chan error, 1)
	go func() {
		c, err := r.Open(t.Context(), dest(7000, ProtoTCP))
		if c != nil {
			c.Close()
		}
		opened <- err
	}()
	<-entered
	cfg.Shares[0].Enabled = false
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := m.Apply(ctx, cfg, testCred, true, false)
	<-canceled
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("disable reported success before pending socket closed: %v", err)
	}
	if err := <-opened; !errors.Is(err, ErrRefused) {
		t.Errorf("revoked admission returned %v", err)
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("revoked pending socket survived: %v", err)
	}
	if err := m.Apply(t.Context(), cfg, testCred, true, false); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Active != 0 {
		t.Fatal("revoked admission retained a session slot")
	}
}
