package tailscale

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSyncChangesOnlyItsOwnDropInAndRestoresFailedReload(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.conf")
	os.WriteFile(other, []byte("unrelated"), 0o600)
	var calls [][]string
	fail := false
	run := func(_ context.Context, args ...string) error {
		calls = append(calls, args)
		if fail {
			return errors.New("failed reload")
		}
		return nil
	}
	if err := syncDropIn(context.Background(), dir, true, run); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, filename))
	if string(data) != content {
		t.Fatalf("drop-in: %s", data)
	}
	if !reflect.DeepEqual(calls, [][]string{{"daemon-reload"}, {"try-restart", "tailscaled.service"}}) {
		t.Fatal(calls)
	}
	if err := syncDropIn(context.Background(), dir, true, run); err != nil || len(calls) != 2 {
		t.Fatal("unchanged integration restarted tailscaled")
	}
	fail = true
	if err := syncDropIn(context.Background(), dir, false, run); err == nil {
		t.Fatal("reload failure hidden")
	}
	data, _ = os.ReadFile(filepath.Join(dir, filename))
	if string(data) != content {
		t.Fatal("failed removal not restored")
	}
	fail = false
	if err := syncDropIn(context.Background(), dir, false, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("drop-in remains")
	}
	data, _ = os.ReadFile(other)
	if string(data) != "unrelated" {
		t.Fatal("unrelated drop-in changed")
	}
}

func TestSyncRefusesWritableOrSymlinkedDirectory(t *testing.T) {
	for _, kind := range []string{"writable", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if kind == "writable" {
				os.Chmod(dir, 0o777)
			} else {
				link := filepath.Join(t.TempDir(), "link")
				os.Symlink(dir, link)
				dir = link
			}
			if err := syncDropIn(context.Background(), dir, true, func(context.Context, ...string) error { t.Fatal("ran systemctl"); return nil }); err == nil {
				t.Fatal("untrusted directory accepted")
			}
		})
	}
}

func TestSyncRefusesSymlinkedDropIn(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("private"), 0o600)
	os.Symlink(outside, filepath.Join(dir, filename))
	if err := syncDropIn(context.Background(), dir, true, func(context.Context, ...string) error { t.Fatal("ran systemctl"); return nil }); err == nil {
		t.Fatal("symlink accepted")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "private" {
		t.Fatal("outside file changed")
	}
}
