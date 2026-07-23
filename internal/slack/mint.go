package slackclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var apiTokenRE = regexp.MustCompile(`"api_token":"([^"]+)"`)

// MintToken mints a fresh xoxc token for a workspace by loading its page with
// the desktop `d` cookie and scraping the embedded api_token. It uses a
// browser-shaped HTTP client with the cookie set.
func MintToken(ctx context.Context, domain, dCookie string) (string, error) {
	client := newCookieHTTPClient(dCookie)
	return mintTokenAt(ctx, client, fmt.Sprintf("https://%s.slack.com", domain), dCookie)
}

// mintTokenAt is the testable core: GET baseURL with the d cookie, scrape
// api_token. The cookie is attached explicitly so httptest servers (which are
// not *.slack.com) still receive it.
func mintTokenAt(ctx context.Context, client *http.Client, baseURL, dCookie string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return "", err
	}
	req.AddCookie(&http.Cookie{Name: "d", Value: dCookie})

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mint token: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := apiTokenRE.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("mint token: api_token not found (is the desktop app signed in?)")
	}
	return string(m[1]), nil
}
