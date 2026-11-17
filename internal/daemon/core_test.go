package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/fakecore"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/protocol"
)

type cleanupFailingCore struct {
	*fakecore.Core
	failStop atomic.Bool
}

func (c *cleanupFailingCore) Stop(ctx context.Context) error {
	if err := c.Core.Stop(ctx); err != nil {
		return err
	}
	if c.failStop.Load() {
		return errors.New("could not clean up target")
	}
	return nil
}

func TestCoreSwitchRestartsAndRollsBack(t *testing.T) {
	mihomo, xray := fakecore.New(), &cleanupFailingCore{Core: fakecore.New()}
	path := filepath.Join(t.TempDir(), "missing.dat")
	xray.SetGeodata(func(core.Profile) []core.GeodataFile {
		return []core.GeodataFile{{Path: path, URL: "https://example.invalid/missing.dat"}}
	})
	h := serveCore(t, os.Getuid(), mihomo, daemon.WithXrayCore(xray))
	c := h.dial(t)
	selectCore := func(name string) protocol.Status {
		t.Helper()
		return call[protocol.Status](t, c, protocol.OpSetCore, protocol.NameArgs{Name: name})
	}
	if s := selectCore("xray"); s.Core != "xray" || s.State != protocol.StateStopped {
		t.Fatalf("select while stopped: %+v", s)
	}
	xray.SetGeodata(nil)
	h.restart(t)
	c = h.dial(t)
	if s := call[protocol.Status](t, c, protocol.OpStart, nil); s.Core != "xray" || s.State != protocol.StateOn {
		t.Fatalf("start on saved core: %+v", s)
	}
	if s := selectCore("mihomo"); s.Core != "mihomo" || s.State != protocol.StateOn {
		t.Fatalf("switch while running: %+v", s)
	}
	xray.FailApply(errors.New("cannot open listener"))
	xray.failStop.Store(true)
	if err := callErr(t, c, protocol.OpSetCore, protocol.NameArgs{Name: "xray"}); !strings.Contains(err.Message, "cannot open listener") ||
		!strings.Contains(err.Message, "could not clean up target") {
		t.Fatalf("switch failure: %v", err)
	}
	if s := call[protocol.Status](t, c, protocol.OpStatus, nil); s.Core != "mihomo" || s.State != protocol.StateOn {
		t.Fatalf("status after failed switch: %+v", s)
	}
	call[protocol.GroupList](t, c, protocol.OpGroups, nil)
	h.restart(t)
	c = h.dial(t)
	if s := call[protocol.Status](t, c, protocol.OpStatus, nil); s.Core != "mihomo" {
		t.Fatalf("saved choice after failed switch: %+v", s)
	}
}

type compatibilityCore struct {
	*fakecore.Core
	warnings []string
}

func (c compatibilityCore) Compatibility(core.Config) core.Compatibility {
	return core.Compatibility{Warnings: c.warnings}
}

func TestCoreSwitchWaitsForCompatibilityConfirmation(t *testing.T) {
	xray := compatibilityCore{Core: fakecore.New(), warnings: []string{"Config \"Unsupported\" skipped"}}
	h := serveCore(t, os.Getuid(), fakecore.New(), daemon.WithXrayCore(xray))
	c := h.dial(t)
	initial := call[protocol.Status](t, c, protocol.OpStart, nil)
	args := protocol.CoreSelectionArgs{Name: "xray"}
	selection := call[protocol.CoreSelection](t, c, protocol.OpSetCore, args)
	if selection.Core != "mihomo" || selection.StartedAt != initial.StartedAt ||
		selection.Confirmation == "" || !slices.Equal(selection.ConfirmWarnings, xray.warnings) {
		t.Fatalf("switch before confirmation: %+v", selection)
	}
	args.Confirmation = selection.Confirmation
	call[protocol.Status](t, c, protocol.OpApply, owned(protocol.Settings{}, protocol.RuleEntry{
		Type: "DOMAIN", Value: "changed.example", Target: "DIRECT",
	}))
	selection = call[protocol.CoreSelection](t, c, protocol.OpSetCore, args)
	if selection.Core != "mihomo" || selection.Confirmation == args.Confirmation ||
		!slices.Equal(selection.ConfirmWarnings, xray.warnings) {
		t.Fatalf("switch after stale confirmation: %+v", selection)
	}
	args.Confirmation = selection.Confirmation
	selection = call[protocol.CoreSelection](t, c, protocol.OpSetCore, args)
	if selection.Core != "xray" || selection.State != protocol.StateOn || len(selection.ConfirmWarnings) != 0 || !slices.Equal(selection.CoreWarnings, xray.warnings) {
		t.Fatalf("switch after confirmation: %+v", selection)
	}
}
