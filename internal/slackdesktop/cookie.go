package slackdesktop

import (
	"path/filepath"
	"runtime"
)

// decryptCookieValue picks the right key + algorithm for the OS and the
// value's version prefix. getPassword is the injected keyring/keychain/DPAPI
// source (may return nil for the linux v10 "peanuts" case).
func decryptCookieValue(goos string, enc []byte, getPassword func() ([]byte, error)) ([]byte, error) {
	if len(enc) < 3 {
		return nil, ErrDecryptFailed
	}
	version := string(enc[:3])
	body := enc[3:]

	switch goos {
	case "windows":
		key, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptGCM(body, key)
	case "darwin":
		pw, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptCBC(body, pw, 1003)
	default: // linux
		if version == "v10" {
			return decryptCBC(body, []byte("peanuts"), 1)
		}
		pw, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptCBC(body, pw, 1)
	}
}

// Cookie reads and decrypts the Slack desktop `d` cookie.
func Cookie() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(dir, "Cookies")
	if runtime.GOOS == "windows" {
		dbPath = filepath.Join(dir, "Network", "Cookies")
	}
	plain, enc, err := readCookieRow(dbPath)
	if err != nil {
		return "", err
	}
	if plain != "" {
		return plain, nil
	}
	val, err := decryptCookieValue(runtime.GOOS, enc, keyringPassword)
	if err != nil {
		return "", err
	}
	return string(val), nil
}
