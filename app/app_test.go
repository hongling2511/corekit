package app_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/app"
	"github.com/hongling2511/corekit/log"
)

type dbOptions struct {
	Host     string `mapstructure:"host"`
	Password string `mapstructure:"password"`
}

type upstream struct {
	Name string `mapstructure:"name"`
	Key  string `mapstructure:"key" secret:"true"`
}

type secretiveOptions struct {
	Log       log.Options `mapstructure:"log"`
	Plain     string      `mapstructure:"plain"`
	DB        dbOptions   `mapstructure:"db" secret:"true"`
	Upstreams []upstream  `mapstructure:"upstreams"`
	Primary   *upstream   `mapstructure:"primary"`
}

func (o *secretiveOptions) AddFlags(fs *pflag.FlagSet) { o.Log.AddFlags(fs) }
func (o *secretiveOptions) Complete() error            { return o.Log.Complete() }
func (o *secretiveOptions) Validate() error            { return o.Log.Validate() }
func (o *secretiveOptions) Logging() *log.Options      { return &o.Log }

// capture runs f with os.Stdout and os.Stderr redirected and returns both.
func capture(t *testing.T, f func()) (stdout, stderr string) {
	t.Helper()
	read := func(target **os.File) func() string {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		orig := *target
		*target = w
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			*target = orig
			_ = w.Close()
			return <-done
		}
	}
	outDone, errDone := read(&os.Stdout), read(&os.Stderr)
	f()
	return outDone(), errDone()
}

func TestStartupLogAndConfigPrintHideCompositeSecrets(t *testing.T) {
	opts := &secretiveOptions{
		Log:       *log.NewOptions(),
		Plain:     "visible-value",
		DB:        dbOptions{Host: "db-host-secret", Password: "db-password-secret"},
		Upstreams: []upstream{{Name: "upstream-name", Key: "upstream-key-secret"}},
		Primary:   &upstream{Name: "primary-name", Key: "primary-key-secret"},
	}
	var code int

	stdout, stderr := capture(t, func() {
		code = app.New("test", opts).Run(context.Background(), []string{"config", "print", "--log.output=stderr"})
	})

	require.Equal(t, 0, code, stderr)
	for name, out := range map[string]string{"config print (stdout)": stdout, "start-up log (stderr)": stderr} {
		for _, secret := range []string{"db-host-secret", "db-password-secret", "upstream-key-secret", "primary-key-secret"} {
			assert.NotContains(t, out, secret, name)
		}
		for _, plain := range []string{"visible-value", "upstream-name", "primary-name"} {
			assert.Contains(t, out, plain, name)
		}
	}
	assert.True(t, strings.Contains(stderr, `"msg":"starting"`), stderr)
}

type plainOptions struct {
	Log log.Options `mapstructure:"log"`
}

func (o *plainOptions) AddFlags(fs *pflag.FlagSet) { o.Log.AddFlags(fs) }
func (o *plainOptions) Complete() error            { return o.Log.Complete() }
func (o *plainOptions) Validate() error            { return o.Log.Validate() }
func (o *plainOptions) Logging() *log.Options      { return &o.Log }

func TestDeclaredSubcommandLogsToConfiguredOutput(t *testing.T) {
	ran := false
	greet := app.Command{Name: "greet", Short: "Log a greeting", Run: func(ctx context.Context) error {
		ran = true
		slog.InfoContext(log.WithTraceID(ctx, "t-1"), "hello")
		return nil
	}}
	tests := []struct {
		args               []string
		wantOut, wantEmpty string
	}{
		{args: []string{"greet"}, wantOut: "stdout", wantEmpty: "stderr"},
		{args: []string{"greet", "--log.output=stderr"}, wantOut: "stderr", wantEmpty: "stdout"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ran = false
			opts := &plainOptions{Log: *log.NewOptions()}
			var code int

			stdout, stderr := capture(t, func() {
				code = app.New("test", opts, greet).Run(context.Background(), tt.args)
			})

			require.Equal(t, 0, code, stderr)
			assert.True(t, ran)
			outputs := map[string]string{"stdout": stdout, "stderr": stderr}
			assert.Contains(t, outputs[tt.wantOut], `"msg":"starting"`)
			assert.Contains(t, outputs[tt.wantOut], `"msg":"hello","trace_id":"t-1"`)
			assert.Empty(t, outputs[tt.wantEmpty])
		})
	}
}
