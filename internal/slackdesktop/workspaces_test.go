package slackdesktop

import (
	"os"
	"testing"
)

func TestParseWorkspaces(t *testing.T) {
	data, err := os.ReadFile("testdata/root-state.json")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := parseWorkspaces(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(ws))
	}
	// Sorted by name for stable output.
	if ws[0].Name != "Truelist" || ws[0].Domain != "truelist-workspace" || ws[0].TeamID != "T054JFC9S2Z" {
		t.Errorf("ws[0] = %+v", ws[0])
	}
}

func TestParseWorkspacesEmpty(t *testing.T) {
	_, err := parseWorkspaces([]byte(`{"workspaces":{}}`))
	if err != ErrNotSignedIn {
		t.Errorf("got %v, want ErrNotSignedIn", err)
	}
}
