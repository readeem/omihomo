package mihomo

import (
	"encoding"
	"encoding/base64"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"

	subscription "github.com/readeem/omihomo/internal/profile"
)

// mihomoOptions are the option structs mihomo decodes each modelled proxy
// type into.
var mihomoOptions = map[string]reflect.Type{
	"ss":        reflect.TypeFor[outbound.ShadowSocksOption](),
	"ssr":       reflect.TypeFor[outbound.ShadowSocksROption](),
	"vmess":     reflect.TypeFor[outbound.VmessOption](),
	"vless":     reflect.TypeFor[outbound.VlessOption](),
	"trojan":    reflect.TypeFor[outbound.TrojanOption](),
	"hysteria":  reflect.TypeFor[outbound.HysteriaOption](),
	"hysteria2": reflect.TypeFor[outbound.Hysteria2Option](),
	"tuic":      reflect.TypeFor[outbound.TuicOption](),
	"wireguard": reflect.TypeFor[outbound.WireGuardOption](),
	"snell":     reflect.TypeFor[outbound.SnellOption](),
	"socks5":    reflect.TypeFor[outbound.Socks5Option](),
	"http":      reflect.TypeFor[outbound.HttpOption](),
	"ssh":       reflect.TypeFor[outbound.SshOption](),
	"anytls":    reflect.TypeFor[outbound.AnyTLSOption](),
	"mieru":     reflect.TypeFor[outbound.MieruOption](),
	"direct":    reflect.TypeFor[outbound.DirectOption](),
}

// Every field the profile models is one mihomo reads, under the same key at
// the same depth and of a shape its decoder takes. A mihomo bump that renames
// or moves a field fails here.
func TestModelledProxyFieldsAreMihomos(t *testing.T) {
	for typ, ours := range subscription.ProxyTypes() {
		theirs, ok := mihomoOptions[typ]
		if !ok {
			t.Errorf("no mihomo option struct for %s", typ)
			continue
		}
		compareFields(t, typ, "", ours, theirs)
	}
}

func compareFields(t *testing.T, typ, path string, ours, theirs reflect.Type) {
	t.Helper()
	for _, f := range flatFields(ours, "yaml") {
		key := tagKey(f, "yaml")
		var their reflect.Type
		if path == "" && key == "smux" {
			// ParseProxy reads smux for every type, apart from the type's own
			// options.
			their = reflect.TypeFor[outbound.SingMuxOption]()
		} else if tf, ok := proxyField(theirs, key); ok {
			their = tf.Type
		} else {
			t.Errorf("%s: mihomo has no %s%s", typ, path, key)
			continue
		}
		ourShape, theirShape := shape(f.Type), shape(their)
		if strings.HasSuffix(ourShape, "struct") && theirShape == "map[any]" {
			// mihomo decodes it later, by the rules of what it configures,
			// as with plugin-opts.
			continue
		}
		if ourShape != theirShape {
			t.Errorf("%s: %s%s is %s, mihomo's is %s", typ, path, key, ourShape, theirShape)
			continue
		}
		if strings.HasSuffix(ourShape, "struct") {
			compareFields(t, typ, path+key+".", elem(f.Type), elem(their))
		}
	}
}

// flatFields lists the fields of t, with those of embedded structs that
// carry no tag of their own in its place, the way mihomo's decoder squashes
// them.
func flatFields(t reflect.Type, tag string) []reflect.StructField {
	var fields []reflect.StructField
	for f := range t.Fields() {
		if f.Anonymous && f.Type.Kind() == reflect.Struct && tagKey(f, tag) == "" {
			fields = append(fields, flatFields(f.Type, tag)...)
		} else if key := tagKey(f, tag); key != "" && key != "-" {
			fields = append(fields, f)
		}
	}
	return fields
}

