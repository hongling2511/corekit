package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Recorder receives the measurements of one finished request: its method,
// matched route ("" when no route matched), status and duration.
type Recorder interface {
	RecordHTTPRequest(ctx context.Context, method, route string, status int, d time.Duration)
}

// Metrics measures every request and hands the measurements to rec; a nil
// rec drops them. A request whose handler panics is measured with status
// 500 before the panic moves on to Recovery.
func Metrics(rec Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		began := time.Now()
		panicked := true
		defer func() {
			if rec == nil {
				return
			}
			status := c.Writer.Status()
			if panicked {
				status = http.StatusInternalServerError
			}
			rec.RecordHTTPRequest(c.Request.Context(), c.Request.Method, c.FullPath(), status, time.Since(began))
		}()
		c.Next()
		panicked = false
	}
}
