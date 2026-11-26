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
// opts.Complete and opts.Validate.
func Load(opts Options, src Source) error {
	v := viper.New()
	for key, value := range values(opts, false) {
		v.SetDefault(key, value)
	}
	if src.File != "" {
		v.SetConfigFile(src.File)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file: %w", err)
		}
	}
	if src.EnvPrefix != "" {
		v.SetEnvPrefix(src.EnvPrefix)
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
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

// Redacted returns the configuration held by opts (a pointer to a struct)
// as dotted keys and values, for printing; it is empty for any other value. Non-empty fields tagged
// `secret:"true"` are replaced with "******".
func Redacted(opts any) map[string]any {
	return values(opts, true)
}

// values flattens opts into dotted keys and their current values. With
// redact, non-empty fields tagged `secret:"true"` are replaced.
func values(opts any, redact bool) map[string]any {
	out := map[string]any{}
	if v := reflect.Indirect(reflect.ValueOf(opts)); v.Kind() == reflect.Struct {
		walk(v, "", redact, out)
	}
	return out
}

func walk(v reflect.Value, prefix string, redact bool, out map[string]any) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name := tagName(f)
		if !f.IsExported() || name == "-" {
			continue
		}
		key := prefix + name
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct {
			walk(fv, key+".", redact, out)
			continue
		}
		if redact && f.Tag.Get("secret") == "true" && !fv.IsZero() {
			out[key] = redacted
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

const redacted = "******"
