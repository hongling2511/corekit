package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/errx"
)

// FailFunc answers a request with the failure response for err.
type FailFunc func(c *gin.Context, err error)

// Recovery turns a panic in the rest of the chain into a failure answered by
// fail with errx.ErrInternal; the panic and its stack go to the log. The
// server keeps serving. A panic with http.ErrAbortHandler, which aborts the
// response on purpose, is passed on.
func Recovery(fail FailFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if err, ok := r.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(r)
			}
			slog.ErrorContext(c.Request.Context(), "panic recovered", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			if c.Writer.Written() {
				c.Abort() // too late for an envelope
				return
			}
			fail(c, errx.ErrInternal.Wrap(fmt.Errorf("panic: %v", r), "recovered"))
		}()
		c.Next()
	}
}
