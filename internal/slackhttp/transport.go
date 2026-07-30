// Package slackhttp owns the two distinct header sets a recent desktop
// Chrome sends to Slack, and the http.RoundTripper that applies one of them.
//
//   - BrowserTransport decorates outbound HTTP requests to *.slack.com with
//     the fetch/XHR set (browserHeaderPairs).
//   - WebSocketHeaders returns the strictly smaller set Chrome sends on a
//     WebSocket upgrade, for the gorilla/websocket dialer, which cannot go
//     through an http.RoundTripper.
//
// The two sets are deliberately different — real Chrome omits Accept,
// Sec-Fetch-*, sec-ch-ua*, and Priority on a WS handshake — so they must not
// be merged. The goal is to make xoxc-token traffic indistinguishable from
// official browser-client traffic at the header level, so Enterprise Grid
// anomaly detectors don't flag slk as a non-browser client and sign the user
// out.
//
// See: docs/superpowers/plans/2026-05-20-browser-like-headers.md and GitHub
// issue #5 for context.
package slackhttp

import (
	"fmt"
	"net/http"
	"net/url"
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

	// Env supplies the Slack client telemetry envelope (_x_id, _x_csid,
	// slack_route, ...). If nil, no envelope params are added — asset
	// fetches to CDN hosts carry no envelope. Even when non-nil, the
	// envelope is scoped to edgeapi and to /api/ paths, so a stray Env
	// on a files.slack.com client cannot decorate download URLs.
	Env *Envelope
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
		for k, v := range browserHeaderPairs() {
			setIfMissing(req.Header, k, v)
		}
		if t.Env != nil {
			applyEnvelopeQuery(req, t.Env)
		}
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

// WebSocketHeaders returns the headers Chrome sends on a WebSocket
// upgrade to Slack. This is deliberately a SMALLER set than the HTTP
// set in browserHeaderPairs: Chrome omits Accept, all Sec-Fetch-*
// headers, all sec-ch-ua* client hints, and Priority on a WS handshake.
//
// Verified against the status-101 upgrade requests in the 2026-07-30
// captures. See docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md.
//
// gorilla/websocket's Dialer owns Connection, Upgrade, and the
// Sec-Websocket-* set — it rejects a caller-supplied duplicate of any of
// them — so those are absent here by design. Host is NOT in that list:
// gorilla explicitly honors a caller-supplied Host, so omitting it here
// is a choice, not a constraint.
func WebSocketHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", UserAgent())
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	h.Set("Origin", "https://app.slack.com")
	return h
}

