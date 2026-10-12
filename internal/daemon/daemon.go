// Package daemon serves the protocol to the owner over a unix socket and
// drives a core.Core on its behalf.
package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/owner"
	"github.com/readeem/omihomo/internal/peercred"
	"github.com/readeem/omihomo/internal/protocol"
)

// Daemon serves the protocol on its listeners until ctx is done, then
// disconnects every client and stops the core. Run closes the listeners.
type Daemon interface {
	Run(ctx context.Context, listeners ...net.Listener) error
}

// ConfigOpt changes one optional setting of New. A zero value, such as a
// nil logger or a non-positive count, keeps the default.
type ConfigOpt func(*config)

type config struct {
	ownerFile        string
	version          string
	log              *slog.Logger
	tick             time.Duration
	egressURL        string
	tailscalePresent func() bool
	maxClients       int
	profile          core.Profile
	groupsEvery      time.Duration
}

// WithOwnerFile reads the owner from path instead of owner.DefaultPath. The
// file is read on every connection, so claiming ownership needs no restart.
func WithOwnerFile(path string) ConfigOpt {
	return func(c *config) { set(&c.ownerFile, path, path != "") }
}

// WithVersion is the version the greeting reports.
func WithVersion(v string) ConfigOpt { return func(c *config) { set(&c.version, v, v != "") } }

func WithLogger(l *slog.Logger) ConfigOpt { return func(c *config) { set(&c.log, l, l != nil) } }

// WithTick is how often the traffic and connections streams push.
func WithTick(d time.Duration) ConfigOpt { return func(c *config) { set(&c.tick, d, d > 0) } }

// WithEgressURL is fetched by egress.check. It must answer with a
// Cloudflare-style trace: "ip=<addr>" lines.
func WithEgressURL(url string) ConfigOpt {
	return func(c *config) { set(&c.egressURL, url, url != "") }
}

// WithTailscaleProbe reports whether tailscaled is installed.
func WithTailscaleProbe(present func() bool) ConfigOpt {
	return func(c *config) { set(&c.tailscalePresent, present, present != nil) }
}

// WithProfile is the subscription content the proxy runs.
func WithProfile(p core.Profile) ConfigOpt { return func(c *config) { c.profile = p } }

// WithGroupsRefresh is how often the groups stream is checked for changes
// the core made on its own, such as an automatic group picking another
// proxy after a health check.
func WithGroupsRefresh(d time.Duration) ConfigOpt {
	return func(c *config) { set(&c.groupsEvery, d, d > 0) }
}

// WithMaxClients bounds how many clients are served at once.
func WithMaxClients(n int) ConfigOpt { return func(c *config) { set(&c.maxClients, n, n > 0) } }

func set[T any](field *T, v T, ok bool) {
	if ok {
		*field = v
	}
}

const (
	// Calls one client may have in flight at once.
	maxInFlight = 8
	// Messages queued for one client before it counts as stuck.
	sendQueue    = 256
	writeTimeout = 5 * time.Second
	callTimeout  = 2 * time.Minute
)

type server struct {
	core core.Core
	cfg  config
	log  *slog.Logger

	clients sync.WaitGroup

	// opMu serializes the calls that change what the core runs.
	opMu sync.Mutex

	mu        sync.Mutex
	state     string
	detail    string
	startedAt int64
	settings  settings
	sessions  map[*session]struct{}

	// pubMu orders pushes, so a client never sees an older value of a state
	// stream after a newer one.
	pubMu      sync.Mutex
	lastPushed map[string][]byte
}

var _ Daemon = (*server)(nil)

