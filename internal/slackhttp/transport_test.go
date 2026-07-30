package slackhttp

import (
	"bytes"
	"io"
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
	if req.Body == nil {
		return c.wrapped.RoundTrip(req)
	}
	// Request.Clone shares Body — it is a one-shot reader, and the
	// wrapped transport drains it. Buffer it so c.last still carries
	// what was actually sent, and hand the wrapped transport an
	// equivalent reader. ContentLength and GetBody on c.last are left
	// exactly as the transport under test set them.
	raw, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	c.last.Body = io.NopCloser(bytes.NewReader(raw))
	fwd := req.Clone(req.Context())
	fwd.Body = io.NopCloser(bytes.NewReader(raw))
	return c.wrapped.RoundTrip(fwd)
}

// roundTripFunc is a minimal inner transport for tests that must call
// BrowserTransport.RoundTrip directly, bypassing http.Client.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

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

func TestBrowserHeaderPairsMatchesCapture(t *testing.T) {
	// Every header a real Chrome 150 sends on a same-site XHR to Slack,
	// with exact values. Verified against the 2026-07-30 HAR captures;
	// see docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md
	// ("Verified impersonation values").
	//
	// This test is deliberately exact rather than a presence check: a
	// wrong value (Sec-Fetch-Mode: navigate on an XHR, say) is just as
	// identifying as a missing header, and an EXTRA header no real
	// Chrome sends is a stable slk-specific signature.
	want := map[string]string{
		"User-Agent":         UserAgent(),
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Origin":             "https://app.slack.com",
		"Sec-Fetch-Site":     "same-site",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Dest":     "empty",
		"Sec-Ch-Ua":          ClientHintUA(),
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": ClientHintPlatform(),
		"Cache-Control":      "no-cache",
		"Pragma":             "no-cache",
		"Priority":           "u=1, i",
	}
	got := browserHeaderPairs()

	for k, v := range want {
		if got[k] != v {
			t.Errorf("browserHeaderPairs()[%q] = %q; want %q", k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("browserHeaderPairs() has unexpected header %q = %q; "+
				"real Chrome does not send it, so it is an slk-specific signature",
				k, got[k])
		}
	}
	if len(got) != len(want) {
		t.Errorf("browserHeaderPairs() has %d headers; want exactly %d", len(got), len(want))
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

func TestImageHeaderPairsMatchesCapture(t *testing.T) {
	// Every header a real Chrome 150 sends on an <img> load from
	// Slack, with exact values. Measured across 40 files.slack.com
	// 200-responses in the 2026-07-30 captures.
	//
	// Six of these differ from the XHR set: the image Accept list,
	// Sec-Fetch-Dest: image, Sec-Fetch-Mode: no-cors, Priority: i, a
	// PRESENT Referer (XHR sends none) and an ABSENT Origin (XHR sends
	// one). Exact, like TestBrowserHeaderPairsMatchesCapture: a wrong
	// value is as identifying as a missing header, and an extra header
	// is a stable slk signature.
	want := map[string]string{
		"User-Agent":         UserAgent(),
		"Accept":             "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
		"Accept-Language":    "en-US,en;q=0.9",
		"Referer":            "https://app.slack.com/",
		"Sec-Fetch-Site":     "same-site",
		"Sec-Fetch-Mode":     "no-cors",
		"Sec-Fetch-Dest":     "image",
		"Sec-Ch-Ua":          ClientHintUA(),
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": ClientHintPlatform(),
		"Cache-Control":      "no-cache",
		"Pragma":             "no-cache",
		"Priority":           "i",
	}
	got := imageHeaderPairs()

	for k, v := range want {
		if got[k] != v {
			t.Errorf("imageHeaderPairs()[%q] = %q; want %q", k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("imageHeaderPairs() has unexpected header %q = %q; "+
				"real Chrome does not send it on an image load, so it is an slk-specific signature",
				k, got[k])
		}
	}
	if len(got) != len(want) {
		t.Errorf("imageHeaderPairs() has %d headers; want exactly %d", len(got), len(want))
	}
	// Origin is the one header the XHR set has and this one must not:
	// Chrome sends no Origin on a no-cors image fetch.
	if v, ok := got["Origin"]; ok {
		t.Errorf("imageHeaderPairs()[Origin] = %q; want absent (no-cors fetches carry no Origin)", v)
	}
}

// doDestReq issues one GET through a BrowserTransport with the given
// Dest and returns the request as the inner transport saw it.
func doDestReq(t *testing.T, dest Dest, env *Envelope, host, path string) *http.Request {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	recorder := &captureRT{wrapped: http.DefaultTransport}
	client := &http.Client{Transport: &BrowserTransport{Inner: recorder, Env: env, Dest: dest}}

	req, err := http.NewRequest("GET", srv.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	return recorder.last
}

func TestBrowserTransport_ImageDestSendsImageHeaders(t *testing.T) {
	// Avatars and file thumbnails are the highest-volume path — 337
	// CDN requests against 53 API calls on a single boot — so sending
	// them the XHR set is the largest single divergence available.
	got := doDestReq(t, DestImage, nil, "files.slack.com", "/files-tmb/T04-F0A-abc/image_480.png")

	for k, want := range map[string]string{
		"Accept":         "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
		"Sec-Fetch-Dest": "image",
		"Sec-Fetch-Mode": "no-cors",
		"Sec-Fetch-Site": "same-site",
		"Priority":       "i",
		"Referer":        "https://app.slack.com/",
	} {
		if v := got.Header.Get(k); v != want {
			t.Errorf("image request %s = %q; want %q", k, v, want)
		}
	}
	// Chrome sends no Origin on a no-cors image fetch.
	if v := got.Header.Get("Origin"); v != "" {
		t.Errorf("image request Origin = %q; want absent", v)
	}
}

func TestBrowserTransport_DefaultDestIsUnchangedXHR(t *testing.T) {
	// The zero value must stay XHR: every existing construction site
	// omits Dest, and a silent change of their behaviour is exactly
	// the class of regression this fix is repairing.
	got := doDestReq(t, DestXHR, nil, "rands-leadership.slack.com", "/api/conversations.history")

	for k, want := range map[string]string{
		"Accept":         "*/*",
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "cors",
		"Priority":       "u=1, i",
		"Origin":         "https://app.slack.com",
	} {
		if v := got.Header.Get(k); v != want {
			t.Errorf("XHR request %s = %q; want %q", k, v, want)
		}
	}
	if v := got.Header.Get("Referer"); v != "" {
		t.Errorf("XHR request Referer = %q; want absent (0 of 279 captured API requests carry one)", v)
	}
	// DestXHR is the zero value, so an unset Dest must behave
	// identically.
	var zero Dest
	if zero != DestXHR {
		t.Errorf("zero Dest = %v; want DestXHR so existing call sites are unaffected", zero)
	}
}

func TestBrowserTransport_ImageDestCarriesNoEnvelope(t *testing.T) {
	// Env: nil is the convention on the image path, but conventions
	// are not enforcement. An image fetch must carry no _x_* params
	// even if an Envelope is wired up by mistake — and the /api/ path
	// here defeats the path-based scoping, so only the Dest check can
	// stop it.
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	got := doDestReq(t, DestImage, env, "rands-leadership.slack.com", "/api/conversations.history")

	if raw := got.URL.RawQuery; raw != "" {
		t.Errorf("image-dest request got envelope params: %q", raw)
	}
}

func TestNewImageHTTPClientUsesImageDest(t *testing.T) {
	c := NewImageHTTPClient(nil)
	bt, ok := c.Transport.(*BrowserTransport)
	if !ok {
		t.Fatalf("NewImageHTTPClient transport is %T; want *BrowserTransport", c.Transport)
	}
	if bt.Dest != DestImage {
		t.Errorf("NewImageHTTPClient Dest = %v; want DestImage", bt.Dest)
	}
	if bt.Env != nil {
		t.Errorf("NewImageHTTPClient Env = %v; want nil (asset fetches carry no envelope)", bt.Env)
	}
}

func newEnvelopeClient(t *testing.T, env *Envelope) (*http.Client, *captureRT) {
	t.Helper()
	recorder := &captureRT{wrapped: http.DefaultTransport}
	bt := &BrowserTransport{Inner: recorder, Env: env}
	return &http.Client{Transport: bt}, recorder
}

// doEnvelopeReq issues one request through BrowserTransport with the
// given Host and path, returning the decorated URL query.
func doEnvelopeReq(t *testing.T, env *Envelope, host, path string) url.Values {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)

	req, err := http.NewRequest("POST", srv.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	return recorder.last.URL.Query()
}

func TestEnvelopeQuery_WorkspaceAPIPreBoot(t *testing.T) {
	q := doEnvelopeReq(t, NewEnvelope(), "rands-leadership.slack.com", "/api/experiments.getByUser")

	for k, want := range map[string]string{
		"_x_frontend_build_type": "current",
		"_x_desktop_ia":          "4",
		"_x_gantry":              "true",
		"fp":                     "6e",
		"_x_num_retries":         "0",
		"_x_version_ts":          DefaultVersionTS,
		"_x_foreground":          "true",
	} {
		if q.Get(k) != want {
			t.Errorf("query %s = %q; want %q", k, q.Get(k), want)
		}
	}
	if !strings.HasPrefix(q.Get("_x_id"), "noversion-") {
		t.Errorf("_x_id = %q; want noversion- prefix pre-boot", q.Get("_x_id"))
	}
	// Pre-boot: absent. Verified 0/14 pre-boot requests carry these.
	for _, k := range []string{"slack_route", "_x_csid", "_x_b3_traceid", "_x_b3_spanid", "_x_b3_sampled"} {
		if q.Get(k) != "" {
			t.Errorf("query %s = %q; want absent pre-boot", k, q.Get(k))
		}
	}
	// _x_app_name is an edgeapi param; it must not appear on workspace API URLs.
	if q.Get("_x_app_name") != "" {
		t.Errorf("_x_app_name = %q; want absent on workspace API (edgeapi-only param)", q.Get("_x_app_name"))
	}
}

func TestEnvelopeQuery_WorkspaceAPIPostBoot(t *testing.T) {
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	q := doEnvelopeReq(t, env, "rands-leadership.slack.com", "/api/conversations.history")

	if q.Get("slack_route") != "T04T4TH8W" {
		t.Errorf("slack_route = %q; want T04T4TH8W", q.Get("slack_route"))
	}
	if q.Get("_x_csid") != env.SessionID() {
		t.Errorf("_x_csid = %q; want %q", q.Get("_x_csid"), env.SessionID())
	}
	if len(q.Get("_x_b3_traceid")) != 32 {
		t.Errorf("_x_b3_traceid = %q; want 32 hex chars", q.Get("_x_b3_traceid"))
	}
	if len(q.Get("_x_b3_spanid")) != 16 {
		t.Errorf("_x_b3_spanid = %q; want 16 hex chars", q.Get("_x_b3_spanid"))
	}
	if q.Get("_x_b3_sampled") != "1" {
		t.Errorf("_x_b3_sampled = %q; want 1", q.Get("_x_b3_sampled"))
	}
	if strings.HasPrefix(q.Get("_x_id"), "noversion-") {
		t.Errorf("_x_id = %q; want 8-hex prefix post-boot", q.Get("_x_id"))
	}
}

func TestEnvelopeQuery_EdgeAPIGetsDifferentParams(t *testing.T) {
	// edgeapi carries ONLY _x_app_name, fp, _x_num_retries (+ b3).
	// Verified: 116/116 edgeapi requests, none with _x_id, _x_version_ts,
	// slack_route, _x_csid, _x_frontend_build_type, _x_desktop_ia or
	// _x_gantry. See testdata/capture-evidence.json.
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	q := doEnvelopeReq(t, env, "edgeapi.slack.com", "/cache/T04T4TH8W/users/info")

	for k, want := range map[string]string{
		"_x_app_name":    "client",
		"fp":             "6e",
		"_x_num_retries": "0",
	} {
		if q.Get(k) != want {
			t.Errorf("edgeapi query %s = %q; want %q", k, q.Get(k), want)
		}
	}
	for _, k := range []string{
		"_x_id", "_x_version_ts", "slack_route", "_x_csid",
		"_x_frontend_build_type", "_x_desktop_ia", "_x_gantry", "_x_foreground",
	} {
		if q.Get(k) != "" {
			t.Errorf("edgeapi query %s = %q; want absent (workspace-API-only param)", k, q.Get(k))
		}
	}
}

func TestEnvelopeQuery_PreservesCallerParams(t *testing.T) {
	q := doEnvelopeReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/conversations.history?channel=C123&limit=28")
	if q.Get("channel") != "C123" || q.Get("limit") != "28" {
		t.Errorf("caller params lost: channel=%q limit=%q", q.Get("channel"), q.Get("limit"))
	}
	if q.Get("fp") != "6e" {
		t.Error("envelope params not added alongside caller params")
	}
}

func TestEnvelopeQuery_DoesNotOverwriteCallerEnvelopeParams(t *testing.T) {
	// A caller that already set an envelope param owns it. The live case
	// is a retry: it sets _x_num_retries=1, and the transport must not
	// report the retried request to Slack as a first attempt.
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	q := doEnvelopeReq(t, env, "rands-leadership.slack.com",
		"/api/conversations.history?_x_num_retries=2&_x_id=caller-id&fp=zz"+
			"&slack_route=T_CALLER&_x_version_ts=999&_x_foreground=false")

	for k, want := range map[string]string{
		"_x_num_retries": "2",
		"_x_id":          "caller-id",
		"fp":             "zz",
		"slack_route":    "T_CALLER",
		"_x_version_ts":  "999",
		"_x_foreground":  "false",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("caller-set %s = %q; want %q (transport must not overwrite)", k, got, want)
		}
	}
	// Each key must appear exactly once: appending rather than
	// overwriting would send a duplicate query key, which is just as
	// separable as the wrong value.
	for k, v := range q {
		if len(v) != 1 {
			t.Errorf("query %s has %d values %v; want exactly 1", k, len(v), v)
		}
	}

	// Same rule on edgeapi's one host-specific param.
	eq := doEnvelopeReq(t, env, "edgeapi.slack.com",
		"/cache/T04T4TH8W/users/info?_x_app_name=caller-app&_x_num_retries=3")
	if eq.Get("_x_app_name") != "caller-app" {
		t.Errorf("caller-set _x_app_name = %q; want caller-app", eq.Get("_x_app_name"))
	}
	if eq.Get("_x_num_retries") != "3" {
		t.Errorf("caller-set _x_num_retries = %q; want 3", eq.Get("_x_num_retries"))
	}
}

func TestEnvelopeQuery_OmitsEmptyValues(t *testing.T) {
	// A zero-value Envelope has no version timestamp. Emitting
	// "_x_version_ts=" is worse than emitting nothing: no real client
	// ever sends the key empty.
	q := doEnvelopeReq(t, &Envelope{}, "rands-leadership.slack.com", "/api/conversations.history")
	for k, v := range q {
		if v[0] == "" {
			t.Errorf("query %s emitted with an empty value; want the key omitted", k)
		}
	}
}

func TestEnvelopeQuery_ParamOrderMatchesCapture(t *testing.T) {
	// 0 of 163 workspace-API requests in the captures had alphabetically
	// sorted params. The client emits one canonical order with fp and
	// _x_num_retries always last. url.Values.Encode() would sort them,
	// which is why this package assembles the query by hand.
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)
	req, _ := http.NewRequest("POST", srv.URL+"/api/conversations.history", nil)
	req.Host = "rands-leadership.slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	var keys []string
	for _, kv := range strings.Split(recorder.last.URL.RawQuery, "&") {
		keys = append(keys, strings.SplitN(kv, "=", 2)[0])
	}
	want := []string{
		"_x_id", "_x_csid", "slack_route", "_x_version_ts", "_x_foreground",
		"_x_frontend_build_type", "_x_desktop_ia", "_x_gantry",
		"_x_b3_traceid", "_x_b3_spanid", "_x_b3_sampled",
		"fp", "_x_num_retries",
	}
	if len(keys) != len(want) {
		t.Fatalf("query keys = %v; want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("query key[%d] = %q; want %q (full order: %v)", i, keys[i], want[i], keys)
		}
	}

	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	same := true
	for i := range keys {
		if keys[i] != sorted[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("query params are alphabetically sorted; the real client never emits that order")
	}
}

func TestEnvelopeQuery_EdgeAPIParamOrder(t *testing.T) {
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)
	req, _ := http.NewRequest("POST", srv.URL+"/cache/T04T4TH8W/users/info", nil)
	req.Host = "edgeapi.slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	var keys []string
	for _, kv := range strings.Split(recorder.last.URL.RawQuery, "&") {
		keys = append(keys, strings.SplitN(kv, "=", 2)[0])
	}
	want := []string{"_x_app_name", "_x_b3_traceid", "_x_b3_spanid", "_x_b3_sampled", "fp", "_x_num_retries"}
	if len(keys) != len(want) {
		t.Fatalf("edgeapi query keys = %v; want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("edgeapi key[%d] = %q; want %q", i, keys[i], want[i])
		}
	}
}

func TestEnvelopeQuery_CallerParamsKeepTheirOrder(t *testing.T) {
	env := NewEnvelope()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)
	// Deliberately not alphabetical.
	req, _ := http.NewRequest("POST", srv.URL+"/api/conversations.history?limit=28&channel=C123", nil)
	req.Host = "rands-leadership.slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	raw := recorder.last.URL.RawQuery
	if !strings.HasPrefix(raw, "limit=28&channel=C123&") {
		t.Errorf("caller params were reordered or re-encoded: %q", raw)
	}
	if !strings.HasSuffix(raw, "&fp=6e&_x_num_retries=0") {
		t.Errorf("envelope tail wrong: %q", raw)
	}
}

func TestEnvelopeQuery_NotAppliedToNonAPIPaths(t *testing.T) {
	// files.slack.com downloads must not carry _x_id/slack_route even if
	// an Envelope is attached to the client.
	env := NewEnvelope()
	env.SetTeamID("T04T4TH8W")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)
	req, _ := http.NewRequest("GET", srv.URL+"/files-tmb/T04-F0A-abc/image_480.png", nil)
	req.Host = "files.slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if raw := recorder.last.URL.RawQuery; raw != "" {
		t.Errorf("non-API Slack path got envelope params: %q", raw)
	}
	// Headers should still apply.
	if recorder.last.Header.Get("Sec-Ch-Ua") == "" {
		t.Error("browser headers missing on files.slack.com request")
	}
}

