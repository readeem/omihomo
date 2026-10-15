package profile

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const testPEM = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

// commonProxies are proxies of every modelled type, written with the fields
// subscriptions commonly carry. Each keeps all of them.
var commonProxies = []map[string]any{
	{"name": "ss", "type": "ss", "server": "a.example", "port": 8388, "cipher": "aes-128-gcm", "password": "pw",
		"udp": true, "udp-over-tcp": false, "ip-version": "ipv4", "tfo": false, "mptcp": true,
		"plugin": "obfs", "plugin-opts": map[string]any{"mode": "tls", "host": "b.example"},
		"smux": map[string]any{"enabled": true, "protocol": "h2mux", "max-streams": 0,
			"brutal-opts": map[string]any{"enabled": false, "up": "50", "down": "100"}}},
	{"name": "ssr", "type": "ssr", "server": "a.example", "port": 443, "cipher": "chacha20-ietf", "password": "pw",
		"obfs": "tls1.2_ticket_auth", "obfs-param": "b.example", "protocol": "auth_sha1_v4", "protocol-param": "", "udp": true},
	{"name": "vmess", "type": "vmess", "server": "a.example", "port": 443, "uuid": "u", "alterId": 0, "cipher": "auto",
		"udp": true, "tls": true, "servername": "b.example", "skip-cert-verify": false, "alpn": []any{"h2", "http/1.1"},
		"client-fingerprint": "chrome", "network": "ws",
		"ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "b.example"}, "max-early-data": 2048,
			"early-data-header-name": "Sec-WebSocket-Protocol"},
		"http-opts": map[string]any{"method": "GET", "path": []any{"/"}, "headers": map[string]any{"Host": []any{"b.example"}}},
		"h2-opts":   map[string]any{"host": []any{"b.example"}, "path": "/"}},
	{"name": "vless", "type": "vless", "server": "a.example", "port": 443, "uuid": "u", "flow": "xtls-rprx-vision",
		"udp": true, "tls": true, "servername": "b.example", "client-fingerprint": "chrome", "network": "tcp",
		"reality-opts": map[string]any{"public-key": "k", "short-id": "0123"}, "packet-encoding": "xudp",
		"encryption": "none", "grpc-opts": map[string]any{"grpc-service-name": "svc"},
		"xhttp-opts": map[string]any{"path": "/x", "mode": "auto"}},
	{"name": "trojan", "type": "trojan", "server": "a.example", "port": 443, "password": "pw", "sni": "b.example",
		"udp": true, "skip-cert-verify": true, "fingerprint": "ab", "network": "grpc",
		"grpc-opts": map[string]any{"grpc-service-name": "svc"}, "ech-opts": map[string]any{"enable": true, "config": "AA=="},
		"ss-opts": map[string]any{"enabled": false, "method": "aes-128-gcm", "password": "pw"}},
	{"name": "hysteria", "type": "hysteria", "server": "a.example", "port": 443, "ports": "1000-2000",
		"protocol": "udp", "up": "30 Mbps", "down": "200 Mbps", "auth-str": "pw", "obfs": "o", "sni": "b.example",
		"alpn": []any{"h3"}, "recv-window-conn": 12582912, "disable-mtu-discovery": false, "fast-open": true},
	{"name": "hysteria2", "type": "hysteria2", "server": "a.example", "port": 443, "ports": "1000-2000",
		"hop-interval": "30", "password": "pw", "obfs": "salamander", "obfs-password": "o", "sni": "b.example",
		"up": "30", "down": "200", "skip-cert-verify": false, "alpn": []any{"h3"}},
	{"name": "tuic", "type": "tuic", "server": "a.example", "port": 443, "uuid": "u", "password": "pw",
		"congestion-controller": "bbr", "udp-relay-mode": "native", "reduce-rtt": true, "alpn": []any{"h3"},
		"sni": "b.example", "disable-sni": false, "heartbeat-interval": 10000, "request-timeout": 8000},
	{"name": "wireguard", "type": "wireguard", "server": "a.example", "port": 51820, "ip": "172.16.0.2",
		"ipv6": "fd01::2", "private-key": "priv", "public-key": "pub", "pre-shared-key": "psk",
		"reserved": []any{1, 2, 3}, "allowed-ips": []any{"0.0.0.0/0"}, "mtu": 1280, "udp": true,
		"remote-dns-resolve": true, "dns": []any{"1.1.1.1"},
		"amnezia-wg-option": map[string]any{"jc": 4, "jmin": 40, "jmax": 70, "s1": 0, "h1": "1"}},
	{"name": "snell", "type": "snell", "server": "a.example", "port": 443, "psk": "pw", "version": 3, "udp": true,
		"obfs-opts": map[string]any{"mode": "http", "host": "b.example"}},
	{"name": "socks5", "type": "socks5", "server": "a.example", "port": 1080, "username": "u", "password": "pw",
		"tls": true, "udp": true, "skip-cert-verify": true, "certificate": testPEM, "private-key": testPEM},
	{"name": "http", "type": "http", "server": "a.example", "port": 8080, "username": "u", "password": "pw",
		"tls": true, "sni": "b.example", "headers": map[string]any{"X-Id": "1"}},
	{"name": "ssh", "type": "ssh", "server": "a.example", "port": 22, "username": "u", "password": "pw",
		"private-key": "-----BEGIN OPENSSH PRIVATE KEY-----\nAA\n-----END OPENSSH PRIVATE KEY-----\n",
		"host-key":    []any{"ssh-ed25519 AAAA"}},
	{"name": "anytls", "type": "anytls", "server": "a.example", "port": 443, "password": "pw", "sni": "b.example",
		"client-fingerprint": "chrome", "udp": true, "alpn": []any{"h2"}, "idle-session-timeout": 30},
	{"name": "mieru", "type": "mieru", "server": "a.example", "port-range": "2090-2099", "transport": "TCP",
		"username": "u", "password": "pw", "multiplexing": "MULTIPLEXING_LOW", "udp": true},
	{"name": "direct", "type": "direct", "ip-version": "ipv4-prefer", "tfo": true},
}

