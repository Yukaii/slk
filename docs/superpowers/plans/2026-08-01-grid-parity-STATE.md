# Grid Parity — Where This Is, 2026-08-01

**Read this first if you are picking the work up.** It records *state* and the
findings that live nowhere else. The design and the plan are the forward-looking
documents and are still accurate — this one says how far through them we got and
what changed underneath them.

## The three documents, and what each is for

| Document | Purpose |
|---|---|
| [`specs/2026-07-30-enterprise-grid-bootstrap-design.md`](../specs/2026-07-30-enterprise-grid-bootstrap-design.md) | The original three-layer design. **Partly superseded** — see *Corrections* below. |
| [`specs/2026-07-31-grid-parity-phase2b-design.md`](../specs/2026-07-31-grid-parity-phase2b-design.md) | Phase 2b design. Current. Its opening section records a wrong correction I made and then reversed; read it. |
| [`plans/2026-07-31-grid-parity-phase2b-bootstrap-rewrite.md`](./2026-07-31-grid-parity-phase2b-bootstrap-rewrite.md) | The 12-task plan. Current, **but its `- [ ]` checkboxes were never ticked** — use the table below instead. Tasks 9/10/11 were re-scoped mid-flight; the file reflects that. |
| [`plans/2026-07-30-grid-parity-phase1-outcomes.md`](./2026-07-30-grid-parity-phase1-outcomes.md) | Phase 1 retrospective. |
| [`plans/2026-07-30-grid-parity-phase2a-outcomes.md`](./2026-07-30-grid-parity-phase2a-outcomes.md) | Phase 2a retrospective. Its *Test-integrity findings* section is the most useful thing to read before writing any test here. |

## Where we are

