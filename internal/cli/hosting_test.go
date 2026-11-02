package cli_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/readeem/omihomo/internal/cli"
	"github.com/readeem/omihomo/internal/hosting"
	"github.com/readeem/omihomo/internal/protocol"
)

const testUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

type hostingDaemon struct {
	mu   sync.Mutex
	ops  []string
	args []json.RawMessage
	snap protocol.HostingSnapshot
}

func fakeHostingDaemon(t *testing.T, snap protocol.HostingSnapshot) *hostingDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "omihomo-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	l, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	d := &hostingDaemon{snap: snap}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go d.serve(conn)
		}
	}()
	t.Setenv("OMIHOMO_SOCKET", filepath.Join(dir, "s"))
	return d
}

func (d *hostingDaemon) serve(conn net.Conn) {
	defer conn.Close()
	send := func(v any) {
		line, _ := protocol.Line(v)
		conn.Write(line)
	}
	send(protocol.Hello{Type: protocol.TypeHello, Protocol: protocol.Version, Version: "test"})
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var req protocol.Request
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			return
		}
		d.mu.Lock()
		d.ops = append(d.ops, req.Op)
		d.args = append(d.args, req.Args)
		snap := d.snap
		d.mu.Unlock()
		send(protocol.Reply{Type: protocol.TypeReply, ID: req.ID, Result: snap})
	}
}
func (d *hostingDaemon) last(t *testing.T) (string, json.RawMessage) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.ops) == 0 {
		t.Fatal("the daemon got no request")
	}
	return d.ops[len(d.ops)-1], d.args[len(d.args)-1]
}

func (d *hostingDaemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.ops)
}

func hostSnapshot(shares ...hosting.Share) protocol.HostingSnapshot {
	snap := protocol.HostingSnapshot{CredentialPresent: true, CredentialID: "cred-1"}
	snap.Config.Relay = &hosting.Relay{Version: 1, Label: "r", Address: "10.77.0.2"}
	snap.Config.Shares = shares
	for _, s := range shares {
		snap.Shares = append(snap.Shares, hosting.ShareStatus{Share: s})
	}
	return snap
}

func TestHostingOpsForwardTyped(t *testing.T) {
	d := fakeHostingDaemon(t, hostSnapshot(hosting.Share{ID: "a", Label: "mc", LocalAddress: "127.0.0.1",
		LocalPort: 25565, ExposedPort: 25565, Protocol: "tcp", Enabled: true}))
	for _, c := range []struct {
		args []string
		op   string
		want string
	}{
		{[]string{"hosting", "status"}, protocol.OpHostingStatus, ""},
		{[]string{"hosting", "relay", "remove"}, protocol.OpHostingRelayRemove, ""},
		{[]string{"hosting", "credential", "remove"}, protocol.OpHostingCredentialRemove, ""},
		{[]string{"hosting", "share", "remove", "a"}, protocol.OpHostingShareRemove, `{"id":"a"}`},
		{[]string{"hosting", "share", "enable", "a"}, protocol.OpHostingShareEnable, `{"id":"a","enabled":true}`},
		{[]string{"hosting", "share", "disable", "a"}, protocol.OpHostingShareEnable, `{"id":"a","enabled":false}`},
		{[]string{"hosting", "pause"}, protocol.OpHostingPause, `{"paused":true}`},
		{[]string{"hosting", "resume"}, protocol.OpHostingPause, `{"paused":false}`},
		{[]string{"hosting", "retry"}, protocol.OpHostingRetry, ""},
	} {
		if code, _, errOut := run(t, "", c.args...); code != cli.ExitOK {
			t.Fatalf("%v: %d %s", c.args, code, errOut)
		}
		op, args := d.last(t)
		if op != c.op || (c.want != "" && string(args) != c.want) {
			t.Fatalf("%v sent %s %s", c.args, op, args)
		}
	}
	if code, _, _ := run(t, "", "hosting", "share", "enable"); code != cli.ExitUsage {
		t.Fatalf("enable without id exit %d", code)
	}
}

