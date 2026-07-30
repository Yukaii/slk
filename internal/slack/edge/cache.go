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
//
// There is deliberately no IsMember field. An earlier draft asserted
// one; the captures disprove it. Across 8 HAR captures — 18
// channels/info requests, 7 responses carrying results, 36 result
// objects — `is_member` appears in 0 of 36, while the other 43 fields
// appear in 36 of 36. Membership does not travel on the result at all;
// it travels on the top-level member_channels array (see
// ChannelsInfoResult.MemberChannels). A bool field here would decode
// false forever and read as "not a member".
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Version is the `updated` stamp, the value that goes back out in
	// updated_ids on the next revalidation. Channels stamp it in
	// milliseconds (1783337533019), users in seconds; to us it is an
	// opaque monotonic version, never a timestamp.
	Version     int64  `json:"updated"`
	IsChannel   bool   `json:"is_channel"`
	IsGroup     bool   `json:"is_group"`
	IsIM        bool   `json:"is_im"`
	IsMPIM      bool   `json:"is_mpim"`
	IsPrivate   bool   `json:"is_private"`
	IsArchived  bool   `json:"is_archived"`
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

// ChannelsInfoResult is everything one ChannelsInfo call learned,
// accumulated across all of its batches.
//
// This is a struct rather than three return values because the three
// outputs are independent, accumulate separately, and are easy to
// transpose at a call site if they were positional; a fourth top-level
// key is also plausible, since Slack has already added two beyond
// `results`.
type ChannelsInfoResult struct {
	// Channels holds only the channels whose version moved, plus the
	// ones sent with version 0. Empty here is the normal case, not a
	// sign the call learned nothing: all 5 observed responses that
	// carried membership also had `"results":[]`.
	Channels []Channel
	// MemberChannels holds the ids the authenticated user belongs to.
	// This is what check_membership:true buys, and it is the reason
	// this endpoint can replace a conversations.list walk: membership
	// comes back regardless of whether any channel record changed —
	// all 5 observed responses carrying it had `"results":[]` — so it
	// is learned without ever enumerating.
	//
	// It is a snapshot over the ids this call sent, not a delta. An id
	// that was sent and is absent here is one the user is not a member
	// of; an id that was never sent says nothing either way.
	MemberChannels []string
	// FailedIDs holds the ids the server could not resolve. Ignoring
	// this is a correctness hazard rather than a lost nicety: absence
	// from Channels otherwise means "unchanged, still fresh", so a
	// failed id would be marked current and its stale record kept
	// forever, because its version never advances. A caller should
	// retry these or leave their rows explicitly stale.
	FailedIDs []string
}

// channelsInfoResponse is one channels/info batch on the wire.
//
// member_channels and failed_ids are each absent from most responses
// (observed 5/18 and 4/18 respectively). Absence decodes to a nil
// slice and means empty, never an error.
type channelsInfoResponse struct {
	Results        []Channel `json:"results"`
	MemberChannels []string  `json:"member_channels"`
	FailedIDs      []string  `json:"failed_ids"`
}

// ChannelsInfo revalidates channels against the edge cache.
//
// updatedIDs maps channel id to the version last seen (0 for a channel
// we have never seen). Only the entries whose version moved, plus the
// unknown ones, come back in Channels, so a fully current cache costs
// one small response per batch instead of a full conversations.list
// walk. An empty or nil map makes no request at all.
//
// check_membership:true does not add a field to those results. It adds
// the top-level member_channels array, returned for every id in the
// batch whether or not that channel changed — see
// ChannelsInfoResult.MemberChannels.
func (c *Client) ChannelsInfo(ctx context.Context, updatedIDs map[string]int64) (ChannelsInfoResult, error) {
	var out ChannelsInfoResult
	err := fetchInfo(ctx, c, "channels/info", map[string]any{
		"check_membership": true,
	}, updatedIDs, channelsInfoBatchSize, func(batch channelsInfoResponse) {
		out.Channels = append(out.Channels, batch.Results...)
		out.MemberChannels = append(out.MemberChannels, batch.MemberChannels...)
		out.FailedIDs = append(out.FailedIDs, batch.FailedIDs...)
	})
	if err != nil {
		return ChannelsInfoResult{}, err
	}
	return out, nil
}

// usersInfoResponse is one users/info batch on the wire.
//
// There is no membership analogue here. Across 30 observed users/info
// responses the only top-level keys are results (30/30), ok (30/30)
// and can_interact (12/30) — no member_channels, no failed_ids.
type usersInfoResponse struct {
	Results []User `json:"results"`
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
	var out []User
	err := fetchInfo(ctx, c, "users/info", map[string]any{
		"check_interaction":          true,
		"include_profile_only_users": true,
	}, updatedIDs, usersInfoBatchSize, func(batch usersInfoResponse) {
		out = append(out, batch.Results...)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// fetchInfo posts updatedIDs to a cache endpoint in batches of at most
// batchSize, decoding each batch into Resp and handing it to merge.
//
// It is generic over the whole per-batch response rather than over the
// result element type, which is the change the captures forced: the
// two endpoints no longer share a response shape. channels/info
// carries member_channels and failed_ids alongside results;
// users/info carries neither and carries can_interact instead. A
// helper parameterised on the element type could only have grown a
// second return value that users/info always left empty, or the
// `endpoint == "channels/info"` string comparison that was rejected
// outright.
//
// Splitting this into two functions was the alternative and was
// rejected: what the endpoints still share is exactly the part that is
// easy to get subtly wrong and expensive to get wrong twice — the
// trailing-partial-batch guard, never sending an empty batch, a fresh
// batch map per flush, and abort-on-first-error. What differs is
// decoding and accumulation, which is now entirely in the caller's
// merge closure where it is plain to read. So the generic earns its
// place, just on a different axis than before.
//
// merge is called once per successful batch, in request order, and
// never after an error. A failed batch fails the whole call and the
// caller discards what already merged: returning partial results with
// a nil error would be indistinguishable from "only these entries
// changed", so a caller would mark the unfetched ids current and never
// revalidate them again.
func fetchInfo[Resp any](
	ctx context.Context,
	c *Client,
	endpoint string,
	flags map[string]any,
	updatedIDs map[string]int64,
	batchSize int,
	merge func(Resp),
) error {
	batch := make(map[string]int64, min(batchSize, len(updatedIDs)))

	flush := func() error {
		payload := make(map[string]any, len(flags)+1)
		maps.Copy(payload, flags)
		payload["updated_ids"] = batch

		var resp Resp
		if err := c.call(ctx, endpoint, payload, &resp); err != nil {
			return err
		}
		merge(resp)
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
				return err
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
			return err
		}
	}

	return nil
}
