package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPostFormRetriesOnInvalidAuth(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient("xoxc-old", "xoxd-1")
	c.apiBaseURL = srv.URL + "/"
	c.httpClient = srv.Client()
	c.reauth = func(ctx context.Context) error { c.token = "xoxc-new"; return nil }

	body, err := c.postForm(context.Background(), "users.prefs.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (retry), got %d", calls)
	}
}
