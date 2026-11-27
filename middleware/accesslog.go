package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLog logs one "http request" record per request, with the request
// context, so the record carries the trace ID. A request whose handler
// panics is logged with status 500 before the panic moves on to Recovery.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		began := time.Now()
		panicked := true
		defer func() {
			status := c.Writer.Status()
			if panicked {
				status = http.StatusInternalServerError
			}
			slog.InfoContext(c.Request.Context(), "http request",
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"route", c.FullPath(),
				"status", status,
				"duration", time.Since(began),
				"client_ip", c.ClientIP(),
			)
		}()
		c.Next()
		panicked = false
	}
}
