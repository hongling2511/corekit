package middleware

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/errx"
)

// TimeoutOptions are the parameters of the optional middleware "timeout",
// under the key middleware.timeout.
type TimeoutOptions struct {
	Duration time.Duration `mapstructure:"duration"` // upper bound for handling one request
}

func newTimeoutOptions() TimeoutOptions { return TimeoutOptions{Duration: 30 * time.Second} }

func (o *TimeoutOptions) addFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.Duration, "middleware.timeout.duration", o.Duration, "timeout: upper bound for handling one request, such as 5s")
}

func (o *TimeoutOptions) validate() []error {
	if o.Duration <= 0 {
		return []error{fmt.Errorf("middleware.timeout.duration: invalid value %s, want a positive duration", o.Duration)}
	}
	return nil
}

// Timeout gives the rest of the chain a request context
// (c.Request.Context()) that expires after o.Duration. Handlers stop work
// through that context; when the deadline passes before anything is written,
// the request is answered by fail with errx.ErrRequestTimeout. A handler
// that ignores its context is not interrupted, and a response it has
// already started is left as it is.
func Timeout(o TimeoutOptions, fail FailFunc) gin.HandlerFunc {
	d := o.Duration
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		if !c.Writer.Written() && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fail(c, errx.ErrRequestTimeout.Wrap(ctx.Err(), fmt.Sprintf("handling took longer than %s", d)))
		}
	}
}
