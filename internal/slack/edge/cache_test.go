package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// capturedRequest is one request the recorder saw, kept as raw bytes so
// each test can decode it in whatever shape it wants to assert on.
type capturedRequest struct {
	path string
	raw  []byte
}

func (r capturedRequest) generic(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.raw, &m); err != nil {
		t.Fatalf("request body is not JSON: %v (%q)", err, r.raw)
	}
	return m
}

func (r capturedRequest) updatedIDs(t *testing.T) map[string]int64 {
	t.Helper()
	var m struct {
		UpdatedIDs map[string]int64 `json:"updated_ids"`
	}
	if err := json.Unmarshal(r.raw, &m); err != nil {
		t.Fatalf("request body is not JSON: %v (%q)", err, r.raw)
	}
	return m.UpdatedIDs
}

func (r capturedRequest) keys(t *testing.T) []string {
	t.Helper()
	var ks []string
	for k := range r.generic(t) {
		ks = append(ks, k)
	}
	return ks
}

// recorder is an httptest server that records every request body and
// answers from a per-request-number reply function.
type recorder struct {
	mu   sync.Mutex
	reqs []capturedRequest
	srv  *httptest.Server
}

// newRecorder starts a server whose reply is chosen by the 1-based
// index of the request. Returning a status other than 200 lets a test
// fail a specific batch.
func newRecorder(t *testing.T, reply func(n int) (int, string)) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, capturedRequest{path: r.URL.Path, raw: raw})
		n := len(rec.reqs)
		rec.mu.Unlock()

		status, body := reply(n)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (rec *recorder) client() *Client {
	c := New("xoxc-test", "T04T4TH8W", rec.srv.Client())
	c.baseURL = rec.srv.URL
	return c
}

func (rec *recorder) requests() []capturedRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([]capturedRequest, len(rec.reqs))
	copy(out, rec.reqs)
	return out
}

// alwaysEmpty is the unchanged-batch reply: the literal 24-byte body
// edgeapi returns when nothing in the batch has changed.
func alwaysEmpty(int) (int, string) { return 200, `{"results":[],"ok":true}` }

func ids(prefix string, n int) map[string]int64 {
	m := make(map[string]int64, n)
	for i := range n {
		m[fmt.Sprintf("%s%05d", prefix, i)] = int64(i)
	}
	return m
}

// ---------------------------------------------------------------- channels

