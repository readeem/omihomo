package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/readeem/omihomo/internal/update"
)

func main() {
	syscall.Umask(0077)
	if err := run(); err != nil {
		if len(os.Args) == 2 && os.Args[1] == "check" {
			_ = json.NewEncoder(os.Stdout).Encode(struct {
				Error string `json:"error"`
			}{err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("GitHub packages require Linux x86_64")
	}
	args := os.Args[1:]
	if len(args) == 0 || (args[0] == "check" && len(args) != 1) ||
		(args[0] != "check" && (len(args) != 2 || (args[0] != "install" && args[0] != "update"))) {
		return fmt.Errorf("usage: omihomo-update check | install VERSION | update VERSION")
	}
	if args[0] != "check" && os.Geteuid() != 0 {
		return fmt.Errorf("run the plugin's omihomo-install command to authenticate installation")
	}
	key, err := update.PublicKey()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	updater := update.New(update.HTTPClient(), update.Pacman{}, key)
	if args[0] == "check" {
		status, err := updater.Check(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	return updater.Apply(ctx, args[0], args[1])
}
