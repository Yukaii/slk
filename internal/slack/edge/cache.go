package edge

import (
	"context"
	"maps"
)

// Batch sizes for the conditional-revalidation endpoints.
//
// These are not documented limits; they are the shapes the official
// web client has been observed sending, measured across 8 HAR captures
// of a live Grid workspace:
//
//	channels/info   18 requests, 1–63 ids per request
//	users/info      30 requests, 1–80 ids per request
//
// Neither distribution shows a client-side cap — batch size just
// tracks how many ids needed checking — so these are chosen at or just
// under the observed maximum. Larger is better for our purposes:
// revalidating a whole workspace in a handful of large requests looks
// like a client warming its cache, while the same ids dribbled out in
// small batches looks like enumeration, which is what we are trying to
// stop doing.
const (
	channelsInfoBatchSize = 60
	usersInfoBatchSize    = 80
)

// Channel is one entry in a channels/info response.
//
// This deliberately models a subset. A real result carries ~45 fields
// (enterprise_id, shared_team_ids, properties{}, channel_agent_status,
// …); decoding ignores the rest, and must keep doing so — Slack adds
// fields to this response without notice.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Version is the `updated` stamp, the value that goes back out in
	// updated_ids on the next revalidation. Channels stamp it in
	// milliseconds (1783337533019), users in seconds; to us it is an
	// opaque monotonic version, never a timestamp.
	Version    int64 `json:"updated"`
	IsChannel  bool  `json:"is_channel"`
	IsGroup    bool  `json:"is_group"`
	IsIM       bool  `json:"is_im"`
	IsMPIM     bool  `json:"is_mpim"`
	IsPrivate  bool  `json:"is_private"`
	IsArchived bool  `json:"is_archived"`
	// IsMember is UNVERIFIED against the captures. check_membership:true
	// is sent precisely to get membership back, but the one fully
	// hydrated channels/info result in
	// internal/slack/testdata/phase2-api-contracts.json does not list
	// is_member (only 3 of the 18 observed requests are sampled there,
	// and only one has a non-empty results array). Treat a false here
	// as "unknown" until a capture confirms the field, and do not add a
	// test asserting it as though it were an observed contract.
	IsMember    bool   `json:"is_member"`
	ContextTeam string `json:"context_team_id"`
	Topic       struct {
		Value string `json:"value"`
	} `json:"topic"`
}

// User is one entry in a users/info response.
//
// Also a deliberate subset. Note what is absent: the observed profile
// object in this response carries avatar_hash, not image_32 — there is
// no image URL anywhere in a users/info profile, so a field for one
// would decode empty forever and quietly hand callers a blank avatar.
// If Phase 2b needs avatars it should add AvatarHash and derive the
// URL, with a capture to back it.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"updated"`
	Deleted bool   `json:"deleted"`
	IsBot   bool   `json:"is_bot"`
	TeamID  string `json:"team_id"`
	Profile struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
	} `json:"profile"`
}

// ChannelsInfo revalidates channels against the edge cache.
//
// updatedIDs maps channel id to the version last seen (0 for a channel
// we have never seen). The response contains only the entries whose
// version moved plus the unknown ones, so a fully current cache costs
// one 24-byte response per batch instead of a full conversations.list
// walk. An empty or nil map makes no request at all.
func (c *Client) ChannelsInfo(ctx context.Context, updatedIDs map[string]int64) ([]Channel, error) {
	return fetchInfo[Channel](ctx, c, "channels/info", map[string]any{
		"check_membership": true,
	}, updatedIDs, channelsInfoBatchSize)
}

// UsersInfo revalidates users against the edge cache, with the same
// conditional semantics as ChannelsInfo.
//
// The response also carries a top-level can_interact object — a
// map[string]bool keyed by user id, produced by check_interaction:true
// — which this package deliberately does not model. Nothing in the
// client consumes it; it is per-batch, so exposing it would mean
// merging maps across batches and widening this signature for data no
// caller wants. Adding it later is a two-line change plus a merge.
func (c *Client) UsersInfo(ctx context.Context, updatedIDs map[string]int64) ([]User, error) {
	return fetchInfo[User](ctx, c, "users/info", map[string]any{
		"check_interaction":          true,
		"include_profile_only_users": true,
	}, updatedIDs, usersInfoBatchSize)
}

// fetchInfo posts updatedIDs to a cache endpoint in batches of at most
// batchSize and concatenates the results.
//
// This is a generic free function rather than a method because Go
// methods cannot take type parameters, and rather than a shared helper
// that inspects the endpoint name to decide what to decode into: the
// only thing that differs between channels/info and users/info is the
// result element type and the flag payload, and both are things the
// caller already knows. Passing the type in makes that explicit and
// keeps a new endpoint from having to be added to a string switch.
//
// A failed batch fails the whole call and discards what already
// succeeded. Returning partial results with a nil error would be
// indistinguishable from "only these entries changed", so a caller
// would mark the unfetched ids current and never revalidate them again.
func fetchInfo[T any](
	ctx context.Context,
	c *Client,
	endpoint string,
	flags map[string]any,
	updatedIDs map[string]int64,
	batchSize int,
) ([]T, error) {
	var out []T
	batch := make(map[string]int64, min(batchSize, len(updatedIDs)))

	flush := func() error {
		payload := make(map[string]any, len(flags)+1)
		maps.Copy(payload, flags)
		payload["updated_ids"] = batch

		var resp struct {
			Results []T `json:"results"`
		}
		if err := c.call(ctx, endpoint, payload, &resp); err != nil {
			return err
		}
		out = append(out, resp.Results...)
		// A fresh map rather than clear(): payload still references
		// this one. clear() happens to be safe today only because
		// call marshals the body synchronously before returning — no
		// test can tell the two apart, which is the point. Allocating
		// removes the dependency on that timing.
		batch = make(map[string]int64, min(batchSize, len(updatedIDs)))
		return nil
	}

	for id, version := range updatedIDs {
		batch[id] = version
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	// The trailing partial batch. This guard is the only thing
	// implementing two behaviours, so do not "simplify" it away: an id
	// count that is an exact multiple of batchSize must not send a
	// trailing empty batch, and an empty or nil updatedIDs must send
	// nothing at all. An updated_ids-less revalidation request is a
	// round trip that can only return nothing, and a stream of them is
	// a shape the official client never produces.
	//
	// (An early `if len(updatedIDs) == 0 { return }` at the top of this
	// function was removed: ranging a nil/empty map already falls
	// through to here, so it was unreachable-in-effect — no mutation of
	// it could fail a test.)
	if len(batch) > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}

	return out, nil
}
