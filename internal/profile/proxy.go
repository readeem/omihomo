package profile

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/readeem/omihomo/internal/core"
)

// Each modelled proxy type is a struct whose yaml tags are mihomo's own names
// for its fields; a key a struct does not name is dropped. A nil field is
// absent, so an explicit false or 0 survives. Embedded structs share a level
// with their parent.
//
// Never modelled, because they let remote content steer the root process:
// dialer-proxy, interface-name and routing-mark, which pick its upstream,
// interface and firewall mark; wireguard's ip-stack, which can bring up a
// system network stack; hysteria2's realm-opts, which reach a rendezvous
// server of the subscription's choosing.
//
// A field tagged inline:"pem" is one mihomo reads as a file path unless it
// holds PEM, and is dropped unless it does; inline:"ssh" is the same for an
// SSH private key.

// dialOpts are the dialing fields of every type.
type dialOpts struct {
	IPVersion *string `yaml:"ip-version"`
	TFO       *bool   `yaml:"tfo"`
	MPTCP     *bool   `yaml:"mptcp"`
}

// serverOpts are the fields of every type that dials one server.
type serverOpts struct {
	dialOpts `yaml:",inline"`
	Server   *string   `yaml:"server"`
	Port     *int      `yaml:"port"`
	Smux     *smuxOpts `yaml:"smux"`
}

type smuxOpts struct {
	Enabled        *bool       `yaml:"enabled"`
	Protocol       *string     `yaml:"protocol"`
	MaxConnections *int        `yaml:"max-connections"`
	MinStreams     *int        `yaml:"min-streams"`
	MaxStreams     *int        `yaml:"max-streams"`
	Padding        *bool       `yaml:"padding"`
	Statistic      *bool       `yaml:"statistic"`
	OnlyTCP        *bool       `yaml:"only-tcp"`
	BrutalOpts     *brutalOpts `yaml:"brutal-opts"`
}

type brutalOpts struct {
	Enabled *bool   `yaml:"enabled"`
	Up      *string `yaml:"up"`
	Down    *string `yaml:"down"`
}

// certOpts verify the server's certificate and present a client one.
type certOpts struct {
	SkipCertVerify *bool   `yaml:"skip-cert-verify"`
	NameCertVerify *string `yaml:"name-cert-verify"`
	Fingerprint    *string `yaml:"fingerprint"`
	Certificate    *string `yaml:"certificate" inline:"pem"`
	PrivateKey     *string `yaml:"private-key" inline:"pem"`
}

// helloOpts shape the client hello of the types that speak TLS or QUIC.
type helloOpts struct {
	ALPN    []string `yaml:"alpn"`
	ECHOpts *echOpts `yaml:"ech-opts"`
}

type echOpts struct {
	Enable          *bool   `yaml:"enable"`
	Config          *string `yaml:"config"`
	QueryServerName *string `yaml:"query-server-name"`
}

// utlsOpts are the client fingerprint and REALITY of vmess, vless and trojan.
type utlsOpts struct {
	ClientFingerprint *string      `yaml:"client-fingerprint"`
	RealityOpts       *realityOpts `yaml:"reality-opts"`
}

type realityOpts struct {
	PublicKey             *string `yaml:"public-key"`
	ShortID               *string `yaml:"short-id"`
	SupportX25519MLKEM768 *bool   `yaml:"support-x25519mlkem768"`
}

// streamTransport are the transports trojan shares with vmess and vless.
type streamTransport struct {
	Network  *string   `yaml:"network"`
	WSOpts   *wsOpts   `yaml:"ws-opts"`
	GRPCOpts *grpcOpts `yaml:"grpc-opts"`
}

// httpTransport are the HTTP transports of vmess and vless.
type httpTransport struct {
	HTTPOpts *httpOpts `yaml:"http-opts"`
	H2Opts   *h2Opts   `yaml:"h2-opts"`
}

