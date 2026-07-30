package slackhttp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// captureRT records every request it sees and forwards to a wrapped RT.
type captureRT struct {
	wrapped http.RoundTripper
	last    *http.Request
}

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	c.last = req.Clone(req.Context())
	return c.wrapped.RoundTrip(req)
}

func newCaptureClient(t *testing.T, srv *httptest.Server) (*http.Client, *captureRT) {
	t.Helper()
	recorder := &captureRT{wrapped: http.DefaultTransport}
	bt := &BrowserTransport{Inner: recorder}
	return &http.Client{Transport: bt}, recorder
}

func TestBrowserTransport_AddsHeadersToSlackHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	client, recorder := newCaptureClient(t, srv)

	// Force the request to look like it's going to slack.com by rewriting Host.
	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	got := recorder.last
	if !strings.HasPrefix(got.Header.Get("User-Agent"), "Mozilla/5.0") {
		t.Errorf("User-Agent = %q; want Mozilla/5.0-prefixed Chrome UA", got.Header.Get("User-Agent"))
	}
	if got.Header.Get("Origin") != "https://app.slack.com" {
		t.Errorf("Origin = %q; want https://app.slack.com", got.Header.Get("Origin"))
	}
	for _, h := range []string{"Accept", "Accept-Language", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		if got.Header.Get(h) == "" {
			t.Errorf("header %s is empty; expected a value", h)
		}
	}
}

func TestBrowserTransport_MatchesSlackSubdomains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	hosts := []string{"slack.com", "files.slack.com", "hackclub.enterprise.slack.com", "wss-primary.slack.com"}
	for _, h := range hosts {
		t.Run(h, func(t *testing.T) {
			client, recorder := newCaptureClient(t, srv)
			req, err := http.NewRequest("GET", srv.URL, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Host = h
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if recorder.last.Header.Get("Origin") == "" {
				t.Errorf("host %s: Origin header missing; expected the transport to recognize this as a Slack host", h)
			}
		})
	}
}

func TestBrowserTransport_DoesNotTouchNonSlackHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	client, recorder := newCaptureClient(t, srv)
	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "example.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if recorder.last.Header.Get("Origin") != "" {
		t.Errorf("Origin set on non-Slack host: %q", recorder.last.Header.Get("Origin"))
	}
	if ua := recorder.last.Header.Get("User-Agent"); strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Errorf("browser User-Agent leaked to non-Slack host: %q", ua)
	}
}

func TestBrowserTransport_DoesNotOverrideCallerHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	client, recorder := newCaptureClient(t, srv)
	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "slack.com"
	req.Header.Set("User-Agent", "custom-agent/1.0")
	req.Header.Set("Authorization", "Bearer xoxc-test")
	req.Header.Set("Cookie", "d=test-cookie")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if got := recorder.last.Header.Get("User-Agent"); got != "custom-agent/1.0" {
		t.Errorf("User-Agent was overridden: got %q, want custom-agent/1.0", got)
	}
	if got := recorder.last.Header.Get("Authorization"); got != "Bearer xoxc-test" {
		t.Errorf("Authorization was overridden: %q", got)
	}
	if got := recorder.last.Header.Get("Cookie"); got != "d=test-cookie" {
		t.Errorf("Cookie was overridden: %q", got)
	}
}

func TestBrowserTransport_HandlesNilHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Literal request — Header is nil, URL is set, Host forces Slack-host match.
	req := &http.Request{
		Method: "GET",
		URL:    u,
		Host:   "slack.com",
	}

	client, recorder := newCaptureClient(t, srv)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if recorder.last.Header.Get("Origin") != "https://app.slack.com" {
		t.Errorf("Origin header missing after nil-Header request")
	}
}

