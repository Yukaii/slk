# Desktop App Auth (auto-extract xoxc/xoxd) Design

## Problem

slk's current onboarding (`--add-workspace`) requires the user to manually
extract two secrets from a **web browser**: the `d` cookie (DevTools >
Application > Cookies) and the `xoxc-` token (a `localConfig_v2` console
snippet). This has two serious costs:

1. **Setup is too hard.** The DevTools ritual defeats most non-technical
   users; incorrect extraction is the most common setup failure.
2. **It trips enterprise security (issue #5).** Extracting and replaying a
   *browser* session token is indistinguishable from session-token theft.
   Enterprise Grid / SSO anomaly detection responds by force-reauthing all
   the user's sessions and alerting admins. slk's current mitigation (browser-
   like headers, token-in-body) fights detection at the wrong layer and
   cannot win.

## Key Insight

`gh-slack` (rneatherway) works on Enterprise Grid where slk fails. It uses the
**same credential type** (`xoxc` + `d`) but sources it differently: it reads
the `d` cookie out of the **Slack desktop app's own cookie store** and mints
the `xoxc` token by loading the workspace page. It reuses the enterprise-
*sanctioned* desktop session rather than a browser session, and notably uses a
plain Go HTTP client (no header mimicry, even sends `Authorization: Bearer`) —
disconfirming slk's "make traffic look like a browser" theory.

A local spike validated the full mechanism on Linux end-to-end:

```
d cookie  : read from ~/.config/Slack/Cookies, decrypted -> xoxd-... (v11/keyring)
xoxc token: minted via GET <domain>.slack.com               -> xoxc-...
auth.test : ok=true (team "Truelist", user "grant")
workspaces: enumerated from storage/root-state.json (name/domain/team_id)
```

## Solution

Replace the manual browser-extraction onboarding **entirely** with automatic
extraction from the Slack **desktop app**. The desktop app is a hard
requirement for slk. slk reads the desktop app's cookie + workspace list off
disk, lets the user multi-select workspaces, mints a token per workspace, and
keeps tokens fresh automatically.

### Design Decisions

- **Replace, don't augment.** The DevTools/paste flow is removed. No manual
  token entry remains (not even a break-glass env var for now).
- **Multi-select workspaces, all pre-selected.** Onboarding shows every
  signed-in workspace; the user accepts all (one Enter) or deselects some.
- **Tokens are ephemeral.** Re-mint every enabled workspace's token from the
  live cookie on startup, and auto-refresh on mid-session `invalid_auth`.
- **Targeted errors.** Every failure path yields a specific, actionable
  message. No silent failures, no generic catch-all.
- **All three platforms** (Linux/macOS/Windows) in this change. Only Linux can
  be validated locally; macOS/Windows + Enterprise Grid confirmation come from
  the community.

## New Component: `internal/slackdesktop`

A focused, self-contained package that reads the Slack desktop app's on-disk
state. Adapted from gh-slack's `internal/config` (MIT-licensed; attribution
retained in the package doc), trimmed to slk's needs and extended for the
`v10` case.

```go
package slackdesktop

// Workspace is one signed-in workspace from the desktop app.
type Workspace struct {
    Name   string // "Truelist"
    Domain string // "truelist-workspace"  (subdomain to mint against)
    TeamID string // "T054JFC9S2Z"
}

// ConfigDir returns the platform Slack desktop config dir, or a typed
// ErrDesktopNotFound if it does not exist.
func ConfigDir() (string, error)

// Workspaces parses storage/root-state.json -> signed-in workspaces.
// Returns ErrNotSignedIn if the list is empty.
func Workspaces() ([]Workspace, error)

// Cookie reads and decrypts the `d` cookie from the Cookies sqlite DB.
// Handles v11 (OS keyring/keychain/DPAPI) and v10 ("peanuts" basic store).
func Cookie() (string, error)
```

### Platform matrix

| OS      | Config dir | Cookie decrypt key | KDF |
|---------|-----------|--------------------|-----|
| Linux   | `~/.config/Slack` | libsecret schema `chrome_libsecret_os_crypt_password_v2`, app `Slack`; fallback `peanuts` for `v10` | PBKDF2-SHA1, salt `saltysalt`, 1 round, 16 bytes |
| macOS   | `~/Library/Application Support/Slack` (or Containers path) | Keychain item "Slack Safe Storage" | PBKDF2-SHA1, salt `saltysalt`, 1003 rounds, 16 bytes |
| Windows | `%APPDATA%/Slack` (Cookies at `Network/Cookies`) | `Local State` `os_crypt.encrypted_key`, DPAPI-unprotected, AES-256-GCM | n/a (GCM, not PBKDF2) |

Decryption details (Linux/macOS, AES-128-CBC):
- Strip the 3-byte `v10`/`v11` version prefix.
- IV = 16 spaces; CBC; strip PKCS#7 padding.
- Strip the 32-byte Chromium domain-hash prefix if present.

Cookie DB read: `SELECT value, encrypted_value FROM cookies WHERE
host_key=".slack.com" AND name="d"`. If `value` is non-empty, use it directly;
otherwise decrypt `encrypted_value`. The DB is opened read-only via
`modernc.org/sqlite` (no cgo).

