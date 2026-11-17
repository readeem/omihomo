package xray_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/vless/inbound"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/core/xray"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/protocol"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRoutesTrafficThroughNativeVLESS(t *testing.T) {
	const id = "92d696db-0166-464e-91bb-b65f72b3d665"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "from the VLESS exit")
	}))
	defer origin.Close()
	port := freePort(t)
	var native conf.Config
	fixture, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id}}},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "freedom", "settings": map[string]any{
				"redirect":   strings.TrimPrefix(origin.URL, "http://"),
				"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	serverConfig, err := native.Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := xcore.New(serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	mixedPort := freePort(t)
	c := xray.New(mixedPort, nil)
	defer c.Stop(context.Background())
	cfg := core.Config{Mode: protocol.ModeRule, Profile: core.Profile{
		Proxies: []core.Proxy{
			{Name: "Xray exit", Type: "vless", Options: map[string]any{
				"server": "127.0.0.1", "port": port, "uuid": id, "skip-cert-verify": false,
			}},
			{Name: "Unavailable", Type: "tuic"},
		},
		Groups: []core.Group{
			{Name: "Proxy", Type: "select", Members: []string{"Xray exit", "Unavailable", "REJECT"}},
			{Name: "TCP only", Type: "select", Members: []string{"Xray exit"}, Options: map[string]any{"disable-udp": true}},
		},
		Rules: []core.Rule{
			{Type: "IP-CIDR", Value: "0.0.0.0/0", Target: "REJECT", Source: protocol.SourceSubscription},
			{Type: "IP-CIDR", Value: "127.0.0.1/32", Target: "REJECT", Params: []string{"no-resolve"}, Source: protocol.SourceSubscription},
			{Type: "DOMAIN", Value: "proxy-test.invalid", Target: "Proxy", Source: protocol.SourceSubscription},
			{Type: "MATCH", Target: "REJECT", Source: protocol.SourceSubscription},
		},
	}}
	if err := c.Check(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	compat := c.Compatibility(cfg)
	if len(compat.Warnings) != 3 || !strings.Contains(compat.Warnings[0], "Unavailable") ||
		!strings.Contains(compat.Warnings[1], "cannot disable UDP") || !strings.Contains(compat.Warnings[2], "IP-CIDR") {
		t.Fatalf("missing incompatibility warnings: %v", compat.Warnings)
	}
	if len(compat.Rules) != 3 || compat.Rules[0].Params != "no-resolve" {
		t.Fatalf("unexpected supported rules: %v", compat.Rules)
	}
	if err := c.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: c.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	dst := &url.URL{Scheme: "http", Host: "proxy-test.invalid"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dst.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	loopback := net.JoinHostPort("127.0.0.1", strconv.Itoa(mixedPort))
	for _, scheme := range []string{"http", "socks5"} {
		local := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: scheme, Host: loopback})}
		defer local.CloseIdleConnections()
		checkResponse(t, &http.Client{Transport: local}, req)
	}
	checkResponse(t, client, req)
	download := fetch.RoutedTransport(c.DialDownload, func(context.Context, string, string) (net.Conn, error) {
		t.Error("download tried direct recovery after reaching the exit")
		return nil, errors.New("unexpected direct recovery")
	})
	defer download.CloseIdleConnections()
	for _, dst := range []string{"https://1.1.1.1", "https://example.com"} {
		if _, err := fetch.New(download).Get(ctx, fetch.Request{URL: dst, AllowDirect: true}); err == nil {
			t.Fatal("plain HTTP origin served HTTPS")
		}
	}
	plainReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://1.1.1.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, &http.Client{Transport: download}, plainReq)
	if err := c.Select(ctx, "Proxy", "REJECT"); err != nil {
		t.Fatal(err)
	}
	transport.CloseIdleConnections()
	if res, err := client.Do(req); err == nil {
		res.Body.Close()
		t.Fatal("traffic went through after switching the group to REJECT")
	}
}

func TestUnreachableExitNeedsDownloadApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c := xray.New(0, nil)
	defer c.Stop(context.Background())
	cfg := core.Config{Mode: protocol.ModeGlobal, Profile: core.Profile{Proxies: []core.Proxy{{
		Name: "Closed exit", Type: "vless", Options: map[string]any{"server": "127.0.0.1", "port": freePort(t), "uuid": "92d696db-0166-464e-91bb-b65f72b3d665"},
	}}}}
	if err := c.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	transport := fetch.RoutedTransport(c.DialDownload, func(context.Context, string, string) (net.Conn, error) {
		t.Error("download used the uplink before approval")
		return nil, errors.New("unexpected direct recovery")
	})
	defer transport.CloseIdleConnections()
	_, err := fetch.New(transport).Get(ctx, fetch.Request{URL: "https://example.com"})
	if !errors.Is(err, fetch.ErrDirectRequired) {
		t.Fatalf("unreachable exit did not ask for approval: %v", err)
	}
}

func TestExitTLSFailureDoesNotAllowDownloadRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	exit, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := exit.AcceptTCP()
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			io.CopyN(io.Discard, conn, 1)
			conn.SetLinger(0)
			conn.Close()
		}
	}()
	defer func() {
		exit.Close()
		<-done
	}()

	c := xray.New(0, nil)
	defer c.Stop(context.Background())
	cfg := core.Config{Mode: protocol.ModeGlobal, Profile: core.Profile{Proxies: []core.Proxy{{
		Name: "Broken TLS exit", Type: "vless",
		Options: map[string]any{"server": "127.0.0.1", "port": exit.Addr().(*net.TCPAddr).Port,
			"uuid": "92d696db-0166-464e-91bb-b65f72b3d665", "tls": true},
	}}}}
	if err := c.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	transport := fetch.RoutedTransport(c.DialDownload, func(context.Context, string, string) (net.Conn, error) {
		t.Error("exit TLS failure allowed direct recovery")
		return nil, errors.New("unexpected direct recovery")
	})
	defer transport.CloseIdleConnections()
	_, err = fetch.New(transport).Get(ctx, fetch.Request{URL: "https://example.com", AllowDirect: true})
	if err == nil {
		t.Fatal("exit TLS handshake succeeded after the connection reset")
	}
}

func TestStoppingClosesPendingSOCKSHandshake(t *testing.T) {
	port := freePort(t)
	c := xray.New(port, nil)
	defer c.Stop(context.Background())
	if err := c.Apply(t.Context(), core.Config{Mode: protocol.ModeRule}); err != nil {
		t.Fatal(err)
	}
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response != [2]byte{5, 0} {
		t.Fatalf("SOCKS greeting: %v, %v", response, err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(response[:])
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stopped core retained an accepted client connection: %v", err)
	}
}

func TestRepeatedRuleSetsExceedXrayRuleBudget(t *testing.T) {
	payload := make([]string, 10_001)
	for i := range payload {
		payload[i] = strconv.Itoa(i) + ".example"
	}
	profile := core.Profile{
		RuleSets: []core.RuleSet{{Name: "domains", Behavior: "domain", Payload: payload}},
	}
	for range 10 {
		profile.Rules = append(profile.Rules, core.Rule{Type: "RULE-SET", Value: "domains", Target: "REJECT"})
	}
	c := xray.New(0, nil)
	report := c.Compatibility(core.Config{Mode: protocol.ModeRule, Profile: profile})
	if len(report.Rules) != 9 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "Xray rule limit") {
		t.Fatalf("expected nine accepted references and one warning, got %d and %v", len(report.Rules), report.Warnings)
	}
}

func checkResponse(t *testing.T, client *http.Client, req *http.Request) {
	t.Helper()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != "from the VLESS exit" {
		t.Fatalf("response %q: %v", body, err)
	}
}
