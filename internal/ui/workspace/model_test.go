package workspace

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWorkspaceRailView(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme Corp", Initials: "AC", HasUnread: false},
		{ID: "T2", Name: "Beta Inc", Initials: "BI", HasUnread: true},
	}, 0)

	view := m.View(20) // 20 rows height
	if !strings.Contains(view, "AC") {
		t.Error("expected 'AC' in view")
	}
	if !strings.Contains(view, "BI") {
		t.Error("expected 'BI' in view")
	}
}

func TestWorkspaceRailSelect(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE"},
	}, 0)

	if m.SelectedID() != "T1" {
		t.Error("expected T1 selected initially")
	}

	m.Select(1)
	if m.SelectedID() != "T2" {
		t.Error("expected T2 selected after Select(1)")
	}
}

// TestClickAt asserts ClickAt's mapping from rail-local y to workspace
// item using the rail's View() layout: row 0 is the top padding, row
// 1 is item 0, row 2 is the gap between items, row 3 is item 1, and
// so on (Padding(1,0) above and "\n\n"-joined item rows).
func TestClickAt(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE"},
		{ID: "T3", Name: "Gamma", Initials: "GA"},
	}, 0)

	cases := []struct {
		name   string
		y      int
		wantID string
		wantOK bool
	}{
		{"top padding", 0, "", false},
		{"first item", 1, "T1", true},
		{"gap between items 0 and 1", 2, "", false},
		{"second item", 3, "T2", true},
		{"gap between items 1 and 2", 4, "", false},
		{"third item", 5, "T3", true},
		{"below last item", 6, "", false},
		{"well below content", 99, "", false},
		{"negative y", -1, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.ClickAt(tc.y)
			if ok != tc.wantOK {
				t.Fatalf("ClickAt(%d) ok=%v want %v", tc.y, ok, tc.wantOK)
			}
			if got.ID != tc.wantID {
				t.Errorf("ClickAt(%d) ID=%q want %q", tc.y, got.ID, tc.wantID)
			}
		})
	}
}

func TestClickAt_EmptyRail(t *testing.T) {
	m := New(nil, 0)
	if _, ok := m.ClickAt(1); ok {
		t.Error("ClickAt on empty rail must return ok=false")
	}
}

func TestNameByID(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "SWAP", Initials: "SW"},
		{ID: "T2", Name: "Home", Initials: "HO"},
	}, 0)
	cases := map[string]string{
		"T1":        "SWAP",
		"T2":        "Home",
		"T-missing": "",
		"":          "",
	}
	for id, want := range cases {
		if got := m.NameByID(id); got != want {
			t.Errorf("NameByID(%q) = %q want %q", id, got, want)
		}
	}
}

func TestOtherUnreadCount_NoReader(t *testing.T) {
	m := New([]WorkspaceItem{{ID: "T1"}}, 0)
	if got := m.OtherUnreadCount("T1"); got != 0 {
		t.Errorf("OtherUnreadCount with no reader = %d want 0", got)
	}
}

func TestOtherUnreadCount(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1"}, {ID: "T2"}, {ID: "T3"},
	}, 0)
	m.SetUnreadReader(func() []string { return []string{"T1", "T2", "T3"} })

	cases := []struct {
		activeID string
		want     int
	}{
		{"T1", 2},
		{"T2", 2},
		{"T3", 2},
		{"T-missing", 3}, // active workspace not in unread set: counts all
		{"", 3},          // empty active: counts all; caller is responsible
	}
	for _, tc := range cases {
		if got := m.OtherUnreadCount(tc.activeID); got != tc.want {
			t.Errorf("OtherUnreadCount(%q) = %d want %d", tc.activeID, got, tc.want)
		}
	}
}

