package settings_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/settings"
)

func TestMissingFilesHoldTheDefaults(t *testing.T) {
	got, err := settings.Load(filepath.Join(t.TempDir(), "omihomo"))
	if err != nil || !reflect.DeepEqual(got, settings.Default()) {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}

func TestSavedFilesLeaveOutZeroSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "omihomo")
	v := protocol.ApplyArgs{Settings: protocol.Settings{Mode: "global", TUN: true},
		Rules: []protocol.UserRule{{Type: "DOMAIN-SUFFIX", Value: "example.com", Target: "DIRECT"}}}
	if err := settings.Save(dir, v); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"settings.json": "{\n  \"mode\": \"global\",\n  \"tun\": true\n}\n",
		"rules.json": "[\n  {\n    \"type\": \"DOMAIN-SUFFIX\",\n    \"value\": \"example.com\",\n" +
			"    \"target\": \"DIRECT\"\n  }\n]\n",
	}
	for name, content := range want {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != content {
			t.Errorf("%s = %q, %v; want %q", name, b, err, content)
		}
		if info, _ := os.Stat(filepath.Join(dir, name)); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v", name, info.Mode())
		}
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v", info.Mode())
	}
	if got, err := settings.Load(dir); err != nil || !reflect.DeepEqual(got, v) {
		t.Errorf("Load = %+v, %v", got, err)
	}
}

func TestSaveRewritesOnlyWhatChanged(t *testing.T) {
	dir := t.TempDir()
	v := settings.Default()
	if err := settings.Save(dir, v); err != nil {
		t.Fatal(err)
	}
	rules, _ := os.Stat(filepath.Join(dir, "rules.json"))
	v.Settings.TUN = true
	if err := settings.Save(dir, v); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(filepath.Join(dir, "rules.json")); !os.SameFile(rules, after) {
		t.Error("an unchanged rules.json was replaced")
	}
	if got, _ := settings.Load(dir); !got.Settings.TUN {
		t.Errorf("settings = %+v", got.Settings)
	}
}

// A dotfile manager in symlink mode links each file into its own source.
func TestSaveWritesThroughASymlink(t *testing.T) {
	dir, source := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(source, []byte(`{"mode":"rule"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	v := settings.Default()
	v.Settings.Mode = protocol.ModeDirect
	if err := settings.Save(dir, v); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "settings.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
	if info, _ := os.Stat(source); info.Mode().Perm() != 0o600 {
		t.Errorf("the source's mode became %v", info.Mode())
	}
	if got, _ := settings.Load(dir); got.Settings.Mode != protocol.ModeDirect {
		t.Errorf("mode = %q", got.Settings.Mode)
	}
}

func TestMalformedFilesNameThemselves(t *testing.T) {
	for name, content := range map[string]string{
		"settings.json": `{"mode":"rule","autostart":true}`,
		"rules.json":    `[] []`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := settings.Load(dir); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: Load = %v", name, err)
		}
	}
}

func TestSaveRepairsPublicPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "omihomo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", "rules.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := settings.Save(dir, settings.Default()); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"", "settings.json", "rules.json"} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0o600)
			if name == "" {
				want = 0o700
			}
			if info.Mode().Perm() != want {
				t.Errorf("%q mode = %o; want %o", name, info.Mode().Perm(), want)
			}
		}
	}
}

func TestTransactionsLockTheDirectoryWithoutAddingAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "omihomo")
	if err := settings.Save(dir, settings.Default()); err != nil {
		t.Fatal(err)
	}
	lock, err := settings.Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "rules.json" || entries[1].Name() != "settings.json" {
		t.Fatalf("config contains %v; want only the two JSON files", entries)
	}
	other, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("another transaction acquired the locked directory: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("closing the transaction did not release its lock: %v", err)
	}
}
