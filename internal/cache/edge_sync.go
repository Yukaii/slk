package cache

import "fmt"

// EdgeChannelUpdate is what edgeapi's channels/info can tell us about a
// channel — and nothing more.
//
// Deliberately not cache.Channel. The full struct has fields no edge
// response carries (IsMember, IsStarred), and a caller filling those
// with zero values and handing them to UpsertChannel is exactly the
// silent data loss this type exists to make impossible. If a field is
// absent here, the column it maps to is preserved.
type EdgeChannelUpdate struct {
	ID      string
	Name    string
	Type    string
	Topic   string
	Version int64
}

// UpdateChannelFromEdge applies a revalidation result, touching only
// the columns channels/info actually populates.
//
// is_member is NOT among them: 0 of 36 observed channels/info results
// carried it, because membership comes back as the response's
// top-level member_channels array instead. Use ApplyMembership for
// that. is_starred, last_read_ts, unread_count and has_unread come
// from other sources entirely and are likewise preserved.
//
// A row that does not exist is left alone rather than inserted: this
// is a revalidation writer, and an unknown channel must go through
// UpsertChannel, which knows every column.
func (db *DB) UpdateChannelFromEdge(u EdgeChannelUpdate) error {
	_, err := db.conn.Exec(`
		UPDATE channels
		SET name = ?, type = ?, topic = ?, version = ?
		WHERE id = ?`,
		u.Name, u.Type, u.Topic, u.Version, u.ID)
	if err != nil {
		return fmt.Errorf("updating channel %s from edge: %w", u.ID, err)
	}
	return nil
}

// ApplyMembership records the membership snapshot from a channels/info
// response.
//
// queriedIDs is every id the request sent; memberIDs is the subset the
// response listed in member_channels. An id in queriedIDs but not
// memberIDs is a genuine non-membership and is cleared. An id in
// NEITHER is untouched — member_channels is a snapshot over what was
// asked, not a workspace-wide list, so treating unqueried ids as
// non-members would drop the user out of every channel outside the
// current batch.
func (db *DB) ApplyMembership(workspaceID string, queriedIDs, memberIDs []string) error {
	if len(queriedIDs) == 0 {
		return nil
	}
	members := make(map[string]bool, len(memberIDs))
	for _, id := range memberIDs {
		members[id] = true
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("applying membership: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`UPDATE channels SET is_member = ? WHERE id = ? AND workspace_id = ?`)
	if err != nil {
		return fmt.Errorf("applying membership: %w", err)
	}
	defer stmt.Close()

	for _, id := range queriedIDs {
		if _, err := stmt.Exec(boolToInt(members[id]), id, workspaceID); err != nil {
			return fmt.Errorf("applying membership for %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// EdgeUserUpdate is what edgeapi's users/info and users/search can tell
// us about a user. Same contract as EdgeChannelUpdate: absent field
// means "preserve the column", never "clear it".
type EdgeUserUpdate struct {
	ID          string
	Name        string
	DisplayName string
	// AvatarURL is empty when the source had none — users/info carries
	// image_original on 255 of 291 observed results, and the users
	// without it are the ones with no custom image. Empty therefore
	// means "this response says nothing", so the column is preserved.
	AvatarURL  string
	IsBot      bool
	IsExternal bool
	Version    int64
}

// UpdateUserFromEdge applies a revalidation result, touching only the
// columns an edge response populates. presence is never among them.
//
// avatar_url is written only when non-empty, for the reason on the
// field: blanking a good URL because this particular user has no
// custom image is the failure this whole file exists to prevent.
func (db *DB) UpdateUserFromEdge(u EdgeUserUpdate) error {
	var err error
	if u.AvatarURL != "" {
		_, err = db.conn.Exec(`
			UPDATE users
			SET name = ?, display_name = ?, avatar_url = ?, is_bot = ?, is_external = ?, version = ?
			WHERE id = ?`,
			u.Name, u.DisplayName, u.AvatarURL, boolToInt(u.IsBot), boolToInt(u.IsExternal), u.Version, u.ID)
	} else {
		_, err = db.conn.Exec(`
			UPDATE users
			SET name = ?, display_name = ?, is_bot = ?, is_external = ?, version = ?
			WHERE id = ?`,
			u.Name, u.DisplayName, boolToInt(u.IsBot), boolToInt(u.IsExternal), u.Version, u.ID)
	}
	if err != nil {
		return fmt.Errorf("updating user %s from edge: %w", u.ID, err)
	}
	return nil
}
