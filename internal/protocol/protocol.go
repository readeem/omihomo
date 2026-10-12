// Package protocol is the contract between the omihomo daemon and its
// clients: the widget and the CLI. It is JSON lines over a unix socket.
//
// The daemon speaks first. An accepted client receives one Hello; a refused
// one receives one Refused and the connection closes. After the greeting the
// client sends Requests, each answered by exactly one Reply with the same id,
// and the daemon interleaves Events for the streams the client subscribed to.
// Replies can arrive out of request order: a delay test does not hold up a
// status read.
//
// The protocol speaks of proxies, groups, rules and connections. It never
// names the core that implements them.
package protocol

import "encoding/json"

// Version is bumped on any change an older peer would misread. A client
// that gets a different version from Hello must not send requests; the
// widget shows "update core" instead.
const Version = 1

// MaxLine bounds one message in either direction, newline excluded.
const MaxLine = 1 << 20

// Message types, the "type" field of everything the daemon sends.
const (
	TypeHello   = "hello"
	TypeRefused = "refused"
	TypeReply   = "reply"
	TypeEvent   = "event"
)

// Hello greets an accepted client.
type Hello struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
}

// Refused is the only thing a client the daemon will not serve receives.
type Refused struct {
	Type  string `json:"type"`
	Error *Error `json:"error"`
}

// Request is one call from a client. Id is chosen by the client, must be
// positive, and is echoed in the Reply.
type Request struct {
	ID   uint64          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Reply answers one Request. Exactly one of Result and Error is set.
type Reply struct {
	Type   string `json:"type"`
	ID     uint64 `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Event is a push on a subscribed stream. An event whose value cannot be
// sent carries Error instead of Data: until the next push, the subscriber's
// copy of the stream is stale.
type Event struct {
	Type   string `json:"type"`
	Stream string `json:"stream"`
	Data   any    `json:"data,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Envelope is what a client decodes every daemon message into before it
// knows the type. Result and Data stay raw for the caller to decode.
type Envelope struct {
	Type     string          `json:"type"`
	Protocol int             `json:"protocol,omitempty"`
	Version  string          `json:"version,omitempty"`
	ID       uint64          `json:"id,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Error          `json:"error,omitempty"`
	Stream   string          `json:"stream,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Error is a failed call or a refusal.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Error codes.
const (
	CodeBadRequest  = "bad_request" // not a request, or args that do not decode
	CodeUnknownOp   = "unknown_op"
	CodeInvalid     = "invalid"     // args decode but a value is not allowed
	CodeNotFound    = "not_found"   // a named group, proxy or connection does not exist
	CodeNotRunning  = "not_running" // the call needs the proxy running
	CodeConflict    = "conflict"    // the call contradicts the current state
	CodeUnsupported = "unsupported" // a known op this daemon does not implement yet
	CodeBusy        = "busy"        // too many calls or clients at once
	CodeForbidden   = "forbidden"   // the peer is not the owner
	CodeCore        = "core_failed" // the core refused or failed the change
	CodeTooLarge    = "too_large"   // the result does not fit in one message
	CodeInternal    = "internal"
)

// Operations. Args and results are the types named beside each.
const (
	OpStatus      = "status"      // nil -> Status
	OpSubscribe   = "subscribe"   // StreamsArgs -> Empty
	OpUnsubscribe = "unsubscribe" // StreamsArgs -> Empty

	OpStart = "start" // nil -> Status
	OpStop  = "stop"  // nil -> Status

	OpSetSettings = "settings.set" // SettingsArgs -> Status

	OpSubscriptions      = "subscriptions.list"     // nil -> SubscriptionList
	OpSubscriptionAdd    = "subscriptions.add"      // SubscriptionURLArgs -> Subscription
	OpSubscriptionUpdate = "subscriptions.update"   // NameArgs -> Subscription
	OpSubscriptionUse    = "subscriptions.activate" // NameArgs -> Status
	OpSubscriptionRemove = "subscriptions.remove"   // NameArgs -> Empty
	OpRules              = "rules.list"             // nil -> RuleList
	OpRuleAdd            = "rules.add"              // RuleArgs -> RuleList
	OpRuleRemove         = "rules.remove"           // IndexArgs -> RuleList
	OpGroups             = "groups.list"            // nil -> GroupList
	OpSelect             = "groups.select"          // SelectArgs -> Group
	OpDelayTest          = "delay.test"             // DelayArgs -> DelayResult
	OpConnections        = "connections.list"       // nil -> Connections
	OpCloseConnections   = "connections.close"      // CloseArgs -> Empty
	OpEgress             = "egress.check"           // nil -> Egress
)

// Streams a client can subscribe to. Subscribing to a state stream (status,
// groups, rules) pushes its current value before the reply, and then every
// change, whoever made it.
const (
	StreamStatus      = "status"      // Status, whenever it changes
	StreamGroups      = "groups"      // GroupList, on a selection, a delay test or a new config
	StreamRules       = "rules"       // RuleList, whenever the runtime's rules change
	StreamTraffic     = "traffic"     // Traffic, once a second while running
	StreamConnections = "connections" // Connections, once a second while running
)

// Proxy states.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateOn       = "on"
	StateDegraded = "degraded"
)

// Modes.
const (
	ModeRule   = "rule"
	ModeGlobal = "global"
	ModeDirect = "direct"
)

// Rule sources, in runtime order: the owner's rules come first, then the
// ones omihomo adds for its integrations, then the subscription's.
const (
	SourceUser         = "user"
	SourceOmihomo      = "omihomo"
	SourceSubscription = "subscription"
)

// Empty is the result of a call that has nothing to say.
type Empty struct{}

// Status is everything the bar icon and the panel header show.
type Status struct {
	State              string `json:"state"`
	Detail             string `json:"detail"`
	ActiveSubscription string `json:"active_subscription"`
	// PrimaryGroup is the group whose selection is "the current proxy".
	// Empty means the first selectable group.
	PrimaryGroup     string `json:"primary_group"`
	Mode             string `json:"mode"`
	TUN              bool   `json:"tun"`
	TUNRedirect      bool   `json:"tun_redirect"`
	Tailscale        bool   `json:"tailscale"`
	TailscalePresent bool   `json:"tailscale_present"`
	TailnetProxy     bool   `json:"tailnet_proxy"`
	// StartedAt is unix milliseconds of the current run, 0 when stopped. A
	// new value means a new runtime: anything read from the old one is stale.
	StartedAt int64 `json:"started_at"`
	// TUNBypass lists the CIDRs TUN routes around, for the rule tester.
	TUNBypass []string `json:"tun_bypass"`
}

// StreamsArgs names streams to subscribe to or drop.
type StreamsArgs struct {
	Streams []string `json:"streams"`
}

// SettingsArgs changes any subset of the typed settings. Absent fields are
// left alone.
type SettingsArgs struct {
	Mode         *string `json:"mode,omitempty"`
	TUN          *bool   `json:"tun,omitempty"`
	TUNRedirect  *bool   `json:"tun_redirect,omitempty"`
	Tailscale    *bool   `json:"tailscale,omitempty"`
	TailnetProxy *bool   `json:"tailnet_proxy,omitempty"`
	PrimaryGroup *string `json:"primary_group,omitempty"`
}

// Subscription is one remote source of groups, proxies and rules.
type Subscription struct {
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
	Upload    *int64 `json:"upload"`
	Download  *int64 `json:"download"`
	Total     *int64 `json:"total"`
	Expire    *int64 `json:"expire"`
	Active    bool   `json:"active"`
}

// SubscriptionList is every subscription, in the order they were added.
type SubscriptionList struct {
	Subscriptions []Subscription `json:"subscriptions"`
}

// SubscriptionURLArgs adds a subscription. The name comes from the source.
type SubscriptionURLArgs struct {
	URL string `json:"url"`
}

// NameArgs names a subscription.
type NameArgs struct {
	Name string `json:"name"`
}

// Rule is one traffic rule.
type Rule struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Target string `json:"target"`
	Source string `json:"source"`
}

// RuleList is the runtime's rules in match order.
type RuleList struct {
	Rules []Rule `json:"rules"`
}

// RuleArgs adds one of the owner's rules.
type RuleArgs struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Target string `json:"target"`
}

