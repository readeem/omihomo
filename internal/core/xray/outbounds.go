package xray

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	"github.com/readeem/omihomo/internal/core"
)

type fields map[string]any

func (f fields) string(key string) string {
	s, _ := f[key].(string)
	return s
}

func (f fields) object(key string) fields {
	m, _ := f[key].(map[string]any)
	return m
}

func (f fields) unsupported(keys ...string) string {
	for _, key := range slices.Sorted(maps.Keys(f)) {
		if !slices.Contains(keys, key) {
			return key
		}
	}
	return ""
}

func raw(v any) *json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	msg := json.RawMessage(data)
	return &msg
}

func buildOutbound(tag, kind string, settings fields, stream *conf.StreamConfig) (*xcore.OutboundHandlerConfig, error) {
	c := conf.OutboundDetourConfig{Tag: tag, Protocol: kind, Settings: raw(settings), StreamSetting: stream}
	return c.Build()
}

func proxyOutbound(tag, socketKey string, p core.Proxy) (*xcore.OutboundHandlerConfig, string) {
	options := fields(p.Options)
	kind := p.Type
	server, port := options.string("server"), options["port"]
	n, ok := port.(int)
	if !ok || n < 1 || n > 65535 || !safeHost(server) {
		return nil, "unsupported endpoint"
	}

	allowed := []string{"server", "port", "network", "tls", "servername", "sni", "udp", "tfo", "mptcp",
		"ip-version", "alpn", "client-fingerprint", "reality-opts", "ws-opts", "grpc-opts", "xhttp-opts",
		"skip-cert-verify"}
	if skip, set := options["skip-cert-verify"]; set && skip != false {
		return nil, "unsupported option skip-cert-verify"
	}
	settings := fields{"address": server, "port": n}
	switch kind {
	case "vless":
		allowed = append(allowed, "uuid", "flow", "encryption", "packet-encoding", "xudp")
		settings["id"], settings["flow"] = options.string("uuid"), options.string("flow")
		encryption := options.string("encryption")
		if encryption == "" {
			encryption = "none"
		}
		settings["encryption"] = encryption
	case "vmess":
		allowed = append(allowed, "uuid", "alterId", "cipher", "packet-encoding", "xudp")
		if alterID, _ := options["alterId"].(int); alterID != 0 {
			return nil, "legacy VMess alterId"
		}
		cipher := options.string("cipher")
		if cipher == "" {
			cipher = "auto"
		}
		settings["vnext"] = []fields{{"address": server, "port": n, "users": []fields{{"id": options.string("uuid"), "security": cipher}}}}
	case "hysteria2":
		return hysteriaOutbound(tag, socketKey, options, server, n)
	case "trojan":
		allowed = append(allowed, "password")
		settings["password"] = options.string("password")
	case "ss":
		allowed = append(allowed, "cipher", "password")
		kind = "shadowsocks"
		settings["method"], settings["password"] = options.string("cipher"), options.string("password")
	default:
		return nil, "unsupported protocol"
	}
	if key := options.unsupported(allowed...); key != "" {
		return nil, "unsupported option " + key
	}
	if encoding := options.string("packet-encoding"); encoding != "" && encoding != "xudp" {
		return nil, "unsupported packet encoding"
	}
	stream, reason := proxyStream(options, server, socketKey, p.Type == "trojan")
	if reason != "" {
		return nil, reason
	}
	return configuredOutbound(tag, kind, settings, stream)
}

func configuredOutbound(tag, kind string, settings fields, stream *conf.StreamConfig) (*xcore.OutboundHandlerConfig, string) {
	out, err := buildOutbound(tag, kind, settings, stream)
	if err != nil {
		// Native builders can embed credentials in their errors.
		return nil, "Xray rejected its settings"
	}
	return out, ""
}

func safeHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == ""
	}
	return isDomain(host)
}

func isDomain(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil || len(host) == 0 || len(host) > 253 {
		return false
	}
	return !strings.ContainsFunc(host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	})
}

