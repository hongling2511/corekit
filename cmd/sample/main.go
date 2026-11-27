// Command sample is the corekit sample binary. Its options object composes
// the library's log, shutdown and HTTP options blocks with fields of its own.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/errx"
	"github.com/hongling2511/corekit/httpx"
	"github.com/hongling2511/corekit/log"
)

// docs/errcodes.md is exported from the registry; a test fails when it is stale.
//go:generate sh ../../scripts/gen-errcodes.sh ../../docs/errcodes.md

// ErrUserNotFound is one of the sample's business error codes (service 11).
var ErrUserNotFound = errx.Register(110001, http.StatusNotFound, "user not found")

// Options is the sample's options object.
type Options struct {
	Log      log.Options         `mapstructure:"log"`
	Shutdown app.ShutdownOptions `mapstructure:"shutdown"`
	HTTP     httpx.Options       `mapstructure:"http"`
	Token    string              `mapstructure:"token" secret:"true"` // a secret, redacted when printed
	Labels   map[string]string   `mapstructure:"labels"`              // free-form name/value pairs, set in the config file
}

func newOptions() *Options {
	return &Options{Log: *log.NewOptions(), Shutdown: *app.NewShutdownOptions(), HTTP: *httpx.NewOptions()}
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.Log.AddFlags(fs)
	o.Shutdown.AddFlags(fs)
	o.HTTP.AddFlags(fs)
	fs.StringVar(&o.Token, "token", o.Token, "API token")
}

func (o *Options) Complete() error {
	return errors.Join(o.Log.Complete(), o.Shutdown.Complete(), o.HTTP.Complete())
}

func (o *Options) Validate() error {
	return errors.Join(o.Log.Validate(), o.Shutdown.Validate(), o.HTTP.Validate())
}

// Logging hands the log options block to the start-up pipeline.
func (o *Options) Logging() *log.Options { return &o.Log }

func main() {
	// gin's debug mode would print route tables to stdout, where the JSON logs go.
	gin.SetMode(gin.ReleaseMode)
	opts := newOptions()
	os.Exit(app.New("sample", opts, serveCommand(opts)).Run(context.Background(), os.Args[1:]))
}
