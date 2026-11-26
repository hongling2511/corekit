package config_test

import (
	"context"
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
	return config.Load(context.Background(), opts, config.Source{File: file, EnvPrefix: "TEST", Flags: fs})
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
		"server.port": int64(8080),
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

func TestLoadStopsWhenContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts := newTestOptions()

	err := config.Load(ctx, opts, config.Source{EnvPrefix: "TEST"})

	require.ErrorIs(t, err, context.Canceled)
}

// textCreds formats itself as text, including its secret; Redacted must not
// trust that text.
type textCreds struct {
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password" secret:"true"`
}

func (c textCreds) MarshalText() ([]byte, error) { return []byte(c.User + ":" + c.Password), nil }

// loudName is a plain field whose type formats itself; Redacted prints the
// underlying value only.
type loudName string

func (n loudName) MarshalJSON() ([]byte, error) { return []byte(`"leak"`), nil }
func (n loudName) String() string               { return "leak" }

type keyed struct {
	ID    string `mapstructure:"id"`
	Token string `mapstructure:"token" secret:"true"`
}

type opaque struct{ hidden string }

type trickyOptions struct {
	Text    textCreds        `mapstructure:"text"`
	TextPtr *textCreds       `mapstructure:"text_ptr"`
	Name    loudName         `mapstructure:"name"`
	ByKey   map[keyed]string `mapstructure:"by_key"`
	Opaque  opaque           `mapstructure:"opaque"`
	Counts  map[loudName]int `mapstructure:"counts"`
	Texts   []textCreds      `mapstructure:"texts"`
	Any     map[string]any   `mapstructure:"any"`
}

func TestRedactedDoesNotTrustCustomFormatting(t *testing.T) {
	opts := &trickyOptions{
		Text:    textCreds{User: "u1", Password: "pw-1"},
		TextPtr: &textCreds{User: "u2", Password: "pw-2"},
		Name:    "plain-name",
		ByKey:   map[keyed]string{{ID: "k", Token: "tok-1"}: "v"},
		Opaque:  opaque{hidden: "x"},
		Counts:  map[loudName]int{"n": 1},
		Texts:   []textCreds{{User: "u3", Password: "pw-3"}},
		Any:     map[string]any{"c": textCreds{User: "u4", Password: "pw-4"}},
	}

	got := config.Redacted(opts)

	assert.Equal(t, map[string]any{
		"text.user":         "u1",
		"text.password":     "******",
		"text_ptr.user":     "u2",
		"text_ptr.password": "******",
		"name":              "plain-name",
		"by_key":            "******",
		"opaque":            "******",
		"counts":            map[string]any{"n": int64(1)},
		"texts":             []any{map[string]any{"user": "u3", "password": "******"}},
		"any":               map[string]any{"c": map[string]any{"user": "u4", "password": "******"}},
	}, got)
}

type node struct {
	Name string `mapstructure:"name"`
	Next *node  `mapstructure:"next"`
}

type cyclicOptions struct {
	Head   *node          `mapstructure:"head"`
	A      *node          `mapstructure:"a"`
	B      *node          `mapstructure:"b"`
	Nested map[string]any `mapstructure:"nested"`
}

func TestRedactedCutsCyclesAndKeepsSharedValues(t *testing.T) {
	head := &node{Name: "head"}
	head.Next = head
	shared := &node{Name: "shared"}
	nested := map[string]any{"name": "m"}
	nested["self"] = nested
	opts := &cyclicOptions{Head: head, A: shared, B: shared, Nested: nested}

	got := config.Redacted(opts)

	assert.Equal(t, map[string]any{
		"head.name": "head",
		"head.next": "<cycle>",
		"a.name":    "shared",
		"a.next":    nil,
		"b.name":    "shared",
		"b.next":    nil,
		"nested":    map[string]any{"name": "m", "self": "<cycle>"},
	}, got)
}

type item struct {
	Name string `mapstructure:"name"`
}

type treeOptions struct {
	Server serverOptions     `mapstructure:"server"`
	Ptr    *item             `mapstructure:"ptr"`
	Items  []item            `mapstructure:"items"`
	ByName map[string]item   `mapstructure:"by_name"`
	Labels map[string]string `mapstructure:"labels"`
}

func (o *treeOptions) AddFlags(*pflag.FlagSet) {}
func (o *treeOptions) Complete() error         { return nil }
func (o *treeOptions) Validate() error         { return nil }

