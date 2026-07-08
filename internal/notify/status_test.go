package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusReporter_RunsWithEnv(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	sr := NewStatusReporter(`printf '%s|%s|%s|%s' "$SLK_UNREAD" "$SLK_OTHER_UNREAD" "$SLK_WORKSPACE" "$SLK_TITLE" >` + out)
	if err := sr.Report(3, 1, "Tone Labs", "slk TL (3) +1"); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading status_command output: %v", err)
	}
	if want := "3|1|Tone Labs|slk TL (3) +1"; string(got) != want {
		t.Errorf("status_command received %q, want %q", got, want)
	}
}

func TestStatusReporter_EmptyIsNil(t *testing.T) {
	if sr := NewStatusReporter(""); sr != nil {
		t.Fatal("empty command should yield a nil StatusReporter")
	}
}

func TestStatusReporter_NilReportIsNoop(t *testing.T) {
	var sr *StatusReporter // nil
	if err := sr.Report(1, 0, "ws", "title"); err != nil {
		t.Errorf("nil StatusReporter.Report should be a no-op, got %v", err)
	}
}

// Workspace/title reach the command via the environment, so they can't inject a
// second shell command.
func TestStatusReporter_NotInjected(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	pwned := filepath.Join(dir, "pwned")
	sr := NewStatusReporter(`printf '%s' "$SLK_WORKSPACE" >` + out)
	if err := sr.Report(1, 0, "; touch "+pwned, "t"); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	if _, err := os.Stat(pwned); !os.IsNotExist(err) {
		t.Error("workspace name was able to inject a shell command")
	}
	got, _ := os.ReadFile(out)
	if want := "; touch " + pwned; string(got) != want {
		t.Errorf("workspace not passed literally: got %q, want %q", got, want)
	}
}
