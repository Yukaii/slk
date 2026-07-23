package main

import (
	"context"
	"testing"

	slackclient "github.com/gammons/slk/internal/slack"
)

func TestRemintTokens(t *testing.T) {
	in := []slackclient.Token{
		{AccessToken: "old1", Cookie: "c-old", Domain: "acme", TeamID: "T1", TeamName: "Acme"},
	}
	saved := map[string]slackclient.Token{}
	out := remintTokens(context.Background(), in,
		func() (string, error) { return "c-new", nil },
		func(_ context.Context, domain, cookie string) (string, error) { return "xoxc-" + domain, nil },
		func(tok slackclient.Token) error { saved[tok.TeamID] = tok; return nil },
	)
	if out[0].AccessToken != "xoxc-acme" || out[0].Cookie != "c-new" {
		t.Fatalf("token not refreshed: %+v", out[0])
	}
	if saved["T1"].AccessToken != "xoxc-acme" {
		t.Fatalf("refreshed token not persisted: %+v", saved["T1"])
	}
}

func TestRemintTokensKeepsOldOnMintFailure(t *testing.T) {
	in := []slackclient.Token{{AccessToken: "old1", Cookie: "c-old", Domain: "acme", TeamID: "T1"}}
	out := remintTokens(context.Background(), in,
		func() (string, error) { return "", context.Canceled }, // cookie read fails
		func(_ context.Context, _, _ string) (string, error) { return "should-not-be-used", nil },
		func(slackclient.Token) error { return nil },
	)
	if out[0].AccessToken != "old1" {
		t.Fatalf("expected fallback to cached token, got %+v", out[0])
	}
}
