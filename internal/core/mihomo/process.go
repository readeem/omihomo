package mihomo

import (
	"errors"
	"os"

	"github.com/readeem/mixray/component/process"
	"github.com/readeem/mixray/constant"

	"github.com/readeem/omihomo/internal/protocol"
)

func processAttribution(m *constant.Metadata) (string, protocol.ProcessStatus, protocol.ProcessReason) {
	switch {
	case m.Type == constant.INNER:
		return constant.MihomoName, protocol.ProcessInternal, ""
	case m.Process != "":
		return m.Process, protocol.ProcessResolved, ""
	case !m.SrcIP.IsValid() || m.SrcIP.IsUnspecified() || m.SrcPort == 0:
		return "", protocol.ProcessNoLocalSocket, ""
	}

	var reason protocol.ProcessReason
	switch err := m.ProcessError; {
	case errors.Is(err, process.ErrNotFound):
		reason = protocol.ProcessSocketNotFound
	case errors.Is(err, process.ErrAmbiguousSocket):
		reason = protocol.ProcessSocketAmbiguous
	case errors.Is(err, process.ErrOwnerNotFound), errors.Is(err, os.ErrNotExist):
		reason = protocol.ProcessOwnerUnavailable
	case errors.Is(err, os.ErrPermission):
		reason = protocol.ProcessPermissionDenied
	case errors.Is(err, os.ErrDeadlineExceeded):
		reason = protocol.ProcessLookupTimeout
	}
	return "", protocol.ProcessLookupFailed, reason
}
