# Grid Parity Phase 2a — Outcomes

Record of what
[`plans/2026-07-30-grid-parity-phase2a-foundations.md`](./2026-07-30-grid-parity-phase2a-foundations.md)
actually shipped, every place the HAR captures overruled the plan, and what
Phase 2b inherits. Companion to
[the Phase 1 outcomes](./2026-07-30-grid-parity-phase1-outcomes.md).

Phase 2a is **purely additive**: nothing outside the new packages calls any of
it. Verified by grep at merge (Task 10, below).

## What shipped

| Unit | Files | What it is |
|---|---|---|
| Cache version columns | `internal/cache/db.go` | `channels.version` / `users.version` INTEGER, `messages.version` TEXT, via the existing `addColumnIfMissing` probe |
| Version helpers | `internal/cache/versions.go` | `ChannelVersions` / `UserVersions` / `MessageVersions` + setters — the `{id: version}` maps `updated_ids` and `cached_latest_updates` are built from |
| edgeapi transport | `internal/slack/edge/client.go` | JSON body as `text/plain;charset=UTF-8`, `/cache/<team>/<endpoint>` |
| Conditional revalidation | `internal/slack/edge/cache.go` | `ChannelsInfo` / `UsersInfo` — **the mechanism that replaces enumeration** |
| Server-side search | `internal/slack/edge/search.go` | `ChannelsSearch` / `UsersSearch` — replaces `conversations.list` for the finder |
| Channel-scoped members | `internal/slack/edge/members.go` | `UsersList` / `ChannelsMembership` / `UsersCounts` |
| Boot parser | `internal/slack/boot/boot.go` | `client.userBoot` — one call for five |
| Channel-open parser | `internal/slack/boot/view.go` | `conversations.view` — history + users + bots + channels + emoji in one |
| Incremental sync | `internal/slack/client.go` (append-only) | `HistoryWithVersions` — `cached_latest_updates` |

~2,000 lines of implementation, ~6,400 of tests.

Two `internal/slackhttp` fixes were **not in the plan** and are described under
*Divergences* below.

## Divergences: where the captures overruled the plan

The plan's standing instruction was "if the captures contradict the plan, the
captures win." They did, eleven times.

### 1. `users/info` batch sizes were wrong

Plan: *"`channels/info` up to 63 ids per call; `users/info` 14-34."*

Measured across all 8 captures:

```
channels/info  n=18  range 1–63   [1,1,1,1,1,1,1,1,1,3,4,6,11,20,20,26,31,63]
users/info     n=30  range 1–80   [...,40,40,57,57,80,80]
```

`users/info` reaches **80**, not 34. Constants are 60 / 80, and the code
comments state the measured ranges rather than the plan's figure.

### 2. `is_member` does not exist on `channels/info` results

Plan's `Channel` struct had `IsMember bool \`json:"is_member"\``. Across 36
observed result objects, `is_member` appears in **0**. The field was removed.

### 3. `check_membership: true` returns a top-level array, not a field

What the flag actually buys is `member_channels` — a `[]string` returned
**even when `results` is empty** (all 5 observed responses carrying it had
`"results":[]`). That is precisely how the official client learns membership
without enumerating, and the plan modelled none of it.

### 4. `failed_ids` exists and is a correctness hazard

`channels/info` returns `failed_ids` (4 of 18 responses) for ids the server
could not resolve. Absence from `results` otherwise means "unchanged, still
fresh" — so an ignored `failed_ids` entry is marked current and its stale
record kept **forever**, because its version never advances. Now surfaced.

### 5. `users/counts` returns an object, not an int

The plan suggested `UsersCounts(...) (int, error)` while warning to check.
7/7 responses nest `{everyone, people, members, guests, bots, apps, by_team}`
plus `invited` (2/7). Modelled as a struct with `ByTeam map[string]int`.

### 6. `users/search` was missing two params

The plan's payload omitted `current_channel` and `default_workspace`, both
present on 2/2 captured requests. Sending a smaller param set than the real
client is exactly the separable difference this project exists to remove.
`UsersSearch` gained a `currentChannel` argument.

### 7. `muted_channels` is not in `userBoot`'s prefs

The **spec** says `prefs` carries "muted channels". It does not — I checked all
702 keys. Mute state lives in `all_notifications_prefs`, a JSON-encoded string
under `channels[id].muted`. slk's own code already documented this
(`client.go:1314`); the spec did not. `boot.Result` exposes the raw string and
leaves parsing to the caller, which also avoids an import cycle with
`internal/slack` in Phase 2b.

### 8. `conversations.view`'s response has two more top-level keys

The spec listed `ok, history, users, bots, channels, emojis`. The real response
also has **`channel`** and `response_metadata`. `channel.id` matters: it is how
a caller detects that the unverified `channel` param was ignored and it got the
last-viewed conversation instead. Now exposed.

