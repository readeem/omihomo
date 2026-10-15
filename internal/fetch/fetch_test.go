package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const token = "s3cr3tT0ken"

// server starts a TLS server and a Fetcher that trusts it.
func server(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Fetcher) {
	t.Helper()
	return serverWith(t, h, func(*http.Transport) {})
}

func serverWith(t *testing.T, h http.HandlerFunc, tweak func(*http.Transport)) (*httptest.Server, *Fetcher) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // handshakes the tests fail on purpose
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tweak(tr)
	t.Cleanup(tr.CloseIdleConnections)
	return srv, New(tr)
}

func get(f *Fetcher, url string) (*Response, error) {
	return f.Get(context.Background(), Request{URL: url, Timeout: 5 * time.Second})
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGet(t *testing.T) {
	var ua, accept atomic.Value
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		accept.Store(r.Header.Get("Accept"))
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2")
		w.Write([]byte("proxies: []\n"))
	})
	resp, err := get(f, srv.URL+"/sub?token="+token)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "proxies: []\n" {
		t.Errorf("body = %q", resp.Body)
	}
	if got := resp.Header.Get("Subscription-Userinfo"); got != "upload=1; download=2" {
		t.Errorf("Subscription-Userinfo = %q", got)
	}
	if ua.Load() != UserAgent {
		t.Errorf("User-Agent = %q, want %q", ua.Load(), UserAgent)
	}
	if accept.Load() != "*/*" {
		t.Errorf("Accept = %q", accept.Load())
	}
}

func TestPlainHTTPRefusedBeforeRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, err := New(nil).Get(context.Background(), Request{URL: srv.URL + "/sub?token=" + token})
	if !errors.Is(err, ErrInsecure) {
		t.Fatalf("err = %v, want ErrInsecure", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server saw %d requests", hits.Load())
	}
}

func TestRedirects(t *testing.T) {
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-https":
			http.Redirect(w, r, "/final?token="+token, http.StatusFound)
		case "/to-http":
			http.Redirect(w, r, "http://127.0.0.1:1/final?token="+token, http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop?token="+token, http.StatusFound)
		case "/final":
			w.Write([]byte("ok"))
		}
	})
	resp, err := get(f, srv.URL+"/to-https")
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "ok" {
		t.Errorf("body = %q", resp.Body)
	}
	if _, err := get(f, srv.URL+"/to-http"); !errors.Is(err, ErrInsecure) {
		t.Errorf("redirect to http: err = %v, want ErrInsecure", err)
	}
	if _, err := get(f, srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("redirect loop: err = %v", err)
	}
}

func TestTooLarge(t *testing.T) {
	const limit = 1 << 10
	big := bytes.Repeat([]byte("a"), limit+1)
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/length":
			w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			w.Write(big)
		case "/chunked":
			// Flushing first sends no Content-Length.
			w.Write(big[:10])
			w.(http.Flusher).Flush()
			w.Write(big[10:])
		case "/early":
			// Announces a huge body and stalls: only the Content-Length
			// check fails this before the timeout.
			w.Header().Set("Content-Length", strconv.Itoa(1<<30))
			w.Write(big[:10])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/exact":
			w.Write(big[:limit])
		}
	})
	for _, path := range []string{"/length", "/chunked", "/early"} {
		_, err := f.Get(context.Background(), Request{URL: srv.URL + path, MaxBytes: limit, Timeout: 5 * time.Second})
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v, want ErrTooLarge", path, err)
		}
	}
	resp, err := f.Get(context.Background(), Request{URL: srv.URL + "/exact", MaxBytes: limit})
	if err != nil || len(resp.Body) != limit {
		t.Errorf("body at the limit: err = %v", err)
	}
}

