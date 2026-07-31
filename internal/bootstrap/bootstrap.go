// Package bootstrap owns the sequence of API calls slk makes when it
// connects to a workspace.
//
// It exists as a package, rather than as more of cmd/slk/main.go's
// connectWorkspace, for one reason: this sequence is what gets slk's
// Enterprise Grid users signed out for "data scraping", and inside
// connectWorkspace no test could reach it. connectWorkspace builds a
// live *slack.Client and calls Connect, so there is no seam without a
// live Slack connection. Everything here takes an interface.
//
// The call budget is the point. Across 8 captures of the official web
// client, a boot issues ~70 API requests and NEVER enumerates: zero
// users.list, zero conversations.list, zero per-channel
// conversations.history. slk previously issued roughly 400 and did all
// three. TestRun_NeverEnumerates is the regression guard.
//
// # Import direction
//
// This package must NOT import internal/slack. Phase 2b makes
// internal/slack import internal/slack/boot, and cmd/slk wires slack
// and bootstrap together; keeping the dependency pointing one way is
// what lets boot and edge stay stdlib-only parsers. The visible cost is
// that Result carries the RAW all_notifications_prefs string rather
// than a parsed mute list — the caller parses it with
// slack.ParseMutedFromAllNotificationsPrefs — and that Counts restates
// slack.UnreadInfo and slack.ThreadsAggregate rather than reusing them.
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gammons/slk/internal/slack/boot"
	"github.com/gammons/slk/internal/slack/edge"
)

// UserBooter fetches and parses client.userBoot.
type UserBooter interface {
	UserBoot(ctx context.Context) (*boot.Result, error)
}

// CountsFetcher fetches client.counts, slk's unread source of truth.
//
// Named CountsFetcher and not Counter, which is what the plan called
// it. slackhttp.Counter is an unrelated concrete type in this same
// phase — it tallies outbound requests by endpoint — and the two would
// sit side by side in cmd/slk's wiring, where "Counter" would name a
// request tally in one line and an unread-state fetcher in the next.
// The agent-noun form also matches UserBooter, Viewer, Historian and
// Revalidator below, none of which is named for the noun it returns.
type CountsFetcher interface {
	Counts(ctx context.Context) (Counts, error)
}

// Viewer fetches conversations.view for one channel. channelID may be
// "", reproducing the captured request, which sent no channel param
// and got back the last-viewed conversation.
type Viewer interface {
	ConversationsView(ctx context.Context, channelID string) (*boot.ViewResult, error)
}

// Historian is the verified fallback for Viewer: conversations.history
// with limit=28 and cached_latest_updates.
type Historian interface {
	HistoryWithVersions(ctx context.Context, channelID string, cached map[string]string) (History, error)
}

// Revalidator is the edgeapi conditional-revalidation pair. This is
// what replaces enumeration.
type Revalidator interface {
	ChannelsInfo(ctx context.Context, updatedIDs map[string]int64) (edge.ChannelsInfoResult, error)
	UsersInfo(ctx context.Context, updatedIDs map[string]int64) ([]edge.User, error)
}

// Store is the cache surface bootstrap writes through. Deliberately
// the narrow revalidation writers from internal/cache/edge_sync.go,
// not the full upserts: a full upsert would blank is_member,
// is_starred, avatar_url and presence, none of which any edge response
// carries.
//
// *cache.DB does NOT satisfy this as written, and that is deliberate
// rather than an oversight. cache.DB.ApplyMembership takes
// (workspaceID string, queriedIDs []string, snap cache.MembershipSnapshot);
// MembershipSnapshot exists precisely because encoding/json cannot
// distinguish an absent member_channels from a literal [], and those
// are opposite answers — "the server said nothing, keep what you have"
// versus "the server looked and named nobody, clear them all". A
// []string here cannot carry that distinction.
//
// The adapter in Task 7 is what bridges it, and it must apply the
// heuristic cache.MembershipSnapshot's own doc prescribes:
//
//	snap := cache.MembershipUnreported()
//	if len(memberIDs) > 0 {
//		snap = cache.MembershipReported(memberIDs, failedIDs)
//	}
//
// Taking cache.MembershipSnapshot directly here would be the honest
// alternative, and would mean importing internal/cache — which Task 6
// does anyway for the UpdateFromEdge writers. Left as-is for now
// because nothing in this package calls Store yet, and a signature
// with a caller is easier to get right than one without.
type Store interface {
	ChannelVersions(workspaceID string) (map[string]int64, error)
	UserVersions(workspaceID string) (map[string]int64, error)
	ApplyMembership(workspaceID string, queriedIDs, memberIDs []string) error
}

// Unread is one channel's unread state from client.counts.
//
// A restatement of slack.UnreadInfo rather than a reuse of it: see the
// package comment on import direction. The field set is identical, so
// the Task 7 adapter's conversion is mechanical.
type Unread struct {
	ChannelID string
	Count     int
	HasUnread bool
	LastRead  string
}

// Threads is client.counts' workspace-wide thread rollup — a
// restatement of slack.ThreadsAggregate.
//
// HasUnreads is the authoritative answer to "does the user have unread
// thread activity", and slk needs it because the local cache holds no
// per-thread read state and its heuristic produces false positives.
type Threads struct {
	HasUnreads   bool
	UnreadCount  int
	MentionCount int
}

// Counts is everything one client.counts call learned.
type Counts struct {
	Unreads []Unread
	Threads Threads
}

