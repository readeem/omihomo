package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/bbolt"

	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/state"
)

const cached = `proxies:
- {name: Example, type: direct}
proxy-groups:
- {name: Proxy, type: select, proxies: [Example]}
rules: ["MATCH,Proxy"]
`

func fixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "old")
	os.MkdirAll(filepath.Join(source, "cache"), 0o700)
	write(t, source, "subscriptions.json", `[{"name":"Example","url":"https://example.com/sub","updated_at":"2026-10-10T12:00:00Z","update_interval_hours":24,"userinfo":{"total":1024}}]`)
	write(t, source, "cache/Example.yaml", cached)
	write(t, source, "active", "Example\n")
	write(t, source, "override.yaml", `config:
  mode: global
  tun: {enable: true, auto-redirect: true}
  external-controller: 0.0.0.0:9090
  secret: never-import-this
omihomo: {primary-group: Proxy, tailscale: true, tailnet-proxy: true}
rules:
  prepend: ["DOMAIN-SUFFIX,example.com,DIRECT"]
`)
	return source, filepath.Join(dir, "new")
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportPreservesSubscriptionsAndTypedSettings(t *testing.T) {
	source, target := fixture(t)
	m, err := Prepare(context.Background(), source, target, true, &mihomo.Core{}, exportFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := store.Subscriptions()
	if err != nil || len(subs.List) != 1 || subs.Active != subs.List[0].ID {
		t.Fatalf("subscriptions: %+v %v", subs, err)
	}
	sub := subs.List[0]
	if sub.Name != "Example" || sub.IntervalSeconds != 86400 || sub.UpdatedAt != 1791633600000 || sub.Total == nil || *sub.Total != 1024 {
		t.Fatalf("record: %+v", sub)
	}
	settings, err := store.Settings()
	if err != nil || !settings.Running || settings.Mode != "global" || !settings.TUN || !settings.TUNRedirect || !settings.Tailscale || !settings.TailnetProxy || settings.PrimaryGroup != "Proxy" || len(settings.Rules) != 1 {
		t.Fatalf("settings: %+v %v", settings, err)
	}
	bytes, _ := os.ReadFile(filepath.Join(target, "settings.json"))
	if strings.Contains(string(bytes), "never-import") || strings.Contains(string(bytes), "external-controller") {
		t.Fatal("raw overrides imported")
	}
	snap, err := store.Snapshot(sub.ID)
	if err != nil || string(snap.Body) != cached {
		t.Fatalf("snapshot: %v", err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o700 {
		t.Fatal(info.Mode())
	}
	if _, err := Prepare(context.Background(), source, target, false, &mihomo.Core{}, exportFixture); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "subscriptions.json")); err != nil {
		t.Fatal("source removed")
	}
}

func TestImportReadsRawWrapperAndCachedHTTPProvider(t *testing.T) {
	for _, kind := range []string{"file", "http"} {
		t.Run(kind, func(t *testing.T) {
			source, target := fixture(t)
			write(t, source, "providers/raw.yaml", cached)
			write(t, source, "cache/Example.yaml", "proxy-providers:\n  subscription: {type: "+kind+", path: ./providers/raw.yaml, url: https://example.com/provider}\nproxy-groups:\n- {name: Proxy, type: select, use: [subscription]}\nrules: [\"MATCH,Proxy\"]\n")
			if kind == "http" {
				write(t, source, "providers/raw.yaml", "proxies: [{name: Example, type: direct}]\n")
			}
			m, err := Prepare(context.Background(), source, target, false, &mihomo.Core{}, exportFixture)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			store, _ := state.Open(m.Dir)
			subs, _ := store.Subscriptions()
			snap, err := store.Snapshot(subs.List[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" && string(snap.Body) != cached {
				t.Fatal("wrapper not expanded")
			}
			if kind == "http" && len(snap.Providers) != 1 {
				t.Fatal("HTTP cache lost")
			}
		})
	}
}

func TestImportPreservesRequestDeadlines(t *testing.T) {
	source, target := fixture(t)
	name := fmt.Sprintf("requests/%x.json", sha256.Sum256([]byte("https://example.com/sub")))
	body := `{"attempted_at":1791633700,"automatic_after":4102444800,"manual_after":4102444700,"paused":false}`
	write(t, source, name, body)
	m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := store.Subscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if s := subs.List[0]; s.LastAttempt != 1791633700000 || s.RetryAt != 4102444800000 || s.HeldUntil != 4102444700000 {
		t.Fatalf("request limits lost: %+v", s)
	}
	if saved, err := os.ReadFile(filepath.Join(source, name)); err != nil || string(saved) != body {
		t.Fatalf("old request state changed: %v", err)
	}
}

func TestImportRefusesPausedOrInvalidRequestState(t *testing.T) {
	for _, body := range []string{
		`{"attempted_at":1,"automatic_after":2,"manual_after":2,"paused":true}`,
		`{"attempted_at":-1,"automatic_after":2,"manual_after":2,"paused":false}`,
		`{"attempted_at":9007199254740992,"automatic_after":2,"manual_after":2,"paused":false}`,
		`{"attempted_at":1.5,"automatic_after":2,"manual_after":2,"paused":false}`,
		`{"attempted_at":1,"automatic_after":null,"manual_after":2,"paused":false}`,
		`{"attempted_at":1,"automatic_after":2,"manual_after":2}`,
		`{`,
	} {
		t.Run(body, func(t *testing.T) {
			source, target := fixture(t)
			write(t, source, fmt.Sprintf("requests/%x.json", sha256.Sum256([]byte("https://example.com/sub"))), body)
			m, err := Prepare(t.Context(), source, target, true, &mihomo.Core{}, exportFixture)
			if m != nil {
				m.Close()
			}
			if err == nil {
				t.Fatal("cutover would resume a restricted subscription")
			}
			if strings.Contains(body, `"paused":true`) && !strings.Contains(err.Error(), "resume") {
				t.Fatalf("pause refusal has no recovery instruction: %v", err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial state published: %v", err)
			}
		})
	}
}

func TestImportPreservesUnclaimedCoreCacheOnRollback(t *testing.T) {
	for _, when := range []string{"before preparation", "during shutdown"} {
		t.Run(when, func(t *testing.T) {
			source, target := fixture(t)
			if when == "before preparation" {
				write(t, target, "core/cache.db", "old cache")
			}
			m, err := Prepare(t.Context(), source, target, true, &mihomo.Core{}, exportFixture)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if when == "during shutdown" {
				write(t, target, "core/cache.db", "old cache")
			}
			if err := m.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := m.Rollback(); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(target, "core/cache.db"))
			if err != nil || string(body) != "old cache" {
				t.Fatalf("previous cache was not restored: %v", err)
			}
		})
	}
}

func TestImportRejectsUnsafeStateBeforeCommit(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "fifo", "broken YAML", "invalid active", "invalid rule", "oversized", "existing state", "unknown core file", "symlinked core cache", "request symlink", "request fifo", "oversized request"} {
		t.Run(kind, func(t *testing.T) {
			source, target := fixture(t)
			switch kind {
			case "traversal":
				write(t, source, "subscriptions.json", `[{"name":"../outside","url":"https://example.com"}]`)
			case "symlink":
				os.Remove(filepath.Join(source, "cache/Example.yaml"))
				os.Symlink("/etc/passwd", filepath.Join(source, "cache/Example.yaml"))
			case "fifo":
				os.Remove(filepath.Join(source, "cache/Example.yaml"))
				syscall.Mkfifo(filepath.Join(source, "cache/Example.yaml"), 0o600)
			case "broken YAML":
				write(t, source, "override.yaml", "config: [")
			case "invalid active":
				write(t, source, "active", "missing")
			case "invalid rule":
				write(t, source, "override.yaml", "rules: {prepend: [\"IP-CIDR,999.0.0.0/8,DIRECT\"]}")
			case "oversized":
				os.Truncate(filepath.Join(source, "subscriptions.json"), (1<<20)+1)
			case "existing state":
				write(t, target, "settings.json", "{}")
			case "unknown core file":
				write(t, target, "core/unknown", "keep")
			case "symlinked core cache":
				os.MkdirAll(filepath.Join(target, "core"), 0o700)
				os.Symlink(filepath.Join(source, "subscriptions.json"), filepath.Join(target, "core/cache.db"))
			case "request symlink", "request fifo", "oversized request":
				name := fmt.Sprintf("requests/%x.json", sha256.Sum256([]byte("https://example.com/sub")))
				write(t, source, name, "")
				path := filepath.Join(source, name)
				if kind == "oversized request" {
					os.Truncate(path, 4097)
				} else {
					os.Remove(path)
					if kind == "request fifo" {
						syscall.Mkfifo(path, 0o600)
					} else {
						os.Symlink(filepath.Join(source, "subscriptions.json"), path)
					}
				}
			}
			m, err := Prepare(context.Background(), source, target, false, &mihomo.Core{}, exportFixture)
			if m != nil {
				m.Close()
			}
			if err == nil {
				t.Fatal("accepted unsafe import")
			}
			if _, err := os.Stat(filepath.Join(source, "subscriptions.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(target, "subscriptions.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial state published")
			}
		})
	}
}

func TestImportWaitsForOldStateWriter(t *testing.T) {
	source, target := fixture(t)
	f, err := os.OpenFile(filepath.Join(source, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if m, err := Prepare(context.Background(), source, target, false, &mihomo.Core{}, exportFixture); err == nil || m != nil {
		t.Fatal("import raced the old writer")
	}
}

func TestImportBoundsRefreshIntervals(t *testing.T) {
	for _, tc := range []struct {
		hours int64
		want  time.Duration
	}{
		{0, 12 * time.Hour}, {-1, 12 * time.Hour}, {24, 24 * time.Hour},
		{8760, 365 * 24 * time.Hour}, {8761, 365 * 24 * time.Hour},
		{2562048, 365 * 24 * time.Hour}, {2147483647, 365 * 24 * time.Hour},
	} {
		t.Run(fmt.Sprint(tc.hours), func(t *testing.T) {
			source, target := fixture(t)
			write(t, source, "subscriptions.json", fmt.Sprintf(`[{"name":"Example","url":"https://example.com/sub","update_interval_hours":%d}]`, tc.hours))
			m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			store, err := state.Open(m.Dir)
			if err != nil {
				t.Fatal(err)
			}
			subs, err := store.Subscriptions()
			if err != nil {
				t.Fatal(err)
			}
			got := time.Duration(subs.List[0].IntervalSeconds) * time.Second
			if got != tc.want {
				t.Fatalf("imported interval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestImportRejectsInvalidSelectionCaches(t *testing.T) {
	for _, kind := range []string{"empty", "malformed", "oversized", "many", "bad-name", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			source, target := fixture(t)
			switch kind {
			case "empty":
				write(t, source, "cache.db", "")
			case "malformed":
				write(t, source, "cache.db", "not a database")
			case "oversized":
				f, err := os.Create(filepath.Join(source, "cache.db"))
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate((16 << 20) + 1)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			default:
				db, err := bbolt.Open(filepath.Join(source, "cache.db"), 0o600, nil)
				if err != nil {
					t.Fatal(err)
				}
				err = db.Update(func(tx *bbolt.Tx) error {
					b, err := tx.CreateBucket([]byte("selected"))
					if err != nil {
						return err
					}
					if kind == "bad-name" {
						return b.Put([]byte("Proxy"), []byte("bad\nmember"))
					}
					for i := range 4097 {
						if err := b.Put(fmt.Appendf(nil, "Group %d", i), []byte("Member")); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if kind == "corrupt" {
					if err := os.Truncate(filepath.Join(source, "cache.db"), 8192); err != nil {
						t.Fatal(err)
					}
				}
			}
			m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
			if m != nil {
				defer m.Close()
			}
			if err == nil {
				t.Fatal("invalid selection cache accepted")
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed import changed target")
			}
		})
	}
}

func TestImportKeepsOnlyMembersOfImportedSelectGroups(t *testing.T) {
	source, target := fixture(t)
	write(t, source, "subscriptions.json", `[{"name":"Example","url":"https://example.com/sub"},{"name":"Other","url":"https://example.com/other"}]`)
	write(t, source, "cache/Other.yaml", `proxies: [{name: First, type: direct}, {name: Chosen, type: direct}]
proxy-groups:
- {name: Secondary, type: select, proxies: [First, Chosen]}
- {name: Proxy, type: select, proxies: [First]}
rules: [MATCH,Secondary]
`)
	write(t, source, "cache/Example.yaml", `proxies: [{name: First, type: direct}, {name: Chosen, type: direct}]
proxy-groups:
- {name: Proxy, type: select, proxies: [First, Chosen]}
- {name: Stale, type: select, proxies: [First]}
- {name: Auto, type: url-test, proxies: [First, Chosen], url: https://example.com, interval: 3600}
rules: [MATCH,Proxy]
`)
	db, err := bbolt.Open(filepath.Join(source, "cache.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucket([]byte("selected"))
		if err != nil {
			return err
		}
		for g, m := range map[string]string{"Proxy": "Chosen", "Secondary": "Chosen", "Stale": "Chosen", "Auto": "Chosen", "Deleted": "Chosen"} {
			if err := b.Put([]byte(g), []byte(m)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	db, err = bbolt.Open(filepath.Join(m.Dir, "core/cache.db"), 0o600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("selected"))
		if b == nil || b.Stats().KeyN != 2 || string(b.Get([]byte("Proxy"))) != "Chosen" || string(b.Get([]byte("Secondary"))) != "Chosen" {
			t.Fatal("prepared cache did not retain only the imported select groups' members")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if m.Settings.Running {
		t.Fatal("selection import started proxy")
	}
}

// A site list the old core cached is staged before the old core stops, so
// the new one starts without downloading it.
func TestImportStagesUsableLegacyGeodata(t *testing.T) {
	source, target := fixture(t)
	write(t, source, "cache/Example.yaml", strings.Replace(cached, `rules: [`, `rules: ["GEOSITE,cn,DIRECT", `, 1))
	// A site list whose CN category holds example.cn.
	write(t, source, "GeoSite.dat", "\x0a\x14\x0a\x02CN\x12\x0e\x08\x02\x12\x0aexample.cn")
	write(t, source, "geoip.metadb", "not a database")
	m, err := Prepare(t.Context(), source, target, true, &mihomo.Core{}, exportFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := os.Stat(filepath.Join(m.Dir, "core", "GeoSite.dat")); err != nil {
		t.Fatalf("site list not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "core", "geoip.metadb")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an invalid database was staged: %v", err)
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--internal-export-selections" {
		if err := ExportLegacySelections(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func exportFixture(ctx context.Context, body []byte) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "--internal-export-selections")
	cmd.Stdin = bytes.NewReader(body)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var selected map[string]string
	err = json.Unmarshal(output, &selected)
	return selected, err
}

func TestImportUsesHashedHTTPProviderCaches(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			source, target := fixture(t)
			proxyURL, ruleURL := "https://127.0.0.1:1/proxies", "https://127.0.0.1:1/rules"
			path := ""
			if explicit {
				path = ", path: ./missing.yaml"
			}
			write(t, source, "cache/Example.yaml", fmt.Sprintf(`proxy-providers:
  subscription: {type: http, url: %s%s}
proxy-groups:
- {name: Proxy, type: select, use: [subscription]}
rule-providers:
  blocked: {type: http, behavior: domain, url: %s%s}
rules: ["RULE-SET,blocked,REJECT", "MATCH,Proxy"]
`, proxyURL, path, ruleURL, path))
			write(t, source, "proxies/7e70227b15519a5b7b69821848fe1be5", "proxies: [{name: Example, type: direct}]")
			write(t, source, "rules/7431d192c67ac40108a40e67949fa761", "payload: [+.blocked.example]")
			m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			store, _ := state.Open(m.Dir)
			subs, _ := store.Subscriptions()
			snap, err := store.Snapshot(subs.Active)
			if err != nil || len(snap.Providers) != 2 {
				t.Fatalf("cached providers lost: %+v %v", snap, err)
			}
		})
	}
}

func TestImportRejectsUnsafeHashedProviderCaches(t *testing.T) {
	for _, kind := range []string{"file symlink", "directory symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			source, target := fixture(t)
			write(t, source, "cache/Example.yaml", `proxy-providers:
  subscription: {type: http, url: https://127.0.0.1:1/proxies}
proxy-groups:
- {name: Proxy, type: select, use: [subscription]}
rules: ["MATCH,Proxy"]
`)
			const name = "7e70227b15519a5b7b69821848fe1be5"
			outside := t.TempDir()
			write(t, outside, name, "proxies: [{name: Example, type: direct}]")
			path := filepath.Join(source, "proxies", name)
			var err error
			switch kind {
			case "directory symlink":
				err = os.Symlink(outside, filepath.Dir(path))
			case "file symlink":
				if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
					err = os.Symlink(filepath.Join(outside, name), path)
				}
			case "oversized":
				write(t, source, "proxies/"+name, "")
				err = os.Truncate(path, 64<<20)
			}
			if err != nil {
				t.Fatal(err)
			}
			m, err := Prepare(t.Context(), source, target, false, &mihomo.Core{}, exportFixture)
			if m != nil {
				m.Close()
			}
			if err == nil {
				t.Fatal("unsafe provider cache imported")
			}
			if strings.Contains(err.Error(), "download") {
				t.Fatalf("unsafe cache treated as missing: %v", err)
			}
		})
	}
}
