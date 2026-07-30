package slackhttp

import (
	"context"
	"strings"
)

// reasonKey is the unexported context key for the _x_reason value. It
// is a struct type rather than a string so no other package's context
// value can collide with it.
type reasonKey struct{}

// WithReason returns a context carrying the _x_reason value for the
// request(s) made with it. Slack's web client tags every API call with
// the UI action that triggered it, e.g. "message-pane/requestHistory",
// "unread-counts/onLastReadUpdated", "initial-data", "boot". Requests
// with no reason at all are one more way slk's traffic differs from
// the official client's.
//
// _x_reason rides the context rather than a transport field because it
// is caller intent: only the call site knows which UI action it is
// serving, and that knowledge would otherwise have to be threaded
// through every API signature.
//
// Note for the code that consumes this: _x_reason is a *body* field,
// not a query param — unlike _x_id, _x_csid, and slack_route, which are
// query params. Across the 2026-07-30 captures it appears 153 times as
// a form field and zero times in a query string, with 48 distinct
// values; the four examples above occur 11, 3, 2 and 6 times
// respectively. See
// docs/superpowers/specs/2026-07-30-enterprise-grid-bootstrap-design.md.
func WithReason(ctx context.Context, reason string) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, reasonKey{}, reason)
}

// ReasonFrom returns the _x_reason carried by ctx, or "" if none.
// Tolerates a nil ctx.
func ReasonFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(reasonKey{}).(string)
	return s
}

// defaultReasons maps an API method name to the _x_reason the official
// web client sends for it, for the endpoints slk actually calls. Each
// value was read off the 2026-07-30 captures — these are observed, not
// invented.
//
// This table exists because WithReason is caller intent and almost no
// caller supplies it: for a long time exactly one production call site
// did. Every other request therefore emitted the trailing envelope
// _x_mode/_x_sonic/_x_app_name with no _x_reason at all. The real
// client sends _x_reason on 153 of 163 requests, so "body has _x_mode
// and lacks _x_reason" was a single predicate matching ~6% of official
// traffic and ~100% of slk's — a sharper separator than sending no
// _x_* fields at all, which is what slk did before this package
// existed.
//
// conversations.history also appears in the captures as
// "unread-counts/onLastReadUpdated" when the client refreshes around
// the unread marker. That variant is caller-specific, so it stays a
// WithReason override rather than a second table entry: an explicit
// reason always beats the default.
var defaultReasons = map[string]string{
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

// genericReason is what an endpoint with no entry in defaultReasons
// gets.
//
// HONEST CAVEAT: this pairing is a GUESS. The string itself is real —
// the captures show the official client sending
// "conditional-fetch-manager" on users.channelSections.list, so it is
// not an slk-invented value that would stand out on its own — but
// there is no capture evidence that the real client ever sends it on,
// say, chat.postMessage. Treat it as unverified.
//
// It is still the right call, because the alternative is emitting
// nothing, and nothing is the separator this whole table exists to
// remove. A wrong-but-attested reason puts slk inside the 94% of
// requests that carry the field; an absent one puts it in a 6% bucket
// on every single request. If a future capture covers more endpoints,
// move them into defaultReasons and shrink this fallback's reach.
const genericReason = "conditional-fetch-manager"

// defaultReason returns the _x_reason to send for an API method when
// the caller supplied none. It never returns "".
func defaultReason(method string) string {
	if r, ok := defaultReasons[method]; ok {
		return r
	}
	return genericReason
}

// methodFromPath extracts the Slack API method name from a request
// path — the segment after /api/. Returns "" for a non-API path, which
// defaultReason then answers with the generic fallback.
func methodFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/")
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
