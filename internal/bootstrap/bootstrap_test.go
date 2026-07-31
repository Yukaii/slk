package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/gammons/slk/internal/slack/boot"
)

// The names the fake records. They are the API method names rather than
// the Go method names on purpose: TestRun_NeverEnumerates asserts
// against method names ("users.list"), so recording anything else would
// make the guard unable to see the very thing it exists to catch.
const (
	callUserBoot = "client.userBoot"
	callCounts   = "client.counts"
)

// cannedBootResult is the userBoot response every test runs against.
//
// EVERY field Run reads is populated with a DISTINCT, NON-ZERO value,
// and that is load-bearing rather than tidiness. Phase 2a lost 9
// mutants to a fixture whose booleans were all false and another to two
// string fields that happened to share a value: a field mapped from the
// wrong source, or dropped entirely, is invisible when the right and
// wrong answers are both the zero value.
//
// The same-typed neighbours are the specific hazard here, because
// swapping them still compiles:
//
//   - IsOpen, ReadOnlyChannels, NonThreadableChannels and
//     ThreadOnlyChannels are all []string on boot.Result. The three Run
//     does NOT map are populated anyway, so that mapping IsOpen from
//     one of them is detectable.
//   - Prefs.AllNotificationsPrefs and Prefs.MutedChannels are both
//     strings, and Run maps them to two different Result fields.
//   - EmojiCacheTS and DefaultWorkspace are both strings.
func cannedBootResult() *boot.Result {
	return &boot.Result{
		Channels: []boot.Channel{
			{
				ID:             "C_GENERAL",
				Name:           "general",
				NameNormalized: "general-normalized",
				Version:        1783337533019,
				Created:        1600000001,
				IsChannel:      true,
				IsGeneral:      true,
				IsShared:       true,
				ContextTeamID:  "T_HOME",
				Creator:        "U_CREATOR_1",
				SharedTeamIDs:  []string{"T_HOME", "T_GUEST"},
				Topic:          boot.TextBlock{Value: "topic one", Creator: "U_CREATOR_1", LastSet: 1600000011},
				Purpose:        boot.TextBlock{Value: "purpose one", Creator: "U_CREATOR_1", LastSet: 1600000012},
			},
			{
				ID:             "C_PRIVATE",
				Name:           "private",
				NameNormalized: "private-normalized",
				Version:        1783337533020,
				Created:        1600000002,
				IsGroup:        true,
				IsPrivate:      true,
				IsArchived:     true,
				IsMPIM:         true,
				IsOrgShared:    true,
				IsExtShared:    true,
				ContextTeamID:  "T_OTHER",
				Creator:        "U_CREATOR_2",
				SharedTeamIDs:  []string{"T_OTHER"},
				Topic:          boot.TextBlock{Value: "topic two", Creator: "U_CREATOR_2", LastSet: 1600000021},
				Purpose:        boot.TextBlock{Value: "purpose two", Creator: "U_CREATOR_2", LastSet: 1600000022},
			},
		},
		IMs: []boot.IM{
			{
				ID:            "D_ALICE",
				UserID:        "U_ALICE",
				Version:       1783337533021,
				Created:       1600000003,
				IsIM:          true,
				IsOpen:        true,
				ContextTeamID: "T_HOME",
			},
			{
				ID:            "D_BOB",
				UserID:        "U_BOB",
				Version:       1783337533022,
				Created:       1600000004,
				IsIM:          true,
				IsArchived:    true,
				IsOrgShared:   true,
				ContextTeamID: "T_OTHER",
			},
		},
		IsOpen:  []string{"C_GENERAL", "D_ALICE"},
		Starred: []json.RawMessage{json.RawMessage(`{"type":"channel","channel":"C_GENERAL"}`)},
		Subteams: boot.Subteams{
			Self: []json.RawMessage{json.RawMessage(`{"id":"S_TEAM"}`)},
		},
		DND: boot.DND{
			Enabled:       true,
			NextStartTS:   1783300000,
			NextEndTS:     1783330000,
			SnoozeEnabled: true,
		},
		Prefs: boot.Prefs{
			MutedChannels:         "C_LEGACY_MUTED,C_LEGACY_MUTED_2",
			AllNotificationsPrefs: `{"channels":{"C_PRIVATE":{"muted":true}}}`,
			Raw:                   json.RawMessage(`{"muted_channels":"C_LEGACY_MUTED,C_LEGACY_MUTED_2"}`),
		},
		Self: boot.Self{
			ID:       "U_SELF",
			Name:     "self-name",
			TeamID:   "T_HOME",
			RealName: "Self Realname",
			TZ:       "America/New_York",
			TZOffset: -14400,
			Version:  1783337533023,
			Profile: boot.SelfProfile{
				RealName:         "Profile Realname",
				DisplayName:      "profile-display",
				AvatarHash:       "abc123hash",
				ImageOriginal:    "https://example.invalid/avatar-original.png",
				Email:            "self@example.invalid",
				StatusText:       "status text",
				StatusEmoji:      ":wave:",
				StatusExpiration: 1783400000,
			},
		},
		Team: boot.Team{
			ID:            "T_HOME",
			Name:          "Home Team",
			Domain:        "home-domain",
			URL:           "https://home.example.invalid/",
			AvatarBaseURL: "https://avatars.example.invalid/",
		},
		ChannelsPriority: map[string]float64{"C_GENERAL": 0.75, "C_PRIVATE": 0.25},
		EmojiCacheTS:     "17833375330191742",

		// Populated but NOT mapped by Run. They exist so that mapping
		// Result.IsOpen from the wrong []string is detectable.
		ReadOnlyChannels:      []string{"C_READONLY"},
		NonThreadableChannels: []string{"C_NONTHREADABLE"},
		ThreadOnlyChannels:    []string{"C_THREADONLY"},

		DefaultWorkspace: "T_DEFAULT_WORKSPACE",
		HasMoreMPDMs:     true,
	}
}