func proxyStream(opts fields, server, socketKey string, tlsRequired bool) (*conf.StreamConfig, string) {
	stream := &conf.StreamConfig{SocketSettings: &conf.SocketConfig{Interface: socketKey}}
	switch opts.string("ip-version") {
	case "", "dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer":
	default:
		return nil, "unsupported IP version preference"
	}

	network := conf.TransportProtocol(opts.string("network"))
	if network == "" {
		network = "tcp"
	}
	stream.Network = &network
	switch network {
	case "tcp", "raw":
	case "ws", "websocket":
		ws := opts.object("ws-opts")
		if key := ws.unsupported("path", "headers", "max-early-data", "early-data-header-name"); key != "" {
			return nil, "unsupported WebSocket option " + key
		}
		headers := headerStrings(ws.object("headers"))
		path, err := url.Parse(ws.string("path"))
		if err != nil || path.IsAbs() || path.Host != "" {
			return nil, "invalid WebSocket path"
		}
		query := path.Query()
		if n, _ := ws["max-early-data"].(int); n != 0 {
			if header := ws.string("early-data-header-name"); header != "" && !strings.EqualFold(header, "Sec-WebSocket-Protocol") {
				return nil, "unsupported WebSocket early-data header"
			}
			query.Set("ed", strconv.Itoa(n))
			path.RawQuery = query.Encode()
		}
		if query.Has("ed") {
			return nil, "WebSocket early data defers connection setup"
		}
		stream.WSSettings = &conf.WebSocketConfig{Path: path.String(), Headers: headers}
	case "grpc":
		grpc := opts.object("grpc-opts")
		if grpc.string("grpc-user-agent") != "" {
			return nil, "unsupported gRPC user agent"
		}
		stream.GRPCSettings = &conf.GRPCConfig{ServiceName: grpc.string("grpc-service-name")}
	case "xhttp", "splithttp":
		xhttp := opts.object("xhttp-opts")
		if key := xhttp.unsupported("path", "host", "mode", "headers", "no-grpc-header", "x-padding-bytes"); key != "" {
			return nil, "unsupported XHTTP option " + key
		}
		headers := headerStrings(xhttp.object("headers"))
		conf := &conf.SplitHTTPConfig{Path: xhttp.string("path"), Host: xhttp.string("host"), Mode: xhttp.string("mode"),
			Headers: headers, NoGRPCHeader: xhttp["no-grpc-header"] == true}
		if padding := xhttp.string("x-padding-bytes"); padding != "" {
			if err := conf.XPaddingBytes.UnmarshalJSON(*raw(padding)); err != nil ||
				conf.XPaddingBytes.From < 0 || conf.XPaddingBytes.To > 2048 {
				return nil, "unsupported XHTTP padding range"
			}
		}
		stream.XHTTPSettings = conf
	default:
		return nil, "unsupported transport"
	}

	sni := opts.string("servername")
	if sni == "" {
		sni = opts.string("sni")
	}
	if sni == "" {
		sni = server
	}
	if !safeHost(sni) {
		return nil, "unsupported TLS server name"
	}
	fingerprint := opts.string("client-fingerprint")
	if reality := opts.object("reality-opts"); len(reality) != 0 {
		if key := reality.unsupported("public-key", "short-id", "support-x25519mlkem768"); key != "" {
			return nil, "unsupported REALITY option " + key
		}
		if reality["support-x25519mlkem768"] == false {
			return nil, "REALITY without the hybrid key exchange"
		}
		if fingerprint == "" {
			fingerprint = "chrome"
		}
		stream.Security = "reality"
		stream.REALITYSettings = &conf.REALITYConfig{ServerName: sni, Fingerprint: fingerprint,
			PublicKey: reality.string("public-key"), ShortId: reality.string("short-id")}
	} else if opts["tls"] == true || tlsRequired {
		var alpn []string
		values, _ := opts["alpn"].([]any)
		for _, value := range values {
			if s, ok := value.(string); ok {
				alpn = append(alpn, s)
			}
		}
		stream.Security = "tls"
		stream.TLSSettings = &conf.TLSConfig{UseSystemCA: true, ServerName: sni, Fingerprint: fingerprint, ALPN: (*conf.StringList)(&alpn)}
	}
	return stream, ""
}

func headerStrings(headers fields) map[string]string {
	out := make(map[string]string, len(headers))
	for key := range headers {
		out[key] = headers.string(key)
	}
	return out
}

func proxyTag(index int) string { return fmt.Sprintf("proxy-%d", index) }
