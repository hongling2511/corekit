package main_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnregisteredMiddlewareFailsStartupValidationNamingIt(t *testing.T) {
	// The fixed chain is not configurable: its names are not optional middlewares either.
	for _, name := range []string{"bogus", "recovery"} {
		for _, command := range [][]string{{"serve", "--http.addr=127.0.0.1:0"}, {"version"}, {"config", "print"}} {
			t.Run(name+" "+command[0], func(t *testing.T) {
				r := run(t, nil, append(command, "--middleware.enabled=served_by,"+name)...)

				assert.NotEqual(t, 0, r.code)
				assert.Contains(t, r.stderr, `middleware.enabled: unknown middleware "`+name+`"`)
				assert.Empty(t, r.stdout, "validation fails before logging starts and before the subcommand runs")
			})
		}
	}
}

func TestRegisteredMiddlewareNamesPassStartupValidation(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"config", "print"}} {
		r := run(t, nil, append(command, "--middleware.enabled=cors,timeout,ratelimit,served_by")...)

		assert.Equal(t, 0, r.code, r.stderr)
	}
}

func TestCORSIsAppliedOnlyWhenEnabled(t *testing.T) {
	const origin = "https://app.example"
	t.Run("off by default", func(t *testing.T) {
		_, base := startHTTP(t, nil)

		r := request(t, http.MethodGet, base+"/users", "", "Origin", origin)

		assert.Equal(t, http.StatusOK, r.status)
		assert.Empty(t, r.header.Get("Access-Control-Allow-Origin"))
	})

	_, base := startHTTP(t, nil, "--middleware.enabled=cors",
		"--middleware.cors.allow_origins="+origin, "--middleware.cors.max_age=10m")
	t.Run("allowed origin", func(t *testing.T) {
		r := request(t, http.MethodGet, base+"/users", "", "Origin", origin)

		assert.Equal(t, http.StatusOK, r.status)
		assert.Equal(t, origin, r.header.Get("Access-Control-Allow-Origin"))
		assert.Contains(t, r.header.Values("Vary"), "Origin")
		assert.Equal(t, "X-Trace-Id", r.header.Get("Access-Control-Expose-Headers"))
	})
	t.Run("failure envelopes carry it too", func(t *testing.T) {
		r := request(t, http.MethodGet, base+"/users/42", "", "Origin", origin)

		assert.Equal(t, http.StatusNotFound, r.status)
		assert.Equal(t, origin, r.header.Get("Access-Control-Allow-Origin"))
	})
	t.Run("other origin", func(t *testing.T) {
		r := request(t, http.MethodGet, base+"/users", "", "Origin", "https://evil.example")

		assert.Equal(t, http.StatusOK, r.status)
		assert.Empty(t, r.header.Get("Access-Control-Allow-Origin"))
	})
	t.Run("preflight", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, base+"/users", nil)
		require.NoError(t, err)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, origin, resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), http.MethodPost)
		assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Content-Type")
		assert.Equal(t, "600", resp.Header.Get("Access-Control-Max-Age"))
	})
}

func TestTimeoutAnswersWithTheTimeoutEnvelope(t *testing.T) {
	s, base := startHTTP(t, demoRoutes, "--middleware.enabled=timeout", "--middleware.timeout.duration=100ms")
	// The handler stops on its context: by failing with the deadline error, as
	// a call into a dependency would, or by returning without an answer.
	for _, query := range []string{"wait=5s", "wait=5s&on_deadline=return"} {
		t.Run(query, func(t *testing.T) {
			r := request(t, http.MethodGet, base+"/demo/slow?"+query, "")

			assert.Equal(t, http.StatusGatewayTimeout, r.status)
			assert.Equal(t, 100008, r.body.Code)
			assert.Equal(t, "request timed out", r.body.Message)
			assert.Equal(t, "null", string(r.body.Data))
			require.NotEmpty(t, r.body.TraceID)
			assert.Equal(t, r.body.TraceID, r.header.Get("X-Trace-Id"))
			logs := requestLogs(t, s, r.body.TraceID, "http request")
			assert.Equal(t, http.StatusGatewayTimeout, logs[len(logs)-1].Status, "the fixed chain sees the timeout answer")
		})
	}

	fast := request(t, http.MethodGet, base+"/demo/slow?wait=1ms", "")
	assert.Equal(t, http.StatusOK, fast.status)
}

