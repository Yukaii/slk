# Desktop App Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace slk's manual browser `xoxc`/`d` onboarding with automatic extraction from the Slack desktop app (read + decrypt the `d` cookie and workspace list off disk, mint a token per workspace, keep tokens fresh).

**Architecture:** A new self-contained `internal/slackdesktop` package reads the desktop app's cookie store (sqlite + OS keyring/keychain/DPAPI) and workspace list (`storage/root-state.json`). `internal/slack` gains `MintToken` (GET `<domain>.slack.com`, scrape `api_token`). Onboarding enumerates workspaces and multi-selects; startup re-mints all tokens from the live cookie before any client connects.

**Tech Stack:** Go, `modernc.org/sqlite` (pure-Go), `r00t2.io/gosecret` (Linux), `github.com/keybase/go-keychain` (macOS), `github.com/billgraziano/dpapi` (Windows), `golang.org/x/crypto/pbkdf2`, `charm.land/huh/v2`.

**Spec:** `docs/superpowers/specs/2026-07-23-desktop-app-auth-design.md`

**Attribution:** Cookie-extraction logic adapted from `github.com/rneatherway/slack` (MIT). Keep a NOTICE in the package doc.

**Module path:** `github.com/gammons/slk`. The Slack package lives at `internal/slack` but its package name is `slackclient`.

---

## Scope / Deferred

- **Included:** cross-platform cookie extraction; workspace enumeration; token minting; multi-select onboarding; startup re-mint; `postForm` retry-on-`invalid_auth`; `Client.Reauth()`; docs.
- **Deferred (follow-up):** intercepting every slack-go Web API call for mid-session refresh. Startup re-mint + `postForm` retry + WebSocket reauth cover the realistic cases; a slack-go call that hits `invalid_auth` mid-session surfaces today's re-auth message until the next launch (which re-mints).

---

## File Structure

- Create: `internal/slackdesktop/doc.go` — package doc + attribution.
- Create: `internal/slackdesktop/errors.go` — typed errors.
- Create: `internal/slackdesktop/configdir.go` — locate the Slack desktop dir.
- Create: `internal/slackdesktop/workspaces.go` — parse `root-state.json`.
- Create: `internal/slackdesktop/cookiedb.go` — read the `d` row from the Cookies sqlite.
- Create: `internal/slackdesktop/decrypt.go` — CBC (unix) + GCM (windows) decryptors, prefix/padding helpers.
- Create: `internal/slackdesktop/password_linux.go` — libsecret via gosecret (build tag).
- Create: `internal/slackdesktop/password_darwin.go` — keychain (build tag).
- Create: `internal/slackdesktop/password_windows.go` — DPAPI'd Local State key (build tag).
- Create: `internal/slackdesktop/cookie.go` — `Cookie()` orchestration (per-GOOS key selection).
- Create: `internal/slackdesktop/*_test.go` — unit tests.
- Modify: `internal/slack/auth.go` — add `Domain` to `Token`.
- Create: `internal/slack/mint.go` — `MintToken`.
- Create: `internal/slack/mint_test.go`.
- Modify: `internal/slack/client.go` — `postForm` retry + `Reauth()`.
- Create: `internal/slack/reauth_test.go`.
- Modify: `cmd/slk/onboarding.go` — rewrite `addWorkspace()`.
- Create: `cmd/slk/onboarding_core.go` — testable non-interactive core.
- Create: `cmd/slk/onboarding_core_test.go`.
- Create: `cmd/slk/remint.go` — startup re-mint helper.
- Create: `cmd/slk/remint_test.go`.
- Modify: `cmd/slk/main.go` — call re-mint after `tokenStore.List()`.
- Modify: `README.md`, `wiki/Setup.md` — new one-step flow.

---

## Task 1: `slackdesktop` package skeleton + typed errors

**Files:**
- Create: `internal/slackdesktop/doc.go`
- Create: `internal/slackdesktop/errors.go`

- [ ] **Step 1: Write `doc.go`**

```go
// Package slackdesktop reads the Slack desktop application's on-disk state:
// the session `d` cookie (from the app's encrypted Cookies sqlite DB) and the
// list of signed-in workspaces (from storage/root-state.json).
//
// The cookie-decryption logic is adapted from github.com/rneatherway/slack
// (MIT License, Copyright (c) rneatherway). Original notice retained.
package slackdesktop
```

- [ ] **Step 2: Write `errors.go`**

```go
package slackdesktop

import "errors"

var (
	ErrDesktopNotFound = errors.New("slack desktop app config directory not found")
	ErrNotSignedIn     = errors.New("no slack workspaces are signed in")
	ErrCookieDBMissing = errors.New("slack cookie database not found")
	ErrKeyringLocked   = errors.New("system keyring is locked")
	ErrNoSecretService = errors.New("no system secret service available")
	ErrDecryptFailed   = errors.New("failed to decrypt slack session cookie")
)
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/slackdesktop/`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/slackdesktop/doc.go internal/slackdesktop/errors.go
git commit -m "feat(slackdesktop): package skeleton and typed errors"
```

---

## Task 2: Locate the Slack desktop config dir

**Files:**
- Create: `internal/slackdesktop/configdir.go`
- Test: `internal/slackdesktop/configdir_test.go`

- [ ] **Step 1: Write the failing test**

```go
package slackdesktop

import (
	"path/filepath"
	"testing"
)

func TestConfigDirForOS(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"linux", map[string]string{"HOME": "/home/x"}, "/home/x/.config/Slack"},
		{"linux", map[string]string{"HOME": "/home/x", "XDG_CONFIG_DIR": "/cfg"}, "/cfg/Slack"},
		{"windows", map[string]string{"APPDATA": `C:\Users\x\AppData\Roaming`}, filepath.Join(`C:\Users\x\AppData\Roaming`, "Slack")},
	}
	for _, c := range cases {
		got := configDirForOS(c.goos, env(c.env), func(string) bool { return false })
		if got != c.want {
			t.Errorf("configDirForOS(%s) = %q, want %q", c.goos, got, c.want)
		}
	}
}

