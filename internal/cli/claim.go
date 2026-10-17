package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/readeem/omihomo/internal/owner"
)

// The units the package installs; the socket unit is what hands the socket
// to the owner, since the sandboxed daemon cannot chown it.
const (
	serviceUnit   = "omihomo.service"
	socketUnit    = "omihomo.socket"
	defaultDropIn = "/etc/systemd/system/omihomo.socket.d/owner.conf"
)

func (a *app) claimCommand() *ffcli.Command {
	flags := a.flags("claim")
	legacyData := flags.String("legacy-data", "", "old data directory (defaults to the invoking account's .local/share/omihomo)")
	legacyConfig := flags.String("legacy-config", "", "old config directory (defaults to the invoking account's .config)")
	cmd := &ffcli.Command{Name: "claim", ShortUsage: "sudo omihomo claim [flags]",
		ShortHelp: "make the account that runs sudo the owner",
		LongHelp: "Records the account that ran sudo as the one owner of this install, gives it the\n" +
			"daemon's socket, and starts the daemon. The install step runs it after installing\n" +
			"the package; running it again from another account moves the install to that account.",
		FlagSet: flags}
	cmd.Exec = func(ctx context.Context, args []string) error {
		if len(args) != 0 {
			return usage(cmd, "claim takes no arguments")
		}
		return a.claimInstall(ctx, *legacyData, *legacyConfig)
	}
	return cmd
}

// claimer is claim's view of the machine, so tests can run it unprivileged.
type claimer struct {
	euid      int
	getenv    func(string) string
	ownerFile string
	dropIn    string
	systemctl func(ctx context.Context, args ...string) error
}

func (c claimer) claim(ctx context.Context) error {
	if c.euid != 0 {
		return errors.New("claim needs root: run sudo omihomo claim from the account that should own omihomo")
	}
	uid, err := invokingUID(c.getenv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.ownerFile), 0o700); err != nil {
		return fmt.Errorf("create the state directory: %w", err)
	}
	if err := owner.Store(c.ownerFile, uid); err != nil {
		return err
	}
	_, err = writeDropIn(c.dropIn, uid)
	if err != nil {
		return err
	}
	// A running daemon keeps listening on the socket it was handed, so a
	// socket recreated for the new owner reaches it only after both restart.
	// Repeat all steps even when the files match: an earlier claim may have
	// failed after writing them but before reloading or restarting systemd.
	if err := c.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := c.systemctl(ctx, "stop", serviceUnit, socketUnit); err != nil {
		return err
	}
	return c.systemctl(ctx, "start", socketUnit, serviceUnit)
}

// invokingUID is the account sudo or pkexec was run from. Root is never an
// owner: the daemon refuses it like any other account but the owner.
func invokingUID(getenv func(string) string) (int, error) {
	raw := getenv("SUDO_UID")
	if raw == "" {
		raw = getenv("PKEXEC_UID")
	}
	if raw == "" {
		return 0, errors.New("claim records the account that ran sudo or pkexec, and found none: run sudo omihomo claim")
	}
	uid, err := strconv.ParseUint(raw, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("the invoking account's uid %q is not a number", raw)
	}
	if uid == 0 {
		return 0, errors.New("root cannot own omihomo: run sudo omihomo claim from the account that should")
	}
	return int(uid), nil
}

// writeDropIn makes the socket unit create the socket as uid's, and reports
// whether that changed anything.
func writeDropIn(path string, uid int) (bool, error) {
	want := []byte("# Written by omihomo claim: the socket belongs to the account that owns omihomo.\n" +
		"[Socket]\nSocketUser=" + strconv.Itoa(uid) + "\n")
	have, err := os.ReadFile(path)
	if err == nil && bytes.Equal(have, want) {
		return false, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".owner-*.conf")
	if err != nil {
		return false, fmt.Errorf("write the socket's owner: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(want); err != nil {
		tmp.Close()
		return false, fmt.Errorf("write the socket's owner: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return false, fmt.Errorf("write the socket's owner: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("write the socket's owner: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, fmt.Errorf("write the socket's owner: %w", err)
	}
	return true, nil
}

func runSystemctl(ctx context.Context, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %v: %w", args, err)
	}
	return nil
}
