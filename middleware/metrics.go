package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Recorder receives the measurements of one finished request: its method,
// matched route ("" when no route matched), status and duration.
type Recorder interface {
	RecordHTTPRequest(ctx context.Context, method, route string, status int, d time.Duration)
}

// Metrics measures every request and hands the measurements to rec, with
// the status the client gets (see AccessLog).
func Metrics(rec Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		began := time.Now()
		panicked := true
		defer func() {
			rec.RecordHTTPRequest(c.Request.Context(), c.Request.Method, c.FullPath(), finalStatus(c, panicked), time.Since(began))
		}()
		c.Next()
		panicked = false
	}
}
