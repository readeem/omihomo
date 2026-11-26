package mihomo

import (
	goruntime "runtime"
	"runtime/debug"

	"github.com/readeem/mixray/constant"
	"github.com/readeem/mixray/transport/trusttunnel"
)

// Version is the mixray release this core embeds. Upstream's source carries
// a placeholder: its releases replace it with linker flags.
var Version = embeddedVersion()

func embeddedVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path != "github.com/readeem/mixray" {
				continue
			}
			if dep.Replace != nil {
				dep = dep.Replace
			}
			if dep.Version != "" {
				return dep.Version
			}
		}
	}
	return constant.Version
}

func init() {
	// DefaultRawConfig and adapters use this in their client identities too.
	constant.Version = Version
	// TrustTunnel snapshots the upstream version during dependency init.
	trusttunnel.Version = Version
	trusttunnel.TCPUserAgent = goruntime.GOOS + " " + trusttunnel.AppName + "/" + Version
}
