package slackdesktop

import "errors"

var (
	ErrDesktopNotFound = errors.New("slack desktop app config directory not found")
	ErrNotSignedIn     = errors.New("no slack workspaces are signed in")
	ErrCookieDBMissing = errors.New("slack cookie database not found")
	ErrKeyringLocked   = errors.New("system keyring is locked")
	ErrNoSecretService = errors.New("no system secret service available")
	ErrDecryptFailed   = errors.New("failed to decrypt slack session cookie")
)
