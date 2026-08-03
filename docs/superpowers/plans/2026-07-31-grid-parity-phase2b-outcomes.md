# Grid Parity Phase 2b — Outcomes

## Should a Grid tester try this yet?

**No.** One measurement taken while writing this document says so, and it is
the most important thing here:

**On a cold cache, a 35-second boot started 40,523 `users.info` requests —
one per distinct channel member in the workspace.**

Read "started" literally: `slackhttp.Counter` records at `RoundTrip` entry, not
at completion (`internal/slackhttp/transport.go:110`). slk spawns one goroutine
and one request per unresolved user with no worker pool, so all ~40k enter the
transport within moments of each other and then queue on the connection pool.
How many actually reached Slack in those 35 seconds is unknown and is certainly
far smaller — the absence of error lines in the debug log is evidence that most
never returned at all. **Do not quote this as "40k requests hit Slack."** What
it establishes is the fan-out's *shape*: unbounded, and sized by workspace
membership rather than by anything on screen.

The count matches an independent number almost exactly:

```
counter:                                        40,523  users.info
select count(distinct user_id) from channel_members ->  40,527
```

That is one request per distinct channel member, which is the mechanism below,
confirmed arithmetically rather than argued.

It is a direct consequence of Task 8: deleting the `users.list` sweep removed
the thing that was keeping an existing per-user fan-out dormant. On a warm cache
the same boot issues **2**, which is why every other measurement in this
document looks as good as it does and why this went unnoticed for four tasks.

**Who hits it:** anyone whose cache is empty — i.e. a fresh install. That is
exactly the state a new Grid tester would be in.

Nobody should point this at an Enterprise Grid account until that is fixed. Two
contributors have already been signed out helping diagnose the original problem.

Everything below is still true; it is just not the whole picture.

---

## What landed

Tasks 1-7 landed in earlier sessions. This session did 8, 9, 10 and half of 11.

| Task | State | Commit |
|---|---|---|
| 8. Delete the `users.list` sweep | done | `35a0611` |
| 9. Delete `triggerBackfill`, bound reconnect | done | `f08011c` |
| 10. Defer boot-time `subscriptions.thread.getView` | done | `80abaf7` |
| 11a. Channel finder → `edge.ChannelsSearch`, delete `conversations.list` | done | `c60b92a` |
| 11b. Mention picker → `edge.UsersSearch` | **not done** | — |
| 12. Verification | this document | — |

## Measured call counts

Same protocol every time: `SLK_DEBUG=1`, two workspaces (105 and 39 channels),
a 25-35 second session, `grep -A40 'shutdown API request tally' slk-debug.log`.
Run under `script` for a pty, terminated with SIGINT so the shutdown tally is
written.

| Endpoint | Task 7 baseline | After 11a | Deleted by |
|---|---|---|---|
| `conversations.history` | 144 | **3** | Task 9 |
| `subscriptions.thread.getView` | 79 | **0** | Task 10 |
| `conversations.list` | 4 | **0** | Task 11a |
| `users.list` | 2 | **0** | Task 8 |
| `client.counts` | 6 | 6 | — |
| `users.conversations` | 3 | 3 | — |
| **session total** | **270** | **44** | |

**Read the totals with suspicion, the per-endpoint rows without.** The session
total is dominated by whichever background sweep was still running when the
process was killed — the same binary produced 270, 312 and 173 across three
runs of different lengths during this session. The four endpoint rows are
attributable deltas: each went to zero because the code that called it no
longer exists.

Three of the four are now **compile-time** facts rather than behavioural ones:

- `GetUsersContext` (`users.list`) and `GetConversations` (`conversations.list`)
  are gone from the `SlackAPI` interface, which is the only route from slk into
  slack-go. `TestSlackAPI_DeclaresNoWorkspaceEnumeration` fails if either
  returns.
- The reconnect path's client interface declares exactly one method.
  `TestReconnect_ClientSurfaceCannotEnumerate` fails if it grows, and
  `TestReconnect_IsO1NotOChannels` compares a 3-channel workspace against a
  300-channel one and requires the same call list from both.

### Success criteria, honestly scored

| Criterion | Result |
|---|---|
| 1. Boot ≤ 10 API calls | **Not met.** ~22 per workspace-pair boot before background work. Down from ~135, but the budget was 10. |
| 2. Reconnect is O(1) | **Met** in unit tests (3 vs 300 channels, identical call list). Not verified against a real outage — see *Not verified* below. |
| 3. Zero `users.list` | **Met**, structurally. |
| 4. Zero `conversations.list` | **Met**, structurally. |
| 5. ≤ 1 `conversations.history` at boot | **Not met**: 3. One is the `conversations.view` fallback, the others are the reconnect refresh of the active channel firing on first connect. |

## The cold-cache regression, in detail

Reproduce: copy `~/.local/share/slk/tokens` into a temp `XDG_DATA_HOME`, leave
the cache absent, boot.

```
API requests: 40604 total across 18 endpoints
  40523  users.info
     42  conversations.members
```

42 `conversations.members` responses covering 40,527 distinct members, and one
`users.info` started for each. The chain:

1. `membership.Manager.backgroundFetch` fetches `conversations.members` for a
   channel and then calls `resolver.Request(id)` for **every member**
   (`internal/slack/membership/manager.go:165`).