func TestChannelsInfo_SendsUpdatedIDsAndDecodesResults(t *testing.T) {
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"ok":true,"results":[
			{"id":"C2QPK1V44","name":"general","updated":1783337533019,
			 "is_channel":true,"is_group":false,"is_im":false,"is_mpim":false,
			 "is_private":false,"is_archived":false,
			 "context_team_id":"T04T4TH8W",
			 "topic":{"creator":"U1","last_set":123,"value":"stand-ups here"}}
		],"member_channels":["C2QPK1V44"]}`
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{
		"C2QPK1V44":   1783337533019,
		"C092E63RUUC": 0,
	})
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("made %d requests; want 1", len(reqs))
	}
	if reqs[0].path != "/cache/T04T4TH8W/channels/info" {
		t.Errorf("path = %q; want /cache/T04T4TH8W/channels/info", reqs[0].path)
	}

	// The captured request carries exactly three keys. An extra key is
	// as much a divergence from the official client as a missing one —
	// this is the whole point of the package.
	body := reqs[0].generic(t)
	if len(body) != 3 {
		t.Errorf("request keys = %v; want exactly token, check_membership, updated_ids", reqs[0].keys(t))
	}
	if body["check_membership"] != true {
		t.Errorf("check_membership = %v; want true", body["check_membership"])
	}
	if _, ok := body["check_interaction"]; ok {
		t.Errorf("channels/info sent check_interaction; that flag belongs to users/info: %v", reqs[0].keys(t))
	}
	if body["token"] != "xoxc-test" {
		t.Errorf("token = %v; want xoxc-test", body["token"])
	}

	sent := reqs[0].updatedIDs(t)
	if len(sent) != 2 || sent["C2QPK1V44"] != 1783337533019 || sent["C092E63RUUC"] != 0 {
		t.Errorf("updated_ids = %v; want the {id: version} map verbatim, version 0 included", sent)
	}

	if len(got.Channels) != 1 {
		t.Fatalf("got %d channels; want 1", len(got.Channels))
	}
	ch := got.Channels[0]
	if ch.ID != "C2QPK1V44" {
		t.Errorf("ID = %q; want C2QPK1V44", ch.ID)
	}
	if ch.Name != "general" {
		t.Errorf("Name = %q; want general", ch.Name)
	}
	// The version stamp arrives as "updated", not "version". Getting
	// this wrong makes every channel look permanently stale and
	// reintroduces full enumeration.
	if ch.Version != 1783337533019 {
		t.Errorf("Version = %d; want 1783337533019 (from the `updated` field)", ch.Version)
	}
	if !ch.IsChannel {
		t.Error("IsChannel = false; want true")
	}
	if ch.IsGroup || ch.IsIM || ch.IsMPIM || ch.IsPrivate || ch.IsArchived {
		t.Errorf("false flags decoded true: %+v", ch)
	}
	if ch.ContextTeam != "T04T4TH8W" {
		t.Errorf("ContextTeam = %q; want T04T4TH8W", ch.ContextTeam)
	}
	if ch.Topic.Value != "stand-ups here" {
		t.Errorf("Topic.Value = %q; want %q", ch.Topic.Value, "stand-ups here")
	}

	if len(got.MemberChannels) != 1 || got.MemberChannels[0] != "C2QPK1V44" {
		t.Errorf("MemberChannels = %v; want [C2QPK1V44] — this array is what "+
			"check_membership:true actually returns", got.MemberChannels)
	}
	if len(got.FailedIDs) != 0 {
		t.Errorf("FailedIDs = %v; want empty when the response omits failed_ids", got.FailedIDs)
	}
}

// TestChannel_HasNoIsMemberField pins the finding that motivated
// removing it. Across 8 HAR captures — 18 channels/info requests, 7
// responses with results, 36 result objects — `is_member` appears 0
// times, while the other 43 fields appear 36/36. A struct field for it
// would decode false forever and read as "not a member" for every
// channel the user is in.
//
// If the server ever does start sending it, a field is still the wrong
// answer: membership is carried by member_channels, and two sources of
// truth for it would eventually disagree.
func TestChannel_HasNoIsMemberField(t *testing.T) {
	typ := reflect.TypeFor[Channel]()
	if _, ok := typ.FieldByName("IsMember"); ok {
		t.Error("Channel has an IsMember field; the captures show is_member on 0 of 36 " +
			"observed result objects — membership arrives in the top-level member_channels")
	}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Tag.Get("json") == "is_member" {
			t.Errorf("field %s is tagged json:%q; that key does not exist on a "+
				"channels/info result", f.Name, "is_member")
		}
	}
}

// TestChannelsInfo_MembershipArrivesWithNoResults is the common
// real-world shape, not an edge case: 5 of the 6 observed responses
// carrying member_channels had `"results":[]`. Membership comes back
// whether or not any channel record changed, which is exactly what
// lets us stop enumerating.
func TestChannelsInfo_MembershipArrivesWithNoResults(t *testing.T) {
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"results":[],"ok":true,"member_channels":["C2QPK1V44","CL0AET1L0"]}`
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{
		"C2QPK1V44":   1,
		"CL0AET1L0":   2,
		"C092E63RUUC": 3,
	})
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	if len(got.Channels) != 0 {
		t.Errorf("Channels = %+v; want empty", got.Channels)
	}
	if len(got.MemberChannels) != 2 {
		t.Fatalf("MemberChannels = %v; want 2 ids — membership is returned even when "+
			"results is empty, and dropping it forces enumeration", got.MemberChannels)
	}
	seen := map[string]bool{}
	for _, id := range got.MemberChannels {
		seen[id] = true
	}
	if !seen["C2QPK1V44"] || !seen["CL0AET1L0"] {
		t.Errorf("MemberChannels = %v; want C2QPK1V44 and CL0AET1L0", got.MemberChannels)
	}
	// An id sent but absent from member_channels is a non-membership,
	// not missing data.
	if seen["C092E63RUUC"] {
		t.Errorf("MemberChannels = %v; C092E63RUUC was not in the response", got.MemberChannels)
	}
}

