package messages

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Usergroup mentions arrive as <!subteam^SID|@label> (labeled) or
// <!subteam^SID> (bare). The renderer must never leak the raw wire
// token: labeled forms use the embedded label, bare forms resolve
// through the per-workspace usergroup map, and unresolvable bare forms
// fall back to a generic "@group".

func TestRenderUsergroupMentionLabeled(t *testing.T) {
	out := ansi.Strip(RenderSlackMarkdown("ping <!subteam^S123|@team> please", nil, nil))
	if !strings.Contains(out, "@team") {
		t.Errorf("expected @team in output, got %q", out)
	}
	if strings.Contains(out, "<!subteam") {
		t.Errorf("raw subteam token leaked: %q", out)
	}
}

func TestRenderUsergroupMentionBareResolved(t *testing.T) {
	out := ansi.Strip(RenderSlackMarkdownWith("dear <!subteam^S0TESTGRP01> hello", RenderSlackMarkdownOpts{
		UserGroups: map[string]string{"S0TESTGRP01": "platform-team"},
	}))
	if !strings.Contains(out, "@platform-team") {
		t.Errorf("expected @platform-team in output, got %q", out)
	}
	if strings.Contains(out, "<!subteam") {
		t.Errorf("raw subteam token leaked: %q", out)
	}
}

func TestRenderUsergroupMentionBareUnresolved(t *testing.T) {
	out := ansi.Strip(RenderSlackMarkdown("dear <!subteam^S0MISSING> hello", nil, nil))
	if !strings.Contains(out, "@group") {
		t.Errorf("expected @group fallback in output, got %q", out)
	}
	if strings.Contains(out, "<!subteam") {
		t.Errorf("raw subteam token leaked: %q", out)
	}
}

func TestRenderUsergroupMentionBareScopedPerCall(t *testing.T) {
	input := "dear <!subteam^S0TESTGRP01> hello"
	work := ansi.Strip(RenderSlackMarkdownWith(input, RenderSlackMarkdownOpts{
		UserGroups: map[string]string{"S0TESTGRP01": "work-team"},
	}))
	side := ansi.Strip(RenderSlackMarkdownWith(input, RenderSlackMarkdownOpts{
		UserGroups: map[string]string{"S0TESTGRP01": "side-team"},
	}))
	if !strings.Contains(work, "@work-team") {
		t.Errorf("work output = %q; want @work-team", work)
	}
	if !strings.Contains(side, "@side-team") {
		t.Errorf("side output = %q; want @side-team", side)
	}
}

// Labels arrive conventionally "@"-prefixed on the wire but that is
// not guaranteed; the renderer must not double the "@".
func TestRenderUsergroupMentionLabelWithoutAt(t *testing.T) {
	out := ansi.Strip(RenderSlackMarkdown("ping <!subteam^S123|eng>", nil, nil))
	if !strings.Contains(out, "@eng") {
		t.Errorf("expected @eng in output, got %q", out)
	}
	if strings.Contains(out, "@@eng") {
		t.Errorf("doubled @ in output: %q", out)
	}
}

func TestCommonMarkUsergroupMention(t *testing.T) {
	out := SlackMrkdwnToCommonMarkWithUserGroups(
		"ping <!subteam^S0AB12CD3> and <!subteam^S9|@qa>",
		nil,
		nil,
		map[string]string{"S0AB12CD3": "backend"},
	)
	if out != "ping @backend and @qa" {
		t.Errorf("SlackMrkdwnToCommonMarkWithUserGroups = %q; want %q", out, "ping @backend and @qa")
	}
}

func TestFlattenUsergroupBareResolved(t *testing.T) {
	out := FlattenMrkdwnWithUserGroups(
		"ping <!subteam^S0AB12CD3> please",
		nil,
		nil,
		map[string]string{"S0AB12CD3": "backend"},
	)
	if out != "ping @backend please" {
		t.Errorf("FlattenMrkdwnWithUserGroups = %q; want %q", out, "ping @backend please")
	}
}
