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
	// Duration is the deadline given to the request context. It is not a
	// cap on when the client is answered: see Timeout.
	Duration time.Duration `mapstructure:"duration"`
}

func newTimeoutOptions() TimeoutOptions { return TimeoutOptions{Duration: 30 * time.Second} }

func (o *TimeoutOptions) addFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.Duration, "middleware.timeout.duration", o.Duration, "timeout: deadline of the request context, such as 5s; a handler that returns after it without answering gets 504")
}

func (o *TimeoutOptions) validate() []error {
	if o.Duration <= 0 {
		return []error{fmt.Errorf("middleware.timeout.duration: invalid value %s, want a positive duration", o.Duration)}
	}
	return nil
}

// Timeout gives the rest of the chain a request context
// (c.Request.Context()) that expires after o.Duration. It only passes the
// deadline down; it does not cut a handler off. The request is answered with
// 504 and errx.ErrRequestTimeout when the handler cooperates: it stops on
// the context and returns without writing, or fails (httpx.Fail) with the
// deadline error. A response written after the deadline (a handler that
// ignores its context answers normally, only late) and a streaming response
// already started are left as they are.
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
