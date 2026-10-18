// Package cli is the omihomo command: the daemon itself, and the owner's
// subcommands, which talk to the daemon's socket and keep the owner's
// settings files.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/settings"
)

// Set at build time with -ldflags "-X ...cli.Version=vX.Y.Z".
var Version = "dev"

// Exit codes.
const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitUsage       = 2
	ExitUnreachable = 10 // no daemon answers on the socket
	ExitState       = 11 // the call contradicts the current state
	ExitNotFound    = 12
	ExitForbidden   = 13 // this account does not own the daemon
	ExitVersion     = 14 // the daemon speaks another protocol version
	ExitUnsupported = 15
	ExitRejected    = 20 // a subscription's content is not usable
	ExitFetch       = 21 // a download failed
)

// Env is what a command reads and writes besides its arguments.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Run executes one command line and returns its exit code. Help goes to
// stderr and exits 0 when asked for, 2 after a usage error.
func Run(ctx context.Context, args []string, env Env) int {
	root := (&app{env: env}).root()
	if err := root.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		// The flag package has already printed the error and the usage.
		return ExitUsage
	}
	return exitCode(env, root.Run(ctx))
}

type app struct{ env Env }

type usageError struct {
	cmd *ffcli.Command
	msg string
}

func (e *usageError) Error() string { return e.msg }

func usage(cmd *ffcli.Command, format string, args ...any) error {
	return &usageError{cmd: cmd, msg: fmt.Sprintf(format, args...)}
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func exitCode(env Env, err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	fmt.Fprintf(env.Stderr, "omihomo: %v\n", err)
	var (
		uerr *usageError
		xerr *exitError
		verr *client.VersionError
		perr *protocol.Error
	)
	switch {
	case errors.As(err, &uerr):
		fmt.Fprintln(env.Stderr)
		uerr.cmd.FlagSet.Usage()
		return ExitUsage
	case errors.As(err, &xerr):
		return xerr.code
	case errors.As(err, &verr):
		return ExitVersion
	case errors.As(err, &perr):
		switch perr.Code {
		case protocol.CodeBadRequest, protocol.CodeInvalid, protocol.CodeUnknownOp:
			return ExitUsage
		case protocol.CodeNotRunning, protocol.CodeConflict:
			return ExitState
		case protocol.CodeNotFound:
			return ExitNotFound
		case protocol.CodeForbidden:
			return ExitForbidden
		case protocol.CodeUnsupported:
			return ExitUnsupported
		case protocol.CodeRejected:
			return ExitRejected
		case protocol.CodeFetch:
			return ExitFetch
		}
	}
	return ExitFailure
}

func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.env.Stderr)
	return fs
}

func (a *app) group(name, shortUsage, help string, subs ...*ffcli.Command) *ffcli.Command {
	cmd := &ffcli.Command{Name: name, ShortUsage: shortUsage, ShortHelp: help,
		FlagSet: a.flags(name), Subcommands: subs}
	cmd.Exec = func(_ context.Context, args []string) error {
		if len(args) == 0 {
			return usage(cmd, "missing command")
		}
		return usage(cmd, "unknown command %q", args[0])
	}
	return cmd
}

type request func(cmd *ffcli.Command, args []string) (op string, callArgs any, err error)

