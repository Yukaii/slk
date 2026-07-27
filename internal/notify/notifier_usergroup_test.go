package notify

import "testing"

func TestStripSlackMarkupBareSubteamResolved(t *testing.T) {
	got := StripSlackMarkupWithUserGroups(
		"dear <!subteam^S0TESTGRP01> hello",
		nil,
		map[string]string{"S0TESTGRP01": "platform-team"},
	)
	if got != "dear @platform-team hello" {
		t.Errorf("StripSlackMarkupWithUserGroups = %q; want %q", got, "dear @platform-team hello")
	}
}

func TestStripSlackMarkupBareSubteamUnresolved(t *testing.T) {
	got := StripSlackMarkupWithUserGroups("dear <!subteam^S0MISSING> hello", nil, nil)
	if got != "dear @group hello" {
		t.Errorf("StripSlackMarkupWithUserGroups = %q; want %q", got, "dear @group hello")
	}
}

func TestStripSlackMarkupSubteamLabelWithoutAt(t *testing.T) {
	got := StripSlackMarkupWithUserGroups("dear <!subteam^S123|eng> hello", nil, nil)
	if got != "dear @eng hello" {
		t.Errorf("StripSlackMarkupWithUserGroups = %q; want %q", got, "dear @eng hello")
	}
}