func TestEnvelopeQuery_NotAddedToNonSlackHosts(t *testing.T) {
	q := doEnvelopeReq(t, NewEnvelope(), "example.com", "/whatever")
	if q.Get("fp") != "" {
		t.Error("envelope params leaked to a non-Slack host")
	}
}

func TestEnvelopeQuery_NilEnvelopeIsSafe(t *testing.T) {
	// Asset fetches use a BrowserTransport with no Envelope.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	recorder := &captureRT{wrapped: http.DefaultTransport}
	client := &http.Client{Transport: &BrowserTransport{Inner: recorder, Env: nil}}

	req, _ := http.NewRequest("GET", srv.URL+"/api/x", nil)
	req.Host = "slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do with nil Env: %v", err)
	}
	resp.Body.Close()
	if recorder.last.URL.Query().Get("fp") != "" {
		t.Error("params added despite nil Envelope")
	}
	// Headers must still be applied.
	if recorder.last.Header.Get("Sec-Ch-Ua") == "" {
		t.Error("nil Envelope suppressed browser headers")
	}
}

// doBodyReq issues one POST through BrowserTransport and returns the
// decorated body as the inner transport saw it.
func doBodyReq(t *testing.T, env *Envelope, host, path, contentType, body, reason string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, env)

	req, err := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	if reason != "" {
		req = req.WithContext(WithReason(req.Context(), reason))
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	sent, err := io.ReadAll(recorder.last.Body)
	if err != nil {
		t.Fatalf("read captured body: %v", err)
	}
	return string(sent)
}

