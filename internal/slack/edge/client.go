// Package edge wraps edgeapi.slack.com, Slack's edge cache API.
//
// This is a different protocol from the workspace Web API and is
// deliberately a separate package rather than more surface on
// slack.Client: JSON request bodies with a text/plain content type
// (the official client's way of avoiding a CORS preflight), a
// different host, and conditional-revalidation semantics via
// updated_ids.
//
// Request decoration — browser headers and the edgeapi query envelope
// (_x_app_name, fp, _x_num_retries) — is handled entirely by
// slackhttp.BrowserTransport, which already distinguishes edgeapi from
// the workspace API. This package must not set headers itself; doing
// so would reintroduce the divergence Phase 1 removed.
//
// Contracts verified against internal/slack/testdata/phase2-api-contracts.json.
package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultBaseURL = "https://edgeapi.slack.com"

// Client calls edgeapi.slack.com for one workspace.
type Client struct {
	token   string
	teamID  string
	http    *http.Client
	baseURL string
}

// New returns a Client. httpClient must be one built with
// slackhttp.BrowserTransport so requests carry the browser headers and
// edgeapi envelope; pass the same client slack.Client uses.
func New(token, teamID string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		token:   token,
		teamID:  teamID,
		http:    httpClient,
		baseURL: defaultBaseURL,
	}
}

// call POSTs payload (with the token merged in) to
// /cache/<teamID>/<endpoint> and decodes the response into out.
func (c *Client) call(ctx context.Context, endpoint string, payload map[string]any, out any) error {
	body := make(map[string]any, len(payload)+1)
	body["token"] = c.token
	for k, v := range payload {
		body[k] = v
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("edge %s: encoding request: %w", endpoint, err)
	}

	url := fmt.Sprintf("%s/cache/%s/%s", c.baseURL, c.teamID, endpoint)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("edge %s: building request: %w", endpoint, err)
	}
	// text/plain, not application/json: the official client uses a
	// CORS "simple request" so the browser skips the preflight. The
	// server accepts JSON regardless.
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("edge %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("edge %s: reading response: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("edge %s: HTTP %d: %s", endpoint, resp.StatusCode, truncate(raw))
	}

	var probe struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("edge %s: decoding %s: %w", endpoint, truncate(raw), err)
	}
	if !probe.OK {
		return fmt.Errorf("edge %s: %s", endpoint, probe.Error)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("edge %s: decoding result: %w", endpoint, err)
		}
	}
	return nil
}

func truncate(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}
