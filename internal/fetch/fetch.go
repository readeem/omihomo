// Package fetch downloads subscriptions, providers and geodata for the
// daemon: https only, with a size limit and a timeout on every request.
//
// Subscription URLs carry access tokens, so no error from this package
// repeats the URL it was given or one a server redirected to.
package fetch

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits and timeouts.
const (
	MaxBytes       = 64 << 20         // default body limit, after decoding
	DefaultTimeout = 60 * time.Second // whole request, body included
	connectTimeout = 10 * time.Second
	headerTimeout  = 30 * time.Second
	UserAgent      = "clash.meta" // subscription servers content-negotiate on it

	maxURLBytes   = 8 << 10
	maxRedirects  = 10
	maxErrorBody  = 4 << 10
	maxRetryAfter = 7 * 24 * time.Hour
)

var (
	// ErrTooLarge means the body exceeds the limit.
	ErrTooLarge = errors.New("response is larger than the size limit")
	// ErrInsecure means a request or redirect that is not https.
	ErrInsecure = errors.New("only https URLs are allowed")

	errWithheld = errors.New("the cause is withheld, since it may name a URL")
	// errResponse is wrapped by what this package finds wrong with a
	// response, which never quotes a URL.
	errResponse = errors.New("bad response")
)

// StatusError is a response other than 200. RetryAfter is the server's
// Retry-After, in seconds or as an HTTP date, 0 when absent or invalid.
type StatusError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("server answered %d %s", e.Code, http.StatusText(e.Code))
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(", retry after %s", e.RetryAfter)
	}
	return msg
}

// Fetcher downloads over https. It is safe for concurrent use.
type Fetcher struct {
	client    *http.Client
	userAgent string
}

// New returns a Fetcher. transport nil means the default, which never uses
// a proxy from the environment. Tests pass one that trusts their server.
func New(transport http.RoundTripper) *Fetcher {
	if transport == nil {
		transport = defaultTransport()
	}
	return &Fetcher{
		client:    &http.Client{Transport: transport, CheckRedirect: checkRedirect},
		userAgent: UserAgent,
	}
}

// defaultTransport is http.DefaultTransport without the environment's
// proxy, with tighter timeouts and at least TLS 1.2. It is built rather
// than cloned so nothing that changes http.DefaultTransport leaks in.
func defaultTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: headerTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: stopped after %d redirects", errResponse, maxRedirects)
	}
	if req.URL.Scheme != "https" || req.URL.Host == "" {
		return fmt.Errorf("redirect refused: %w", ErrInsecure)
	}
	return nil
}

// Request is one download.
type Request struct {
	URL      string
	MaxBytes int64         // 0 = MaxBytes
	Timeout  time.Duration // 0 = DefaultTimeout
}

// Response is a successful download.
type Response struct {
	Body   []byte
	Header http.Header
}

// Get downloads r.URL. A status other than 200 is a *StatusError.
func (f *Fetcher) Get(ctx context.Context, r Request) (*Response, error) {
	resp, err := f.get(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	return resp, nil
}

func (f *Fetcher) get(ctx context.Context, r Request) (*Response, error) {
	if err := CheckURL(r.URL); err != nil {
		return nil, err
	}
	limit := r.MaxBytes
	if limit <= 0 {
		limit = MaxBytes
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeoutCause(ctx, timeout,
		fmt.Errorf("timed out after %s: %w", timeout, context.DeadlineExceeded))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return nil, errors.New("not a valid URL") // the parse error quotes it
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, failure(ctx, err, req.URL)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.CopyN(io.Discard, resp.Body, maxErrorBody)
		return nil, &StatusError{
			Code:       resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	body, err := readBody(resp, limit)
	if err != nil {
		return nil, failure(ctx, err, req.URL)
	}
	return &Response{Body: body, Header: resp.Header}, nil
}

// readBody reads at most limit decoded bytes. The transport decodes the
// gzip it asked for; a server that gzips unasked is decoded here.
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	body := io.Reader(resp.Body)
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch {
	case resp.Uncompressed || enc == "" || enc == "identity":
		if resp.ContentLength > limit {
			return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
		}
	case enc == "gzip" || enc == "x-gzip":
		zr, err := gzip.NewReader(resp.Body)
		if errors.Is(err, io.EOF) {
			return []byte{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: gzip does not decode", errResponse)
		}
		defer zr.Close()
		body = zr
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
	default:
		return nil, fmt.Errorf("%w: unsupported content encoding", errResponse)
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return data, nil
}

// failure explains err without the URL. A request that failed once ctx was
// done failed because of it, so the context's cause is the answer.
func failure(ctx context.Context, err error, u *url.URL) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return scrub(err, u)
}

// scrub drops the URL that *url.Error adds to err, and passes on only the
// causes known never to quote a URL: network, TLS and decoding failures, and
// this package's own. Anything else, such as a malformed Location header the
// client quotes, is withheld.
func scrub(err error, u *url.URL) error {
	for {
		ue, ok := err.(*url.Error)
		if !ok {
			break
		}
		err = ue.Err
	}
	if !knownCause(err) {
		return errWithheld
	}
	msg := err.Error()
	leaks := strings.Contains(msg, "://") ||
		u.RawQuery != "" && strings.Contains(msg, u.RawQuery) ||
		u.User != nil && strings.Contains(msg, u.User.String()) ||
		len(u.Path) > 1 && strings.Contains(msg, u.Path)
	if leaks {
		return errWithheld
	}
	return err
}

func knownCause(err error) bool {
	var (
		op        *net.OpError
		dns       *net.DNSError
		verify    *tls.CertificateVerificationError
		record    tls.RecordHeaderError
		alert     tls.AlertError
		unknownCA x509.UnknownAuthorityError
		host      x509.HostnameError
		invalid   x509.CertificateInvalidError
		corrupt   flate.CorruptInputError
	)
	return errors.Is(err, ErrTooLarge) || errors.Is(err, ErrInsecure) || errors.Is(err, errResponse) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, gzip.ErrHeader) || errors.Is(err, gzip.ErrChecksum) ||
		errors.As(err, &op) || errors.As(err, &dns) || errors.As(err, &verify) || errors.As(err, &record) ||
		errors.As(err, &alert) || errors.As(err, &unknownCA) || errors.As(err, &host) || errors.As(err, &invalid) ||
		errors.As(err, &corrupt)
}

// CheckURL reports whether raw is a URL the daemon will download: https,
// a host, no whitespace or control characters, at most 8 KiB. The error
// never repeats the URL.
func CheckURL(raw string) error {
	if raw == "" {
		return errors.New("URL is empty")
	}
	if len(raw) > maxURLBytes {
		return fmt.Errorf("URL is longer than %d KiB", maxURLBytes>>10)
	}
	if !utf8.ValidString(raw) {
		return errors.New("URL is not valid UTF-8")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("URL contains whitespace or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a valid URL") // the parse error quotes it
	}
	if u.Scheme != "https" {
		return ErrInsecure
	}
	if u.Hostname() == "" {
		return errors.New("URL has no host")
	}
	return nil
}

// retryAfter parses a Retry-After value, in seconds or as an HTTP date
// relative to now. Absurd values are capped at a week.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if strings.Trim(v, "0123456789") == "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n > uint64(maxRetryAfter/time.Second) {
			return maxRetryAfter // only ErrRange is possible here
		}
		return time.Duration(n) * time.Second
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	return min(max(t.Sub(now), 0), maxRetryAfter)
}
