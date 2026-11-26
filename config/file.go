package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// readFile reads the configuration file at path, the format given by its
// extension, into one key tree: dotted keys (server.port: 5) are expanded
// into nested maps. The same tree is checked against the options type t and
// then handed to v, so every key that passes the check is the key loaded.
func readFile(ctx context.Context, v *viper.Viper, path string, t reflect.Type) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}
	dec, err := viper.NewCodecRegistry().Decoder(strings.TrimPrefix(filepath.Ext(path), "."))
	if err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	raw := map[string]any{}
	if err := dec.Decode(b, raw); err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	tree, err := expand(raw, "")
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if unknown := checkTree(tree, t, ""); len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("config file %s: unknown keys: %s", path, strings.Join(unknown, ", "))
	}
	if err := v.MergeConfigMap(tree.(map[string]any)); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

// expand returns raw with every dotted map key split into nested maps, also
// inside lists. Keys are lower-cased, as viper treats them. A key set twice
// (server.port and server: {port}) is an error.
func expand(raw any, path string) (any, error) {
	switch raw := raw.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range raw {
			parts := strings.Split(strings.ToLower(k), ".")
			// Wrap val in maps for all but the first part of the key.
			for i := len(parts) - 1; i > 0; i-- {
				val = map[string]any{parts[i]: val}
			}
			val, err := expand(val, path+parts[0]+".")
			if err != nil {
				return nil, err
			}
			if err := merge(out, parts[0], val, path); err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(raw))
		for i, val := range raw {
			var err error
			if out[i], err = expand(val, path+strconv.Itoa(i)+"."); err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return raw, nil
	}
}

// merge sets dst[key] = val, merging nested maps.
func merge(dst map[string]any, key string, val any, path string) error {
	old, ok := dst[key]
	if !ok {
		dst[key] = val
		return nil
	}
	oldMap, ok1 := old.(map[string]any)
	newMap, ok2 := val.(map[string]any)
	if !ok1 || !ok2 {
		return fmt.Errorf("key %s is set twice", path+key)
	}
	for k, v := range newMap {
		if err := merge(oldMap, k, v, path+key+"."); err != nil {
			return err
		}
	}
	return nil
}

// checkTree returns the dotted path of every key in the configuration tree
// that matches no field of type t. Map entry names are free; struct fields
// inside map values, list elements and pointers are checked, and a scalar
// field takes no keys below it. Interface fields take anything; other shape
// mismatches are left to the decoder.
func checkTree(tree any, t reflect.Type, path string) []string {
	t = derefType(t)
	m, isMap := tree.(map[string]any)
	switch t.Kind() {
	case reflect.Interface:
		return nil
	case reflect.Struct:
		if !isMap {
			return nil
		}
		byKey := map[string]field{}
		for _, f := range structFields(t) {
			byKey[strings.ToLower(f.key)] = f
		}
		var unknown []string
		for k, val := range m {
			f, ok := byKey[k]
			if !ok {
				unknown = append(unknown, path+k)
				continue
			}
			unknown = append(unknown, checkTree(val, f.typ, path+k+".")...)
		}
		return unknown
	case reflect.Map:
		var unknown []string
		for k, val := range m {
			unknown = append(unknown, checkTree(val, t.Elem(), path+k+".")...)
		}
		return unknown
	case reflect.Slice, reflect.Array:
		list, _ := tree.([]any)
		var unknown []string
		for i, val := range list {
			unknown = append(unknown, checkTree(val, t.Elem(), path+strconv.Itoa(i)+".")...)
		}
		return unknown
	default: // a scalar: any key below it is unknown
		var unknown []string
		for k := range m {
			unknown = append(unknown, leaves(m[k], path+k)...)
		}
		return unknown
	}
}

// leaves returns the dotted paths of the leaves below tree.
func leaves(tree any, path string) []string {
	m, ok := tree.(map[string]any)
	if !ok || len(m) == 0 {
		return []string{path}
	}
	var out []string
	for k, val := range m {
		out = append(out, leaves(val, path+"."+k)...)
	}
	return out
}
