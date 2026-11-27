package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/httpx"
	"github.com/hongling2511/corekit/middleware"
)

// `serve` runs the HTTP server and, after it, a placeholder component. Two
// environment variables, read by serve alone, make the placeholder
// demonstrate the shutdown behaviour; they are not configuration keys.
const (
	demoFailEnv = "SAMPLE_DEMO_FAIL" // a duration: the second component fails this long after starting
	demoHangEnv = "SAMPLE_DEMO_HANG" // true: the second component's Stop never returns
)

func serveCommand(opts *Options) app.Command {
	return app.Command{
		Name:  "serve",
		Short: "Run the service until SIGINT or SIGTERM",
		Run: func(ctx context.Context) error {
			second, err := demoPlaceholder("second")
			if err != nil {
				return err
			}
			demo, err := demoRoutes()
			if err != nil {
				return err
			}
			registry := middleware.NewRegistry(&opts.Middleware)
			registry.Register("served_by", servedBy)
			optional, err := registry.Build(httpx.Fail)
			if err != nil {
				return err
			}
			users := &userHandler{svc: newUserService()}
			server := httpx.New(&opts.HTTP, func(r gin.IRouter) {
				users.routes(r)
				if demo {
					addDemoRoutes(r)
				}
			}, optional...)
			return app.RunComponents(ctx, opts.Shutdown.Timeout, server, second)
		},
	}
}

// servedBy is the sample's own optional middleware: it names the service in
// the X-Served-By response header.
func servedBy(middleware.FailFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Served-By", "sample")
		c.Next()
	}
}

// demoPlaceholder returns a placeholder configured by the demo variables.
func demoPlaceholder(name string) (*placeholder, error) {
	p := &placeholder{name: name}
	if v := os.Getenv(demoFailEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s: invalid value %q, want a positive duration", demoFailEnv, v)
		}
		p.fail = d
	}
	if v := os.Getenv(demoHangEnv); v != "" {
		hang, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid value %q, want true or false", demoHangEnv, v)
		}
		p.hang = hang
	}
	return p, nil
}

// placeholder is a component that does nothing until it is stopped.
type placeholder struct {
	name string
	fail time.Duration // fail this long after starting; 0 never
	hang bool          // Stop never returns
}

func (p *placeholder) Name() string { return p.name }

func (p *placeholder) Start(ctx context.Context) error {
	var fail <-chan time.Time
	if p.fail > 0 {
		fail = time.After(p.fail)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-fail:
		return errors.New("demo failure")
	}
}

func (p *placeholder) Stop(context.Context) error {
	if p.hang {
		select {} // never returns: the shutdown timeout must end the process
	}
	return nil
}
