package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/readeem/omihomo/internal/hosting"
	"github.com/readeem/omihomo/internal/protocol"
)

type unavailableHosting struct{}

func (unavailableHosting) Run(context.Context, hosting.Relay, string, hosting.Receiver) error {
	return errors.New("hosting transport unavailable")
}

// WithHostingTransport supplies the adapter behind the hosting lifecycle boundary.
func WithHostingTransport(t hosting.Transport) ConfigOpt {
	return func(c *config) { c.hostingTransport = t }
}

func (d *server) hostingSnapshot() protocol.HostingSnapshot {
	snapshot := d.hosting.Snapshot()
	d.mu.Lock()
	record, problem := d.hostingState, d.hostingError
	d.mu.Unlock()
	if problem != "" {
		snapshot.Config = record.Config
		snapshot.State = hosting.StateError
		snapshot.Reason = problem
	}
	return protocol.HostingSnapshot{Snapshot: snapshot, CredentialPresent: record.Credential != "", CredentialID: record.CredentialID}
}

func (d *server) syncHosting(ctx context.Context) {
	d.mu.Lock()
	record := d.hostingState
	d.mu.Unlock()
	err := d.hosting.Apply(ctx, record.Config, record.Credential, d.running(), false)
	d.mu.Lock()
	d.hostingError = ""
	if err != nil {
		d.hostingError = "hosting configuration cannot run"
	}
	d.mu.Unlock()
}

func hostingID() string {
	var value [16]byte
	_, err := rand.Read(value[:])
	if err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(value[:])
}

func (d *server) hostingOperation(ctx context.Context, req protocol.Request) (any, *protocol.Error) {
	if len(req.Args) > 16<<10 {
		return nil, fail(protocol.CodeTooLarge, "hosting input exceeds 16 KiB")
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next := d.hostingState
	d.mu.Unlock()
	next.Config.Shares = slices.Clone(next.Config.Shares)
	if next.Config.Relay != nil {
		relay := *next.Config.Relay
		next.Config.Relay = &relay
	}
	decodeArgs := func(value any) *protocol.Error {
		if len(req.Args) == 0 {
			req.Args = []byte("{}")
		}
		if err := protocol.Decode(req.Args, value); err != nil {
			return fail(protocol.CodeBadRequest, "invalid hosting arguments")
		}
		return nil
	}
	shareIndex := func(id string) int {
		return slices.IndexFunc(next.Config.Shares, func(s hosting.Share) bool { return s.ID == id })
	}
	retry := false
	switch req.Op {
	case protocol.OpHostingStatus:
		if err := decodeArgs(&struct{}{}); err != nil {
			return nil, err
		}
		return d.hostingSnapshot(), nil
	case protocol.OpHostingRelaySet:
		var args protocol.HostingRelayArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		if args.Relay.CredentialRef != "" && args.Relay.CredentialRef != next.CredentialID {
			return nil, fail(protocol.CodeInvalid, "unknown hosting credential reference")
		}
		args.Relay.CredentialRef = next.CredentialID
		next.Config.Relay = &args.Relay
	case protocol.OpHostingRelayRemove:
		if err := decodeArgs(&struct{}{}); err != nil {
			return nil, err
		}
		next.Config.Relay = nil
		next.Credential = ""
		next.CredentialID = ""
	case protocol.OpHostingCredentialSet:
		var args protocol.HostingCredentialArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		credential, err := hosting.ValidateCredential(args.Credential)
		clear(args.Credential)
		if err != nil {
			return nil, fail(protocol.CodeInvalid, "invalid hosting credential")
		}
		next.Credential = credential
		next.CredentialID = hostingID()
		if next.Config.Relay != nil {
			next.Config.Relay.CredentialRef = next.CredentialID
		}
		retry = true
	case protocol.OpHostingCredentialRemove:
		if err := decodeArgs(&struct{}{}); err != nil {
			return nil, err
		}
		next.Credential = ""
		next.CredentialID = ""
		if next.Config.Relay != nil {
			next.Config.Relay.CredentialRef = ""
		}
	case protocol.OpHostingShareSave:
		var args protocol.HostingShareArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		if args.Share.LocalAddress == "" {
			args.Share.LocalAddress = "127.0.0.1"
		}
		if args.Share.ExposedPort == 0 {
			args.Share.ExposedPort = args.Share.LocalPort
		}
		if args.Share.ID == "" {
			args.Share.ID = hostingID()
			args.Share.Enabled = false
			next.Config.Shares = append(next.Config.Shares, args.Share)
		} else {
			index := shareIndex(args.Share.ID)
			if index < 0 {
				return nil, fail(protocol.CodeNotFound, "hosting share not found")
			}
			next.Config.Shares[index] = args.Share
		}
	case protocol.OpHostingShareRemove:
		var args protocol.HostingShareIDArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		index := shareIndex(args.ID)
		if index < 0 {
			return nil, fail(protocol.CodeNotFound, "hosting share not found")
		}
		next.Config.Shares = slices.Delete(next.Config.Shares, index, index+1)
	case protocol.OpHostingShareEnable:
		var args protocol.HostingShareEnableArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		index := shareIndex(args.ID)
		if index < 0 {
			return nil, fail(protocol.CodeNotFound, "hosting share not found")
		}
		next.Config.Shares[index].Enabled = args.Enabled
	case protocol.OpHostingPause:
		var args protocol.HostingPauseArgs
		if err := decodeArgs(&args); err != nil {
			return nil, err
		}
		next.Config.Paused = args.Paused
	case protocol.OpHostingRetry:
		if err := decodeArgs(&struct{}{}); err != nil {
			return nil, err
		}
		retry = true
	default:
		return nil, fail(protocol.CodeUnknownOp, "unknown hosting operation")
	}
	if err := hosting.Validate(next.Config); err != nil {
		return nil, fail(protocol.CodeInvalid, "%v", err)
	}
	if err := d.store.SaveHosting(next); err != nil {
		return nil, fail(protocol.CodeInternal, "hosting state could not be saved")
	}
	d.mu.Lock()
	d.hostingState = next
	d.hostingError = ""
	d.mu.Unlock()
	err := d.hosting.Apply(ctx, next.Config, next.Credential, d.running(), retry)
	if err != nil {
		d.mu.Lock()
		d.hostingError = "hosting update could not start"
		d.mu.Unlock()
		d.publish(ctx)
		return nil, fail(protocol.CodeCore, "hosting access was updated but the tunnel could not start")
	}
	d.publish(ctx)
	return d.hostingSnapshot(), nil
}

func isHostingOperation(op string) bool { return strings.HasPrefix(op, "hosting.") }
