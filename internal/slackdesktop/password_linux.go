//go:build linux

package slackdesktop

import (
	"strings"

	"r00t2.io/gosecret"
)

// keyringPassword fetches the "Slack Safe Storage" password from the
// libsecret-backed Secret Service.
func keyringPassword() ([]byte, error) {
	service, err := gosecret.NewService()
	if err != nil {
		if strings.Contains(err.Error(), "not provided") || strings.Contains(err.Error(), "no such") {
			return nil, ErrNoSecretService
		}
		return nil, ErrNoSecretService
	}
	defer service.Close()

	attrs := map[string]string{
		"xdg:schema":  "chrome_libsecret_os_crypt_password_v2",
		"application": "Slack",
	}
	unlocked, locked, err := service.SearchItems(attrs)
	if err != nil {
		return nil, err
	}
	if len(unlocked) == 0 {
		if len(locked) > 0 {
			return nil, ErrKeyringLocked
		}
		// Try the v1 schema before giving up.
		attrs["xdg:schema"] = "chrome_libsecret_os_crypt_password_v1"
		unlocked, locked, err = service.SearchItems(attrs)
		if err != nil {
			return nil, err
		}
		if len(unlocked) == 0 {
			if len(locked) > 0 {
				return nil, ErrKeyringLocked
			}
			return nil, ErrNoSecretService
		}
	}
	return unlocked[0].Secret.Value, nil
}
