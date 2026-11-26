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
	"encoding"
	"fmt"
	"reflect"
	"slices"
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
	known := defaults(opts)
	for key, value := range known {
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
		v.AllowEmptyEnv(true) // a set but empty variable still overrides; Validate decides if empty is allowed
		v.AutomaticEnv()
	}
	if src.Flags != nil {
		if err := v.BindPFlags(src.Flags); err != nil {
			return fmt.Errorf("bind flags: %w", err)
		}
	}
	if err := checkKeys(v.AllKeys(), known); err != nil {
		return err
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

// checkKeys rejects keys that match no field of the options struct, such as
// a typo (log.leve1) in the configuration file. A key below a known key, as
// in labels.team for a map field labels, is accepted.
func checkKeys(keys []string, known map[string]any) error {
	var unknown []string
	for _, key := range keys {
		if !isKnown(key, known) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("unknown config keys: %s", strings.Join(unknown, ", "))
	}
	return nil
}

func isKnown(key string, known map[string]any) bool {
	if _, ok := known[key]; ok {
		return true
	}
	for k := range known {
		if strings.HasPrefix(key, k+".") {
			return true
		}
	}
	return false
}

// Redacted returns the configuration held by opts (a pointer to a struct)
// as dotted keys and values, for printing; it is empty for any other value.
//
// A non-empty field tagged `secret:"true"` is replaced with "******" as a
// whole, whatever its type. Nested structs (also behind pointers) flatten
// into dotted keys; structs inside slices, arrays, maps and interfaces become
// nested maps, with their own secret fields hidden. Values that cannot be
// shown safely (functions, channels, unsafe pointers) are hidden too.
func Redacted(opts any) map[string]any {
	out := map[string]any{}
	if v := reflect.Indirect(reflect.ValueOf(opts)); v.Kind() == reflect.Struct {
		flatten(v, "", out)
	}
	return out
}

// flatten writes the redacted fields of struct v into out under dotted keys.
func flatten(v reflect.Value, prefix string, out map[string]any) {
	for f, fv := range fields(v) {
		key := prefix + tagName(f)
		if isSecret(f) && !fv.IsZero() {
			out[key] = redacted
			continue
		}
		if s := reflect.Indirect(fv); s.Kind() == reflect.Struct && !isText(s) {
			flatten(s, key+".", out)
			continue
		}
		out[key] = project(fv)
	}
}

// project returns a printable copy of v with secrets hidden.
func project(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return project(v.Elem())
	case reflect.Struct:
		if isText(v) {
			text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return redacted
			}
			return string(text)
		}
		m := map[string]any{}
		for f, fv := range fields(v) {
			if isSecret(f) && !fv.IsZero() {
				m[tagName(f)] = redacted
				continue
			}
			m[tagName(f)] = project(fv)
		}
		return m
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		list := make([]any, v.Len())
		for i := range v.Len() {
			list[i] = project(v.Index(i))
		}
		return list
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		m := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			m[fmt.Sprint(it.Key().Interface())] = project(it.Value())
		}
		return m
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return redacted
	default:
		return v.Interface()
	}
}

// fields yields the configuration fields of struct v: exported and not
// tagged mapstructure:"-".
func fields(v reflect.Value) func(yield func(reflect.StructField, reflect.Value) bool) {
	return func(yield func(reflect.StructField, reflect.Value) bool) {
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() || tagName(f) == "-" {
				continue
			}
			if !yield(f, v.Field(i)) {
				return
			}
		}
	}
}

func isSecret(f reflect.StructField) bool { return f.Tag.Get("secret") == "true" }

// isText reports whether struct v prints as text (time.Time, for example).
func isText(v reflect.Value) bool {
	_, ok := v.Interface().(encoding.TextMarshaler)
	return ok
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
		if fv.Kind() == reflect.Struct && !isText(fv) {
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

const redacted = "******"
