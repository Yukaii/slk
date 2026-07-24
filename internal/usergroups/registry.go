// Package usergroups contains pure helpers for workspace-scoped Slack
// usergroup mention maps.
package usergroups

import "strings"

// Copy returns an independent copy of an id -> handle map. Nil input
// returns nil so callers can preserve zero-value "no groups loaded"
// state without allocating.
func Copy(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for id, handle := range m {
		out[id] = handle
	}
	return out
}

// Equal reports whether two id -> handle maps hold the same entries.
// nil and empty compare equal: both mean "no groups loaded", and the
// setters that use this treat them identically.
func Equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, handle := range a {
		if other, ok := b[id]; !ok || other != handle {
			return false
		}
	}
	return true
}

// Display returns the "@handle" display text for a subteam token. The
// embedded label wins when present; bare tokens resolve through the
// provided workspace-scoped map; unresolved IDs fall back to "@group".
func Display(groups map[string]string, id, label string) string {
	if label != "" {
		return "@" + strings.TrimPrefix(label, "@")
	}
	if handle := groups[id]; handle != "" {
		return "@" + handle
	}
	return "@group"
}
