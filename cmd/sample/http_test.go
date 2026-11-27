package main_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envelope is the response body every endpoint returns.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TraceID string          `json:"trace_id"`
}

// startHTTP starts `sample serve` on a random local port and returns it with
// the base URL of its HTTP server.
func startHTTP(t *testing.T, env []string, args ...string) (*serving, string) {
	t.Helper()
	s := startServe(t, env, args...)
	l := s.waitFor(t, "http server listening", "")
	var f struct {
		Addr string `json:"addr"`
	}
	require.NoError(t, json.Unmarshal([]byte(l.raw), &f))
	require.NotEmpty(t, f.Addr, l.raw)
	return s, "http://" + f.Addr
}

type response struct {
	status int
	header http.Header
	body   envelope
	raw    string
}

// request sends method url with an optional JSON body and decodes the envelope.
func request(t *testing.T, method, url, body string, header ...string) response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	r := response{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	require.NoError(t, json.Unmarshal(raw, &r.body), "body: %s", raw)
	return r
}

func TestListReturnsItemsAndTotalInTheSuccessEnvelope(t *testing.T) {
	_, base := startHTTP(t, nil)

	r := request(t, http.MethodGet, base+"/users", "")

	assert.Equal(t, http.StatusOK, r.status)
	assert.Equal(t, 0, r.body.Code)
	assert.Equal(t, "OK", r.body.Message)
	assert.NotEmpty(t, r.body.TraceID)
	var data map[string]any
	require.NoError(t, json.Unmarshal(r.body.Data, &data))
	assert.Equal(t, map[string]any{"items": []any{}, "total": float64(0)}, data)
}

func TestRegisteredErrorAnswersWithItsCodeMessageAndStatus(t *testing.T) {
	_, base := startHTTP(t, nil)

	r := request(t, http.MethodGet, base+"/users/42", "")

	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Equal(t, 110001, r.body.Code)
	assert.Equal(t, "user not found", r.body.Message)
	assert.Equal(t, "null", string(r.body.Data))
	assert.NotEmpty(t, r.body.TraceID)
}

func TestCreatedUserIsListedAndFetched(t *testing.T) {
	_, base := startHTTP(t, nil)

	created := request(t, http.MethodPost, base+"/users", `{"name": "ada"}`)
	require.Equal(t, http.StatusOK, created.status, created.raw)
	var user struct{ ID, Name string }
	require.NoError(t, json.Unmarshal(created.body.Data, &user))
	assert.Equal(t, "ada", user.Name)

	got := request(t, http.MethodGet, base+"/users/"+user.ID, "")
	assert.Equal(t, http.StatusOK, got.status)
	assert.JSONEq(t, string(created.body.Data), string(got.body.Data))

	list := request(t, http.MethodGet, base+"/users", "")
	assert.JSONEq(t, `{"items": [{"id": "`+user.ID+`", "name": "ada"}], "total": 1}`, string(list.body.Data))
}

func TestBindFailureAnswersWithTheBindErrorEnvelope(t *testing.T) {
	_, base := startHTTP(t, nil)

	for name, body := range map[string]string{"missing field": `{}`, "malformed": `{"name":`, "wrong type": `{"name": 7}`} {
		t.Run(name, func(t *testing.T) {
			r := request(t, http.MethodPost, base+"/users", body)

			assert.Equal(t, http.StatusBadRequest, r.status)
			assert.Equal(t, 100002, r.body.Code)
			assert.Equal(t, "invalid request parameters", r.body.Message)
			assert.Equal(t, "null", string(r.body.Data))
			assert.NotEmpty(t, r.body.TraceID)
		})
	}
}

func TestUnmatchedRouteAnswersWithTheFailureEnvelope(t *testing.T) {
	_, base := startHTTP(t, nil)

	for _, tt := range []struct {
		method, path string
		status, code int
		message      string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, 100006, "route not found"},
		{http.MethodDelete, "/users", http.StatusMethodNotAllowed, 100007, "method not allowed"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r := request(t, tt.method, base+tt.path, "")

			assert.Equal(t, tt.status, r.status)
			assert.Equal(t, tt.code, r.body.Code)
			assert.Equal(t, tt.message, r.body.Message)
			assert.Equal(t, "null", string(r.body.Data))
			assert.NotEmpty(t, r.body.TraceID)
			assert.Equal(t, r.body.TraceID, r.header.Get("X-Trace-Id"))
		})
	}
}

