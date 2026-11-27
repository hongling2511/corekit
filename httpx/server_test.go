package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/httpx"
	"github.com/hongling2511/corekit/obs"
)

// The metrics have no exposure endpoint yet, so this is the one place they
// can be observed: the server's own snapshot.
func TestServerRecordsEveryRequestOnce(t *testing.T) {
	srv := httpx.New(httpx.NewOptions(), func(r gin.IRouter) {
		r.GET("/ok", func(c *gin.Context) { httpx.OK(c, nil) })
		r.GET("/boom", func(*gin.Context) { panic("boom") })
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	for _, path := range []string{"/ok", "/ok", "/boom", "/nope"} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	got := srv.Metrics()
	for i := range got {
		assert.Positive(t, got[i].Duration, got[i])
		got[i].Duration = 0
	}
	assert.Equal(t, []obs.HTTPRequestStats{
		{Method: "GET", Route: "", Status: http.StatusNotFound, Count: 1},
		{Method: "GET", Route: "/boom", Status: http.StatusInternalServerError, Count: 1},
		{Method: "GET", Route: "/ok", Status: http.StatusOK, Count: 2},
	}, got)
}

func TestServersKeepSeparateMetrics(t *testing.T) {
	routes := func(r gin.IRouter) { r.GET("/ok", func(c *gin.Context) { httpx.OK(c, nil) }) }
	a, b := httpx.New(httpx.NewOptions(), routes), httpx.New(httpx.NewOptions(), routes)
	ts := httptest.NewServer(a)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ok")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Len(t, a.Metrics(), 1)
	assert.Empty(t, b.Metrics())
}