func TestBrowserHeaders_ContainsAllRequiredKeys(t *testing.T) {
	h := BrowserHeaders()
	// No Referer: the official web client sends none on /api/ calls, so
	// neither does the WebSocket upgrade path this feeds.
	for _, key := range []string{"User-Agent", "Accept", "Accept-Language", "Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform", "Cache-Control", "Pragma", "Priority"} {
		if h.Get(key) == "" {
			t.Errorf("BrowserHeaders missing %s", key)
		}
	}
}

func TestUserAgentForGOOS(t *testing.T) {
	cases := []struct {
		goos       string
		wantSubstr string
	}{
		{"linux", "X11; Linux x86_64"},
		{"darwin", "Macintosh; Intel Mac OS X"},
		{"windows", "Windows NT 10.0; Win64; x64"},
		{"freebsd", "X11; Linux x86_64"}, // unknown → linux fallback
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			got := userAgentForGOOS(tc.goos)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("userAgentForGOOS(%q) = %q; want substring %q", tc.goos, got, tc.wantSubstr)
			}
			if !strings.HasPrefix(got, "Mozilla/5.0") {
				t.Errorf("userAgentForGOOS(%q) = %q; want Mozilla/5.0 prefix", tc.goos, got)
			}
		})
	}
}

func TestUserAgentAndClientHintsShareMajorVersion(t *testing.T) {
	ua := UserAgent()
	// Extract "150" from ".../Chrome/150.0.0.0 Safari/..."
	m := regexp.MustCompile(`Chrome/(\d+)\.`).FindStringSubmatch(ua)
	if m == nil {
		t.Fatalf("UserAgent() = %q; no Chrome/<major> found", ua)
	}
	uaMajor := m[1]

	hint := ClientHintUA()
	if !strings.Contains(hint, `"Chromium";v="`+uaMajor+`"`) {
		t.Errorf("ClientHintUA() = %q; want it to contain Chromium v=%q", hint, uaMajor)
	}
	if !strings.Contains(hint, `"Google Chrome";v="`+uaMajor+`"`) {
		t.Errorf("ClientHintUA() = %q; want it to contain Google Chrome v=%q", hint, uaMajor)
	}
}

func TestClientHintUAMatchesCapture(t *testing.T) {
	// Verified value: 1032 requests across five HAR captures of the
	// Slack web client, 2026-07-30, Chrome 150 on Linux. See
	// docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md
	// ("Verified impersonation values").
	//
	// Chrome permutes the GREASE token and the brand ordering between
	// versions, so this is pinned verbatim rather than assembled from
	// parts: bumping the version requires a fresh capture.
	const want = `"Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"`
	if got := ClientHintUA(); got != want {
		t.Errorf("ClientHintUA() = %q; want %q", got, want)
	}
}

func TestUserAgentMatchesCapture(t *testing.T) {
	// Verified: 1516 requests across five HAR captures, 2026-07-30.
	const want = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	if got := userAgentForGOOS("linux"); got != want {
		t.Errorf("userAgentForGOOS(\"linux\") = %q; want %q", got, want)
	}
}

func TestClientHintPlatformForGOOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		// Values must match exactly what Chrome sends in
		// sec-ch-ua-platform. Chrome uses "macOS" (not "Mac OS X"),
		// "Windows", and "Linux", each including the double quotes as
		// part of the header value.
		{"darwin", `"macOS"`},
		{"windows", `"Windows"`},
		{"linux", `"Linux"`},
		{"freebsd", `"Linux"`},
		{"openbsd", `"Linux"`},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := clientHintPlatformForGOOS(tt.goos); got != tt.want {
				t.Errorf("clientHintPlatformForGOOS(%q) = %q; want %q", tt.goos, got, tt.want)
			}
		})
	}
}

func TestClientHintPlatformDelegatesToGOOS(t *testing.T) {
	want := clientHintPlatformForGOOS(runtime.GOOS)
	got := ClientHintPlatform()
	if got != want {
		t.Errorf("ClientHintPlatform() = %q; want %q (must delegate to runtime.GOOS)", got, want)
	}
	// sec-ch-ua-platform is a structured-header string: the quotes are
	// part of the value, not Go syntax.
	if len(got) < 2 || got[0] != '"' || got[len(got)-1] != '"' {
		t.Errorf("ClientHintPlatform() = %q; want a double-quoted value", got)
	}
}