2. `userResolver.Request` short-circuits when the user is already in the cache
   (`cmd/slk/main.go:318`). Its own comment says exactly why that gate is there:
   *"without this, every channel-switch refetches users.info for each member,
   which is O(channel-size) API calls per switch (a 1000-member shared channel =
   1000 calls)"*.
3. On a miss it spawns **one goroutine per user**, unbounded.

The `users.list` sweep used to fill that cache in ~50 pages before the
membership manager got going, so the gate hit on nearly every call. Deleting the
sweep did not create this fan-out; it removed the thing that hid it. The gate is
still correct — it is the *population strategy behind it* that Task 8 removed
without replacing.

Two things are wrong and both need fixing:

- **Unbounded.** One goroutine and one request per user, no worker pool, no rate
  limit. Even the right number of users would arrive as a burst — and the burst
  is the fingerprint, independent of how many requests survive the queue.
- **Eager.** These are the members of every channel the manager touches, not the
  authors of messages on screen. The mention picker shows at most 7 rows.

The shape of the fix is already in the tree and unused for this purpose:
`edge.UsersInfo(ctx, updatedIDs)` takes `{id: version}` in batches and is what
`internal/bootstrap` already uses for revalidation. Routing resolver misses
through it turns N requests into N/50 — but the real fix is to stop asking for
users nobody is about to look at, which is a design question, not a batching
one.

**This is Phase 2c's first task, and it blocks any Grid test.**

## What contradicted the plan

- **Task 10's hook was wrong on the first attempt.** The plan says to move the
  subscription fetch to "the first open of the Threads view", and the obvious
  hook — the threads list fetcher — also runs on workspace-ready, because the
  sidebar draws a Threads unread badge before the view is ever opened. Hanging
  the network call there left `subscriptions.thread.getView` at 60 per boot. It
  took a measured run to notice; the unit tests were all green. The fix was a
  separate `ThreadService.EnsureSubscriptions` called only from the
  `ThreadsViewActivatedMsg` arm, with a test asserting workspace-ready does
  *not* trigger it.
- **The plan's Task 9 replacement said "the active channel only, via the normal
  open path with cached_latest_updates".** The normal open path
  (`fetchChannelMessages` → `GetHistory`) does not use `cached_latest_updates`;
  only `internal/bootstrap`'s fallback does. The reconnect refresh goes through
  the normal path as-is. That is a bytes optimisation left undone, not a call
  count one — it is still exactly one request.
- **`cache.BackfillCandidates` was deleted; `ChannelsWithMessages` was kept.**
  The plan left the choice open. `BackfillCandidates` existed only to enumerate
  the fan-out's candidates and means nothing without it; `ChannelsWithMessages`
  is a plain query with no fan-out semantics baked in.
- **`GetHistorySince` was kept** despite losing its only caller. The
  per-channel primitive is not the bug; the loop over channels was.

## Not verified

Four things need a human at a terminal and are **not** claimed:

1. **Names resolve for authors you have never DMed** (Task 8). The cold-cache
   finding above suggests they resolve *aggressively*, but the rendered result
   was never looked at.
2. **The Threads view populates on first open** (Task 10). The trigger is
   tested; the view is not.
3. **The finder shows channels you have not joined, and a four-character burst
   produces one or two `edge:channels/search` rather than four** (Task 11a). The
   debounce is unit-tested against synthesised ticks; no real keystroke has gone
   through it.
4. **A real outage → reconnect issues a small constant number of calls**
   (Task 9). First-connect exercises the same handler and was measured; a
   genuine WebSocket drop was not.

Also not done: the fresh-profile cold boot the plan asks for. It needs
interactive re-auth, which cannot be driven headlessly.

## Gate

Green after every commit:

```
go build ./... && go vet ./... && go test ./... -race && golangci-lint run ./...
```

`golangci-lint`: 0 issues. Network isolation confirmed:

```
unshare -rn sh -c 'ip link set lo up && go test ./...'   # all ok
```

Every mutation listed in the plan for Tasks 8-11a was run and killed its test;
the two worth naming are (a) re-adding `ListThreadSubscriptions` to the
reconnect client interface, and (b) fanning the reconnect refresh out over every
cached channel — the exact bug Task 9 exists to prevent. Both fail loudly.

One mutation initially failed for the wrong reason: adding `GetUsersContext`
back to `SlackAPI` broke the mock's compile before the assertion could run. It
was re-run with the mock method restored so the reflection assertion was what
failed. A mutation that kills a test by breaking the build has proved nothing.

## Open items, in priority order

1. **The cold-cache `users.info` fan-out.** Blocks Grid testing.
2. **Task 11b**: the mention picker still filters the in-memory roster locally
   rather than calling `edge.UsersSearch`. Not a regression — it is what it
   always did — and less urgent than it looks, because the roster it filters is
   now much smaller.
3. **`users.conversations` (3 per boot)**: `Client.GetChannels` still enumerates
   the joined channel list, and `bootstrap`'s own regression guard lists
   `users.conversations` as forbidden. `userBoot` already returns the same
   conversations. Nothing in Phase 2b's plan deletes it.
4. **`client.counts` (6 per boot)**: `bootstrap.Run` and the first-connect
   reconnect handler both call it, per workspace. One of the two is redundant.
5. **Boot budget**: ~22 calls against a criterion of 10.