var demoRoutes = []string{"SAMPLE_DEMO_ROUTES=true"}

func TestUnregisteredErrorAnswersAsInternalWithoutDetails(t *testing.T) {
	s, base := startHTTP(t, demoRoutes)

	r := request(t, http.MethodGet, base+"/demo/error", "")

	assert.Equal(t, http.StatusInternalServerError, r.status)
	assert.Equal(t, 100001, r.body.Code)
	assert.Equal(t, "internal error", r.body.Message)
	assert.Equal(t, "null", string(r.body.Data))
	assert.NotContains(t, r.raw, "10.0.0.7")
	lines := requestLogs(t, s, r.body.TraceID)
	require.Len(t, lines, 2)
	assert.Equal(t, "request failed", lines[0].Msg)
	assert.Contains(t, lines[0].raw, "10.0.0.7", "the details go to the log")
}

func TestPanicAnswersWithTheInternalErrorEnvelope(t *testing.T) {
	s, base := startHTTP(t, demoRoutes)

	r := request(t, http.MethodGet, base+"/demo/panic", "")

	assert.Equal(t, http.StatusInternalServerError, r.status)
	assert.Equal(t, 100001, r.body.Code)
	assert.Equal(t, "internal error", r.body.Message)
	assert.Equal(t, "null", string(r.body.Data))
	assert.NotEmpty(t, r.body.TraceID)
	assert.Equal(t, r.body.TraceID, r.header.Get("X-Trace-Id"))
	assert.NotContains(t, r.raw, "demo panic")
	// The fixed chain runs recovery → tracing → access_log → metrics: the
	// panic is logged with the trace ID and the access log records the 500.
	lines := requestLogs(t, s, r.body.TraceID, "request failed")
	var msgs []string
	for _, l := range lines {
		msgs = append(msgs, l.Msg)
		switch l.Msg {
		case "panic recovered":
			assert.Contains(t, l.raw, "demo panic")
		case "http request":
			assert.Equal(t, http.StatusInternalServerError, l.Status, l.raw)
			assert.Equal(t, "/demo/panic", l.Route)
		}
	}
	assert.Equal(t, []string{"http request", "panic recovered", "request failed"}, msgs)

	after := request(t, http.MethodGet, base+"/users", "")
	assert.Equal(t, http.StatusOK, after.status, "the server keeps serving")
}

func TestDemoRoutesAreOffByDefault(t *testing.T) {
	_, base := startHTTP(t, nil)

	r := request(t, http.MethodGet, base+"/demo/panic", "")

	assert.Equal(t, http.StatusNotFound, r.status)
}

