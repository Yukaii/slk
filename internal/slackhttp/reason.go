package slackhttp

import "context"

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