// TestChannelsInfo_SurfacesFailedIDs covers the correctness hazard: an
// id the server could not resolve is absent from results, exactly like
// an unchanged one. Without failed_ids the caller marks it fresh and
// keeps a stale record forever, because its version never advances.
func TestChannelsInfo_SurfacesFailedIDs(t *testing.T) {
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"results":[],"ok":true,
			"member_channels":["C2QPK1V44"],
			"failed_ids":["C092E63RUUCX","C0B0QD6BH1N"]}`
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{
		"C2QPK1V44":    1,
		"C092E63RUUCX": 2,
		"C0B0QD6BH1N":  3,
	})
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	// failed_ids is not an error: the rest of the batch succeeded.
	if len(got.FailedIDs) != 2 {
		t.Fatalf("FailedIDs = %v; want 2 — a failed id is indistinguishable from an "+
			"unchanged one unless it is surfaced", got.FailedIDs)
	}
	seen := map[string]bool{}
	for _, id := range got.FailedIDs {
		seen[id] = true
	}
	if !seen["C092E63RUUCX"] || !seen["C0B0QD6BH1N"] {
		t.Errorf("FailedIDs = %v; want C092E63RUUCX and C0B0QD6BH1N", got.FailedIDs)
	}
	if len(got.MemberChannels) != 1 || got.MemberChannels[0] != "C2QPK1V44" {
		t.Errorf("MemberChannels = %v; want [C2QPK1V44] alongside the failures",
			got.MemberChannels)
	}
}

// TestChannelsInfo_AbsentMembershipAndFailuresDecodeEmpty pins the
// dominant observed shape: member_channels appears in 5 of 18
// responses and failed_ids in 4 of 18, so absence is the norm and must
// mean empty, never an error.
func TestChannelsInfo_AbsentMembershipAndFailuresDecodeEmpty(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty) // {"results":[],"ok":true}
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{"C2QPK1V44": 1})
	if err != nil {
		t.Fatalf("ChannelsInfo on a response with neither key: %v", err)
	}
	if len(got.MemberChannels) != 0 {
		t.Errorf("MemberChannels = %v; want empty when member_channels is absent",
			got.MemberChannels)
	}
	if len(got.FailedIDs) != 0 {
		t.Errorf("FailedIDs = %v; want empty when failed_ids is absent", got.FailedIDs)
	}
}

// TestChannelsInfo_AccumulatesMembershipAcrossBatches is the batching
// bug most likely to slip through: member_channels is per-batch, so
// assigning instead of appending silently keeps only the last batch
// and reports every channel in the earlier batches as a non-membership.
func TestChannelsInfo_AccumulatesMembershipAcrossBatches(t *testing.T) {
	// Three batches, each naming a distinct member channel and a
	// distinct failure.
	rec := newRecorder(t, func(n int) (int, string) {
		return 200, fmt.Sprintf(
			`{"ok":true,"results":[],"member_channels":["M%d"],"failed_ids":["F%d"]}`, n, n)
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), ids("C", channelsInfoBatchSize*2+10))
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	if n := len(rec.requests()); n != 3 {
		t.Fatalf("made %d requests; want 3", n)
	}

	member := map[string]bool{}
	for _, id := range got.MemberChannels {
		member[id] = true
	}
	for _, want := range []string{"M1", "M2", "M3"} {
		if !member[want] {
			t.Errorf("member channel %s missing from %v; membership from earlier batches "+
				"was overwritten instead of accumulated", want, got.MemberChannels)
		}
	}
	if len(got.MemberChannels) != 3 {
		t.Errorf("MemberChannels = %v; want exactly 3 ids, one per batch", got.MemberChannels)
	}

	failed := map[string]bool{}
	for _, id := range got.FailedIDs {
		failed[id] = true
	}
	for _, want := range []string{"F1", "F2", "F3"} {
		if !failed[want] {
			t.Errorf("failed id %s missing from %v; failures from earlier batches "+
				"were overwritten instead of accumulated", want, got.FailedIDs)
		}
	}
	if len(got.FailedIDs) != 3 {
		t.Errorf("FailedIDs = %v; want exactly 3 ids, one per batch", got.FailedIDs)
	}
}

// TestChannelsInfo_MembershipAccumulatesInOrder pins the ordering too:
// a merge that kept only the first batch, or reversed them, would pass
// the set-membership assertions above.
func TestChannelsInfo_MembershipAccumulatesInOrder(t *testing.T) {
	rec := newRecorder(t, func(n int) (int, string) {
		return 200, fmt.Sprintf(`{"ok":true,"results":[],"member_channels":["M%d"]}`, n)
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), ids("C", channelsInfoBatchSize*2+10))
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	want := []string{"M1", "M2", "M3"}
	if !slices.Equal(got.MemberChannels, want) {
		t.Errorf("MemberChannels = %v; want %v in request order", got.MemberChannels, want)
	}
}

func TestChannelsInfo_EmptyResultsMeansNothingChanged(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{"CL0AET1L0": 1783337533019})
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	if len(got.Channels) != 0 {
		t.Errorf("got %d channels; want 0 — an empty results array means nothing changed, not an error", len(got.Channels))
	}
	// The request still has to happen: this is the revalidation.
	if n := len(rec.requests()); n != 1 {
		t.Errorf("made %d requests; want 1", n)
	}
}

func TestChannelsInfo_NoIDsMakesNoRequest(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	for _, in := range []map[string]int64{nil, {}} {
		got, err := c.ChannelsInfo(context.Background(), in)
		if err != nil {
			t.Fatalf("ChannelsInfo(%v): %v", in, err)
		}
		if len(got.Channels) != 0 || len(got.MemberChannels) != 0 || len(got.FailedIDs) != 0 {
			t.Errorf("ChannelsInfo(%v) returned %+v; want a zero result", in, got)
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests for an empty id set; want 0 — an empty updated_ids "+
			"map is a pointless round trip and, worse, looks like a probe", n)
	}
}

func TestChannelsInfo_SplitsLargeIDSets(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	const total = channelsInfoBatchSize*2 + 10
	want := ids("C", total)
	if _, err := c.ChannelsInfo(context.Background(), want); err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}

	reqs := rec.requests()
	if len(reqs) != 3 {
		t.Fatalf("made %d requests for %d ids; want 3 (%d+%d+10)",
			len(reqs), total, channelsInfoBatchSize, channelsInfoBatchSize)
	}

	seen := map[string]int64{}
	for i, r := range reqs {
		batch := r.updatedIDs(t)
		if len(batch) > channelsInfoBatchSize {
			t.Errorf("request %d carried %d ids; want at most %d", i, len(batch), channelsInfoBatchSize)
		}
		if len(batch) == 0 {
			t.Errorf("request %d carried no ids; an empty batch should never be sent", i)
		}
		for id, v := range batch {
			if _, dup := seen[id]; dup {
				t.Errorf("id %s sent in more than one batch; batches must not overlap", id)
			}
			seen[id] = v
		}
	}
	// Catches both a dropped final partial batch and a batch map
	// reused across flushes.
	if len(seen) != total {
		t.Errorf("sent %d distinct ids across all batches; want all %d", len(seen), total)
	}
	for id, v := range want {
		if got, ok := seen[id]; !ok || got != v {
			t.Errorf("id %s: sent %d (present=%v); want %d", id, got, ok, v)
		}
	}
}

func TestChannelsInfo_ExactMultipleOfBatchSizeSendsNoEmptyBatch(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	if _, err := c.ChannelsInfo(context.Background(), ids("C", channelsInfoBatchSize)); err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	if n := len(rec.requests()); n != 1 {
		t.Errorf("made %d requests for exactly %d ids; want 1 — a trailing empty batch "+
			"is a wasted round trip", n, channelsInfoBatchSize)
	}
}

func TestChannelsInfo_ReturnsResultsFromEveryBatch(t *testing.T) {
	// Each batch answers with one distinct channel. Anything that
	// returns only the first batch's results, or overwrites instead of
	// appending, loses rows here.
	rec := newRecorder(t, func(n int) (int, string) {
		return 200, fmt.Sprintf(`{"ok":true,"results":[{"id":"C%d","name":"batch%d","updated":%d}]}`, n, n, n)
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), ids("C", channelsInfoBatchSize*2+10))
	if err != nil {
		t.Fatalf("ChannelsInfo: %v", err)
	}
	if len(got.Channels) != 3 {
		t.Fatalf("got %d channels from 3 batches; want 3 — results from later batches were dropped: %+v", len(got.Channels), got.Channels)
	}
	seen := map[string]bool{}
	for _, ch := range got.Channels {
		seen[ch.ID] = true
	}
	for _, want := range []string{"C1", "C2", "C3"} {
		if !seen[want] {
			t.Errorf("channel %s missing from the merged result: %+v", want, got.Channels)
		}
	}
}

func TestChannelsInfo_IgnoresUnknownResponseFields(t *testing.T) {
	// Every field observed on a real channels/info result, plus a
	// top-level extra. Slack adds fields without notice; a decode that
	// rejected them would break the client in production, not in CI.
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"ok":true,"some_future_top_level_key":{"a":1},"results":[{
			"id":"C2QPK1V44","enterprise_id":"","context_team_id":"T04T4TH8W",
			"internal_team_ids":[],"pending_connected_team_ids":[],"pending_shared":[],
			"shared_team_ids":["T04T4TH8W"],"connected_limited_team_ids":[],
			"connected_team_ids":[],"conversation_host_id":"","creator":"U04T4TH8Y",
			"name":"general","name_normalized":"general","previous_names":[],
			"created":1668181000,"unlinked":0,"updated":1783337533019,
			"is_archived":false,"is_channel":true,"is_frozen":false,"is_general":true,
			"is_group":false,"is_im":false,"is_moved":0,"is_mpim":false,
			"is_org_default":false,"is_org_mandatory":false,"is_record_channel":false,
			"is_file":false,"is_shared":false,"is_ext_shared":false,"is_org_shared":false,
			"is_pending_ext_shared":false,"is_private":false,"is_global_shared":false,
			"parent_conversation":"",
			"purpose":{"creator":"U1","last_set":1,"value":"p"},
			"topic":{"creator":"U1","last_set":1,"value":"t"},
			"properties":{"canvas":{"file_id":"F1"},"tabs":[{"id":"x"}]},
			"frozen_reason":"","is_ext_ws_shared":false,"use_case":"",
			"channel_agent_status":"","a_field_slack_ships_next_week":42
		}]}`
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{"C2QPK1V44": 1})
	if err != nil {
		t.Fatalf("ChannelsInfo on a full real-shaped response: %v", err)
	}
	if len(got.Channels) != 1 {
		t.Fatalf("got %d channels; want 1", len(got.Channels))
	}
	if got.Channels[0].ID != "C2QPK1V44" || got.Channels[0].Version != 1783337533019 ||
		got.Channels[0].Topic.Value != "t" {
		t.Errorf("modelled fields did not survive the unmodelled ones: %+v", got.Channels[0])
	}
}

