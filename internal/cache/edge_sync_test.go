package cache

import (
	"database/sql"
	"errors"
	"testing"
)

func openEdgeSyncTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedChannelFull writes a channel with every column non-zero, so any
// column a later partial update wrongly clears is visible.
func seedChannelFull(t *testing.T, db *DB, workspaceID, id string) {
	t.Helper()
	if err := db.UpsertChannel(Channel{
		ID: id, WorkspaceID: workspaceID, Name: "original-name",
		Type: "channel", Topic: "original topic",
		IsMember: true, IsStarred: true, UpdatedAt: 111,
	}); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
}

func getChannelRow(t *testing.T, db *DB, id string) Channel {
	t.Helper()
	ch, err := db.GetChannel(id)
	if err != nil {
		t.Fatalf("GetChannel(%s): %v", id, err)
	}
	return ch
}

func getUserRow(t *testing.T, db *DB, id string) User {
	t.Helper()
	u, err := db.GetUser(id)
	if err != nil {
		t.Fatalf("GetUser(%s): %v", id, err)
	}
	return u
}

func TestUpdateChannelFromEdge_PreservesColumnsEdgeCannotSupply(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	seedChannelFull(t, db, "T1", "C1")

	// What channels/info actually returns: name, type, topic, version.
	// NOT is_member (0 of 36 observed results carry it) and NOT
	// is_starred (no edge endpoint returns it).
	if err := db.UpdateChannelFromEdge(EdgeChannelUpdate{
		ID: "C1", Name: "new-name", Type: "private", Topic: "new topic",
		Version: sampleChannelVersion,
	}); err != nil {
		t.Fatalf("UpdateChannelFromEdge: %v", err)
	}

	got := getChannelRow(t, db, "C1")
	if got.Name != "new-name" || got.Topic != "new topic" || got.Type != "private" {
		t.Errorf("edge-owned columns not written: %+v", got)
	}
	// The whole point of this method existing.
	if !got.IsMember {
		t.Error("is_member was cleared; channels/info does not carry it, so it must be preserved — clearing it drops the user out of their own channels")
	}
	if !got.IsStarred {
		t.Error("is_starred was cleared; no edge endpoint returns it, so it must be preserved")
	}

	vers, err := db.ChannelVersions("T1")
	if err != nil {
		t.Fatalf("ChannelVersions: %v", err)
	}
	if vers["C1"] != sampleChannelVersion {
		t.Errorf("version = %d; want %d", vers["C1"], sampleChannelVersion)
	}
}

func TestApplyMembership_SetsAndClearsOnlyTheIDsQueried(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	for _, id := range []string{"C1", "C2", "C3"} {
		seedChannelFull(t, db, "T1", id)
	}

	// MemberChannels is a snapshot over the ids SENT, not a delta: an
	// id that was sent and is absent is a non-membership; an id never
	// sent says nothing. C3 was not queried and must be untouched.
	if err := db.ApplyMembership("T1", []string{"C1", "C2"}, []string{"C1"}); err != nil {
		t.Fatalf("ApplyMembership: %v", err)
	}

	if !getChannelRow(t, db, "C1").IsMember {
		t.Error("C1 was in member_channels and must stay a member")
	}
	if getChannelRow(t, db, "C2").IsMember {
		t.Error("C2 was queried and absent from member_channels, so it is a non-membership and must be cleared")
	}
	if !getChannelRow(t, db, "C3").IsMember {
		t.Error("C3 was never queried; ApplyMembership must not touch it — treating unqueried as non-member drops every channel not in the batch")
	}
	if !getChannelRow(t, db, "C2").IsStarred {
		t.Error("ApplyMembership must only write is_member")
	}
}

func TestUpdateUserFromEdge_PreservesColumnsEdgeCannotSupply(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	if err := db.UpsertUser(User{
		ID: "U1", WorkspaceID: "T1", Name: "orig", DisplayName: "Orig",
		AvatarURL: "https://example.invalid/orig.png", Presence: "active",
		IsBot: false, IsExternal: true, UpdatedAt: 222,
	}); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	// No AvatarURL supplied: users/info carries image_original on 255
	// of 291 observed results, but a user with no custom image has
	// none, and blanking a good URL on that basis is the bug.
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{
		ID: "U1", Name: "new", DisplayName: "New", IsBot: true,
		Version: sampleUserVersion,
	}); err != nil {
		t.Fatalf("UpdateUserFromEdge: %v", err)
	}

	u := getUserRow(t, db, "U1")
	if u.Name != "new" || u.DisplayName != "New" || !u.IsBot {
		t.Errorf("edge-owned columns not written: %+v", u)
	}
	if u.AvatarURL != "https://example.invalid/orig.png" {
		t.Errorf("avatar_url = %q; want the original preserved — an empty AvatarURL means 'this source has none', not 'this user has none'", u.AvatarURL)
	}
	if u.Presence != "active" {
		t.Errorf("presence = %q; want active preserved — no edge endpoint returns presence", u.Presence)
	}
	// is_external IS edge-owned: it is derived from the team_id every
	// edge result carries, so a revalidation that stops writing it
	// leaves a guest flagged after they join, or vice versa.
	if u.IsExternal {
		t.Error("is_external = true; want the update's false written — is_external is derived from team_id, which every edge result carries")
	}
	vers, err := db.UserVersions("T1")
	if err != nil {
		t.Fatalf("UserVersions: %v", err)
	}
	if vers["U1"] != sampleUserVersion {
		t.Errorf("version = %d; want %d — an unstamped row is sent as 0 in updated_ids and refetched in full on every boot, forever", vers["U1"], sampleUserVersion)
	}
}

