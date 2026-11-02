package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/daemon"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/uplink"
)

func nextDownload(t *testing.T, c *client.Client) protocol.PendingDownload {
	t.Helper()
	for {
		var list protocol.DownloadList
		if err := json.Unmarshal(next(t, c, protocol.StreamDownloads), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Downloads) > 0 {
			return list.Downloads[0]
		}
	}
}

func TestDirectApprovalIsConsumedByOneDownload(t *testing.T) {
	origin := newOrigin(t)
	origin.set("/work", page{body: workYAML, header: titled("Work")})
	origin.set("/second", page{body: workYAML, header: titled("Second")})
	var directCalls atomic.Int32
	transport := fetch.RoutedTransport(func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		directCalls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	})
	transport.TLSClientConfig.RootCAs = origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	h := serve(t, os.Getuid(), daemon.WithTransport(transport))
	c, watcher := h.dial(t), h.dial(t)
	call[protocol.Empty](t, watcher, protocol.OpSubscribe, protocol.StreamsArgs{Streams: []string{protocol.StreamDownloads}})
	call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: origin.url("/work")})
	if directCalls.Load() != 1 || len(call[protocol.DownloadList](t, c, protocol.OpDownloads, nil).Downloads) != 0 {
		t.Fatal("first subscription needed permission")
	}

	begin := func(op string, args any) <-chan error {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- c.Call(t.Context(), op, args, nil) }()
		return done
	}
	finish := func(done <-chan error, allow bool) {
		t.Helper()
		select {
		case err := <-done:
			if allow && err != nil {
				t.Fatal(err)
			}
			if !allow && (err == nil || !strings.Contains(err.Error(), "declined")) {
				t.Fatalf("decline result: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("download did not finish after decision")
		}
	}
	for _, allow := range []bool{false, true, false} {
		h.clock.advance(time.Minute)
		done := begin(protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "Work"})
		pending := nextDownload(t, watcher)
		if pending.Label != "Subscription Work" || origin.count("/work") != int(directCalls.Load()) {
			t.Fatalf("pending download exposed unexpected data: %+v", pending)
		}
		before := directCalls.Load()
		call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID, Allow: allow})
		finish(done, allow)
		expected := before
		if allow {
			expected++
		}
		if directCalls.Load() != expected {
			t.Fatalf("direct calls = %d, expected %d", directCalls.Load(), expected)
		}
		if err := callErr(t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID, Allow: true}); err.Code != protocol.CodeNotFound {
			t.Fatalf("approval replay: %v", err)
		}
	}
	origin.set("/work", page{header: titled("Work"), body: workYAML + `
proxy-providers:
 remote: {type: http, url: "` + origin.URL + `/provider"}
`})
	origin.set("/provider", page{body: "proxies: []"})
	h.clock.advance(time.Minute)
	providerUpdate := begin(protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "Work"})
	subscription := nextDownload(t, watcher)
	call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: subscription.ID, Allow: true})
	provider := nextDownload(t, watcher)
	if !strings.HasPrefix(provider.Label, "Provider remote") || origin.count("/provider") != 0 {
		t.Fatalf("subscription approval escaped to its provider: %+v", provider)
	}
	call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: provider.ID})
	finish(providerUpdate, false)

	done := begin(protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: origin.url("/second")})
	pending := nextDownload(t, watcher)
	call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID})
	finish(done, false)
	if len(call[protocol.DownloadList](t, c, protocol.OpDownloads, nil).Downloads) != 0 {
		t.Fatal("finished approval remained pending")
	}
}

func TestPolicyRefusalNeverRequestsDirectApproval(t *testing.T) {
	transport := fetch.RoutedTransport(func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: uplink.ErrDownloadAddress}
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Error("policy refusal triggered direct dial")
		return nil, errors.New("unexpected")
	})
	h := serve(t, os.Getuid(), daemon.WithTransport(transport))
	c := h.dial(t)
	err := callErr(t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: "https://example.com/sub"})
	if !strings.Contains(err.Message, "public address") || len(call[protocol.DownloadList](t, c, protocol.OpDownloads, nil).Downloads) > 0 {
		t.Fatalf("policy refusal became approval: %v", err)
	}
}
