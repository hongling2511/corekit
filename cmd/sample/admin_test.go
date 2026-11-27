package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listening returns the address the server that logs msg listens on, from
// the lines seen so far or the ones to come.
func (s *serving) listening(t *testing.T, msg string) string {
	t.Helper()
	l, ok := logLine{}, false
	for _, seen := range s.seen {
		if seen.Msg == msg {
			l, ok = seen, true
		}
	}
	if !ok {
		l = s.waitFor(t, msg, "")
	}
	var f struct {
		Addr string `json:"addr"`
	}
	require.NoError(t, json.Unmarshal([]byte(l.raw), &f))
	require.NotEmpty(t, f.Addr, l.raw)
	return "http://" + f.Addr
}

// startBoth starts `sample serve` and returns it with the base URLs of its
// business and management ports.
func startBoth(t *testing.T, env []string, args ...string) (s *serving, base, admin string) {
	t.Helper()
	s = startServe(t, env, args...)
	return s, s.listening(t, "http server listening"), s.listening(t, "admin server listening")
}

// get sends GET url and returns the status and the raw body.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestLivezAnswersOnTheManagementPort(t *testing.T) {
	_, _, admin := startBoth(t, nil)

	status, _ := get(t, admin+"/livez")

	assert.Equal(t, http.StatusOK, status)
}

func TestReadyzFailsWhenAnyCheckFailsWhileLivezStaysUp(t *testing.T) {
	for _, tt := range []struct {
		desc, checks string
		ready        int
		failed       []string
	}{
		{desc: "no checks", ready: http.StatusOK},
		{desc: "every check passes", checks: "db:ok,redis:ok", ready: http.StatusOK},
		{desc: "one check fails", checks: "db:ok,redis:fail", ready: http.StatusServiceUnavailable, failed: []string{"redis"}},
		{desc: "every check fails", checks: "db:fail,redis:fail", ready: http.StatusServiceUnavailable, failed: []string{"db", "redis"}},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			var env []string
			if tt.checks != "" {
				env = []string{"SAMPLE_DEMO_CHECKS=" + tt.checks}
			}
			_, _, admin := startBoth(t, env)

			ready, body := get(t, admin+"/readyz")
			live, _ := get(t, admin+"/livez")

			assert.Equal(t, tt.ready, ready, body)
			assert.Equal(t, http.StatusOK, live)
			for _, name := range tt.failed {
				assert.Contains(t, body, name+": failed\n", "the body names each failed check")
			}
			if tt.failed == nil {
				assert.NotContains(t, body, ": ")
			}
			assert.NotContains(t, body, "demo check failed", "the reason goes only to the log")
		})
	}
}

func TestReadinessFailureReasonsGoToTheLog(t *testing.T) {
	s, _, admin := startBoth(t, []string{"SAMPLE_DEMO_CHECKS=db:ok,redis:fail"})

	_, body := get(t, admin+"/readyz")
	l := s.waitFor(t, "readiness check failed", "")

	assert.Equal(t, "redis: failed\n", body)
	assert.Contains(t, l.raw, `"check":"redis"`)
	assert.Contains(t, l.raw, "demo check failed")
}

func TestAHangingCheckFailsReadinessWithinTheTimeout(t *testing.T) {
	// The hanging check comes first: the checks after it, which passed or
	// failed long before the deadline, keep their own result.
	_, _, admin := startBoth(t, []string{"SAMPLE_DEMO_CHECKS=cache:hang,db:ok,redis:fail,queue:panic"}, "--admin.readiness_timeout=300ms")

	for range 5 {
		began := time.Now()
		status, body := get(t, admin+"/readyz")

		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Equal(t, "cache: timed out\nredis: failed\nqueue: failed\n", body)
		assert.Less(t, time.Since(began), 2*time.Second)
	}
	live, _ := get(t, admin+"/livez")
	assert.Equal(t, http.StatusOK, live, "a panicking check does not take the process down")
}

func TestInvalidDemoChecksAreRejected(t *testing.T) {
	r := run(t, []string{"SAMPLE_DEMO_CHECKS=db:maybe"}, "serve", "--http.addr=127.0.0.1:0", "--admin.addr=127.0.0.1:0")

	assert.NotEqual(t, 0, r.code)
	assert.Contains(t, r.stderr, "SAMPLE_DEMO_CHECKS")
}

func TestVersionAnswersOnTheManagementPortLikeTheVersionCommand(t *testing.T) {
	_, _, admin := startBoth(t, nil)

	status, body := get(t, admin+"/version")

	assert.Equal(t, http.StatusOK, status)
	want := run(t, nil, "version")
	require.Equal(t, 0, want.code, want.stderr)
	assert.Equal(t, want.stdout, body)
}

