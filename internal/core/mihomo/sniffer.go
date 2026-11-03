package mihomo

import (
	"context"

	"github.com/metacubex/mihomo/constant"
)

type sniffedProxy struct{ constant.Proxy }

func (p sniffedProxy) DialContext(ctx context.Context, metadata *constant.Metadata) (constant.Conn, error) {
	return p.Proxy.DialContext(ctx, sniffedDestination(metadata))
}

func (p sniffedProxy) ListenPacketContext(ctx context.Context, metadata *constant.Metadata) (constant.PacketConn, error) {
	return p.Proxy.ListenPacketContext(ctx, sniffedDestination(metadata))
}

func sniffedDestination(metadata *constant.Metadata) *constant.Metadata {
	if metadata.SniffHost == "" || !metadata.DstIP.IsValid() {
		return metadata
	}
	destination := metadata.Clone()
	destination.Host = ""
	return destination
}
