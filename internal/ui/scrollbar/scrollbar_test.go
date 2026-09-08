package scrollbar

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	bg    = lipgloss.Color("#000000")
	track = lipgloss.Color("#444444")
	thumb = lipgloss.Color("#ff00ff")
)

func TestOverlay_NoOverflowReturnsUnchanged(t *testing.T) {
	in := []string{"row a", "row b"}
	cp := append([]string(nil), in...)
	out := Overlay(cp, 10, 2, 0, 2, bg, track, thumb)
	if strings.Join(out, "\n") != strings.Join(in, "\n") {
		t.Fatalf("expected unchanged when total <= visibleHeight; got %q", out)
	}
}

func TestOverlay_AddsGlyphPerVisibleRow(t *testing.T) {
	in := []string{"aaaaa", "bbbbb", "ccccc"}
	out := Overlay(append([]string(nil), in...), 5, 9, 0, 3, bg, track, thumb)
	if len(out) != 3 {
		t.Fatalf("expected 3 rows; got %d", len(out))
	}
	for i, row := range out {
		// Each row must end with either the track or thumb glyph, and
		// must be exactly width display columns wide.
		if w := ansi.StringWidth(row); w != 5 {
			t.Fatalf("row %d width = %d; want 5 (%q)", i, w, row)
		}
		if !(strings.Contains(row, "\u2588") || strings.Contains(row, "\u2502")) {
			t.Fatalf("row %d missing scrollbar glyph: %q", i, row)
		}
	}
}

func TestOverlay_ThumbAtTopWhenAtTop(t *testing.T) {
	in := []string{"...", "...", "..."}
	out := Overlay(append([]string(nil), in...), 3, 100, 0, 3, bg, track, thumb)
	// First row is at yOffset=0, total=100, visible=3 => thumb starts at row 0.
	if !strings.Contains(out[0], "\u2588") {
		t.Fatalf("expected thumb on row 0 at top; got %q", out[0])
	}
}

func TestOverlay_ThumbAtBottomWhenAtBottom(t *testing.T) {
	in := []string{"...", "...", "..."}
	// yOffset = total - visible = 97 -> thumb sits at bottom.
	out := Overlay(append([]string(nil), in...), 3, 100, 97, 3, bg, track, thumb)
	if !strings.Contains(out[2], "\u2588") {
		t.Fatalf("expected thumb on last row at bottom; got %q", out[2])
	}
}

func TestOverlay_PadsShortRowsToWidth(t *testing.T) {
	in := []string{"x", "yy", "zzz"}
	out := Overlay(append([]string(nil), in...), 6, 9, 0, 3, bg, track, thumb)
	for i, row := range out {
		if w := ansi.StringWidth(row); w != 6 {
			t.Fatalf("row %d width = %d; want 6 (%q)", i, w, row)
		}
	}
}

func TestOverlay_WideCharAtGutterEdgeStaysAligned(t *testing.T) {
	// A full-width row ending with a 2-cell char (CJK / emoji at the wrap
	// edge): cutting to gutterCol drops the whole cluster, so without a
	// post-cut re-pad the overlaid row ends up one cell short and the
	// scrollbar gutter staggers. Every row must stay exactly width.
	//
	// width=6 -> gutterCol=5. "abcdあ" is 4 + 2 = 6 cells; Cut to 5 drops
	// "あ" leaving 4 cells.
	in := []string{"abcdあ", "abcdef", "abcあd"}
	out := Overlay(append([]string(nil), in...), 6, 9, 0, 3, bg, track, thumb)
	for i, row := range out {
		if w := ansi.StringWidth(row); w != 6 {
			t.Fatalf("row %d width = %d; want 6 (%q)", i, w, row)
		}
	}
}

func TestVisible(t *testing.T) {
	if Visible(5, 5) {
		t.Error("Visible(5,5) should be false")
	}
	if !Visible(6, 5) {
		t.Error("Visible(6,5) should be true")
	}
}