func TestManagementEndpointsAreNotOnTheBusinessPort(t *testing.T) {
	_, base, admin := startBoth(t, nil)

	for _, path := range []string{"/livez", "/readyz", "/metrics", "/debug/pprof/", "/debug/pprof/cmdline", "/version"} {
		t.Run(path, func(t *testing.T) {
			r := request(t, http.MethodGet, base+path, "")

			assert.Equal(t, http.StatusNotFound, r.status)
			assert.Equal(t, 100006, r.body.Code)
			assert.Equal(t, "route not found", r.body.Message)
			assert.NotEmpty(t, r.body.TraceID)

			status, _ := get(t, admin+path)
			assert.Equal(t, http.StatusOK, status, "it is on the management port")
		})
	}
}

func TestPprofCanBeTurnedOff(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want int
	}{
		{nil, http.StatusOK},
		{[]string{"--admin.pprof=false"}, http.StatusNotFound},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			_, _, admin := startBoth(t, nil, tt.args...)

			for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/symbol"} {
				status, _ := get(t, admin+path)
				assert.Equal(t, tt.want, status, path)
			}
			live, _ := get(t, admin+"/livez")
			assert.Equal(t, http.StatusOK, live)
		})
	}
}

// scrape reads /metrics from the management port into a map from series
// ("name{labels}" as exposed) to value.
func scrape(t *testing.T, admin string) map[string]float64 {
	t.Helper()
	status, body := get(t, admin+"/metrics")
	require.Equal(t, http.StatusOK, status, body)
	series := map[string]float64{}
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		require.Positive(t, i, line)
		v, err := strconv.ParseFloat(line[i+1:], 64)
		require.NoError(t, err, line)
		series[line[:i]] = v
	}
	return series
}

func TestHTTPRequestMetricsChangeAfterRequests(t *testing.T) {
	_, base, admin := startBoth(t, demoRoutes)
	type series struct{ route, status string }
	requests := map[series]int{
		{"/users", "200"}:      2,
		{"", "404"}:            1, // an unmatched route has no route template
		{"/demo/panic", "500"}: 1,
		{"/demo/error", "500"}: 1,
		{"/users/:id", "404"}:  1, // a registered business error
	}
	paths := map[series]string{
		{"/users", "200"}: "/users", {"", "404"}: "/nope", {"/demo/panic", "500"}: "/demo/panic",
		{"/demo/error", "500"}: "/demo/error", {"/users/:id", "404"}: "/users/42",
	}
	key := func(name string, s series) string {
		return name + `{method="GET",route="` + s.route + `",status="` + s.status + `"}`
	}

	before := scrape(t, admin)
	for s, n := range requests {
		for range n {
			request(t, http.MethodGet, base+paths[s], "")
		}
	}
	after := scrape(t, admin)

	for name := range before {
		assert.False(t, strings.HasPrefix(name, "http_server_"), "no request before the test's own, not even a self-ping: %s", name)
	}
	for s, n := range requests {
		assert.Equal(t, float64(n), after[key("http_server_requests_total", s)], s)
		assert.Equal(t, float64(n), after[key("http_server_request_duration_seconds_count", s)], s)
		assert.Positive(t, after[key("http_server_request_duration_seconds_sum", s)], s)
	}
}

func TestInvalidManagementAndTracingOptionsAreRejected(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--admin.addr="}, "admin.addr"},
		{[]string{"--admin.readiness_timeout=0s"}, "admin.readiness_timeout"},
		{[]string{"--tracing.endpoint=collector:4318"}, "tracing.endpoint"},
		{[]string{"--tracing.endpoint=ftp://collector/v1/traces"}, "tracing.endpoint"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			r := run(t, nil, append([]string{"serve", "--http.addr=127.0.0.1:0"}, tt.args...)...)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, tt.want)
		})
	}
}

// fetchProfile GETs a pprof profile and decodes it.
func fetchProfile(t *testing.T, url string) *profile.Profile {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	p, err := profile.Parse(resp.Body)
	require.NoError(t, err)
	return p
}

// total sums the sample values of the profile's type typ.
func total(t *testing.T, p *profile.Profile, typ string) int64 {
	t.Helper()
	i := slices.IndexFunc(p.SampleType, func(st *profile.ValueType) bool { return st.Type == typ })
	require.GreaterOrEqual(t, i, 0, "no %s in the profile", typ)
	var sum int64
	for _, s := range p.Sample {
		sum += s.Value[i]
	}
	return sum
}

