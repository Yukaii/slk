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

func TestReasonDoesNotCollideWithOtherKeys(t *testing.T) {
	// The context key must be an unexported struct type, not a string,
	// so an unrelated package storing ctx.Value("reason") cannot be
	// mistaken for ours.
	ctx := context.WithValue(context.Background(), "reason", "not-ours") //nolint:staticcheck // deliberate
	if got := ReasonFrom(ctx); got != "" {
		t.Errorf("ReasonFrom picked up a foreign string key: %q", got)
	}
}
