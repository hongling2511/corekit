// Package obs is corekit's observability capability: the request metrics
// the servers record, the tracer that exports their spans with OTLP, and
// the management server that exposes probes, metrics, pprof and the build
// information on a port of its own.
package obs

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is a process's metrics, exposed on the management server's
// /metrics in the Prometheus text format. It holds its own registry, with
// the Go runtime and process metrics and the HTTP request metrics:
//
//   - http_server_requests_total{method, route, status}
//   - http_server_request_duration_seconds{method, route, status} (histogram)
//
// route is the route template, "" when no route matched. Create one per
// process and pass it to every server; it is safe for concurrent use.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics returns a process's metrics, with no request recorded yet.
func NewMetrics() *Metrics {
	labels := []string{"method", "route", "status"}
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_server_requests_total",
			Help: "HTTP requests served, by method, route template and status.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "Time to serve an HTTP request, by method, route template and status.",
			Buckets: prometheus.DefBuckets,
		}, labels),
	}
	m.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), m.requests, m.duration)
	return m
}

// RecordHTTPRequest adds one finished request.
func (m *Metrics) RecordHTTPRequest(_ context.Context, method, route string, status int, d time.Duration) {
	code := strconv.Itoa(status)
	m.requests.WithLabelValues(method, route, code).Inc()
	m.duration.WithLabelValues(method, route, code).Observe(d.Seconds())
}

func (m *Metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