// cannedCounts is the client.counts response every test runs against.
// Distinct, non-zero throughout for the same reason as
// cannedBootResult.
func cannedCounts() Counts {
	return Counts{
		Unreads: []Unread{
			{ChannelID: "C_GENERAL", Count: 7, HasUnread: true, LastRead: "1700000001.000100"},
			{ChannelID: "D_ALICE", Count: 3, HasUnread: true, LastRead: "1700000002.000200"},
		},
		Threads: Threads{HasUnreads: true, UnreadCount: 11, MentionCount: 5},
	}
}

// fakeDeps is the whole test harness: it records an ordered call log,
// returns the canned responses above, and injects a per-dependency
// error.
//
// It is deliberately the ONLY thing the tests talk to. bootstrap exists
// because connectWorkspace builds a live *slack.Client, so a test that
// needed a server here would have reproduced the problem the package
// was created to solve.
type fakeDeps struct {
	mu    sync.Mutex
	calls []string

	bootRes     *boot.Result
	userBootErr error

	counts    Counts
	countsErr error

	logs []string

	deps Deps
}

func newFakeDeps() *fakeDeps {
	f := &fakeDeps{
		bootRes: cannedBootResult(),
		counts:  cannedCounts(),
	}
	f.deps = Deps{
		WorkspaceID: "T_HOME",
		Boot:        f,
		Counts:      f,
		Log:         f.log,
	}
	return f
}

// Deps returns the dependency set to hand Run. Tests mutate f.deps
// directly before calling it.
func (f *fakeDeps) Deps() Deps { return f.deps }

func (f *fakeDeps) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeDeps) log(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = args
	f.logs = append(f.logs, format)
}

func (f *fakeDeps) logged() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logs...)
}

