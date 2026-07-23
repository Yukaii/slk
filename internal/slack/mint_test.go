package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestMintTokenRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0") // retry immediately, keep test fast
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`<html>"api_token":"xoxc-ok"</html>`))
	}))
	defer srv.Close()

	got, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxc-ok" {
		t.Errorf("got %q, want xoxc-ok", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (one retry), got %d", calls)
	}
}

func TestMintTokenGivesUpAfterRepeated429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc")
	if err == nil {
		t.Fatal("expected error after repeated 429s")
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 attempts (maxAttempts), got %d", calls)
	}
}
