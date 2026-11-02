package protocol

import "github.com/readeem/omihomo/internal/hosting"

const (
	OpHostingStatus           = "hosting.status"
	OpHostingRelaySet         = "hosting.relay.set"
	OpHostingRelayRemove      = "hosting.relay.remove"
	OpHostingCredentialSet    = "hosting.credential.set"
	OpHostingCredentialRemove = "hosting.credential.remove"
	OpHostingShareSave        = "hosting.share.save"
	OpHostingShareRemove      = "hosting.share.remove"
	OpHostingShareEnable      = "hosting.share.enable"
	OpHostingPause            = "hosting.pause"
	OpHostingRetry            = "hosting.retry"
	StreamHosting             = "hosting"
)

// HostingSnapshot reports desired shares and live state without credential material.
type HostingSnapshot struct {
	hosting.Snapshot
	CredentialPresent bool   `json:"credential_present"`
	CredentialID      string `json:"credential_id"`
}

// HostingRelayArgs replaces the single typed relay profile.
type HostingRelayArgs struct {
	Relay hosting.Relay `json:"relay"`
}

// HostingCredentialArgs carries bounded credential bytes over the owner-only socket.
type HostingCredentialArgs struct {
	Credential []byte `json:"credential"`
}

// HostingShareArgs creates a disabled share or replaces an existing share.
type HostingShareArgs struct {
	Share hosting.Share `json:"share"`
}

// HostingShareIDArgs identifies an owner's share.
type HostingShareIDArgs struct {
	ID string `json:"id"`
}

// HostingShareEnableArgs explicitly changes whether a share is exposed.
type HostingShareEnableArgs struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// HostingPauseArgs preserves selections while closing or resuming the tunnel.
type HostingPauseArgs struct {
	Paused bool `json:"paused"`
}
