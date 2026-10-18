package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/migration"
	"github.com/readeem/omihomo/internal/owner"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/settings"
)

var oldUnits = []string{"omihomo-subscription-update.timer", "omihomo-subscription-update.service", "omihomo-subscription-startup.service", "omihomo.service"}

type cutover struct {
	root      *os.Root
	home      *os.Root
	config    *os.Root
	data      string
	state     string
	uid, gid  int
	userctl   func(context.Context, ...string) (bool, error)
	systemctl func(context.Context, ...string) error
	claim     func(context.Context) error
	// apply runs the owner's settings files, as the owner.
	apply   func(context.Context) error
	check   func(context.Context, bool) error
	undo    []func() error
	discard []func() error
}

func (a *app) claimInstall(ctx context.Context, legacyData, legacyConfig string) error {
	if os.Geteuid() != 0 {
		return errors.New("claim needs root: run sudo omihomo claim")
	}
	lock, err := os.OpenFile("/var/lib/.omihomo-claim.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("another claim is already in progress")
	}
	uid, err := invokingUID(os.Getenv)
	if err != nil {
		return err
	}
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return errors.New("the invoking account could not be found")
	}
	if legacyData == "" {
		legacyData = filepath.Join(account.HomeDir, ".local/share/omihomo")
	}
	if legacyConfig == "" {
		legacyConfig = filepath.Join(account.HomeDir, ".config")
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return err
	}
	defer root.Close()
	home, err := os.OpenRoot(account.HomeDir)
	if err != nil {
		return err
	}
	defer home.Close()
	config, err := os.OpenRoot(legacyConfig)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if config != nil {
		defer config.Close()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return errors.New("the invoking account has no numeric group")
	}
	asOwner := func(ctx context.Context, args ...string) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/runuser", append([]string{"-u", account.Username, "--", "/usr/bin/omihomo"}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin", "LANG=C", "OMIHOMO_SOCKET=" + client.DefaultSocket, "XDG_CONFIG_HOME=" + legacyConfig}
		cmd.Stderr = a.env.Stderr
		return cmd.Run()
	}
	machine := cutover{root: root, home: home, config: config, data: legacyData, state: filepath.Dir(owner.DefaultPath), uid: uid, gid: gid,
		systemctl: func(ctx context.Context, args ...string) error { return runSystemctl(ctx, a.env.Stderr, args...) },
		userctl: func(ctx context.Context, args ...string) (bool, error) {
			cmd := exec.CommandContext(ctx, "/usr/bin/runuser", "-u", account.Username, "--", "/usr/bin/env", "XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(uid), "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+strconv.Itoa(uid)+"/bus", "/usr/bin/systemctl", "--user")
			cmd.Env = []string{"PATH=/usr/bin", "LANG=C"}
			cmd.Args = append(cmd.Args, args...)
			cmd.Stderr = a.env.Stderr
			err := cmd.Run()
			if args[0] == "is-active" {
				var exit *exec.ExitError
				if errors.As(err, &exit) && (exit.ExitCode() == 3 || exit.ExitCode() == 4) {
					return false, nil
				}
			}
			return err == nil, err
		},
		claim: func(ctx context.Context) error {
			return claimer{euid: os.Geteuid(), getenv: os.Getenv, ownerFile: owner.DefaultPath, dropIn: defaultDropIn,
				systemctl: func(ctx context.Context, args ...string) error { return runSystemctl(ctx, a.env.Stderr, args...) }}.claim(ctx)
		},
	}
	machine.apply = func(ctx context.Context) error { return asOwner(ctx, "apply") }
	machine.check = func(ctx context.Context, running bool) error {
		if running {
			return asOwner(ctx, "start")
		}
		return asOwner(ctx, "status")
	}
	return machine.run(ctx)
}