func TestConfigDirForOSDarwinPrefersFirstExisting(t *testing.T) {
	home := "/Users/x"
	first := filepath.Join(home, "Library", "Application Support", "Slack")
	got := configDirForOS("darwin", func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, func(p string) bool { return p == first })
	if got != first {
		t.Errorf("darwin config dir = %q, want %q", got, first)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slackdesktop/ -run TestConfigDirForOS -v`
Expected: FAIL — `configDirForOS` undefined.

- [ ] **Step 3: Write `configdir.go`**

```go
package slackdesktop

import (
	"os"
	"path/filepath"
	"runtime"
)

// configDirForOS computes the Slack desktop config dir for a given OS.
// getenv and exists are injected for testability.
func configDirForOS(goos string, getenv func(string) string, exists func(string) bool) string {
	switch goos {
	case "windows":
		return filepath.Join(getenv("APPDATA"), "Slack")
	case "darwin":
		home := getenv("HOME")
		first := filepath.Join(home, "Library", "Application Support", "Slack")
		second := filepath.Join(home, "Library", "Containers", "com.tinyspeck.slackmacgap", "Data", "Library", "Application Support", "Slack")
		if exists(first) {
			return first
		}
		return second
	default: // linux and others
		if x := getenv("XDG_CONFIG_DIR"); x != "" {
			return filepath.Join(x, "Slack")
		}
		return filepath.Join(getenv("HOME"), ".config", "Slack")
	}
}

// ConfigDir returns the Slack desktop config dir, or ErrDesktopNotFound if it
// does not exist on disk.
func ConfigDir() (string, error) {
	dir := configDirForOS(runtime.GOOS, os.Getenv, func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && info.IsDir()
	})
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", ErrDesktopNotFound
	}
	return dir, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slackdesktop/ -run TestConfigDir -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slackdesktop/configdir.go internal/slackdesktop/configdir_test.go
git commit -m "feat(slackdesktop): locate desktop config dir per-OS"
```

---

## Task 3: Parse workspaces from root-state.json

**Files:**
- Create: `internal/slackdesktop/workspaces.go`
- Test: `internal/slackdesktop/workspaces_test.go`
- Create: `internal/slackdesktop/testdata/root-state.json`

- [ ] **Step 1: Write the fixture `testdata/root-state.json`**

```json
{
  "workspaces": {
    "T054JFC9S2Z": { "name": "Truelist", "url": "https://truelist-workspace.slack.com/", "domain": "truelist-workspace", "id": "T054JFC9S2Z" },
    "TUJLNE62Z":   { "name": "UserEvidence", "url": "https://userevidence.slack.com/", "domain": "userevidence", "id": "TUJLNE62Z" }
  }
}
```

- [ ] **Step 2: Write the failing test**

```go
package slackdesktop

import (
	"os"
	"testing"
)

func TestParseWorkspaces(t *testing.T) {
	data, err := os.ReadFile("testdata/root-state.json")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := parseWorkspaces(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(ws))
	}
	// Sorted by name for stable output.
	if ws[0].Name != "Truelist" || ws[0].Domain != "truelist-workspace" || ws[0].TeamID != "T054JFC9S2Z" {
		t.Errorf("ws[0] = %+v", ws[0])
	}
}

