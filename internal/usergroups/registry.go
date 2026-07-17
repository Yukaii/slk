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