type wsOpts struct {
	Path                     *string           `yaml:"path"`
	Headers                  map[string]string `yaml:"headers"`
	MaxEarlyData             *int              `yaml:"max-early-data"`
	EarlyDataHeaderName      *string           `yaml:"early-data-header-name"`
	V2rayHTTPUpgrade         *bool             `yaml:"v2ray-http-upgrade"`
	V2rayHTTPUpgradeFastOpen *bool             `yaml:"v2ray-http-upgrade-fast-open"`
}

type grpcOpts struct {
	ServiceName *string `yaml:"grpc-service-name"`
	UserAgent   *string `yaml:"grpc-user-agent"`
}

type httpOpts struct {
	Method  *string             `yaml:"method"`
	Path    []string            `yaml:"path"`
	Headers map[string][]string `yaml:"headers"`
}

type h2Opts struct {
	Host []string `yaml:"host"`
	Path *string  `yaml:"path"`
}

type xhttpOpts struct {
	Path          *string           `yaml:"path"`
	Host          *string           `yaml:"host"`
	Mode          *string           `yaml:"mode"`
	Headers       map[string]string `yaml:"headers"`
	NoGRPCHeader  *bool             `yaml:"no-grpc-header"`
	XPaddingBytes *string           `yaml:"x-padding-bytes"`
}

type shadowsocks struct {
	serverOpts        `yaml:",inline"`
	Cipher            *string       `yaml:"cipher"`
	Password          *string       `yaml:"password"`
	UDP               *bool         `yaml:"udp"`
	UDPOverTCP        *bool         `yaml:"udp-over-tcp"`
	UDPOverTCPVersion *int          `yaml:"udp-over-tcp-version"`
	ClientFingerprint *string       `yaml:"client-fingerprint"`
	Plugin            *string       `yaml:"plugin"`
	PluginOpts        *ssPluginOpts `yaml:"plugin-opts"`
}

// ssPluginOpts holds the options of every modelled plugin; check keeps only
// those of the plugin in use.
type ssPluginOpts struct {
	certOpts                 `yaml:",inline"`
	Mode                     *string           `yaml:"mode"`
	Host                     *string           `yaml:"host"`
	Path                     *string           `yaml:"path"`
	TLS                      *bool             `yaml:"tls"`
	Headers                  map[string]string `yaml:"headers"`
	Mux                      *bool             `yaml:"mux"`
	V2rayHTTPUpgrade         *bool             `yaml:"v2ray-http-upgrade"`
	V2rayHTTPUpgradeFastOpen *bool             `yaml:"v2ray-http-upgrade-fast-open"`
	Password                 *string           `yaml:"password"`
	Version                  *int              `yaml:"version"`
	ALPN                     []string          `yaml:"alpn"`
	VersionHint              *string           `yaml:"version-hint"`
	RestlsScript             *string           `yaml:"restls-script"`
}

// ssPlugins are the modelled shadowsocks plugins and their options. The
// others, kcptun, gost-plugin and jls among them, drop the proxy.
var ssPlugins = map[string][]string{
	"obfs": {"mode", "host"},
	"v2ray-plugin": {"mode", "host", "path", "tls", "headers", "mux", "v2ray-http-upgrade", "v2ray-http-upgrade-fast-open",
		"skip-cert-verify", "name-cert-verify", "fingerprint", "certificate", "private-key"},
	"shadow-tls": {"host", "password", "version", "alpn",
		"skip-cert-verify", "name-cert-verify", "fingerprint", "certificate", "private-key"},
	"restls": {"host", "password", "version-hint", "restls-script", "skip-cert-verify", "name-cert-verify", "fingerprint"},
}

type shadowsocksR struct {
	serverOpts    `yaml:",inline"`
	Cipher        *string `yaml:"cipher"`
	Password      *string `yaml:"password"`
	Obfs          *string `yaml:"obfs"`
	ObfsParam     *string `yaml:"obfs-param"`
	Protocol      *string `yaml:"protocol"`
	ProtocolParam *string `yaml:"protocol-param"`
	UDP           *bool   `yaml:"udp"`
}