func (c *cutover) run(ctx context.Context) (err error) {
	var (
		active []string
		units  []string
	)
	if c.config != nil {
		for _, unit := range oldUnits {
			if _, e := c.config.Lstat("systemd/user/" + unit); e == nil {
				units = append(units, unit)
				on, e := c.userctl(ctx, "is-active", "--quiet", unit)
				if e != nil {
					return fmt.Errorf("inspect old user service: %w", e)
				}
				if on {
					active = append(active, unit)
				}
			} else if !errors.Is(e, fs.ErrNotExist) {
				return e
			}
		}
	}
	running := false
	for _, unit := range active {
		if unit == serviceUnit {
			running = true
		}
	}
	removeLauncher, err := c.oldLauncher()
	if err != nil {
		return err
	}

	var parser mihomo.Core
	staged, err := migration.Prepare(ctx, c.data, c.state, running, parser.ParseLinks)
	if err != nil {
		return fmt.Errorf("prepare migration (old install unchanged): %w", err)
	}
	if staged != nil {
		defer staged.Close()
	}
	legacy := staged != nil || len(units) > 0
	for _, name := range []string{"usr/share/libalpm/hooks/omihomo-permissions.hook", "usr/share/libalpm/hooks/omihomo-capabilities.hook"} {
		if _, e := c.root.Lstat(name); e == nil {
			legacy = true
		} else if !errors.Is(e, fs.ErrNotExist) {
			return e
		}
	}
	if !legacy {
		if err := c.claim(ctx); err != nil {
			return err
		}
		if removeLauncher {
			return c.home.Remove(".local/bin/omihomo")
		}
		return nil
	}
	hadTailscale := false
	changed := false
	defer func() {
		if err == nil {
			if staged != nil {
				staged.Finish()
			}
			for _, discard := range c.discard {
				if e := discard(); e != nil {
					err = errors.Join(err, fmt.Errorf("remove cutover backup: %w", e))
				}
			}
			return
		}
		if !changed {
			return
		}
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, c.systemctl(recovery, "stop", serviceUnit, socketUnit))
		for _, v := range slices.Backward(c.undo) {
			err = errors.Join(err, v())
		}
		if staged != nil {
			err = errors.Join(err, staged.Rollback())
		}
		if len(units) > 0 {
			_, e := c.userctl(recovery, "daemon-reload")
			err = errors.Join(err, e)
		}
		if hadTailscale {
			err = errors.Join(err, c.systemctl(recovery, "daemon-reload"), c.systemctl(recovery, "try-restart", "tailscaled.service"))
		}
		if len(active) > 0 {
			_, e := c.userctl(recovery, append([]string{"start"}, active...)...)
			err = errors.Join(err, e)
		}
	}()
	if err = c.systemctl(ctx, "stop", serviceUnit, socketUnit); err != nil {
		return err
	}
	changed = true
	if len(units) > 0 {
		if _, err = c.userctl(ctx, append([]string{"stop"}, units...)...); err != nil {
			return err
		}
	}
	if staged != nil {
		if err = staged.Commit(); err != nil {
			return err
		}
		if c.config != nil {
			if err = c.writeSettings(protocol.ApplyArgs{Settings: staged.Settings.Settings, Rules: staged.Settings.Rules}); err != nil {
				return fmt.Errorf("write the settings files: %w", err)
			}
		}
	}
	for _, unit := range units {
		if err = c.move(c.config, "systemd/user/"+unit); err != nil {
			return err
		}
		for _, target := range []string{"default.target.wants", "timers.target.wants"} {
			if err = c.move(c.config, "systemd/user/"+target+"/"+unit); err != nil {
				return err
			}
		}
	}
	if len(units) > 0 {
		if _, err = c.userctl(ctx, "daemon-reload"); err != nil {
			return err
		}
	}
	for _, hook := range []string{"usr/share/libalpm/hooks/omihomo-permissions.hook", "usr/share/libalpm/hooks/omihomo-capabilities.hook"} {
		if err = c.move(c.root, hook); err != nil {
			return err
		}
	}
	if err = c.stripPrivileges(); err != nil {
		return err
	}
	if removeLauncher {
		if err = c.move(c.home, ".local/bin/omihomo"); err != nil {
			return err
		}
	}

	const tailscaleDropIn = "etc/systemd/system/tailscaled.service.d/omihomo.conf"
	if _, e := c.root.Lstat(tailscaleDropIn); e == nil {
		hadTailscale = true
		if err = c.move(c.root, tailscaleDropIn); err != nil {
			return err
		}
	} else if errors.Is(e, fs.ErrNotExist) {
		c.undo = append(c.undo, func() error {
			e := c.root.Remove(tailscaleDropIn)
			if errors.Is(e, fs.ErrNotExist) {
				return nil
			}
			return e
		})
	} else {
		return e
	}

	if err = c.claim(ctx); err != nil {
		return err
	}
	// Settings files the owner already had are what runs, not the import.
	if staged != nil && c.config != nil && c.apply != nil {
		if err = c.apply(ctx); err != nil {
			return err
		}
	}
	if c.check != nil {
		return c.check(ctx, running)
	}
	return nil
}