func TestEnvelopeBody_AddsFieldsInCaptureOrder(t *testing.T) {
	// Trailing order on 149/163 captured requests is exactly
	// _x_reason, _x_mode, _x_sonic, _x_app_name — business params
	// first. url.Values.Encode() would sort alphabetically, putting
	// _x_app_name first and token last, an order no client emits.
	got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/conversations.history", "application/x-www-form-urlencoded",
		"token=xoxc-abc&channel=C123", "message-pane/requestHistory")

	var keys []string
	for _, kv := range strings.Split(got, "&") {
		keys = append(keys, strings.SplitN(kv, "=", 2)[0])
	}
	want := []string{"token", "channel", "_x_reason", "_x_mode", "_x_sonic", "_x_app_name"}
	if len(keys) != len(want) {
		t.Fatalf("body keys = %v; want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("body key[%d] = %q; want %q (full: %v)", i, keys[i], want[i], keys)
		}
	}

	vals, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", got, err)
	}
	for k, want := range map[string]string{
		"token":       "xoxc-abc",
		"channel":     "C123",
		"_x_reason":   "message-pane/requestHistory",
		"_x_mode":     "online",
		"_x_sonic":    "true",
		"_x_app_name": "client",
	} {
		if vals.Get(k) != want {
			t.Errorf("body field %s = %q; want %q", k, vals.Get(k), want)
		}
	}
}