type vmess struct {
	serverOpts          `yaml:",inline"`
	certOpts            `yaml:",inline"`
	helloOpts           `yaml:",inline"`
	utlsOpts            `yaml:",inline"`
	streamTransport     `yaml:",inline"`
	httpTransport       `yaml:",inline"`
	UUID                *string `yaml:"uuid"`
	AlterID             *int    `yaml:"alterId"`
	Cipher              *string `yaml:"cipher"`
	UDP                 *bool   `yaml:"udp"`
	TLS                 *bool   `yaml:"tls"`
	ServerName          *string `yaml:"servername"`
	PacketEncoding      *string `yaml:"packet-encoding"`
	XUDP                *bool   `yaml:"xudp"`
	GlobalPadding       *bool   `yaml:"global-padding"`
	AuthenticatedLength *bool   `yaml:"authenticated-length"`
}

type vless struct {
	serverOpts      `yaml:",inline"`
	certOpts        `yaml:",inline"`
	helloOpts       `yaml:",inline"`
	utlsOpts        `yaml:",inline"`
	streamTransport `yaml:",inline"`
	httpTransport   `yaml:",inline"`
	UUID            *string    `yaml:"uuid"`
	Flow            *string    `yaml:"flow"`
	Encryption      *string    `yaml:"encryption"`
	UDP             *bool      `yaml:"udp"`
	TLS             *bool      `yaml:"tls"`
	ServerName      *string    `yaml:"servername"`
	PacketEncoding  *string    `yaml:"packet-encoding"`
	XUDP            *bool      `yaml:"xudp"`
	XHTTPOpts       *xhttpOpts `yaml:"xhttp-opts"`
}

type trojan struct {
	serverOpts      `yaml:",inline"`
	certOpts        `yaml:",inline"`
	helloOpts       `yaml:",inline"`
	utlsOpts        `yaml:",inline"`
	streamTransport `yaml:",inline"`
	Password        *string       `yaml:"password"`
	SNI             *string       `yaml:"sni"`
	UDP             *bool         `yaml:"udp"`
	SSOpts          *trojanSSOpts `yaml:"ss-opts"`
}

type trojanSSOpts struct {
	Enabled  *bool   `yaml:"enabled"`
	Method   *string `yaml:"method"`
	Password *string `yaml:"password"`
}

type hysteria struct {
	serverOpts          `yaml:",inline"`
	certOpts            `yaml:",inline"`
	helloOpts           `yaml:",inline"`
	Ports               *string `yaml:"ports"`
	Protocol            *string `yaml:"protocol"`
	ObfsProtocol        *string `yaml:"obfs-protocol"`
	Up                  *string `yaml:"up"`
	UpSpeed             *int    `yaml:"up-speed"`
	Down                *string `yaml:"down"`
	DownSpeed           *int    `yaml:"down-speed"`
	Auth                *string `yaml:"auth"`
	AuthStr             *string `yaml:"auth-str"`
	Obfs                *string `yaml:"obfs"`
	SNI                 *string `yaml:"sni"`
	RecvWindowConn      *int    `yaml:"recv-window-conn"`
	RecvWindow          *int    `yaml:"recv-window"`
	DisableMTUDiscovery *bool   `yaml:"disable-mtu-discovery"`
	FastOpen            *bool   `yaml:"fast-open"`
	HopInterval         *int    `yaml:"hop-interval"`
}

type hysteria2 struct {
	serverOpts   `yaml:",inline"`
	certOpts     `yaml:",inline"`
	helloOpts    `yaml:",inline"`
	Ports        *string `yaml:"ports"`
	HopInterval  *string `yaml:"hop-interval"`
	Up           *string `yaml:"up"`
	Down         *string `yaml:"down"`
	Password     *string `yaml:"password"`
	Obfs         *string `yaml:"obfs"`
	ObfsPassword *string `yaml:"obfs-password"`
	SNI          *string `yaml:"sni"`
	CWND         *int    `yaml:"cwnd"`
}

