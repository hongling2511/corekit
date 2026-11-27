// Package obs is corekit's observability capability. For now it holds the
// in-memory HTTP request metrics a server records into; their exposure
// arrives with the management port.
package obs

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
)

// HTTPRequestStats is what one method, route and status have accumulated.
type HTTPRequestStats struct {
	Method   string
	Route    string // the route template; "" when no route matched
	Status   int
	Count    int
	Duration time.Duration // total over Count requests
}

// HTTPMetrics accumulates request measurements in memory. Each server owns
// one; it is safe for concurrent use.
type HTTPMetrics struct {
	mu    sync.Mutex
	stats map[httpKey]*HTTPRequestStats
}

type httpKey struct {
	method, route string
	status        int
}

// NewHTTPMetrics returns empty metrics.
func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{stats: map[httpKey]*HTTPRequestStats{}}
}

// RecordHTTPRequest adds one finished request.
func (m *HTTPMetrics) RecordHTTPRequest(_ context.Context, method, route string, status int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := httpKey{method, route, status}
	s, ok := m.stats[k]
	if !ok {
		s = &HTTPRequestStats{Method: method, Route: route, Status: status}
		m.stats[k] = s
	}
	s.Count++
	s.Duration += d
}

// Snapshot returns a copy of the accumulated stats, ordered by route,
// method and status.
func (m *HTTPMetrics) Snapshot() []HTTPRequestStats {
	m.mu.Lock()
	out := make([]HTTPRequestStats, 0, len(m.stats))
	for _, s := range m.stats {
		out = append(out, *s)
	}
	m.mu.Unlock()
	slices.SortFunc(out, func(a, b HTTPRequestStats) int {
		return cmp.Or(cmp.Compare(a.Route, b.Route), cmp.Compare(a.Method, b.Method), cmp.Compare(a.Status, b.Status))
	})
	return out
}