func TestEnvelopeBody_SetsContentLengthAndGetBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, recorder := newEnvelopeClient(t, NewEnvelope())

	req, _ := http.NewRequest("POST", srv.URL+"/api/x",
		strings.NewReader("token=xoxc-abc"))
	req.Host = "rands-leadership.slack.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	sent, _ := io.ReadAll(recorder.last.Body)
	if recorder.last.ContentLength != int64(len(sent)) {
		t.Errorf("ContentLength = %d; want %d (body was rewritten)", recorder.last.ContentLength, len(sent))
	}
	// GetBody must be replayable for HTTP/2 retry and redirects.
	if recorder.last.GetBody == nil {
		t.Fatal("GetBody is nil after body rewrite; retries would send an empty body")
	}
	rc, err := recorder.last.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	replayed, _ := io.ReadAll(rc)
	if string(replayed) != string(sent) {
		t.Errorf("GetBody replay = %q; want %q", replayed, sent)
	}
}

func TestEnvelopeBody_DefaultsReasonPerEndpoint(t *testing.T) {
	// WithReason has one production call site, so nearly every request
	// arrives with no reason on its context. Without a default those
	// requests emit _x_mode/_x_sonic/_x_app_name and no _x_reason — a
	// shape the real client produces on 10 of 163 requests and slk
	// would produce on all of them. Each value below is the one the
	// official client tags that endpoint with.
	cases := map[string]string{
		"client.userBoot":            "initial-data",
		"client.shouldReload":        "boot",
		"client.counts":              "fetchClientCountsOnConnect",
		"conversations.history":      "message-pane/requestHistory",
		"conversations.mark":         "viewed",
		"conversations.genericInfo":  "fallback:fetchAndUpsertChannelsById",
		"users.prefs.get":            "fetch-frecency-prefs",
		"users.channelSections.list": "conditional-fetch-manager",
		"dnd.info":                   "fetchAndUpsertDndForUsers-getDndTimesFor:self",
	}
	for method, want := range cases {
		t.Run(method, func(t *testing.T) {
			got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
				"/api/"+method, "application/x-www-form-urlencoded", "token=xoxc-abc", "")
			vals, err := url.ParseQuery(got)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", got, err)
			}
			if v := vals.Get("_x_reason"); v != want {
				t.Errorf("_x_reason = %q; want %q (body %q)", v, want, got)
			}
			// The rest of the envelope tail is unchanged — except that
			// client.userBoot and client.shouldReload are two of the
			// seven boot-phase endpoints the official client sends no
			// _x_mode on (see mode.go), so their tail is this sequence
			// minus that one field. Spelled out here rather than asked
			// of sendsXMode: a test that derived it from the code under
			// test would follow a wrong exclusion set instead of failing
			// on it.
			wantTail := "_x_mode=online&_x_sonic=true&_x_app_name=client"
			if method == "client.userBoot" || method == "client.shouldReload" {
				wantTail = "_x_sonic=true&_x_app_name=client"
			}
			if !strings.HasSuffix(got, wantTail) {
				t.Errorf("body = %q; want tail %q", got, wantTail)
			}
		})
	}
}

