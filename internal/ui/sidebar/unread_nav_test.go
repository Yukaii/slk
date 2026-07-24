package sidebar

import (
	"testing"

	"github.com/gammons/slk/internal/cache"
)

// items C1..C4; C2 and C4 are unread, C3 is unread-but-muted (excluded),
// C1 is read. NextUnread walks m.filtered (visible section order); a
// single explicit Section on every item keeps that order == input order
// so these assertions read cleanly.
func unreadNavModel() Model {
	m := New([]ChannelItem{
		{ID: "C1", Name: "one", Type: "channel", Section: "Eng"},
		{ID: "C2", Name: "two", Type: "channel", Section: "Eng"},
		{ID: "C3", Name: "muted", Type: "channel", Section: "Eng", IsMuted: true},
		{ID: "C4", Name: "four", Type: "channel", Section: "Eng"},
	})
	m.SetReadStateReader(func() map[string]cache.ReadState {
		return map[string]cache.ReadState{
			"C2": {HasUnread: true},
			"C3": {HasUnread: true}, // muted -> not visibly unread
			"C4": {HasUnread: true},
		}
	})
	return m
}

func TestNextUnread_ForwardSkipsCurrentAndMuted(t *testing.T) {
	m := unreadNavModel()

	// From C1 (read): next unread forward is C2.
	if id, _, _, ok := m.NextUnread("C1", 1); !ok || id != "C2" {
		t.Fatalf("from C1 want C2, got id=%q ok=%v", id, ok)
	}
	// From C2: skip muted C3, land on C4.
	if id, _, _, ok := m.NextUnread("C2", 1); !ok || id != "C4" {
		t.Fatalf("from C2 want C4 (muted C3 skipped), got id=%q ok=%v", id, ok)
	}
	// From C4: wrap around to C2.
	if id, _, _, ok := m.NextUnread("C4", 1); !ok || id != "C2" {
		t.Fatalf("from C4 want wrap to C2, got id=%q ok=%v", id, ok)
	}
}

func TestPrevUnread_Backward(t *testing.T) {
	m := unreadNavModel()

	// From C4: previous unread is C2 (C3 muted).
	if id, _, _, ok := m.NextUnread("C4", -1); !ok || id != "C2" {
		t.Fatalf("prev from C4 want C2, got id=%q ok=%v", id, ok)
	}
	// From C2: wrap backward to C4.
	if id, _, _, ok := m.NextUnread("C2", -1); !ok || id != "C4" {
		t.Fatalf("prev from C2 want wrap to C4, got id=%q ok=%v", id, ok)
	}
}

func TestNextUnread_ReturnsNameAndType(t *testing.T) {
	m := unreadNavModel()
	id, name, chType, ok := m.NextUnread("C1", 1)
	if !ok || id != "C2" || name != "two" || chType != "channel" {
		t.Fatalf("want C2/two/channel, got %q/%q/%q ok=%v", id, name, chType, ok)
	}
}

// The currently-open channel is always skipped even when it is itself
// unread (e.g. just marked unread), so a press always advances.
func TestNextUnread_SkipsCurrentEvenIfUnread(t *testing.T) {
	m := unreadNavModel()
	if id, _, _, ok := m.NextUnread("C2", 1); !ok || id != "C4" {
		t.Fatalf("from unread C2 want advance to C4, got id=%q ok=%v", id, ok)
	}
}

// Only the current channel is unread -> nothing else to jump to.
func TestNextUnread_NoOtherUnread(t *testing.T) {
	m := New([]ChannelItem{
		{ID: "C1", Name: "one", Type: "channel"},
		{ID: "C2", Name: "two", Type: "channel"},
	})
	m.SetReadStateReader(func() map[string]cache.ReadState {
		return map[string]cache.ReadState{"C1": {HasUnread: true}}
	})
	if _, _, _, ok := m.NextUnread("C1", 1); ok {
		t.Fatalf("only C1 unread and it is current: want ok=false")
	}
}

// afterID not present (nothing open yet) still finds the first unread.
func TestNextUnread_UnknownAfterID(t *testing.T) {
	m := unreadNavModel()
	if id, _, _, ok := m.NextUnread("", 1); !ok || id != "C2" {
		t.Fatalf("from empty want first unread C2, got id=%q ok=%v", id, ok)
	}
}

// No reader installed -> safe no-op.
func TestNextUnread_NoReader(t *testing.T) {
	m := New([]ChannelItem{{ID: "C1", Name: "one", Type: "channel"}})
	if _, _, _, ok := m.NextUnread("", 1); ok {
		t.Fatalf("no reader: want ok=false")
	}
}
