package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/readeem/omihomo/internal/core/mihomo"
	"github.com/readeem/omihomo/internal/migration"
)

func (a *app) preflightCommand() *ffcli.Command {
	flags := a.flags("preflight")
	data := flags.String("legacy-data", "", "old data directory (defaults to $XDG_DATA_HOME/omihomo)")
	cmd := &ffcli.Command{Name: "preflight", ShortUsage: "omihomo preflight [--legacy-data DIR]",
		ShortHelp: "validate a legacy import without changing the installation", FlagSet: flags}
	cmd.Exec = func(ctx context.Context, args []string) error {
		if len(args) != 0 {
			return usage(cmd, "preflight takes no arguments")
		}
		source := *data
		if source == "" {
			base := os.Getenv("XDG_DATA_HOME")
			if !filepath.IsAbs(base) {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				base = filepath.Join(home, ".local/share")
			}
			source = filepath.Join(base, "omihomo")
		}
		dir, err := os.MkdirTemp("/tmp", "omihomo-preflight-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		uid, gid, err := exportOwner()
		if err != nil {
			return err
		}
		var parser mihomo.Core
		staged, err := migration.Prepare(ctx, source, filepath.Join(dir, "state"), false, parser.ParseLinks, selectionExporter(uid, gid))
		if err != nil {
			return fmt.Errorf("migration preflight (old install unchanged): %w", err)
		}
		if staged != nil {
			defer staged.Close()
		}
		return json.NewEncoder(a.env.Stdout).Encode(struct {
			Import bool `json:"import"`
		}{staged != nil})
	}
	return cmd
}
