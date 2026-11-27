// Command sample is the corekit sample binary. Its options object composes
// the library's log options block with one field of its own.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/log"
)

// Options is the sample's options object.
type Options struct {
	Log      log.Options         `mapstructure:"log"`
	Shutdown app.ShutdownOptions `mapstructure:"shutdown"`
	Token    string              `mapstructure:"token" secret:"true"` // a secret, redacted when printed
}

func newOptions() *Options {
	return &Options{Log: *log.NewOptions(), Shutdown: *app.NewShutdownOptions()}
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.Log.AddFlags(fs)
	o.Shutdown.AddFlags(fs)
	fs.StringVar(&o.Token, "token", o.Token, "API token")
}

func (o *Options) Complete() error { return errors.Join(o.Log.Complete(), o.Shutdown.Complete()) }

func (o *Options) Validate() error { return errors.Join(o.Log.Validate(), o.Shutdown.Validate()) }

// Logging hands the log options block to the start-up pipeline.
func (o *Options) Logging() *log.Options { return &o.Log }

func main() {
	opts := newOptions()
	os.Exit(app.New("sample", opts, serveCommand(opts)).Run(context.Background(), os.Args[1:]))
}
