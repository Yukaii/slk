package slackhttp

import (
	"context"
	"testing"
)

func TestReasonRoundTrip(t *testing.T) {
	ctx := WithReason(context.Background(), "message-pane/requestHistory")
	if got := ReasonFrom(ctx); got != "message-pane/requestHistory" {
		t.Errorf("ReasonFrom = %q; want message-pane/requestHistory", got)
	}
}

func TestReasonDefaultsWhenAbsent(t *testing.T) {
	if got := ReasonFrom(context.Background()); got != "" {
		t.Errorf("ReasonFrom(empty ctx) = %q; want \"\"", got)
	}
}

func TestReasonIgnoresEmpty(t *testing.T) {
	ctx := WithReason(context.Background(), "")
	if got := ReasonFrom(ctx); got != "" {
		t.Errorf("ReasonFrom = %q; want \"\"", got)
	}
}

func TestReasonNilContext(t *testing.T) {
	// Defensive: some call sites in this codebase pass a nil ctx.
	//nolint:staticcheck // SA1012: passing nil is the behaviour under test.
	if got := ReasonFrom(nil); got != "" {
		t.Errorf("ReasonFrom(nil) = %q; want \"\"", got)
	}
}

func TestReasonEmptyDoesNotClobberOuter(t *testing.T) {
	// TestReasonIgnoresEmpty cannot tell "did not store" from "stored an
	// empty string" — both read back as "". This can: a caller that
	// derives a reason and comes up empty must leave an outer reason
	// intact rather than blanking it.
	ctx := WithReason(context.Background(), "message-pane/requestHistory")
	ctx = WithReason(ctx, "")
	if got := ReasonFrom(ctx); got != "message-pane/requestHistory" {
		t.Errorf("WithReason(ctx, \"\") clobbered the outer reason: %q", got)
	}
}

func TestReasonInnermostWins(t *testing.T) {
	ctx := WithReason(context.Background(), "outer")
	ctx = WithReason(ctx, "inner")
	if got := ReasonFrom(ctx); got != "inner" {
		t.Errorf("ReasonFrom = %q; want inner", got)
	}
}

func TestDefaultReasonMatchesCapture(t *testing.T) {
	// Each pair was read off the 2026-07-30 captures of the official
	// web client. These are the endpoints slk actually calls; the
	// values are what the real client tags them with.
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
			if got := defaultReason(method); got != want {
				t.Errorf("defaultReason(%q) = %q; want %q", method, got, want)
			}
		})
	}
}

func TestDefaultReasonForUnmappedMethodIsNonEmpty(t *testing.T) {
	// Emitting NOTHING is the separator this exists to remove: a body
	// carrying _x_mode but no _x_reason matches ~6% of the official
	// client's traffic and would match 100% of slk's. A plausible but
	// unverified value is strictly better than a structurally absent
	// field.
	for _, method := range []string{"chat.postMessage", "reactions.add", "", "some.unknown.method"} {
		if got := defaultReason(method); got == "" {
			t.Errorf("defaultReason(%q) = \"\"; want a non-empty fallback", method)
		}
	}
}

func TestMethodFromPath(t *testing.T) {
	cases := map[string]string{
		"/api/conversations.history": "conversations.history",
		"/api/client.counts":         "client.counts",
		"/api/":                      "",
		"/files-tmb/x.png":           "",
		"":                           "",
		// Slack API paths are /api/<method> with nothing after, but a
		// trailing segment must not become part of the method name.
		"/api/conversations.history/extra": "conversations.history",
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := methodFromPath(path); got != want {
				t.Errorf("methodFromPath(%q) = %q; want %q", path, got, want)
			}
		})
	}
}

func TestReasonDoesNotCollideWithOtherKeys(t *testing.T) {
	// The context key must be an unexported struct type, not a string,
	// so an unrelated package storing ctx.Value("reason") cannot be
	// mistaken for ours.
	ctx := context.WithValue(context.Background(), "reason", "not-ours") //nolint:staticcheck // deliberate
	if got := ReasonFrom(ctx); got != "" {
		t.Errorf("ReasonFrom picked up a foreign string key: %q", got)
	}
}
