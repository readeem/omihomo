// Package core is the boundary between the daemon and the proxy engine it
// hosts. The daemon owns settings, state and clients; a Core only runs what
// it is given. Nothing outside an implementation of this package knows which
// engine that is.
package core

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"github.com/readeem/omihomo/internal/protocol"
)

// Config is everything a Core needs to run. The daemon builds it from its
// typed settings and the active subscription; a Core never reads settings of
// its own and never fetches anything.
type Config struct {
	Mode         string
	TUN          bool
	TUNRedirect  bool
	Tailscale    bool
	TailnetProxy bool
	// PrimaryGroup is the group global mode and Omihomo's own rules send
	// traffic through. Empty, or a name absent from the profile, means the
	// first select group, falling back to the first group of any type.
	PrimaryGroup string
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

// ErrNotRunning is returned by calls that need a running core.
var ErrNotRunning = errors.New("the proxy is not running")

// ErrNotFound is returned when a named group, proxy or connection does not
// exist in the running config.
var ErrNotFound = errors.New("not found")

// ErrStopped is wrapped by a failed Apply that could not put the previous
// config back either, and left the engine stopped.
var ErrStopped = errors.New("the proxy stopped")

// MaxConnections bounds the connections one snapshot lists, so a busy
// machine's list still fits in one protocol message.
const MaxConnections = 1000

// Core runs one proxy engine in-process.
//
// Apply starts the engine on cfg, or reloads a running engine onto it. A
// failed Apply leaves the previous config running, or the engine stopped if
// it was; when the previous config cannot run again either, the engine
// stops and the error wraps ErrStopped. Stop is idempotent. All other calls fail with ErrNotRunning while
// stopped. Implementations must be safe for concurrent use.
type Core interface {
	Apply(ctx context.Context, cfg Config) error
	Stop(ctx context.Context) error

	Groups(ctx context.Context) ([]protocol.Group, error)
	Select(ctx context.Context, group, proxy string) error
	// TestDelay tests proxies of group, every member when proxies is empty,
	// and maps each to its delay in milliseconds, 0 for a failure.
	TestDelay(ctx context.Context, group string, proxies []string) (map[string]int, error)

	Rules(ctx context.Context) ([]protocol.Rule, error)

	// Connections lists the newest connections, at most MaxConnections of
	// them; Open and the totals count every one.
	Connections(ctx context.Context) (protocol.Connections, error)
	// CloseConnections closes the given ids; ids that are already gone are
	// not an error. A nil slice closes every connection.
	CloseConnections(ctx context.Context, ids []string) error
	// Traffic is the throughput over the last second.
	Traffic(ctx context.Context) (protocol.Traffic, error)

	// DialContext opens a connection routed by the running config, as if a
	// local application had made it through the proxy.
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}
