package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/cli"
	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/protocol"
)

// TestMain keeps every test away from the account's own settings files.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--internal-export-selections" {
		os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
	dir, err := os.MkdirTemp("", "omihomo-cli-config-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// serve runs a fake-core daemon owned by ownerUID and points the CLI at it.
func serve(t *testing.T, ownerUID int, opts ...daemon.ConfigOpt) {
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
	d, err := daemon.New(fakecore.New(), append([]daemon.ConfigOpt{
		daemon.WithOwnerFile(ownerFile),
		daemon.WithStateDir(filepath.Join(dir, "state")),
		daemon.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		daemon.WithTailscaleProbe(func() bool { return false }),
	}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	go func() { d.Run(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	t.Setenv("OMIHOMO_SOCKET", socket)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
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
	if code, _, _ := run(t, "http://example.com/sub\n", "sub", "add"); code != cli.ExitUsage {
		t.Fatalf("sub add over http exit %d", code)
	}
	if code, _, _ := run(t, "", "stop"); code != cli.ExitOK {
		t.Fatalf("stop exit %d", code)
	}
}

func TestAutomaticGroupsCanBePrimary(t *testing.T) {
	serve(t, os.Getuid())
	if code, _, errOut := run(t, "", "start"); code != cli.ExitOK {
		t.Fatalf("start: %d %s", code, errOut)
	}
	if code, out, errOut := run(t, "", "set", "group", "Auto"); code != cli.ExitOK || !strings.Contains(out, `"primary_group":"Auto"`) {
		t.Fatalf("make automatic group primary: %d %s %s", code, out, errOut)
	}
	if code, _, _ := run(t, "", "group", "select", "Auto", "Tokyo"); code != cli.ExitNotFound {
		t.Fatalf("select member of automatic group: %d", code)
	}
}

func TestSubscriptionAndSettingsCommands(t *testing.T) {
	var body atomic.Value
	body.Store("proxies:\n  - {name: Tokyo, type: trojan, server: tokyo.example, port: 443, password: p}\n" +
		"proxy-groups:\n  - {name: Proxy, type: select, proxies: [Tokyo, DIRECT]}\nrules:\n  - MATCH,Proxy\n")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Profile-Title", "Work")
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=3")
		fmt.Fprint(w, body.Load())
	}))
	defer srv.Close()
	serve(t, os.Getuid(), daemon.WithTransport(srv.Client().Transport))

	code, out, errOut := run(t, srv.URL+"/s?token=s3cret\n", "sub", "add")
	var sub protocol.Subscription
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &sub) != nil || sub.Name != "Work" || !sub.Active || *sub.Total != 3 {
		t.Fatalf("sub add: %d %q %s", code, out, errOut)
	}
	if code, out, _ := run(t, "", "sub", "list"); code != cli.ExitOK || !strings.Contains(out, `"name":"Work"`) || strings.Contains(out, "s3cret") {
		t.Fatalf("sub list: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "sub", "update", "Work"); code != cli.ExitState {
		t.Fatalf("an update right after the add exit %d", code)
	}
	if code, _, _ := run(t, "", "rule", "add", "DOMAIN-SUFFIX", "example.com", "Proxy"); code != cli.ExitOK {
		t.Fatalf("rule add exit %d", code)
	}
	if code, _, errOut := run(t, "", "rule", "add", "IP-CIDR", "not-a-cidr", "DIRECT"); code != cli.ExitUsage || !strings.Contains(errOut, "invalid IP-CIDR") {
		t.Fatalf("rule add with a bad value: %d %s", code, errOut)
	}
	if code, out, _ := run(t, "", "rule", "list"); code != cli.ExitOK || !strings.Contains(out, `"value":"example.com"`) {
		t.Fatalf("rule list: %d %q", code, out)
	}
	if code, out, _ := run(t, "", "set", "mode", "direct"); code != cli.ExitOK || !strings.Contains(out, `"active_subscription":"Work"`) {
		t.Fatalf("set mode: %d %q", code, out)
	}
	if code, _, _ := run(t, "", "start"); code != cli.ExitOK {
		t.Fatalf("start exit %d", code)
	}
	if code, _, _ := run(t, "", "sub", "remove", "Work"); code != cli.ExitState {
		t.Fatalf("removing the running subscription exit %d", code)
	}
	if code, _, _ := run(t, "", "rule", "remove", "1"); code != cli.ExitOK {
		t.Fatalf("rule remove exit %d", code)
	}
	if code, _, _ := run(t, "", "stop"); code != cli.ExitOK {
		t.Fatalf("stop exit %d", code)
	}
	if code, out, _ := run(t, "", "sub", "remove", "Work"); code != cli.ExitOK || out != "" {
		t.Fatalf("sub remove: %d %q", code, out)
	}
	body.Store("not a subscription")
	if code, _, _ := run(t, srv.URL+"/other\n", "sub", "add"); code != cli.ExitRejected {
		t.Fatalf("an unusable subscription exit %d", code)
	}
	if code, _, _ := run(t, "https://127.0.0.1:1/none\n", "sub", "add"); code != cli.ExitFetch {
		t.Fatalf("an unreachable subscription exit %d", code)
	}
}

// Changes land in the owner's settings files once the daemon runs them, and
// edits to the files run on apply.
func TestSettingsFilesHoldTheChanges(t *testing.T) {
	serve(t, os.Getuid())
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "omihomo")
	file := func(name string) string {
		t.Helper()
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	if code, _, errOut := run(t, "", "set", "tun", "on"); code != cli.ExitOK {
		t.Fatalf("set tun: %d %s", code, errOut)
	}
	if code, _, errOut := run(t, "", "rule", "add", "domain-suffix", "example.com", "Elsewhere"); code != cli.ExitOK {
		t.Fatalf("rule add while stopped: %d %s", code, errOut)
	}
	if got := file("settings.json"); got != "{\n  \"mode\": \"rule\",\n  \"tun\": true\n}\n" {
		t.Errorf("settings.json = %q", got)
	}
	if got := file("rules.json"); !strings.Contains(got, `"type": "DOMAIN-SUFFIX"`) {
		t.Errorf("rules.json = %q", got)
	}

	// What the daemon refuses is not saved.
	if code, _, _ := run(t, "", "set", "tailscale", "on"); code != cli.ExitState {
		t.Fatalf("tailscale without tailscaled exit %d", code)
	}
	if code, _, _ := run(t, "", "rule", "remove", "2"); code != cli.ExitNotFound {
		t.Fatalf("removing a rule that is not there exit %d", code)
	}
	if strings.Contains(file("settings.json"), "tailscale") {
		t.Errorf("a refused change was saved: %s", file("settings.json"))
	}

	// While the proxy runs, a name it lacks is refused.
	if code, _, _ := run(t, "", "start"); code != cli.ExitOK {
		t.Fatalf("start exit %d", code)
	}
	if code, _, _ := run(t, "", "rule", "add", "DOMAIN", "a.example", "Elsewhere"); code != cli.ExitNotFound {
		t.Errorf("rule add for a missing target exit %d", code)
	}
	if code, _, _ := run(t, "", "rule", "add", "DOMAIN", "a.example", "Tokyo"); code != cli.ExitOK {
		t.Errorf("rule add for a proxy exit %d", code)
	}
	if code, out, errOut := run(t, "", "rule", "list"); code != cli.ExitOK || !strings.Contains(out, `"target":"Elsewhere"`) {
		t.Fatalf("the unavailable target must keep its rule index: %d %s %s", code, out, errOut)
	}
	if code, out, errOut := run(t, "", "rule", "remove", "1"); code != cli.ExitOK || strings.Contains(out, `"target":"Elsewhere"`) || !strings.Contains(out, `"target":"Tokyo"`) {
		t.Fatalf("remove the first saved rule: %d %s %s", code, out, errOut)
	}
	if code, _, _ := run(t, "", "set", "group", "Missing"); code != cli.ExitNotFound {
		t.Errorf("a missing group became primary, exit %d", code)
	}

	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"mode":"global"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "", "apply"); code != cli.ExitOK || !strings.Contains(out, `"mode":"global"`) || !strings.Contains(out, `"tun":false`) {
		t.Fatalf("apply: %d %q", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte(`[{"type":"DOMAIN"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, "", "apply"); code != cli.ExitUsage || !strings.Contains(errOut, "rule 1") {
		t.Fatalf("apply an incomplete rule: %d %s", code, errOut)
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
