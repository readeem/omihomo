package mihomo

import (
	goruntime "runtime"
	"runtime/debug"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/trusttunnel"
)

// Version is the mihomo release this core embeds. Upstream's source carries
// a placeholder: its releases replace it with linker flags.
var Version = embeddedVersion()

func embeddedVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path != "github.com/metacubex/mihomo" {
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
	return C.Version
}

func init() {
	// DefaultRawConfig and adapters use this in their client identities too.
	C.Version = Version
	// TrustTunnel snapshots the upstream version during dependency init.
	trusttunnel.Version = Version
	trusttunnel.TCPUserAgent = goruntime.GOOS + " " + trusttunnel.AppName + "/" + Version
}
