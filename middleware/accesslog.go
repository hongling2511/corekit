package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLog logs one "http request" record per request, with the request
// context, so the record carries the trace ID. A request whose handler
// panics before writing is logged with status 500 (what Recovery answers);
// one that panics after writing, with the status sent.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		began := time.Now()
		panicked := true
		defer func() {
			slog.InfoContext(c.Request.Context(), "http request",
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"route", c.FullPath(),
				"status", finalStatus(c, panicked),
				"duration", time.Since(began),
				"client_ip", c.ClientIP(),
			)
		}()
		c.Next()
		panicked = false
	}
}

// finalStatus is the status the client gets: the one written, or 500 when
// the request panicked before anything was written.
func finalStatus(c *gin.Context, panicked bool) int {
	if panicked && !c.Writer.Written() {
		return http.StatusInternalServerError
	}
	return c.Writer.Status()
}
