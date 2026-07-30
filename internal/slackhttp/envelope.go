package slackhttp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// DefaultVersionTS is the fallback Slack build timestamp used before
// client.shouldReload reports the real one. Observed 2026-07-30. It is
// only a seed: Envelope.SetVersionTS replaces it on the first
// successful boot, and the value is persisted per workspace so later
// runs start current.
const DefaultVersionTS = "1785403654"

// Envelope carries the per-session values Slack's web client puts on
// every API request. One Envelope per process is correct; it is safe
// for concurrent use.
//
// Identity has two phases, mirroring the official client:
//   - Pre-boot (no team id yet): _x_id uses the "noversion-" prefix,
//     and neither _x_csid nor slack_route is sent.
//   - Post-boot (team id known): _x_id uses an 8-hex client id, and
//     _x_csid and slack_route are sent.
//
// Verified in initial-load.har: experiments.getByUser at t+3.0s has
// _x_id=noversion-… and no slack_route; sfdc.integration.listOrgs at
// t+4.6s has _x_id=741e4b14-… and slack_route=T04T4TH8W. The _x_id
// prefix and _x_csid are distinct values (741e4b14 vs U4129EELrMo).
type Envelope struct {
	clientID  string // 8 hex chars, stable for the process
	sessionID string // 11 base64url chars, stable for the process
	teamID    atomic.Value
	versionTS atomic.Value

	// lastMillis is the last epoch-millisecond handed out by RequestID.
	// See nextMillis for why _x_id needs a counter and not just a clock.
	lastMillis atomic.Int64
}

// NewEnvelope returns an Envelope in the pre-boot phase.
func NewEnvelope() *Envelope {
	e := &Envelope{
		clientID:  randHex(4),   // 4 bytes -> 8 hex chars
		sessionID: randToken(8), // 8 bytes -> 11 base64url chars
	}
	e.teamID.Store("")
	e.versionTS.Store(DefaultVersionTS)
	return e
}

// SetTeamID records the workspace id and moves the envelope into its
// post-boot phase. Ignores empty input so a failed lookup cannot
// regress the envelope to its pre-boot form mid-session.
func (e *Envelope) SetTeamID(id string) {
	if id == "" {
		return
	}
	e.teamID.Store(id)
}

// TeamID returns the workspace id, or "" pre-boot.
func (e *Envelope) TeamID() string {
	s, _ := e.teamID.Load().(string)
	return s
}

// SetVersionTS records the Slack build timestamp reported by
// client.shouldReload. Ignores empty input so a failed lookup cannot
// clobber a good value.
func (e *Envelope) SetVersionTS(ts string) {
	if ts == "" {
		return
	}
	e.versionTS.Store(ts)
}

// VersionTS returns the current build timestamp.
func (e *Envelope) VersionTS() string {
	s, _ := e.versionTS.Load().(string)
	return s
}

// SessionID returns the _x_csid value, or "" pre-boot.
func (e *Envelope) SessionID() string {
	if e.TeamID() == "" {
		return ""
	}
	return e.sessionID
}

// RequestID returns a fresh _x_id for one request. The value is unique
// for the life of the process; see nextMillis.
func (e *Envelope) RequestID() string {
	prefix := "noversion"
	if e.TeamID() != "" {
		prefix = e.clientID
	}
	ms := e.nextMillis()
	return fmt.Sprintf("%s-%d.%03d", prefix, ms/1000, ms%1000)
}

// nextMillis returns a strictly increasing epoch-millisecond value: the
// current time, or one past the previous value when the clock has not
// advanced. It is lock-free so request goroutines never contend.
//
// _x_id's wire format is <prefix>-<unix>.<millis> — three decimal places,
// pinned by the captures — so a bare clock read collides whenever two
// requests are built in the same millisecond. That is not hypothetical:
// slk issues several calls back-to-back at boot, and a plain time.Now()
// implementation duplicated on the *second* consecutive call.
//
// Note the official client does NOT guarantee uniqueness here. In
// initial-load.har, users.prefs.get and teams.trials.info both carry
// _x_id=741e4b14-1785407067.503. So duplicates are within observed
// behaviour and are not themselves a fingerprint. We avoid them anyway
// because _x_id exists to correlate a single call, and a collision
// makes slk's own traffic ambiguous for no benefit.
//
// The clamp only ever moves the value forward, and only under bursts
// exceeding one request per millisecond, so the timestamp stays within
// a few milliseconds of the wall clock in any realistic TUI workload.
func (e *Envelope) nextMillis() int64 {
	for {
		last := e.lastMillis.Load()
		next := time.Now().UnixMilli()
		if next <= last {
			next = last + 1
		}
		if e.lastMillis.CompareAndSwap(last, next) {
			return next
		}
	}
}

// TraceIDs returns a fresh (traceID, spanID) pair for one request,
// used for the _x_b3_traceid / _x_b3_spanid query params.
func (e *Envelope) TraceIDs() (string, string) {
	return randHex(16), randHex(8)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is not recoverable and not worth
		// propagating through every request; fall back to a
		// time-derived value so requests still carry a plausible id.
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8 * 8))
		}
	}
	return hex.EncodeToString(b)
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8 * 8))
		}
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