func (a *app) call(name, shortUsage, help string, req request) *ffcli.Command {
	fs := a.flags(name)
	pretty := fs.Bool("pretty", false, "indent the JSON result")
	cmd := &ffcli.Command{Name: name, ShortUsage: shortUsage, ShortHelp: help, FlagSet: fs}
	cmd.Exec = func(ctx context.Context, args []string) error {
		op, callArgs, err := req(cmd, args)
		if err != nil {
			return err
		}
		c, err := dial(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		var result json.RawMessage
		if err := c.Call(ctx, op, callArgs, &result); err != nil {
			return err
		}
		return a.print(result, *pretty)
	}
	return cmd
}

func nargs(n int, op string, args func([]string) any) request {
	return func(cmd *ffcli.Command, rest []string) (string, any, error) {
		if len(rest) != n {
			return "", nil, usage(cmd, "%s takes %d arguments, got %d", cmd.Name, n, len(rest))
		}
		if args == nil {
			return op, nil, nil
		}
		return op, args(rest), nil
	}
}

func (a *app) root() *ffcli.Command {
	root := a.group("omihomo", "omihomo <command> [flags] [args...]", "",
		a.call("status", "omihomo status [--pretty]", "show the proxy's state and settings",
			nargs(0, protocol.OpStatus, nil)),
		a.call("start", "omihomo start [--pretty]", "start the proxy", nargs(0, protocol.OpStart, nil)),
		a.call("stop", "omihomo stop [--pretty]", "stop the proxy", nargs(0, protocol.OpStop, nil)),
		a.setCommand(),
		a.applyCommand(),
		a.subCommand(),
		a.ruleCommand(),
		a.groupCommand(),
		a.connCommand(),
		a.call("egress", "omihomo egress [--pretty]", "show where traffic leaves the proxy",
			nargs(0, protocol.OpEgress, nil)),
		a.watchCommand(),
		a.versionCommand(),
		a.claimCommand(),
		a.daemonCommand(),
	)
	root.LongHelp = "Every command but claim and daemon talks to the daemon's socket and prints one JSON\n" +
		"line. set, rule add and rule remove also save your settings files, settings.json and\n" +
		"rules.json in $XDG_CONFIG_HOME/omihomo, and apply runs what they hold. Flags go before\n" +
		"arguments; -- ends the flags. OMIHOMO_SOCKET points at another socket. Run\n" +
		"omihomo <command> -h for more."
	exec := root.Exec
	root.Exec = func(ctx context.Context, args []string) error {
		if len(args) == 1 && args[0] == "help" {
			return flag.ErrHelp
		}
		return exec(ctx, args)
	}
	return root
}

// edit is a command that changes the owner's settings files. Its arguments
// are checked before anything is read; the change is applied to the daemon,
// and saved only once the daemon runs it. show is the op whose result it
// prints, or "" for the daemon's status.
func (a *app) edit(name, shortUsage, help, show string, parse func(cmd *ffcli.Command, args []string) (change, error)) *ffcli.Command {
	fs := a.flags(name)
	pretty := fs.Bool("pretty", false, "indent the JSON result")
	cmd := &ffcli.Command{Name: name, ShortUsage: shortUsage, ShortHelp: help, FlagSet: fs}
	cmd.Exec = func(ctx context.Context, args []string) error {
		ch, err := parse(cmd, args)
		if err != nil {
			return err
		}
		dir, err := settings.Dir()
		if err != nil {
			return err
		}
		lock, err := settings.Lock(dir)
		if err != nil {
			return err
		}
		defer lock.Close()
		v, err := settings.Load(dir)
		if err != nil {
			return err
		}
		c, err := dial(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		if err := ch(ctx, c, &v); err != nil {
			return err
		}
		var result json.RawMessage
		if err := c.Call(ctx, protocol.OpApply, v, &result); err != nil {
			return err
		}
		if err := settings.Save(dir, v); err != nil {
			return fmt.Errorf("the daemon runs the change, but it was not saved: %w", err)
		}
		if show != "" {
			if err := c.Call(ctx, show, nil, &result); err != nil {
				return err
			}
		}
		return a.print(result, *pretty)
	}
	return cmd
}

// change makes one edit to the owner's settings, asking c what it needs to
// check it.
type change func(ctx context.Context, c *client.Client, v *protocol.ApplyArgs) error

func (a *app) setCommand() *ffcli.Command {
	setting := func(name, value, help string, parse func(string) (change, error)) *ffcli.Command {
		return a.edit(name, fmt.Sprintf("omihomo set %s [--pretty] %s", name, value), help, "",
			func(cmd *ffcli.Command, rest []string) (change, error) {
				if len(rest) != 1 {
					return nil, usage(cmd, "set %s takes one value", name)
				}
				ch, err := parse(rest[0])
				if err != nil {
					return nil, usage(cmd, "%v", err)
				}
				return ch, nil
			})
	}
	toggle := func(name, help string, field func(*protocol.Settings) *bool) *ffcli.Command {
		return setting(name, "<on|off>", help, func(v string) (change, error) {
			if v != "on" && v != "off" {
				return nil, fmt.Errorf("set %s takes on or off", name)
			}
			return func(_ context.Context, _ *client.Client, s *protocol.ApplyArgs) error {
				*field(&s.Settings) = v == "on"
				return nil
			}, nil
		})
	}
	return a.group("set", "omihomo set <setting> [--pretty] <value>", "change a setting",
		setting("mode", "<rule|global|direct>", "route by rules, everything through the proxy, or nothing",
			func(v string) (change, error) {
				return func(_ context.Context, _ *client.Client, s *protocol.ApplyArgs) error {
					s.Settings.Mode = v
					return nil
				}, nil
			}),
		setting("group", "<name>", `the primary group; "" for the first selectable one`,
			func(v string) (change, error) {
				return func(ctx context.Context, c *client.Client, s *protocol.ApplyArgs) error {
					if v != "" {
						if err := checkName(ctx, c, v, true); err != nil {
							return err
						}
					}
					s.Settings.PrimaryGroup = v
					return nil
				}, nil
			}),
		toggle("tun", "route all of the machine's traffic through the proxy",
			func(s *protocol.Settings) *bool { return &s.TUN }),
		toggle("tun-redirect", "accelerate TUN with auto-redirect",
			func(s *protocol.Settings) *bool { return &s.TUNRedirect }),
		toggle("tailscale", "let tailscaled reach its servers through the proxy",
			func(s *protocol.Settings) *bool { return &s.Tailscale }),
		toggle("tailnet-proxy", "send the tailnet to the proxy server",
			func(s *protocol.Settings) *bool { return &s.TailnetProxy }),
	)
}

// builtinTargets are the targets every subscription has.
var builtinTargets = []string{"DIRECT", "REJECT", "REJECT-DROP"}

// checkName refuses a group, or with groupOnly false a proxy or a built-in
// target, that the running proxy lacks. A stopped proxy has nothing to check
// against, and anything well formed is kept.
func checkName(ctx context.Context, c *client.Client, name string, groupOnly bool) error {
	if !groupOnly && slices.Contains(builtinTargets, name) {
		return nil
	}
	var list protocol.GroupList
	err := c.Call(ctx, protocol.OpGroups, nil, &list)
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.Code == protocol.CodeNotRunning {
		return nil
	}
	if err != nil {
		return err
	}
	for _, g := range list.Groups {
		if g.Name == name && (g.Selectable || !groupOnly) {
			return nil
		}
		if !groupOnly && slices.ContainsFunc(g.Members, func(p protocol.Proxy) bool { return p.Name == name }) {
			return nil
		}
	}
	if groupOnly {
		return &exitError{ExitNotFound, fmt.Errorf("no selectable group %q", name)}
	}
	return &exitError{ExitNotFound, fmt.Errorf("the active subscription has no group or proxy %q", name)}
}

func (a *app) applyCommand() *ffcli.Command {
	return a.edit("apply", "omihomo apply [--pretty]", "run what your settings files hold", "",
		func(cmd *ffcli.Command, rest []string) (change, error) {
			if len(rest) != 0 {
				return nil, usage(cmd, "apply takes no arguments")
			}
			return func(context.Context, *client.Client, *protocol.ApplyArgs) error { return nil }, nil
		})
}

func (a *app) subCommand() *ffcli.Command {
	name := func(verb, op, help string) *ffcli.Command {
		return a.call(verb, "omihomo sub "+verb+" [--pretty] <name>", help,
			nargs(1, op, func(rest []string) any { return protocol.NameArgs{Name: rest[0]} }))
	}
	return a.group("sub", "omihomo sub <command>", "manage subscriptions",
		a.call("list", "omihomo sub list [--pretty]", "list subscriptions", nargs(0, protocol.OpSubscriptions, nil)),
		a.call("add", "omihomo sub add [--pretty] < url", "add the subscription whose URL is on stdin",
			func(cmd *ffcli.Command, rest []string) (string, any, error) {
				if len(rest) != 0 {
					return "", nil, usage(cmd, "sub add reads the URL from stdin, so it never shows in a process list")
				}
				url, err := readURL(a.env.Stdin)
				if err != nil {
					return "", nil, usage(cmd, "%v", err)
				}
				return protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: url}, nil
			}),
		name("update", protocol.OpSubscriptionUpdate, "fetch a subscription again"),
		name("activate", protocol.OpSubscriptionUse, "run a subscription's config"),
		name("remove", protocol.OpSubscriptionRemove, "remove a subscription"),
	)
}

func (a *app) ruleCommand() *ffcli.Command {
	return a.group("rule", "omihomo rule <command>", "list and change your rules",
		a.call("list", "omihomo rule list [--pretty]", "list the runtime's rules in match order",
			nargs(0, protocol.OpRules, nil)),
		a.edit("add", "omihomo rule add [--pretty] <type> <value> <target>", "add one of your rules", protocol.OpRules,
			func(cmd *ffcli.Command, rest []string) (change, error) {
				if len(rest) != 3 {
					return nil, usage(cmd, "rule add takes a type, a value and a target")
				}
				r := protocol.UserRule{Type: strings.ToUpper(strings.TrimSpace(rest[0])), Value: rest[1], Target: rest[2]}
				return func(ctx context.Context, c *client.Client, v *protocol.ApplyArgs) error {
					if err := checkName(ctx, c, r.Target, false); err != nil {
						return err
					}
					v.Rules = append(v.Rules, r)
					return nil
				}, nil
			}),
		a.edit("remove", "omihomo rule remove [--pretty] <index>", "remove your rule at a 1-based index", protocol.OpRules,
			func(cmd *ffcli.Command, rest []string) (change, error) {
				if len(rest) != 1 {
					return nil, usage(cmd, "rule remove takes one index")
				}
				n, err := strconv.Atoi(rest[0])
				if err != nil || n < 1 {
					return nil, usage(cmd, "a rule index is a positive number")
				}
				return func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs) error {
					if n > len(v.Rules) {
						return &exitError{ExitNotFound, fmt.Errorf("there is no rule %d of yours", n)}
					}
					v.Rules = slices.Delete(v.Rules, n-1, n)
					return nil
				}, nil
			}),
	)
}