func TestHostingStatusShowsPublicAddresses(t *testing.T) {
	fakeHostingDaemon(t, hostSnapshot(
		hosting.Share{ID: "a", Label: "mc", LocalAddress: "127.0.0.1", LocalPort: 1, ExposedPort: 25565, Protocol: "tcp"}))
	_, out, _ := run(t, "", "hosting", "status")
	var view struct {
		CredentialPresent bool `json:"credential_present"`
		Addresses         []struct{ ID, Address string }
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil || !view.CredentialPresent ||
		len(view.Addresses) != 1 || view.Addresses[0].Address != "10.77.0.2:25565" {
		t.Fatalf("status: %q %v", out, err)
	}
}

func TestShareAddIsDisabledWithDefaults(t *testing.T) {
	d := fakeHostingDaemon(t, hostSnapshot())
	if code, _, errOut := run(t, "", "hosting", "share", "add", "--label", "mc", "--local-port", "25565"); code != cli.ExitOK {
		t.Fatalf("add: %d %s", code, errOut)
	}
	op, args := d.last(t)
	var got protocol.HostingShareArgs
	json.Unmarshal(args, &got)
	want := hosting.Share{Label: "mc", LocalAddress: "127.0.0.1", LocalPort: 25565, ExposedPort: 25565, Protocol: "tcp"}
	if op != protocol.OpHostingShareSave || got.Share != want {
		t.Fatalf("sent %s %+v", op, got)
	}
	n := d.count()
	if code, _, _ := run(t, "", "hosting", "share", "add", "--label", "mc"); code != cli.ExitUsage || d.count() != n {
		t.Fatalf("add without a port must fail before calling the daemon")
	}
}

func TestShareEditKeepsIDAndEnabled(t *testing.T) {
	cur := hosting.Share{ID: "a", Label: "mc", LocalAddress: "127.0.0.1", LocalPort: 25565, ExposedPort: 25565, Protocol: "tcp", Enabled: true}
	d := fakeHostingDaemon(t, hostSnapshot(cur))
	if code, _, errOut := run(t, "", "hosting", "share", "edit", "--protocol", "both", "a"); code != cli.ExitOK {
		t.Fatalf("edit: %d %s", code, errOut)
	}
	op, args := d.last(t)
	var got protocol.HostingShareArgs
	json.Unmarshal(args, &got)
	cur.Protocol = "both"
	if op != protocol.OpHostingShareSave || got.Share != cur {
		t.Fatalf("sent %s %+v", op, got)
	}
	if code, _, _ := run(t, "", "hosting", "share", "edit", "--label", "x", "missing"); code != cli.ExitNotFound {
		t.Fatalf("unknown share exit %d", code)
	}
	if code, _, _ := run(t, "", "hosting", "share", "edit", "a"); code != cli.ExitUsage {
		t.Fatalf("edit without fields exit %d", code)
	}
}

func TestRelayImportIsTyped(t *testing.T) {
	d := fakeHostingDaemon(t, hostSnapshot())
	profile := `{"version":1,"label":"r","host":"relay.example","port":443,"address":"10.77.0.2","credential_ref":"cred-1",` +
		`"reality":{"server_name":"example.com","public_key":"k","short_id":"ab"}}`
	if code, _, errOut := run(t, profile, "hosting", "relay", "set"); code != cli.ExitOK {
		t.Fatalf("relay set: %d %s", code, errOut)
	}
	op, args := d.last(t)
	var got protocol.HostingRelayArgs
	json.Unmarshal(args, &got)
	if op != protocol.OpHostingRelaySet || got.Relay.Host != "relay.example" || got.Relay.Reality.ShortID != "ab" {
		t.Fatalf("sent %s %s", op, args)
	}
	n := d.count()
	for name, in := range map[string]string{
		"unknown field": strings.Replace(profile, `"label"`, `"extra":1,"label"`, 1),
		"trailing data": profile + `{}`,
		"oversized":     `{"label":"` + strings.Repeat("x", 17<<10) + `"}`,
	} {
		if code, _, _ := run(t, in, "hosting", "relay", "set"); code != cli.ExitUsage {
			t.Fatalf("%s: exit %d", name, code)
		}
	}
	if d.count() != n {
		t.Fatal("a rejected profile reached the daemon")
	}
}

func TestCredentialImportKeepsSecretPrivate(t *testing.T) {
	d := fakeHostingDaemon(t, hostSnapshot())
	code, out, errOut := run(t, testUUID+"\n", "hosting", "credential", "set")
	if code != cli.ExitOK {
		t.Fatalf("stdin import: %d %s", code, errOut)
	}
	op, args := d.last(t)
	var got protocol.HostingCredentialArgs
	json.Unmarshal(args, &got)
	if op != protocol.OpHostingCredentialSet || string(got.Credential) != testUUID {
		t.Fatalf("sent %s", op)
	}
	if strings.Contains(out+errOut, testUUID) {
		t.Fatal("the credential was printed")
	}

	dir := t.TempDir()
	private := filepath.Join(dir, "cred")
	os.WriteFile(private, []byte(testUUID), 0o600)
	loose := filepath.Join(dir, "loose")
	os.WriteFile(loose, []byte(testUUID), 0o600)
	os.Chmod(loose, 0o644)
	link := filepath.Join(dir, "link")
	os.Symlink(private, link)
	big := filepath.Join(dir, "big")
	os.WriteFile(big, []byte(strings.Repeat("a", 300)), 0o600)

	if code, out, errOut := run(t, "", "hosting", "credential", "set", "--file", private); code != cli.ExitOK {
		t.Fatalf("file import: %d %s %s", code, out, errOut)
	}
	n := d.count()
	for name, file := range map[string]string{"loose": loose, "symlink": link, "oversized": big, "directory": dir} {
		code, out, errOut := run(t, "", "hosting", "credential", "set", "--file", file)
		if code != cli.ExitUsage || strings.Contains(out+errOut, testUUID) {
			t.Fatalf("%s: exit %d %q", name, code, errOut)
		}
	}
	if code, out, errOut := run(t, "", "hosting", "credential", "set", testUUID); code != cli.ExitUsage ||
		strings.Contains(out+errOut, testUUID) {
		t.Fatalf("argument credential: exit %d, secret echoed %v", code, strings.Contains(out+errOut, testUUID))
	}
	if d.count() != n {
		t.Fatal("a rejected credential reached the daemon")
	}
}
