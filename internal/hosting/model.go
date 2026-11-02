// Package hosting validates the hosting configuration and runs the engine-independent
// tunnel lifecycle: which shares are exposed, which sessions may exist, and when the
// tunnel runs. The transport itself lives behind the Transport interface.
package hosting

import (
	"context"
	"errors"
	"net"
)

// Limits and fixed parameters of hosting.
const (
	MaxShares      = 32
	MaxTCP         = 256
	MaxUDP         = 256
	MaxDatagram    = 8190
	MaxLabelBytes  = 64
	MaxCredential  = 256
	ProfileVersion = 1
)

// Share protocols and tunnel states.
const (
	ProtoTCP  = "tcp"
	ProtoUDP  = "udp"
	ProtoBoth = "both"

	StateIdle         = "idle"
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateReconnecting = "reconnecting"
	StateError        = "error"
)

var (
	// ErrInvalid wraps every validation failure; messages name fields, never values.
	ErrInvalid = errors.New("hosting: invalid configuration")
	// ErrAuth is returned by a Transport when the relay explicitly refuses the
	// registration or authentication. It stops retries until the credential changes
	// or the owner retries.
	ErrAuth = errors.New("hosting: relay refused authentication")
	// ErrRefused is returned by Receiver.Open for traffic that is not an enabled share.
	ErrRefused = errors.New("hosting: destination refused")
	// ErrUnavailable is returned when the local target cannot be reached.
	ErrUnavailable = errors.New("hosting: target unavailable")
	// ErrOversized is returned when a datagram exceeds MaxDatagram.
	ErrOversized = errors.New("hosting: datagram too large")
	// ErrClosed is returned by Apply after Close.
	ErrClosed = errors.New("hosting: closed")
)

// Reality holds the pinned transport-security parameters.
type Reality struct {
	ServerName string `json:"server_name"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

// Relay is the owner-approved connection profile of a compatible reverse relay.
type Relay struct {
	Version       int     `json:"version"`
	Label         string  `json:"label"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Address       string  `json:"address"`
	CredentialRef string  `json:"credential_ref,omitempty"`
	Reality       Reality `json:"reality"`
}

// Share maps an exposed port to a loopback service on this computer.
type Share struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	LocalAddress string `json:"local_address"`
	LocalPort    int    `json:"local_port"`
	ExposedPort  int    `json:"exposed_port"`
	Protocol     string `json:"protocol"`
	Enabled      bool   `json:"enabled"`
}

// Config is the owner's desired hosting state. It never holds the credential.
type Config struct {
	Relay  *Relay  `json:"relay,omitempty"`
	Shares []Share `json:"shares"`
	Paused bool    `json:"paused"`
}

// Destination is what a friend asked the relay to reach.
type Destination struct {
	Address string
	Port    int
	Network string
}

// Receiver is the manager's side of a running transport.
type Receiver interface {
	// Open admits a destination and returns a connection to its local target.
	Open(context.Context, Destination) (net.Conn, error)
	// Connected reports that the reverse control connection is live.
	Connected()
	// Drop counts a datagram the transport discarded.
	Drop(oversized bool)
}

// Transport runs one reverse tunnel until ctx ends or the tunnel fails. It returns
// ErrAuth when the relay refuses the identity.
type Transport interface {
	Run(ctx context.Context, relay Relay, credential string, recv Receiver) error
}

// ShareStatus is a share with its live session count.
type ShareStatus struct {
	Share
	Active int `json:"active"`
}

// Snapshot is the observable hosting state; it never contains the credential.
type Snapshot struct {
	Config    Config        `json:"config"`
	State     string        `json:"state"`
	Reason    string        `json:"reason,omitempty"`
	ChangedAt int64         `json:"changed_at"`
	Active    int           `json:"active"`
	Shares    []ShareStatus `json:"shares"`
	Refused   uint64        `json:"refused"`
	Dropped   uint64        `json:"dropped"`
	Oversized uint64        `json:"oversized"`
}

func (c Config) clone() Config {
	out := Config{Paused: c.Paused, Shares: append([]Share(nil), c.Shares...)}
	if c.Relay != nil {
		r := *c.Relay
		out.Relay = &r
	}
	return out
}

func (s Share) covers(network string) bool {
	return s.Protocol == ProtoBoth || s.Protocol == network
}
