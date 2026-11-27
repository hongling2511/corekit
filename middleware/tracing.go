// Package middleware holds corekit's gin middlewares. The fixed chain that
// httpx installs on every server, in this order, is Recovery → Tracing →
// AccessLog → Metrics.
//
// After the fixed chain come the optional middlewares the configuration
// enables by name (Options.Enabled), built by a Registry: the built-in
// "cors", "timeout" and "ratelimit", and those the program registers.
package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/hongling2511/corekit/log"
)

// TraceIDHeader is the response header that carries the request's trace ID.
const TraceIDHeader = "X-Trace-Id"

// Tracing starts an OpenTelemetry server span for every request. It
// continues the upstream trace of a W3C traceparent header and starts a new
// trace otherwise. The trace ID goes into the request context (see
// log.TraceID, so every record logged with that context carries it) and the
// X-Trace-Id response header.
//
// Spans are not exported yet: the tracer provider has no exporter, but it
// generates trace IDs all the same.
func Tracing() gin.HandlerFunc {
	tracer := sdktrace.NewTracerProvider().Tracer("github.com/hongling2511/corekit/middleware")
	propagator := propagation.TraceContext{}
	return func(c *gin.Context) {
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		name := c.Request.Method // an unmatched route has no route template
		if route := c.FullPath(); route != "" {
			name += " " + route
		}
		ctx, span := tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		traceID := span.SpanContext().TraceID().String()
		c.Request = c.Request.WithContext(log.WithTraceID(ctx, traceID))
		c.Header(TraceIDHeader, traceID)
		c.Next()
	}
}
