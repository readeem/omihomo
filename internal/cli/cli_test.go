package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/cli"
	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/protocol"
)

// serve runs a fake-core daemon owned by ownerUID and points the CLI at it.
func serve(t *testing.T, ownerUID int) {
	t.Helper()
	dir, err := os.MkdirTemp("", "omihomo-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ownerFile := filepath.Join(dir, "owner")
	os.WriteFile(ownerFile, []byte(fmt.Sprintln(ownerUID)), 0o600)
	socket := filepath.Join(dir, "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	d := daemon.New(fakecore.New(),
		daemon.WithOwnerFile(ownerFile),
		daemon.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		daemon.WithTailscaleProbe(func() bool { return false }),
	)
	go func() { d.Run(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("OMIHOMO_SOCKET", socket)
}

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := cli.Run(ctx, args, cli.Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

func TestCommandsRunOverTheSocket(t *testing.T) {
	serve(t, os.Getuid())

	code, out, _ := run(t, "", "status")
	var st protocol.Status
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &st) != nil || st.State != "stopped" {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "group", "list"); code != cli.ExitState {
		t.Fatalf("group list while stopped exit %d", code)
	}
	if code, _, errOut := run(t, "", "start"); code != cli.ExitOK {
		t.Fatalf("start: %d %s", code, errOut)
	}
	if code, out, _ := run(t, "", "set", "mode", "global"); code != cli.ExitOK || !strings.Contains(out, `"mode":"global"`) {
		t.Fatalf("set mode: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "set", "tun", "--pretty", "on"); code != cli.ExitOK || !strings.Contains(out, "\n  \"tun\": true") {
		t.Fatalf("set tun --pretty: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "group", "select", "Proxy", "Frankfurt"); code != cli.ExitOK || !strings.Contains(out, `"now":"Frankfurt"`) {
		t.Fatalf("select: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "group", "select", "Proxy", "Mars"); code != cli.ExitNotFound {
		t.Fatalf("select unknown exit %d", code)
	}
	if code, out, _ := run(t, "", "group", "test", "Auto", "Tokyo"); code != cli.ExitOK || strings.TrimSpace(out) != `{"delays":{"Tokyo":120}}` {
		t.Fatalf("test: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "conn", "close", "--all"); code != cli.ExitOK || out != "" {
		t.Fatalf("close all: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "set", "tailscale", "on"); code != cli.ExitState {
		t.Fatalf("tailscale without tailscaled exit %d", code)
	}
	if code, _, _ := run(t, "https://example.com/sub\n", "sub", "add"); code != cli.ExitUnsupported {
		t.Fatalf("sub add exit %d", code)
	}
	if code, _, _ := run(t, "", "stop"); code != cli.ExitOK {
		t.Fatalf("stop exit %d", code)
	}
}

func TestUsageErrorsNeverReachTheDaemon(t *testing.T) {
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(t.TempDir(), "none"))
	for _, args := range [][]string{
		{"set", "tun", "maybe"},
		{"set", "colour", "blue"},
		{"rule", "remove", "0"},
		{"sub", "add", "https://token@example.com"},
		{"conn", "close"},
		{"frobnicate"},
	} {
		if code, _, _ := run(t, "", args...); code != cli.ExitUsage {
			t.Errorf("%v exit %d", args, code)
		}
	}
	if code, _, _ := run(t, "", "sub", "add"); code != cli.ExitUsage {
		t.Errorf("sub add with empty stdin exit %d", code)
	}
}

func TestHelpSucceeds(t *testing.T) {
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(t.TempDir(), "none"))
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
		{"status", "--help"},
		{"group", "select", "-h"},
		{"set", "--help"},
		{"daemon", "--help"},
	} {
		code, out, errOut := run(t, "", args...)
		if code != cli.ExitOK || out != "" || !strings.Contains(errOut, "USAGE") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if _, _, errOut := run(t, "", "--help"); !strings.Contains(errOut, "watch") || !strings.Contains(errOut, "daemon") {
		t.Errorf("root help does not list every command: %q", errOut)
	}
}

func TestArgumentsAreChecked(t *testing.T) {
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(t.TempDir(), "none"))
	for _, args := range [][]string{
		{},
		{"version", "unexpected"},
		{"status", "extra"},
		{"set"},
		{"set", "mode"},
		{"set", "mode", "rule", "global"},
		{"group", "select", "Proxy"},
		{"group", "select", "Proxy", "Tokyo", "--pretty"},
		{"conn", "close", "--all", "abc"},
		{"status", "--verbose"},
		{"daemon", "--core", "fake", "extra"},
		{"daemon", "--core", "nope"},
	} {
		if code, _, errOut := run(t, "", args...); code != cli.ExitUsage || !strings.Contains(errOut, "USAGE") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestFlagsEndAtDoubleDash(t *testing.T) {
	serve(t, os.Getuid())
	if code, _, errOut := run(t, "", "start"); code != cli.ExitOK {
		t.Fatalf("start: %d %s", code, errOut)
	}
	code, _, errOut := run(t, "", "group", "select", "--", "Proxy", "--pretty")
	if code != cli.ExitNotFound || !strings.Contains(errOut, "--pretty") {
		t.Fatalf("select a proxy named --pretty: %d %q", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	if code, out, _ := run(t, "", "version"); code != cli.ExitOK || !strings.Contains(out, "protocol") {
		t.Fatalf("version: %d %q", code, out)
	}
}

func TestNoDaemon(t *testing.T) {
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(t.TempDir(), "none"))
	if code, _, errOut := run(t, "", "status"); code != cli.ExitUnreachable || !strings.Contains(errOut, "not running") {
		t.Fatalf("status without a daemon: %d %q", code, errOut)
	}
}

func TestNotTheOwner(t *testing.T) {
	serve(t, os.Getuid()+1)
	if code, _, errOut := run(t, "", "status"); code != cli.ExitForbidden || !strings.Contains(errOut, "does not own") {
		t.Fatalf("status as another account: %d %q", code, errOut)
	}
}

func TestProtocolMismatch(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, `{"type":"hello","protocol":%d,"version":"future"}`+"\n", protocol.Version+1)
		io.Copy(io.Discard, conn)
	}()
	t.Setenv("OMIHOMO_SOCKET", socket)
	if code, _, errOut := run(t, "", "status"); code != cli.ExitVersion || !strings.Contains(errOut, "update the core") {
		t.Fatalf("status against a newer daemon: %d %q", code, errOut)
	}
}

func TestWatchPrintsEvents(t *testing.T) {
	serve(t, os.Getuid())
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	done := make(chan int)
	go func() {
		done <- cli.Run(ctx, []string{"watch", "status"}, cli.Env{Stdout: &out, Stderr: io.Discard})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), `"stream":"status"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no event printed: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != cli.ExitOK {
		t.Fatalf("watch exit %d", code)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