func TestNamedProfilesWithSecondsAreDeltasOverThatWindow(t *testing.T) {
	_, base, admin := startBoth(t, nil)
	for range 50 { // allocations before the window
		request(t, http.MethodGet, base+"/users", "")
	}
	fetchProfile(t, admin+"/debug/pprof/heap?gc=1")
	idle := fetchProfile(t, admin+"/debug/pprof/allocs?seconds=1")
	busy := make(chan *profile.Profile, 1)
	began := time.Now()
	go func() { busy <- fetchProfile(t, admin+"/debug/pprof/allocs?seconds=2") }()
	time.Sleep(200 * time.Millisecond)
	for range 200 { // allocations within the window
		request(t, http.MethodPost, base+"/users", `{"name": "ada"}`)
	}
	// Allocation profiles show the state as of the last garbage collection.
	fetchProfile(t, admin+"/debug/pprof/heap?gc=1")
	delta := <-busy
	absolute := fetchProfile(t, admin+"/debug/pprof/allocs") // everything since start-up

	assert.GreaterOrEqual(t, time.Since(began), 2*time.Second, "the profile covers the window")
	assert.GreaterOrEqual(t, delta.DurationNanos, int64(2*time.Second))
	assert.Less(t, total(t, delta, "alloc_space"), total(t, absolute, "alloc_space"), "what was allocated before the window is left out")
	assert.Greater(t, total(t, delta, "alloc_objects"), total(t, idle, "alloc_objects"), "what was allocated within the window shows")
	for _, name := range []string{"heap", "goroutine"} {
		p := fetchProfile(t, admin+"/debug/pprof/"+name+"?seconds=1")
		assert.GreaterOrEqual(t, p.DurationNanos, int64(time.Second), name)
	}
}

func TestInvalidProfileWindowsAreRejected(t *testing.T) {
	_, _, admin := startBoth(t, nil)

	for _, query := range []string{"heap?seconds=0", "heap?seconds=1.5", "heap?seconds=x", "heap?seconds=1&debug=1"} {
		status, body := get(t, admin+"/debug/pprof/"+query)
		assert.Equal(t, http.StatusBadRequest, status, query+": "+body)
	}
}

func TestHeapProfileCollectsGarbageOnlyWhenGCIsPositive(t *testing.T) {
	_, _, admin := startBoth(t, []string{"GOGC=off"}) // no collection but the ones asked for
	gcs := func() float64 { return scrape(t, admin)["go_gc_duration_seconds_count"] }

	before := gcs()
	for _, query := range []string{"heap", "heap?gc=0", "heap?gc=-1", "heap?gc=x"} {
		fetchProfile(t, admin+"/debug/pprof/"+query)
	}
	assert.Equal(t, before, gcs(), "no garbage collection")
	fetchProfile(t, admin+"/debug/pprof/heap?gc=1")
	assert.Equal(t, before+1, gcs(), "gc=1 collects once")
}

func TestReadinessFailsAtOnceOnStopWhileTheServiceKeepsServingThroughTheDrainWindow(t *testing.T) {
	s, base, admin := startBoth(t, nil, "--shutdown.drain_window=1s")
	ready, _ := get(t, admin+"/readyz")
	require.Equal(t, http.StatusOK, ready)

	began := time.Now()
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	s.waitFor(t, "draining", "")

	ready, body := get(t, admin+"/readyz")
	live, _ := get(t, admin+"/livez")
	served := request(t, http.MethodGet, base+"/users", "")
	inWindow := time.Since(began)
	code, events := s.exit(t)

	assert.Equal(t, http.StatusServiceUnavailable, ready)
	assert.Equal(t, "shutting down\n", body)
	assert.Equal(t, http.StatusOK, live)
	assert.Equal(t, http.StatusOK, served.status, "the business port still takes new requests")
	assert.Less(t, inWindow, time.Second, "all of the above happened within the window")
	assert.GreaterOrEqual(t, time.Since(began), time.Second, "the components stop after the window")
	assert.Equal(t, 0, code, s.stderr.String())
	assert.Contains(t, events, "component stopped http")
}

func TestDrainWindowMustFitInTheShutdownTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"--shutdown.drain_window=-1s"},
		{"--shutdown.drain_window=30s"},
		{"--shutdown.drain_window=2s", "--shutdown.timeout=1s"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := run(t, nil, append([]string{"serve", "--http.addr=127.0.0.1:0", "--admin.addr=127.0.0.1:0"}, args...)...)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, "shutdown.drain_window")
		})
	}
}

func TestAReadinessProbeWaitingOnItsChecksAnswersShuttingDownOnceTheStopBegins(t *testing.T) {
	s, _, admin := startBoth(t, []string{"SAMPLE_DEMO_CHECKS=db:hang"},
		"--admin.readiness_timeout=5s", "--shutdown.drain_window=1s", "--shutdown.timeout=10s")
	type answer struct {
		status int
		body   string
	}
	probe := make(chan answer, 1)
	go func() {
		status, body := get(t, admin+"/readyz")
		probe <- answer{status, body}
	}()
	s.waitFor(t, "demo check running", "") // the probe is waiting on its check

	began := time.Now()
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	got := <-probe

	assert.Equal(t, answer{http.StatusServiceUnavailable, "shutting down\n"}, got)
	assert.Less(t, time.Since(began), time.Second, "at once, not at the readiness timeout")
	code, _ := s.exit(t)
	assert.Equal(t, 0, code, s.stderr.String())
}
