# Local Hysteria receive-order fix

Source: [MetaCubeX/sing-quic at 1c242664697a441b01cfaf67bdbb7bb0d532745e](https://github.com/MetaCubeX/sing-quic/tree/1c242664697a441b01cfaf67bdbb7bb0d532745e).
The upstream license and module files are retained.

`hysteria2/client_packet.go` handles received datagrams sequentially. Upstream
starts a goroutine per datagram, which can reorder packets before delivery to
the application. The root module replaces this dependency locally so tests,
development builds and release packages use the same fix.

Remove the replacement when upstream preserves receive order. The regression
test is `TestHysteriaReceivesDatagramsInPeerOrder` in `internal/core/mihomo`.