func TestEnvelopeBody_UnmappedEndpointStillSendsReason(t *testing.T) {
	// An endpoint with no captured reason still gets one. Sending a
	// plausible-but-unverified value is better than sending none:
	// "has _x_mode, lacks _x_reason" is a single-predicate separator.
	got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/some.unmapped.method", "application/x-www-form-urlencoded", "token=xoxc-abc", "")
	vals, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", got, err)
	}
	if vals.Get("_x_reason") == "" {
		t.Errorf("body = %q; want a non-empty _x_reason on an unmapped endpoint", got)
	}
}

func TestEnvelopeBody_ExplicitReasonBeatsDefault(t *testing.T) {
	// conversations.history has a mapped default, but the caller knows
	// which UI action it is serving — a refresh around the unread
	// marker sends a different reason on the same endpoint.
	got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/conversations.history", "application/x-www-form-urlencoded",
		"token=xoxc-abc", "unread-counts/onLastReadUpdated")
	vals, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", got, err)
	}
	if v := vals["_x_reason"]; len(v) != 1 || v[0] != "unread-counts/onLastReadUpdated" {
		t.Errorf("_x_reason = %v; want exactly [unread-counts/onLastReadUpdated]", v)
	}
}

func TestEnvelopeBody_NeverSendsXModeWithoutXReason(t *testing.T) {
	// The exact predicate this default exists to eliminate. A
	// workspace-API body that carries _x_mode must carry _x_reason,
	// whatever the endpoint and whether or not the caller set one.
	paths := []string{
		"/api/conversations.history",
		"/api/client.counts",
		"/api/some.unmapped.method",
		"/api/chat.postMessage",
	}
	for _, path := range paths {
		for _, reason := range []string{"", "boot"} {
			name := path
			if reason != "" {
				name += "+reason"
			}
			t.Run(name, func(t *testing.T) {
				got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
					path, "application/x-www-form-urlencoded", "token=xoxc-abc", reason)
				if !strings.Contains(got, "_x_mode") {
					t.Fatalf("body = %q; expected the envelope tail on a workspace API body", got)
				}
				vals, err := url.ParseQuery(got)
				if err != nil {
					t.Fatalf("ParseQuery(%q): %v", got, err)
				}
				if vals.Get("_x_reason") == "" {
					t.Errorf("body = %q carries _x_mode without _x_reason; that pair is a "+
						"single-predicate separator matching ~6%% of official traffic", got)
				}
			})
		}
	}
}