func TestChannelsInfo_PropagatesAPIError(t *testing.T) {
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"ok":false,"error":"invalid_auth"}`
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), map[string]int64{"C1": 1})
	if err == nil {
		t.Fatal("ChannelsInfo returned nil error on ok:false")
	}
	if !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("error = %v; want it to mention invalid_auth", err)
	}
	if got.Channels != nil || got.MemberChannels != nil || got.FailedIDs != nil {
		t.Errorf("got = %+v; want a zero result alongside an error", got)
	}
}

func TestChannelsInfo_MidBatchErrorAbortsAndDiscardsPartialResults(t *testing.T) {
	// Chosen behaviour: a failed batch fails the whole call and no
	// partial results are returned. The alternative — returning what
	// succeeded with a nil error — is indistinguishable from "only
	// these changed", so the caller would mark the unfetched ids
	// current and never revalidate them again.
	rec := newRecorder(t, func(n int) (int, string) {
		if n == 2 {
			return 200, `{"ok":false,"error":"ratelimited"}`
		}
		return 200, fmt.Sprintf(
			`{"ok":true,"results":[{"id":"C%d","updated":%d}],"member_channels":["M%d"],"failed_ids":["F%d"]}`,
			n, n, n, n)
	})
	c := rec.client()

	got, err := c.ChannelsInfo(context.Background(), ids("C", channelsInfoBatchSize*2+10))
	if err == nil {
		t.Fatal("ChannelsInfo returned nil error when the second batch failed")
	}
	if !strings.Contains(err.Error(), "ratelimited") {
		t.Errorf("error = %v; want it to mention ratelimited", err)
	}
	// Membership and failures accumulated by the first batch must be
	// discarded too: a partial membership snapshot read as a complete
	// one turns every unqueried channel into a non-membership.
	if got.Channels != nil || got.MemberChannels != nil || got.FailedIDs != nil {
		t.Errorf("got = %+v; want a zero result — partial results would look like "+
			"'only these changed' and strand the unfetched ids", got)
	}
	if n := len(rec.requests()); n != 2 {
		t.Errorf("made %d requests; want 2 — the third batch should not be attempted "+
			"after the second failed", n)
	}
}

// ------------------------------------------------------------------- users

func TestUsersInfo_SendsExpectedFlags(t *testing.T) {
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"ok":true,"results":[
			{"id":"U04T4TH8Y","team_id":"T04T4TH8W","name":"grant","deleted":false,
			 "is_bot":false,"updated":1612802061,
			 "profile":{"display_name":"Grant","real_name":"Grant Ammons","avatar_hash":"g1a2b3"}}
		],"can_interact":{"U04T4TH8Y":true}}`
	})
	c := rec.client()

	got, err := c.UsersInfo(context.Background(), map[string]int64{
		"U04T4TH8Y":   1612802061,
		"U0B0QD6BH1N": 0,
	})
	if err != nil {
		t.Fatalf("UsersInfo: %v", err)
	}

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("made %d requests; want 1", len(reqs))
	}
	if reqs[0].path != "/cache/T04T4TH8W/users/info" {
		t.Errorf("path = %q; want /cache/T04T4TH8W/users/info", reqs[0].path)
	}

	body := reqs[0].generic(t)
	if len(body) != 4 {
		t.Errorf("request keys = %v; want exactly token, check_interaction, "+
			"include_profile_only_users, updated_ids", reqs[0].keys(t))
	}
	if body["check_interaction"] != true {
		t.Errorf("check_interaction = %v; want true", body["check_interaction"])
	}
	if body["include_profile_only_users"] != true {
		t.Errorf("include_profile_only_users = %v; want true", body["include_profile_only_users"])
	}
	if _, ok := body["check_membership"]; ok {
		t.Errorf("users/info sent check_membership; that flag belongs to channels/info: %v", reqs[0].keys(t))
	}

	sent := reqs[0].updatedIDs(t)
	if len(sent) != 2 || sent["U04T4TH8Y"] != 1612802061 || sent["U0B0QD6BH1N"] != 0 {
		t.Errorf("updated_ids = %v; want the {id: version} map verbatim", sent)
	}

	if len(got) != 1 {
		t.Fatalf("got %d users; want 1", len(got))
	}
	u := got[0]
	if u.ID != "U04T4TH8Y" {
		t.Errorf("ID = %q; want U04T4TH8Y", u.ID)
	}
	if u.Name != "grant" {
		t.Errorf("Name = %q; want grant", u.Name)
	}
	if u.TeamID != "T04T4TH8W" {
		t.Errorf("TeamID = %q; want T04T4TH8W", u.TeamID)
	}
	if u.Deleted || u.IsBot {
		t.Errorf("false flags decoded true: %+v", u)
	}
	// users/info stamps `updated` in whole seconds, channels/info in
	// milliseconds. Both are just opaque version stamps to us, but
	// they come from the same field name.
	if u.Version != 1612802061 {
		t.Errorf("Version = %d; want 1612802061 (from the `updated` field)", u.Version)
	}
	if u.Profile.DisplayName != "Grant" {
		t.Errorf("Profile.DisplayName = %q; want Grant", u.Profile.DisplayName)
	}
	if u.Profile.RealName != "Grant Ammons" {
		t.Errorf("Profile.RealName = %q; want Grant Ammons", u.Profile.RealName)
	}
}