// IndexArgs removes the owner's rule at a 1-based index.
type IndexArgs struct {
	Index int `json:"index"`
}

// Proxy is one member of a group: a proxy, or another group.
type Proxy struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Alive bool   `json:"alive"`
	// Delay is the last measured delay in milliseconds, 0 when unknown or
	// failed.
	Delay int `json:"delay"`
}

// Group is a set of proxies with one selected.
type Group struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Now        string  `json:"now"`
	Selectable bool    `json:"selectable"`
	Members    []Proxy `json:"members"`
}

// GroupList is the running config's groups.
type GroupList struct {
	Groups []Group `json:"groups"`
}

// SelectArgs selects Proxy in Group.
type SelectArgs struct {
	Group string `json:"group"`
	Proxy string `json:"proxy"`
}

// DelayArgs tests Proxies in Group, or the whole group when Proxies is
// empty.
type DelayArgs struct {
	Group   string   `json:"group"`
	Proxies []string `json:"proxies,omitempty"`
}

// DelayResult maps each tested proxy to its delay in milliseconds; 0 is a
// failed test.
type DelayResult struct {
	Delays map[string]int `json:"delays"`
}

// Connection is one open connection through the proxy.
type Connection struct {
	ID              string `json:"id"`
	Network         string `json:"network"`
	Inbound         string `json:"inbound"`
	Host            string `json:"host"`
	Process         string `json:"process"`
	SourceIP        string `json:"source_ip"`
	SourcePort      int    `json:"source_port"`
	DestinationIP   string `json:"destination_ip"`
	DestinationPort int    `json:"destination_port"`
	Chain           string `json:"chain"`
	Rule            string `json:"rule"`
	Upload          int64  `json:"upload"`
	Download        int64  `json:"download"`
	StartedAt       int64  `json:"started_at"`
}

// Connections is a snapshot of what is open now. Connections lists the
// newest of them when there are too many for one message; Open counts all.
type Connections struct {
	UploadTotal   int64        `json:"upload_total"`
	DownloadTotal int64        `json:"download_total"`
	Open          int          `json:"open"`
	Connections   []Connection `json:"connections"`
}

// CloseArgs closes connections by id, or every connection with All.
type CloseArgs struct {
	IDs []string `json:"ids,omitempty"`
	All bool     `json:"all,omitempty"`
}

// Traffic is throughput in bytes per second.
type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Egress is where traffic leaves the proxy, measured through it.
type Egress struct {
	IP      string `json:"ip"`
	Latency int    `json:"latency"` // milliseconds
}
