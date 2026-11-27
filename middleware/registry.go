package middleware

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/spf13/pflag"
)

// Options is the optional-middleware options block. Embed it in an options
// object under the key "middleware"; its flags are --middleware.enabled and
// the parameters of the built-in optional middlewares.
type Options struct {
	// Enabled names the optional middlewares to install, in order. They run
	// after the fixed chain; a name missing here is not installed.
	Enabled   []string         `mapstructure:"enabled"`
	CORS      CORSOptions      `mapstructure:"cors"`
	Timeout   TimeoutOptions   `mapstructure:"timeout"`
	RateLimit RateLimitOptions `mapstructure:"ratelimit"`
}

// NewOptions returns the default options: no optional middleware enabled.
func NewOptions() *Options {
	return &Options{Enabled: []string{}, CORS: newCORSOptions(), Timeout: newTimeoutOptions(), RateLimit: newRateLimitOptions()}
}

// AddFlags declares the block's flags on fs.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&o.Enabled, "middleware.enabled", o.Enabled, "optional middlewares to install after the fixed chain, in order, such as cors,timeout,ratelimit")
	o.CORS.addFlags(fs)
	o.Timeout.addFlags(fs)
	o.RateLimit.addFlags(fs)
}

// Complete does nothing; the block has no derived fields.
func (o *Options) Complete() error { return nil }

// Validate reports every invalid value, naming its key. Whether a name is
// registered is checked by Registry.Build.
func (o *Options) Validate() error {
	var errs []error
	seen := map[string]bool{}
	for _, name := range o.Enabled {
		switch {
		case name == "":
			errs = append(errs, errors.New("middleware.enabled: empty middleware name"))
		case seen[name]:
			errs = append(errs, fmt.Errorf("middleware.enabled: middleware %q is listed twice", name))
		}
		seen[name] = true
	}
	errs = append(errs, o.CORS.validate()...)
	errs = append(errs, o.Timeout.validate()...)
	errs = append(errs, o.RateLimit.validate()...)
	return errors.Join(errs...)
}

// Factory builds an optional middleware. fail answers a request with the
// failure response for an error.
type Factory func(fail FailFunc) gin.HandlerFunc

// Registry maps names to optional middlewares and builds the ones the
// configuration enables. It belongs to whoever assembles the server: there
// is no package-level registry.
type Registry struct {
	opts      *Options
	factories map[string]Factory
}

// NewRegistry returns a registry for opts holding the built-in optional
// middlewares. Register adds the program's own.
func NewRegistry(opts *Options) *Registry {
	r := &Registry{opts: opts, factories: map[string]Factory{}}
	r.Register("cors", func(FailFunc) gin.HandlerFunc { return CORS(opts.CORS) })
	r.Register("timeout", func(fail FailFunc) gin.HandlerFunc { return Timeout(opts.Timeout, fail) })
	r.Register("ratelimit", func(fail FailFunc) gin.HandlerFunc { return RateLimit(opts.RateLimit, fail) })
	return r
}

// Register adds the optional middleware name, built by f. Register at
// start-up: an empty name, a nil factory or a name registered twice is a
// programming error and panics.
func (r *Registry) Register(name string, f Factory) {
	switch {
	case name == "":
		panic("middleware: Register with an empty name")
	case f == nil:
		panic(fmt.Sprintf("middleware: Register %q with a nil factory", name))
	case r.factories[name] != nil:
		panic(fmt.Sprintf("middleware: %q is already registered", name))
	}
	r.factories[name] = f
}

// Build returns the middlewares the options enable, in their order, each
// built with fail. A name that is not registered is an error naming it.
func (r *Registry) Build(fail FailFunc) ([]gin.HandlerFunc, error) {
	out := make([]gin.HandlerFunc, 0, len(r.opts.Enabled))
	for _, name := range r.opts.Enabled {
		f, ok := r.factories[name]
		if !ok {
			return nil, fmt.Errorf("middleware.enabled: unknown middleware %q", name)
		}
		out = append(out, f(fail))
	}
	return out, nil
}