### 9–10. Two `_x_*` envelope divergences shipped in Phase 1 — **not in the plan at all**

Found while preparing Task 7. Phase 1's transport appended `_x_reason` and
`_x_mode` to every urlencoded body. The official client does not:

```
(_x_reason, _x_mode) across 163 captured form bodies
  (true,  true)  -> 149
  (true,  false) ->   4     client.shouldReload, client.userBoot
  (false, false) ->  10     api.features, client.getWebSocketURL,
                            conversations.view, experiments.getByUser,
                            features.access.policies.list
  (false, true)  ->   0     never happens
```

The split is clean per-endpoint — **zero endpoints are mixed**. Fixed in
`4df0f14` and `7a3293d` with exact-match exclusion sets, plus a guard making
the `(false, true)` shape unreachable by construction.

This mattered immediately: slk already calls `client.shouldReload` (Phase 1
added it), so slk was shipping the divergence on every startup. And Tasks 7–8
add `client.userBoot` and `conversations.view`, which would have been divergent
from birth.

**The captures cannot distinguish** "these endpoints never carry the flags"
from "nothing carries them before the client is online" — every observation of
all 7 is at boot. The endpoint set is what is encoded; `mode.go` records the
ambiguity and says what would collapse it.

### 11. The plan's own network-isolation check was broken

Task 10 Step 3 specified:

```bash
unshare -rn go test ./... 2>&1 | grep -E '^(FAIL|---)'
```

Bare `unshare -rn` leaves loopback **DOWN**, so every `httptest`-based test
fails to dial `127.0.0.1` and the check cannot distinguish "a test called
slack.com" from "loopback is down". The working form is:

```bash
unshare -rn sh -c 'ip link set lo up && go test ./...'
```

## Test-integrity findings

Phase 1's lesson was that roughly a third of its tests passed against a broken
implementation. Every task here was mutation-tested, and reviewers re-ran the
mutations independently rather than trusting reports. That found:

- **Two of the plan's own supplied tests were vacuous** (Task 3).
  `TestClient_PropagatesHTTPError` returned a 500 with an *empty body*, so
  removing the status check still errored — on `json.Unmarshal("")`. It passed
  against a broken implementation. `TestClient_RespectsContextCancellation`'s
  blocking handler is unreachable with a pre-cancelled context and converted a
  regression into a **25-second timeout panic** instead of an assertion.
