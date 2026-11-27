package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
)

// Component is something long-running: an HTTP server, a task consumer, a
// scheduler.
type Component interface {
	// Name identifies the component in logs and errors.
	Name() string
	// Start runs the component and blocks until ctx is cancelled or the
	// component fails. Returning before ctx is cancelled, even with a nil
	// error, counts as a failure.
	Start(ctx context.Context) error
	// Stop stops the component gracefully, giving up when ctx is done.
	Stop(ctx context.Context) error
}

// RunComponents starts the components in parallel and runs them until ctx
// is cancelled, the process receives SIGINT or SIGTERM, or a component
// fails. It then stops them one at a time in reverse order: it calls Stop,
// cancels the component's Start context and waits for Start to return. The
// whole stop has the timeout as its upper bound; components still running
// then are abandoned.
//
// Once stopping has begun, a second SIGINT or SIGTERM ends the process at
// once. A component whose Start returns because a component stopped before
// it has gone counts as failed.
//
// Start returning nil or a pure context.Canceled (possibly wrapped, but not
// joined with another error) after its component was told to stop is a
// clean stop; any other error, including one reported while
// stopping (a final flush that failed), is a failure.
//
// It returns nil after a clean shutdown on cancellation or a signal,
// otherwise the components' failures joined with the errors from stopping:
// a Stop error and a stop timeout also make the shutdown unclean.
func RunComponents(ctx context.Context, timeout time.Duration, components ...Component) error {
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	g, gctx := errgroup.WithContext(sigCtx)

	// A component's Start context keeps ctx's values but is cancelled only
	// when that component is stopped, so the stop order holds.
	base := context.WithoutCancel(ctx)
	running := make([]*runningComponent, len(components))
	for i, c := range components {
		cctx, cancel := context.WithCancel(base)
		r := &runningComponent{Component: c, cancel: cancel, exited: make(chan struct{})}
		running[i] = r
		// Components abandoned by a stop timeout still get their Start
		// context cancelled on return.
		defer r.abandon()
		slog.InfoContext(ctx, "component starting", "component", c.Name())
		g.Go(func() error {
			defer close(r.exited)
			err := c.Start(cctx)
			stopping := r.stopping.Load()
			switch {
			case stopping && (err == nil || onlyCanceled(err)):
				return nil // the expected end of a stop
			case err == nil:
				err = errors.New("exited unexpectedly")
			}
			slog.ErrorContext(ctx, "component failed", "component", c.Name(), "error", err)
			r.err = fmt.Errorf("component %s: %w", c.Name(), err)
			return r.err
		})
	}

	<-gctx.Done()
	// From here a second SIGINT or SIGTERM has its default effect and ends
	// the process at once.
	stopSignals()
	slog.InfoContext(ctx, "stopping components", "cause", context.Cause(gctx).Error(), "timeout", timeout.String())
	stopCtx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	var stopErrs []error
	timedOut := false
	for i := len(running) - 1; i >= 0 && !timedOut; i-- {
		exited, err := running[i].stop(stopCtx)
		if err != nil {
			stopErrs = append(stopErrs, err)
		}
		if !exited {
			timedOut = true
			left := stillRunning(running[:i+1])
			slog.ErrorContext(ctx, "stop timed out", "timeout", timeout.String(), "running", left)
			stopErrs = append(stopErrs, fmt.Errorf("stop timed out after %s; still running: %v", timeout, left))
		}
	}
	if !timedOut {
		_ = g.Wait() // every Start has returned; their failures are in r.err
	}
	var errs []error
	for _, r := range running {
		select {
		case <-r.exited:
			if r.err != nil {
				errs = append(errs, r.err)
			}
		default: // abandoned by the timeout
		}
	}
	return errors.Join(append(errs, stopErrs...)...)
}

// onlyCanceled reports whether err is a pure cancellation: every leaf of
// its error tree is context.Canceled. A cancellation joined with another
// error is not.
func onlyCanceled(err error) bool {
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if !onlyCanceled(e) {
				return false
			}
		}
		return len(u.Unwrap()) > 0
	case interface{ Unwrap() error }:
		return onlyCanceled(u.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}

// stillRunning returns the names of the components whose Start has not
// returned, in stop order.
func stillRunning(running []*runningComponent) []string {
	var names []string
	for i := len(running) - 1; i >= 0; i-- {
		select {
		case <-running[i].exited:
		default:
			names = append(names, running[i].Name())
		}
	}
	return names
}

type runningComponent struct {
	Component
	cancel   context.CancelFunc
	exited   chan struct{} // closed when Start returns
	err      error         // Start's failure; read only after exited is closed
	stopping atomic.Bool   // set before Stop is called; Start returning after it is not a failure
}

func (r *runningComponent) abandon() {
	r.stopping.Store(true)
	r.cancel()
}

// stop stops one component: Stop, then cancel its Start context and wait for
// Start to return, all bounded by ctx. It reports whether Start has
// returned, and Stop's error.
func (r *runningComponent) stop(ctx context.Context) (exited bool, err error) {
	select {
	case <-r.exited:
		return true, nil // it failed or exited already
	default:
	}
	slog.InfoContext(ctx, "component stopping", "component", r.Name())
	r.stopping.Store(true)
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(ctx) }()
	select {
	case err = <-stopped:
	case <-ctx.Done():
		return false, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "component stop failed", "component", r.Name(), "error", err)
		err = fmt.Errorf("stop component %s: %w", r.Name(), err)
	}
	r.cancel()
	select {
	case <-r.exited:
	case <-ctx.Done():
		return false, err
	}
	if err == nil {
		slog.InfoContext(ctx, "component stopped", "component", r.Name())
	}
	return true, err
}

// ShutdownOptions is the shutdown options block. Embed it in an options
// object under the key "shutdown"; its flags are --shutdown.timeout and
// --shutdown.drain_window.
type ShutdownOptions struct {
	Timeout time.Duration `mapstructure:"timeout"` // upper bound of the whole stop
	// DrainWindow is how long the process, once it reports itself not
	// ready, keeps serving before its components stop, so load balancers
	// take it out of rotation first. It is part of Timeout.
	DrainWindow time.Duration `mapstructure:"drain_window"`
}

// NewShutdownOptions returns the default shutdown options: a 30s timeout,
// no drain window.
func NewShutdownOptions() *ShutdownOptions {
	return &ShutdownOptions{Timeout: 30 * time.Second}
}

// AddFlags declares the block's flags on fs.
func (o *ShutdownOptions) AddFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.Timeout, "shutdown.timeout", o.Timeout,
		"upper bound of the graceful stop, such as 30s (shown in nanoseconds in config print and the start-up log)")
	fs.DurationVar(&o.DrainWindow, "shutdown.drain_window", o.DrainWindow,
		"how long to keep serving, reported not ready, before stopping, such as 5s; part of shutdown.timeout")
}

// Complete does nothing; the block has no derived fields.
func (o *ShutdownOptions) Complete() error { return nil }

// Validate reports a timeout that is not positive and a drain window that
// is negative or does not leave time to stop within the timeout.
func (o *ShutdownOptions) Validate() error {
	var errs []error
	if o.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("shutdown.timeout: invalid value %s, want a positive duration", o.Timeout))
	}
	if o.DrainWindow < 0 || (o.Timeout > 0 && o.DrainWindow >= o.Timeout) {
		errs = append(errs, fmt.Errorf("shutdown.drain_window: invalid value %s, want at least 0 and less than shutdown.timeout (%s)", o.DrainWindow, o.Timeout))
	}
	return errors.Join(errs...)
}