// New returns a daemon driving c, with the proxy stopped.
func New(c core.Core, opts ...ConfigOpt) Daemon {
	cfg := config{
		ownerFile: owner.DefaultPath,
		version:   "dev",
		log:       slog.Default(),
		tick:      time.Second,
		egressURL: "https://cloudflare.com/cdn-cgi/trace",
		tailscalePresent: func() bool {
			_, err := exec.LookPath("tailscaled")
			return err == nil
		},
		maxClients:  16,
		groupsEvery: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &server{
		core:       c,
		cfg:        cfg,
		log:        cfg.log,
		state:      protocol.StateStopped,
		settings:   defaultSettings(),
		sessions:   map[*session]struct{}{},
		lastPushed: map[string][]byte{},
	}
}

func (d *server) Run(ctx context.Context, listeners ...net.Listener) error {
	if len(listeners) == 0 {
		return errors.New("no socket to serve")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, len(listeners))
	for _, l := range listeners {
		wg.Go(func() {
			if err := d.accept(ctx, l); err != nil {
				errs <- err
				cancel()
			}
		})
	}
	wg.Go(func() { d.tick(ctx) })

	<-ctx.Done()
	for _, l := range listeners {
		l.Close()
	}
	wg.Wait()
	d.clients.Wait()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := d.core.Stop(stopCtx); err != nil {
		d.log.Error("stop the core on shutdown", "err", err)
	}
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func (d *server) accept(ctx context.Context, l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		d.clients.Go(func() { d.admit(ctx, conn) })
	}
}

func (d *server) admit(ctx context.Context, conn net.Conn) {
	refuse := func(code, message string) {
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		if b, err := protocol.Line(protocol.Refused{Type: protocol.TypeRefused,
			Error: &protocol.Error{Code: code, Message: message}}); err == nil {
			conn.Write(b)
		}
		conn.Close()
	}

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		refuse(protocol.CodeForbidden, "only unix socket peers are served")
		return
	}
	uid, err := peercred.UID(unixConn)
	if err != nil {
		d.log.Warn("refused a client whose credentials could not be read", "err", err)
		refuse(protocol.CodeForbidden, "could not identify the connecting account")
		return
	}
	ownerUID, err := owner.Load(d.cfg.ownerFile)
	if err != nil {
		d.log.Warn("refused a client: no usable owner", "uid", uid, "err", err)
		if errors.Is(err, owner.ErrUnclaimed) {
			refuse(protocol.CodeForbidden, err.Error())
		} else {
			refuse(protocol.CodeForbidden, "the owner record is unusable; reinstall the core")
		}
		return
	}
	if uid != ownerUID {
		d.log.Warn("refused a client that is not the owner", "uid", uid)
		refuse(protocol.CodeForbidden, "this account does not own omihomo")
		return
	}

	s := newSession(d, conn)
	d.mu.Lock()
	full := len(d.sessions) >= d.cfg.maxClients
	if !full {
		d.sessions[s] = struct{}{}
	}
	d.mu.Unlock()
	if full {
		refuse(protocol.CodeBusy, "too many clients")
		return
	}
	s.send(protocol.Hello{Type: protocol.TypeHello, Protocol: protocol.Version, Version: d.cfg.version})
	s.serve(ctx)

	d.mu.Lock()
	delete(d.sessions, s)
	d.mu.Unlock()
}

func (d *server) tick(ctx context.Context) {
	t := time.NewTicker(d.cfg.tick)
	defer t.Stop()
	groups := time.NewTicker(d.cfg.groupsEvery)
	defer groups.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-groups.C:
			// The core changes groups on its own too; a push goes out only
			// when they differ from the last one.
			if d.running() && d.listening(protocol.StreamGroups) {
				d.pubMu.Lock()
				d.publishLocked(ctx, protocol.StreamGroups, nil)
				d.pubMu.Unlock()
			}
			continue
		case <-t.C:
		}
		if !d.running() {
			continue
		}
		if d.listening(protocol.StreamTraffic) {
			if v, err := d.core.Traffic(ctx); err == nil {
				d.broadcast(protocol.StreamTraffic, v)
			}
		}
		if d.listening(protocol.StreamConnections) {
			if v, err := d.core.Connections(ctx); err == nil {
				d.broadcast(protocol.StreamConnections, v)
			}
		}
	}
}

func (d *server) running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state == protocol.StateOn || d.state == protocol.StateDegraded
}

func (d *server) listening(stream string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for s := range d.sessions {
		if s.wants(stream) {
			return true
		}
	}
	return false
}

func (d *server) subscribers(stream string) []*session {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*session
	for s := range d.sessions {
		if s.wants(stream) {
			out = append(out, s)
		}
	}
	return out
}

func (d *server) broadcast(stream string, v any) {
	b := d.eventLine(stream, v)
	for _, s := range d.subscribers(stream) {
		s.sendLine(b)
	}
}

// eventLine encodes a push. A value too large to send is pushed as an error
// instead, so subscribers know their copy is stale rather than missing it.
func (d *server) eventLine(stream string, data any) []byte {
	b, err := protocol.Line(protocol.Event{Type: protocol.TypeEvent, Stream: stream, Data: data})
	if err == nil {
		return b
	}
	d.log.Error("encode event", "stream", stream, "err", err)
	code := protocol.CodeInternal
	if errors.Is(err, protocol.ErrTooLarge) {
		code = protocol.CodeTooLarge
	}
	b, _ = protocol.Line(protocol.Event{Type: protocol.TypeEvent, Stream: stream, Error: fail(code, "%v", err)})
	return b
}