// The avatar-carrying branch is a second SQL statement, so every column
// assertion made against the other branch has to be made here too or a
// column silently dropped from one of them goes unnoticed.
func TestUpdateUserFromEdge_AvatarBranchWritesTheSameColumns(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	if err := db.UpsertUser(User{
		ID: "U1", WorkspaceID: "T1", Name: "orig", DisplayName: "Orig",
		AvatarURL: "https://example.invalid/orig.png", Presence: "active",
		IsBot: false, IsExternal: true, UpdatedAt: 222,
	}); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{
		ID: "U1", Name: "new", DisplayName: "New",
		AvatarURL: "https://example.invalid/new.png",
		IsBot:     true, IsExternal: false, Version: sampleUserVersion,
	}); err != nil {
		t.Fatalf("UpdateUserFromEdge: %v", err)
	}

	u := getUserRow(t, db, "U1")
	if u.Name != "new" || u.DisplayName != "New" || !u.IsBot {
		t.Errorf("edge-owned columns not written: %+v", u)
	}
	if u.IsExternal {
		t.Error("is_external not written by the avatar branch")
	}
	if u.Presence != "active" {
		t.Errorf("presence = %q; the avatar branch must preserve it too", u.Presence)
	}
	vers, err := db.UserVersions("T1")
	if err != nil {
		t.Fatalf("UserVersions: %v", err)
	}
	if vers["U1"] != sampleUserVersion {
		t.Errorf("version = %d; want %d — the avatar branch must stamp it too", vers["U1"], sampleUserVersion)
	}
}

func TestUpdateUserFromEdge_WritesAvatarWhenSupplied(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	if err := db.UpsertUser(User{
		ID: "U1", WorkspaceID: "T1", Name: "orig",
		AvatarURL: "https://example.invalid/orig.png", Presence: "active",
	}); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{
		ID: "U1", Name: "orig", AvatarURL: "https://example.invalid/new.png",
	}); err != nil {
		t.Fatalf("UpdateUserFromEdge: %v", err)
	}
	u := getUserRow(t, db, "U1")
	if u.AvatarURL != "https://example.invalid/new.png" {
		t.Errorf("avatar_url = %q; want the new one — preserve must not mean ignore", u.AvatarURL)
	}
}

func TestUpdateFromEdge_UnknownRowIsANoOpNotAnInsert(t *testing.T) {
	// These are revalidation writers. A row we have never seen is
	// hydrated through the normal Upsert path, which knows every
	// column; inserting a half-populated row here would create a user
	// with no avatar and a channel with is_member=false.
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")

	if err := db.UpdateChannelFromEdge(EdgeChannelUpdate{ID: "CNOPE", Name: "x"}); err != nil {
		t.Fatalf("UpdateChannelFromEdge on a missing row: %v", err)
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{ID: "UNOPE", Name: "x"}); err != nil {
		t.Fatalf("UpdateUserFromEdge on a missing row: %v", err)
	}
	if _, err := db.GetChannel("CNOPE"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetChannel(CNOPE) err = %v; want sql.ErrNoRows — UpdateChannelFromEdge inserted a row, it must only update", err)
	}
	if _, err := db.GetUser("UNOPE"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetUser(UNOPE) err = %v; want sql.ErrNoRows — UpdateUserFromEdge inserted a row, it must only update", err)
	}
}

// A revalidation pass calls these once per changed id. An UPDATE that
// lost its WHERE clause would rewrite every channel in the cache with
// one channel's name, type, topic and version — and because the values
// written are all valid, nothing downstream would notice.
func TestUpdateChannelFromEdge_TouchesOnlyTheTargetRow(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	seedChannelFull(t, db, "T1", "C1")
	seedChannelFull(t, db, "T1", "C2")
	if err := db.SetChannelVersion("C2", 42); err != nil {
		t.Fatalf("SetChannelVersion: %v", err)
	}

	if err := db.UpdateChannelFromEdge(EdgeChannelUpdate{
		ID: "C1", Name: "new-name", Type: "private", Topic: "new topic",
		Version: sampleChannelVersion,
	}); err != nil {
		t.Fatalf("UpdateChannelFromEdge: %v", err)
	}

	other := getChannelRow(t, db, "C2")
	if other.Name != "original-name" || other.Type != "channel" || other.Topic != "original topic" {
		t.Errorf("C2 was rewritten by an update aimed at C1: %+v", other)
	}
	vers, err := db.ChannelVersions("T1")
	if err != nil {
		t.Fatalf("ChannelVersions: %v", err)
	}
	if vers["C2"] != 42 {
		t.Errorf("C2 version = %d; want 42 — an update aimed at C1 restamped it", vers["C2"])
	}
}

