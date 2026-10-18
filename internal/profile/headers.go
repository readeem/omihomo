package profile

import (
	"encoding/base64"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// MaxTitle bounds a subscription name in runes, including collision suffixes.
	MaxTitle = 64

	// DefaultInterval is how often a subscription refreshes when its server
	// does not say.
	DefaultInterval = 12 * time.Hour
)

// Name is what a subscription calls itself: the profile-title header,
// base64 when it says so, else the filename in content-disposition, else
// the URL's host. Every server-written name is made safe to use as a
// command argument: no control characters or path separators, no leading
// dot, trimmed and at most 64 characters.
func Name(h http.Header, rawURL string) string {
	title := h.Get("Profile-Title")
	if rest, ok := strings.CutPrefix(title, "base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
		title = ""
		if err == nil {
			title = string(decoded)
		}
	}
	name := cleanName(title)
	if name == "" {
		if _, params, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
			name = cleanName(params["filename"])
		}
	}
	if name == "" {
		if u, err := url.Parse(rawURL); err == nil {
			name = cleanName(u.Hostname())
		}
	}
	if name == "" {
		name = "subscription"
	}
	return name
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return ' '
		case unicode.IsControl(r) || r == unicode.ReplacementChar:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimLeft(s, ". ")
	if r := []rune(s); len(r) > MaxTitle {
		s = string(r[:MaxTitle])
	}
	return strings.TrimSpace(s)
}

// Usage is the subscription-userinfo header: bytes uploaded, downloaded and
// allowed, and when the subscription expires, in unix seconds. Each is nil
// when the server does not say.
type Usage struct {
	Upload, Download, Total, Expire *int64
}

func ReadUsage(h http.Header) Usage {
	var u Usage
	for field := range strings.SplitSeq(h.Get("Subscription-Userinfo"), ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			u.Upload = &n
		case "download":
			u.Download = &n
		case "total":
			u.Total = &n
		case "expire":
			u.Expire = &n
		}
	}
	return u
}

// maxInterval keeps a server's interval from overflowing a deadline.
const maxInterval = 365 * 24 * time.Hour

// Interval is the profile-update-interval header, whole positive hours,
// or DefaultInterval.
func Interval(h http.Header) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h.Get("Profile-Update-Interval")))
	if err != nil || n <= 0 {
		return DefaultInterval
	}
	if time.Duration(n) > maxInterval/time.Hour {
		return maxInterval
	}
	return time.Duration(n) * time.Hour
}
