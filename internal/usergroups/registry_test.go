package usergroups

import "testing"

func TestCopyReturnsIndependentMap(t *testing.T) {
	src := map[string]string{"S1": "eng"}
	got := Copy(src)
	got["S1"] = "mutated"
	if src["S1"] != "eng" {
		t.Errorf("Copy result aliases source map: source S1 = %q", src["S1"])
	}
}

func TestDisplayPrefersLabelAndNormalizesAt(t *testing.T) {
	groups := map[string]string{"S1": "eng"}
	if got := Display(groups, "S1", "ops"); got != "@ops" {
		t.Errorf("Display label without @ = %q, want @ops", got)
	}
	if got := Display(groups, "S1", "@ops"); got != "@ops" {
		t.Errorf("Display label with @ = %q, want @ops", got)
	}
}

func TestDisplayResolvesBareID(t *testing.T) {
	if got := Display(map[string]string{"S1": "eng"}, "S1", ""); got != "@eng" {
		t.Errorf("Display bare ID = %q, want @eng", got)
	}
}

func TestDisplayFallsBackForUnknownID(t *testing.T) {
	if got := Display(map[string]string{"S1": "eng"}, "S2", ""); got != "@group" {
		t.Errorf("Display unknown ID = %q, want @group", got)
	}
}
