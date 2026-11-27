// Package log sets up the standard library's log/slog: an embeddable options
// block, a logger constructor (JSON for production, text for development,
// writing only to stdout or stderr) and a Handler that adds the trace_id and
// user identifier carried by the context to every record.
package log

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

// Options is the log options block. Embed it in an options object under the
// key "log"; its flags are --log.level, --log.format and --log.output.
type Options struct {
	Level  string `mapstructure:"level"`  // debug, info, warn or error
	Format string `mapstructure:"format"` // json (production) or text (development)
	Output string `mapstructure:"output"` // stdout or stderr
}

// NewOptions returns the default log options: info, json, stdout.
func NewOptions() *Options {
	return &Options{Level: "info", Format: "json", Output: "stdout"}
}

// AddFlags declares the block's flags on fs.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Level, "log.level", o.Level, "log level: debug, info, warn or error")
	fs.StringVar(&o.Format, "log.format", o.Format, "log format: json or text")
	fs.StringVar(&o.Output, "log.output", o.Output, "log output: stdout or stderr")
}

// Complete normalises the values to lower case.
func (o *Options) Complete() error {
	o.Level = strings.ToLower(o.Level)
	o.Format = strings.ToLower(o.Format)
	o.Output = strings.ToLower(o.Output)
	return nil
}

// Validate reports every invalid value, naming its key.
func (o *Options) Validate() error {
	var errs []error
	if _, err := parseLevel(o.Level); err != nil {
		errs = append(errs, fmt.Errorf("log.level: invalid value %q, want debug, info, warn or error", o.Level))
	}
	if o.Format != "json" && o.Format != "text" {
		errs = append(errs, fmt.Errorf("log.format: invalid value %q, want json or text", o.Format))
	}
	if o.Output != "stdout" && o.Output != "stderr" {
		errs = append(errs, fmt.Errorf("log.output: invalid value %q, want stdout or stderr", o.Output))
	}
	return errors.Join(errs...)
}

// New returns a logger configured by o (completed and validated on a copy),
// wrapped in the context Handler.
func New(opts *Options) (*slog.Logger, error) {
	o := *opts
	if err := o.Complete(); err != nil {
		return nil, err
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	level, _ := parseLevel(o.Level)
	var w io.Writer = os.Stdout
	if o.Output == "stderr" {
		w = os.Stderr
	}
	ho := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(w, ho)
	if o.Format == "text" {
		h = slog.NewTextHandler(w, ho)
	}
	return slog.New(NewContextHandler(h)), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug", "info", "warn", "error":
		var l slog.Level
		err := l.UnmarshalText([]byte(s))
		return l, err
	}
	return 0, fmt.Errorf("unknown level %q", s)
}
