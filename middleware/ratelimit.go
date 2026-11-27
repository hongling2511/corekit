package middleware

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/errx"
)

// RateLimitOptions are the parameters of the optional middleware
// "ratelimit", under the key middleware.ratelimit.
type RateLimitOptions struct {
	// One bucket per server in the process, shared by every client and route;
	// each replica counts on its own.
	Rate  float64 `mapstructure:"rate"`  // requests per second the bucket refills
	Burst int     `mapstructure:"burst"` // bucket size: requests admitted at once
}

func newRateLimitOptions() RateLimitOptions { return RateLimitOptions{Rate: 100, Burst: 100} }

func (o *RateLimitOptions) addFlags(fs *pflag.FlagSet) {
	fs.Float64Var(&o.Rate, "middleware.ratelimit.rate", o.Rate, "ratelimit: requests per second admitted on average, shared by all clients and routes of this process")
	fs.IntVar(&o.Burst, "middleware.ratelimit.burst", o.Burst, "ratelimit: requests admitted at once, shared by all clients and routes of this process")
}

func (o *RateLimitOptions) validate() []error {
	var errs []error
	if !(o.Rate > 0) { // also rejects NaN
		errs = append(errs, fmt.Errorf("middleware.ratelimit.rate: invalid value %v, want a positive number", o.Rate))
	}
	if o.Burst < 1 {
		errs = append(errs, errors.New("middleware.ratelimit.burst: must be at least 1"))
	}
	return errs
}

// RateLimit admits requests from a token bucket of o.Burst tokens that
// refills o.Rate tokens per second. There is one bucket per server in the
// process: every client and every route share its quota, and each replica
// counts on its own (with n replicas the service admits up to n times the
// rate). A request finding the bucket empty is answered
// by fail with errx.ErrTooManyRequests.
func RateLimit(o RateLimitOptions, fail FailFunc) gin.HandlerFunc {
	b := &bucket{rate: o.Rate, burst: float64(o.Burst), tokens: float64(o.Burst), last: time.Now()}
	return func(c *gin.Context) {
		if !b.take(time.Now()) {
			fail(c, errx.ErrTooManyRequests)
			return
		}
		c.Next()
	}
}

// bucket is a token bucket.
type bucket struct {
	mu          sync.Mutex
	rate, burst float64
	tokens      float64
	last        time.Time
}

// take refills the bucket for the time since the last call and takes one
// token, reporting whether there was one.
func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed.Seconds()*b.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