func TestUpdateUserFromEdge_TouchesOnlyTheTargetRow(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	for _, id := range []string{"U1", "U2"} {
		if err := db.UpsertUser(User{
			ID: id, WorkspaceID: "T1", Name: "orig-" + id, DisplayName: "Orig",
			AvatarURL: "https://example.invalid/" + id + ".png", Presence: "active",
		}); err != nil {
			t.Fatalf("UpsertUser(%s): %v", id, err)
		}
	}
	if err := db.SetUserVersion("U2", 42); err != nil {
		t.Fatalf("SetUserVersion: %v", err)
	}

	if err := db.UpdateUserFromEdge(EdgeUserUpdate{
		ID: "U1", Name: "new", DisplayName: "New", Version: sampleUserVersion,
	}); err != nil {
		t.Fatalf("UpdateUserFromEdge: %v", err)
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{
		ID: "U1", Name: "new", DisplayName: "New",
		AvatarURL: "https://example.invalid/new.png", Version: sampleUserVersion,
	}); err != nil {
		t.Fatalf("UpdateUserFromEdge (avatar branch): %v", err)
	}

	other := getUserRow(t, db, "U2")
	if other.Name != "orig-U2" || other.AvatarURL != "https://example.invalid/U2.png" {
		t.Errorf("U2 was rewritten by an update aimed at U1: %+v", other)
	}
	vers, err := db.UserVersions("T1")
	if err != nil {
		t.Fatalf("UserVersions: %v", err)
	}
	if vers["U2"] != 42 {
		t.Errorf("U2 version = %d; want 42 — an update aimed at U1 restamped it", vers["U2"])
	}
}

// An id is only meaningful inside its workspace. Grid users have many,
// and dropping the workspace scope makes one workspace's membership
// snapshot rewrite another's.
func TestApplyMembership_ScopedToWorkspace(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	seedWorkspace(t, db, "T2")
	seedChannelFull(t, db, "T1", "C1")
	seedChannelFull(t, db, "T2", "C2")

	// C2 belongs to T2. Naming it in a T1 batch must not clear it.
	if err := db.ApplyMembership("T1", []string{"C1", "C2"}, nil); err != nil {
		t.Fatalf("ApplyMembership: %v", err)
	}

	if getChannelRow(t, db, "C1").IsMember {
		t.Error("C1 is in T1, was queried and absent from member_channels; it must be cleared")
	}
	if !getChannelRow(t, db, "C2").IsMember {
		t.Error("C2 is in T2; a T1 membership snapshot must not reach across workspaces")
	}
}

// An absent or empty member_channels is a real answer, not a missing
// one: it means none of the ids asked about are joined. Treating it as
// "say nothing" means leaving every channel the user left still marked
// as joined, which is the same class of bug as the reverse.
func TestApplyMembership_EmptyMemberListClearsEveryQueriedID(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	seedChannelFull(t, db, "T1", "C1")
	seedChannelFull(t, db, "T1", "C2")

	if err := db.ApplyMembership("T1", []string{"C1", "C2"}, nil); err != nil {
		t.Fatalf("ApplyMembership: %v", err)
	}

	for _, id := range []string{"C1", "C2"} {
		if getChannelRow(t, db, id).IsMember {
			t.Errorf("%s was queried and member_channels was empty, so it is a non-membership and must be cleared", id)
		}
	}
}

// These writers are called in a loop over a revalidation batch. A
// swallowed error turns a dead database into a silently stale cache
// that no caller can detect.
func TestEdgeWriters_PropagateDatabaseErrors(t *testing.T) {
	db := openEdgeSyncTestDB(t)
	seedWorkspace(t, db, "T1")
	seedChannelFull(t, db, "T1", "C1")
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := db.UpdateChannelFromEdge(EdgeChannelUpdate{ID: "C1", Name: "x"}); err == nil {
		t.Error("UpdateChannelFromEdge on a closed database returned nil")
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{ID: "U1", Name: "x"}); err == nil {
		t.Error("UpdateUserFromEdge on a closed database returned nil")
	}
	if err := db.UpdateUserFromEdge(EdgeUserUpdate{ID: "U1", Name: "x", AvatarURL: "y"}); err == nil {
		t.Error("UpdateUserFromEdge (avatar branch) on a closed database returned nil")
	}
	if err := db.ApplyMembership("T1", []string{"C1"}, nil); err == nil {
		t.Error("ApplyMembership on a closed database returned nil")
	}
}
