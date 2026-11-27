package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/httpx"
	"github.com/hongling2511/corekit/middleware"
	"github.com/hongling2511/corekit/obs"
)

// `serve` runs the management server, the HTTP server and, after them, a
// placeholder component. Environment variables read by serve alone make the
// placeholder demonstrate the shutdown behaviour and add demo readiness
// checks; they are not configuration keys.
const (
	demoFailEnv = "SAMPLE_DEMO_FAIL" // a duration: the second component fails this long after starting
	demoHangEnv = "SAMPLE_DEMO_HANG" // true: the second component's Stop never returns
	// name:ok|fail|hang|panic,...: readiness checks that always pass, always
	// fail, never return or panic, in place of the database and Redis checks of later stages.
	demoChecksEnv = "SAMPLE_DEMO_CHECKS"
)

type demoCheck struct {
	name  string
	check func(context.Context) error
}

// demoChecks returns the readiness checks the demo variable declares.
func demoChecks() ([]demoCheck, error) {
	v := os.Getenv(demoChecksEnv)
	if v == "" {
		return nil, nil
	}
	var checks []demoCheck
	for _, item := range strings.Split(v, ",") {
		name, result, _ := strings.Cut(item, ":")
		c := demoCheck{name: name}
		switch result {
		case "ok":
			c.check = func(context.Context) error { return nil }
		case "fail":
			c.check = func(context.Context) error { return errors.New("demo check failed") }
		case "hang": // ignores its context: only the server-side deadline ends the probe
			c.check = func(ctx context.Context) error {
				slog.InfoContext(ctx, "demo check running", "check", name)
				select {}
			}
		case "panic":
			c.check = func(context.Context) error { panic("demo check panic") }
		}
		if name == "" || c.check == nil {
			return nil, fmt.Errorf("%s: invalid item %q, want name:ok, name:fail, name:hang or name:panic", demoChecksEnv, item)
		}
		checks = append(checks, c)
	}
	return checks, nil
}

func serveCommand(opts *Options) app.Command {
	return app.Command{
		Name:  "serve",
		Short: "Run the service until SIGINT or SIGTERM",
		Run: func(ctx context.Context) error {
			second, err := demoPlaceholder("second")
			if err != nil {
				return err
			}
			checks, err := demoChecks()
			if err != nil {
				return err
			}
			demo, err := demoRoutes()
			if err != nil {
				return err
			}
			optional, err := opts.middlewares.Build(httpx.Fail)
			if err != nil {
				return err
			}
			users := &userHandler{svc: newUserService()}
			tracer, err := obs.NewTracer(ctx, &opts.Tracing, "sample")
			if err != nil {
				return err
			}
			metrics := obs.NewMetrics()
			server := httpx.New(&opts.HTTP, tracer, metrics, func(r gin.IRouter) {
				users.routes(r)
				if demo {
					addDemoRoutes(r)
				}
			}, optional...)
			// The tracer and the management server start first and stop last:
			// probes and metrics answer while the others drain, and the last
			// spans are flushed after them, all within shutdown.timeout.
			admin := obs.NewAdmin(&opts.Admin, metrics)
			for _, c := range checks {
				admin.AddReadinessCheck(c.name, c.check)
			}
			// The drain runs last so it stops first: /readyz fails at once and
			// the others keep serving through the drain window.
			return app.RunComponents(ctx, opts.Shutdown.Timeout, tracer, admin, server, second, admin.Drain(opts.Shutdown.DrainWindow))
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
