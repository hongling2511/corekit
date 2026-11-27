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
	expanded, err := expand(raw, "", false)
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	tree := expanded.(map[string]any) // expand keeps a map a map
	unknown, err := checkTree(tree, t, "")
	if err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("config file %s: unknown keys: %s", path, strings.Join(unknown, ", "))
	}
	if err := v.MergeConfigMap(tree); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

// expand returns raw as viper will load it: keys lower-cased, maps with
// non-string keys turned into string-keyed maps, and every dotted key of a
// map outside lists split into nested maps (so a map entry name cannot
// contain "."). Inside list elements keys are kept whole, as viper keeps them.
// A key set twice in the decoded tree (server.port and server: {port}, or
// Port and port) is an error. Members a format collapses while decoding (a
// JSON object naming one member twice keeps the last) never reach this check.
func expand(raw any, path string, inList bool) (any, error) {
	switch raw := raw.(type) {
	case map[any]any:
		m := make(map[string]any, len(raw))
		for k, val := range raw {
			m[fmt.Sprint(k)] = val
		}
		return expand(m, path, inList)
	case map[string]any:
		out := map[string]any{}
		for k, val := range raw {
			parts := []string{strings.ToLower(k)}
			if !inList {
				parts = strings.Split(parts[0], ".")
			}
			// Wrap val in maps for all but the first part of the key.
			for i := len(parts) - 1; i > 0; i-- {
				val = map[string]any{parts[i]: val}
			}
			val, err := expand(val, path+parts[0]+".", inList)
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
			if out[i], err = expand(val, path+strconv.Itoa(i)+".", true); err != nil {
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
// field takes no keys below it. Interface fields take anything. A map field
// takes only a mapping, {} included, or no key at all: any other value, a
// list or an explicit null included, is an error naming the key, since the
// decoder would merge a list of maps into it and drop unknown keys. Other
// shape mismatches are left to the decoder.
func checkTree(tree any, t reflect.Type, path string) ([]string, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	m, isMap := tree.(map[string]any)
	var unknown []string
	switch t.Kind() {
	case reflect.Interface: // takes anything
	case reflect.Struct:
		if !isMap {
			return nil, nil
		}
		byKey := map[string]field{}
		for _, f := range structFields(t) {
			byKey[f.key] = f
		}
		for k, val := range m {
			f, ok := byKey[k]
			if !ok {
				unknown = append(unknown, path+k)
				continue
			}
			u, err := checkTree(val, f.typ, path+k+".")
			if err != nil {
				return nil, err
			}
			unknown = append(unknown, u...)
		}
	case reflect.Map:
		if !isMap {
			got := fmt.Sprintf("%T", tree)
			if tree == nil {
				got = "null"
			}
			return nil, fmt.Errorf("key %s: expected a mapping, got %s", strings.TrimSuffix(path, "."), got)
		}
		for k, val := range m {
			u, err := checkTree(val, t.Elem(), path+k+".")
			if err != nil {
				return nil, err
			}
			unknown = append(unknown, u...)
		}
	case reflect.Slice, reflect.Array:
		if isMap { // decoded as a one-element list
			return checkTree(m, t.Elem(), path)
		}
		list, _ := tree.([]any)
		for i, val := range list {
			u, err := checkTree(val, t.Elem(), path+strconv.Itoa(i)+".")
			if err != nil {
				return nil, err
			}
			unknown = append(unknown, u...)
		}
	default: // a scalar: any key below it is unknown
		for k := range m {
			unknown = append(unknown, leaves(m[k], path+k)...)
		}
	}
	return unknown, nil
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
