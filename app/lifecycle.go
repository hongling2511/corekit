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
// It returns nil after a shutdown on cancellation or a signal, otherwise the
// failure of the first failing component, joined with any errors from
// stopping.
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
			if r.stopping.Load() {
				return nil // asked to stop
			}
			if err == nil {
				err = errors.New("exited unexpectedly")
			}
			slog.ErrorContext(ctx, "component failed", "component", c.Name(), "error", err)
			return fmt.Errorf("component %s: %w", c.Name(), err)
		})
	}

	<-gctx.Done()
	slog.InfoContext(ctx, "stopping components", "cause", context.Cause(gctx).Error(), "timeout", timeout.String())
	stopCtx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	var stopErrs []error
	for i := len(running) - 1; i >= 0; i-- {
		if err := running[i].stop(stopCtx); err != nil {
			stopErrs = append(stopErrs, err)
		}
		if stopCtx.Err() != nil {
			var left []string
			for j := i; j >= 0; j-- {
				select {
				case <-running[j].exited:
				default:
					left = append(left, running[j].Name())
				}
			}
			slog.ErrorContext(ctx, "stop timed out", "timeout", timeout.String(), "running", left)
			stopErrs = append(stopErrs, fmt.Errorf("stop timed out after %s; still running: %v", timeout, left))
			break
		}
	}
	// Every Start has returned unless the stop timed out; then the first
	// failure, if any, is the group context's cause.
	var startErr error
	if stopCtx.Err() == nil {
		startErr = g.Wait()
	} else if sigCtx.Err() == nil {
		startErr = context.Cause(gctx) // a component failed
	}
	return errors.Join(append([]error{startErr}, stopErrs...)...)
}

type runningComponent struct {
	Component
	cancel   context.CancelFunc
	exited   chan struct{} // closed when Start returns
	stopping atomic.Bool   // set before Stop is called; Start returning after it is not a failure
}

func (r *runningComponent) abandon() {
	r.stopping.Store(true)
	r.cancel()
}

// stop stops one component: Stop, then cancel its Start context and wait for
// Start to return, all bounded by ctx.
func (r *runningComponent) stop(ctx context.Context) error {
	select {
	case <-r.exited:
		return nil // it failed or exited already
	default:
	}
	slog.InfoContext(ctx, "component stopping", "component", r.Name())
	r.stopping.Store(true)
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(ctx) }()
	var err error
	select {
	case err = <-stopped:
	case <-ctx.Done():
		return nil // reported as a timeout by the caller
	}
	r.cancel()
	select {
	case <-r.exited:
	case <-ctx.Done():
		return err
	}
	if err != nil {
		slog.ErrorContext(ctx, "component stop failed", "component", r.Name(), "error", err)
		return fmt.Errorf("stop component %s: %w", r.Name(), err)
	}
	slog.InfoContext(ctx, "component stopped", "component", r.Name())
	return nil
}

// ShutdownOptions is the shutdown options block. Embed it in an options
// object under the key "shutdown"; its flag is --shutdown.timeout.
type ShutdownOptions struct {
	Timeout time.Duration `mapstructure:"timeout"` // upper bound of the whole stop
}

// NewShutdownOptions returns the default shutdown options: a 30s timeout.
func NewShutdownOptions() *ShutdownOptions {
	return &ShutdownOptions{Timeout: 30 * time.Second}
}

// AddFlags declares the block's flags on fs.
func (o *ShutdownOptions) AddFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.Timeout, "shutdown.timeout", o.Timeout, "upper bound of the graceful stop, such as 30s")
}

// Complete does nothing; the block has no derived fields.
func (o *ShutdownOptions) Complete() error { return nil }

// Validate reports a timeout that is not positive.
func (o *ShutdownOptions) Validate() error {
	if o.Timeout <= 0 {
		return fmt.Errorf("shutdown.timeout: invalid value %s, want a positive duration", o.Timeout)
	}
	return nil
}