func TestEveryModelledTypeKeepsItsCommonFields(t *testing.T) {
	seen := map[string]bool{}
	for _, in := range commonProxies {
		p, err := Proxy(in)
		if err != nil {
			t.Errorf("%v: %v", in["name"], err)
			continue
		}
		seen[p.Type] = true
		want := maps.Clone(in)
		delete(want, "name")
		delete(want, "type")
		if p.Name != in["name"] || p.Type != in["type"] || !reflect.DeepEqual(p.Options, want) {
			t.Errorf("%s: got %s %s %v\nwant %v", in["name"], p.Name, p.Type, p.Options, want)
		}
	}
	for typ := range ProxyTypes() {
		if !seen[typ] {
			t.Errorf("no common fields test for %s", typ)
		}
	}
}

func TestProxyDropsUnknownAndDangerousKeys(t *testing.T) {
	for _, tc := range []struct {
		in        map[string]any
		dropped   []string
		keptInner map[string]any
	}{
		{
			in: map[string]any{"name": "a", "type": "vmess", "server": "a.example", "port": 443, "uuid": "u",
				"dialer-proxy": "Other", "interface-name": "eth0", "routing-mark": 255, "made-up": 1,
				"certificate": "/etc/ssl/private/key.pem", "private-key": "key.pem"},
			dropped: []string{"dialer-proxy", "interface-name", "routing-mark", "made-up", "certificate", "private-key"},
		},
		{
			in: map[string]any{"name": "a", "type": "trojan", "server": "a.example", "port": 443, "password": "pw",
				"certificate": testPEM, "private-key": "  \n" + testPEM, "ca": testPEM, "ca-str": testPEM},
			dropped: []string{"ca", "ca-str"},
		},
		{
			in: map[string]any{"name": "a", "type": "ssh", "server": "a.example", "port": 22, "username": "u",
				"private-key": "/root/.ssh/id_ed25519"},
			dropped: []string{"private-key"},
		},
		{
			in: map[string]any{"name": "a", "type": "ss", "server": "a.example", "port": 443, "cipher": "aes-128-gcm",
				"password": "pw", "plugin": "v2ray-plugin", "plugin-opts": map[string]any{"mode": "websocket",
					"tls": true, "certificate": "cert.pem", "version-hint": "tls13", "unknown": 1}},
			keptInner: map[string]any{"plugin-opts": map[string]any{"mode": "websocket", "tls": true}},
		},
		{
			in: map[string]any{"name": "a", "type": "hysteria2", "server": "a.example", "port": 443, "password": "pw",
				"realm-opts": map[string]any{"enable": true, "server-url": "https://c.example"}},
			dropped: []string{"realm-opts"},
		},
		{
			in: map[string]any{"name": "a", "type": "wireguard", "server": "a.example", "port": 51820,
				"private-key": "priv", "public-key": "pub", "ip-stack": map[string]any{"mode": "system"}},
			dropped: []string{"ip-stack"},
		},
	} {
		p, err := Proxy(tc.in)
		if err != nil {
			t.Errorf("%v: %v", tc.in, err)
			continue
		}
		for _, k := range tc.dropped {
			if _, ok := p.Options[k]; ok {
				t.Errorf("%s kept %s: %v", tc.in["type"], k, p.Options)
			}
		}
		for k, want := range tc.keptInner {
			if !reflect.DeepEqual(p.Options[k], want) {
				t.Errorf("%s: %s = %v, want %v", tc.in["type"], k, p.Options[k], want)
			}
		}
		for k, v := range tc.in {
			if k == "name" || k == "type" || tc.keptInner[k] != nil || slices.Contains(tc.dropped, k) {
				continue
			}
			if _, ok := p.Options[k]; !ok {
				t.Errorf("%s dropped %s=%v", tc.in["type"], k, v)
			}
		}
	}
}

