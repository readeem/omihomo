// Package tailscale applies Omihomo's fixed loopback proxy to tailscaled.
package tailscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const dropInDir = "/etc/systemd/system/tailscaled.service.d"
const filename = "omihomo.conf"
const content = "[Service]\nEnvironment=HTTP_PROXY=http://127.0.0.1:7899\nEnvironment=HTTPS_PROXY=http://127.0.0.1:7899\nEnvironment=NO_PROXY=localhost,127.0.0.1,::1,100.64.0.0/10,fd7a:115c:a1e0::/48\n"

// Sync installs or removes only Omihomo's proxy drop-in and reloads tailscaled.
func Sync(ctx context.Context, enabled bool) error {
	return syncDropIn(ctx, dropInDir, enabled, func(ctx context.Context, args ...string) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/usr/bin/systemctl", args...).Run(); err != nil {
			return fmt.Errorf("systemctl %s failed", args[0])
		}
		return nil
	})
}

func syncDropIn(ctx context.Context, dir string, enabled bool, run func(context.Context, ...string) error) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) && !enabled {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Tailscale integration directory is unavailable; reinstall the daemon package: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("Tailscale integration directory is untrusted")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	previous, err := read(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !enabled && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if enabled && bytes.Equal(previous, []byte(content)) {
		return nil
	}
	if enabled {
		err = write(root, []byte(content))
	} else {
		err = root.Remove(filename)
	}
	if err != nil {
		return err
	}
	apply := func() error {
		if err := run(ctx, "daemon-reload"); err != nil {
			return err
		}
		return run(ctx, "try-restart", "tailscaled.service")
	}
	if err := apply(); err != nil {
		var restore error
		if previous == nil {
			restore = root.Remove(filename)
		} else {
			restore = write(root, previous)
		}
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return errors.Join(err, restore, run(recovery, "daemon-reload"), run(recovery, "try-restart", "tailscaled.service"))
	}
	return nil
}

func write(root *os.Root, data []byte) error {
	const temp = ".omihomo.conf.tmp"
	if err := root.Remove(temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return root.Rename(temp, filename)
}

func read(root *os.Root) ([]byte, error) {
	f, err := root.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, errors.New("Tailscale drop-in must be a regular file under 64 KiB")
	}
	return io.ReadAll(io.LimitReader(f, 65537))
}
