package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMintTokenScrapesAPIToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("d"); c == nil || c.Value != "xoxd-abc" {
			t.Errorf("missing/incorrect d cookie: %+v", c)
		}
		w.Write([]byte(`<html>...,"api_token":"xoxc-12345",...</html>`))
	}))
	defer srv.Close()

	got, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxc-12345" {
		t.Errorf("got %q, want xoxc-12345", got)
	}
}

func TestMintTokenNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>no token here</html>`))
	}))
	defer srv.Close()
	if _, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc"); err == nil {
		t.Error("expected error when api_token absent")
	}
}