type tuic struct {
	serverOpts            `yaml:",inline"`
	certOpts              `yaml:",inline"`
	helloOpts             `yaml:",inline"`
	Token                 *string `yaml:"token"`
	UUID                  *string `yaml:"uuid"`
	Password              *string `yaml:"password"`
	IP                    *string `yaml:"ip"`
	HeartbeatInterval     *int    `yaml:"heartbeat-interval"`
	ReduceRTT             *bool   `yaml:"reduce-rtt"`
	RequestTimeout        *int    `yaml:"request-timeout"`
	UDPRelayMode          *string `yaml:"udp-relay-mode"`
	CongestionController  *string `yaml:"congestion-controller"`
	DisableSNI            *bool   `yaml:"disable-sni"`
	MaxUDPRelayPacketSize *int    `yaml:"max-udp-relay-packet-size"`
	FastOpen              *bool   `yaml:"fast-open"`
	MaxOpenStreams        *int    `yaml:"max-open-streams"`
	SNI                   *string `yaml:"sni"`
}

// wireguard's keys are inline base64, which mihomo never reads as paths.
type wireguard struct {
	dialOpts            `yaml:",inline"`
	wireguardPeer       `yaml:",inline"`
	IP                  *string         `yaml:"ip"`
	IPv6                *string         `yaml:"ipv6"`
	PrivateKey          *string         `yaml:"private-key"`
	MTU                 *int            `yaml:"mtu"`
	UDP                 *bool           `yaml:"udp"`
	PersistentKeepalive *int            `yaml:"persistent-keepalive"`
	AmneziaWGOption     *amneziaOpts    `yaml:"amnezia-wg-option"`
	Peers               []wireguardPeer `yaml:"peers"`
	RemoteDNSResolve    *bool           `yaml:"remote-dns-resolve"`
	DNS                 []string        `yaml:"dns"`
}

type wireguardPeer struct {
	Server       *string  `yaml:"server"`
	Port         *int     `yaml:"port"`
	PublicKey    *string  `yaml:"public-key"`
	PreSharedKey *string  `yaml:"pre-shared-key"`
	Reserved     []uint8  `yaml:"reserved"`
	AllowedIPs   []string `yaml:"allowed-ips"`
}

type amneziaOpts struct {
	JC   *int    `yaml:"jc"`
	JMin *int    `yaml:"jmin"`
	JMax *int    `yaml:"jmax"`
	S1   *int    `yaml:"s1"`
	S2   *int    `yaml:"s2"`
	S3   *int    `yaml:"s3"`
	S4   *int    `yaml:"s4"`
	H1   *string `yaml:"h1"`
	H2   *string `yaml:"h2"`
	H3   *string `yaml:"h3"`
	H4   *string `yaml:"h4"`
	I1   *string `yaml:"i1"`
	I2   *string `yaml:"i2"`
	I3   *string `yaml:"i3"`
	I4   *string `yaml:"i4"`
	I5   *string `yaml:"i5"`
}

type snell struct {
	serverOpts `yaml:",inline"`
	PSK        *string        `yaml:"psk"`
	UDP        *bool          `yaml:"udp"`
	Version    *int           `yaml:"version"`
	ObfsOpts   *snellObfsOpts `yaml:"obfs-opts"`
}

// snellObfsOpts is simple-obfs; the TLS-camouflage modes are not modelled.
type snellObfsOpts struct {
	Mode *string `yaml:"mode"`
	Host *string `yaml:"host"`
}

type socks5 struct {
	serverOpts `yaml:",inline"`
	certOpts   `yaml:",inline"`
	Username   *string `yaml:"username"`
	Password   *string `yaml:"password"`
	TLS        *bool   `yaml:"tls"`
	UDP        *bool   `yaml:"udp"`
}

