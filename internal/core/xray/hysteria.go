package xray

import (
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

func hysteriaOutbound(tag, socketKey string, options fields, server string, port int) (*core.OutboundHandlerConfig, string) {
	if key := options.unsupported("server", "port", "password", "sni", "alpn", "skip-cert-verify", "ip-version", "tfo", "mptcp", "obfs", "obfs-password"); key != "" {
		return nil, "unsupported option " + key
	}
	password := options.string("password")
	if password == "" {
		return nil, "missing Hysteria 2 password"
	}
	sni := options.string("sni")
	if sni == "" {
		sni = server
	}
	if !safeHost(sni) {
		return nil, "unsupported TLS server name"
	}
	alpn, _ := options["alpn"].([]any)
	if len(alpn) != 0 && (len(alpn) != 1 || alpn[0] != "h3") {
		return nil, "unsupported Hysteria 2 ALPN"
	}
	network := conf.TransportProtocol("hysteria")
	stream := &conf.StreamConfig{
		Network: &network, Security: "tls",
		TLSSettings:      &conf.TLSConfig{UseSystemCA: true, ServerName: sni, ALPN: &conf.StringList{"h3"}},
		HysteriaSettings: &conf.HysteriaConfig{Version: 2, Auth: password},
		SocketSettings:   &conf.SocketConfig{Interface: socketKey},
	}
	switch options.string("obfs") {
	case "":
		if options.string("obfs-password") != "" {
			return nil, "Hysteria 2 obfuscation password without a method"
		}
	case "salamander":
		if options.string("obfs-password") == "" {
			return nil, "missing Hysteria 2 obfuscation password"
		}
		stream.FinalMask = &conf.FinalMask{Udp: []conf.Mask{
			{Type: "salamander", Settings: raw(fields{"password": options.string("obfs-password")})},
		}}
	default:
		return nil, "unsupported Hysteria 2 obfuscation"
	}
	return configuredOutbound(tag, "hysteria", fields{"version": 2, "address": server, "port": port}, stream)
}