func TestProxyKeepsInlinePEM(t *testing.T) {
	p, err := Proxy(map[string]any{"name": "a", "type": "http", "server": "a.example", "port": 443,
		"tls": true, "certificate": testPEM, "private-key": testPEM})
	if err != nil {
		t.Fatal(err)
	}
	if p.Options["certificate"] != testPEM || p.Options["private-key"] != testPEM {
		t.Errorf("inline PEM was dropped: %v", p.Options)
	}
}

func TestProxyRejects(t *testing.T) {
	base := func(typ string, kv ...any) map[string]any {
		m := map[string]any{"name": "Bad", "type": typ, "server": "a.example", "port": 443, "password": "hunter2"}
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == nil {
				delete(m, kv[i].(string))
			} else {
				m[kv[i].(string)] = kv[i+1]
			}
		}
		return m
	}
	for _, tc := range []struct {
		why string
		in  map[string]any
	}{
		{"no name", base("trojan", "name", nil)},
		{"empty name", base("trojan", "name", "")},
		{"no type", base("trojan", "type", nil)},
		{"type not a string", base("trojan", "type", 1)},
		{"openvpn", base("openvpn")},
		{"tailscale", base("tailscale")},
		{"reject", base("reject")},
		{"dns", base("dns")},
		{"masque", base("masque")},
		{"gost-relay", base("gost-relay")},
		{"no server", base("trojan", "server", nil)},
		{"empty server", base("trojan", "server", "")},
		{"no port", base("trojan", "port", nil)},
		{"port 0", base("trojan", "port", 0)},
		{"port too big", base("trojan", "port", 65536)},
		{"port string garbage", base("trojan", "port", "https")},
		{"port fraction", base("trojan", "port", 443.5)},
		{"port a list", base("trojan", "port", []any{443})},
		{"bool from string", base("trojan", "udp", "true")},
		{"bool from map", base("trojan", "udp", map[string]any{})},
		{"string from bool", base("trojan", "sni", true)},
		{"list from string", base("trojan", "alpn", "h2")},
		{"null in list", base("trojan", "alpn", []any{"h2", nil})},
		{"opts not a map", base("trojan", "ws-opts", "/ws")},
		{"nested garbage", base("trojan", "ws-opts", map[string]any{"max-early-data": "lots"})},
		{"header not a string", base("trojan", "ws-opts", map[string]any{"headers": map[string]any{"Host": []any{"x"}}})},
		{"unsupported ss plugin", base("ss", "cipher", "aes-128-gcm", "plugin", "kcptun")},
		{"faketcp", base("hysteria", "protocol", "faketcp")},
		{"faketcp via obfs-protocol", base("hysteria", "obfs-protocol", "faketcp")},
		{"snell shadow-tls", base("snell", "psk", "x", "obfs-opts", map[string]any{"mode": "shadow-tls"})},
		{"wireguard peer without port", base("wireguard", "peers", []any{map[string]any{"server": "b.example"}})},
		{"reserved out of range", base("wireguard", "reserved", []any{1, 2, 256})},
		{"reserved bad base64", base("wireguard", "reserved", "!!")},
		{"hysteria2 without port or ports", base("hysteria2", "port", nil)},
	} {
		_, err := Proxy(tc.in)
		if err == nil {
			t.Errorf("%s: accepted", tc.why)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error shows the password: %v", tc.why, err)
		}
	}
}

