package daemon_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestCorePreferencePersistsAndKeepsTheRunningCore(t *testing.T) {
	engine := fakecore.New()
	h := serveCore(t, os.Getuid(), engine, daemon.WithCores(protocol.CoreMihomo, protocol.CoreXray))
	c := h.dial(t)
	selectCore := func(name protocol.CoreName) protocol.Status {
		t.Helper()
		return call[protocol.Status](t, c, protocol.OpSetCore, protocol.CoreArgs{Name: name})
	}
	if s := selectCore("xray"); s.Core != "xray" || s.State != protocol.StateStopped {
		t.Fatalf("select while stopped: %+v", s)
	}
	h.restart(t)
	c = h.dial(t)
	initial := call[protocol.Status](t, c, protocol.OpStart, nil)
	if initial.Core != "xray" || initial.State != protocol.StateOn {
		t.Fatalf("start on saved core preference: %+v", initial)
	}
	if cfg, running := engine.Config(); !running || cfg.Core != protocol.CoreXray {
		t.Fatalf("saved preference did not reach the running core")
	}
	if s := selectCore("mihomo"); s.Core != "mihomo" || s.State != protocol.StateOn || s.StartedAt != initial.StartedAt {
		t.Fatalf("switch while running: %+v", s)
	}
	if cfg, running := engine.Config(); !running || cfg.Core != protocol.CoreMihomo {
		t.Fatalf("switch did not change the running core's preference")
	}
	engine.FailApply(errors.New("cannot start the Xray outbounds"))
	if err := callErr(t, c, protocol.OpSetCore, protocol.CoreArgs{Name: protocol.CoreXray}); !strings.Contains(err.Message, "cannot start the Xray outbounds") {
		t.Fatalf("switch failure: %v", err)
	}
	if s := call[protocol.Status](t, c, protocol.OpStatus, nil); s.Core != "mihomo" || s.State != protocol.StateOn {
		t.Fatalf("status after failed switch: %+v", s)
	}
	call[protocol.GroupList](t, c, protocol.OpGroups, nil)
	engine.FailApply(nil)
	h.restart(t)
	c = h.dial(t)
	if s := call[protocol.Status](t, c, protocol.OpStatus, nil); s.Core != "mihomo" {
		t.Fatalf("saved choice after failed switch: %+v", s)
	}
}