// writeSettings gives the owner the imported settings as their settings
// files, unless they already have either, as a dotfile manager may have
// placed them before the claim.
func (c *cutover) writeSettings(v protocol.ApplyArgs) error {
	files := settings.Encode(v)
	for _, f := range files {
		if _, err := c.config.Lstat("omihomo/" + f.Name); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := c.config.Mkdir("omihomo", 0o700); err == nil {
		c.undo = append(c.undo, func() error { return c.config.Remove("omihomo") })
		if err := c.config.Lchown("omihomo", c.uid, c.gid); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return err
	}
	for _, f := range files {
		name := "omihomo/" + f.Name
		file, err := c.config.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		c.undo = append(c.undo, func() error { return c.config.Remove(name) })
		err = file.Chown(c.uid, c.gid)
		if err == nil {
			_, err = file.Write(f.Data)
		}
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *cutover) move(root *os.Root, name string) error {
	if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	backup := name + ".cutover-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := root.Rename(name, backup); err != nil {
		return err
	}
	c.undo = append(c.undo, func() error { return root.Rename(backup, name) })
	c.discard = append(c.discard, func() error { return root.Remove(backup) })
	return nil
}

func (c *cutover) stripPrivileges() error {
	file, err := c.root.OpenFile("usr/bin/mihomo", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("old mihomo is not a regular file")
	}
	fd := int(file.Fd())
	attrs := map[string][]byte{}
	for _, name := range []string{"security.capability", "system.posix_acl_access"} {
		data := make([]byte, 65536)
		n, e := unix.Fgetxattr(fd, name, data)
		if errors.Is(e, unix.ENODATA) || errors.Is(e, unix.ENOTSUP) {
			continue
		}
		if e != nil {
			return e
		}
		attrs[name] = data[:n]
	}
	mode := info.Mode()
	c.undo = append(c.undo, func() error {
		f, e := c.root.OpenFile("usr/bin/mihomo", os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		defer f.Close()
		if e = f.Chmod(mode); e != nil {
			return e
		}
		for name, data := range attrs {
			if e = unix.Fsetxattr(int(f.Fd()), name, data, 0); e != nil {
				return e
			}
		}
		return nil
	})
	if err = file.Chmod(0o700); err != nil {
		return err
	}
	for name := range attrs {
		if err = unix.Fremovexattr(fd, name); err != nil {
			return err
		}
	}
	return file.Chmod(0o755)
}

func (c *cutover) oldLauncher() (bool, error) {
	info, err := c.home.Lstat(".local/bin/omihomo")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	if info.Mode().IsRegular() && info.Size() <= 4096 {
		f, err := c.home.OpenFile(".local/bin/omihomo", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return false, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return false, err
		}
		if strings.Contains(string(data), `exec "$OMIHOMO_ROOT/bin/omihomo" "$@"`) {
			return true, nil
		}
	}
	return false, errors.New("the user omihomo launcher is unrecognized and shadows the packaged CLI; move it aside before claiming")
}
