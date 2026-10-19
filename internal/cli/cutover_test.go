package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/owner"
	"github.com/readeem/omihomo/internal/settings"
)

// oldData writes an old install's data directory with one subscription and
// override, if any.
func oldData(t *testing.T, c *cutover, override string) {
	t.Helper()
	files := map[string]string{
		"subscriptions.json": `[{"name":"Example","url":"https://example.com/sub"}]`,
		"active":             "Example",
		"cache/Example.yaml": "proxies: [{name: Example, type: direct}]\nproxy-groups: [{name: Proxy, type: select, proxies: [Example]}]\nrules: [MATCH,Proxy]\n",
	}
	if override != "" {
		files["override.yaml"] = override
	}
	if err := os.MkdirAll(c.state, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(c.data, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCutoverImportsAfterUnclaimedCoreShutdown(t *testing.T) {
	c, _ := cutoverFixture(t)
	oldData(t, c, "")
	engine, err := mihomo.New(filepath.Join(c.state, "core"), nil)
	if err != nil {
		t.Fatal(err)
	}
	systemctl := c.systemctl
	c.systemctl = func(ctx context.Context, args ...string) error {
		if slices.Equal(args, []string{"stop", serviceUnit, socketUnit}) {
			if err := engine.Stop(ctx); err != nil {
				return err
			}
			entries, err := os.ReadDir(filepath.Join(c.state, "core"))
			if err != nil || len(entries) == 0 {
				t.Fatalf("shutdown did not create a core cache: %v", err)
			}
			for _, name := range []string{"settings.json", "subscriptions.json"} {
				if _, err := os.Lstat(filepath.Join(c.state, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected daemon state %s: %v", name, err)
				}
			}
		}
		return systemctl(ctx, args...)
	}
	if err := c.run(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func cutoverFixture(t *testing.T) (*cutover, *[]string) {
	t.Helper()
	base := t.TempDir()
	machineRoot := filepath.Join(base, "system")
	home := filepath.Join(base, "home")
	config := filepath.Join(home, ".config")
	for _, dir := range []string{machineRoot, home, config} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(machineRoot, "usr/bin/mihomo"), "fake old core")
	if err := os.Chmod(filepath.Join(machineRoot, "usr/bin/mihomo"), os.ModeSetuid|0o750); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(machineRoot, "usr/share/libalpm/hooks/omihomo-permissions.hook"), "old permissions hook")
	for _, unit := range oldUnits {
		write(filepath.Join(config, "systemd/user", unit), "old unit")
	}
	os.MkdirAll(filepath.Join(config, "systemd/user/default.target.wants"), 0o700)
	os.Symlink("../omihomo.service", filepath.Join(config, "systemd/user/default.target.wants/omihomo.service"))
	os.MkdirAll(filepath.Join(home, ".local/bin"), 0o700)
	os.Symlink("/old/plugin/bin/omihomo", filepath.Join(home, ".local/bin/omihomo"))
	root, _ := os.OpenRoot(machineRoot)
	h, _ := os.OpenRoot(home)
	cfg, _ := os.OpenRoot(config)
	t.Cleanup(func() { root.Close(); h.Close(); cfg.Close() })
	calls := []string{}
	c := &cutover{root: root, home: h, config: cfg, data: filepath.Join(home, ".local/share/omihomo"), state: filepath.Join(machineRoot, "var/lib/omihomo"),
		uid: os.Getuid(), gid: os.Getgid(),
		userctl: func(_ context.Context, args ...string) (bool, error) {
			calls = append(calls, "user "+strings.Join(args, " "))
			return true, nil
		},
		systemctl: func(_ context.Context, args ...string) error {
			calls = append(calls, "system "+strings.Join(args, " "))
			return nil
		},
		claim: func(context.Context) error { calls = append(calls, "claim"); return nil },
		apply: func(context.Context) error { calls = append(calls, "apply"); return nil },
		check: func(_ context.Context, running bool) error {
			if !running {
				t.Error("old running state lost")
			}
			calls = append(calls, "check")
			return nil
		},
	}
	return c, &calls
}

func TestCutoverCleansOldRuntimeOnlyAfterPreparation(t *testing.T) {
	c, calls := cutoverFixture(t)
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, unit := range oldUnits {
		if _, err := c.config.Lstat("systemd/user/" + unit); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unit %s: %v", unit, err)
		}
	}
	if _, err := c.home.Lstat(".local/bin/omihomo"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old CLI: %v", err)
	}
	if _, err := c.root.Lstat("usr/share/libalpm/hooks/omihomo-permissions.hook"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old hook: %v", err)
	}
	info, _ := c.root.Stat("usr/bin/mihomo")
	if info.Mode().Perm() != 0o755 || info.Mode()&os.ModeSetuid != 0 {
		t.Fatal(info.Mode())
	}
	if (*calls)[len(*calls)-1] != "check" {
		t.Fatal(*calls)
	}
}

func TestInstallationRetryKeepsTheRecordedOwner(t *testing.T) {
	for _, recorded := range []int{-1, os.Getuid(), os.Getuid() + 1} {
		t.Run(fmt.Sprint(recorded), func(t *testing.T) {
			c, calls := cutoverFixture(t)
			c.keepOwner = true
			if recorded >= 0 {
				if err := os.MkdirAll(c.state, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := owner.Store(filepath.Join(c.state, "owner"), recorded); err != nil {
					t.Fatal(err)
				}
			}
			err := c.run(t.Context())
			if recorded > c.uid {
				if err == nil || len(*calls) != 0 {
					t.Fatalf("another owner's installation was changed: %v, %v", err, *calls)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCutoverRestoresOldRuntimeAfterFailedStep(t *testing.T) {
	for _, step := range []string{"user stop", "user daemon-reload", "claim", "check"} {
		t.Run(step, func(t *testing.T) {
			c, calls := cutoverFixture(t)
			initial, _ := c.root.Stat("usr/bin/mihomo")
			fail := errors.New("injected failure")
			userctl := c.userctl
			c.userctl = func(ctx context.Context, args ...string) (bool, error) {
				if "user "+args[0] == step {
					return false, fail
				}
				return userctl(ctx, args...)
			}
			if step == "claim" {
				c.claim = func(context.Context) error { return fail }
			}
			if step == "check" {
				c.check = func(context.Context, bool) error { return fail }
			}
			if err := c.run(context.Background()); !errors.Is(err, fail) {
				t.Fatalf("cutover: %v", err)
			}
			for _, unit := range oldUnits {
				if _, err := c.config.Lstat("systemd/user/" + unit); err != nil {
					t.Fatalf("old unit not restored: %v", err)
				}
			}
			if _, err := c.home.Lstat(".local/bin/omihomo"); err != nil {
				t.Fatal("old CLI not restored")
			}
			if _, err := c.root.Lstat("usr/share/libalpm/hooks/omihomo-permissions.hook"); err != nil {
				t.Fatal("hook not restored")
			}
			info, _ := c.root.Stat("usr/bin/mihomo")
			if info.Mode() != initial.Mode() {
				t.Fatalf("old permissions: %v", info.Mode())
			}
			if !slices.ContainsFunc(*calls, func(call string) bool { return strings.HasPrefix(call, "user start ") }) {
				t.Fatal("old runtime not restarted")
			}
		})
	}
}

func TestCutoverDoesNotAlterUnrelatedMihomoInstall(t *testing.T) {
	c, _ := cutoverFixture(t)
	c.config = nil
	c.root.Remove("usr/share/libalpm/hooks/omihomo-permissions.hook")
	info, _ := c.root.Stat("usr/bin/mihomo")
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := c.root.Stat("usr/bin/mihomo")
	if info.Mode() != after.Mode() {
		t.Fatal("unrelated install modified")
	}
}

func TestCutoverRemovesOldWrapperAndRefusesUnknownLauncher(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "wrapper", false: "unknown"}[known], func(t *testing.T) {
			c, calls := cutoverFixture(t)
			c.home.Remove(".local/bin/omihomo")
			body := []byte("custom launcher")
			if known {
				body = []byte("#!/usr/bin/env bash\nexec \"$OMIHOMO_ROOT/bin/omihomo\" \"$@\"\n")
			}
			f, err := c.home.OpenFile(".local/bin/omihomo", os.O_CREATE|os.O_WRONLY, 0o700)
			if err != nil {
				t.Fatal(err)
			}
			f.Write(body)
			f.Close()
			err = c.run(context.Background())
			if known {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("unknown launcher removed")
				}
				if slices.Contains(*calls, "system stop omihomo.service omihomo.socket") {
					t.Fatal("old runtime changed before preflight failed")
				}
			}
		})
	}
}

func TestCutoverGivesTheOwnerTheImportedSettingsFiles(t *testing.T) {
	c, calls := cutoverFixture(t)
	oldData(t, c, "config: {mode: global, tun: {enable: true}}\nrules: {prepend: [\"DOMAIN-SUFFIX,example.com,DIRECT\"]}\n")
	if err := c.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"omihomo/settings.json": "{\n  \"mode\": \"global\",\n  \"tun\": true\n}\n",
		"omihomo/rules.json": "[\n  {\n    \"type\": \"DOMAIN-SUFFIX\",\n    \"value\": \"example.com\",\n" +
			"    \"target\": \"DIRECT\"\n  }\n]\n",
	}
	for name, content := range want {
		b, err := c.config.ReadFile(name)
		if err != nil || string(b) != content {
			t.Errorf("%s = %q, %v", name, b, err)
		}
	}
	if !slices.Equal((*calls)[len(*calls)-3:], []string{"claim", "apply", "check"}) {
		t.Errorf("calls end %v, want the files applied before the check", *calls)
	}
}

// A dotfile manager may have placed them before the claim.
func TestCutoverKeepsSettingsFilesTheOwnerHas(t *testing.T) {
	c, calls := cutoverFixture(t)
	oldData(t, c, "config: {mode: global}\n")
	if err := c.config.Mkdir("omihomo", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.config.WriteFile("omihomo/settings.json", []byte(`{"mode":"direct"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b, _ := c.config.ReadFile("omihomo/settings.json"); string(b) != `{"mode":"direct"}` {
		t.Errorf("settings.json = %q", b)
	}
	if _, err := c.config.Lstat("omihomo/rules.json"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rules.json was written beside the owner's settings: %v", err)
	}
	if !slices.Contains(*calls, "apply") {
		t.Errorf("calls %v, want the owner's files applied", *calls)
	}
}

func TestAFailedCutoverRemovesTheSettingsFilesItWrote(t *testing.T) {
	c, _ := cutoverFixture(t)
	oldData(t, c, "")
	fail := errors.New("injected failure")
	c.check = func(context.Context, bool) error { return fail }
	if err := c.run(t.Context()); !errors.Is(err, fail) {
		t.Fatalf("cutover: %v", err)
	}
	if _, err := c.config.Lstat("omihomo"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the settings directory outlived the rollback: %v", err)
	}
}

func TestCutoverPreservesLinkedSettingsDirectory(t *testing.T) {
	c, calls := cutoverFixture(t)
	oldData(t, c, "config: {mode: global}")
	linked := t.TempDir()
	want := settings.Default()
	want.Settings.Mode = "direct"
	if err := settings.Save(linked, want); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linked, filepath.Join(c.config.Name(), "omihomo")); err != nil {
		t.Fatal(err)
	}
	c.apply = func(context.Context) error {
		got, err := settings.Load(filepath.Join(c.config.Name(), "omihomo"))
		if err != nil {
			return err
		}
		if got.Settings.Mode != want.Settings.Mode {
			t.Fatalf("existing settings replaced: %+v", got)
		}
		*calls = append(*calls, "apply")
		return nil
	}
	if err := c.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*calls, "apply") {
		t.Fatalf("owner's settings not applied: %v", *calls)
	}
	if info, err := c.config.Lstat("omihomo"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings directory link replaced: %v", err)
	}
}
