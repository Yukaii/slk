package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/slack/edge"
	"github.com/gammons/slk/internal/ui"
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
	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil, nil, nil)

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
	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil, nil, nil)

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

// fakeBatcher implements userBatcher and records each batch it was
// asked for.
type fakeBatcher struct {
	mu   sync.Mutex
	sent []map[string]int64
	res  []edge.User
	err  error
}

func (f *fakeBatcher) UsersInfo(_ context.Context, updatedIDs map[string]int64) ([]edge.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[string]int64, len(updatedIDs))
	for k, v := range updatedIDs {
		cp[k] = v
	}
	f.sent = append(f.sent, cp)
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

func (f *fakeBatcher) calls() []map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]int64(nil), f.sent...)
}

// edgeUserRecord builds an edge.User, whose Profile is an anonymous
// struct with no spellable type at a call site.
func edgeUserRecord(id, name, display, real, teamID string, version int64, isBot bool) edge.User {
	u := edge.User{ID: id, Name: name, Version: version, IsBot: isBot, TeamID: teamID}
	u.Profile.DisplayName = display
	u.Profile.RealName = real
	return u
}

func TestUserResolver_BatchesMissesThroughEdge(t *testing.T) {
	// Measured on the first working Grid session: 282 users.info
	// calls, one per miss, with no coalescing. edge users/info takes
	// 80 ids a request and returns full records inline — the same
	// misses are ~4 requests.
	db := newTestDB(t)
	batcher := &fakeBatcher{res: []edge.User{
		edgeUserRecord("U001", "alice", "Alice", "", "T1", 1783337599010, false),
		edgeUserRecord("U002", "bob", "", "Bob Real", "T1", 1783337599011, false),
	}}
	var sentMu sync.Mutex
	var sent []tea.Msg
	r := newUserResolver("T1", nil, db, nil, func(m tea.Msg) {
		sentMu.Lock()
		sent = append(sent, m)
		sentMu.Unlock()
	}, batcher, nil)

	r.Request("U001")
	r.Request("U002")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(batcher.calls()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	calls := batcher.calls()
	if len(calls) != 1 {
		t.Fatalf("edge batches = %d; want 1 — misses inside the window coalesce (sent: %v)", len(calls), calls)
	}
	want := map[string]int64{"U001": 0, "U002": 0}
	if !reflect.DeepEqual(calls[0], want) {
		t.Errorf("batch = %v; want %v — 0 is the protocol's 'never seen, send the full record'", calls[0], want)
	}

	// Cached, so a second Request is a no-op.
	vers, err := db.UserVersions("T1")
	if err != nil {
		t.Fatalf("UserVersions: %v", err)
	}
	for _, id := range []string{"U001", "U002"} {
		if _, err := db.GetUser(id); err != nil {
			t.Fatalf("%s was not cached from the edge batch: %v", id, err)
		}
		if vers[id] == 0 {
			t.Errorf("%s cached with version 0; the batch returned one and conditional revalidation reads it", id)
		}
	}
	// Display-name chain: display when set, real otherwise.
	u1, _ := db.GetUser("U001")
	if u1.DisplayName != "Alice" {
		t.Errorf("U001 display = %q; want Alice", u1.DisplayName)
	}
	u2, _ := db.GetUser("U002")
	if u2.DisplayName != "Bob Real" {
		t.Errorf("U002 display = %q; want the real-name fallback", u2.DisplayName)
	}
	// One UserResolvedMsg per resolved user, same as the per-user path.
	sentMu.Lock()
	resolved := 0
	for _, m := range sent {
		if _, ok := m.(ui.UserResolvedMsg); ok {
			resolved++
		}
	}
	sentMu.Unlock()
	if resolved != 2 {
		t.Errorf("UserResolvedMsg count = %d; want 2 — the UI patches display names live from these", resolved)
	}
	// Dedup end-to-end: a repeat Request resolves nothing further.
	r.Request("U001")
	time.Sleep(userResolverBatchWindow + 300*time.Millisecond)
	if n := len(batcher.calls()); n != 1 {
		t.Errorf("a repeat Request produced batch %d; the cache check must make it a no-op", n)
	}
}

func TestUserResolver_EdgeMissFallsBackToPerUser(t *testing.T) {
	// ids edge does not return are resolved the old way — absence
	// from the batch means "could not resolve", and a raw user id on
	// screen is the failure this whole path exists to avoid.
	db := newTestDB(t)
	batcher := &fakeBatcher{res: []edge.User{
		edgeUserRecord("U001", "alice", "Alice", "", "T1", 1, false),
	}}
	var profiles atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profiles.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U002","name":"bob","team_id":"T1","profile":{"display_name":"Bob"}}}`))
	}))
	defer srv.Close()

	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil, batcher, nil)
	r.Request("U001")
	r.Request("U002")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := db.GetUser("U002"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := db.GetUser("U002"); err != nil {
		t.Fatal("U002 was absent from the edge batch and was never resolved per-user")
	}
	if got := profiles.Load(); got != 1 {
		t.Errorf("per-user users.info calls = %d; want 1 — only the id edge missed", got)
	}
}

func TestUserResolver_EdgeErrorFallsBackToPerUser(t *testing.T) {
	db := newTestDB(t)
	batcher := &fakeBatcher{err: errors.New("ratelimited")}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U001","name":"alice","team_id":"T1","profile":{"display_name":"Alice"}}}`))
	}))
	defer srv.Close()

	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil, batcher, nil)
	r.Request("U001")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := db.GetUser("U001"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the edge call failed and the per-user fallback never ran")
}

func TestUserResolver_DegradedWorkspaceSkipsEdge(t *testing.T) {
	// Once boot has marked a workspace's edge broken, the resolver
	// must not spend even one call discovering it again.
	db := newTestDB(t)
	batcher := &fakeBatcher{res: []edge.User{
		edgeUserRecord("U001", "alice", "Alice", "", "T1", 1, false),
	}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U001","name":"alice","team_id":"T1","profile":{"display_name":"Alice"}}}`))
	}))
	defer srv.Close()

	r := newUserResolver("T1", newTestClient(t, srv), db, nil, nil, batcher, func() bool { return true })
	r.Request("U001")
	time.Sleep(userResolverBatchWindow + 300*time.Millisecond)

	if n := len(batcher.calls()); n != 0 {
		t.Errorf("a degraded workspace made %d edge calls; want 0", n)
	}
	if _, err := db.GetUser("U001"); err != nil {
		t.Error("U001 was not resolved per-user on the degraded path")
	}
}