### Typed errors

`ErrDesktopNotFound`, `ErrNotSignedIn`, `ErrCookieDBMissing`,
`ErrKeyringLocked`, `ErrNoSecretService`, `ErrDecryptFailed` — each mapped to a
specific onboarding message (see Onboarding Flow).

## Token Minting + Auto-Refresh (`internal/slack`)

New:

```go
// MintToken performs GET https://<domain>.slack.com with the d cookie and
// scrapes the api_token ("xoxc-...") from the returned page.
func MintToken(ctx context.Context, domain, cookie string) (string, error)
```

- **Startup:** for each enabled workspace, read the live cookie once and
  `MintToken` a fresh `xoxc` token; update the stored token.
- **Mid-session:** the client wraps API calls so that a `invalid_auth`
  response triggers: re-read cookie -> `MintToken` -> update store -> retry
  once. A second consecutive failure surfaces a re-auth status message.

The stored token becomes a cache; the live desktop cookie is the source of
truth. If minting fails while offline, fall back to the cached token.

## Storage Change (`internal/slack/auth.go`)

`Token` gains `Domain` (required to re-mint). Everything else — the
`{teamID}.json` layout at `~/.local/share/slk/tokens/`, `0600`/`0700` perms,
`TokenStore` API — is unchanged.

```go
type Token struct {
    AccessToken string `json:"access_token"` // xoxc-... (cache; re-minted)
    Cookie      string `json:"cookie"`       // d cookie (cache; re-read live)
    Domain      string `json:"domain"`       // NEW: "truelist-workspace"
    TeamID      string `json:"team_id"`
    TeamName    string `json:"team_name"`
}
```

## Onboarding Flow (`cmd/slk/onboarding.go`)

`addWorkspace()` is rewritten. The DevTools instructions, the `xoxc-` paste
prompt, and the `d` cookie prompt are deleted. New flow:

1. `slackdesktop.Cookie()` — read + decrypt the `d` cookie.
2. `slackdesktop.Workspaces()` — enumerate signed-in workspaces.
3. `huh` multi-select of workspaces, all pre-selected.
4. For each selected: `MintToken` + `auth.test` validation via `Connect`.
5. Save a `Token` per workspace.

On any `slackdesktop` error, print the matching targeted message and exit
non-zero:

| Error | Message |
|-------|---------|
| `ErrDesktopNotFound` | "Slack desktop app not found. Install it and sign in, then retry." |
| `ErrNotSignedIn` | "No Slack workspaces are signed in. Open Slack, sign in, then retry." |
| `ErrCookieDBMissing` | "Slack is installed but has never signed in on this machine." |
| `ErrKeyringLocked` | "Your system keyring is locked. Unlock it (log in to your desktop session) and retry." |
| `ErrNoSecretService` | "No system keyring/secret service found. slk needs it to read the Slack session." |
| `ErrDecryptFailed` | "Could not decrypt the Slack session cookie. Please file an issue with your OS + Slack version." |

## What Stays the Same

- `NewClient(xoxcToken, dCookie)` signature and all Web API / WebSocket code.
- SQLite cache, service layer, UI layer, `SlackAPI` interface.
- Token storage location, format, and permissions (aside from the new field).
- The browser-like header transport is retained (harmless; out of scope to
  remove here).

## Docs

- `README.md`: rewrite Setup to the one-step flow; drop the Enterprise-Grid
  header-mimicry framing; state the desktop app is required.
- `wiki/Setup.md`: replace the DevTools walkthrough with "sign in to the
  desktop app, run `slk --add-workspace`, pick your workspaces."

## Testing

Unit:
- Decryptor against known AES-CBC vectors (Linux/macOS) and an AES-GCM vector
  (Windows).
- `root-state.json` parser against a checked-in fixture.
- `api_token` scraper against sample page HTML (present + absent).
- Auth-refresh retry logic against a fake client returning `invalid_auth` once.

Manual integration (Linux, maintainer machine):
- `slk --add-workspace` against the live desktop app; confirm multi-select,
  minting, and `auth.test` succeed for a real workspace.

Community-assisted (cannot validate locally):
- macOS keychain + Windows DPAPI decryption paths.
- Enterprise Grid: confirm this flow does **not** trigger the issue-#5 booting.

## Dependencies

Port a small, focused package rather than vendoring `github.com/rneatherway/slack`
(which pulls glamour/markdown/websocket/etc. slk does not need). New direct
deps, all already transitively present or small:

- `modernc.org/sqlite` — pure-Go cookie DB read.
- Linux: `r00t2.io/gosecret` (libsecret via D-Bus, pure Go).
- macOS: shell out to `/usr/bin/security` (NOT a cgo binding like
  `keybase/go-keychain` — the release builds with `CGO_ENABLED=0` and
  cross-compiles darwin, which a Security.framework cgo dep would break).
- Windows: `github.com/billgraziano/dpapi` (pure Go).
- `golang.org/x/crypto/pbkdf2`.

## Out of Scope

- Removing the browser-like header transport.
- A break-glass manual-token path (may revisit if community hits environments
  with no readable keyring).
- Any change to real-time event handling or the cache.
```