// requestLogs reads log lines until the one with msg (the access log when
// empty) logged for the request with traceID, and returns every line logged
// with that trace ID so far.
func requestLogs(t *testing.T, s *serving, traceID string, msg ...string) []logLine {
	t.Helper()
	until := "http request"
	if len(msg) > 0 {
		until = msg[0]
	}
	for {
		l := s.waitFor(t, until, "")
		if l.TraceID == traceID {
			break
		}
	}
	var lines []logLine
	for _, l := range s.seen {
		if strings.Contains(l.raw, traceID) {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestUpstreamTraceparentIsContinued(t *testing.T) {
	s, base := startHTTP(t, nil)
	const upstream = "4bf92f3577b34da6a3ce929d0e0e4736"

	for _, flags := range []string{"01", "00"} { // sampled and not sampled
		t.Run(flags, func(t *testing.T) {
			r := request(t, http.MethodGet, base+"/users/42", "", "traceparent", "00-"+upstream+"-00f067aa0ba902b7-"+flags)

			assert.Equal(t, upstream, r.body.TraceID)
			assert.Equal(t, upstream, r.header.Get("X-Trace-Id"))
		})
	}
	lines := requestLogs(t, s, upstream)
	var msgs []string
	for _, l := range lines {
		assert.Equal(t, upstream, l.TraceID, l.raw)
		msgs = append(msgs, l.Msg)
	}
	assert.Contains(t, msgs, "request failed")
	assert.Contains(t, msgs, "http request")
}

func TestEveryRequestGetsItsOwnTraceIDWithoutTraceparent(t *testing.T) {
	s, base := startHTTP(t, nil)

	seen := map[string]bool{}
	for _, header := range [][]string{nil, {"traceparent", "garbage"}} {
		for range 2 {
			r := request(t, http.MethodGet, base+"/users", "", header...)

			assert.Regexp(t, `^[0-9a-f]{32}$`, r.body.TraceID)
			assert.NotEqual(t, strings.Repeat("0", 32), r.body.TraceID)
			assert.Equal(t, r.body.TraceID, r.header.Get("X-Trace-Id"))
			assert.False(t, seen[r.body.TraceID], "trace IDs are unique")
			seen[r.body.TraceID] = true
			lines := requestLogs(t, s, r.body.TraceID)
			assert.NotEmpty(t, lines)
		}
	}
}

func TestNoSeparateRequestID(t *testing.T) {
	s, base := startHTTP(t, nil)

	r := request(t, http.MethodGet, base+"/users", "")

	assert.NotContains(t, strings.ToLower(r.raw), "request_id")
	for name := range r.header {
		assert.NotContains(t, strings.ToLower(name), "request-id")
	}
	for _, l := range requestLogs(t, s, r.body.TraceID) {
		assert.NotContains(t, strings.ToLower(l.raw), "request_id")
	}
}

func TestStopFinishesRequestsInFlight(t *testing.T) {
	s, base := startHTTP(t, demoRoutes)
	type answer struct {
		status int
		body   envelope
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		resp, err := http.Get(base + "/demo/slow?wait=500ms")
		if a.err = err; err == nil {
			defer func() { _ = resp.Body.Close() }()
			a.status = resp.StatusCode
			a.err = json.NewDecoder(resp.Body).Decode(&a.body)
		}
		done <- a
	}()
	s.waitFor(t, "demo slow request waiting", "")

	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	s.waitFor(t, "component stopping", "http")
	_, err := http.Get(base + "/users")
	require.Error(t, err, "no new connections once stopping")
	r := <-done
	code, events := s.exit(t)

	require.NoError(t, r.err, "the request in flight is answered")
	assert.Equal(t, http.StatusOK, r.status)
	assert.Equal(t, 0, r.body.Code)
	assert.Equal(t, 0, code, s.stderr.String())
	assert.Contains(t, events, "component stopped http")
}

func TestInvalidHTTPOptionsAreRejected(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--http.addr="}, "http.addr"},
		{[]string{"--http.read_timeout=0s"}, "http.read_timeout"},
		{[]string{"--http.write_timeout=-1s"}, "http.write_timeout"},
		{[]string{"--http.idle_timeout=0s"}, "http.idle_timeout"},
		{[]string{"--http.tls.cert_file=cert.pem"}, "http.tls"},
		{[]string{"--http.tls.key_file=key.pem"}, "http.tls"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			r := run(t, nil, append([]string{"serve"}, tt.args...)...)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, tt.want)
		})
	}
}

func TestServesHTTPSWhenTLSFilesAreSet(t *testing.T) {
	certFile, keyFile, pool := selfSignedCert(t)
	s := startServe(t, nil, "--http.tls.cert_file="+certFile, "--http.tls.key_file="+keyFile)
	l := s.waitFor(t, "http server listening", "")
	var f struct {
		Addr string `json:"addr"`
		TLS  bool   `json:"tls"`
	}
	require.NoError(t, json.Unmarshal([]byte(l.raw), &f))
	assert.True(t, f.TLS)

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get("https://" + f.Addr + "/users")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, body.Code)

	plain, err := http.Get("http://" + f.Addr + "/users")
	if err == nil {
		defer func() { _ = plain.Body.Close() }()
		assert.Equal(t, http.StatusBadRequest, plain.StatusCode, "plain HTTP is not served next to HTTPS")
	}
}

// selfSignedCert writes a self-signed certificate for 127.0.0.1 and its key
// to a temporary directory and returns their paths and a pool trusting it.
func selfSignedCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func TestStdoutHoldsOnlyJSONLogLines(t *testing.T) {
	s, base := startHTTP(t, nil)
	r := request(t, http.MethodGet, base+"/users", "")
	requestLogs(t, s, r.body.TraceID)

	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	_, _ = s.exit(t)

	for _, l := range s.seen {
		assert.True(t, json.Valid([]byte(l.raw)), "not a JSON log line: %q", l.raw)
	}
}
