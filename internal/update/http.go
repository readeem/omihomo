package update

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  15 * time.Second,
			MaxResponseHeaderBytes: 32 << 10,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
			DisableCompression:     true,
			DisableKeepAlives:      true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 4 || !allowedURL(req.URL) {
				return errors.New("release redirect is outside the trusted download hosts")
			}
			return nil
		},
	}
}

func allowedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func (u *Updater) read(ctx context.Context, address string, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := u.transfer(ctx, address, &buf, limit, false); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (u *Updater) download(ctx context.Context, address string, dest io.Writer, size int64) error {
	return u.transfer(ctx, address, dest, size, true)
}

func (u *Updater) transfer(ctx context.Context, address string, dest io.Writer, limit int64, exact bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil || !allowedURL(req.URL) {
		return errors.New("invalid release download address")
	}
	req.Header.Set("User-Agent", "omihomo-update")
	resp, err := u.client.Do(req)
	if err != nil {
		// Signed asset redirect URLs contain bearer query parameters.
		// Never echo net/http's URL-bearing error to the terminal or widget.
		return errors.New("release download failed; check connectivity and try again")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit || (exact && resp.ContentLength >= 0 && resp.ContentLength != limit) {
		return errors.New("release download has an unexpected size")
	}
	n, err := io.Copy(dest, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return errors.New("release download was interrupted")
	}
	if n > limit || (exact && n != limit) {
		return errors.New("release download has an unexpected size")
	}
	return nil
}
