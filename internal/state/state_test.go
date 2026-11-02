package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/readeem/omihomo/internal/protocol"
)

func TestFreshStateHasDefaults(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings()
	if err != nil || !reflect.DeepEqual(settings, DefaultSettings()) {
		t.Errorf("settings = %+v, %v", settings, err)
	}
	subs, err := s.Subscriptions()
	if err != nil || subs.Active != "" || len(subs.List) != 0 {
		t.Errorf("subscriptions = %+v, %v", subs, err)
	}
}

func TestStateRoundTripsAndStaysPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	settings.TUN = true
	settings.DNSDestination = "Resolver"
	settings.Rules = append(settings.Rules, protocol.UserRule{Type: "DOMAIN-SUFFIX", Value: "example.com", Target: "DIRECT"})
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	id := NewID()
	n := int64(5)
	subs := Subscriptions{Active: id, List: []Subscription{{ID: id, Name: "Work", URL: "https://example.test/s?token=x", Upload: &n}}}
	if err := s.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Body: []byte("proxies: []\n\xff"), Providers: map[string][]byte{"proxy:p": []byte("x")}}
	if err := s.SaveSnapshot(id, snap); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(); err != nil || !reflect.DeepEqual(got, settings) {
		t.Errorf("settings = %+v, %v", got, err)
	}
	if got, err := s.Subscriptions(); err != nil || !reflect.DeepEqual(got, subs) {
		t.Errorf("subscriptions = %+v, %v", got, err)
	}
	if got, err := s.Snapshot(id); err != nil || !reflect.DeepEqual(got, snap) {
		t.Errorf("snapshot = %+v, %v", got, err)
	}

	for _, path := range []string{dir, filepath.Join(dir, snapshotDir)} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", path, info.Mode(), err)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, _ := e.Info()
		if !e.IsDir() && info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", e.Name(), info.Mode())
		}
	}

	if err := s.RemoveSnapshot(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(id); err == nil {
		t.Error("the snapshot survived its removal")
	}
}

func TestSnapshotIDsCannotEscape(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../settings", "", "ABC", NewID() + "/x"} {
		if err := s.SaveSnapshot(id, Snapshot{}); err == nil {
			t.Errorf("saved a snapshot as %q", id)
		}
	}
}

func TestSettingsSavedWithEveryFieldStillRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := `{"mode":"global","tun":true,"tun_redirect":false,"tailscale":false,"tailnet_proxy":false,` +
		`"primary_group":"","rules":[{"type":"DOMAIN","value":"a.example","target":"DIRECT"}],"running":true}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Settings{Mode: "global", TUN: true,
		Rules: []protocol.UserRule{{Type: "DOMAIN", Value: "a.example", Target: "DIRECT"}}, Running: true}
	if got, err := s.Settings(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %+v, %v", got, err)
	}
}
