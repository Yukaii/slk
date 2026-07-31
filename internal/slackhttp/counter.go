package slackhttp

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Counter tallies API requests by endpoint for one process.
//
// It exists because Phase 2b's success criteria are call counts — "a
// boot issues <= 10 API calls, with zero users.list and zero
// per-channel conversations.history fan-out" — and until this, the
// only way to check them was scrolling debug logs. Nobody is testing
// slk against a real Enterprise Grid account until all three grid-parity
// phases land, so a local count is the only feedback loop there is.
//
// The zero value is ready to use. Safe for concurrent use: RoundTrip
// runs on many goroutines at once.
type Counter struct {
	mu sync.Mutex
	n  map[string]int
}

// Record tallies one request by rawURL. Non-API URLs are ignored:
// asset fetches (files.slack.com, *.slack-edge.com) are a different
// concern — Layer 3 — and counting them here would drown the API
// numbers the criteria are about, since one boot pulls ~337 assets
// against ~70 API calls in the official client.
func (c *Counter) Record(rawURL string) {
	endpoint, ok := endpointName(rawURL)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = make(map[string]int, 32)
	}
	c.n[endpoint]++
}

// Snapshot returns a copy of the tally. Callers may mutate it.
func (c *Counter) Snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.n))
	for k, v := range c.n {
		out[k] = v
	}
	return out
}

// Total is the sum of all endpoint counts.
func (c *Counter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, v := range c.n {
		total += v
	}
	return total
}

// Report renders the tally highest-count-first, for a debug log.
func (c *Counter) Report() string {
	snap := c.Snapshot()
	type row struct {
		name string
		n    int
	}
	rows := make([]row, 0, len(snap))
	total := 0
	for k, v := range snap {
		rows = append(rows, row{k, v})
		total += v
	}
	// Count desc, then name asc, so the output is stable enough to
	// diff between two runs.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].name < rows[j].name
	})
	var b strings.Builder
	fmt.Fprintf(&b, "API requests: %d total across %d endpoints\n", total, len(rows))
	for _, r := range rows {
		fmt.Fprintf(&b, "  %5d  %s\n", r.n, r.name)
	}
	return b.String()
}

// endpointName reduces a URL to the name the tally is keyed by, and
// reports whether it is an API call at all.
//
// Workspace API: everything after /api/. edgeapi: the last two path
// segments with an "edge:" prefix, so channels/info and users/info stay
// distinguishable from the workspace endpoints and from each other.
func endpointName(rawURL string) (string, bool) {
	// Deliberately string surgery rather than url.Parse: this runs on
	// every request and never needs the query, the fragment, or
	// percent-decoding.
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", false
	}
	host, path := rest[:slash], rest[slash:]
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}

	if isEdgeAPIHost(host) {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 2 {
			return "", false
		}
		return "edge:" + parts[len(parts)-2] + "/" + parts[len(parts)-1], true
	}
	if i := strings.Index(path, "/api/"); i >= 0 {
		name := path[i+len("/api/"):]
		if name == "" {
			return "", false
		}
		return name, true
	}
	return "", false
}