func TestEnvelopeBody_LeavesMultipartAlone(t *testing.T) {
	// Two bodies, because the realistic one is not enough on its own:
	// its Content-Disposition carries a ';', and Go's url.ParseQuery
	// rejects ';' as a separator, so that body survives via the
	// unparseable-form pass-through even with the content-type guard
	// deleted. The semicolon-free body parses cleanly as a one-key
	// form, so only the content-type guard keeps it intact.
	bodies := map[string]string{
		"realistic":      "--BOUNDARY\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\nDATA\r\n--BOUNDARY--\r\n",
		"form-parseable": "--BOUNDARY\r\nContent-Disposition: form-data\r\n\r\nDATA\r\n--BOUNDARY--\r\n",
	}
	for name, raw := range bodies {
		t.Run(name, func(t *testing.T) {
			got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
				"/api/files.upload", "multipart/form-data; boundary=BOUNDARY", raw, "boot")
			if got != raw {
				t.Errorf("multipart body was rewritten:\ngot  %q\nwant %q", got, raw)
			}
		})
	}
}

func TestEnvelopeBody_LeavesJSONAlone(t *testing.T) {
	// A JSON object with no '&', ';' or '%' parses cleanly as a
	// single-key form, so url.ParseQuery will NOT reject it. Only the
	// content-type guard stops it being rewritten, and rewriting it
	// would corrupt the request.
	const raw = `{"token":"xoxc-abc","updated_ids":{"C123":1}}`

	// edgeapi posts JSON with content-type text/plain. This one is
	// doubly protected — the edgeapi host is excluded before
	// content-type is even read — but it is the shape slk actually
	// sends, so pin it.
	got := doBodyReq(t, NewEnvelope(), "edgeapi.slack.com",
		"/cache/T04/users/info", "text/plain;charset=UTF-8", raw, "boot")
	if got != raw {
		t.Errorf("edgeapi text/plain JSON body was rewritten:\ngot  %q\nwant %q", got, raw)
	}

	// A JSON POST to a workspace /api/ path has no host-based
	// protection: the content-type guard is the only thing between it
	// and a corrupted body.
	got = doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/chat.postMessage", "application/json; charset=utf-8", raw, "boot")
	if got != raw {
		t.Errorf("workspace-API JSON body was rewritten:\ngot  %q\nwant %q", got, raw)
	}
}