func TestProxyErrorsHideSecrets(t *testing.T) {
	for _, in := range []map[string]any{
		{"name": "a", "type": "vless", "server": "a.example", "port": "hunter2", "uuid": "hunter2"},
		{"name": "a", "type": "vless", "server": "a.example", "port": 443, "uuid": map[string]any{"x": "hunter2"}},
		{"name": "a", "type": "ss", "server": "a.example", "port": 443, "password": "hunter2", "plugin": "hunter-plugin"},
	} {
		_, err := Proxy(in)
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%v: err = %v", in, err)
		}
	}
}

func TestProxyCoercesLikeMihomo(t *testing.T) {
	p, err := Proxy(map[string]any{"name": 7, "type": "vless", "server": "a.example", "port": "443", "uuid": "u",
		"udp": false, "tls": 1, "alterId": "0", "Skip_Cert_Verify": true, "reality-opts": map[string]any{"short-id": 1234},
		"ws-opts": map[string]any{"max-early-data": float64(2048), "headers": map[string]string{"Host": "b.example"}},
		"alpn":    []string{"h2"}, "xudp": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"server": "a.example", "port": 443, "uuid": "u", "udp": false, "tls": true,
		"skip-cert-verify": true, "reality-opts": map[string]any{"short-id": "1234"},
		"ws-opts": map[string]any{"max-early-data": 2048, "headers": map[string]any{"Host": "b.example"}},
		"alpn":    []any{"h2"}}
	if p.Name != "7" || !reflect.DeepEqual(p.Options, want) {
		t.Errorf("got %q %v\nwant %v", p.Name, p.Options, want)
	}
}

func TestProxyKeepsPortRangesWithoutPort(t *testing.T) {
	for _, in := range []map[string]any{
		{"name": "a", "type": "hysteria2", "server": "a.example", "ports": "1000-2000", "password": "pw"},
		{"name": "a", "type": "mieru", "server": "a.example", "port-range": "1000-2000", "transport": "TCP"},
		{"name": "a", "type": "wireguard", "private-key": "k", "peers": []any{
			map[string]any{"server": "a.example", "port": 51820, "public-key": "p", "allowed-ips": []any{"0.0.0.0/0"}}}},
	} {
		if _, err := Proxy(in); err != nil {
			t.Errorf("%s: %v", in["type"], err)
		}
	}
}

func TestProxyKeepsReservedAsBytes(t *testing.T) {
	p, err := Proxy(map[string]any{"name": "a", "type": "wireguard", "server": "a.example", "port": 1,
		"private-key": "k", "public-key": "p", "reserved": "AQID"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{1, 2, 3}; !reflect.DeepEqual(p.Options["reserved"], want) {
		t.Errorf("reserved = %v, want %v", p.Options["reserved"], want)
	}
}