type httpProxy struct {
	serverOpts `yaml:",inline"`
	certOpts   `yaml:",inline"`
	Username   *string           `yaml:"username"`
	Password   *string           `yaml:"password"`
	TLS        *bool             `yaml:"tls"`
	SNI        *string           `yaml:"sni"`
	Headers    map[string]string `yaml:"headers"`
}

type sshProxy struct {
	serverOpts           `yaml:",inline"`
	Username             *string  `yaml:"username"`
	Password             *string  `yaml:"password"`
	PrivateKey           *string  `yaml:"private-key" inline:"ssh"`
	PrivateKeyPassphrase *string  `yaml:"private-key-passphrase"`
	HostKey              []string `yaml:"host-key"`
	HostKeyAlgorithms    []string `yaml:"host-key-algorithms"`
}

type anytls struct {
	serverOpts               `yaml:",inline"`
	certOpts                 `yaml:",inline"`
	helloOpts                `yaml:",inline"`
	Password                 *string `yaml:"password"`
	SNI                      *string `yaml:"sni"`
	ClientFingerprint        *string `yaml:"client-fingerprint"`
	UDP                      *bool   `yaml:"udp"`
	IdleSessionCheckInterval *int    `yaml:"idle-session-check-interval"`
	IdleSessionTimeout       *int    `yaml:"idle-session-timeout"`
	MinIdleSession           *int    `yaml:"min-idle-session"`
}

type mieru struct {
	serverOpts    `yaml:",inline"`
	PortRange     *string `yaml:"port-range"`
	Transport     *string `yaml:"transport"`
	UDP           *bool   `yaml:"udp"`
	Username      *string `yaml:"username"`
	Password      *string `yaml:"password"`
	Multiplexing  *string `yaml:"multiplexing"`
	HandshakeMode *string `yaml:"handshake-mode"`
}

type direct struct {
	dialOpts `yaml:",inline"`
}

var proxyTypes = map[string]reflect.Type{
	"ss":        reflect.TypeFor[shadowsocks](),
	"ssr":       reflect.TypeFor[shadowsocksR](),
	"vmess":     reflect.TypeFor[vmess](),
	"vless":     reflect.TypeFor[vless](),
	"trojan":    reflect.TypeFor[trojan](),
	"hysteria":  reflect.TypeFor[hysteria](),
	"hysteria2": reflect.TypeFor[hysteria2](),
	"tuic":      reflect.TypeFor[tuic](),
	"wireguard": reflect.TypeFor[wireguard](),
	"snell":     reflect.TypeFor[snell](),
	"socks5":    reflect.TypeFor[socks5](),
	"http":      reflect.TypeFor[httpProxy](),
	"ssh":       reflect.TypeFor[sshProxy](),
	"anytls":    reflect.TypeFor[anytls](),
	"mieru":     reflect.TypeFor[mieru](),
	"direct":    reflect.TypeFor[direct](),
}

// ProxyTypes maps each modelled proxy type to its typed struct, for the test
// that checks every modelled field against the core's own.
func ProxyTypes() map[string]reflect.Type {
	return maps.Clone(proxyTypes)
}

// Proxy keeps what omihomo models of one proxy as a subscription writes it,
// or returns an error saying why the proxy is dropped.
func Proxy(m map[string]any) (core.Proxy, error) {
	name, err := optString(m["name"])
	if err != nil || name == "" {
		return core.Proxy{}, errors.New("a proxy has no name")
	}
	typ, _ := m["type"].(string)
	if typ == "" {
		return core.Proxy{}, fmt.Errorf("proxy %q has no type", name)
	}
	t, ok := proxyTypes[typ]
	if !ok {
		return core.Proxy{}, fmt.Errorf("proxy %q: type %q is not supported", name, typ)
	}
	v := reflect.New(t)
	if err := readStruct(m, v.Elem(), ""); err != nil {
		return core.Proxy{}, fmt.Errorf("proxy %q: %w", name, err)
	}
	if c, ok := v.Interface().(interface{ check() error }); ok {
		if err := c.check(); err != nil {
			return core.Proxy{}, fmt.Errorf("proxy %q: %w", name, err)
		}
	}
	opts := map[string]any{}
	writeStruct(v.Elem(), opts)
	keyPairs(opts, typ != "ssh" && typ != "wireguard")
	return core.Proxy{Name: name, Type: typ, Options: opts}, nil
}

