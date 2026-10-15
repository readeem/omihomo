// Package state keeps what the daemon remembers across restarts: the owner's
// settings, the subscriptions with what was last downloaded for each, and
// whether the proxy was on. It all lives in one directory only the daemon's
// account can read, because subscription URLs carry access tokens.
//
// Every write replaces a whole file atomically, so a crash leaves either the
// old or the new version, never a mix.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/readeem/omihomo/internal/protocol"
)

// DefaultDir is the daemon's state directory.
const DefaultDir = "/var/lib/omihomo"

const (
	settingsFile      = "settings.json"
	subscriptionsFile = "subscriptions.json"
	snapshotDir       = "subscriptions"
)

// Settings are the owner's choices, the typed replacement for hand-edited
// configuration.
type Settings struct {
	Mode         string `json:"mode"`
	TUN          bool   `json:"tun"`
	TUNRedirect  bool   `json:"tun_redirect"`
	Tailscale    bool   `json:"tailscale"`
	TailnetProxy bool   `json:"tailnet_proxy"`
	PrimaryGroup string `json:"primary_group"`
	// Rules are the owner's own, matched before everything else.
	Rules []Rule `json:"rules"`
	// Running is whether the proxy was on, restored when the daemon starts.
	Running bool `json:"running"`
}

// Rule is one of the owner's rules.
type Rule struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Target string `json:"target"`
}

// DefaultSettings are the settings of a fresh install.
func DefaultSettings() Settings { return Settings{Mode: protocol.ModeRule, Rules: []Rule{}} }

// Subscription is one subscription's record. Times are unix milliseconds.
type Subscription struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	// UpdatedAt is the last download that was accepted.
	UpdatedAt int64 `json:"updated_at"`
	// IntervalSeconds is how often the server asks to be refreshed.
	IntervalSeconds int64  `json:"interval_seconds"`
	Upload          *int64 `json:"upload"`
	Download        *int64 `json:"download"`
	Total           *int64 `json:"total"`
	Expire          *int64 `json:"expire"`
	// LastAttempt is the last request to the server, whatever came of it.
	LastAttempt int64 `json:"last_attempt"`
	// RetryAt, when set, is the earliest next automatic request after a
	// failure.
	RetryAt int64 `json:"retry_at"`
	// HeldUntil is when the server's Retry-After runs out; no request,
	// automatic or asked for, goes out before.
	HeldUntil int64 `json:"held_until"`
	// LastError says why the last refresh failed; empty after a success.
	LastError string `json:"last_error"`
}

// Subscriptions is every subscription in the order they were added, and the
// id of the active one, or "".
type Subscriptions struct {
	Active string         `json:"active"`
	List   []Subscription `json:"subscriptions"`
}

// Snapshot is a subscription as last downloaded and accepted: its body and
// the bodies of its providers, by profile.Download key. It is parsed again
// on every load, so a tighter allowlist applies to what was stored before.
type Snapshot struct {
	Body      []byte            `json:"body"`
	Providers map[string][]byte `json:"providers"`
}

// Store reads and writes one state directory.
type Store struct{ dir string }

// Open uses dir, creating it if missing. It must belong to this process's
// account; it is made private to it if it is not already.
func Open(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the state directory %q is not absolute", dir)
	}
	for _, d := range []string{dir, filepath.Join(dir, snapshotDir)} {
		if err := privateDir(d); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s must be a directory owned by uid %d", dir, os.Geteuid())
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("make %s private: %w", dir, err)
		}
	}
	return nil
}

// Dir is the state directory.
func (s *Store) Dir() string { return s.dir }

// Settings returns the stored settings, or DefaultSettings if there are
// none yet.
func (s *Store) Settings() (Settings, error) {
	out := DefaultSettings()
	if err := s.read(settingsFile, &out); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Settings{}, err
	}
	if out.Rules == nil {
		out.Rules = []Rule{}
	}
	return out, nil
}

func (s *Store) SaveSettings(v Settings) error { return s.write(settingsFile, v) }

// Subscriptions returns the stored subscriptions, none if there are none
// yet.
func (s *Store) Subscriptions() (Subscriptions, error) {
	out := Subscriptions{List: []Subscription{}}
	if err := s.read(subscriptionsFile, &out); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Subscriptions{}, err
	}
	for _, sub := range out.List {
		if !validID.MatchString(sub.ID) {
			return Subscriptions{}, fmt.Errorf("%s: a subscription has an invalid id", subscriptionsFile)
		}
	}
	return out, nil
}

func (s *Store) SaveSubscriptions(v Subscriptions) error { return s.write(subscriptionsFile, v) }

var validID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewID returns a fresh subscription id.
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func snapshotFile(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("invalid subscription id %q", id)
	}
	return filepath.Join(snapshotDir, id+".json"), nil
}

// Snapshot returns what was last accepted for subscription id.
func (s *Store) Snapshot(id string) (Snapshot, error) {
	name, err := snapshotFile(id)
	if err != nil {
		return Snapshot{}, err
	}
	var out Snapshot
	if err := s.read(name, &out); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

func (s *Store) SaveSnapshot(id string, v Snapshot) error {
	name, err := snapshotFile(id)
	if err != nil {
		return err
	}
	return s.write(name, v)
}

// RemoveSnapshot deletes what was stored for id; a missing one is no error.
func (s *Store) RemoveSnapshot(id string) error {
	name, err := snapshotFile(id)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}

func (s *Store) read(name string, v any) error {
	path := filepath.Join(s.dir, name)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}

// write replaces name with v, fsynced before and after the rename.
func (s *Store) write(name string, v any) error {
	path := filepath.Join(s.dir, name)
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(name)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(append(b, '\n'))
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
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