func TestGzip(t *testing.T) {
	plain := []byte(strings.Repeat("proxies: []\n", 100))
	small := gzipped(t, plain)
	bomb := gzipped(t, make([]byte, 8<<20))
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Gzips whether or not the client asked.
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Profile-Update-Interval", "24")
		if r.URL.Path == "/bomb" {
			w.Write(bomb)
			return
		}
		w.Write(small)
	}
	transports := map[string]func(*http.Transport){
		"transport decodes": func(*http.Transport) {},
		"fetch decodes":     func(tr *http.Transport) { tr.DisableCompression = true },
	}
	for name, tweak := range transports {
		t.Run(name, func(t *testing.T) {
			srv, f := serverWith(t, handler, tweak)
			resp, err := get(f, srv.URL+"/sub")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(resp.Body, plain) {
				t.Errorf("body not decoded: %d bytes", len(resp.Body))
			}
			if ce := resp.Header.Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q after decoding", ce)
			}
			if resp.Header.Get("Profile-Update-Interval") != "24" {
				t.Error("headers lost")
			}
			_, err = f.Get(context.Background(), Request{URL: srv.URL + "/bomb", MaxBytes: 1 << 20, Timeout: 5 * time.Second})
			if !errors.Is(err, ErrTooLarge) {
				t.Errorf("gzip bomb: err = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	date := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/seconds":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/date":
			w.Header().Set("Retry-After", date)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	})
	cases := []struct {
		path     string
		code     int
		min, max time.Duration
	}{
		{"/missing", 404, 0, 0},
		{"/seconds", 429, 120 * time.Second, 120 * time.Second},
		{"/date", 503, 58 * time.Minute, time.Hour},
	}
	for _, c := range cases {
		_, err := get(f, srv.URL+c.path)
		var se *StatusError
		if !errors.As(err, &se) {
			t.Errorf("%s: err = %v, want *StatusError", c.path, err)
			continue
		}
		if se.Code != c.code || se.RetryAfter < c.min || se.RetryAfter > c.max {
			t.Errorf("%s: %+v, want code %d, retry after %s..%s", c.path, se, c.code, c.min, c.max)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{" 30 ", 30 * time.Second},
		{"-5", 0},
		{"1.5", 0},
		{"soon", 0},
		{"99999999", maxRetryAfter},
		{"999999999999999999999999", maxRetryAfter},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{now.Add(400 * 24 * time.Hour).Format(http.TimeFormat), maxRetryAfter},
	}
	for _, c := range cases {
		if got := retryAfter(c.in, now); got != c.want {
			t.Errorf("retryAfter(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestTimeout(t *testing.T) {
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/body" {
			w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})
	for _, path := range []string{"/headers", "/body"} {
		start := time.Now()
		_, err := f.Get(context.Background(), Request{URL: srv.URL + path, Timeout: 100 * time.Millisecond})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: err = %v, want deadline exceeded", path, err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: took %s", path, d)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := f.Get(ctx, Request{URL: srv.URL + "/headers"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: err = %v, want context.Canceled", err)
	}
}

func TestErrorsHideURL(t *testing.T) {
	srv, f := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			w.Write(make([]byte, 2048))
		case "/to-http":
			http.Redirect(w, r, "http://127.0.0.1:1/x?token="+token, http.StatusFound)
		case "/bad-location":
			w.Header().Set("Location", "https://[bad/x?token="+token)
			w.WriteHeader(http.StatusFound)
		case "/relative-bad-location":
			// Neither the request's path nor its query appears in it.
			w.Header().Set("Location", "/redirect/%zz?secret="+token)
			w.WriteHeader(http.StatusFound)
		case "/bad-gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.Write([]byte("not gzip " + token))
		case "/slow":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	})
	closed := httptest.NewTLSServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	untrusted := New(nil) // does not trust the test certificate
	cases := []struct {
		name string
		f    *Fetcher
		url  string
	}{
		{"status", f, srv.URL + "/missing/" + token + "?token=" + token},
		{"too large", f, srv.URL + "/large?token=" + token},
		{"relative bad location", f, srv.URL + "/relative-bad-location?other=1"},
		{"redirect to http", f, srv.URL + "/to-http?token=" + token},
		{"bad location", f, srv.URL + "/bad-location?token=" + token},
		{"bad gzip", f, srv.URL + "/bad-gzip?token=" + token},
		{"timeout", f, srv.URL + "/slow?token=" + token},
		{"refused", f, closedURL + "/sub?token=" + token},
		{"untrusted certificate", untrusted, srv.URL + "/sub?token=" + token},
		{"userinfo", untrusted, strings.Replace(srv.URL, "https://", "https://user:"+token+"@", 1) + "/sub"},
		{"token in path", untrusted, srv.URL + "/sub/" + token},
		{"plain http", f, "http://example.com/sub?token=" + token},
		{"whitespace", f, "https://example.com/sub?token=" + token + " x"},
		{"unparsable", f, "https://[bad/sub?token=" + token},
	}
	for _, c := range cases {
		_, err := c.f.Get(context.Background(), Request{URL: c.url, MaxBytes: 1024, Timeout: 500 * time.Millisecond})
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		t.Logf("%s: %v", c.name, err)
		if strings.Contains(err.Error(), token) {
			t.Errorf("%s: error leaks the token: %v", c.name, err)
		}
		if !strings.HasPrefix(err.Error(), "download failed: ") {
			t.Errorf("%s: error = %q", c.name, err)
		}
	}
}

func TestCheckURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://example.com/sub?token=abc&flag=clash", true},
		{"https://user:pass@example.com:8443/api/v1/sub#frag", true},
		{"HTTPS://example.com/sub", true},
		{"https://[::1]:443/sub", true},
		{"", false},
		{"http://example.com/sub", false},
		{"ftp://example.com/sub", false},
		{"example.com/sub", false},
		{"https:///sub", false},
		{"https://:443/sub", false},
		{"https:opaque", false},
		{"https://example.com/a b", false},
		{" https://example.com/sub", false},
		{"https://example.com/sub\n", false},
		{"https://example.com/\x00", false},
		{"https://example.com/\x7f", false},
		{"https://example.com/ ", false},
		{"https://example.com/\xff", false},
		{"https://[bad/sub", false},
		{"https://example.com/" + strings.Repeat("a", maxURLBytes), false},
	}
	for _, c := range cases {
		err := CheckURL(c.url)
		if (err == nil) != c.ok {
			t.Errorf("CheckURL(%.40q) = %v, want ok=%v", c.url, err, c.ok)
		}
		if err != nil && c.url != "" && strings.Contains(err.Error(), c.url) {
			t.Errorf("CheckURL(%.40q) repeats the URL: %v", c.url, err)
		}
	}
	if err := CheckURL("http://example.com"); !errors.Is(err, ErrInsecure) {
		t.Errorf("http: err = %v, want ErrInsecure", err)
	}
}

func TestDefaultTransport(t *testing.T) {
	tr, ok := New(nil).client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("default transport is not an *http.Transport")
	}
	if tr.Proxy != nil {
		t.Error("default transport uses a proxy")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("default transport allows TLS below 1.2")
	}
	if tr.DisableCompression {
		t.Error("default transport does not ask for gzip")
	}
}