func TestEnvelopeBody_NoBodyIsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client, _ := newEnvelopeClient(t, NewEnvelope())

	req, _ := http.NewRequest("GET", srv.URL+"/api/auth.test", nil)
	req.Host = "rands-leadership.slack.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do with nil body: %v", err)
	}
	resp.Body.Close()
}

func TestEnvelopeBody_PreservesCallerSetFields(t *testing.T) {
	// A caller that already set _x_reason must win.
	got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
		"/api/x", "application/x-www-form-urlencoded",
		"token=xoxc-abc&_x_reason=caller-wins", "transport-would-set-this")
	vals, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if v := vals["_x_reason"]; len(v) != 1 || v[0] != "caller-wins" {
		t.Errorf("_x_reason = %v; want exactly [caller-wins]", v)
	}
}

func TestBrowserTransport_NilURLDoesNotPanic(t *testing.T) {
	// Only reachable by calling RoundTrip directly — http.Client.Do
	// rejects a nil URL first. Both applyEnvelopeQuery and
	// applyEnvelopeBody dereference req.URL.
	bt := &BrowserTransport{
		Inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
		}),
		Env: NewEnvelope(),
	}
	req := &http.Request{
		Method: "POST",
		Host:   "rands-leadership.slack.com",
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader("token=xoxc-abc")),
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := bt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip with nil URL: %v", err)
	}
	resp.Body.Close()
}

func TestEnvelopeBody_NotAppliedToNonAPIPaths(t *testing.T) {
	got := doBodyReq(t, NewEnvelope(), "files.slack.com",
		"/files-tmb/x.png", "application/x-www-form-urlencoded", "a=1", "boot")
	if got != "a=1" {
		t.Errorf("non-API path body was rewritten: %q", got)
	}
}

// The seven endpoints below are spelled out rather than read from
// xModeExcludedMethods on purpose. A test that iterated the map it is
// checking would pass on any set, including an empty one — it would
// track the implementation instead of the captures.
var xModeAbsentEndpoints = []string{
	"api.features",
	"client.getWebSocketURL",
	"client.shouldReload",
	"client.userBoot",
	"conversations.view",
	"experiments.getByUser",
	"features.access.policies.list",
}

