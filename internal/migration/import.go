// Package migration imports an old installation once, without changing its source.
package migration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/profile"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/state"
)

type record struct {
	Name    string                                           `json:"name"`
	URL     string                                           `json:"url"`
	Updated string                                           `json:"updated_at"`
	Hours   int64                                            `json:"update_interval_hours"`
	Usage   struct{ Upload, Download, Total, Expire *int64 } `json:"userinfo"`
}

// Import is a validated replacement of an empty daemon state directory.
type Import struct {
	Dir       string
	Settings  state.Settings
	target    string
	backup    string
	installed bool
	lock      *os.File
}

type Core interface {
	ParseLinks(data []byte) ([]map[string]any, error)
	ImportSelections(home string, p core.Profile, selections map[string]string) error
}

// Prepare stages subscriptions and typed settings. Existing daemon state is never
// overwritten. A missing old subscriptions file means there is nothing to import.
func Prepare(ctx context.Context, source, target string, running bool, engine Core, export Exporter) (*Import, error) {
	if _, err := os.Lstat(filepath.Join(target, "subscriptions.json")); err == nil {
		return nil, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	root, err := os.OpenRoot(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	lock, lockErr := root.OpenFile(".lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if lockErr != nil && !errors.Is(lockErr, fs.ErrNotExist) {
		return nil, lockErr
	}
	keepLock := false
	defer func() {
		if lock != nil && !keepLock {
			lock.Close()
		}
	}()
	if lock != nil {
		info, err := lock.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("old state lock is not a regular file")
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return nil, errors.New("the old install is updating; retry claim after it finishes")
		}
	}

	body, err := read(root, "subscriptions.json", 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []record
	if json.Unmarshal(body, &records) != nil || len(records) > 256 {
		return nil, errors.New("old subscription list is invalid or exceeds 256 records")
	}
	if err := emptyState(target); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(target), ".omihomo-import-*")
	if err != nil {
		return nil, err
	}
	staged := &Import{Dir: dir, target: target, lock: lock}
	ok := false
	defer func() {
		if !ok {
			staged.Close()
		}
	}()
	store, err := state.Open(dir)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(root)
	if err != nil {
		return nil, err
	}
	settings.Running = running
	active, err := optional(root, "active", 1024)
	if err != nil {
		return nil, err
	}
	activeName := strings.TrimSpace(string(active))
	var activeProfile core.Profile
	subs := state.Subscriptions{List: []state.Subscription{}}
	seen := map[string]bool{}
	left := int64(fetch.MaxBytes)
	for i, old := range records {
		if !safeName(old.Name) || seen[old.Name] {
			return nil, fmt.Errorf("old subscription %d has an invalid or duplicate name", i+1)
		}
		seen[old.Name] = true
		if err := fetch.CheckURL(old.URL); err != nil {
			return nil, fmt.Errorf("old subscription %d needs an HTTPS URL", i+1)
		}
		sub := state.Subscription{ID: state.NewID(), Name: old.Name, URL: old.URL,
			Upload: old.Usage.Upload, Download: old.Usage.Download, Total: old.Usage.Total, Expire: old.Usage.Expire}
		if err := requestState(root, &sub); err != nil {
			return nil, fmt.Errorf("old subscription %d request state: %w", i+1, err)
		}
		cached, err := read(root, "cache/"+old.Name+".yaml", left)
		if err != nil {
			return nil, fmt.Errorf("read old subscription %d cache: %w", i+1, err)
		}
		left -= int64(len(cached))
		snap, p, err := snapshot(ctx, root, cached, &left, engine.ParseLinks)
		if err != nil {
			return nil, fmt.Errorf("old subscription %d cannot be imported: %w", i+1, err)
		}
		sub.IntervalSeconds = int64(profile.Interval(http.Header{
			"Profile-Update-Interval": {strconv.FormatInt(old.Hours, 10)},
		}) / time.Second)
		if t, err := time.Parse(time.RFC3339, old.Updated); err == nil {
			sub.UpdatedAt = t.UnixMilli()
		}
		if err := store.SaveSnapshot(sub.ID, snap); err != nil {
			return nil, err
		}
		subs.List = append(subs.List, sub)
		if old.Name == activeName {
			subs.Active = sub.ID
			activeProfile = p
		}
	}
	if activeName != "" && subs.Active == "" {
		return nil, errors.New("the old active subscription is missing from its list")
	}
	if settings.Running && subs.Active == "" {
		return nil, errors.New("the running old service has no active subscription")
	}
	selections, err := exportSelections(ctx, root, export)
	if err != nil {
		return nil, fmt.Errorf("read old selections: %w", err)
	}
	if err := engine.ImportSelections(filepath.Join(dir, "core"), activeProfile, core.FilterSelections(activeProfile, selections)); err != nil {
		return nil, fmt.Errorf("prepare imported selections: %w", err)
	}
	if err := store.SaveSettings(settings); err != nil {
		return nil, err
	}
	if err := store.SaveSubscriptions(subs); err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(filepath.Join(target, "owner")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "owner"), data, 0o600); err != nil {
			return nil, err
		}
	}
	staged.Settings = settings
	ok = true
	keepLock = true
	return staged, nil
}