// keyPairs keeps a client certificate and its key only as a pair that
// loads: mihomo reads both as file paths when they do not, whatever they
// look like. top says whether m's own fields are TLS ones; an ssh or a
// wireguard key is not.
func keyPairs(m map[string]any, top bool) {
	if top {
		cert, hasCert := m["certificate"].(string)
		key, hasKey := m["private-key"].(string)
		if hasCert || hasKey {
			if _, err := tls.X509KeyPair([]byte(cert), []byte(key)); err != nil {
				delete(m, "certificate")
				delete(m, "private-key")
			}
		}
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			keyPairs(sub, true)
		}
	}
}

func (s *serverOpts) check() error {
	return checkEndpoint(s.Server, s.Port, false)
}

func (p *shadowsocks) check() error {
	if err := p.serverOpts.check(); err != nil {
		return err
	}
	if p.Plugin == nil || *p.Plugin == "" {
		p.PluginOpts = nil
		return nil
	}
	keys, ok := ssPlugins[*p.Plugin]
	if !ok {
		return fmt.Errorf("plugin %q is not supported", *p.Plugin)
	}
	if p.PluginOpts != nil {
		keepFields(reflect.ValueOf(p.PluginOpts).Elem(), keys)
	}
	return nil
}

// hysteriaProtocols leaves out faketcp, for which mihomo runs iptables.
var hysteriaProtocols = []string{"", "udp", "wechat-video"}

func (p *hysteria) check() error {
	for _, proto := range []*string{p.Protocol, p.ObfsProtocol} {
		if proto != nil && !slices.Contains(hysteriaProtocols, *proto) {
			return fmt.Errorf("protocol %q is not supported", *proto)
		}
	}
	if p.HopInterval != nil && *p.HopInterval > maxHopInterval {
		return fmt.Errorf("hop-interval must be at most %d seconds", maxHopInterval)
	}
	return checkEndpoint(p.Server, p.Port, p.Ports != nil)
}

// maxHopInterval bounds a port hop interval in seconds. mihomo turns it into
// a ticker, and one that overflows the duration panics where nothing can
// recover it.
const maxHopInterval = 24 * 60 * 60

func (p *hysteria2) check() error {
	if p.HopInterval != nil {
		lo, hi, ok := strings.Cut(*p.HopInterval, "-")
		if !ok {
			hi = lo
		}
		a, err1 := strconv.ParseUint(strings.TrimSpace(lo), 10, 32)
		b, err2 := strconv.ParseUint(strings.TrimSpace(hi), 10, 32)
		if err1 != nil || err2 != nil || a > b || b > maxHopInterval {
			return fmt.Errorf("hop-interval must be seconds, or a range of them, up to %d", maxHopInterval)
		}
	}
	return checkEndpoint(p.Server, p.Port, p.Ports != nil)
}

func (p *mieru) check() error {
	return checkEndpoint(p.Server, p.Port, p.PortRange != nil)
}

// check needs an endpoint on every peer, or on the proxy itself when it
// lists none.
func (p *wireguard) check() error {
	if len(p.Peers) == 0 {
		return checkEndpoint(p.Server, p.Port, false)
	}
	for i, peer := range p.Peers {
		if err := checkEndpoint(peer.Server, peer.Port, false); err != nil {
			return fmt.Errorf("peer %d: %w", i+1, err)
		}
	}
	return nil
}

func (p *snell) check() error {
	if p.ObfsOpts != nil && p.ObfsOpts.Mode != nil && !slices.Contains([]string{"", "tls", "http"}, *p.ObfsOpts.Mode) {
		return fmt.Errorf("obfs mode %q is not supported", *p.ObfsOpts.Mode)
	}
	return p.serverOpts.check()
}