func TestEnvelopeBody_OmitsXModeOnBootPhaseEndpoints(t *testing.T) {
	// _x_mode is not universal. Of the 163 captured form bodies, 14
	// carry none, split cleanly across these seven boot-phase
	// endpoints — zero endpoints are mixed. slk shipped the
	// unconditional form, so it sent _x_mode on client.shouldReload,
	// which Phase 1 calls on every startup, and would have done the
	// same for client.userBoot and conversations.view.
	for _, method := range xModeAbsentEndpoints {
		t.Run(method, func(t *testing.T) {
			got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
				"/api/"+method, "application/x-www-form-urlencoded", "token=xoxc-abc", "")

			vals, err := url.ParseQuery(got)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", got, err)
			}
			if v, ok := vals["_x_mode"]; ok {
				t.Errorf("body = %q has _x_mode=%v; the captures show %s sending none (n=2)",
					got, v, method)
			}
			// The other three keep their captured relative order when
			// _x_mode drops out — the tail shrinks, it does not
			// reshuffle, and _x_reason is still mandatory.
			if !strings.HasSuffix(got, "_x_sonic=true&_x_app_name=client") {
				t.Errorf("body = %q; want a _x_sonic/_x_app_name tail", got)
			}
			if vals.Get("_x_reason") == "" {
				t.Errorf("body = %q has no _x_reason; dropping _x_mode must not drop the reason", got)
			}
			keys := queryKeyOrder(got)
			want := []string{"token", "_x_reason", "_x_sonic", "_x_app_name"}
			if len(keys) != len(want) {
				t.Fatalf("body keys = %v; want exactly %v", keys, want)
			}
			for i := range want {
				if keys[i] != want[i] {
					t.Errorf("body key[%d] = %q; want %q (full: %v)", i, keys[i], want[i], keys)
				}
			}
		})
	}
}

func TestEnvelopeBody_KeepsXModeOnEveryOtherEndpoint(t *testing.T) {
	// The exclusion is by EXACT method name. A prefix or namespace
	// match — the realistic way to get this wrong — would strip
	// _x_mode from these, and the captures show every one of them
	// sending it on every observation.
	cases := []struct{ method, why string }{
		{"client.counts", "shares the client. namespace with 3 excluded methods; sends _x_mode 6/6"},
		{"conversations.history", "shares conversations. with conversations.view; 14/14"},
		{"conversations.listPrefs", "shares conversations. with conversations.view; 7/7"},
		{"conversations.bulkReacjiTriggers", "shares conversations. with conversations.view; 7/7"},
		{"dnd.teamInfo", "highest-volume form body in the captures; 17/17"},
		{"bookmarks.list", "8/8"},
		{"users.prefs.get", "4/4"},
		{"api.test", "shares the api. namespace with api.features"},
		{"features.access.policies.listMore", "has an excluded method as a strict prefix"},
		{"conversations.viewers", "has conversations.view as a strict prefix"},
		{"client.userBootstrap", "has client.userBoot as a strict prefix"},
		{"some.unmapped.method", "no capture entry at all; unknown endpoints join the 149/163 majority"},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := doBodyReq(t, NewEnvelope(), "rands-leadership.slack.com",
				"/api/"+tc.method, "application/x-www-form-urlencoded", "token=xoxc-abc", "")

			vals, err := url.ParseQuery(got)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", got, err)
			}
			if v := vals.Get("_x_mode"); v != "online" {
				t.Errorf("body = %q has _x_mode=%q; want \"online\" (%s)", got, v, tc.why)
			}
			// And in the captured position: after _x_reason, before
			// _x_sonic and _x_app_name.
			if !strings.HasSuffix(got, "_x_mode=online&_x_sonic=true&_x_app_name=client") {
				t.Errorf("body = %q; want the _x_mode/_x_sonic/_x_app_name tail", got)
			}
			keys := queryKeyOrder(got)
			want := []string{"token", "_x_reason", "_x_mode", "_x_sonic", "_x_app_name"}
			if len(keys) != len(want) {
				t.Fatalf("body keys = %v; want exactly %v", keys, want)
			}
			for i := range want {
				if keys[i] != want[i] {
					t.Errorf("body key[%d] = %q; want %q (full: %v)", i, keys[i], want[i], keys)
				}
			}
		})
	}
}

func TestEnvelopeBody_XModeExclusionSetMatchesCaptures(t *testing.T) {
	// Guards the SIZE of the set from both directions. Inverting the
	// rule, or widening it to a prefix, changes how many endpoints are
	// excluded, and the two tables above would still each pass if the
	// other's endpoints leaked in — this one pins the boundary itself.
	if len(xModeExcludedMethods) != len(xModeAbsentEndpoints) {
		t.Errorf("xModeExcludedMethods has %d entries; the captures name exactly %d "+
			"endpoints that never send _x_mode: %v",
			len(xModeExcludedMethods), len(xModeAbsentEndpoints), xModeAbsentEndpoints)
	}
	for _, m := range xModeAbsentEndpoints {
		if _, ok := xModeExcludedMethods[m]; !ok {
			t.Errorf("xModeExcludedMethods is missing %q, which sends no _x_mode in 2 of 2 captures", m)
		}
	}
}
