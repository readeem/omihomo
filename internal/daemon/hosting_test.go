package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/hosting"
	"github.com/readeem/omihomo/internal/protocol"
)

type hostingTransport struct {
	runs      atomic.Int32
	receivers chan hosting.Receiver
}

func (f *hostingTransport) Run(ctx context.Context, _ hosting.Relay, _ string, r hosting.Receiver) error {
	f.runs.Add(1)
	r.Connected()
	select {
	case f.receivers <- r:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func hostingRelay() hosting.Relay {
	return hosting.Relay{Version: 1, Label: "Fixture relay", Host: "relay.example", Port: 443, Address: "10.254.198.31", Reality: hosting.Reality{ServerName: "relay.example", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "abcd"}}
}

func hostingConfig(t *testing.T, h *harness, port int) (*client.Client, string) {
	t.Helper()
	c := h.dial(t)
	fresh := call[protocol.HostingSnapshot](t, c, protocol.OpHostingStatus, nil)
	if fresh.State != hosting.StateIdle || fresh.CredentialPresent || fresh.Active != 0 {
		t.Fatal("fresh install exposes hosting")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingRelaySet, protocol.HostingRelayArgs{Relay: hostingRelay()})
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingCredentialSet, protocol.HostingCredentialArgs{Credential: []byte("a0497f79-475f-42a8-92d7-808a49dfb36b")})
	saved := call[protocol.HostingSnapshot](t, c, protocol.OpHostingShareSave, protocol.HostingShareArgs{Share: hosting.Share{Label: "TCP fixture", LocalPort: port, ExposedPort: 25565, Protocol: hosting.ProtoTCP, Enabled: true}})
	if len(saved.Config.Shares) != 1 || saved.Config.Shares[0].Enabled || saved.Config.Shares[0].LocalAddress != "127.0.0.1" {
		t.Fatal("creation did not save a disabled explicit loopback share")
	}
	return c, saved.Config.Shares[0].ID
}

func receiveHosting(t *testing.T, f *hostingTransport) hosting.Receiver {
	t.Helper()
	select {
	case r := <-f.receivers:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("hosting did not establish a tunnel")
		return nil
	}
}

func TestHostingMasterModesAndDurableCredentialRevocation(t *testing.T) {
	transport := &hostingTransport{receivers: make(chan hosting.Receiver, 8)}
	h := serve(t, os.Getuid(), daemon.WithHostingTransport(transport))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c, id := hostingConfig(t, h, listener.Addr().(*net.TCPAddr).Port)
	call[protocol.Status](t, c, protocol.OpStart, nil)
	if transport.runs.Load() != 0 {
		t.Fatal("disabled shares established a tunnel")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingShareEnable, protocol.HostingShareEnableArgs{ID: id, Enabled: true})
	receiver := receiveHosting(t, transport)
	local, err := receiver.Open(t.Context(), hosting.Destination{Address: hostingRelay().Address, Network: "tcp", Port: 25565})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	target, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, mode := range []string{protocol.ModeDirect, protocol.ModeGlobal, protocol.ModeRule} {
		call[protocol.Status](t, c, protocol.OpApply, owned(protocol.Settings{Mode: mode, TUN: true}))
	}
	if transport.runs.Load() != 1 {
		t.Fatal("proxy mode or TUN change moved hosting identity")
	}
	status := call[protocol.HostingSnapshot](t, c, protocol.OpHostingStatus, nil)
	if status.Active != 1 || status.Shares[0].Active != 1 {
		t.Fatal("hosting session counts missing")
	}
	call[protocol.Empty](t, c, protocol.OpSubscribe, protocol.StreamsArgs{Streams: []string{protocol.StreamHosting}})
	event := next(t, c, protocol.StreamHosting)
	public, _ := json.Marshal(status)
	if strings.Contains(string(public), "a0497f79") || strings.Contains(string(event), "a0497f79") {
		t.Fatal("credential escaped in public state")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingCredentialRemove, nil)
	local.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := local.Read(make([]byte, 1)); err == nil {
		t.Fatal("credential removal left a session alive")
	}
	h.restart(t)
	c = h.dial(t)
	restarted := call[protocol.HostingSnapshot](t, c, protocol.OpHostingStatus, nil)
	if restarted.CredentialPresent || restarted.CredentialID != "" || !restarted.Config.Shares[0].Enabled || restarted.Reason != "unprovisioned" {
		t.Fatal("revocation or explicit selection did not survive restart")
	}
	if transport.runs.Load() != 1 {
		t.Fatal("restart resurrected a deleted credential")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingCredentialSet, protocol.HostingCredentialArgs{Credential: []byte("b0497f79-475f-42a8-92d7-808a49dfb36b")})
	receiveHosting(t, transport)
	call[protocol.Status](t, c, protocol.OpStop, nil)
	h.restart(t)
	c = h.dial(t)
	if snapshot := call[protocol.HostingSnapshot](t, c, protocol.OpHostingStatus, nil); snapshot.State != hosting.StateIdle || snapshot.Reason != "proxy off" {
		t.Fatal("proxy stop did not survive restart")
	}
	if transport.runs.Load() != 2 {
		t.Fatal("master off revived a tunnel")
	}
}

func TestHostingValidationAndPausedSelectionsSurviveRestart(t *testing.T) {
	transport := &hostingTransport{receivers: make(chan hosting.Receiver, 4)}
	h := serve(t, os.Getuid(), daemon.WithHostingTransport(transport))
	c, id := hostingConfig(t, h, 25565)
	bad := []any{
		map[string]any{"relay": hostingRelay(), "listener": "a0497f79-475f-42a8-92d7-808a49dfb36b"},
		map[string]any{"relay": map[string]any{"version": 1, "executable": "ignored"}},
	}
	for _, args := range bad {
		err := callErr(t, c, protocol.OpHostingRelaySet, args)
		if err.Code != protocol.CodeBadRequest || strings.Contains(err.Message, "a0497f79") {
			t.Fatal("untyped hosting content accepted or echoed")
		}
	}
	if err := callErr(t, c, protocol.OpHostingRelaySet, json.RawMessage(`{"relay":{},"padding":"`+strings.Repeat("x", 16<<10)+`"}`)); err.Code != protocol.CodeTooLarge {
		t.Fatal("oversized hosting import accepted")
	}
	invalid := hosting.Share{ID: id, Label: "LAN", LocalAddress: "192.0.2.1", LocalPort: 25565, ExposedPort: 25565, Protocol: hosting.ProtoTCP}
	if err := callErr(t, c, protocol.OpHostingShareSave, protocol.HostingShareArgs{Share: invalid}); err.Code != protocol.CodeInvalid {
		t.Fatal("non-loopback destination accepted")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingShareEnable, protocol.HostingShareEnableArgs{ID: id, Enabled: true})
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingPause, protocol.HostingPauseArgs{Paused: true})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	h.restart(t)
	c = h.dial(t)
	status := call[protocol.HostingSnapshot](t, c, protocol.OpHostingStatus, nil)
	if !status.Config.Paused || !status.Config.Shares[0].Enabled || status.Reason != "paused" || transport.runs.Load() != 0 {
		t.Fatal("paused selections did not survive restart")
	}
	call[protocol.HostingSnapshot](t, c, protocol.OpHostingPause, protocol.HostingPauseArgs{Paused: false})
	receiver := receiveHosting(t, transport)
	local, err := receiver.Open(t.Context(), hosting.Destination{Address: hostingRelay().Address, Network: "tcp", Port: 25565})
	if local != nil {
		local.Close()
	}
	if err != nil && !errors.Is(err, hosting.ErrUnavailable) && !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected target status: %v", err)
	}
}
