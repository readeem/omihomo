package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Pacman struct{}

func (Pacman) Installed(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/pacman", "-Q", "omihomo-bin")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(string(out)) == "" &&
			strings.TrimSpace(string(exit.Stderr)) == "error: package 'omihomo-bin' was not found" {
			return "", nil
		}
		return "", errors.New("cannot read installed package metadata")
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "omihomo-bin" {
		return "", errors.New("unexpected installed package metadata")
	}
	return fields[1], nil
}

func (Pacman) Install(ctx context.Context, path, version string) error {
	if os.Geteuid() != 0 {
		return errors.New("package installation requires authentication")
	}
	inspect := exec.CommandContext(ctx, "/usr/bin/pacman", "-Qip", "--", path)
	inspect.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	metadata, err := inspect.Output()
	if err != nil {
		return errors.New("cannot inspect the verified package")
	}
	identity := make(map[string]string)
	for line := range strings.SplitSeq(string(metadata), "\n") {
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || (name != "Name" && name != "Version" && name != "Architecture") {
			continue
		}
		if _, exists := identity[name]; exists {
			return errors.New("ambiguous package identity")
		}
		identity[name] = strings.TrimSpace(value)
	}
	if identity["Name"] != "omihomo-bin" || identity["Version"] != version || identity["Architecture"] != "x86_64" {
		return errors.New("package identity does not match the signed release")
	}
	// A download deadline must never terminate pacman during a transaction.
	cmd := exec.Command("/usr/bin/pacman", "-U", "--needed", "--", path)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=/root", "TERM=" + os.Getenv("TERM")}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pacman did not install the package: %w", err)
	}
	return nil
}
