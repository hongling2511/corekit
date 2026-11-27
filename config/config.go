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
	"context"
	"fmt"
	"reflect"
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
	if err := checkType(reflect.TypeOf(opts)); err != nil {
		return err
	}
	v := viper.New()
	for key, value := range defaults(opts) {
		v.SetDefault(key, value)
	}
	if src.File != "" {
		if err := readFile(ctx, v, src.File, reflect.TypeOf(opts)); err != nil {
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

// defaults flattens opts (a pointer to a struct) into dotted keys and the
// values it holds, the lowest-precedence source. Blocks, also behind a
// non-nil pointer, flatten further, so environment variables reach their
// fields; a nil pointer block is one key.
func defaults(opts any) map[string]any {
	out := map[string]any{}
	if v := reflect.Indirect(reflect.ValueOf(opts)); v.Kind() == reflect.Struct {
		walkDefaults(v, "", out, map[uintptr]bool{})
	}
	return out
}

func walkDefaults(v reflect.Value, prefix string, out map[string]any, onPath map[uintptr]bool) {
	for _, f := range structFields(v.Type()) {
		fv := v.FieldByIndex(f.index)
		switch {
		case isBlock(f.typ):
			walkDefaults(fv, prefix+f.key+".", out, onPath)
		case isBlockPointer(f.typ) && !fv.IsNil() && !onPath[fv.Pointer()]:
			onPath[fv.Pointer()] = true
			walkDefaults(fv.Elem(), prefix+f.key+".", out, onPath)
			delete(onPath, fv.Pointer())
		default:
			out[prefix+f.key] = fv.Interface()
		}
	}
}
