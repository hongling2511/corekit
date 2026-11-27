package main_test

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// export is one request a fake OTLP receiver got.
type export struct {
	method, path string
	body         []byte
}

// otlpReceiver is a fake OTLP/HTTP receiver: it records every request, on
// any path, and answers each after delay.
type otlpReceiver struct {
	root    string // base URL, without a path
	mu      sync.Mutex
	exports []export
}

func startOTLPReceiver(t *testing.T, delay time.Duration) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.exports = append(r.exports, export{req.Method, req.URL.Path, body})
		r.mu.Unlock()
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	r.root = srv.URL
	return r
}

func (r *otlpReceiver) got() []export {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]export(nil), r.exports...)
}

// spanTraceIDs decodes an OTLP ExportTraceServiceRequest and returns the
// hex trace IDs of its spans: resource_spans (1) → scope_spans (2) →
// spans (2) → trace_id (1).
func spanTraceIDs(t *testing.T, msg []byte) []string {
	t.Helper()
	var ids []string
	for _, rs := range fields(t, msg, 1) {
		for _, ss := range fields(t, rs, 2) {
			for _, span := range fields(t, ss, 2) {
				for _, id := range fields(t, span, 1) {
					ids = append(ids, hex.EncodeToString(id))
				}
			}
		}
	}
	return ids
}

// fields returns the values of the length-delimited fields numbered num.
func fields(t *testing.T, msg []byte, num protowire.Number) [][]byte {
	t.Helper()
	var out [][]byte
	for len(msg) > 0 {
		n, typ, l := protowire.ConsumeTag(msg)
		require.GreaterOrEqual(t, l, 0, "malformed protobuf")
		msg = msg[l:]
		l = protowire.ConsumeFieldValue(n, typ, msg)
		require.GreaterOrEqual(t, l, 0, "malformed protobuf")
		if n == num && typ == protowire.BytesType {
			v, _ := protowire.ConsumeBytes(msg)
			out = append(out, v)
		}
		msg = msg[l:]
	}
	return out
}

func TestSpansAreExportedWithOTLPWhenABackendIsConfigured(t *testing.T) {
	recv := startOTLPReceiver(t, 0)
	s, base, _ := startBoth(t, nil, "--tracing.endpoint="+recv.root+"/v1/traces")

	r := request(t, http.MethodGet, base+"/users", "")
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	code, _ := s.exit(t)

	assert.Equal(t, 0, code, s.stderr.String())
	var ids []string
	for _, e := range recv.got() {
		assert.Equal(t, http.MethodPost, e.method)
		assert.Equal(t, "/v1/traces", e.path)
		ids = append(ids, spanTraceIDs(t, e.body)...)
	}
	assert.Equal(t, []string{r.body.TraceID}, ids, "the request's span reached the backend by the end of the stop")
}

func TestNothingIsExportedWithoutABackend(t *testing.T) {
	recv := startOTLPReceiver(t, 0)
	// The standard OpenTelemetry variable is not a corekit configuration key.
	s, base, _ := startBoth(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + recv.root})

	r := request(t, http.MethodGet, base+"/users", "")
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	code, _ := s.exit(t)

	assert.Equal(t, 0, code, s.stderr.String())
	assert.Empty(t, s.stderr.String(), "no error")
	assert.NotEmpty(t, r.body.TraceID, "trace IDs are generated all the same")
	assert.Empty(t, recv.got(), "no request of any kind reached the receiver")
	for _, l := range s.seen {
		assert.NotContains(t, l.raw, `"level":"ERROR"`)
	}
}

func TestTheTraceFlushSharesTheShutdownTimeout(t *testing.T) {
	// The backend never answers in time and a request is in flight when the
	// stop begins: draining and flushing together still end within
	// shutdown.timeout.
	recv := startOTLPReceiver(t, 10*time.Second)
	s, base, _ := startBoth(t, demoRoutes, "--tracing.endpoint="+recv.root+"/v1/traces", "--shutdown.timeout=2s")
	request(t, http.MethodGet, base+"/users", "")
	slow := make(chan int, 1)
	go func() {
		resp, err := http.Get(base + "/demo/slow?wait=1500ms")
		if err != nil {
			slow <- 0
			return
		}
		_ = resp.Body.Close()
		slow <- resp.StatusCode
	}()
	s.waitFor(t, "demo slow request waiting", "")

	began := time.Now()
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	code, _ := s.exit(t)
	took := time.Since(began)

	assert.Equal(t, http.StatusOK, <-slow, "the request in flight finished")
	assert.NotEqual(t, 0, code, "the flush did not finish in time")
	// A flush with a timeout of its own would end at drain + 2s, 3.5s or more.
	assert.Less(t, took, 2900*time.Millisecond, "one shutdown.timeout bounds the whole stop")
	assert.NotEmpty(t, recv.got(), "the flush was attempted")
}