func requestState(root *os.Root, sub *state.Subscription) error {
	name := fmt.Sprintf("requests/%x.json", sha256.Sum256([]byte(sub.URL)))
	body, err := optional(root, name, 4096)
	if err != nil {
		return err
	}
	if body == nil {
		return nil
	}
	var old struct {
		AttemptedAt    *int64 `json:"attempted_at"`
		AutomaticAfter *int64 `json:"automatic_after"`
		ManualAfter    *int64 `json:"manual_after"`
		Paused         *bool  `json:"paused"`
	}
	if err := json.Unmarshal(body, &old); err != nil || old.Paused == nil {
		return errors.New("invalid saved request limits")
	}
	for _, deadline := range []*int64{old.AttemptedAt, old.AutomaticAfter, old.ManualAfter} {
		// The old CLI accepted only JSON-safe integer seconds; this also keeps
		// conversion to milliseconds within int64.
		if deadline == nil || *deadline < 0 || *deadline > 9007199254740991 {
			return errors.New("invalid saved request limits")
		}
	}
	if *old.Paused {
		return errors.New("requests are paused; resolve access and use the old CLI's sub resume command, or remove the subscription before upgrading")
	}
	sub.LastAttempt = *old.AttemptedAt * 1000
	sub.HeldUntil = *old.ManualAfter * 1000
	sub.RetryAt = max(*old.AutomaticAfter*1000, sub.HeldUntil)
	return nil
}

func emptyState(target string) error {
	entries, err := os.ReadDir(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "owner" && entry.Type().IsRegular() {
			continue
		}
		if (entry.Name() == "core" || entry.Name() == "subscriptions") && entry.IsDir() {
			children, err := os.ReadDir(filepath.Join(target, entry.Name()))
			if err != nil {
				return err
			}
			if len(children) == 0 || (entry.Name() == "core" && len(children) == 1 && children[0].Name() == "cache.db" && children[0].Type().IsRegular()) {
				continue
			}
		}
		return errors.New("daemon state already exists; refusing to replace it with an old install")
	}
	return nil
}

// Commit installs the staged directory, keeping the previous directory for rollback.
func (m *Import) Commit() error {
	if err := emptyState(m.target); err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(m.target), ".omihomo-before-import-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(m.target, backup); err == nil {
		m.backup = backup
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(m.Dir, m.target); err != nil {
		if m.backup != "" {
			return errors.Join(err, os.Rename(m.backup, m.target))
		}
		return err
	}
	m.installed = true
	return nil
}

// Rollback restores the previous daemon state after an unsuccessful cutover.
func (m *Import) Rollback() error {
	if !m.installed {
		return nil
	}
	if err := os.Rename(m.target, m.Dir); err != nil {
		return err
	}
	m.installed = false
	if m.backup != "" {
		return os.Rename(m.backup, m.target)
	}
	return nil
}

// Finish discards the previous empty state after a successful cutover.
func (m *Import) Finish() {
	if m.backup != "" {
		os.RemoveAll(m.backup)
		m.backup = ""
	}
}

// Close removes the staging directory. A backup survives failed rollback.
func (m *Import) Close() {
	if m.lock != nil {
		m.lock.Close()
	}
	os.RemoveAll(m.Dir)
}

func read(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(name), err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("old state must contain regular files")
	}
	if limit < 0 || info.Size() > limit {
		return nil, errors.New("old state exceeds the import size limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("old state exceeds the import size limit")
	}
	return b, err
}

