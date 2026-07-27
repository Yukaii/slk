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

func TestEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, map[string]string{}, true},
		{"same entries", map[string]string{"S1": "eng"}, map[string]string{"S1": "eng"}, true},
		{"changed handle", map[string]string{"S1": "eng"}, map[string]string{"S1": "ops"}, false},
		{"changed id", map[string]string{"S1": "eng"}, map[string]string{"S2": "eng"}, false},
		{"extra entry", map[string]string{"S1": "eng"}, map[string]string{"S1": "eng", "S2": "ops"}, false},
	}
	for _, tc := range cases {
		if got := Equal(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: Equal(%v, %v) = %v, want %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}
