package notify

import (
	"os"
	"os/exec"
	"strconv"
)

// StatusReporter runs a user-configured status_command whenever slk's unread
// state changes, exposing that state through environment variables so an
// external surface (a status bar, tmux, a terminal multiplexer's sidebar) can
// reflect it.
type StatusReporter struct {
	command string
}

// NewStatusReporter returns a StatusReporter, or nil when command is empty so
// callers can skip wiring it. Report is nil-safe, so a nil result is usable.
func NewStatusReporter(command string) *StatusReporter {
	if command == "" {
		return nil
	}
	return &StatusReporter{command: command}
}

// Report runs the status_command with the current unread state exposed as
// $SLK_UNREAD, $SLK_OTHER_UNREAD, $SLK_WORKSPACE and $SLK_TITLE. Values are
// passed through the environment rather than interpolated into the command, so
// a workspace name or title can't inject shell syntax. Nil-safe (no-op).
func (r *StatusReporter) Report(unread, otherUnread int, workspace, title string) error {
	if r == nil {
		return nil
	}
	cmd := exec.Command("sh", "-c", r.command)
	cmd.Env = append(os.Environ(),
		"SLK_UNREAD="+strconv.Itoa(unread),
		"SLK_OTHER_UNREAD="+strconv.Itoa(otherUnread),
		"SLK_WORKSPACE="+workspace,
		"SLK_TITLE="+title,
	)
	return cmd.Run()
}
