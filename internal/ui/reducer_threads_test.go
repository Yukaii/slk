package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gammons/slk/internal/ids"
)

// TestApp_WorkspaceReadyAndActivationBothEnsureSubscriptions pins where
// the subscriptions.thread.getView fetch is triggered.
//
// Boot is a trigger because the socket replays nothing: an app that
// was closed (or asleep) for days missed every thread_subscription_
// changed event, and rendering the threads view from the days-old
// SQLite snapshot is the staleness this fetch fixes. Activation stays
// a trigger as a safety net. Collapsing both to one actual network
// sweep per workspace is the main-package gate's job
// (threadSubsGate) — the reducer deliberately fires unconditionally
// and stays dumb about throttling.
func TestApp_WorkspaceReadyAndActivationBothEnsureSubscriptions(t *testing.T) {
	app := NewApp()
	ensured := make(chan string, 4)
	app.SetThreadService(NewThreadService(ThreadServiceFuncs{
		ListFetch: func(teamID ids.TeamID) tea.Msg {
			return ThreadsListLoadedMsg{TeamID: string(teamID)}
		},
		EnsureSubscriptions: func(teamID ids.TeamID) {
			ensured <- string(teamID)
		},
	}))

	_, cmd := app.Update(WorkspaceReadyMsg{TeamID: "T1", TeamName: "Test", InitialActive: true})
	for _, m := range drainBatch(cmd) {
		_ = m
	}
	select {
	case team := <-ensured:
		if team != "T1" {
			t.Errorf("workspace-ready ensured subscriptions for %q; want T1", team)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("workspace-ready did not ensure subscriptions; a workspace slk never opens the Threads view on stays stale for the whole session")
	}

	app.activeTeamID = "T1"
	_, cmd = app.Update(ThreadsViewActivatedMsg{})
	for _, m := range drainBatch(cmd) {
		_ = m
	}
	select {
	case team := <-ensured:
		if team != "T1" {
			t.Errorf("activation ensured subscriptions for %q; want T1", team)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("opening the Threads view did not ensure subscriptions")
	}
}
