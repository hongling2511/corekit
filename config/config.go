// Package config loads a strongly typed options struct from command-line
// flags, environment variables, a configuration file and defaults, in that
// order of precedence. viper is used only inside this package.
//
// Configuration keys come from `mapstructure` struct tags; nested structs
// form dotted keys (server.port). A flag binds to the key equal to its name,
// and an environment variable is the key upper-cased with "." replaced by
// "_" behind a prefix (TEST_SERVER_PORT). Defaults are the values the
// options struct holds before loading.
package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Options is an options object: it declares its flags, completes itself
// (defaults and derived fields) and validates itself. Complete always runs
// before Validate.
type Options interface {
	AddFlags(fs *pflag.FlagSet)
	Complete() error
	Validate() error
}

// Source says where configuration comes from besides the defaults.
type Source struct {
	File      string         // configuration file; empty reads no file
	EnvPrefix string         // environment variable prefix; empty reads no environment
	Flags     *pflag.FlagSet // parsed flags declared by Options.AddFlags; may be nil
}

// Load merges the sources into opts (a pointer to a struct), then calls
// opts.Complete and opts.Validate. It returns ctx's error, without loading,
// if ctx is already done.
func Load(ctx context.Context, opts Options, src Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v := viper.New()
	for key, value := range defaults(opts) {
		v.SetDefault(key, value)
	}
	if src.File != "" {
		if err := readFile(v, src.File, reflect.TypeOf(opts)); err != nil {
			return err
		}
	}
	if src.EnvPrefix != "" {
		v.SetEnvPrefix(src.EnvPrefix)
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		v.AllowEmptyEnv(true) // a set but empty variable still overrides; Validate decides if empty is allowed
		v.AutomaticEnv()
	}
	if src.Flags != nil {
		if err := v.BindPFlags(src.Flags); err != nil {
			return fmt.Errorf("bind flags: %w", err)
		}
	}
	if err := v.Unmarshal(opts); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if err := opts.Complete(); err != nil {
		return fmt.Errorf("complete config: %w", err)
	}
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}

// readFile reads the configuration file at path into v, rejecting keys that
// match no field of the options type t (a typo such as log.leve1). The format
// comes from the file extension.
func readFile(v *viper.Viper, path string, t reflect.Type) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}
	format := strings.TrimPrefix(filepath.Ext(path), ".")
	dec, err := viper.NewCodecRegistry().Decoder(format)
	if err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	raw := map[string]any{}
	if err := dec.Decode(b, raw); err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	var unknown []string
	checkTree(raw, t, "", &unknown)
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("config file %s: unknown keys: %s", path, strings.Join(unknown, ", "))
	}
	v.SetConfigType(format)
	if err := v.ReadConfig(bytes.NewReader(b)); err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	return nil
}

// checkTree appends to unknown the dotted path of every key in the raw
// configuration tree that matches no field of type t. Map entry names are
// free, but struct fields inside map values, slice elements and pointers are
// checked. Values whose shape does not fit t are left to the decoder.
func checkTree(raw any, t reflect.Type, path string, unknown *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := raw.(map[string]any)
		if !ok {
			return
		}
		fieldTypes := map[string]reflect.Type{}
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && tagName(f) != "-" {
				fieldTypes[strings.ToLower(tagName(f))] = f.Type
			}
		}
		for k, val := range m {
			ft, ok := fieldTypes[strings.ToLower(k)]
			if !ok {
				*unknown = append(*unknown, path+k)
				continue
			}
			checkTree(val, ft, path+k+".", unknown)
		}
	case reflect.Map:
		if m, ok := raw.(map[string]any); ok {
			for k, val := range m {
				checkTree(val, t.Elem(), path+k+".", unknown)
			}
		}
	case reflect.Slice, reflect.Array:
		if list, ok := raw.([]any); ok {
			for i, val := range list {
				checkTree(val, t.Elem(), path+strconv.Itoa(i)+".", unknown)
			}
		}
	}
}

// defaults flattens opts (a pointer to a struct) into dotted keys and the
// values it holds, the lowest-precedence source.
func defaults(opts any) map[string]any {
	out := map[string]any{}
	if v := reflect.Indirect(reflect.ValueOf(opts)); v.Kind() == reflect.Struct {
		walkDefaults(v, "", out)
	}
	return out
}

func walkDefaults(v reflect.Value, prefix string, out map[string]any) {
	for f, fv := range fields(v) {
		key := prefix + tagName(f)
		if fv.Kind() == reflect.Struct && hasFields(fv) {
			walkDefaults(fv, key+".", out)
			continue
		}
		out[key] = fv.Interface()
	}
}

// tagName returns a field's key from its mapstructure tag, or the lower-cased
// field name when untagged; "-" marks a field that is not configuration.
func tagName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	return name
}
