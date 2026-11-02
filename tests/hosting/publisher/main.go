package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/readeem/omihomo/internal/hosting"
	"github.com/readeem/omihomo/internal/hosting/xray"
	"github.com/readeem/omihomo/internal/uplink"
)

type observedTransport struct {
	hosting.Transport
	active  atomic.Int64
	maximum atomic.Int64
	runs    atomic.Int64
}

func (t *observedTransport) Run(ctx context.Context, relay hosting.Relay, credential string, recv hosting.Receiver) error {
	n := t.active.Add(1)
	defer t.active.Add(-1)
	t.runs.Add(1)
	for old := t.maximum.Load(); n > old; old = t.maximum.Load() {
		if t.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	return t.Transport.Run(ctx, relay, credential, recv)
}

func main() {
	if len(os.Args) != 3 || os.Args[1] != "--config" {
		os.Exit(2)
	}
	file, err := os.Open(os.Args[2])
	if err != nil {
		os.Exit(2)
	}
	var input struct {
		Config     hosting.Config `json:"config"`
		Credential string         `json:"credential"`
		DNS        string         `json:"dns"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		file.Close()
		os.Exit(2)
	}
	file.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	var manager *hosting.Manager
	var mu sync.Mutex
	changed := func() {
		mu.Lock()
		defer mu.Unlock()
		if manager != nil {
			snapshot := manager.Snapshot()
			fmt.Println("hosting", snapshot.State)
		}
	}
	var dnsServers []netip.AddrPort
	if input.DNS != "" {
		server, err := netip.ParseAddrPort(input.DNS)
		if err != nil || !server.Addr().IsLoopback() {
			os.Exit(2)
		}
		dnsServers = append(dnsServers, server)
	}
	network := uplink.Network{Discover: func(context.Context) ([]uplink.Link, error) {
		return []uplink.Link{{Name: "lo", DNS: dnsServers, Addresses: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}}}, nil
	}}
	transport := &observedTransport{Transport: xray.New(network.DialContext)}
	manager = hosting.New(transport, changed)
	defer manager.Close()
	if err := manager.Apply(ctx, input.Config, input.Credential, true, false); err != nil {
		fmt.Println("invalid fixture configuration")
		os.Exit(2)
	}
	fmt.Println("started")
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 16<<10)
		for scanner.Scan() {
			var command struct {
				ID         uint64         `json:"id"`
				Config     hosting.Config `json:"config"`
				Credential string         `json:"credential"`
				DNS        string         `json:"dns"`
				Running    bool           `json:"running"`
				Retry      bool           `json:"retry"`
			}
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.DisallowUnknownFields()
			valid := decoder.Decode(&command) == nil
			if valid {
				var extra any
				valid = decoder.Decode(&extra) == io.EOF
			}
			ok := false
			if valid {
				applyCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				ok = manager.Apply(applyCtx, command.Config, command.Credential, command.Running, command.Retry) == nil
				stop()
			}
			mu.Lock()
			body, _ := json.Marshal(struct {
				ID          uint64           `json:"id"`
				OK          bool             `json:"ok"`
				Snapshot    hosting.Snapshot `json:"snapshot"`
				ActiveRuns  int64            `json:"active_runs"`
				MaximumRuns int64            `json:"maximum_runs"`
				Runs        int64            `json:"runs"`
			}{command.ID, ok, manager.Snapshot(), transport.active.Load(), transport.maximum.Load(), transport.runs.Load()})
			fmt.Println("ack", string(body))
			mu.Unlock()
		}
	}()
	<-ctx.Done()
}
