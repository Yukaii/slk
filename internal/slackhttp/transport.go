// Package slackhttp provides a Slack-aware http.RoundTripper that decorates
// outbound requests with the headers a recent desktop Chrome would send when
// app.slack.com makes a fetch/XHR/WebSocket call. The goal is to make
// xoxc-token traffic indistinguishable from official browser-client traffic
// at the header level, so Enterprise Grid anomaly detectors don't flag slk
// as a non-browser client and sign the user out.
//
// See: docs/superpowers/plans/2026-05-20-browser-like-headers.md and GitHub
// issue #5 for context.
package slackhttp

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
)

// BrowserTransport wraps an inner http.RoundTripper and adds browser-like
// headers to requests bound for *.slack.com hosts. It never overwrites
// headers the caller has already set, so caller-controlled values like
// Authorization, Cookie, or a custom User-Agent for diagnostics survive.
type BrowserTransport struct {
	// Inner is the underlying transport that actually performs the round
	// trip. If nil, http.DefaultTransport is used.
	Inner http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *BrowserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if (req.URL != nil && isSlackHost(req.URL.Host)) || isSlackHost(req.Host) {
		// Clone the request so we don't mutate the caller's copy — net/http's
		// RoundTripper contract forbids in-place modification.
		req = req.Clone(req.Context())
		// http.Header.Clone() returns nil when its receiver is nil, so a
		// caller who constructed *http.Request as a literal without setting
		// Header would otherwise hit a "nil map" panic on the first Set.
		if req.Header == nil {
			req.Header = http.Header{}
		}
		setIfMissing(req.Header, "User-Agent", UserAgent())
		setIfMissing(req.Header, "Accept", "*/*")
		setIfMissing(req.Header, "Accept-Language", "en-US,en;q=0.9")
		setIfMissing(req.Header, "Origin", "https://app.slack.com")
		setIfMissing(req.Header, "Referer", "https://app.slack.com/")
		setIfMissing(req.Header, "Sec-Fetch-Site", "same-site")
		setIfMissing(req.Header, "Sec-Fetch-Mode", "cors")
		setIfMissing(req.Header, "Sec-Fetch-Dest", "empty")
	}
	inner := t.Inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	return inner.RoundTrip(req)
}

// NewBrowserHTTPClient returns an *http.Client wired up with BrowserTransport
// and an optional cookie jar. Use this anywhere an http.Client is needed for
// Slack traffic.
func NewBrowserHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Transport: &BrowserTransport{Inner: http.DefaultTransport},
		Jar:       jar,
	}
}

// BrowserHeaders returns the full set of browser-like headers as an
// http.Header value. Callers that can't use BrowserTransport (notably the
// WebSocket dialer in gorilla/websocket, which takes raw upgrade headers
// rather than a RoundTripper) consume this directly.
func BrowserHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", UserAgent())
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Origin", "https://app.slack.com")
	h.Set("Referer", "https://app.slack.com/")
	h.Set("Sec-Fetch-Site", "same-site")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Dest", "empty")
	return h
}

// chromeMajor is the Chrome major version slk impersonates. The
// User-Agent string and the sec-ch-ua client hints are both derived
// from it so they can never disagree — a Chrome UA paired with absent
// or mismatched client hints is a combination real Chrome never emits,
// and is trivially detectable. Bump this at release time.
const chromeMajor = "150"

// UserAgent returns a Chrome User-Agent appropriate for the host OS.
func UserAgent() string {
	return userAgentForGOOS(runtime.GOOS)
}

func userAgentForGOOS(goos string) string {
	const tmpl = "Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36"
	switch goos {
	case "darwin":
		return fmt.Sprintf(tmpl, "Macintosh; Intel Mac OS X 10_15_7", chromeMajor)
	case "windows":
		return fmt.Sprintf(tmpl, "Windows NT 10.0; Win64; x64", chromeMajor)
	default:
		// Linux and anything else (freebsd, openbsd, ...) → Linux UA.
		return fmt.Sprintf(tmpl, "X11; Linux x86_64", chromeMajor)
	}
}

// ClientHintUA returns the sec-ch-ua header value matching UserAgent().
// Format mirrors Chrome 150 exactly, including the GREASE brand.
func ClientHintUA() string {
	return fmt.Sprintf(`"Not;A=Brand";v="8", "Chromium";v="%s", "Google Chrome";v="%s"`,
		chromeMajor, chromeMajor)
}

// ClientHintPlatform returns the sec-ch-ua-platform value for the host OS.
func ClientHintPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return `"macOS"`
	case "windows":
		return `"Windows"`
	default:
		return `"Linux"`
	}
}

func isSlackHost(host string) bool {
	if host == "" {
		return false
	}
	// Strip any :port suffix.
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host == "slack.com" || strings.HasSuffix(host, ".slack.com")
}

func setIfMissing(h http.Header, key, value string) {
	if h.Get(key) == "" {
		h.Set(key, value)
	}
}
