package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/app"
)

// demoOptions are knobs of the placeholder components that `serve` runs
// until real services exist; they demonstrate the shutdown behaviour.
type demoOptions struct {
	Fail time.Duration `mapstructure:"fail"` // the second component fails this long after starting; 0 never
	Hang bool          `mapstructure:"hang"` // the second component's Stop ignores its deadline
}

func (o *demoOptions) AddFlags(fs *pflag.FlagSet) {
	fs.DurationVar(&o.Fail, "demo.fail", o.Fail, "placeholder component fails this long after starting (0: never)")
	fs.BoolVar(&o.Hang, "demo.hang", o.Hang, "placeholder component hangs when stopped")
}

func (o *demoOptions) Validate() error {
	if o.Fail < 0 {
		return fmt.Errorf("demo.fail: invalid value %s, want 0 or a positive duration", o.Fail)
	}
	return nil
}

func serveCommand(opts *Options) app.Command {
	return app.Command{
		Name:  "serve",
		Short: "Run the service until SIGINT or SIGTERM",
		Run: func(ctx context.Context) error {
			return app.RunComponents(ctx, opts.Shutdown.Timeout,
				&placeholder{name: "first"},
				&placeholder{name: "second", fail: opts.Demo.Fail, hang: opts.Demo.Hang},
			)
		},
	}
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
