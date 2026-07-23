//go:build darwin

package slackdesktop

import "github.com/keybase/go-keychain"

func keyringPassword() ([]byte, error) {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService("Slack Safe Storage")
	q.SetMatchLimit(keychain.MatchLimitOne)
	q.SetReturnData(true)
	results, err := keychain.QueryItem(q)
	if err != nil {
		return nil, ErrNoSecretService
	}
	if len(results) == 0 {
		return nil, ErrNoSecretService
	}
	return results[0].Data, nil
}
