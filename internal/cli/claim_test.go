package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/owner"
)

type fakeMachine struct {
	claimer
	env   map[string]string
	calls [][]string
	fail  error
}

func newFakeMachine(t *testing.T, env map[string]string) *fakeMachine {
	dir := t.TempDir()
	m := &fakeMachine{env: env}
	m.claimer = claimer{
		euid:      0,
		getenv:    func(k string) string { return m.env[k] },
		ownerFile: filepath.Join(dir, "var/lib/omihomo/owner"),
		dropIn:    filepath.Join(dir, "etc/systemd/system/omihomo.socket.d/owner.conf"),
		systemctl: func(_ context.Context, args ...string) error {
			m.calls = append(m.calls, args)
			return m.fail
		},
	}
	return m
}

func TestClaimRecordsTheSudoAccount(t *testing.T) {
	m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
	if err := m.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if uid, err := owner.Load(m.ownerFile); err != nil || uid != 1000 {
		t.Fatalf("owner = %d, %v", uid, err)
	}
	if info, err := os.Stat(filepath.Dir(m.ownerFile)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state directory = %v, %v", info, err)
	}
	dropIn, err := os.ReadFile(m.dropIn)
	if err != nil || !strings.Contains(string(dropIn), "[Socket]\nSocketUser=1000\n") {
		t.Fatalf("drop-in = %q, %v", dropIn, err)
	}
	if info, _ := os.Stat(m.dropIn); info.Mode().Perm() != 0o644 {
		t.Fatalf("drop-in mode %v", info.Mode())
	}
	want := [][]string{
		{"daemon-reload"},
		{"stop", "omihomo.service", "omihomo.socket"},
		{"start", "omihomo.socket", "omihomo.service"},
	}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("systemctl calls = %v", m.calls)
	}
}

func TestClaimAgainReconcilesSystemd(t *testing.T) {
	m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
	if err := m.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.calls = nil
	if err := m.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"daemon-reload"}, {"stop", "omihomo.service", "omihomo.socket"}, {"start", "omihomo.socket", "omihomo.service"}}; !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("systemctl calls = %v", m.calls)
	}
}

func TestClaimMovesTheInstallToAnotherAccount(t *testing.T) {
	m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
	if err := m.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.calls = nil
	m.env = map[string]string{"PKEXEC_UID": "1001"}
	if err := m.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if uid, _ := owner.Load(m.ownerFile); uid != 1001 {
		t.Fatalf("owner = %d", uid)
	}
	if dropIn, _ := os.ReadFile(m.dropIn); !strings.Contains(string(dropIn), "SocketUser=1001\n") {
		t.Fatalf("drop-in = %q", dropIn)
	}
	if len(m.calls) != 3 || m.calls[0][0] != "daemon-reload" {
		t.Fatalf("systemctl calls = %v", m.calls)
	}
}

func TestClaimRefusesWithoutAnInvokingAccount(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no sudo":     {},
		"from root":   {"SUDO_UID": "0"},
		"not a uid":   {"SUDO_UID": "1000; rm -rf /"},
		"negative":    {"SUDO_UID": "-1"},
		"too large":   {"SUDO_UID": "4294967296"},
		"empty sudo":  {"SUDO_UID": "", "PKEXEC_UID": "x"},
		"pkexec root": {"PKEXEC_UID": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			m := newFakeMachine(t, env)
			if err := m.claim(context.Background()); err == nil {
				t.Fatal("claimed")
			}
			if _, err := os.Stat(m.ownerFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owner file: %v", err)
			}
			if len(m.calls) != 0 {
				t.Fatalf("systemctl calls = %v", m.calls)
			}
		})
	}
}

func TestClaimNeedsRoot(t *testing.T) {
	m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
	m.euid = 1000
	if err := m.claim(context.Background()); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("claim as an ordinary account = %v", err)
	}
	if _, err := os.Stat(m.ownerFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner file: %v", err)
	}
}

func TestClaimReportsSystemctlFailures(t *testing.T) {
	m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
	m.fail = errors.New("boom")
	if err := m.claim(context.Background()); err == nil {
		t.Fatal("claim hid a systemctl failure")
	}
}

func TestClaimRetriesAfterSystemctlFailure(t *testing.T) {
	for _, failedStep := range []string{"daemon-reload", "stop", "start"} {
		t.Run(failedStep, func(t *testing.T) {
			m := newFakeMachine(t, map[string]string{"SUDO_UID": "1000"})
			working := m.systemctl
			m.systemctl = func(ctx context.Context, args ...string) error {
				if args[0] == failedStep {
					return errors.New("interrupted")
				}
				return working(ctx, args...)
			}
			if err := m.claim(context.Background()); err == nil {
				t.Fatal("claim hid a systemctl failure")
			}
			m.systemctl = working
			m.calls = nil
			if err := m.claim(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := [][]string{{"daemon-reload"}, {"stop", serviceUnit, socketUnit}, {"start", socketUnit, serviceUnit}}
			if !reflect.DeepEqual(m.calls, want) {
				t.Fatalf("retry calls = %v", m.calls)
			}
		})
	}
}