// History is what a conversations.history fallback returned — a
// restatement of slack.HistoryResult, again to keep internal/slack out
// of this package's imports.
//
// Messages is []json.RawMessage rather than []slack.Message so that a
// view result and a history result can both land in Result.Messages
// without one of them being converted first. boot.History.Messages is
// already raw for its own reasons (the shape varies: 17 distinct keys
// across 56 captured messages, only 8 on all of them), so raw is the
// type the two paths already share.
type History struct {
	// Messages are the bodies the server actually sent. With a
	// populated cached map this is only what CHANGED.
	Messages []json.RawMessage
	// UnchangedTS lists the timestamps from the request's
	// cached_latest_updates the server confirms the caller still holds.
	UnchangedTS []string
	// LatestUpdates is {ts: version} for the returned messages, to be
	// fed back as the cached map next time. The versions are opaque
	// and are only ever echoed, never parsed or compared.
	LatestUpdates map[string]string
	// HasMore reports whether the requested window was truncated.
	HasMore bool
}

// Result is everything the boot sequence learned, in the shape
// connectWorkspace consumes.
//
// The boot types are reused rather than restated — boot.Self,
// boot.Team, boot.Channel, boot.IM and boot.DND all appear verbatim.
// They are stdlib-only parse targets with no behaviour, and copying
// them here would create two shapes to keep in agreement forever.
type Result struct {
	Self boot.Self
	Team boot.Team

	// Channels are the conversations the user belongs to, DMs
	// excluded; IMs are the DMs. userBoot splits them, and so does
	// this.
	Channels []boot.Channel
	IMs      []boot.IM

	// IsOpen holds the conversation ids currently shown in the
	// sidebar, channels and DMs mixed.
	IsOpen []string

	DND boot.DND

	// ChannelsPriority is Slack's per-channel affinity score.
	ChannelsPriority map[string]float64

	// EmojiCacheTS is a 17-character cache token to be echoed back
	// verbatim. It looks numeric and is not.
	EmojiCacheTS string

	// MutePrefsRaw is the RAW all_notifications_prefs value: a
	// JSON-encoded string whose contents are JSON. It is not parsed
	// here because slack.ParseMutedFromAllNotificationsPrefs already
	// decodes exactly this and calling it would mean importing
	// internal/slack. Callers parse.
	MutePrefsRaw string

	// LegacyMutedRaw is the legacy flat comma-separated muted_channels
	// list. It was absent from the captured response — all 702 prefs
	// keys were checked — but slk's existing GetMutedChannels still
	// merges it for workspaces that do ship it, so it is carried
	// through rather than dropped.
	LegacyMutedRaw string

	// Counts is the unread state. It is the zero value when
	// client.counts failed, which is not distinguishable from a
	// workspace with nothing unread — deliberately, since the failure
	// is logged and the difference is cosmetic.
	Counts Counts
}

// Deps is everything Run needs. Every field is required unless its
// comment says otherwise.
type Deps struct {
	WorkspaceID string

	Boot       UserBooter
	Counts     CountsFetcher
	View       Viewer
	History    Historian
	Revalidate Revalidator
	Store      Store

	// OpenChannelID is the conversation to open — the restored last
	// channel, or the configured default. Empty means "whatever Slack
	// considers last-viewed", which is what the capture did.
	OpenChannelID string

	// Log is optional; nil discards.
	Log func(format string, args ...any)
}

// Run performs the boot sequence and returns everything the UI needs.
//
// On any error the returned *Result is nil. That is load-bearing
// rather than tidiness, and it is the same rule boot.UserBoot follows:
// a caller handed both a Result and an error can use the Result, and a
// workspace assembled from a failed boot renders like a real one.
func Run(ctx context.Context, deps Deps) (*Result, error) {
	logf := deps.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Deps is assembled field by field at a call site in
	// cmd/slk/main.go, so a forgotten dependency arrives as a nil
	// interface. Calling through it panics, which in a Bubble Tea
	// program means a stack trace over a torn-down terminal instead of
	// a message. Boot and Counts are checked because Boot and Counts
	// are what Run uses; later tasks check what they add.
	if deps.Boot == nil {
		return nil, errors.New("bootstrap: Deps.Boot is required")
	}
	if deps.Counts == nil {
		return nil, errors.New("bootstrap: Deps.Counts is required")
	}

	bootRes, err := deps.Boot.UserBoot(ctx)
	if err != nil {
		// Fatal: every step below is keyed by what this returned.
		return nil, fmt.Errorf("bootstrap: userBoot: %w", err)
	}

	out := &Result{
		Self:             bootRes.Self,
		Team:             bootRes.Team,
		Channels:         bootRes.Channels,
		IMs:              bootRes.IMs,
		IsOpen:           bootRes.IsOpen,
		DND:              bootRes.DND,
		ChannelsPriority: bootRes.ChannelsPriority,
		EmojiCacheTS:     bootRes.EmojiCacheTS,
		MutePrefsRaw:     bootRes.Prefs.AllNotificationsPrefs,
		LegacyMutedRaw:   bootRes.Prefs.MutedChannels,
	}

	// Unread state. Non-fatal: badges are cosmetic and a workspace
	// that boots without them beats one that does not boot.
	if counts, err := deps.Counts.Counts(ctx); err != nil {
		logf("bootstrap: counts: %v (continuing without unread state)", err)
	} else {
		out.Counts = counts
	}

	return out, nil
}
