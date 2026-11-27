package httpx

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/pflag"
)

// Options is the HTTP server options block. Embed it in an options object
// under the key "http"; its flags are --http.addr, --http.read_timeout,
// --http.write_timeout, --http.idle_timeout, --http.tls.cert_file and
// --http.tls.key_file.
//
// TLS is normally terminated by the gateway in front of the service; set
// both TLS files to terminate it in the server instead. A server serves
// either HTTP or HTTPS, never both.
type Options struct {
	Addr         string        `mapstructure:"addr"`          // listen address, host:port
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`  // reading a whole request, body included
	WriteTimeout time.Duration `mapstructure:"write_timeout"` // from the end of the request headers to the end of the response
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`  // keep-alive connections between requests
	TLS          TLSOptions    `mapstructure:"tls"`
}

// TLSOptions is the optional in-server TLS: both files, or neither.
type TLSOptions struct {
	CertFile string `mapstructure:"cert_file"` // PEM certificate chain
	KeyFile  string `mapstructure:"key_file"`  // PEM private key
}

// NewOptions returns the default HTTP options: :8080, 30s read and write
// timeouts, 120s idle timeout, no TLS.
func NewOptions() *Options {
	return &Options{Addr: ":8080", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
}

// AddFlags declares the block's flags on fs.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Addr, "http.addr", o.Addr, "HTTP listen address, host:port")
	fs.DurationVar(&o.ReadTimeout, "http.read_timeout", o.ReadTimeout, "upper bound for reading a request, such as 30s")
	fs.DurationVar(&o.WriteTimeout, "http.write_timeout", o.WriteTimeout, "upper bound for writing a response, such as 30s")
	fs.DurationVar(&o.IdleTimeout, "http.idle_timeout", o.IdleTimeout, "how long an idle keep-alive connection stays open, such as 120s")
	fs.StringVar(&o.TLS.CertFile, "http.tls.cert_file", o.TLS.CertFile, "TLS certificate file; set with http.tls.key_file to serve HTTPS")
	fs.StringVar(&o.TLS.KeyFile, "http.tls.key_file", o.TLS.KeyFile, "TLS private key file; set with http.tls.cert_file to serve HTTPS")
}

// Complete does nothing; the block has no derived fields.
func (o *Options) Complete() error { return nil }

// Validate reports every invalid value, naming its key.
func (o *Options) Validate() error {
	var errs []error
	if o.Addr == "" {
		errs = append(errs, errors.New("http.addr: must not be empty"))
	}
	for _, d := range []struct {
		key string
		v   time.Duration
	}{{"http.read_timeout", o.ReadTimeout}, {"http.write_timeout", o.WriteTimeout}, {"http.idle_timeout", o.IdleTimeout}} {
		if d.v <= 0 {
			errs = append(errs, fmt.Errorf("%s: invalid value %s, want a positive duration", d.key, d.v))
		}
	}
	if (o.TLS.CertFile == "") != (o.TLS.KeyFile == "") {
		errs = append(errs, errors.New("http.tls: set both cert_file and key_file, or neither"))
	}
	return errors.Join(errs...)
}