func (a *app) groupCommand() *ffcli.Command {
	return a.group("group", "omihomo group <command>", "list groups and choose proxies",
		a.call("list", "omihomo group list [--pretty]", "list groups and their members",
			nargs(0, protocol.OpGroups, nil)),
		a.call("select", "omihomo group select [--pretty] <group> <proxy>", "select a proxy in a group",
			nargs(2, protocol.OpSelect, func(rest []string) any {
				return protocol.SelectArgs{Group: rest[0], Proxy: rest[1]}
			})),
		a.call("test", "omihomo group test [--pretty] <group> [<proxy>...]",
			"measure delays, of every member when no proxy is named",
			func(cmd *ffcli.Command, rest []string) (string, any, error) {
				if len(rest) == 0 {
					return "", nil, usage(cmd, "group test needs a group")
				}
				return protocol.OpDelayTest, protocol.DelayArgs{Group: rest[0], Proxies: rest[1:]}, nil
			}),
	)
}

func (a *app) connCommand() *ffcli.Command {
	var all *bool
	closeCmd := a.call("close", "omihomo conn close [--pretty] --all | <id>...", "close connections",
		func(cmd *ffcli.Command, rest []string) (string, any, error) {
			if *all == (len(rest) > 0) {
				return "", nil, usage(cmd, "conn close takes connection ids or --all")
			}
			if *all {
				return protocol.OpCloseConnections, protocol.CloseArgs{All: true}, nil
			}
			return protocol.OpCloseConnections, protocol.CloseArgs{IDs: rest}, nil
		})
	all = closeCmd.FlagSet.Bool("all", false, "close every connection")
	return a.group("conn", "omihomo conn <command>", "list and close connections",
		a.call("list", "omihomo conn list [--pretty]", "list open connections", nargs(0, protocol.OpConnections, nil)),
		closeCmd,
	)
}

