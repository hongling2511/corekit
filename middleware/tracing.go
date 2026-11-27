// Package middleware holds corekit's gin middlewares. The fixed chain that
// httpx installs on every server, in this order, is Recovery → Tracing →
// AccessLog → Metrics.
//
// After the fixed chain come the optional middlewares the configuration
// enables by name (Options.Enabled), built by a Registry: the built-in
// "cors", "timeout" and "ratelimit", and those the program registers.
package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/log"
)

// TraceIDHeader is the response header that carries the request's trace ID.
const TraceIDHeader = "X-Trace-Id"

// Tracer starts the server span of one request.
type Tracer interface {
	// StartServerSpan starts a span named name for a request with header,
	// continuing the upstream trace of a W3C traceparent header. It returns
	// ctx carrying the span, the trace ID and the function that ends the span.
	StartServerSpan(ctx context.Context, name string, header http.Header) (context.Context, string, func())
}

// Tracing starts a server span with tracer for every request. The trace ID
// goes into the request context (see log.TraceID, so every record logged
// with that context carries it) and the X-Trace-Id response header.
func Tracing(tracer Tracer) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Request.Method // an unmatched route has no route template
		if route := c.FullPath(); route != "" {
			name += " " + route
		}
		ctx, traceID, end := tracer.StartServerSpan(c.Request.Context(), name, c.Request.Header)
		defer end()
		c.Request = c.Request.WithContext(log.WithTraceID(ctx, traceID))
		c.Header(TraceIDHeader, traceID)
		c.Next()
	}
}