func TestUsersInfo_NoIDsMakesNoRequest(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	for _, in := range []map[string]int64{nil, {}} {
		got, err := c.UsersInfo(context.Background(), in)
		if err != nil {
			t.Fatalf("UsersInfo(%v): %v", in, err)
		}
		if len(got) != 0 {
			t.Errorf("UsersInfo(%v) returned %d users; want 0", in, len(got))
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests for an empty id set; want 0", n)
	}
}

func TestUsersInfo_EmptyResultsMeansNothingChanged(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	got, err := c.UsersInfo(context.Background(), map[string]int64{"U04T4TH8Y": 1612802061})
	if err != nil {
		t.Fatalf("UsersInfo: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d users; want 0", len(got))
	}
	if n := len(rec.requests()); n != 1 {
		t.Errorf("made %d requests; want 1", n)
	}
}

func TestUsersInfo_SplitsLargeIDSets(t *testing.T) {
	rec := newRecorder(t, alwaysEmpty)
	c := rec.client()

	const total = usersInfoBatchSize*2 + 10
	want := ids("U", total)
	if _, err := c.UsersInfo(context.Background(), want); err != nil {
		t.Fatalf("UsersInfo: %v", err)
	}

	reqs := rec.requests()
	if len(reqs) != 3 {
		t.Fatalf("made %d requests for %d ids; want 3", len(reqs), total)
	}
	seen := map[string]int64{}
	for i, r := range reqs {
		batch := r.updatedIDs(t)
		if len(batch) > usersInfoBatchSize {
			t.Errorf("request %d carried %d ids; want at most %d", i, len(batch), usersInfoBatchSize)
		}
		if len(batch) == 0 {
			t.Errorf("request %d carried no ids; an empty batch should never be sent", i)
		}
		for id, v := range batch {
			if _, dup := seen[id]; dup {
				t.Errorf("id %s sent in more than one batch", id)
			}
			seen[id] = v
		}
	}
	if len(seen) != total {
		t.Errorf("sent %d distinct ids across all batches; want all %d", len(seen), total)
	}
	for id, v := range want {
		if got, ok := seen[id]; !ok || got != v {
			t.Errorf("id %s: sent %d (present=%v); want %d", id, got, ok, v)
		}
	}
}

func TestUsersInfo_ReturnsResultsFromEveryBatch(t *testing.T) {
	rec := newRecorder(t, func(n int) (int, string) {
		return 200, fmt.Sprintf(`{"ok":true,"results":[{"id":"U%d","name":"batch%d","updated":%d}]}`, n, n, n)
	})
	c := rec.client()

	got, err := c.UsersInfo(context.Background(), ids("U", usersInfoBatchSize*2+10))
	if err != nil {
		t.Fatalf("UsersInfo: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d users from 3 batches; want 3: %+v", len(got), got)
	}
}

func TestUsersInfo_MidBatchErrorAbortsAndDiscardsPartialResults(t *testing.T) {
	rec := newRecorder(t, func(n int) (int, string) {
		if n == 2 {
			return 500, `{"ok":true,"results":[]}`
		}
		return 200, fmt.Sprintf(`{"ok":true,"results":[{"id":"U%d","updated":%d}]}`, n, n)
	})
	c := rec.client()

	got, err := c.UsersInfo(context.Background(), ids("U", usersInfoBatchSize*2+10))
	if err == nil {
		t.Fatal("UsersInfo returned nil error when the second batch got HTTP 500")
	}
	if got != nil {
		t.Errorf("got = %+v; want nil results alongside an error", got)
	}
	if n := len(rec.requests()); n != 2 {
		t.Errorf("made %d requests; want 2 — later batches should not be attempted", n)
	}
}

func TestUsersInfo_IgnoresUnknownResponseFieldsIncludingCanInteract(t *testing.T) {
	// can_interact is a real top-level key in every users/info
	// response (it is what check_interaction:true buys) and nothing in
	// this package models it. Neither it nor any unmodelled per-user
	// field may break the decode.
	rec := newRecorder(t, func(int) (int, string) {
		return 200, `{"ok":true,"results":[{
			"id":"U04T4TH8Y","team_id":"T04T4TH8W","name":"grant","deleted":false,
			"color":"9f69e7","real_name":"Grant Ammons","tz":"America/New_York",
			"tz_label":"Eastern Standard Time","tz_offset":-18000,
			"profile":{"title":"","phone":"","skype":"","real_name":"Grant Ammons",
			  "real_name_normalized":"Grant Ammons","display_name":"Grant",
			  "display_name_normalized":"Grant","fields":null,"status_text":"",
			  "status_emoji":"","status_emoji_display_info":[],"status_expiration":0,
			  "status_clear_on_focus_end":false,"avatar_hash":"g1a2b3","start_date":"",
			  "huddle_state":"default_unset","first_name":"Grant","last_name":"Ammons",
			  "status_text_canonical":"","team":"T04T4TH8W"},
			"is_admin":true,"is_owner":true,"is_primary_owner":true,"is_restricted":false,
			"is_ultra_restricted":false,"is_bot":false,"is_app_user":false,
			"updated":1612802061,"is_email_confirmed":true,
			"who_can_share_contact_card":"EVERYONE","a_field_slack_ships_next_week":42
		}],"can_interact":{"U04T4TH8Y":true,"U0B0QD6BH1N":false},"ok_extra":1}`
	})
	c := rec.client()

	got, err := c.UsersInfo(context.Background(), map[string]int64{"U04T4TH8Y": 1})
	if err != nil {
		t.Fatalf("UsersInfo on a full real-shaped response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d users; want 1", len(got))
	}
	if got[0].ID != "U04T4TH8Y" || got[0].Version != 1612802061 || got[0].Profile.DisplayName != "Grant" {
		t.Errorf("modelled fields did not survive the unmodelled ones: %+v", got[0])
	}
}

// ---------------------------------------------------------------- batching

// TestBatchSizes_StayWithinObservedShapes pins the constants against
// the captures. Exceeding a batch size the official client has never
// sent is exactly the kind of divergence that gets flagged.
func TestBatchSizes_StayWithinObservedShapes(t *testing.T) {
	if channelsInfoBatchSize > 63 {
		t.Errorf("channelsInfoBatchSize = %d; the largest channels/info batch observed "+
			"across 18 captured requests is 63", channelsInfoBatchSize)
	}
	if usersInfoBatchSize > 80 {
		t.Errorf("usersInfoBatchSize = %d; the largest users/info batch observed "+
			"across 30 captured requests is 80", usersInfoBatchSize)
	}
	if channelsInfoBatchSize < 1 || usersInfoBatchSize < 1 {
		t.Fatal("batch sizes must be positive; a zero size never flushes")
	}
}
