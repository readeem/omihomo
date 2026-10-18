// Package settings keeps the owner's settings.json and rules.json in their
// config directory, where a dotfile manager can track them. The files are
// the source of truth: a change is applied to the daemon first and saved only
// once it runs, and the daemon keeps a copy so the proxy can start before the
// owner logs in. A missing file holds the defaults.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/readeem/omihomo/internal/protocol"
)

const (
	settingsFile = "settings.json"
	rulesFile    = "rules.json"
)

// Dir is $XDG_CONFIG_HOME/omihomo, or ~/.config/omihomo when that is unset
// or relative.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "omihomo"), nil
}

// Default is what a fresh install runs.
func Default() protocol.ApplyArgs {
	return protocol.ApplyArgs{Settings: protocol.Settings{Mode: protocol.ModeRule}, Rules: []protocol.UserRule{}}
}

// Load reads the files in dir. Only their shape is checked here; the daemon
// checks their values.
func Load(dir string) (protocol.ApplyArgs, error) {
	out := Default()
	if err := read(filepath.Join(dir, settingsFile), &out.Settings); err != nil {
		return protocol.ApplyArgs{}, err
	}
	if err := read(filepath.Join(dir, rulesFile), &out.Rules); err != nil {
		return protocol.ApplyArgs{}, err
	}
	if out.Rules == nil {
		out.Rules = []protocol.UserRule{}
	}
	return out, nil
}

func read(path string, v any) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	// Both files travel to the daemon in one message.
	b, err := io.ReadAll(io.LimitReader(f, protocol.MaxLine+1))
	if err != nil {
		return err
	}
	if len(b) > protocol.MaxLine {
		return fmt.Errorf("%s is over 1 MiB", path)
	}
	if err := protocol.Decode(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// File is one of the owner's files and what it holds.
type File struct {
	Name string
	Data []byte
}

// Encode is what Save writes for v: indented JSON without the settings at
// their zero value.
func Encode(v protocol.ApplyArgs) []File {
	rules := v.Rules
	if rules == nil {
		rules = []protocol.UserRule{}
	}
	return []File{{settingsFile, indent(v.Settings)}, {rulesFile, indent(rules)}}
}

func indent(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // plain structs always encode
	}
	return append(b, '\n')
}

// Save writes v to dir, replacing only the files whose content changes, so
// an unchanged file keeps its timestamp and triggers no watcher. A file that
// is a symlink, as a dotfile manager may leave, is written where it points.
func Save(dir string, v protocol.ApplyArgs) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	for _, f := range Encode(v) {
		path := filepath.Join(dir, f.Name)
		if target, err := filepath.EvalSymlinks(path); err == nil {
			path = target
		} else if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("resolve %s: %w", f.Name, err)
		}
		if info, err := os.Stat(path); err == nil {
			if info.Mode().Perm() != 0o600 {
				if err := os.Chmod(path, 0o600); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, f.Data) {
			continue
		}
		if err := replace(path, f.Data); err != nil {
			return fmt.Errorf("save %s: %w", f.Name, err)
		}
	}
	return nil
}

func replace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	return err
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// Lock serializes a load/apply/save transaction with other CLI clients. Close
// the returned directory to release it. File replacements retain that inode.
func Lock(dir string) (*os.File, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.IsDir() {
		err = errors.New("settings path is not a directory")
	}
	if err == nil {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