// called reports whether name was invoked at all.
func (f *fakeDeps) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// countPrefix counts invocations whose name starts with prefix. Used
// for the conversations.history fan-out guard, where the question is
// "how many" rather than "any at all".
func (f *fakeDeps) countPrefix(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

func (f *fakeDeps) UserBoot(context.Context) (*boot.Result, error) {
	f.record(callUserBoot)
	if f.userBootErr != nil {
		// Mirrors boot.UserBoot, which returns a nil Result on every
		// error path deliberately — a populated one would hand the
		// caller a plausible workspace built from a rejected response.
		return nil, f.userBootErr
	}
	return f.bootRes, nil
}

// poisonedCounts is what the fake returns ALONGSIDE an error.
//
// A zero value there would make "use the counts even though the call
// failed" an equivalent mutant — indistinguishable from the correct
// behaviour, since both leave Result.Counts zero. Returning something
// non-zero and obviously wrong is what makes that mutation killable,
// and the hazard is real: Slack's own endpoints return a fully
// populated body next to ok:false, which is why boot.UserBoot goes out
// of its way to return a nil Result on every error path.
func poisonedCounts() Counts {
	return Counts{
		Unreads: []Unread{{ChannelID: "C_POISON", Count: 999, HasUnread: true, LastRead: "9999999999.999999"}},
		Threads: Threads{HasUnreads: true, UnreadCount: 999, MentionCount: 999},
	}
}

func (f *fakeDeps) Counts(context.Context) (Counts, error) {
	f.record(callCounts)
	if f.countsErr != nil {
		return poisonedCounts(), f.countsErr
	}
	return f.counts, nil
}

// prefixEqual reports whether got starts with want.
//
// A prefix rather than an equality check because later tasks append
// steps to the sequence; the ORDER of the first two is the invariant,
// not the total length.
func prefixEqual(got, want []string) bool {
	if len(got) < len(want) {
		return false
	}
	for i, w := range want {
		if got[i] != w {
			return false
		}
	}
	return true
}

func TestRun_CallsUserBootThenCounts(t *testing.T) {
	f := newFakeDeps()
	res, err := Run(context.Background(), f.Deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Order matters: counts is keyed by the conversations userBoot
	// returns, so calling it first would ask about channels we have
	// not learned about yet.
	want := []string{callUserBoot, callCounts}
	if !prefixEqual(f.calls, want) {
		t.Errorf("call sequence = %v; want it to start %v", f.calls, want)
	}
	if res == nil {
		t.Fatal("Run returned a nil Result with a nil error")
	}
	if res.Self.ID != "U_SELF" {
		t.Errorf("Result.Self.ID = %q; want U_SELF", res.Self.ID)
	}
}

func TestRun_NeverEnumerates(t *testing.T) {
	// The regression guard this whole package exists for. slk's
	// Enterprise Grid accounts get signed out for "data scraping",
	// and across 8 captures the official client issues ZERO
	// users.list, ZERO conversations.list, and zero per-channel
	// conversations.history at boot.
	f := newFakeDeps()
	if _, err := Run(context.Background(), f.Deps()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, forbidden := range []string{"users.list", "conversations.list", "users.conversations"} {
		if f.called(forbidden) {
			t.Errorf("boot called %s; the official client never does, and it is the signature that gets Grid users signed out (sequence: %v)", forbidden, f.calls)
		}
	}
	if n := f.countPrefix("conversations.history"); n > 1 {
		t.Errorf("boot made %d conversations.history calls; at most one (the opened channel's fallback) is allowed, never a per-channel fan-out (sequence: %v)", n, f.calls)
	}
}

func TestRun_BootCallBudget(t *testing.T) {
	// Success criterion 1: a boot issues <= 10 API calls. The fake
	// counts one per dependency invocation, which is the same unit
	// the slackhttp Counter measures.
	f := newFakeDeps()
	if _, err := Run(context.Background(), f.Deps()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.calls) > 10 {
		t.Errorf("boot issued %d calls; budget is 10 (sequence: %v)", len(f.calls), f.calls)
	}
}

func TestRun_UserBootFailureIsFatal(t *testing.T) {
	// Everything downstream is keyed by what userBoot returns. There
	// is no degraded mode worth having.
	f := newFakeDeps()
	f.userBootErr = errors.New("invalid_auth")
	if _, err := Run(context.Background(), f.Deps()); err == nil {
		t.Fatal("Run returned nil error when userBoot failed")
	}
}

func TestRun_UserBootFailureReturnsNoResult(t *testing.T) {
	// Same reasoning boot.UserBoot's own doc comment gives for
	// returning a nil *Result on every error path: a caller handed
	// both a Result and an error can use the Result, and a
	// half-populated workspace renders as a real one. The error must
	// be the only thing available.
	f := newFakeDeps()
	f.userBootErr = errors.New("invalid_auth")
	res, err := Run(context.Background(), f.Deps())
	if err == nil {
		t.Fatal("Run returned nil error when userBoot failed")
	}
	if res != nil {
		t.Errorf("Run returned a non-nil Result (%+v) alongside error %v; a caller can and will use it", res, err)
	}
}

func TestRun_UserBootErrorWrapsTheCause(t *testing.T) {
	// connectWorkspace distinguishes invalid_auth from every other
	// failure to decide whether to re-prompt for a token. A flattened
	// error string makes that impossible.
	f := newFakeDeps()
	cause := errors.New("invalid_auth")
	f.userBootErr = cause
	_, err := Run(context.Background(), f.Deps())
	if !errors.Is(err, cause) {
		t.Errorf("Run error = %v; want it to wrap %v", err, cause)
	}
}

func TestRun_CountsFailureIsNotFatal(t *testing.T) {
	// Unread badges are cosmetic; a workspace that boots without them
	// is far better than one that does not boot.
	f := newFakeDeps()
	f.countsErr = errors.New("ratelimited")
	res, err := Run(context.Background(), f.Deps())
	if err != nil {
		t.Fatalf("Run: counts failure should not be fatal, got %v", err)
	}
	if res == nil {
		t.Fatal("Run returned nil Result")
	}
}

func TestRun_CountsFailureDiscardsTheValue(t *testing.T) {
	// The value returned next to an error must not reach the Result.
	// Slack answers ok:false with a fully populated body, so "err !=
	// nil but the value looks fine" is the normal shape of a failure
	// here, not a corner case.
	f := newFakeDeps()
	f.countsErr = errors.New("ratelimited")
	res, err := Run(context.Background(), f.Deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(res.Counts, Counts{}) {
		t.Errorf("Result.Counts = %#v; want the zero value — counts failed and its return value must be discarded", res.Counts)
	}
}

func TestRun_MissingCountsDependencyIsAnError(t *testing.T) {
	// Same reasoning as the Boot check: a forgotten field in the Deps
	// literal is a nil interface, and calling through it panics.
	// Counts is documented required, so a nil one is a wiring bug to
	// report, not an unread-free workspace to render.
	f := newFakeDeps()
	f.deps.Counts = nil
	res, err := Run(context.Background(), f.Deps())
	if err == nil {
		t.Fatal("Run with no Counts dependency returned nil error")
	}
	if res != nil {
		t.Errorf("Run returned a non-nil Result (%+v) alongside error %v", res, err)
	}
}

func TestRun_CountsFailureIsLogged(t *testing.T) {
	// A silently swallowed counts failure is indistinguishable from a
	// workspace with nothing unread, which is the state an operator
	// would be debugging.
	f := newFakeDeps()
	f.countsErr = errors.New("ratelimited")
	if _, err := Run(context.Background(), f.Deps()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.logged()) == 0 {
		t.Error("counts failed and Run logged nothing")
	}
}

func TestRun_NilLogDoesNotPanic(t *testing.T) {
	// Deps.Log is documented optional. The only path that logs today
	// is the counts failure, so a missing nil-guard is invisible
	// until the day counts fails in production -- which is exactly
	// the day the process must not die.
	f := newFakeDeps()
	f.countsErr = errors.New("ratelimited")
	f.deps.Log = nil
	if _, err := Run(context.Background(), f.Deps()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_MissingBootDependencyIsAnError(t *testing.T) {
	// Deps is a struct of interfaces built at a call site in
	// cmd/slk/main.go, so a forgotten field is a nil interface and a
	// nil-interface method call is a panic that takes the whole TUI
	// down with a stack trace instead of a message.
	//
	// Every OTHER dependency is populated on purpose. An empty Deps{}
	// would leave Counts nil too, and the Counts guard alone would
	// satisfy this assertion — the test would pass with the Boot check
	// deleted, which is exactly what the mutation run caught it doing.
	f := newFakeDeps()
	f.deps.Boot = nil
	res, err := Run(context.Background(), f.Deps())
	if err == nil {
		t.Fatal("Run with no Boot dependency returned nil error")
	}
	if res != nil {
		t.Errorf("Run returned a non-nil Result (%+v) alongside error %v", res, err)
	}
}

func TestRun_CarriesEveryMappedFieldFromUserBoot(t *testing.T) {
	// Each of these is a field a mutant can drop or source from the
	// wrong place and still compile. The []string and string
	// neighbours in particular -- see cannedBootResult's comment.
	f := newFakeDeps()
	res, err := Run(context.Background(), f.Deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := cannedBootResult()

	for _, tc := range []struct {
		field string
		got   any
		want  any
	}{
		{"Self", res.Self, want.Self},
		{"Team", res.Team, want.Team},
		{"Channels", res.Channels, want.Channels},
		{"IMs", res.IMs, want.IMs},
		{"IsOpen", res.IsOpen, want.IsOpen},
		{"DND", res.DND, want.DND},
		{"ChannelsPriority", res.ChannelsPriority, want.ChannelsPriority},
		{"EmojiCacheTS", res.EmojiCacheTS, want.EmojiCacheTS},
		{"MutePrefsRaw", res.MutePrefsRaw, want.Prefs.AllNotificationsPrefs},
		{"LegacyMutedRaw", res.LegacyMutedRaw, want.Prefs.MutedChannels},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("Result.%s = %#v; want %#v", tc.field, tc.got, tc.want)
		}
	}
}

func TestRun_CarriesCountsOntoTheResult(t *testing.T) {
	// Fetching counts and then dropping them costs a request and
	// delivers nothing -- a failure mode that no other test in this
	// file can see, since they only assert counts was CALLED.
	f := newFakeDeps()
	res, err := Run(context.Background(), f.Deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(res.Counts, cannedCounts()) {
		t.Errorf("Result.Counts = %#v; want %#v", res.Counts, cannedCounts())
	}
}
