package slackdesktop

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

const cookieQuery = `SELECT value, encrypted_value FROM cookies WHERE host_key=".slack.com" AND name="d"`

// readCookieRow returns the plaintext value (usually empty) and the encrypted
// value blob for the Slack `d` cookie.
func readCookieRow(dbPath string) (string, []byte, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", nil, ErrCookieDBMissing
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", nil, err
	}
	defer db.Close()

	var plain string
	var enc []byte
	if err := db.QueryRow(cookieQuery).Scan(&plain, &enc); err != nil {
		return "", nil, err
	}
	return plain, enc, nil
}