func TestParseWorkspacesEmpty(t *testing.T) {
	_, err := parseWorkspaces([]byte(`{"workspaces":{}}`))
	if err != ErrNotSignedIn {
		t.Errorf("got %v, want ErrNotSignedIn", err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/slackdesktop/ -run TestParseWorkspaces -v`
Expected: FAIL — `parseWorkspaces` undefined.

- [ ] **Step 4: Write `workspaces.go`**

```go
package slackdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Workspace is one signed-in workspace from the Slack desktop app.
type Workspace struct {
	Name   string
	Domain string // subdomain under .slack.com
	TeamID string
}

type rootState struct {
	Workspaces map[string]struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Domain string `json:"domain"`
		ID     string `json:"id"`
	} `json:"workspaces"`
}

func parseWorkspaces(data []byte) ([]Workspace, error) {
	var rs rootState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, err
	}
	var out []Workspace
	for _, w := range rs.Workspaces {
		if w.Domain == "" || w.ID == "" {
			continue
		}
		out = append(out, Workspace{Name: w.Name, Domain: w.Domain, TeamID: w.ID})
	}
	if len(out) == 0 {
		return nil, ErrNotSignedIn
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Workspaces reads and parses the desktop app's signed-in workspace list.
func Workspaces() ([]Workspace, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "storage", "root-state.json"))
	if err != nil {
		return nil, ErrNotSignedIn
	}
	return parseWorkspaces(data)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/slackdesktop/ -run TestParseWorkspaces -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/slackdesktop/workspaces.go internal/slackdesktop/workspaces_test.go internal/slackdesktop/testdata/root-state.json
git commit -m "feat(slackdesktop): parse signed-in workspaces from root-state.json"
```

---

## Task 4: Read the `d` cookie row from the Cookies sqlite DB

**Files:**
- Create: `internal/slackdesktop/cookiedb.go`
- Test: `internal/slackdesktop/cookiedb_test.go`

- [ ] **Step 1: Write the failing test** (builds a temp sqlite fixture)

```go
package slackdesktop

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func makeCookieDB(t *testing.T, plain string, enc []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Cookies")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cookies VALUES (?,?,?,?)`, ".slack.com", "d", plain, enc); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCookieRow(t *testing.T) {
	path := makeCookieDB(t, "", []byte{0x01, 0x02, 0x03})
	plain, enc, err := readCookieRow(path)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "" {
		t.Errorf("plain = %q, want empty", plain)
	}
	if len(enc) != 3 {
		t.Errorf("enc len = %d, want 3", len(enc))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slackdesktop/ -run TestReadCookieRow -v`
Expected: FAIL — `readCookieRow` undefined.

- [ ] **Step 3: Write `cookiedb.go`**

```go
package slackdesktop

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

const cookieQuery = `SELECT value, encrypted_value FROM cookies WHERE host_key=".slack.com" AND name="d"`

// readCookieRow returns the plaintext value (usually empty) and the encrypted
// value blob for the Slack `d` cookie.
func readCookieRow(dbPath string) (string, []byte, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", nil, ErrCookieDBMissing
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", nil, err
	}
	defer db.Close()

	var plain string
	var enc []byte
	if err := db.QueryRow(cookieQuery).Scan(&plain, &enc); err != nil {
		return "", nil, err
	}
	return plain, enc, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slackdesktop/ -run TestReadCookieRow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slackdesktop/cookiedb.go internal/slackdesktop/cookiedb_test.go
git commit -m "feat(slackdesktop): read d cookie row from Cookies sqlite"
```

---

## Task 5: Decryptors (CBC unix + GCM windows) and helpers

**Files:**
- Create: `internal/slackdesktop/decrypt.go`
- Test: `internal/slackdesktop/decrypt_test.go`

- [ ] **Step 1: Write the failing test** (round-trip encrypt→decrypt for CBC; prefix/padding helpers)

```go
package slackdesktop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"

	"golang.org/x/crypto/pbkdf2"
	"crypto/sha1"
)

func cbcEncrypt(t *testing.T, plaintext, password []byte, rounds int) []byte {
	t.Helper()
	dk := pbkdf2.Key(password, []byte("saltysalt"), rounds, 16, sha1.New)
	block, _ := aes.NewCipher(dk)
	iv := bytes.Repeat([]byte{' '}, 16)
	pad := 16 - len(plaintext)%16
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out
}

func TestDecryptCBC(t *testing.T) {
	pw := []byte("peanuts")
	want := []byte("xoxd-secret-value")
	enc := cbcEncrypt(t, want, pw, 1)
	got, err := decryptCBC(enc, pw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoveDomainHashPrefix(t *testing.T) {
	// 32-byte prefix + payload
	prefix := slackDomainHashPrefixes[0]
	in := append(append([]byte{}, prefix...), []byte("xoxd-abc")...)
	if got := removeDomainHashPrefix(in); string(got) != "xoxd-abc" {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slackdesktop/ -run 'TestDecryptCBC|TestRemoveDomainHashPrefix' -v`
Expected: FAIL — undefined `decryptCBC`, `slackDomainHashPrefixes`, `removeDomainHashPrefix`.

- [ ] **Step 3: Write `decrypt.go`** (adapted from gh-slack `unix_decryptor.go`, `windows_decryptor.go`, `cookie.go`)

```go
package slackdesktop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// slackDomainHashPrefixes are the Chromium SHA256 domain-hash prefixes that
// precede the decrypted value for slack.com / .slack.com.
var slackDomainHashPrefixes = [][]byte{
	{3, 202, 236, 172, 132, 247, 212, 240, 217, 211, 68, 226, 103, 153, 245, 64, 85, 68, 2, 183, 83, 182, 186, 218, 14, 102, 237, 62, 231, 241, 231, 142},
	{145, 28, 115, 68, 173, 92, 42, 78, 104, 243, 5, 63, 24, 206, 51, 190, 31, 169, 160, 244, 247, 106, 147, 228, 60, 68, 92, 134, 105, 199, 162, 120},
}

func removeDomainHashPrefix(value []byte) []byte {
	for _, p := range slackDomainHashPrefixes {
		if bytes.HasPrefix(value, p) {
			return value[len(p):]
		}
	}
	return value
}

// decryptCBC decrypts a v10/v11 Chromium cookie value (Linux/macOS) using a
// PBKDF2-SHA1 key (salt "saltysalt", 16 bytes) and a 16-space IV. `value`
// must already have its 3-byte version prefix stripped.
func decryptCBC(value, password []byte, rounds int) ([]byte, error) {
	if len(value) == 0 || len(value)%16 != 0 {
		return nil, fmt.Errorf("%w: bad ciphertext length %d", ErrDecryptFailed, len(value))
	}
	dk := pbkdf2.Key(password, []byte("saltysalt"), rounds, 16, sha1.New)
	block, err := aes.NewCipher(dk)
	if err != nil {
		return nil, err
	}
	iv := bytes.Repeat([]byte{' '}, 16)
	out := make([]byte, len(value))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, value)
	// Strip PKCS#7 padding.
	n := int(out[len(out)-1])
	if n <= 0 || n > 16 || n > len(out) {
		return nil, fmt.Errorf("%w: bad padding", ErrDecryptFailed)
	}
	return removeDomainHashPrefix(out[:len(out)-n]), nil
}

// decryptGCM decrypts a v10 Chromium cookie value (Windows) using an
// AES-256-GCM key. `value` must already have its 3-byte version prefix
// stripped; nonce is the first 12 bytes.
func decryptGCM(value, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < 12 {
		return nil, fmt.Errorf("%w: short gcm value", ErrDecryptFailed)
	}
	out, err := gcm.Open(nil, value[:12], value[12:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	return removeDomainHashPrefix(out), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slackdesktop/ -run 'TestDecryptCBC|TestRemoveDomainHashPrefix' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slackdesktop/decrypt.go internal/slackdesktop/decrypt_test.go
git commit -m "feat(slackdesktop): AES-CBC/GCM cookie decryptors"
```

---

## Task 6: Per-OS password sources (build-tagged)

**Files:**
- Create: `internal/slackdesktop/password_linux.go`
- Create: `internal/slackdesktop/password_darwin.go`
- Create: `internal/slackdesktop/password_windows.go`

> These wrap OS keyrings and cannot be unit-tested in CI without a live
> secret store. Each returns `([]byte, error)` and maps failures to typed
> errors. macOS/Windows are community-validated.

- [ ] **Step 1: Write `password_linux.go`** (adapted from gh-slack `cookie_password_linux.go`; adds typed errors)

```go
//go:build linux

package slackdesktop

import (
	"strings"

	"r00t2.io/gosecret"
)

// keyringPassword fetches the "Slack Safe Storage" password from the
// libsecret-backed Secret Service.
func keyringPassword() ([]byte, error) {
	service, err := gosecret.NewService()
	if err != nil {
		if strings.Contains(err.Error(), "not provided") || strings.Contains(err.Error(), "no such") {
			return nil, ErrNoSecretService
		}
		return nil, ErrNoSecretService
	}
	defer service.Close()

	attrs := map[string]string{
		"xdg:schema":  "chrome_libsecret_os_crypt_password_v2",
		"application": "Slack",
	}
	unlocked, locked, err := service.SearchItems(attrs)
	if err != nil {
		return nil, err
	}
	if len(unlocked) == 0 {
		if len(locked) > 0 {
			return nil, ErrKeyringLocked
		}
		// Try the v1 schema before giving up.
		attrs["xdg:schema"] = "chrome_libsecret_os_crypt_password_v1"
		unlocked, locked, err = service.SearchItems(attrs)
		if err != nil {
			return nil, err
		}
		if len(unlocked) == 0 {
			if len(locked) > 0 {
				return nil, ErrKeyringLocked
			}
			return nil, ErrNoSecretService
		}
	}
	return unlocked[0].Secret.Value, nil
}
```

- [ ] **Step 2: Write `password_darwin.go`** (keychain "Slack Safe Storage")

```go
//go:build darwin

package slackdesktop

import "github.com/keybase/go-keychain"

func keyringPassword() ([]byte, error) {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService("Slack Safe Storage")
	q.SetMatchLimit(keychain.MatchLimitOne)
	q.SetReturnData(true)
	results, err := keychain.QueryItem(q)
	if err != nil {
		return nil, ErrNoSecretService
	}
	if len(results) == 0 {
		return nil, ErrNoSecretService
	}
	return results[0].Data, nil
}
```

- [ ] **Step 3: Write `password_windows.go`** (adapted from gh-slack `cookie_password_windows.go`)

```go
//go:build windows

package slackdesktop

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/billgraziano/dpapi"
)

// keyringPassword returns the AES-256 key from Local State (Windows). On
// Windows this key feeds AES-GCM (not PBKDF2).
func keyringPassword() ([]byte, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	bs, err := os.ReadFile(filepath.Join(dir, "Local State"))
	if err != nil {
		return nil, ErrNoSecretService
	}
	var ls struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(bs, &ls); err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(ls.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}
	if len(key) < 5 || string(key[:5]) != "DPAPI" {
		return nil, fmt.Errorf("%w: unexpected key header", ErrDecryptFailed)
	}
	decrypted, err := dpapi.DecryptBytes(key[5:])
	if err != nil {
		return nil, ErrNoSecretService
	}
	return decrypted, nil
}
```

- [ ] **Step 4: Verify Linux build compiles**

Run: `go build ./internal/slackdesktop/`
Expected: exit 0 (Linux tags active). Run `GOOS=windows go build ./internal/slackdesktop/` and `GOOS=darwin go build ./internal/slackdesktop/` to smoke-test the tagged files compile (they need the deps in go.mod — add them in Step 5 if the build reports missing modules).

- [ ] **Step 5: Add dependencies**

```bash
go get github.com/keybase/go-keychain@latest
go get github.com/billgraziano/dpapi@latest
go get r00t2.io/gosecret@v1.1.5
go get modernc.org/sqlite@latest
go mod tidy
```

- [ ] **Step 6: Commit**

```bash
git add internal/slackdesktop/password_*.go go.mod go.sum
git commit -m "feat(slackdesktop): per-OS keyring/keychain/DPAPI password sources"
```

---

## Task 7: `Cookie()` orchestration (per-GOOS key selection + v10 peanuts)

**Files:**
- Create: `internal/slackdesktop/cookie.go`
- Test: `internal/slackdesktop/cookie_test.go`

- [ ] **Step 1: Write the failing test** (inject password + fixture DB via unexported seam)

```go
package slackdesktop

import "testing"

func TestDecryptCookieValueLinuxPeanuts(t *testing.T) {
	// v10 on linux -> password "peanuts", rounds 1
	want := []byte("xoxd-linux-peanuts")
	enc := append([]byte("v10"), cbcEncrypt(t, want, []byte("peanuts"), 1)...)
	got, err := decryptCookieValue("linux", enc, func() ([]byte, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecryptCookieValueLinuxKeyring(t *testing.T) {
	want := []byte("xoxd-linux-keyring")
	enc := append([]byte("v11"), cbcEncrypt(t, want, []byte("keyring-pw"), 1)...)
	got, err := decryptCookieValue("linux", enc, func() ([]byte, error) { return []byte("keyring-pw"), nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slackdesktop/ -run TestDecryptCookieValue -v`
Expected: FAIL — `decryptCookieValue` undefined.

- [ ] **Step 3: Write `cookie.go`**

```go
package slackdesktop

import (
	"path/filepath"
	"runtime"
)

// decryptCookieValue picks the right key + algorithm for the OS and the
// value's version prefix. getPassword is the injected keyring/keychain/DPAPI
// source (may return nil for the linux v10 "peanuts" case).
func decryptCookieValue(goos string, enc []byte, getPassword func() ([]byte, error)) ([]byte, error) {
	if len(enc) < 3 {
		return nil, ErrDecryptFailed
	}
	version := string(enc[:3])
	body := enc[3:]

	switch goos {
	case "windows":
		key, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptGCM(body, key)
	case "darwin":
		pw, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptCBC(body, pw, 1003)
	default: // linux
		if version == "v10" {
			return decryptCBC(body, []byte("peanuts"), 1)
		}
		pw, err := getPassword()
		if err != nil {
			return nil, err
		}
		return decryptCBC(body, pw, 1)
	}
}

// Cookie reads and decrypts the Slack desktop `d` cookie.
func Cookie() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(dir, "Cookies")
	if runtime.GOOS == "windows" {
		dbPath = filepath.Join(dir, "Network", "Cookies")
	}
	plain, enc, err := readCookieRow(dbPath)
	if err != nil {
		return "", err
	}
	if plain != "" {
		return plain, nil
	}
	val, err := decryptCookieValue(runtime.GOOS, enc, keyringPassword)
	if err != nil {
		return "", err
	}
	return string(val), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slackdesktop/ -run TestDecryptCookieValue -v`
Expected: PASS.

- [ ] **Step 5: Manual end-to-end check (Linux maintainer machine)**

Add a temporary `cmd/slk-cookiecheck/main.go` that calls `slackdesktop.Cookie()` and prints the prefix; run it in the graphical session (or with `DBUS_SESSION_BUS_ADDRESS` set); confirm `xoxd-`. Delete the temp command before committing. (This mirrors the validated spike.)

- [ ] **Step 6: Commit**

```bash
git add internal/slackdesktop/cookie.go internal/slackdesktop/cookie_test.go
git commit -m "feat(slackdesktop): Cookie() orchestration with v10 peanuts + per-OS keys"
```

---

## Task 8: `MintToken` in `internal/slack`

**Files:**
- Create: `internal/slack/mint.go`
- Test: `internal/slack/mint_test.go`

- [ ] **Step 1: Write the failing test**

```go
package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMintTokenScrapesAPIToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("d"); c == nil || c.Value != "xoxd-abc" {
			t.Errorf("missing/incorrect d cookie: %+v", c)
		}
		w.Write([]byte(`<html>...,"api_token":"xoxc-12345",...</html>`))
	}))
	defer srv.Close()

	got, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxc-12345" {
		t.Errorf("got %q, want xoxc-12345", got)
	}
}

func TestMintTokenNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>no token here</html>`))
	}))
	defer srv.Close()
	if _, err := mintTokenAt(context.Background(), srv.Client(), srv.URL, "xoxd-abc"); err == nil {
		t.Error("expected error when api_token absent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slack/ -run TestMintToken -v`
Expected: FAIL — `mintTokenAt` undefined.

- [ ] **Step 3: Write `mint.go`**

```go
package slackclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var apiTokenRE = regexp.MustCompile(`"api_token":"([^"]+)"`)

// MintToken mints a fresh xoxc token for a workspace by loading its page with
// the desktop `d` cookie and scraping the embedded api_token. It uses a
// browser-shaped HTTP client with the cookie set.
func MintToken(ctx context.Context, domain, dCookie string) (string, error) {
	client := newCookieHTTPClient(dCookie)
	return mintTokenAt(ctx, client, fmt.Sprintf("https://%s.slack.com", domain), dCookie)
}

// mintTokenAt is the testable core: GET baseURL with the d cookie, scrape
// api_token. The cookie is attached explicitly so httptest servers (which are
// not *.slack.com) still receive it.
func mintTokenAt(ctx context.Context, client *http.Client, baseURL, dCookie string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return "", err
	}
	req.AddCookie(&http.Cookie{Name: "d", Value: dCookie})

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mint token: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := apiTokenRE.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("mint token: api_token not found (is the desktop app signed in?)")
	}
	return string(m[1]), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slack/ -run TestMintToken -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slack/mint.go internal/slack/mint_test.go
git commit -m "feat(slack): MintToken mints xoxc from desktop d cookie"
```

---

## Task 9: Add `Domain` to `Token`

**Files:**
- Modify: `internal/slack/auth.go:15-20`
- Test: `internal/slack/auth_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestTokenRoundTripIncludesDomain(t *testing.T) {
	dir := t.TempDir()
	s := NewTokenStore(dir)
	in := Token{AccessToken: "xoxc-1", Cookie: "xoxd-1", Domain: "acme", TeamID: "T1", TeamName: "Acme"}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("T1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "acme" {
		t.Errorf("Domain = %q, want acme", got.Domain)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slack/ -run TestTokenRoundTripIncludesDomain -v`
Expected: FAIL — `Token` has no field `Domain`.

- [ ] **Step 3: Add the field** — modify `internal/slack/auth.go`

```go
type Token struct {
	AccessToken string `json:"access_token"` // xoxc-... token
	Cookie      string `json:"cookie"`       // d cookie value
	Domain      string `json:"domain"`       // workspace subdomain (to re-mint)
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slack/ -run TestTokenRoundTripIncludesDomain -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slack/auth.go internal/slack/auth_test.go
git commit -m "feat(slack): add Domain to Token for re-minting"
```

---

## Task 10: Onboarding core (testable) + rewrite `addWorkspace`

**Files:**
- Create: `cmd/slk/onboarding_core.go`
- Test: `cmd/slk/onboarding_core_test.go`
- Modify: `cmd/slk/onboarding.go`

- [ ] **Step 1: Write the failing test for the core**

```go
package main

import (
	"context"
	"testing"

	slackclient "github.com/gammons/slk/internal/slack"
	"github.com/gammons/slk/internal/slackdesktop"
)

func TestBuildWorkspaceTokens(t *testing.T) {
	ws := []slackdesktop.Workspace{
		{Name: "Acme", Domain: "acme", TeamID: "T1"},
		{Name: "Beta", Domain: "beta", TeamID: "T2"},
	}
	selected := map[string]bool{"T1": true} // only Acme
	mint := func(_ context.Context, domain, cookie string) (string, error) {
		return "xoxc-" + domain, nil
	}
	toks, err := buildWorkspaceTokens(context.Background(), "xoxd-c", ws, selected, mint)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0].TeamID != "T1" || toks[0].AccessToken != "xoxc-acme" || toks[0].Domain != "acme" {
		t.Fatalf("unexpected tokens: %+v", toks)
	}
	if toks[0].Cookie != "xoxd-c" || toks[0].TeamName != "Acme" {
		t.Fatalf("unexpected token fields: %+v", toks[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/slk/ -run TestBuildWorkspaceTokens -v`
Expected: FAIL — `buildWorkspaceTokens` undefined.

- [ ] **Step 3: Write `onboarding_core.go`**

```go
package main

import (
	"context"

	slackclient "github.com/gammons/slk/internal/slack"
	"github.com/gammons/slk/internal/slackdesktop"
)

// minter matches slackclient.MintToken; injected for testing.
type minter func(ctx context.Context, domain, cookie string) (string, error)

// buildWorkspaceTokens mints a token for each selected workspace and returns
// the Token records to persist. Workspaces whose TeamID is not in `selected`
// are skipped.
func buildWorkspaceTokens(ctx context.Context, cookie string, ws []slackdesktop.Workspace, selected map[string]bool, mint minter) ([]slackclient.Token, error) {
	var out []slackclient.Token
	for _, w := range ws {
		if !selected[w.TeamID] {
			continue
		}
		tok, err := mint(ctx, w.Domain, cookie)
		if err != nil {
			return nil, err
		}
		out = append(out, slackclient.Token{
			AccessToken: tok,
			Cookie:      cookie,
			Domain:      w.Domain,
			TeamID:      w.TeamID,
			TeamName:    w.Name,
		})
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/slk/ -run TestBuildWorkspaceTokens -v`
Expected: PASS.

- [ ] **Step 5: Rewrite `addWorkspace()` in `cmd/slk/onboarding.go`**

Replace the entire function body with the desktop-driven flow. Key structure (full replacement):

```go
func addWorkspace() error {
	dataDir := xdgData()
	tokenDir := filepath.Join(dataDir, "tokens")
	tokenStore := slackclient.NewTokenStore(tokenDir)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4A9EFF")).MarginBottom(1)
	subtitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).MarginBottom(1)
	stepStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#50C878"))
	successStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#50C878")).MarginTop(1)
	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E04040"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	fmt.Println()
	fmt.Println(titleStyle.Render("slk -- Add Workspace"))
	fmt.Println(subtitleStyle.Render("Reading your signed-in workspaces from the Slack desktop app."))
	fmt.Println()

	// Read cookie + workspaces from the desktop app.
	cookie, err := slackdesktop.Cookie()
	if err != nil {
		fmt.Println(errorStyle.Render("  " + desktopErrorMessage(err)))
		return err
	}
	workspaces, err := slackdesktop.Workspaces()
	if err != nil {
		fmt.Println(errorStyle.Render("  " + desktopErrorMessage(err)))
		return err
	}

	// Multi-select (all pre-selected).
	selected := map[string]bool{}
	var opts []huh.Option[string]
	for _, w := range workspaces {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%s  (%s.slack.com)", w.Name, w.Domain), w.TeamID))
		selected[w.TeamID] = true
	}
	chosen := make([]string, 0, len(workspaces))
	for _, w := range workspaces {
		chosen = append(chosen, w.TeamID)
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Workspaces to add").
				Description("All selected by default; space to toggle, enter to confirm.").
				Options(opts...).
				Value(&chosen),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeDracula))
	if err := form.Run(); err != nil {
		return fmt.Errorf("form cancelled")
	}
	selected = map[string]bool{}
	for _, id := range chosen {
		selected[id] = true
	}

	// Mint tokens for the selected workspaces.
	fmt.Println()
	fmt.Println(stepStyle.Render("Connecting..."))
	tokens, err := buildWorkspaceTokens(context.Background(), cookie, workspaces, selected, slackclient.MintToken)
	if err != nil {
		fmt.Println(errorStyle.Render("  Failed to mint token: " + err.Error()))
		return err
	}

	// Validate each and save.
	for _, tok := range tokens {
		client := slackclient.NewClient(tok.AccessToken, tok.Cookie)
		if err := client.Connect(context.Background()); err != nil {
			fmt.Println(errorStyle.Render(fmt.Sprintf("  %s: authentication failed: %v", tok.TeamName, err)))
			return fmt.Errorf("authentication failed for %s: %w", tok.TeamName, err)
		}
		if err := tokenStore.Save(tok); err != nil {
			return fmt.Errorf("saving token for %s: %w", tok.TeamName, err)
		}

		// Append a [workspaces.<slug>] config block (best-effort).
		configPath := filepath.Join(xdgConfig(), "config.toml")
		slug := uniqueSlug(config.Slugify(tok.TeamName), existingSlugs(configPath))
		if err := appendWorkspaceConfigBlock(configPath, slug, tok.TeamID, tok.TeamName); err != nil {
			fmt.Println(dimStyle.Render("  Note: could not write config.toml: " + err.Error()))
		}
		fmt.Println(successStyle.Render("  Added ") + dimStyle.Render(tok.TeamName))
	}

	fmt.Println()
	fmt.Println(successStyle.Render(fmt.Sprintf("  %d workspace(s) added!", len(tokens))))
	fmt.Println(dimStyle.Render("  Run ") + lipgloss.NewStyle().Bold(true).Render("slk") + dimStyle.Render(" to start."))
	fmt.Println()
	return nil
}

// desktopErrorMessage maps a slackdesktop error to an actionable message.
func desktopErrorMessage(err error) string {
	switch {
	case errors.Is(err, slackdesktop.ErrDesktopNotFound):
		return "Slack desktop app not found. Install it and sign in, then retry."
	case errors.Is(err, slackdesktop.ErrNotSignedIn):
		return "No Slack workspaces are signed in. Open Slack, sign in, then retry."
	case errors.Is(err, slackdesktop.ErrCookieDBMissing):
		return "Slack is installed but has never signed in on this machine."
	case errors.Is(err, slackdesktop.ErrKeyringLocked):
		return "Your system keyring is locked. Unlock it (log in to your desktop session) and retry."
	case errors.Is(err, slackdesktop.ErrNoSecretService):
		return "No system keyring/secret service found. slk needs it to read the Slack session."
	case errors.Is(err, slackdesktop.ErrDecryptFailed):
		return "Could not decrypt the Slack session cookie. Please file an issue with your OS + Slack version."
	default:
		return "Could not read Slack desktop session: " + err.Error()
	}
}
```

Update the import block of `onboarding.go` to add `"errors"` and `"github.com/gammons/slk/internal/slackdesktop"`, and drop the now-unused `"charm.land/huh/v2/spinner"` import if no longer referenced.

- [ ] **Step 6: Run build + tests**

Run: `go build ./... && go test ./cmd/slk/ -run TestBuildWorkspaceTokens -v`
Expected: build clean; test PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/slk/onboarding.go cmd/slk/onboarding_core.go cmd/slk/onboarding_core_test.go
git commit -m "feat(onboarding): auto-detect workspaces from the desktop app"
```

---

## Task 11: Startup re-mint

**Files:**
- Create: `cmd/slk/remint.go`
- Test: `cmd/slk/remint_test.go`
- Modify: `cmd/slk/main.go:596-607`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"testing"

	slackclient "github.com/gammons/slk/internal/slack"
)

func TestRemintTokens(t *testing.T) {
	in := []slackclient.Token{
		{AccessToken: "old1", Cookie: "c-old", Domain: "acme", TeamID: "T1", TeamName: "Acme"},
	}
	saved := map[string]slackclient.Token{}
	out := remintTokens(context.Background(), in,
		func() (string, error) { return "c-new", nil },
		func(_ context.Context, domain, cookie string) (string, error) { return "xoxc-" + domain, nil },
		func(tok slackclient.Token) error { saved[tok.TeamID] = tok; return nil },
	)
	if out[0].AccessToken != "xoxc-acme" || out[0].Cookie != "c-new" {
		t.Fatalf("token not refreshed: %+v", out[0])
	}
	if saved["T1"].AccessToken != "xoxc-acme" {
		t.Fatalf("refreshed token not persisted: %+v", saved["T1"])
	}
}

func TestRemintTokensKeepsOldOnMintFailure(t *testing.T) {
	in := []slackclient.Token{{AccessToken: "old1", Cookie: "c-old", Domain: "acme", TeamID: "T1"}}
	out := remintTokens(context.Background(), in,
		func() (string, error) { return "", context.Canceled }, // cookie read fails
		func(_ context.Context, _, _ string) (string, error) { return "should-not-be-used", nil },
		func(slackclient.Token) error { return nil },
	)
	if out[0].AccessToken != "old1" {
		t.Fatalf("expected fallback to cached token, got %+v", out[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/slk/ -run TestRemint -v`
Expected: FAIL — `remintTokens` undefined.

- [ ] **Step 3: Write `remint.go`**

```go
package main

import (
	"context"
	"log"

	slackclient "github.com/gammons/slk/internal/slack"
)

// remintTokens refreshes every token's xoxc from the live desktop cookie. On
// any failure for a given token it keeps the cached token (offline-friendly).
// cookieFn is read once up front; mintFn/saveFn are injected for testing.
func remintTokens(
	ctx context.Context,
	tokens []slackclient.Token,
	cookieFn func() (string, error),
	mintFn func(ctx context.Context, domain, cookie string) (string, error),
	saveFn func(slackclient.Token) error,
) []slackclient.Token {
	cookie, err := cookieFn()
	if err != nil {
		log.Printf("remint: could not read desktop cookie, using cached tokens: %v", err)
		return tokens
	}
	out := make([]slackclient.Token, len(tokens))
	copy(out, tokens)
	for i := range out {
		if out[i].Domain == "" {
			continue // legacy token without a domain; cannot re-mint
		}
		newTok, err := mintFn(ctx, out[i].Domain, cookie)
		if err != nil {
			log.Printf("remint: %s: %v (keeping cached token)", out[i].TeamName, err)
			continue
		}
		out[i].AccessToken = newTok
		out[i].Cookie = cookie
		if err := saveFn(out[i]); err != nil {
			log.Printf("remint: %s: save failed: %v", out[i].TeamName, err)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/slk/ -run TestRemint -v`
Expected: PASS.

- [ ] **Step 5: Wire into `main.go`** — after the token-load block (currently ends at line 607, `tokens` populated), insert:

```go
	// Re-mint tokens from the live desktop cookie so every launch starts
	// with fresh xoxc tokens (they expire; the desktop cookie is the source
	// of truth). Falls back to cached tokens when offline / desktop absent.
	tokens = remintTokens(ctx, tokens,
		slackdesktop.Cookie,
		slackclient.MintToken,
		tokenStore.Save,
	)
```

Add `"github.com/gammons/slk/internal/slackdesktop"` to `main.go` imports. Note: `ctx` is declared later at line 623 in the current file — move that `ctx := context.Background()` declaration above this block, or use `context.Background()` inline here.

- [ ] **Step 6: Build + test**

Run: `go build ./... && go test ./cmd/slk/ -run TestRemint -v`
Expected: build clean; PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/slk/remint.go cmd/slk/remint_test.go cmd/slk/main.go
git commit -m "feat: re-mint tokens from desktop cookie on startup"
```

---

## Task 12: `postForm` retry-on-invalid_auth + `Client.Reauth`

**Files:**
- Modify: `internal/slack/client.go` (`postForm` ~1309; add `Reauth`)
- Test: `internal/slack/reauth_test.go`

- [ ] **Step 1: Write the failing test**

```go
package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPostFormRetriesOnInvalidAuth(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient("xoxc-old", "xoxd-1")
	c.apiBaseURL = srv.URL + "/"
	c.httpClient = srv.Client()
	c.reauth = func(ctx context.Context) error { c.token = "xoxc-new"; return nil }

	body, err := c.postForm(context.Background(), "users.prefs.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls (retry), got %d", calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slack/ -run TestPostFormRetriesOnInvalidAuth -v`
Expected: FAIL — `c.reauth` field undefined (and no retry logic).

- [ ] **Step 3: Add a `reauth` field, `Reauth`, and retry logic**

In the `Client` struct (client.go, near the other fields) add:

```go
	// reauth, when set, re-mints the token and rebuilds the inner api
	// client. Called once on an invalid_auth response before retrying.
	reauth func(ctx context.Context) error
```

Add the method:

```go
// SetReauth installs the mid-session re-authentication hook. cookieFn reads
// the live desktop cookie; the client re-mints for the given domain.
func (c *Client) SetReauth(domain string, cookieFn func() (string, error)) {
	c.reauth = func(ctx context.Context) error {
		cookie, err := cookieFn()
		if err != nil {
			return err
		}
		tok, err := MintToken(ctx, domain, cookie)
		if err != nil {
			return err
		}
		c.token = tok
		c.cookie = cookie
		c.httpClient = newCookieHTTPClient(cookie)
		c.api = slack.New(c.token, slack.OptionHTTPClient(c.httpClient), slack.OptionAPIURL(c.apiBaseURL))
		return nil
	}
}
```

Refactor `postForm` (client.go:1309) so its HTTP round-trip lives in an inner closure, and wrap it with a single retry when the response body contains `"error":"invalid_auth"` and `c.reauth != nil`:

```go
func (c *Client) postForm(ctx context.Context, method string, form url.Values) ([]byte, error) {
	do := func() ([]byte, error) {
		body := url.Values{"token": {c.token}}
		for k, vs := range form {
			body[k] = vs
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.apiBaseURL+method, strings.NewReader(body.Encode()))
		if err != nil {
			return nil, fmt.Errorf("creating %s request: %w", method, err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		httpClient := c.httpClient
		if httpClient == nil {
			httpClient = newCookieHTTPClient(c.cookie)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("calling %s: %w", method, err)
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

	out, err := do()
	if err != nil {
		return nil, err
	}
	if c.reauth != nil && bytes.Contains(out, []byte(`"error":"invalid_auth"`)) {
		if rerr := c.reauth(ctx); rerr == nil {
			return do()
		}
	}
	return out, nil
}
```

Ensure `bytes` and `io` are imported in client.go (add if missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/slack/ -run TestPostFormRetriesOnInvalidAuth -v`
Expected: PASS.

- [ ] **Step 5: Wire `SetReauth` where clients are constructed** — in `cmd/slk/main.go` after each `slackclient.NewClient(tok.AccessToken, tok.Cookie)` + `Connect` (lines ~1762, ~4003, ~4043), add:

```go
		client.SetReauth(tok.Domain, slackdesktop.Cookie)
```

(Use the matching loop variable name at each site: `token`/`tok`.)

- [ ] **Step 6: Build + full test**

Run: `go build ./... && go test ./internal/slack/ -v`
Expected: build clean; PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/slack/client.go internal/slack/reauth_test.go cmd/slk/main.go
git commit -m "feat(slack): re-mint and retry on mid-session invalid_auth (postForm + WS hook)"
```

---

## Task 13: Update docs

**Files:**
- Modify: `README.md` (Setup ~62-71, Enterprise Grid ~73-90)
- Modify: `wiki/Setup.md`

- [ ] **Step 1: Rewrite README Setup section**

Replace the DevTools framing with:

```markdown
## Setup

slk reads your session directly from the **Slack desktop app** — no DevTools,
no tokens to copy. Make sure you're signed in to the desktop app, then:

```bash
slk --add-workspace
```

slk lists the workspaces you're signed in to; pick the ones you want and
you're done.
```

- [ ] **Step 2: Rewrite the "Enterprise Grid" section**

Replace with:

```markdown
## Enterprise Grid

slk reuses the **desktop app's** existing signed-in session (the same session
your admin already sanctioned) rather than a browser session, which avoids the
session-anomaly alerts that browser-token extraction can trigger. If you're on
Enterprise Grid and still hit a sign-out or security alert after adding a
workspace, please file an issue — include your OS and Slack desktop version.
```

- [ ] **Step 3: Rewrite `wiki/Setup.md`**

Replace the `d`-cookie / `xoxc` DevTools walkthrough with the one-step desktop
flow: "1. Sign in to the Slack desktop app. 2. Run `slk --add-workspace`.
3. Select your workspaces." Keep the token-expiry section but replace its
advice with "slk re-mints tokens automatically from the desktop app; you do
not need to do anything when a token expires."

- [ ] **Step 4: Commit**

```bash
git add README.md wiki/Setup.md
git commit -m "docs: desktop-app one-step setup; drop DevTools walkthrough"
```

---

## Task 14: Full build, vet, and test sweep

- [ ] **Step 1: Cross-compile smoke test**

Run:
```bash
go build ./...
GOOS=darwin  GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: all succeed (validates the build-tagged password files + deps).

- [ ] **Step 2: Vet + test**

Run: `go vet ./... && go test ./...`
Expected: no vet errors; all tests pass.

- [ ] **Step 3: Manual integration (Linux, maintainer machine)**

In the graphical session (keyring unlocked): remove any existing token dir,
run `slk --add-workspace`, confirm the multi-select lists real workspaces,
selecting them mints tokens and connects, and `slk` starts. Confirm a second
launch still works (re-mint path).

- [ ] **Step 4: Commit any fixes, then open the PR referencing issue #5.**

```bash
git commit -am "fix: address build/test sweep findings"  # if needed
```
```
