package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/hosting"
	"github.com/readeem/omihomo/internal/protocol"
)

const maxRelayProfile = 16 << 10

type hostPlan struct {
	op      string
	args    any
	resolve func(ctx context.Context, c *client.Client) (any, error)
}

func (a *app) hostCall(name, shortUsage, help string, plan planner) *ffcli.Command {
	return a.hostFlagged(name, shortUsage, help, func(*flag.FlagSet) planner { return plan })
}

type planner func(cmd *ffcli.Command, rest []string) (hostPlan, error)

func (a *app) hostFlagged(name, shortUsage, help string, setup func(*flag.FlagSet) planner) *ffcli.Command {
	fs := a.flags(name)
	pretty := fs.Bool("pretty", false, "indent the JSON result")
	plan := setup(fs)
	cmd := &ffcli.Command{Name: name, ShortUsage: shortUsage, ShortHelp: help, FlagSet: fs}
	cmd.Exec = func(ctx context.Context, rest []string) error {
		p, err := plan(cmd, rest)
		if err != nil {
			return err
		}
		c, err := dial(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		if p.resolve != nil {
			if p.args, err = p.resolve(ctx, c); err != nil {
				return err
			}
		}
		var snap protocol.HostingSnapshot
		if err := c.Call(ctx, p.op, p.args, &snap); err != nil {
			return err
		}
		out, err := json.Marshal(hostingView(snap))
		if err != nil {
			return err
		}
		return a.print(out, *pretty)
	}
	return cmd
}

type hostingAddress struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

func hostingView(snap protocol.HostingSnapshot) any {
	view := struct {
		protocol.HostingSnapshot
		Addresses []hostingAddress `json:"addresses,omitempty"`
	}{HostingSnapshot: snap}
	if r := snap.Config.Relay; r != nil {
		for _, s := range snap.Shares {
			view.Addresses = append(view.Addresses, hostingAddress{s.ID, s.Protocol,
				net.JoinHostPort(r.Address, strconv.Itoa(s.ExposedPort))})
		}
	}
	return view
}

func noArgs(op string) func(*ffcli.Command, []string) (hostPlan, error) {
	return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
		if len(rest) != 0 {
			return hostPlan{}, usage(cmd, "%s takes no arguments", cmd.Name)
		}
		return hostPlan{op: op}, nil
	}
}

func oneID(op string, args func(id string) any) func(*ffcli.Command, []string) (hostPlan, error) {
	return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
		if len(rest) != 1 {
			return hostPlan{}, usage(cmd, "%s takes one share id", cmd.Name)
		}
		return hostPlan{op: op, args: args(rest[0])}, nil
	}
}

func (a *app) hostingCommand() *ffcli.Command {
	pause := func(name, help string, paused bool) *ffcli.Command {
		return a.hostCall(name, "omihomo hosting "+name+" [--pretty]", help,
			func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
				p, err := noArgs(protocol.OpHostingPause)(cmd, rest)
				p.args = protocol.HostingPauseArgs{Paused: paused}
				return p, err
			})
	}
	enable := func(name, help string, on bool) *ffcli.Command {
		return a.hostCall(name, "omihomo hosting share "+name+" [--pretty] <id>", help,
			oneID(protocol.OpHostingShareEnable, func(id string) any {
				return protocol.HostingShareEnableArgs{ID: id, Enabled: on}
			}))
	}

	relaySet := a.hostFlagged("set", "omihomo hosting relay set [--pretty] [--file <profile.json>]",
		"save the relay profile from stdin or a file", func(fs *flag.FlagSet) planner {
			file := fs.String("file", "", "read the profile from this file instead of stdin")
			return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
				if len(rest) != 0 {
					return hostPlan{}, usage(cmd, "relay set reads the profile from stdin or --file")
				}
				relay, err := a.readRelay(*file)
				if err != nil {
					return hostPlan{}, usage(cmd, "%v", err)
				}
				return hostPlan{op: protocol.OpHostingRelaySet, args: protocol.HostingRelayArgs{Relay: relay}}, nil
			}
		})
	credSet := a.hostFlagged("set", "omihomo hosting credential set [--pretty] [--file <path>]",
		"store the hosting credential from stdin or an owner-only file", func(fs *flag.FlagSet) planner {
			file := fs.String("file", "", "read the credential from this owner-only file instead of stdin")
			return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
				if len(rest) != 0 {
					return hostPlan{}, usage(cmd, "credential set reads the credential from stdin or --file, never an argument")
				}
				cred, err := a.readCredential(*file)
				if err != nil {
					return hostPlan{}, usage(cmd, "%v", err)
				}
				return hostPlan{op: protocol.OpHostingCredentialSet, args: protocol.HostingCredentialArgs{Credential: cred}}, nil
			}
		})

	return a.group("hosting", "omihomo hosting <command>", "share local servers through a private relay",
		a.hostCall("status", "omihomo hosting status [--pretty]", "show the relay, tunnel state and shares",
			noArgs(protocol.OpHostingStatus)),
		a.group("relay", "omihomo hosting relay <command>", "configure the relay profile",
			relaySet,
			a.hostCall("remove", "omihomo hosting relay remove [--pretty]", "remove the relay profile",
				noArgs(protocol.OpHostingRelayRemove))),
		a.group("credential", "omihomo hosting credential <command>", "manage the hosting credential",
			credSet,
			a.hostCall("remove", "omihomo hosting credential remove [--pretty]", "remove the hosting credential",
				noArgs(protocol.OpHostingCredentialRemove))),
		a.hostingShareCommand(enable),
		pause("pause", "close the tunnel and every forwarded session, keeping the shares", true),
		pause("resume", "open the tunnel again", false),
		a.hostCall("retry", "omihomo hosting retry [--pretty]", "retry after the relay refused the credential",
			noArgs(protocol.OpHostingRetry)),
	)
}

