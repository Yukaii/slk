package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestUserResolver_BoundsConcurrentRequests is the second half of the
// cold-cache fix.
//
// Removing the membership fan-out stops the 40,000-request burst at its
// source, but Request is still reachable from render paths, the
// unresolved-DM sweep and inbound messages, and it used to spawn one
// goroutine per call with nothing between it and the transport. On a
// cold cache that is a burst waiting for a big enough trigger. The
// count of requests is a product question; the rate they leave at is
// not, and a client that opens hundreds of connections at once looks
// like nothing a person is driving.
func TestUserResolver_BoundsConcurrentRequests(t *testing.T) {
	var inFlight, maxInFlight, completed int32

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			hi := atomic.LoadInt32(&maxInFlight)
			if cur <= hi || atomic.CompareAndSwapInt32(&maxInFlight, hi, cur) {
				break
			}
		}
		// Long enough that a per-request goroutine would pile up
		// visibly rather than finishing before the next one starts.
		time.Sleep(25 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		atomic.AddInt32(&completed, 1)
		w.Header().Set("Content-Type", "application/json")
		// image_32 is deliberately absent: avatar.Cache.Preload
		// returns before touching its receiver when the URL is empty,
		// which is what lets this test pass a nil avatar cache.
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U1","name":"someone","team_id":"T1","profile":{"display_name":"Someone"}}}`))
	}))
	defer srv.Close()

	db := newTestDB(t)
	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil)

	const requests = 60
	for i := 0; i < requests; i++ {
		r.Request(fmt.Sprintf("U%03d", i))
	}

	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&completed) < requests && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&completed); got != requests {
		t.Fatalf("%d of %d requests completed; the pool must bound concurrency, not drop work", got, requests)
	}
	if got := atomic.LoadInt32(&maxInFlight); got > userResolverConcurrency {
		t.Errorf("peak concurrent users.info requests = %d; want at most %d — one goroutine per unresolved user is how a cold cache produced a 40,000-request burst", got, userResolverConcurrency)
	}
}

// TestUserResolver_RequestDoesNotBlockTheCaller pins the property the
// pool must not cost us: Request is called from render and event paths
// that cannot wait on the network.
func TestUserResolver_RequestDoesNotBlockTheCaller(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U1","name":"n","team_id":"T1","profile":{"display_name":"N"}}}`))
	}))
	defer srv.Close()
	defer close(release)

	db := newTestDB(t)
	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil)

	// Enough to fill the pool several times over. Every one of these
	// must return immediately even though nothing can complete.
	done := make(chan struct{})
	go func() {
		for i := 0; i < userResolverConcurrency*4; i++ {
			r.Request(fmt.Sprintf("U%03d", i))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Request blocked its caller; it is called from the render path and from WS event handlers, neither of which may wait on a users.info round trip")
	}
}