// checkEndpoint needs a server and a valid port, which a type that also
// takes a port range may leave out.
func checkEndpoint(server *string, port *int, portOptional bool) error {
	switch {
	case server == nil || *server == "":
		return errors.New("server is missing")
	case port == nil && !portOptional:
		return errors.New("port is missing")
	case port != nil && (*port < 1 || *port > 65535):
		return errors.New("port is out of range")
	}
	return nil
}

// readStruct fills v from the keys of m its fields name, coercing values the
// way mihomo's own decoder does. Keys it does not name are left behind.
func readStruct(m map[string]any, v reflect.Value, path string) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			if err := readStruct(m, v.Field(i), path); err != nil {
				return err
			}
			continue
		}
		key := fieldKey(f)
		raw, ok := lookup(m, key)
		if !ok || raw == nil {
			continue
		}
		if err := readValue(raw, v.Field(i), path+key); err != nil {
			return err
		}
		if kind := f.Tag.Get("inline"); kind != "" && !isInline(kind, v.Field(i).Elem().String()) {
			v.Field(i).SetZero()
		}
	}
	return nil
}

// lookup finds key in m as mihomo does: exactly, else ignoring case and
// reading underscores as dashes.
func lookup(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if strings.EqualFold(strings.ReplaceAll(k, "_", "-"), key) {
			return m[k], true
		}
	}
	return nil, false
}

// isInline reports whether s holds what mihomo reads inline rather than as
// a path to a file.
func isInline(kind, s string) bool {
	// mihomo falls back to reading a file when PEM does not decode, so
	// only PEM that does counts as inline.
	block, _ := pem.Decode([]byte(strings.TrimSpace(s)))
	if kind == "ssh" {
		// mihomo reads the key inline only when it says PRIVATE KEY.
		return block != nil && strings.Contains(s, "PRIVATE KEY")
	}
	return block != nil
}

func readValue(raw any, v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		if err := readValue(raw, p.Elem(), path); err != nil {
			return err
		}
		v.Set(p)
	case reflect.String:
		s, err := optString(raw)
		if err != nil {
			return fmt.Errorf("%s is not a string", path)
		}
		v.SetString(s)
	case reflect.Int:
		n, err := optInt(raw)
		if err != nil {
			return fmt.Errorf("%s is not a whole number", path)
		}
		v.SetInt(int64(n))
	case reflect.Uint8:
		n, err := optInt(raw)
		if err != nil || n < 0 || n > math.MaxUint8 {
			return fmt.Errorf("%s is not a byte", path)
		}
		v.SetUint(uint64(n))
	case reflect.Bool:
		b, err := optBool(raw)
		if err != nil {
			return fmt.Errorf("%s is not true or false", path)
		}
		v.SetBool(b)
	case reflect.Struct:
		m, ok := optMap(raw)
		if !ok {
			return fmt.Errorf("%s is not a map", path)
		}
		return readStruct(m, v, path+".")
	case reflect.Slice:
		if s, ok := raw.(string); ok && v.Type().Elem().Kind() == reflect.Uint8 {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return fmt.Errorf("%s is not base64", path)
			}
			v.SetBytes(b)
			return nil
		}
		rv := reflect.ValueOf(raw)
		if rv.Kind() != reflect.Slice {
			return fmt.Errorf("%s is not a list", path)
		}
		out := reflect.MakeSlice(v.Type(), rv.Len(), rv.Len())
		for i := range rv.Len() {
			if err := readValue(rv.Index(i).Interface(), out.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		v.Set(out)
	case reflect.Map:
		m, ok := optMap(raw)
		if !ok {
			return fmt.Errorf("%s is not a map", path)
		}
		out := reflect.MakeMapWithSize(v.Type(), len(m))
		for _, k := range slices.Sorted(maps.Keys(m)) {
			e := reflect.New(v.Type().Elem()).Elem()
			if err := readValue(m[k], e, path+"."+k); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(k), e)
		}
		v.Set(out)
	default:
		panic("profile: unmodelled field kind " + v.Kind().String())
	}
	return nil
}

