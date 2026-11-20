package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/fakecore"
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

func TestManualRefreshCanRetryAfterDecliningScheduledDownload(t *testing.T) {
	origin := newOrigin(t)
	origin.set("/work", page{body: workYAML, header: titled("Work")})
	var proxyDown atomic.Bool
	dial := (&net.Dialer{}).DialContext
	transport := fetch.RoutedTransport(func(ctx context.Context, network, address string) (net.Conn, error) {
		if proxyDown.Load() {
			return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
		}
		return dial(ctx, network, address)
	}, dial)
	transport.TLSClientConfig.RootCAs = origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	h := serve(t, os.Getuid(), daemon.WithTransport(transport), daemon.WithRefreshCheck(10*time.Millisecond))
	c := h.dial(t)
	sub := call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: origin.url("/work")})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	call[protocol.Empty](t, c, protocol.OpSubscribe, protocol.StreamsArgs{
		Streams: []string{protocol.StreamDownloads, protocol.StreamSubscriptions},
	})
	next(t, c, protocol.StreamSubscriptions)
	proxyDown.Store(true)
	h.clock.advance(time.Duration(sub.Interval) * time.Second)
	pending := nextDownload(t, c)
	call[protocol.Empty](t, c, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID})

	var scheduled protocol.SubscriptionList
	if err := json.Unmarshal(next(t, c, protocol.StreamSubscriptions), &scheduled); err != nil {
		t.Fatal(err)
	}
	if result := scheduled.Subscriptions[0]; !strings.Contains(result.Error, "declined") || result.NextUpdate <= h.clock.Now().UnixMilli() {
		t.Fatalf("declining did not schedule the next automatic retry: %+v", result)
	}
	proxyDown.Store(false)
	call[protocol.Subscription](t, c, protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: sub.Name})
	if n := origin.count("/work"); n != 2 {
		t.Fatalf("origin got %d requests, want the initial download and manual refresh", n)
	}
}

func TestPausingScheduledRefreshCancelsItsApproval(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stop, geodata bool
	}{
		{name: "disable updates"},
		{name: "stop proxy", stop: true},
		{name: "disable updates during geodata", geodata: true},
		{name: "stop proxy during geodata", stop: true, geodata: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newOrigin(t)
			o.set("/work", page{body: workYAML, header: titled("Work")})
			transport := fetch.RoutedTransport(func(context.Context, string, string) (net.Conn, error) {
				return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
			}, (&net.Dialer{}).DialContext)
			transport.TLSClientConfig.RootCAs = o.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
			geoip := filepath.Join(t.TempDir(), "geoip.metadb")
			c0 := fakecore.New()
			c0.SetGeodata(func(p core.Profile) []core.GeodataFile {
				if slices.ContainsFunc(p.Rules, func(r core.Rule) bool { return r.Type == "GEOIP" }) {
					return []core.GeodataFile{{Path: geoip, URL: o.URL + "/geoip.metadb"}}
				}
				return nil
			})
			h := serveCore(t, os.Getuid(), c0, daemon.WithTransport(transport), daemon.WithRefreshCheck(time.Millisecond))
			c, watcher := h.dial(t), h.dial(t)
			sub := call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
			call[protocol.Status](t, c, protocol.OpStart, nil)
			if tc.geodata {
				o.set("/work", page{body: strings.Replace(workYAML, "  - MATCH,Proxy", "  - GEOIP,NL,DIRECT\n  - MATCH,Proxy", 1), header: titled("Work")})
			}
			call[protocol.Empty](t, watcher, protocol.OpSubscribe, protocol.StreamsArgs{Streams: []string{protocol.StreamDownloads}})
			h.clock.advance(time.Duration(sub.Interval) * time.Second)
			pending := nextDownload(t, watcher)
			if tc.geodata {
				call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID, Allow: true})
				pending = nextDownload(t, watcher)
				if pending.Label != "Geodata geoip.metadb" {
					t.Fatalf("expected geodata approval, got %+v", pending)
				}
			}
			if tc.stop {
				call[protocol.Status](t, c, protocol.OpStop, nil)
			} else {
				call[protocol.Status](t, c, protocol.OpApply, owned(protocol.Settings{SubscriptionUpdatesDisabled: true}))
			}
			waitFor(t, func() bool { return len(call[protocol.DownloadList](t, c, protocol.OpDownloads, nil).Downloads) == 0 })
			if err := callErr(t, c, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: pending.ID, Allow: true}); err.Code != protocol.CodeNotFound {
				t.Fatalf("cancelled approval remained usable: %v", err)
			}
			after := call[protocol.SubscriptionList](t, c, protocol.OpSubscriptions, nil).Subscriptions[0]
			if after.UpdatedAt != sub.UpdatedAt || after.NextUpdate != sub.NextUpdate || after.Error != sub.Error {
				t.Fatalf("cancelling changed retry state: before=%+v after=%+v", sub, after)
			}
			o.set("/work", page{body: workYAML, header: titled("Work")})
			done := make(chan error, 1)
			go func() {
				done <- c.Call(t.Context(), protocol.OpSubscriptionUpdate, protocol.NameArgs{Name: "Work"}, nil)
			}()
			manual := nextDownload(t, watcher)
			call[protocol.Empty](t, watcher, protocol.OpDownloadDecision, protocol.DownloadDecisionArgs{ID: manual.ID, Allow: true})
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("manual refresh blocked behind cancelled scheduled refresh")
			}
		})
	}
}

