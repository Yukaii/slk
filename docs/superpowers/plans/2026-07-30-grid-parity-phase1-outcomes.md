# Grid Parity Phase 1 — Outcomes

Record of what Layer 1 of
[`specs/2026-07-30-enterprise-grid-bootstrap-design.md`](../specs/2026-07-30-enterprise-grid-bootstrap-design.md)
actually shipped, what it found that the design did not predict, and what it
deliberately left divergent. Phase 2 (Layer 2 — Bootstrap Rewrite) is built
from the spec; this file is the errata that made the spec accurate.

Kept separate from the spec because the spec is a forward-looking design for
three layers, and burying a retrospective in the middle of it makes the Layer
2 reader work harder.

## What shipped

`internal/slackhttp` grew from a header decorator into the full request
envelope, wired into `internal/slack.Client` and applied at
`BrowserTransport.RoundTrip` — one chokepoint under both slack-go and the
hand-rolled `postForm` path.

- **Headers.** Added `sec-ch-ua`, `sec-ch-ua-mobile`, `sec-ch-ua-platform`,
  `cache-control`, `pragma`, `priority`. Removed the `Referer` slk was
  sending on every API call, which the official client sends on none.
  Bumped the UA to Chrome/150 and derived the client-hint version from the
  same `chromeMajor` constant so the two cannot drift.
- **`Envelope`** — per-process telemetry identity with a pre-boot and a
  post-boot phase, mirroring the real client's `noversion-` → 8-hex `_x_id`
  transition and the appearance of `_x_csid` / `slack_route` once the team
  id is known.
- **Query envelope**, assembled by hand in the captured order, with
  different sets for the workspace API (13 params) and edgeapi (6).
- **Body envelope** — `_x_reason`, `_x_mode`, `_x_sonic`, `_x_app_name`
  appended in captured order to `x-www-form-urlencoded` bodies only.
  `_x_reason` rides a context value (`slackhttp.WithReason`) because only
  the call site knows which UI action it is serving.
- **`_x_version_ts` sourcing** — seeded per workspace from `config.toml`,
  refreshed in the background from `client.shouldReload` after connect, and
  persisted for the next run.
- **Two testdata fixtures.** `capture-evidence.json` (measured aggregates)
  and `official-request-shape.json` (the ordered shape contract), the latter
  driving `golden_test.go` as the single source of truth for param and
  header order.

## Found during implementation, not in the original plan

1. **The WebSocket upgrade was sending headers real Chrome omits.**
   Pre-existing bug, not introduced by this work: slk sent
   `Sec-Fetch-Dest: websocket` — plus `Accept`, the rest of `Sec-Fetch-*`,
   the `sec-ch-ua*` hints and `Priority` — on the WS handshake, on the
   assumption that browsers do. They do not. Chrome's status-101 upgrade in
   `initial-load.har` and `coldboot.har` carries a strictly smaller set. A
   long-lived socket is the single most durable thing to fingerprint, so
   this was arguably worse than the `Referer`. `WebSocketHeaders()` is now
   deliberately separate from `browserHeaderPairs()`, and
   `TestGolden_WebSocketUpgradeHeaders` asserts the WS set stays strictly
   smaller.

2. **The background refresh introduced a concurrent `config.toml` writer
   race.** The per-workspace `_x_version_ts` refresh runs in a goroutine per
   workspace (`connectWorkspace`, `cmd/slk/main.go`), and every config saver
   is a read-modify-write of the whole file. Two workspaces finishing
   `client.shouldReload` at the same moment could interleave and drop one
   another's write — and the same hazard already existed between the theme
   and width savers, it had simply never been hit because those are
   user-driven and serial. Fixed with one `configWriteMu` covering all four
   savers: `saveGlobalTheme`, `saveWorkspaceTheme`, `saveWorkspaceWidth`,
   `saveWorkspaceVersionTS`. Deliberately a single package-level mutex
   rather than per-saver locks: they contend on the same file, so separate
   locks would be theatre.

3. **`_x_id` is not unique in the real client.** The plan implicitly assumed
   a unique request id. In `initial-load.har`, 53 requests produced 52
   distinct values, with `741e4b14-1785407067.503` sent twice 68 ms apart;
   across all captures, 163 requests produced 160 distinct values. The
   client timestamps at call-composition time with no uniqueness clamp. An
   always-unique sequence where Chrome shows occasional same-millisecond
   collisions is itself a distributional signal, so `Envelope.RequestID`
   deliberately does *not* deduplicate. The first implementation in this
   phase carried a `lastMillis` counter to guarantee uniqueness, following
   the plan's own test; `3088184` measured the captures and tore it out,
   testing the *format* instead.

4. **Param order turned out to be a contract, not a detail.** 0 of 163
   workspace-API requests carried alphabetically sorted params.
   `url.Values.Encode()` sorts, so the obvious implementation would have
   given every slk request a perfectly alphabetized query string — a
   cleaner, more stable signature than the one being removed. The transport
   assembles queries and bodies by hand as a result.

## Known residual divergences

Shipped knowingly. Each is a measured, still-separable difference.

| Divergence | Official | slk | Why deferred |
|---|---|---|---|
| Body encoding | `multipart/form-data`, 163/163 captured bodies | `application/x-www-form-urlencoded` | Re-encoding at the transport means parsing and rebuilding every body across ~50 endpoints. Real regression risk for a signal weaker than the header and param gaps. Field *order* is already correct, so it carries over when this lands. |
| `_x_b3_*` trio | present on 14–18% of requests | emitted on every post-boot request | They are per-request random values, not constants, so over-sending is far less identifying than emitting a wrong fixed value or omitting them entirely. Varying the rate is guesswork without knowing what triggers them. |
| `_x_foreground` | `true` on 145/163 (88%); varies with browser tab focus | always `true` | A TUI has no tab-focus equivalent. Omitting a param present on 88% of traffic is the larger divergence. |

None of these is addressed by Layer 2; they remain open after it ships.

## Not covered by tests

**The seed call site and the refresh-goroutine wiring in
`connectWorkspace`** (`cmd/slk/main.go`). `seedVersionTS`,
`saveWorkspaceVersionTS`, `Envelope.SetVersionTS` and `Client.ShouldReload`
are each covered directly, but the code that sequences them — seed before
`Connect`, refresh in a goroutine after, persist the result, swallow failure
— is not. `connectWorkspace` takes a live `*slackclient.Client` it constructs
itself and calls `Connect`, so there is no injectable seam without either a
live Slack connection or an interface extraction whose only consumer would be
the test. Left as a deliberate gap rather than refactoring production
structure to chase coverage; the risk is a wiring mistake (wrong order, or a
dropped error) that unit tests would not catch anyway.

Also untested by construction: the actual detection outcome. Success criterion
5 in the spec — an Enterprise Grid tester completing add-workspace, boot and
channel switching without a sign-out — is the only thing that settles whether
Layer 1 works, and it requires a volunteer who bears real cost if it fails.

## Verification at merge

```
go build ./...          clean
go vet ./...            clean
go test ./... -race     all packages ok
golangci-lint run ./... 0 issues
```

`gofmt -l` reports 30 unformatted files repo-wide; all 30 predate this branch
(verified byte-identical `gofmt -d` output against the merge-base), and this
phase deliberately reformatted none of them to keep the diff readable. Every
file this phase created is gofmt-clean.