func TestOtherUnreadCount_EmptyReaderResult(t *testing.T) {
	m := New([]WorkspaceItem{{ID: "T1"}, {ID: "T2"}}, 0)
	m.SetUnreadReader(func() []string { return nil })
	if got := m.OtherUnreadCount("T1"); got != 0 {
		t.Errorf("OtherUnreadCount with empty reader = %d want 0", got)
	}
}

// itemRows returns the rendered rail rows that hold workspace tiles:
// row 0 is the top padding and tiles sit at rows 1, 3, 5, ... with
// blank gap rows between them (mirrors View's Padding(1,0) +
// "\n\n"-join layout, as in TestClickAt).
func itemRows(t *testing.T, m Model) []string {
	t.Helper()
	lines := strings.Split(m.View(10), "\n")
	var rows []string
	for i := 1; i < len(lines); i += 2 {
		rows = append(rows, lines[i])
		if len(rows) == 2 {
			break
		}
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 item rows, got %d", len(rows))
	}
	return rows
}

// TestView_UnreadDotKeepsTileWidth asserts the layout-shift fix: an
// unread dot must not change the tile's visible width. The dot lives
// in a permanently reserved trailing slot ("BE " vs "BE●"), so the
// row width is identical with and without unreads.
func TestView_UnreadDotKeepsTileWidth(t *testing.T) {
	read := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE"},
	}, 0)
	unread := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE", HasUnread: true},
	}, 0)

	readRows := itemRows(t, read)
	unreadRows := itemRows(t, unread)

	if !strings.Contains(unreadRows[1], "●") {
		t.Error("unread tile should render the ● dot")
	}
	if strings.Contains(readRows[1], "●") {
		t.Error("read tile should not render the ● dot")
	}
	for i := range readRows {
		if w, uw := lipgloss.Width(readRows[i]), lipgloss.Width(unreadRows[i]); w != uw {
			t.Errorf("row %d width changed with unread dot: read=%d unread=%d", i, w, uw)
		}
	}
}

// TestView_SelectingUnreadKeepsWidth asserts the second half of the
// jitter: selecting an unread workspace hides its dot (existing
// semantics) but must not shrink the tile, so switching workspaces
// never shifts the rail.
func TestView_SelectingUnreadKeepsWidth(t *testing.T) {
	items := []WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE", HasUnread: true},
	}
	away := itemRows(t, New(items, 0))
	onIt := itemRows(t, New(items, 1))

	if !strings.Contains(away[1], "●") {
		t.Error("unread workspace should show ● when not selected")
	}
	if strings.Contains(onIt[1], "●") {
		t.Error("unread workspace should hide ● while selected")
	}
	for i := range away {
		if w, ow := lipgloss.Width(away[i]), lipgloss.Width(onIt[i]); w != ow {
			t.Errorf("row %d width changed on selection: away=%d selected=%d", i, w, ow)
		}
	}
}

// TestView_DotUsesTileBackground guards the dot's background: a
// foreground-only dot style emits an ANSI reset that punches a
// rail-colored hole into the tile, so the dot must carry the tile's
// own background (detected here as an SGR background escape
// immediately before the ● glyph).
func TestView_DotUsesTileBackground(t *testing.T) {
	m := New([]WorkspaceItem{
		{ID: "T1", Name: "Acme", Initials: "AC"},
		{ID: "T2", Name: "Beta", Initials: "BE", HasUnread: true},
	}, 0)
	rows := itemRows(t, m)
	idx := strings.Index(rows[1], "●")
	if idx < 0 {
		t.Fatal("unread tile should render the ● dot")
	}
	// The SGR sequence opening the dot cell ends at the last ESC
	// before the glyph; it must set a background (48;...) as well
	// as the accent foreground.
	esc := strings.LastIndex(rows[1][:idx], "\x1b[")
	if esc < 0 {
		t.Fatal("expected an SGR sequence before the ● dot")
	}
	if sgr := rows[1][esc:idx]; !strings.Contains(sgr, "48;") {
		t.Errorf("dot SGR %q must include a background (48;...) matching the tile", sgr)
	}
}