func TestLoadRejectsUnknownKeysAnywhereInTheFile(t *testing.T) {
	tests := []struct{ file, want string }{
		{"bogus: {}\n", "bogus"},
		{"server:\n  prot: {}\n", "server.prot"},
		{"ptr:\n  nmae: x\n", "ptr.nmae"},
		{"items:\n  - name: a\n  - nmae: b\n", "items.1.nmae"},
		{"by_name:\n  first:\n    nmae: x\n", "by_name.first.nmae"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			err := load(t, &treeOptions{}, writeFile(t, "config.yaml", tt.file))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLoadAcceptsDynamicMapEntries(t *testing.T) {
	file := "server:\n  port: 2\nptr:\n  name: p\nitems:\n  - name: a\nby_name:\n  first:\n    name: x\nlabels:\n  team: core\n"
	opts := &treeOptions{}

	require.NoError(t, load(t, opts, writeFile(t, "config.yaml", file)))

	assert.Equal(t, &treeOptions{
		Server: serverOptions{Port: 2},
		Ptr:    &item{Name: "p"},
		Items:  []item{{Name: "a"}},
		ByName: map[string]item{"first": {Name: "x"}},
		Labels: map[string]string{"team": "core"},
	}, opts)
}

func TestLoadAcceptsDottedKeysInFile(t *testing.T) {
	opts := newTestOptions()

	require.NoError(t, load(t, opts, writeFile(t, "config.yaml", "server.port: 5\n")))
	assert.Equal(t, 5, opts.Server.Port)

	err := load(t, newTestOptions(), writeFile(t, "config.yaml", "server.prot: 5\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.prot")
}

type BaseOptions struct {
	Name   string `mapstructure:"name"`
	Secret string `mapstructure:"secret" secret:"true"`
}

type squashOptions struct {
	BaseOptions `mapstructure:",squash"`
	Port        int `mapstructure:"port"`
}

func (o *squashOptions) AddFlags(*pflag.FlagSet) {}
func (o *squashOptions) Complete() error         { return nil }
func (o *squashOptions) Validate() error         { return nil }

func TestSquashedFieldsUseTheParentKeys(t *testing.T) {
	opts := &squashOptions{}

	require.NoError(t, load(t, opts, writeFile(t, "config.yaml", "name: x\nsecret: s\nport: 1\n")))

	assert.Equal(t, "x", opts.Name)
	assert.Equal(t, map[string]any{"name": "x", "secret": "******", "port": int64(1)}, config.Redacted(opts))
	err := load(t, &squashOptions{}, writeFile(t, "config.yaml", "baseoptions:\n  name: x\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseoptions")
}

type entry struct {
	Name string `mapstructure:"name"`
}

type keyTreeOptions struct {
	Server  serverOptions    `mapstructure:"server"`
	Entries map[string]entry `mapstructure:"entries"`
}

func (o *keyTreeOptions) AddFlags(*pflag.FlagSet) {}
func (o *keyTreeOptions) Complete() error         { return nil }
func (o *keyTreeOptions) Validate() error         { return nil }

func TestLoadKeyTree(t *testing.T) {
	tests := []struct {
		desc    string
		file    string
		want    *keyTreeOptions
		wantErr string
	}{
		{
			desc: "nested", file: "server:\n  port: 5\nentries:\n  foo:\n    name: valid\n",
			want: &keyTreeOptions{Server: serverOptions{Port: 5}, Entries: map[string]entry{"foo": {Name: "valid"}}},
		},
		{
			desc: "dotted", file: "server.port: 5\nentries.foo.name: valid\n",
			want: &keyTreeOptions{Server: serverOptions{Port: 5}, Entries: map[string]entry{"foo": {Name: "valid"}}},
		},
		{
			desc: "dotted and nested merge", file: "entries.foo.name: a\nentries:\n  bar:\n    name: b\n",
			want: &keyTreeOptions{Server: serverOptions{Port: 1}, Entries: map[string]entry{"foo": {Name: "a"}, "bar": {Name: "b"}}},
		},
		{desc: "key below a scalar", file: "server.port.typo: 5\n", wantErr: "server.port.typo"},
		{desc: "nested key below a scalar", file: "server:\n  port:\n    typo: 5\n", wantErr: "server.port.typo"},
		{desc: "typo in a dotted map entry", file: "entries.foo.nmae: x\n", wantErr: "entries.foo.nmae"},
		{desc: "same key twice", file: "server.port: 5\nserver:\n  port: 6\n", wantErr: "server.port"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			opts := &keyTreeOptions{Server: serverOptions{Port: 1}}

			err := load(t, opts, writeFile(t, "config.yaml", tt.file))

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts)
		})
	}
}

type viewOptions struct {
	Items []any `mapstructure:"items"`
}

func TestRedactedPrintsSliceViewsThatShareStorage(t *testing.T) {
	// The second element is a shorter view of the same storage, not a cycle.
	items := []any{"visible", nil}
	items[1] = items[:1]

	got := config.Redacted(&viewOptions{Items: items})

	assert.Equal(t, []any{"visible", []any{"visible"}}, got["items"])
}