// optString takes a string, or a number written without quotes.
func optString(raw any) (string, error) {
	rv := reflect.ValueOf(raw)
	switch {
	case rv.Kind() == reflect.String:
		return rv.String(), nil
	case rv.CanInt():
		return strconv.FormatInt(rv.Int(), 10), nil
	case rv.CanUint():
		return strconv.FormatUint(rv.Uint(), 10), nil
	case rv.CanFloat():
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), nil
	}
	return "", errors.New("not a string")
}

// optInt takes a whole number, or one written as a decimal string.
func optInt(raw any) (int, error) {
	rv := reflect.ValueOf(raw)
	switch {
	case rv.CanInt() && rv.Int() >= math.MinInt && rv.Int() <= math.MaxInt:
		return checkInt(int(rv.Int()))
	case rv.CanUint() && rv.Uint() <= math.MaxInt:
		return checkInt(int(rv.Uint()))
	case rv.CanFloat() && rv.Float() == math.Trunc(rv.Float()) && math.Abs(rv.Float()) < 1<<53:
		return checkInt(int(rv.Float()))
	case rv.Kind() == reflect.String:
		n, err := strconv.ParseInt(rv.String(), 10, 0)
		if err != nil {
			return 0, err
		}
		return checkInt(int(n))
	}
	return 0, errors.New("not a whole number")
}

// checkInt keeps every proxy number between 0 and MaxInt32. No proxy field
// is negative, and mihomo turns some into durations or buffer sizes without
// checking them, where a negative or huge one panics.
func checkInt(n int) (int, error) {
	if n < 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%d is out of range", n)
	}
	return n, nil
}

// optBool takes a bool, or a number that is true unless 0. Like mihomo, it
// takes no strings.
func optBool(raw any) (bool, error) {
	rv := reflect.ValueOf(raw)
	switch {
	case rv.Kind() == reflect.Bool:
		return rv.Bool(), nil
	case rv.CanInt():
		return rv.Int() != 0, nil
	case rv.CanUint():
		return rv.Uint() != 0, nil
	}
	return false, errors.New("not a bool")
}

// optMap takes any map with string keys.
func optMap(raw any) (map[string]any, bool) {
	if m, ok := raw.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(raw)
	if rv.Kind() != reflect.Map {
		return nil, false
	}
	m := make(map[string]any, rv.Len())
	for it := rv.MapRange(); it.Next(); {
		k, ok := it.Key().Interface().(string)
		if !ok {
			return nil, false
		}
		m[k] = it.Value().Interface()
	}
	return m, true
}

// writeStruct puts the fields of v that are present into out, under their
// keys.
func writeStruct(v reflect.Value, out map[string]any) {
	t := v.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		switch {
		case f.Anonymous:
			writeStruct(fv, out)
		case !fv.IsNil():
			out[fieldKey(f)] = writeValue(fv)
		}
	}
}

func writeValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Pointer:
		return writeValue(v.Elem())
	case reflect.String:
		return v.String()
	case reflect.Int:
		return int(v.Int())
	case reflect.Uint8:
		return int(v.Uint())
	case reflect.Bool:
		return v.Bool()
	case reflect.Struct:
		m := map[string]any{}
		writeStruct(v, m)
		return m
	case reflect.Slice:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = writeValue(v.Index(i))
		}
		return out
	case reflect.Map:
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			out[it.Key().String()] = writeValue(it.Value())
		}
		return out
	}
	panic("profile: unmodelled field kind " + v.Kind().String())
}

// keepFields clears the fields of v whose keys are not in keys.
func keepFields(v reflect.Value, keys []string) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			keepFields(v.Field(i), keys)
		} else if !slices.Contains(keys, fieldKey(f)) {
			v.Field(i).SetZero()
		}
	}
}

func fieldKey(f reflect.StructField) string {
	key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	return key
}