func TestBrowserTransport_HeaderParity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	client, recorder := newCaptureClient(t, srv)

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Host = "slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	got := recorder.last

	// Present, matching the official client.
	want := map[string]string{
		"Sec-Ch-Ua":          ClientHintUA(),
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": ClientHintPlatform(),
		"Cache-Control":      "no-cache",
		"Pragma":             "no-cache",
		"Priority":           "u=1, i",
	}
	for k, v := range want {
		if got.Header.Get(k) != v {
			t.Errorf("header %s = %q; want %q", k, got.Header.Get(k), v)
		}
	}

	// Absent: the official client sends no Referer on API calls.
	if r := got.Header.Get("Referer"); r != "" {
		t.Errorf("Referer = %q; want absent (official client sends none)", r)
	}
}

func TestBrowserHeadersMatchesRoundTripHeaders(t *testing.T) {
	// BrowserHeaders() is the exported view of what RoundTrip puts on
	// HTTP requests; the two must not drift. It does NOT feed the
	// WebSocket dialer — that path uses WebSocketHeaders(), a
	// deliberately smaller set, because real Chrome sends fewer headers
	// on a WS upgrade than on an XHR.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newCaptureClient(t, srv)

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Host = "slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	ws := BrowserHeaders()
	for k := range ws {
		if got, want := recorder.last.Header.Get(k), ws.Get(k); got != want {
			t.Errorf("header %s: RoundTrip set %q, BrowserHeaders has %q", k, got, want)
		}
	}
	// And the reverse: nothing RoundTrip sets should be missing from
	// BrowserHeaders (ignoring headers net/http adds itself).
	for k := range recorder.last.Header {
		switch k {
		case "Accept-Encoding", "Content-Length", "Host", "User-Agent":
			continue // net/http or checked above
		}
		if ws.Get(k) == "" {
			t.Errorf("RoundTrip sets %s but BrowserHeaders does not", k)
		}
	}
	if ws.Get("User-Agent") != recorder.last.Header.Get("User-Agent") {
		t.Error("User-Agent differs between RoundTrip and BrowserHeaders")
	}
}

func TestWebSocketHeadersMatchesCapture(t *testing.T) {
	// Verified against the WS upgrade (status 101) in initial-load.har
	// and coldboot.har, 2026-07-30, Chrome 150 on Linux. Chrome sends a
	// SMALLER header set on a WebSocket upgrade than on an XHR: no
	// Accept, no Sec-Fetch-*, no sec-ch-ua*, no Priority, no Referer.
	h := WebSocketHeaders()

	want := map[string]string{
		"User-Agent":      UserAgent(),
		"Accept-Language": "en-US,en;q=0.9",
		"Cache-Control":   "no-cache",
		"Pragma":          "no-cache",
		"Origin":          "https://app.slack.com",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("WebSocketHeaders()[%s] = %q; want %q", k, got, v)
		}
	}

	// Headers real Chrome does NOT send on a WS upgrade. Sending any of
	// them is a slk-specific signature.
	mustBeAbsent := []string{
		"Accept",
		"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest",
		"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform",
		"Priority",
		"Referer",
	}
	for _, k := range mustBeAbsent {
		if got := h.Get(k); got != "" {
			t.Errorf("WebSocketHeaders()[%s] = %q; want absent (real Chrome omits it on WS upgrade)", k, got)
		}
	}

	// Exact key count guards against silently gaining a header later.
	if len(h) != len(want) {
		keys := make([]string, 0, len(h))
		for k := range h {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("WebSocketHeaders() has %d headers %v; want exactly %d", len(h), keys, len(want))
	}
}