func optional(root *os.Root, name string, limit int64) ([]byte, error) {
	b, err := read(root, name, limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

func safeName(name string) bool {
	return name != "" && len(name) <= 256 && !strings.HasPrefix(name, ".") && strings.TrimSpace(name) == name &&
		!strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl)
}

func snapshot(ctx context.Context, root *os.Root, body []byte, left *int64, links profile.Links) (state.Snapshot, core.Profile, error) {
	type provider struct {
		Type string `yaml:"type"`
		Path string `yaml:"path"`
	}
	var doc struct {
		Providers map[string]provider `yaml:"proxy-providers"`
		Rules     map[string]provider `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return state.Snapshot{}, core.Profile{}, errors.New("cached YAML is malformed")
	}
	for _, p := range doc.Providers {
		if p.Type == "file" {
			if len(doc.Providers) != 1 || !strings.HasPrefix(p.Path, "./providers/") || !filepath.IsLocal(p.Path) {
				return state.Snapshot{}, core.Profile{}, errors.New("old file provider is not a subscription cache")
			}
			raw, err := read(root, p.Path, *left)
			if err != nil {
				return state.Snapshot{}, core.Profile{}, err
			}
			*left -= int64(len(raw))
			body = raw
		}
	}
	parsed, err := profile.Read(body, links)
	if err != nil {
		return state.Snapshot{}, core.Profile{}, errors.New("cached subscription could not be parsed")
	}
	bodies := map[string][]byte{}
	downloads := parsed.Downloads()
	if len(downloads) > 64 {
		return state.Snapshot{}, core.Profile{}, errors.New("cached subscription has too many providers")
	}
	for _, p := range downloads {
		def := doc.Providers[p.Name]
		if p.Kind == profile.RuleProvider {
			def = doc.Rules[p.Name]
		}
		var (
			data    []byte
			readErr error
		)
		if def.Path != "" && filepath.IsLocal(def.Path) {
			data, readErr = read(root, def.Path, *left)
		} else {
			readErr = fs.ErrNotExist
		}
		if errors.Is(readErr, fs.ErrNotExist) {
			if *left <= 0 {
				return state.Snapshot{}, core.Profile{}, errors.New("old state exceeds the import size limit")
			}
			response, err := fetch.New(nil).Get(ctx, fetch.Request{URL: p.URL, MaxBytes: *left})
			if err != nil {
				return state.Snapshot{}, core.Profile{}, fmt.Errorf("provider %q has no usable cache and could not be downloaded: %w", p.Name, err)
			}
			data = response.Body
		} else if readErr != nil {
			return state.Snapshot{}, core.Profile{}, readErr
		}
		*left -= int64(len(data))
		bodies[p.Key()] = data
	}
	p, _, err := parsed.Build(bodies)
	if err != nil {
		return state.Snapshot{}, core.Profile{}, errors.New("cached subscription has no usable profile")
	}
	return state.Snapshot{Body: body, Providers: bodies}, p, nil
}

func readSettings(root *os.Root) (state.Settings, error) {
	s := state.DefaultSettings()
	b, err := optional(root, "override.yaml", 1<<20)
	if err != nil {
		return s, err
	}
	var old struct {
		Config struct {
			Mode string `yaml:"mode"`
			TUN  struct {
				Enable   bool `yaml:"enable"`
				Redirect bool `yaml:"auto-redirect"`
			} `yaml:"tun"`
		} `yaml:"config"`
		Omihomo struct {
			Group     string `yaml:"primary-group"`
			Tailscale bool   `yaml:"tailscale"`
			Tailnet   bool   `yaml:"tailnet-proxy"`
		} `yaml:"omihomo"`
		Rules struct {
			Prepend []string `yaml:"prepend"`
			Append  []string `yaml:"append"`
			Filter  []string `yaml:"filter"`
		} `yaml:"rules"`
	}
	if yaml.Unmarshal(b, &old) != nil {
		return s, errors.New("old override YAML is malformed")
	}
	if old.Config.Mode != "" {
		s.Mode = old.Config.Mode
	}
	if s.Mode != "rule" && s.Mode != "global" && s.Mode != "direct" {
		return s, errors.New("old override has an invalid mode")
	}
	if len(old.Omihomo.Group) > 256 || strings.ContainsFunc(old.Omihomo.Group, unicode.IsControl) {
		return s, errors.New("old primary group is invalid")
	}
	s.TUN, s.TUNRedirect = old.Config.TUN.Enable, old.Config.TUN.Redirect
	s.PrimaryGroup, s.Tailscale, s.TailnetProxy = old.Omihomo.Group, old.Omihomo.Tailscale, old.Omihomo.Tailnet
	if len(old.Rules.Append) > 0 || len(old.Rules.Filter) > 0 {
		return s, errors.New("raw append/filter rules are unsupported; convert them to typed prepend rules before cutover")
	}
	for _, line := range old.Rules.Prepend {
		r, err := profile.ParseRule(line)
		if err != nil {
			return s, errors.New("old override has a malformed rule")
		}
		switch r.Type {
		case "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "PROCESS-NAME":
		default:
			return s, errors.New("old override has a raw rule; convert it to a typed rule before cutover")
		}
		if r.Target == "" || r.Value == "" || strings.TrimSpace(r.Target) != r.Target || strings.TrimSpace(r.Value) != r.Value || len(r.Params) != 0 || len(r.Value) > 256 || len(r.Target) > 256 || strings.ContainsFunc(r.Value+r.Target, unicode.IsControl) {
			return s, errors.New("old override rule cannot be represented as a typed rule")
		}
		if r.Type == "IP-CIDR" {
			if _, err := netip.ParsePrefix(r.Value); err != nil {
				return s, errors.New("old rule has an invalid CIDR")
			}
		}
		if (r.Type == "DOMAIN-SUFFIX" || r.Type == "DOMAIN-KEYWORD") && strings.ContainsAny(r.Value, " /\\") {
			return s, errors.New("old rule has an invalid domain value")
		}
		s.Rules = append(s.Rules, protocol.UserRule{Type: r.Type, Value: r.Value, Target: r.Target})
	}
	if len(s.Rules) > 1000 {
		return s, errors.New("old override has too many user rules")
	}
	return s, nil
}