Branch `feat/grid-parity`, worktree `.worktrees/grid-parity-phase1` (the
directory name is a leftover; it holds all three phases). Draft PR
[gammons/slk#121](https://github.com/gammons/slk/pull/121). HEAD `26adbc0`,
everything pushed, tree clean.

Phases 1 and 2a are complete. Phase 2b is **7 of 12 tasks done**.

| Task | State | Key commits |
|---|---|---|
| 1. Request counter | ✅ | `372cd0f`, `91de452`, `7d53bcb`, `bec5ab3` |
| 2. Cache partial writers | ✅ | `23dd441`, `9b0d52b` |
| 3. `edge.User.ImageOriginal` + membership batch presence | ✅ | `2c2c619`, `2350c4c` |
| 4. `internal/bootstrap` skeleton | ✅ | `cbede9d` |
| 5. `conversations.view` + fallback | ✅ | `5b210fd`, `26adbc0` |
| 6. Scoped revalidation | ✅ | `1997284`, `263464c` |
| 7. Wire `connectWorkspace` | ✅ | `12b5a7d`, `26adbc0` |
| 8. Delete the `users.list` sweep | ⬜ next | |
| 9. Delete `triggerBackfill` + thread-subscription sweep, bound reconnect | ⬜ | |
| 10. Defer boot-time `subscriptions.thread.getView` | ⬜ | |
| 11. Finder/mentions → edge search, **delete `GetAllPublicChannels`** | ⬜ | |
| 12. Full verification + outcomes doc | ⬜ | |

**slk currently boots through `internal/bootstrap` and still runs every old
enumeration alongside it.** That is deliberate (plan Task 7): no intermediate
commit may leave slk unable to boot. Call counts are *up* by ~9 per boot right
now. Tasks 8-11 delete the old paths, each next to its replacement.

## Corrections to the original design, made during the work

The Layer 2 design predates any measurement. Four of its claims are wrong.

1. **`conversations.list` IS called.** The design said so, I "corrected" it to dead code, and I was wrong — that reversal is documented at the top of the 2b design. The live path is `Client.GetAllPublicChannels` ← `fetchBrowseableChannels` ← the `go` spawn in `run`. **Find these by symbol, not by line** — see *A note on line citations* below. It pages at `Limit: 1000` to populate the finder with unjoined channels. **Deleting it belongs with Task 11**, next to the `edge.ChannelsSearch` move that replaces it — deleting it earlier drops unjoined channels from the finder with nothing in their place.

2. **The WebSocket does not replay missed messages.** The design inferred from slk's socket params (`sync_desync=1`, `ms_latest=true`, `flannel=3`, `lazy_channels=1`) that slk probably receives the same replay as the official client. Measured 2026-08-01: after a 90-second outage the socket delivered ~160 `presence_change` events and nothing else. **`client.counts` stays in the reconnect path.**

3. **slk never refreshes `client.counts` on reconnect today.** `rtmEventHandler.OnConnect` does presence/DND, a section rebootstrap, backfill and a membership refresh — no counts. That is why a message posted during an outage never appeared. Task 9's bounded handler is a **user-visible bug fix**, not only a fingerprint change.

4. **The reconnect cost is not where the design put it.** Measured on a 105-channel workspace:

   ```
   channel-phase       total_msgs=0   dur_ms=2711     <- 2.7s, found nothing
   subscription-phase  subs=1000      dur_ms=132248   <- 132s
   ListThreadSubscriptions: hit hard cap 1000, stopping   (x4 per session)
   ```

   The thread-subscription sweep is **50x** the channel backfill and runs on
   every reconnect. It moved from a Task 10 cleanup into Task 9.

   Also measured: 288 per-channel `conversations.history` calls in one
   3-minute session, **250 of them (86%) returning zero messages**. The
   design's "most calls return zero messages quickly, so this is harmless" is
   confirmed on the first half and refuted on the second.

5. **`conversations.view`'s `channel` param works.** The design's one flagged
   unknown. Verified 2026-08-01 on two non-Grid workspaces — honoured both
   times, no fallback. **Still unverified on Grid**, so the probe-and-compare
   stays.

## Measurement: read this before quoting any number

**"Boot slk and quit" is not a repeatable protocol.** Totals are dominated by
background sweeps racing process shutdown. Same binary, same ~5-second session:
`users.list` came back **12, 34, and 97**; `users.info` 2 and 60. An early
"180 calls" baseline in this session's history is noise and should not be
quoted.

What works: build both commits (`git archive` the parent), run them at matched
durations, and compare **per-endpoint attributable deltas**, not totals. Task 12
must do this. The Task 7 delta below was derived that way and is stable across
runs:

```
+2 client.userBoot   +2 conversations.view   +3 edge:channels/info
+2 edge:users/info   +2 client.counts        -2 users.prefs.get   = +9 net
```

The instrument is `slackhttp.DefaultCounter`, dumped at shutdown under
`SLK_DEBUG=1` (`grep -A40 'shutdown API request tally' slk-debug.log`).

## A note on line citations

**Do not trust a `file.go:NNN` citation in any of these documents, including
this one.** Line numbers drift with every task, and that drift caused the single
worst error in this project: the Layer 2 design cited `conversations.list` at
`main.go:2177`, the line had moved, I searched for a wrapper under a name I had
inferred from the prose rather than verified, found nothing, and concluded the
call was dead code. It was not. I then wrote that conclusion into two documents
and stated it confidently before a measured boot disproved it.

Cite and search by **symbol**. The symbols that matter, current at `26adbc0`:

| Symbol | File | Line at `26adbc0` |
|---|---|---|
| `Client.GetAllPublicChannels` | `internal/slack/client.go` | 532 |
| `fetchBrowseableChannels` | `cmd/slk/main.go` | 2276 |
| its `go` spawn | `cmd/slk/main.go` | 1788 |
| `rtmEventHandler.OnConnect` | `cmd/slk/main.go` | 3776 |
| `rtmEventHandler.triggerBackfill` | `cmd/slk/main.go` | 3841 |
| the `users.list` sweep (`client.GetUsers`) | `cmd/slk/main.go` | 2132 |

Re-derive with `rg -n 'func fetchBrowseableChannels'` before acting on any of
them.

## Operational notes

- **The 8 HAR captures live in the worktree root** and hold live `xoxc`/`xoxd` credentials and real message content. They are gitignored via `.gitignore` **and** `.git/info/exclude`. Never `git add -A`. Never paste raw capture content into a file or a commit message. Sanitized aggregates go in `internal/slack/testdata/phase2-api-contracts.json`; `/tmp/opencode/phase2_fixtures.py` shows the pattern and has an assert-no-token-leak check.
- **The fixture extractor truncates.** It keeps `samples[:3]` and summarises `results[0]`, so any per-field claim about an array element is a single-element generalisation unless it has a denominator. That bug produced a wrong avatar contract in Phase 2a. The `measured` blocks added later cover all observations; prefer those.
- **Very long subagent prompts get cancelled.** Several dispatches in this session died mid-flight — one left a live `// MUTANT` marker in `counter.go`. If a task ends unexpectedly, `grep -rn MUTANT internal/ cmd/` and check `git status` before trusting anything.
- **`go test ... | tail` reports tail's exit status.** Five implementers on this project recorded false mutation results that way. Redirect to a file and read `$?` on the next line.
- **Removing a struct tag is usually not a valid Go mutation** — `encoding/json` falls back to case-insensitive field-name matching. Use `json:"-"` or a mis-tag.
- Gate: `go build ./... && go vet ./... && go test ./... -race && golangci-lint run ./...`
- Network isolation check needs loopback up: `unshare -rn sh -c 'ip link set lo up && go test ./...'`. Bare `unshare -rn` leaves `lo` DOWN and every `httptest` test fails for the wrong reason.
- `gofmt -l` reports ~30 unformatted files repo-wide, all predating this branch. Do not reformat them; only files you touch must be clean.

## Standing risks

- **The cache column mapping** is the most likely source of silent damage. `edge` results cover different column subsets than `UpsertChannel`/`UpsertUser` write, so revalidation goes through the partial writers in `internal/cache/edge_sync.go`. If avatars, membership or starred state start disappearing, look there first.
- **`Result.Messages` is fetched and discarded.** `conversations.view` is currently pure cost — the channel still renders through the old cache + `GetHistory` path. Tasks 8-11 wire it. The `[]slack.Message` → `[]json.RawMessage` conversion in `cmd/slk/bootstrap_adapters.go` is lossy and unvalidated against a real render.
- **Cold-cache convergence takes two boots.** The partial writers are UPDATE-only, so on an empty cache they find no rows; first-sight hydration inserts at version 0 and the next boot re-requests in full. Bytes, not correctness.
- **Nobody has tested any of this on Enterprise Grid**, and nobody should until all three phases land. Two contributors have already been signed out helping diagnose the original problem. The Phase 2a outcomes doc leads with this and so should any summary.
