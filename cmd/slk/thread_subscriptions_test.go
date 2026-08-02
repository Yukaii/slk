package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gammons/slk/internal/cache"
	slackclient "github.com/gammons/slk/internal/slack"
	"github.com/slack-go/slack"
)

// fakeSubscriptions implements threadSubscriptionLister and counts its
// calls, so a caller that fetches the list more than once per trigger
// is visible.
type fakeSubscriptions struct {
	mu       sync.Mutex
	response []slackclient.ThreadSubscriptionView
	err      error
	calls    int
}

func (f *fakeSubscriptions) ListThreadSubscriptions(ctx context.Context) ([]slackclient.ThreadSubscriptionView, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

// subView constructs a ThreadSubscriptionView from primitives so these
// tests stay readable.
func subView(channel, threadTS, lastRead, text, user string, active bool) slackclient.ThreadSubscriptionView {
	return slackclient.ThreadSubscriptionView{
		Subscription: slackclient.ThreadSubscription{
			ChannelID: channel, ThreadTS: threadTS, LastRead: lastRead, Active: active,
		},
		RootMessage: slack.Message{
			Msg: slack.Msg{
				Timestamp:       threadTS,
				ThreadTimestamp: threadTS,
				User:            user,
				Text:            text,
				Channel:         channel,
			},
		},
	}
}

func newSubscriptionSync(db *cache.DB, fake *fakeSubscriptions, cb func(bool)) *threadSubscriptionSync {
	return &threadSubscriptionSync{client: fake, db: db, workspaceID: "T1", availableCb: cb}
}

// TestThreadSubscriptions_PopulatesTable verifies the sync fetches the
// workspace's subscription list and writes each active item into the
// thread_subscriptions table.
func TestThreadSubscriptions_PopulatesTable(t *testing.T) {
	db := newTestDB(t)
	fake := &fakeSubscriptions{response: []slackclient.ThreadSubscriptionView{
		subView("C1", "1700000100.000000", "1700000150.000000", "p1", "U2", true),
		subView("C2", "1700000200.000000", "1700000250.000000", "p2", "U3", true),
	}}
	if err := newSubscriptionSync(db, fake, nil).sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := db.ListActiveThreadSubscriptions("T1")
	if err != nil {
		t.Fatalf("ListActiveThreadSubscriptions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 subscriptions in DB, got %d", len(got))
	}
}

// TestThreadSubscriptions_UpsertsRootMessageIntoMessagesCache verifies
// every root_msg from the view response is upserted into the messages
// cache, so the threads view can render parents without a separate
// conversations.replies fetch per thread.
func TestThreadSubscriptions_UpsertsRootMessageIntoMessagesCache(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertChannel(cache.Channel{ID: "C1", WorkspaceID: "T1", Name: "general"}); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	fake := &fakeSubscriptions{response: []slackclient.ThreadSubscriptionView{
		subView("C1", "1700000100.000000", "1700000150.000000", "parent X", "U2", true),
	}}
	if err := newSubscriptionSync(db, fake, nil).sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	msgs, err := db.GetThreadReplies("C1", "1700000100.000000")
	if err != nil {
		t.Fatalf("GetThreadReplies: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 cached message (the parent), got %d", len(msgs))
	}
	if msgs[0].Text != "parent X" || msgs[0].UserID != "U2" {
		t.Fatalf("root_msg fields not preserved: %+v", msgs[0])
	}
}

// TestThreadSubscriptions_ReconcilesUnsubscribes verifies a local
// subscription absent from the server's fresh list is tombstoned.
func TestThreadSubscriptions_ReconcilesUnsubscribes(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertThreadSubscription("T1", "C1", "1700000100.000000", "1700000150.000000", true); err != nil {
		t.Fatalf("UpsertThreadSubscription: %v", err)
	}
	fake := &fakeSubscriptions{response: []slackclient.ThreadSubscriptionView{
		subView("C2", "1700000300.000000", "1700000350.000000", "p2", "U3", true),
	}}
	if err := newSubscriptionSync(db, fake, nil).sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := db.ListActiveThreadSubscriptions("T1")
	if err != nil {
		t.Fatalf("ListActiveThreadSubscriptions: %v", err)
	}
	if len(got) != 1 || got[0].ChannelID != "C2" {
		t.Fatalf("expected only C2 active after reconcile, got %+v", got)
	}
}

// TestThreadSubscriptions_ErrorTriggersAvailabilityCallback verifies an
// API error fires availableCb(false) and surfaces the error.
func TestThreadSubscriptions_ErrorTriggersAvailabilityCallback(t *testing.T) {
	db := newTestDB(t)
	var calls []bool
	cb := func(available bool) { calls = append(calls, available) }
	fake := &fakeSubscriptions{err: errors.New("network kaboom")}

	if err := newSubscriptionSync(db, fake, cb).sync(context.Background()); err == nil {
		t.Fatalf("expected error, got nil")
	}
	if len(calls) != 1 || calls[0] {
		t.Fatalf("expected one callback with available=false, got %v", calls)
	}
}

// TestThreadSubscriptions_SuccessTriggersAvailabilityCallback verifies a
// successful pass fires availableCb(true) exactly once.
func TestThreadSubscriptions_SuccessTriggersAvailabilityCallback(t *testing.T) {
	db := newTestDB(t)
	var calls []bool
	cb := func(available bool) { calls = append(calls, available) }
	if err := newSubscriptionSync(db, &fakeSubscriptions{}, cb).sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("expected one callback with available=true, got %v", calls)
	}
}
