package main

import (
	"context"
	"errors"
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

func serveCommand(opts *Options) app.Command {
	return app.Command{
		Name:  "serve",
		Short: "Run the service until SIGINT or SIGTERM",
		Run: func(ctx context.Context) error {
			return app.RunComponents(ctx, opts.Shutdown.Timeout,
				&placeholder{name: "first"},
				&placeholder{name: "second", demoOptions: opts.Demo},
			)
		},
	}
}

// placeholder is a component that does nothing until it is stopped.
type placeholder struct {
	name string
	demoOptions
}

func (p *placeholder) Name() string { return p.name }

func (p *placeholder) Start(ctx context.Context) error {
	var fail <-chan time.Time
	if p.Fail > 0 {
		fail = time.After(p.Fail)
	}
	select {
	case <-ctx.Done():
		return nil
	case <-fail:
		return errors.New("demo failure")
	}
}

func (p *placeholder) Stop(context.Context) error {
	if p.Hang {
		select {} // never returns: the shutdown timeout must end the process
	}
	return nil
}
