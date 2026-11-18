package daemon

import (
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