func proxyField(t reflect.Type, key string) (reflect.StructField, bool) {
	for _, f := range flatFields(t, "proxy") {
		if tagKey(f, "proxy") == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func tagKey(f reflect.StructField, tag string) string {
	key, _, _ := strings.Cut(f.Tag.Get(tag), ",")
	return key
}

var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// shape is what kind of value t takes from YAML: a type mihomo parses from
// text is a string, and every integer is a number.
func shape(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return "string"
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.Slice:
		return "[]" + shape(t.Elem())
	case reflect.Map:
		return "map[" + shape(t.Elem()) + "]"
	case reflect.Interface:
		return "any"
	}
	return t.Kind().String()
}

func elem(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t
}

// Mihomo builds a proxy from what the profile keeps of each modelled type.
func TestMihomoBuildsEveryModelledProxy(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	realityKey := base64.RawURLEncoding.EncodeToString(append(make([]byte, 31), 9))
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"
	samples := []map[string]any{
		{"name": "ss", "type": "ss", "server": "a.example", "port": 8388, "cipher": "aes-128-gcm", "password": "pw",
			"udp": true, "ip-version": "ipv4", "smux": map[string]any{"enabled": true, "protocol": "h2mux"}},
		{"name": "ss-obfs", "type": "ss", "server": "a.example", "port": 8388, "cipher": "aes-128-gcm", "password": "pw",
			"plugin": "obfs", "plugin-opts": map[string]any{"mode": "tls", "host": "b.example"}},
		{"name": "ss-v2ray", "type": "ss", "server": "a.example", "port": 8388, "cipher": "aes-128-gcm", "password": "pw",
			"plugin": "v2ray-plugin", "plugin-opts": map[string]any{"mode": "websocket", "host": "b.example",
				"path": "/ws", "tls": true, "mux": false, "headers": map[string]any{"X-Id": "1"}}},
		{"name": "ss-shadow-tls", "type": "ss", "server": "a.example", "port": 443, "cipher": "aes-128-gcm",
			"password": "pw", "client-fingerprint": "chrome", "plugin": "shadow-tls",
			"plugin-opts": map[string]any{"host": "b.example", "password": "pw", "version": 3}},
		{"name": "ss-restls", "type": "ss", "server": "a.example", "port": 443, "cipher": "aes-128-gcm",
			"password": "pw", "client-fingerprint": "chrome", "plugin": "restls",
			"plugin-opts": map[string]any{"host": "b.example", "password": "pw", "version-hint": "tls13",
				"restls-script": "300?100<1,400~100,350~100,600~100,300~200,300~100"}},
		{"name": "ssr", "type": "ssr", "server": "a.example", "port": 443, "cipher": "chacha20-ietf", "password": "pw",
			"obfs": "tls1.2_ticket_auth", "protocol": "auth_sha1_v4", "udp": true},
		{"name": "vmess", "type": "vmess", "server": "a.example", "port": 443, "uuid": uuid, "alterId": 0,
			"cipher": "auto", "udp": true, "tls": true, "servername": "b.example", "network": "ws",
			"alpn": []any{"http/1.1"}, "ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "b.example"}}},
		{"name": "vmess-h2", "type": "vmess", "server": "a.example", "port": 443, "uuid": uuid, "alterId": 0,
			"cipher": "auto", "tls": true, "network": "h2", "h2-opts": map[string]any{"host": []any{"b.example"}, "path": "/"}},
		{"name": "vmess-http", "type": "vmess", "server": "a.example", "port": 80, "uuid": uuid, "alterId": 0,
			"cipher": "auto", "network": "http",
			"http-opts": map[string]any{"method": "GET", "path": []any{"/"}, "headers": map[string]any{"Host": []any{"b.example"}}}},
		{"name": "vless", "type": "vless", "server": "a.example", "port": 443, "uuid": uuid, "flow": "xtls-rprx-vision",
			"udp": true, "tls": true, "servername": "b.example", "client-fingerprint": "chrome", "network": "tcp",
			"reality-opts": map[string]any{"public-key": realityKey, "short-id": "0123"}},
		{"name": "vless-xhttp", "type": "vless", "server": "a.example", "port": 443, "uuid": uuid, "tls": true,
			"network": "xhttp", "xhttp-opts": map[string]any{"path": "/x", "mode": "auto"}},
		{"name": "trojan", "type": "trojan", "server": "a.example", "port": 443, "password": "pw", "sni": "b.example",
			"udp": true, "network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "svc"}},
		{"name": "hysteria", "type": "hysteria", "server": "a.example", "port": 443, "protocol": "udp",
			"up": "30 Mbps", "down": "200 Mbps", "auth-str": "pw", "sni": "b.example", "alpn": []any{"h3"}},
		{"name": "hysteria2", "type": "hysteria2", "server": "a.example", "port": 443, "password": "pw",
			"obfs": "salamander", "obfs-password": "o", "sni": "b.example", "up": "30", "down": "200"},
		{"name": "tuic", "type": "tuic", "server": "a.example", "port": 443, "uuid": uuid, "password": "pw",
			"congestion-controller": "bbr", "udp-relay-mode": "native", "alpn": []any{"h3"}, "sni": "b.example"},
		{"name": "wireguard", "type": "wireguard", "server": "a.example", "port": 51820, "ip": "172.16.0.2",
			"private-key": key, "public-key": key, "reserved": []any{1, 2, 3}, "allowed-ips": []any{"0.0.0.0/0"},
			"udp": true, "mtu": 1280},
		{"name": "snell", "type": "snell", "server": "a.example", "port": 443, "psk": "pw", "version": 3,
			"obfs-opts": map[string]any{"mode": "http", "host": "b.example"}},
		{"name": "socks5", "type": "socks5", "server": "a.example", "port": 1080, "username": "u", "password": "pw", "udp": true},
		{"name": "http", "type": "http", "server": "a.example", "port": 8080, "username": "u", "password": "pw",
			"tls": true, "sni": "b.example", "headers": map[string]any{"X-Id": "1"}},
		{"name": "ssh", "type": "ssh", "server": "a.example", "port": 22, "username": "u", "password": "pw"},
		{"name": "anytls", "type": "anytls", "server": "a.example", "port": 443, "password": "pw", "sni": "b.example",
			"client-fingerprint": "chrome", "udp": true},
		{"name": "mieru", "type": "mieru", "server": "a.example", "port": 2999, "transport": "TCP",
			"username": "u", "password": "pw", "multiplexing": "MULTIPLEXING_LOW"},
		{"name": "direct", "type": "direct", "ip-version": "ipv4-prefer"},
	}
	seen := map[string]bool{}
	for _, in := range samples {
		p, err := subscription.Proxy(in)
		if err != nil {
			t.Errorf("%s: %v", in["name"], err)
			continue
		}
		if len(p.Options) != len(in)-2 {
			t.Errorf("%s: the allowlist dropped some of %v", in["name"], in)
		}
		m := maps.Clone(p.Options)
		m["name"], m["type"] = p.Name, p.Type
		proxy, err := adapter.ParseProxy(m)
		if err != nil {
			t.Errorf("%s: mihomo refuses it: %v", p.Name, err)
			continue
		}
		seen[p.Type] = true
		_ = proxy.Close()
	}
	for typ := range subscription.ProxyTypes() {
		if !seen[typ] {
			t.Errorf("mihomo built no %s proxy", typ)
		}
	}
}