func (a *app) hostingShareCommand(enable func(name, help string, on bool) *ffcli.Command) *ffcli.Command {
	add := a.hostFlagged("add", "omihomo hosting share add [--pretty] --label <name> --local-port <n> [--local-address 127.0.0.1] [--exposed-port <n>] [--protocol tcp|udp|both]",
		"create a share; it starts disabled", func(fs *flag.FlagSet) planner {
			f := shareFlags(fs)
			return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
				if len(rest) != 0 {
					return hostPlan{}, usage(cmd, "share add takes flags only")
				}
				set := visited(cmd)
				if !set["label"] || !set["local-port"] {
					return hostPlan{}, usage(cmd, "share add needs --label and --local-port")
				}
				share := hosting.Share{Label: *f.label, LocalAddress: *f.address, LocalPort: *f.local,
					ExposedPort: *f.exposed, Protocol: *f.proto}
				if share.ExposedPort == 0 {
					share.ExposedPort = share.LocalPort
				}
				return hostPlan{op: protocol.OpHostingShareSave, args: protocol.HostingShareArgs{Share: share}}, nil
			}
		})
	edit := a.hostFlagged("edit", "omihomo hosting share edit [--pretty] <id> [--label <name>] [--local-address <ip>] [--local-port <n>] [--exposed-port <n>] [--protocol tcp|udp|both]",
		"change the named fields of a share; it stays enabled or disabled", func(fs *flag.FlagSet) planner {
			f := shareFlags(fs)
			return func(cmd *ffcli.Command, rest []string) (hostPlan, error) {
				if len(rest) != 1 {
					return hostPlan{}, usage(cmd, "share edit takes one share id")
				}
				set := visited(cmd)
				if len(set) == 0 {
					return hostPlan{}, usage(cmd, "share edit needs at least one field to change")
				}
				id := rest[0]
				return hostPlan{op: protocol.OpHostingShareSave, resolve: func(ctx context.Context, c *client.Client) (any, error) {
					var snap protocol.HostingSnapshot
					if err := c.Call(ctx, protocol.OpHostingStatus, nil, &snap); err != nil {
						return nil, err
					}
					for _, s := range snap.Config.Shares {
						if s.ID != id {
							continue
						}
						if set["label"] {
							s.Label = *f.label
						}
						if set["local-address"] {
							s.LocalAddress = *f.address
						}
						if set["local-port"] {
							s.LocalPort = *f.local
						}
						if set["exposed-port"] {
							s.ExposedPort = *f.exposed
						}
						if set["protocol"] {
							s.Protocol = *f.proto
						}
						return protocol.HostingShareArgs{Share: s}, nil
					}
					return nil, &exitError{ExitNotFound, errors.New("no such share")}
				}}, nil
			}
		})

	return a.group("share", "omihomo hosting share <command>", "manage shared local servers",
		add, edit,
		a.hostCall("remove", "omihomo hosting share remove [--pretty] <id>", "remove a share and close its sessions",
			oneID(protocol.OpHostingShareRemove, func(id string) any { return protocol.HostingShareIDArgs{ID: id} })),
		enable("enable", "expose a share through the relay", true),
		enable("disable", "stop exposing a share and close its sessions", false),
	)
}

type shareFlagSet struct {
	label, address, proto *string
	local, exposed        *int
}

func shareFlags(fs *flag.FlagSet) shareFlagSet {
	return shareFlagSet{
		label:   fs.String("label", "", "name shown to the owner"),
		address: fs.String("local-address", "127.0.0.1", "loopback address of the server: 127.0.0.1 or ::1"),
		local:   fs.Int("local-port", 0, "port of the server on this computer"),
		exposed: fs.Int("exposed-port", 0, "port friends connect to; the local port by default"),
		proto:   fs.String("protocol", hosting.ProtoTCP, "tcp, udp or both"),
	}
}

func visited(cmd *ffcli.Command) map[string]bool {
	set := map[string]bool{}
	cmd.FlagSet.Visit(func(f *flag.Flag) {
		if f.Name != "pretty" {
			set[f.Name] = true
		}
	})
	return set
}

func (a *app) readRelay(path string) (hosting.Relay, error) {
	var relay hosting.Relay
	raw, err := a.readInput(path, maxRelayProfile, false)
	if err != nil {
		return relay, fmt.Errorf("relay profile: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&relay); err != nil || dec.More() {
		return hosting.Relay{}, errors.New("relay profile: not a valid profile (unknown, missing or mistyped fields)")
	}
	return relay, nil
}

func (a *app) readCredential(path string) ([]byte, error) {
	raw, err := a.readInput(path, hosting.MaxCredential+2, true)
	if err != nil {
		return nil, fmt.Errorf("credential: %w", err)
	}
	cred := bytes.TrimSpace(raw)
	if len(cred) == 0 {
		return nil, errors.New("credential: empty")
	}
	if len(cred) > hosting.MaxCredential {
		return nil, errors.New("credential: too long")
	}
	return cred, nil
}

func (a *app) readInput(path string, limit int, private bool) ([]byte, error) {
	var r io.Reader = a.env.Stdin
	if path != "" {
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, errors.New("cannot open the file (a symbolic link is refused)")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("the file is not a regular file")
		}
		if private {
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm()&^0o600 != 0 {
				return nil, errors.New("the file must belong to you and not be accessible by others (chmod 600)")
			}
		}
		r = f
	}
	if r == nil {
		return nil, errors.New("nothing to read; pipe it on stdin or use --file")
	}
	raw, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, errors.New("read failed")
	}
	if len(raw) > limit {
		return nil, errors.New("input too large")
	}
	return raw, nil
}