func TestRegisteredInternalErrorAfterTheDeadlineKeepsItsCode(t *testing.T) {
	_, base := startHTTP(t, demoRoutes, "--middleware.enabled=timeout", "--middleware.timeout.duration=50ms")

	// The handler chooses errx.ErrInternal itself, with the deadline error as its cause.
	r := request(t, http.MethodGet, base+"/demo/slow?wait=5s&on_deadline=internal", "")

	assert.Equal(t, http.StatusInternalServerError, r.status)
	assert.Equal(t, 100001, r.body.Code)
	assert.Equal(t, "internal error", r.body.Message)
}

func TestTimeoutIsOffByDefault(t *testing.T) {
	_, base := startHTTP(t, demoRoutes)

	r := request(t, http.MethodGet, base+"/demo/slow?wait=300ms", "")

	assert.Equal(t, http.StatusOK, r.status)
}

func TestRateLimitAnswersWithTheTooManyRequestsEnvelope(t *testing.T) {
	// A bucket of 2 that refills one request every ~17 minutes.
	s, base := startHTTP(t, nil, "--middleware.enabled=ratelimit",
		"--middleware.ratelimit.rate=0.001", "--middleware.ratelimit.burst=2")

	for range 2 {
		assert.Equal(t, http.StatusOK, request(t, http.MethodGet, base+"/users", "").status)
	}
	r := request(t, http.MethodGet, base+"/users", "")

	assert.Equal(t, http.StatusTooManyRequests, r.status)
	assert.Equal(t, 100009, r.body.Code)
	assert.Equal(t, "too many requests", r.body.Message)
	assert.Equal(t, "null", string(r.body.Data))
	require.NotEmpty(t, r.body.TraceID)
	assert.Equal(t, r.body.TraceID, r.header.Get("X-Trace-Id"))
	logs := requestLogs(t, s, r.body.TraceID, "http request")
	assert.Equal(t, http.StatusTooManyRequests, logs[len(logs)-1].Status, "the fixed chain sees the rejection")
}

func TestRateLimitIsOffByDefault(t *testing.T) {
	_, base := startHTTP(t, nil, "--middleware.ratelimit.rate=0.001", "--middleware.ratelimit.burst=1")

	for range 3 {
		assert.Equal(t, http.StatusOK, request(t, http.MethodGet, base+"/users", "").status)
	}
}

func TestBusinessMiddlewareRegistersLikeTheBuiltIns(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		_, base := startHTTP(t, nil)

		assert.Empty(t, request(t, http.MethodGet, base+"/users", "").header.Get("X-Served-By"))
	})
	t.Run("enabled", func(t *testing.T) {
		_, base := startHTTP(t, nil, "--middleware.enabled=served_by")

		assert.Equal(t, "sample", request(t, http.MethodGet, base+"/users", "").header.Get("X-Served-By"))
	})
}

func TestMiddlewaresAreEnabledFromTheConfigFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.yaml")
	require.NoError(t, os.WriteFile(file, []byte(`middleware:
  enabled: [served_by, ratelimit]
  ratelimit:
    rate: 0.001
    burst: 1
`), 0o600))
	_, base := startHTTP(t, nil, "--config", file)

	first := request(t, http.MethodGet, base+"/users", "")
	second := request(t, http.MethodGet, base+"/users", "")

	assert.Equal(t, http.StatusOK, first.status)
	assert.Equal(t, http.StatusTooManyRequests, second.status)
	assert.Equal(t, "sample", second.header.Get("X-Served-By"), "served_by runs before ratelimit, in the listed order")
}

func TestInvalidMiddlewareOptionsAreRejected(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--middleware.enabled=cors,cors"}, `middleware.enabled: middleware "cors" is listed twice`},
		{[]string{"--middleware.timeout.duration=0s"}, "middleware.timeout.duration"},
		{[]string{"--middleware.ratelimit.rate=0"}, "middleware.ratelimit.rate"},
		{[]string{"--middleware.ratelimit.burst=0"}, "middleware.ratelimit.burst"},
		{[]string{"--middleware.cors.max_age=-1s"}, "middleware.cors.max_age"},
		{[]string{"--middleware.cors.allow_origins=*", "--middleware.cors.allow_credentials"}, "middleware.cors.allow_credentials"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			r := run(t, nil, append([]string{"serve", "--http.addr=127.0.0.1:0"}, tt.args...)...)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, tt.want)
		})
	}
}
