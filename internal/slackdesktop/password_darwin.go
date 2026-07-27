//go:build darwin

package slackdesktop

import (
	"bytes"
	"os/exec"
	"strings"
)

// keyringPassword fetches the "Slack Safe Storage" password from the macOS
// login keychain by shelling out to /usr/bin/security.
//
// We deliberately shell out rather than use a Security.framework binding: the
// release build sets CGO_ENABLED=0 (see .goreleaser.yaml) and cross-compiles
// darwin from Linux, so a cgo keychain dependency would break the macOS build.
// `security find-generic-password -w -s "Slack Safe Storage"` prints just the
// password on stdout.
func keyringPassword() ([]byte, error) {
	cmd := exec.Command("/usr/bin/security", "find-generic-password", "-w", "-s", "Slack Safe Storage")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, ErrNoSecretService
	}
	pw := strings.TrimRight(out.String(), "\r\n")
	if pw == "" {
		return nil, ErrNoSecretService
	}
	return []byte(pw), nil
}
