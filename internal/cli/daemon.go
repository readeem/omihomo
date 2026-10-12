package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/readeem/omihomo/internal/clashfile"
	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/owner"
	"github.com/readeem/omihomo/internal/systemd"
)

func (a *app) daemonCommand() *ffcli.Command {
	set := a.flags("daemon")
	socket := set.String("socket", client.DefaultSocket, "socket to bind when not socket-activated")
	ownerFile := set.String("owner-file", owner.DefaultPath, "file recording the owner's uid")
	coreName := set.String("core", "mihomo", "proxy core to run: mihomo, or fake to proxy nothing")
	home := set.String("home", defaultHome, "the core's directory for its state and geodata")
	profile := set.String("profile", "", "development only: run the proxies, groups and rules of this Clash YAML file")
	cmd := &ffcli.Command{Name: "daemon", ShortUsage: "omihomo daemon [flags]", ShortHelp: "run the daemon",
		LongHelp: "Serves until SIGTERM or SIGINT. Under systemd it takes the socket from socket\n" +
			"activation and reports readiness; started by hand it binds --socket itself.",
		FlagSet: set}
	cmd.Exec = func(ctx context.Context, args []string) error {
		if len(args) != 0 {
			return usage(cmd, "daemon takes no arguments")
		}
		// Everything the daemon and the core write is the owner's business
		// only, the core's selection cache included.
		syscall.Umask(0o077)
		log := slog.New(slog.NewTextHandler(a.env.Stderr, nil))
		var opts []daemon.ConfigOpt
		if *profile != "" {
			p, err := loadProfile(*profile, log)
			if err != nil {
				return err
			}
			opts = append(opts, daemon.WithProfile(p))
		}
		var c core.Core
		switch *coreName {
		case "fake":
			c = fakecore.New()
			log.Warn("running the fake core: nothing is proxied")
		case "mihomo":
			m, err := mihomo.New(*home, log)
			if err != nil {
				return fmt.Errorf("start the core: %w", err)
			}
			log.Info("hosting mihomo", "version", mihomo.Version, "home", *home)
			c = m
		default:
			return usage(cmd, "unknown core %q", *coreName)
		}
		return serve(ctx, log, c, *socket, *ownerFile, opts...)
	}
	return cmd
}

const defaultHome = "/var/lib/omihomo/core"

func loadProfile(path string, log *slog.Logger) (core.Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Profile{}, fmt.Errorf("read the profile: %w", err)
	}
	p, skipped, err := clashfile.Parse(data)
	if err != nil {
		return core.Profile{}, fmt.Errorf("read the profile %s: %w", path, err)
	}
	for _, what := range skipped {
		log.Warn("the profile leaves out " + what)
	}
	log.Warn("running a development profile", "path", path,
		"proxies", len(p.Proxies), "groups", len(p.Groups), "rules", len(p.Rules))
	return p, nil
}

func serve(ctx context.Context, log *slog.Logger, c core.Core, socket, ownerFile string, opts ...daemon.ConfigOpt) error {
	listeners, err := systemd.Listeners()
	if err != nil {
		return fmt.Errorf("socket activation: %w", err)
	}
	if len(listeners) == 0 {
		l, err := listen(socket, ownerFile)
		if err != nil {
			return fmt.Errorf("bind %s: %w", socket, err)
		}
		listeners = append(listeners, l)
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	opts = append([]daemon.ConfigOpt{daemon.WithOwnerFile(ownerFile), daemon.WithVersion(Version), daemon.WithLogger(log)}, opts...)
	d := daemon.New(c, opts...)
	if err := systemd.Notify("READY=1"); err != nil {
		log.Warn("notify systemd", "err", err)
	}
	log.Info("serving", "version", Version, "socket", listeners[0].Addr().String())
	err = d.Run(ctx, listeners...)
	systemd.Notify("STOPPING=1")
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// listen binds path as a socket only its owner can open. Under systemd the
// socket unit does this instead, which is what lets a sandboxed daemon
// without CAP_CHOWN hand the socket to the owner.
//
// The socket's directory must belong to the daemon's account and be
// writable by nobody else; otherwise another account could swap the path
// between the bind and the chown and have root chown something else.
func listen(path, ownerFile string) (net.Listener, error) {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect the socket directory: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s must be a directory owned by uid %d and writable by nobody else", dir, os.Geteuid())
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if conn, err := net.Dial("unix", path); err == nil {
			conn.Close()
			return nil, fmt.Errorf("another daemon is serving %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove the stale socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect the socket path: %w", err)
	}
	old := syscall.Umask(0o177)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		uid, err := owner.Load(ownerFile)
		if err == nil {
			err = os.Lchown(path, uid, -1)
		}
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("hand the socket to the owner: %w", err)
		}
	}
	return l, nil
}
