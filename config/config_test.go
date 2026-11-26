package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/config"
)

type serverOptions struct {
	Port int `mapstructure:"port"`
}

// testOptions composes a nested block (server) the way business options
// compose the library's option blocks.
type testOptions struct {
	Name   string        `mapstructure:"name"`
	Server serverOptions `mapstructure:"server"`
}

func newTestOptions() *testOptions {
	return &testOptions{Name: "default", Server: serverOptions{Port: 1}}
}

func (o *testOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Name, "name", o.Name, "name")
	fs.IntVar(&o.Server.Port, "server.port", o.Server.Port, "port")
}

func (o *testOptions) Complete() error { return nil }
func (o *testOptions) Validate() error { return nil }

// load declares opts' flags, parses args and loads the configuration.
func load(t *testing.T, opts config.Options, file string, args ...string) error {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	opts.AddFlags(fs)
	require.NoError(t, fs.Parse(args))
	return config.Load(opts, config.Source{File: file, EnvPrefix: "TEST", Flags: fs})
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadPrecedence(t *testing.T) {
	const file = "name: from-file\nserver:\n  port: 2\n"
	tests := []struct {
		desc     string
		file     bool
		env      map[string]string
		args     []string
		wantName string
		wantPort int
	}{
		{desc: "default when nothing else is set", wantName: "default", wantPort: 1},
		{desc: "file beats default", file: true, wantName: "from-file", wantPort: 2},
		{
			desc: "env beats file", file: true,
			env:      map[string]string{"TEST_NAME": "from-env", "TEST_SERVER_PORT": "3"},
			wantName: "from-env", wantPort: 3,
		},
		{
			desc: "flag beats env", file: true,
			env:      map[string]string{"TEST_NAME": "from-env", "TEST_SERVER_PORT": "3"},
			args:     []string{"--name=from-flag", "--server.port=4"},
			wantName: "from-flag", wantPort: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			path := ""
			if tt.file {
				path = writeFile(t, "config.yaml", file)
			}
			opts := newTestOptions()

			require.NoError(t, load(t, opts, path, tt.args...))

			assert.Equal(t, tt.wantName, opts.Name)
			assert.Equal(t, tt.wantPort, opts.Server.Port)
		})
	}
}

// orderOptions records the order of Complete and Validate; Complete derives
// Addr, which Validate requires.
type orderOptions struct {
	Host  string `mapstructure:"host"`
	Addr  string `mapstructure:"-"`
	calls []string
}

func (o *orderOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Host, "host", o.Host, "host")
}

func (o *orderOptions) Complete() error {
	o.calls = append(o.calls, "complete")
	if o.Host != "" {
		o.Addr = o.Host + ":80"
	}
	return nil
}

func (o *orderOptions) Validate() error {
	o.calls = append(o.calls, "validate")
	if o.Addr == "" {
		return errors.New("host: must not be empty")
	}
	return nil
}

func TestLoadCompletesBeforeValidating(t *testing.T) {
	opts := &orderOptions{}

	require.NoError(t, load(t, opts, "", "--host=example.com"))

	assert.Equal(t, []string{"complete", "validate"}, opts.calls)
	assert.Equal(t, "example.com:80", opts.Addr)
}

func TestLoadReturnsValidationError(t *testing.T) {
	err := load(t, &orderOptions{}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host: must not be empty")
}

func TestLoadNamesTheKeyThatFailsToDecode(t *testing.T) {
	t.Setenv("TEST_SERVER_PORT", "not-a-number")

	err := load(t, newTestOptions(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.port")
}

type secretOptions struct {
	User     string        `mapstructure:"user"`
	Password string        `mapstructure:"password" secret:"true"`
	Empty    string        `mapstructure:"empty" secret:"true"`
	Server   serverOptions `mapstructure:"server"`
}

func TestRedactedHidesSecretsAndFlattensKeys(t *testing.T) {
	opts := &secretOptions{User: "alice", Password: "hunter2", Server: serverOptions{Port: 8080}}

	got := config.Redacted(opts)

	assert.Equal(t, map[string]any{
		"user":        "alice",
		"password":    "******",
		"empty":       "",
		"server.port": 8080,
	}, got)
}

func TestRedactedSkipsFieldsExcludedFromConfig(t *testing.T) {
	got := config.Redacted(&orderOptions{Host: "h", Addr: "derived"})

	assert.Equal(t, map[string]any{"host": "h"}, got)
}

func TestRedactedOfNonStructIsEmpty(t *testing.T) {
	assert.Empty(t, config.Redacted("not a struct"))
}

type credentials struct {
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

type upstream struct {
	Name string `mapstructure:"name"`
	Key  string `mapstructure:"key" secret:"true"`
}

type compositeOptions struct {
	Plain    string              `mapstructure:"plain"`
	Creds    credentials         `mapstructure:"creds" secret:"true"`
	CredsPtr *credentials        `mapstructure:"creds_ptr" secret:"true"`
	Upstream *upstream           `mapstructure:"upstream"`
	List     []upstream          `mapstructure:"list"`
	ByName   map[string]upstream `mapstructure:"by_name"`
	Ptrs     []*upstream         `mapstructure:"ptrs"`
	Keys     []string            `mapstructure:"keys" secret:"true"`
	Labels   map[string]string   `mapstructure:"labels"`
	Hidden   func()              `mapstructure:"hidden"`
	Any      any                 `mapstructure:"any"`
}

func TestRedactedHidesSecretsInCompositeValues(t *testing.T) {
	opts := &compositeOptions{
		Plain:    "visible",
		Creds:    credentials{User: "admin", Password: "pw-1"},
		CredsPtr: &credentials{User: "root", Password: "pw-2"},
		Upstream: &upstream{Name: "a", Key: "key-1"},
		List:     []upstream{{Name: "b", Key: "key-2"}},
		ByName:   map[string]upstream{"c": {Name: "c", Key: "key-3"}},
		Ptrs:     []*upstream{{Name: "d", Key: "key-4"}, nil},
		Keys:     []string{"key-5"},
		Labels:   map[string]string{"team": "core"},
		Hidden:   func() {},
		Any:      upstream{Name: "e", Key: "key-6"},
	}

	got := config.Redacted(opts)

	assert.Equal(t, map[string]any{
		"plain":         "visible",
		"creds":         "******",
		"creds_ptr":     "******",
		"upstream.name": "a",
		"upstream.key":  "******",
		"list":          []any{map[string]any{"name": "b", "key": "******"}},
		"by_name":       map[string]any{"c": map[string]any{"name": "c", "key": "******"}},
		"ptrs":          []any{map[string]any{"name": "d", "key": "******"}, nil},
		"keys":          "******",
		"labels":        map[string]any{"team": "core"},
		"hidden":        "******",
		"any":           map[string]any{"name": "e", "key": "******"},
	}, got)
}

func TestLoadRejectsUnknownKeysInFile(t *testing.T) {
	path := writeFile(t, "config.yaml", "name: x\nserver:\n  prot: 2\n")

	err := load(t, newTestOptions(), path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.prot")
}

func TestLoadEmptyEnvOverridesFile(t *testing.T) {
	t.Setenv("TEST_NAME", "")
	path := writeFile(t, "config.yaml", "name: from-file\n")
	opts := newTestOptions()

	require.NoError(t, load(t, opts, path))

	assert.Equal(t, "", opts.Name)
}