func (a *app) versionCommand() *ffcli.Command {
	cmd := &ffcli.Command{Name: "version", ShortUsage: "omihomo version", ShortHelp: "print this client's version",
		FlagSet: a.flags("version")}
	cmd.Exec = func(_ context.Context, args []string) error {
		if len(args) != 0 {
			return usage(cmd, "version takes no arguments")
		}
		fmt.Fprintf(a.env.Stdout, "omihomo %s (protocol %d)\n", Version, protocol.Version)
		return nil
	}
	return cmd
}

func (a *app) watchCommand() *ffcli.Command {
	cmd := &ffcli.Command{Name: "watch", ShortUsage: "omihomo watch [<stream>...]",
		ShortHelp: "print pushed events as JSON lines",
		LongHelp: "Streams are status, groups, rules, subscriptions, traffic and connections; all of\nthem by default.\n" +
			`Each event prints as {"stream","data"}, or {"stream","error"} when it could not be sent.`,
		FlagSet: a.flags("watch")}
	cmd.Exec = func(ctx context.Context, streams []string) error {
		if len(streams) == 0 {
			streams = []string{protocol.StreamStatus, protocol.StreamGroups, protocol.StreamRules,
				protocol.StreamSubscriptions, protocol.StreamTraffic, protocol.StreamConnections}
		}
		c, err := dial(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		if err := c.Call(ctx, protocol.OpSubscribe, protocol.StreamsArgs{Streams: streams}, nil); err != nil {
			return err
		}
		enc := json.NewEncoder(a.env.Stdout)
		for {
			select {
			case <-ctx.Done():
				return nil
			case ev, ok := <-c.Events():
				if !ok {
					return &exitError{ExitUnreachable, errors.New("the daemon closed the connection")}
				}
				enc.Encode(struct {
					Stream string          `json:"stream"`
					Data   json.RawMessage `json:"data,omitempty"`
					Error  *protocol.Error `json:"error,omitempty"`
				}{ev.Stream, ev.Data, ev.Error})
			}
		}
	}
	return cmd
}

func readURL(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", errors.New("sub add reads the URL from stdin")
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 8192))
	if err != nil {
		return "", fmt.Errorf("read the URL from stdin: %w", err)
	}
	url := strings.TrimSpace(string(raw))
	if url == "" {
		return "", errors.New("sub add reads the URL from stdin, and it was empty")
	}
	return url, nil
}

func dial(ctx context.Context) (*client.Client, error) {
	c, err := client.Dial(ctx, client.SocketPath())
	if err != nil {
		var (
			perr *protocol.Error
			verr *client.VersionError
		)
		if errors.As(err, &perr) || errors.As(err, &verr) {
			return nil, err
		}
		return nil, &exitError{ExitUnreachable, fmt.Errorf("the daemon is not running or not installed (%w)", err)}
	}
	return c, nil
}

// An empty result prints nothing.
func (a *app) print(result json.RawMessage, pretty bool) error {
	if len(result) == 0 || string(result) == "{}" {
		return nil
	}
	var (
		buf bytes.Buffer
		err error
	)
	if pretty {
		err = json.Indent(&buf, result, "", "  ")
	} else {
		err = json.Compact(&buf, result)
	}
	if err != nil {
		return fmt.Errorf("malformed result: %w", err)
	}
	buf.WriteByte('\n')
	_, err = a.env.Stdout.Write(buf.Bytes())
	return err
}