// browserHeaderPairs is the single source of truth for the headers a
// Chrome tab sends on a same-site XHR to Slack. RoundTrip is its only
// consumer.
//
// The WebSocket upgrade deliberately does NOT consume this — see
// WebSocketHeaders. Chrome's WS handshake omits Accept, Sec-Fetch-*,
// sec-ch-ua*, and Priority, so sharing this set with the WS path would
// make the socket separable rather than consistent.
//
// Deliberately contains NO Referer: the official web client sends none
// on /api/ calls, and slk sending one made it separable. Verified
// across all 8 of the 2026-07-30 HAR captures: 279 requests to
// *.slack.com/api/* and edgeapi.slack.com, zero with a Referer.
//
// Caveat for anyone re-deriving that number: Chrome DevTools records an
// EMPTY `referer:` key on requests it aborted (status 0, e.g. during the
// deliberate network-outage capture). Those are not Referers. The only
// two non-empty Referers anywhere in the captures are a webfont pointing
// at its CSS and an image on slack-imgs.com — static subresources, not
// API calls. See
// docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md.
func browserHeaderPairs() map[string]string {
	return map[string]string{
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
}

// chromeMajor is the Chrome major version slk impersonates. Both the
// User-Agent string and the sec-ch-ua client hints interpolate it, so
// their *version numbers* cannot drift apart — a Chrome UA paired with
// absent or mismatched client hints is a combination real Chrome never
// emits, and is trivially detectable.
//
// Only the version number is derived from this constant. The rest of
// the sec-ch-ua value — the GREASE brand token and the ordering of the
// three brand entries — is hardcoded in ClientHintUA, and Chrome
// permutes both between major versions. Chrome 147 sent
// `"Google Chrome";v="147", "Not.A/Brand";v="8", "Chromium";v="147"`;
// Chrome 150 sends `"Not;A=Brand";v="8", "Chromium";v="150",
// "Google Chrome";v="150"` — a different token and a different order.
//
// So do NOT bump this constant on its own. Doing so yields a correct
// UA paired with a sec-ch-ua no real Chrome emits, which is a stable,
// slk-specific fingerprint: worse than sending nothing. A bump
// requires a fresh capture of the real client, with ClientHintUA
// updated to match. See the "Verified impersonation values" section of
// docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md.
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

// ClientHintUA returns the sec-ch-ua header value paired with
// UserAgent(). The brand list — the GREASE token, the three entries and
// their order — reproduces what Chrome 150 was observed sending in
// captures of the Slack web client taken 2026-07-30; only the version
// number comes from chromeMajor. Because Chrome varies the GREASE token
// and the ordering per major version, this string is correct for the
// captured version only: see the chromeMajor doc comment before
// changing either.
func ClientHintUA() string {
	return fmt.Sprintf(`"Not;A=Brand";v="8", "Chromium";v="%s", "Google Chrome";v="%s"`,
		chromeMajor, chromeMajor)
}

// ClientHintPlatform returns the sec-ch-ua-platform value for the host OS.
func ClientHintPlatform() string {
	return clientHintPlatformForGOOS(runtime.GOOS)
}

// clientHintPlatformForGOOS is split out so every branch is testable on
// any host, matching the userAgentForGOOS pattern. The quotes are part
// of the header value: sec-ch-ua-platform is a structured-header
// string, so Chrome sends `"Linux"`, not bare Linux.
func clientHintPlatformForGOOS(goos string) string {
	switch goos {
	case "darwin":
		return `"macOS"`
	case "windows":
		return `"Windows"`
	default:
		// Linux and anything else (freebsd, openbsd, ...) → Linux.
		return `"Linux"`
	}
}

// isEdgeAPIHost reports whether host is Slack's edge cache API, which
// takes a different (much smaller) envelope than the workspace API.
func isEdgeAPIHost(host string) bool {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host == "edgeapi.slack.com"
}

// envelopeHost returns the host req is logically addressed to.
//
// req.Host takes precedence over req.URL.Host because that is what the
// server sees: net/http sends req.Host as the Host header whenever it
// is non-empty, regardless of the address actually dialed. RoundTrip's
// Slack-host gate accepts a match on either field, so classifying the
// envelope on URL.Host alone would send the workspace param set to a
// request whose Host header says edgeapi.
func envelopeHost(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	if req.URL != nil {
		return req.URL.Host
	}
	return ""
}

// isWorkspaceAPIPath reports whether path is a workspace API call, the
// only place the workspace envelope belongs.
//
// Without this scope check, any Slack host that isn't edgeapi gets the
// workspace set — so an Envelope accidentally attached to the client
// used for files.slack.com downloads would decorate every asset URL
// with _x_id and slack_route. No real client does that, and "asset
// clients pass Env: nil" is a convention nothing enforced.
func isWorkspaceAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

// envelopeParam is one query param in the order the official client
// emits it.
type envelopeParam struct{ key, value string }

// applyEnvelopeQuery appends Slack's client telemetry params to req's
// URL in the ORDER the official client emits them, never overwriting a
// param the caller already set.
//
// Order matters. Across the 2026-07-30 captures, 0 of 163 workspace-API
// requests carried alphabetically-sorted params; the client emits one
// canonical sequence with optional members omitted in place, and fp /
// _x_num_retries always last. url.Values.Encode() sorts keys, so using
// it would give every slk request a perfectly alphabetized query
// string — a stable distributional signature, which is exactly what
// this package exists to remove.
//
// Host classes take different sets — measured, not assumed (see
// testdata/capture-evidence.json):
//
//	workspace API (*.slack.com/api/*), 163 requests:
//	  _x_id, _x_csid, slack_route, _x_version_ts, _x_foreground,
//	  _x_frontend_build_type, _x_desktop_ia, _x_gantry,
//	  _x_b3_traceid, _x_b3_spanid, _x_b3_sampled, fp, _x_num_retries
//	  (_x_csid/slack_route/_x_b3_* are post-boot only)
//
//	edgeapi.slack.com, 116 requests:
//	  _x_app_name, _x_b3_traceid, _x_b3_spanid, _x_b3_sampled,
//	  fp, _x_num_retries
//	  never: _x_id, _x_version_ts, slack_route, _x_csid, or any
//	         _x_frontend_build_type/_x_desktop_ia/_x_gantry
//
// Sending the workspace set to edgeapi would be an slk-specific
// signature, as would sending it to a non-API path.
func applyEnvelopeQuery(req *http.Request, env *Envelope) {
	existing := req.URL.Query()
	var out []envelopeParam

	add := func(key, value string) {
		if value == "" || existing.Get(key) != "" {
			return
		}
		out = append(out, envelopeParam{key, value})
	}

	postBoot := env.TeamID() != ""

	switch {
	case isEdgeAPIHost(envelopeHost(req)):
		add("_x_app_name", "client")
	case isWorkspaceAPIPath(req.URL.Path):
		add("_x_id", env.RequestID())
		if postBoot {
			add("_x_csid", env.SessionID())
			add("slack_route", env.TeamID())
		}
		add("_x_version_ts", env.VersionTS())
		// The real client varies _x_foreground with browser tab focus
		// (145/163 carry true). A TUI has no equivalent notion, and
		// omitting a param present on 88% of traffic is the larger
		// divergence, so always send true.
		add("_x_foreground", "true")
		add("_x_frontend_build_type", "current")
		add("_x_desktop_ia", "4")
		add("_x_gantry", "true")
	default:
		// A Slack host that is neither edgeapi nor an API path —
		// files.slack.com and the CDNs. Real clients send no envelope
		// there, not even fp, so send nothing at all.
		return
	}

	// B3 trace ids appear on only 14-18% of real requests, but they are
	// per-request random values rather than constants, so over-sending
	// is much less identifying than emitting a wrong fixed value.
	if postBoot {
		trace, span := env.TraceIDs()
		add("_x_b3_traceid", trace)
		add("_x_b3_spanid", span)
		add("_x_b3_sampled", "1")
	}

	// Always last, on both host classes.
	add("fp", "6e")
	add("_x_num_retries", "0")

	req.URL.RawQuery = appendQuery(req.URL.RawQuery, out)
}

// appendQuery appends params to an existing raw query string without
// reordering or re-encoding what is already there.
func appendQuery(raw string, params []envelopeParam) string {
	if len(params) == 0 {
		return raw
	}
	var b strings.Builder
	b.WriteString(raw)
	for _, p := range params {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.value))
	}
	return b.String()
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