type validatingGeodata struct {
	*fakecore.Core
	entered, release chan struct{}
}

func (c *validatingGeodata) InstallGeodata(f core.GeodataFile, path string, p core.Profile) error {
	close(c.entered)
	<-c.release
	return c.Core.InstallGeodata(f, path, p)
}

func TestCancelledRefreshDoesNotApplyAfterGeodataValidation(t *testing.T) {
	o := newOrigin(t)
	o.set("/work", page{body: workYAML, header: titled("Work")})
	o.set("/geoip.metadb", page{body: "geodata"})
	geoip := filepath.Join(t.TempDir(), "geoip.metadb")
	c0 := &validatingGeodata{Core: fakecore.New(), entered: make(chan struct{}), release: make(chan struct{})}
	c0.SetGeodata(func(p core.Profile) []core.GeodataFile {
		if slices.ContainsFunc(p.Rules, func(r core.Rule) bool { return r.Type == "GEOIP" }) {
			return []core.GeodataFile{{Path: geoip, URL: o.URL + "/geoip.metadb"}}
		}
		return nil
	})
	h := serveCore(t, os.Getuid(), c0, append(o.opts(), daemon.WithRefreshCheck(time.Millisecond))...)
	unblock := sync.OnceFunc(func() { close(c0.release) })
	t.Cleanup(unblock)
	c := h.dial(t)
	sub := call[protocol.Subscription](t, c, protocol.OpSubscriptionAdd, protocol.SubscriptionURLArgs{URL: o.url("/work")})
	call[protocol.Status](t, c, protocol.OpStart, nil)
	o.set("/work", page{body: strings.Replace(workYAML, "  - MATCH,Proxy", "  - GEOIP,NL,DIRECT\n  - MATCH,Proxy", 1), header: titled("Work")})
	h.clock.advance(time.Duration(sub.Interval) * time.Second)
	select {
	case <-c0.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled refresh did not validate geodata")
	}
	h.cancel()
	unblock()
	h.shutdown(t)
	if cfg, _ := c0.Config(); slices.ContainsFunc(cfg.Profile.Rules, func(r core.Rule) bool { return r.Type == "GEOIP" }) {
		t.Fatal("cancelled refresh applied new content after validation")
	}
	h.clock.advance(-time.Duration(sub.Interval) * time.Second)
	h.restart(t)
	after := call[protocol.SubscriptionList](t, h.dial(t), protocol.OpSubscriptions, nil).Subscriptions[0]
	if after.UpdatedAt != sub.UpdatedAt || after.NextUpdate != sub.NextUpdate || after.Error != sub.Error {
		t.Fatalf("cancelled refresh changed saved retry state: before=%+v after=%+v", sub, after)
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