- **Three pre-existing artifacts asserted the bugs in passing.**
  `TestEnvelopeBody_DefaultsReasonPerEndpoint` asserted the `_x_mode` tail for
  endpoints that send none; `TestEnvelopeBody_OmitsXModeOnBootPhaseEndpoints`
  claimed "dropping `_x_mode` must not drop the reason" for all seven when it is
  true of two; and `official-request-shape.json`'s `x_reason_always_present:
  true` conceded in its own rationale text that the client omits it on 10 of 163
  and then declared always-present anyway.
- **All-zero fixtures prove nothing** (Task 4). Nine mutants survived because
  every boolean in the fixtures was `false` — the assertions could not tell
  "decoded false" from "never decoded". `IsChannel` was the only boolean pinned
  `true` and the only one whose mutants died: a clean natural experiment. The
  same pattern recurred with *strings* in Task 7 (two fields sharing a fixture
  value, so a tag swap decoded the right answer for the wrong reason) and with
  *ordering* in Task 5 (a frecency fixture that was accidentally already
  sorted, so a sort was invisible).
- **Partial data alongside an error** survived in every task until pinned.
  `encoding/json` records the first `UnmarshalTypeError` and **keeps decoding**,
  so a response can populate fields *and* return an error.
- **Removing a struct tag is often not a valid mutation in Go** — `encoding/json`
  falls back to case-insensitive field-name matching, so `OK bool` still decodes
  `"ok"`. Use `json:"-"` or a mis-tag when you mean "stops decoding".
- Three implementers recorded **false mutation results** from piping `go test`
  through `tail`, which reports *tail's* exit status. One caught it themselves
  and said so; one used a `defer` on an unnamed return value that mutated
  nothing and briefly logged a false "SURVIVED".

## Evidence base

`internal/slack/testdata/phase2-api-contracts.json` grew a `measured` block per
edge endpoint. The original extractor kept `samples[:3]`, and for
`channels/info` those three happened to be unchanged (`results:[]`) responses —
so `member_channels` and `failed_ids`, both modelled here, had **no committed
evidence at all**, and a reviewer auditing "the largest observed request carried
66 ids" found 51 and could not confirm it.

Every numeric claim in a Phase 2a code comment is now checkable from the repo:
key frequencies, request list lengths, scalar value distributions, the
`members + non_members == users sent` invariant (10/10), the `counts` field
frequency, and the capture count. Aggregates only — no ids, no bodies, no
tokens, with an assert-no-token-leak check on write.

`*.har` was added to `.gitignore` and to `.git/info/exclude`. The captures were
sitting untracked-but-not-ignored in the worktree root, one `git add -A` from a
public PR, carrying live `xoxc`/`xoxd` credentials and real message content.

## Verification at merge

```
go build ./...                                   clean
go vet ./...                                     clean
go test ./... -race                              all packages ok
golangci-lint run ./...                          0 issues
grep -rn 'slack/edge\|slack/boot' cmd/ internal/ no matches outside those packages
unshare -rn sh -c 'ip link set lo up && go test ./...'   all ok — no test touches the network
```

`gofmt -l` reports 30 unformatted files repo-wide, all pre-existing. Of the
files this branch touched, only `internal/config/config.go` is among them, and
its gofmt deviation is byte-identical to the merge-base — this branch added no
new formatting drift and deliberately reformatted nothing.

## What Phase 2b inherits

### Decisions deferred to the caller

- **`Set*Version` is a silent no-op on a missing row.** Matches the existing
  `SetChannelSyncedAt` pattern, and the comments now say so. But the consequence
  is worse here: a dropped `synced_at` costs one redundant fetch; a dropped
  *version* means that row sends `0` forever and pulls a full record on every
  boot, silently.
- **`ChannelVersions` includes non-member channels and never shrinks**, so
  `updated_ids` grows with every channel ever cached. Whether to filter on
  `is_member` is a design question the captures should settle first.
- **`MemberChannels` is a snapshot over the ids sent, not a workspace-wide
  list.** An id that was sent and is absent is a non-membership; an id never
  sent says nothing. A caller that unions it into a persisted set without also
  removing sent-but-absent ids will accumulate stale memberships.
- **`UsersList` has no upper bound on `count`.** `UsersList(ctx, ch, 5000)` is
  the enumeration in a single request. Documented rather than capped, because a
  silent truncation lies to the caller.
- **`HistoryWithVersions` does not clamp `limit`.** It defaults to 28 (14/14
  observed) but passes a caller's value through. Clamping would make a caller
  asking for 200 loop eight times — trading one anomalous request for a burst of
  eight, and burst volume is what anomaly detection scores.

### Unresolved facts that need a capture

- **The `conversations.view` `channel` param is still unverified.** The captured
  request carried none and returned the last-viewed conversation. The parser
  makes the failure *detectable* via `channel.id`, but **Phase 2b must actually
  perform the probe-and-compare** — if it calls `ConversationsView` and ignores
  `Channel.ID`, slk renders the wrong channel's history with no error anywhere.
  Verified fallback: `HistoryWithVersions` with `limit=28`.
- **`userBoot`'s `starred` and `subteams.self` were empty in 2/2 captures**, so
  their element shapes are unknown and both are `[]json.RawMessage`. The plan
  claims `userBoot` replaces `stars.list` and `usergroups.list`; **it cannot be
  confirmed to**, and slk calls both today (`client.go:1595`, `client.go:821`).
  Neither appears anywhere in the 8 captures. A capture from a workspace with a
  non-empty starred list and a non-empty usergroup set is the missing fact.
- **`edge.User` has no avatar URL.** `users/info` profiles carry `avatar_hash`
  but no `image_*`; `users/search` profiles *do* carry `image_original`. The two
  endpoints disagree, and the field was left off rather than modelled as one
  that decodes empty forever.
- **A fixed batch size is itself a divergence.** The observed distributions are
  ragged because they are demand-driven; there is no client-side cap. A cold
  revalidation of a 10k-user workspace at a fixed 80 is 125 consecutive
  exactly-80 requests, which is a cleaner signature than the ragged real thing.
  Jitter was deliberately *not* added — inventing a shape with no evidence is
  the Phase 1 `sec-ch-ua` mistake. The real fix is 2b scoping revalidation to
  the ids actually needed.

### Still open from Phase 1

Everything in Phase 1's residual-divergence table remains open — multipart
bodies, alphabetical business-param order, `_x_b3_*` over-sending,
`_x_foreground`, `Accept-Encoding`, and the `Authorization: Bearer` header on
image fetches and `chat.getPermalink`. The two `_x_*` fixes above are the only
Phase 1 residuals this phase closed, and they were not on that list because
nobody had measured them yet.

### Not addressed by Phase 2a at all

The whole point of Layer 2 — deleting `triggerBackfill`, `conversations.list`
and the `users.list` sync, and rewiring `connectWorkspace` — is Phase 2b. Phase
2a only builds the pieces. **slk's runtime behaviour is unchanged by this
phase**, so it cannot on its own affect the sign-outs.
