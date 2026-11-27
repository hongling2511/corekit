package obs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/obs"
)

// Importing obs must not change the process's default HTTP routes: pprof
// is served only by the management server's own router.
func TestImportingObsLeavesTheDefaultMuxAlone(t *testing.T) {
	_ = obs.NewAdmin(obs.NewAdminOptions(), obs.NewMetrics())

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/heap"} {
		_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
		assert.Empty(t, pattern, path)
	}
}

// scrape returns the /metrics exposition of admin.
func scrape(t *testing.T, admin http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

func TestMetricsInstancesAreIndependent(t *testing.T) {
	a, b := obs.NewMetrics(), obs.NewMetrics()
	adminA, adminB := obs.NewAdmin(obs.NewAdminOptions(), a), obs.NewAdmin(obs.NewAdminOptions(), b)

	a.RecordHTTPRequest(context.Background(), http.MethodGet, "/ok", http.StatusOK, time.Millisecond)

	assert.Contains(t, scrape(t, adminA), `http_server_requests_total{method="GET",route="/ok",status="200"} 1`)
	gotB := scrape(t, adminB)
	assert.Contains(t, gotB, "go_goroutines", "B exposes its own runtime metrics")
	assert.NotContains(t, gotB, "http_server_requests_total{", "nothing recorded into A shows in B")
}
