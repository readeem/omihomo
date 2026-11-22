// Package core is the boundary between the daemon and the proxy engine it
// hosts. The daemon owns settings, state and clients. A Core runs the
// configuration the daemon gives it.
package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"

	"github.com/readeem/omihomo/internal/protocol"
)

// Config is everything a Core needs to run. The daemon builds it from its
// typed settings and the active subscription; a Core never reads settings of
// its own and never fetches anything.
type Config struct {
	Core                 protocol.CoreName // Empty uses mihomo.
	Mode                 protocol.Mode
	TUN                  bool
	TUNRedirect          bool
	Tailscale            bool
	TailnetProxy         bool
	OmihomoRulesDisabled bool
	// PrimaryGroup is the group global mode and Omihomo's own rules send
	// traffic through. Empty, or a name absent from the profile, means the
	// first visible select group, falling back to the first visible group.
	// If all groups are hidden, they remain eligible. The chosen group is
	// always visible for control.
	PrimaryGroup string
	// Empty follows GLOBAL; an unavailable explicit destination rejects the config.
	DNSDestination string
	// TUNBypass is the address space TUN routes around.
	TUNBypass []netip.Prefix
	Profile   Profile
}

// Profile is the content of the active subscription, already downloaded and
// allowlisted by the daemon. Proxy providers arrive expanded into Proxies,
// and rule providers as RuleSets.
type Profile struct {
	Proxies  []Proxy
	Groups   []Group
	SubRules map[string][]Rule
	Rules    []Rule
	RuleSets []RuleSet
}

// Proxy is one outbound. Options holds its protocol fields under the names
// subscriptions use for them, for example "server", "port" or "uuid".
type Proxy struct {
	Name    string
	Type    string
	Options map[string]any
}

// revisionKey keeps a revision from confirming a guessed credential.
var revisionKey = rand.Text()

// Revision changes whenever the proxy's type or options do, and never with
// its name alone. It is stable only within one process.
func (p Proxy) Revision() string {
	// Options are decoded YAML, which always encodes; json sorts map keys.
	content, _ := json.Marshal(struct {
		Type    string
		Options map[string]any
	}{p.Type, p.Options})
	mac := hmac.New(sha256.New, []byte(revisionKey))
	mac.Write(content)
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

// Group picks among its Members, which name proxies or other groups.
// Options holds the type's own fields, for example "url", "interval" or
// "tolerance".
type Group struct {
	Name    string
	Type    string
	Members []string
	Options map[string]any
}

// Rule matches traffic and sends it to Target, a group, a proxy, DIRECT or
// REJECT. Value is empty for MATCH. Params are trailing flags such as
// "no-resolve". Source is one of the protocol's rule sources, user or
// subscription; a Core adds the omihomo rules itself.
type Rule struct {
	Type   string
	Value  string
	Target string
	Params []string
	Source string
}

// RuleSet is a named list that RULE-SET rules match against. Behavior is
// "domain", "ipcidr" or "classical", and decides how each Payload line reads.
type RuleSet struct {
	Name     string
	Behavior string
	Payload  []string
}

var (
	// ErrInvalid marks a config rejected before runtime changes, rather than a
	// local failure to start its listeners or devices.
	ErrInvalid = errors.New("invalid proxy configuration")

	// ErrNotRunning is returned by calls that need a running core.
	ErrNotRunning = errors.New("the proxy is not running")

	// ErrNotFound is returned when a named group, proxy or connection does not
	// exist in the running config.
	ErrNotFound = errors.New("not found")

	// ErrStopped is wrapped by a failed Apply that could not put the previous
	// config back either, and left the engine stopped.
	ErrStopped = errors.New("the proxy stopped")
)

// MaxConnections bounds the connections one snapshot lists, so a busy
// machine's list still fits in one protocol message.
const MaxConnections = 1000

// Core runs one proxy engine in-process.
//
// Apply starts the engine on cfg, or reloads a running engine onto it. A
// failed Apply leaves the previous config running, or the engine stopped if
// it was; when the previous config cannot run again either, the engine
// stops and the error wraps ErrStopped. Stop is idempotent. Implementations
// must be safe for concurrent use.
type Core interface {
	Apply(ctx context.Context, cfg Config) error
	// Check reports why cfg would not run, without running it or changing
	// what runs. Geodata it needs must be in place, as for Apply.
	Check(ctx context.Context, cfg Config) error
	Stop(ctx context.Context) error

	// While stopped, Groups and Select use cfg without starting the engine.
	// While running, they use the live config. An empty stopped profile returns ErrNotRunning.
	Groups(ctx context.Context, cfg Config) ([]protocol.Group, error)
	Select(ctx context.Context, cfg Config, group, proxy string) error
	// TestDelay tests proxies of group, or all members other than except when
	// proxies is empty. It returns completed probes in milliseconds, 0 for a failure.
	TestDelay(ctx context.Context, group string, proxies []string, except string) (map[string]int, error)

	Rules(ctx context.Context) ([]protocol.Rule, error)

	// Connections lists the newest connections, at most MaxConnections of
	// them; Open and the totals count every one.
	Connections(ctx context.Context) (protocol.Connections, error)
	// ConnectionHistory also serves the most recent session after Stop.
	ConnectionHistory(ctx context.Context, args protocol.ConnectionHistoryArgs) (protocol.ConnectionHistory, error)
	// CloseConnections closes the given ids; ids that are already gone are
	// not an error. A nil slice closes every connection.
	CloseConnections(ctx context.Context, ids []string) error
	// Traffic is the throughput over the last second.
	Traffic(ctx context.Context) (protocol.Traffic, error)

	// DialContext opens a connection routed by the running config, as if a
	// local application had made it through the proxy.
	DialContext(ctx context.Context, network, address string) (net.Conn, error)

	// DialProxy opens a TCP connection through a named proxy or group.
	DialProxy(ctx context.Context, proxy, address string) (net.Conn, error)

	// DialDownload uses configured DNS and the primary exit, with synchronous failure reporting.
	DialDownload(ctx context.Context, network, address string) (net.Conn, error)

	// ParseLinks reads a list of share links, plain or base64, into proxies
	// written the way a subscription's YAML writes them: "name", "type" and
	// the protocol's fields. They are remote content, still to be
	// allowlisted. It works whether or not the core runs.
	ParseLinks(data []byte) ([]map[string]any, error)

	// Geodata names the files the rules of p match against, with where each
	// is downloaded from. The daemon fetches the ones missing before an
	// Apply; the core never fetches them itself.
	Geodata(p Profile) []GeodataFile
	// InstallGeodata moves the file at path into place as f once it is
	// usable for the rules of p. The core reads a site list on its next
	// Apply, and a database it looks up while routing on its next start. A
	// broken database can end the process, and one without what the rules
	// match fails them, so either leaves what is in place.
	InstallGeodata(f GeodataFile, path string, p Profile) error
}

// Literal destinations keep the proxy from choosing the address family via DNS.
const (
	TraceIPv4URL = "https://1.1.1.1/cdn-cgi/trace"
	TraceIPv6URL = "https://[2606:4700:4700::1111]/cdn-cgi/trace"
)

// GeodataFile is one geodata file a profile needs: Path is absolute, URL is
// HTTPS.
type GeodataFile struct {
	Path string
	URL  string
}
