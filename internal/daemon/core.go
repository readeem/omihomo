package daemon

import (
	"context"
	"slices"

	"github.com/readeem/omihomo/internal/protocol"
)

func (d *server) preferredCore(saved protocol.CoreName) protocol.CoreName {
	if slices.Contains(d.cfg.cores, saved) {
		return saved
	}
	return d.cfg.cores[0]
}

func (d *server) checkCore(name protocol.CoreName) *protocol.Error {
	if name != protocol.CoreMihomo && name != protocol.CoreXray {
		return fail(protocol.CodeInvalid, "core must be mihomo or xray")
	}
	if d.preferredCore(name) != name {
		return fail(protocol.CodeConflict, "%s is unavailable in this daemon", name)
	}
	return nil
}

func (d *server) switchCore(ctx context.Context, name protocol.CoreName) (protocol.Status, *protocol.Error) {
	if err := d.checkCore(name); err != nil {
		return protocol.Status{}, err
	}

	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next, p := d.settings, d.profile
	d.mu.Unlock()
	if name == d.preferredCore(next.Core) {
		return d.status(), nil
	}
	if err := ctx.Err(); err != nil {
		return protocol.Status{}, coreError(err)
	}
	next.Core = name
	var err *protocol.Error
	if d.running() {
		err = d.commitSettings(ctx, next, p)
	} else {
		err = d.saveSettings(next)
	}
	if err != nil {
		return protocol.Status{}, err
	}
	d.publish(ctx)
	return d.status(), nil
}
